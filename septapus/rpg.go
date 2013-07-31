package septapus

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"html/template"
	"image"
	"image/color"
	"math"
	"math/rand"
	"mime/multipart"
	"net/http"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/fluffle/goirc/client"
	"github.com/fluffle/golog/logging"
	"github.com/iopred/septapus/hsv"
)

var rpgkey = flag.String("rpgkey", "", "Private key for uploading rpg information")
var rpgurl = flag.String("rpgurl", "http://septapus.com/rpg/rpg.php", "Url to upload the generated rpg information")
var rpgallowrepeats = flag.Bool("rpgallowrepeats", false, "Can one person chat repeatedly to fight monsters.")

const (
	SLOT_WEAPON = iota
	SLOT_HEAD
	SLOT_BODY
	NUM_SLOTS
)

const (
	ITEM_JUNK int64 = iota
	ITEM_NORMAL
	ITEM_MAGIC
	ITEM_RARE
	ITEM_UNIQUE
)

type Item struct {
	Name   string
	Level  int64
	Rarity int64
}

type Character struct {
	Name         string
	XP           int64
	Level        int64
	Items        Items
	OldItems     Items
	Listening    bool
	Achievements AchievementsEarned
	stats        Stats
}

type Stat int64

type Stats map[Stat]int64

const (
	STAT_LEVEL Stat = iota
	STAT_DEFEATED
	STAT_DEFEATED_LESS_THAN_10
	STAT_SLAYED
	STAT_RAID_SIZE
	STAT_ITEM_RARITY
	STAT_SMALL_DEFEATED
	STAT_LARGE_DEFEATED
	STAT_UNIQUE_DEFEATED
	STAT_RARE_DEFEATED
	STAT_DKP
)

type Goal struct {
	Stat   Stat
	Target int64
}
type Goals []*Goal

func NewGoal(stat Stat, target int64) *Goal {
	return &Goal{stat, target}
}

func (goal *Goal) isSatisfied(stats Stats) bool {
	return stats[goal.Stat] >= goal.Target
}

type AchievementID string
type AchievementGroup string

type Achievement struct {
	ID          AchievementID
	Group       AchievementGroup
	Name        string
	Description string
	Goals       Goals
}

func NewAchievement(id AchievementID, group AchievementGroup, name, description string, goals ...*Goal) *Achievement {
	achievement := &Achievement{
		ID:          id,
		Group:       group,
		Name:        name,
		Description: description,
	}
	for _, goal := range goals {
		achievement.addGoal(goal)
	}
	return achievement
}

func (achievement *Achievement) isSatisfied(stats Stats) bool {
	for _, goal := range achievement.Goals {
		if !goal.isSatisfied(stats) {
			return false
		}
	}
	return true
}

func (achievement *Achievement) addGoal(goal *Goal) {
	achievement.Goals = append(achievement.Goals, goal)
}

type Achievements []*Achievement
type AchievementsEarned map[AchievementID]time.Time

func (achievements *Achievements) check(stats Stats, achievementsEarned AchievementsEarned) {
	for _, achievement := range *achievements {
		if achievementsEarned[achievement.ID].IsZero() && achievement.isSatisfied(stats) {
			achievementsEarned[achievement.ID] = time.Now()
		}
	}
}

func (achievements *Achievements) add(achievement *Achievement) {
	*achievements = append(*achievements, achievement)
}

type Characters []*Character
type Items []*Item

func (c Characters) Len() int      { return len(c) }
func (c Characters) Swap(i, j int) { c[i], c[j] = c[j], c[i] }
func (c Characters) Less(i, j int) bool {
	if c[i].Level == c[j].Level {
		return c[i].XP > c[j].XP
	}
	return c[i].Level > c[j].Level
}

type Monster struct {
	Name       string
	MaxHealth  int64
	Health     int64
	Difficulty float64
	Characters map[string]int64
	Prefix     string
	Slayed     string
	Born       time.Time
	Died       time.Time
}

type Monsters []*Monster

func (m Monsters) Len() int           { return len(m) }
func (m Monsters) Swap(i, j int)      { m[i], m[j] = m[j], m[i] }
func (m Monsters) Less(i, j int) bool { return m[i].MaxHealth > m[j].MaxHealth }

type Game struct {
	sync.RWMutex
	Server     ServerName
	Room       RoomName
	Characters map[string]*Character
	Monster    *Monster
	Defeated   Monsters
	Last       string
}

type RPGPlugin struct {
	settings *PluginSettings
}

var (
	monsterNames  []string
	monsterSmall  []string
	monsterLarge  []string
	monsterUnique []string
	monsterRare   []string

	healthColors []color.Color
	healthRatios []float64
	levelColors  []color.Color
	levelRatios  []float64
	raidColors   []color.Color
	raidRatios   []float64
	itemColors   []color.Color

	itemNames [][]string
	prefixes  []string
	suffixes  []string
	uniques   []string

	bannedItemNames [][]string
	bannedPrefixes  []string

	achievements Achievements
)

func init() {
	monsterSmall = []string{"Tiny", "Small", "Weak", "Infected", "Sick", "Fragile", "Impaired", "Blind"}
	monsterLarge = []string{"Large", "Giant", "Huge", "Epic", "King", "Champion", "Queen", "Master", "Lord"}
	monsterUnique = []string{"blood", "death", "rot", "sneeze", "pus", "spit", "puke", "burn", "shot", "rend", "slice", "maim", "boil", "singe", "taunt", "scab", "scratch"}
	monsterNames = []string{"Skeleton", "Zombie", "Slime", "Kobold", "Ant", "Cockatrice", "Pyrolisk", "Werewolf", "Wolf", "Warg", "Hell-hound", "Gas Spore", "Gremlin", "Gargoyle", "Mind Flayer", "Imp", "Mimic", "Nymph", "Goblin", "Orc", "Mastodon", "Kraken", "Spider", "Scorpion", "Unicorn", "Narwhal", "Narhorse", "Worm", "Angel", "Archon", "Bat", "Centaur", "Dragon", "Elemental", "Minotaur", "Lich", "Mummy", "Naga", "Ogre", "Snake", "Troll", "Ghoul", "Golem", "Doppelganger", "Ghost", "Shade", "Demon", "Pit Fiend", "Balrog"}
	monsterRare = []string{"Yanthra", "Baelzebub"}

	healthColors = []color.Color{color.RGBA{0, 0, 0, 1}, color.RGBA{153, 0, 0, 1}, color.RGBA{204, 0, 0, 1}, color.RGBA{255, 153, 0, 1}, color.RGBA{255, 204, 0, 1}, color.RGBA{0, 204, 0, 1}}
	healthRatios = []float64{0, 0.5, 0.625, 0.75, 0.875, 1}
	levelColors = []color.Color{color.RGBA{255, 204, 0, 1}, color.RGBA{255, 51, 0, 1}}
	levelRatios = []float64{0, 1}
	raidColors = []color.Color{color.RGBA{204, 204, 204, 1}, color.RGBA{153, 153, 153, 1}}
	raidRatios = []float64{0, 1}

	itemColors = []color.Color{color.RGBA{0, 0, 0, 1}, color.RGBA{0, 204, 0, 1}, color.RGBA{0, 0, 204, 1}, color.RGBA{132, 37, 201, 1}, color.RGBA{255, 153, 0, 1}}

	prefixes = []string{"Iron", "Wooden", "Bronze", "Tin", "Golden", "Silver", "Platinum", "Titanium", "Irradiated", "Liquid", "Steel", "Chilling", "Icy", "Fiery", "Frozen", "Poisoned", "Toxic", "Concrete", "Slippery", "Metal", "Pointy", "Huge", "Massive", "Chrome", "Glass", "Transparent", "Black", "Universal", "Sticky", "Heavy", "Epic", "Eternal", "Ethereal", "Stainless", "Radiant", "Gleaming", "Smoldering", "Charged", "Static", "Roaring", "Talking", "Singing", "Imaginary", "Quintissential", "Glowing", "Raging", "Acrobat's", "Amber", "Angel's", "Archangel's", "Arching", "Arcadian", "Artisan's", "Astral", "Azure", "Beserker", "Beryl", "Blazing", "Blessed", "Blighting", "Boreal", "Brutal", "Burgundy", "Buzzing", "Celestial", "Chromatic", "Cobalt", "Condensing", "Consecrated", "Coral", "Corrosive", "Crimson", "Cruel", "Cunning", "Deadly", "Dense", "Devious", "Divine", "Echoing", "Elysian", "Emerald", "Faithful", "Fanatic", "Feral", "Ferocious", "Fine", "Flaming", "Foul", "Freezing", "Furious", "Garnet", "Glacial", "Glimmering", "Glorious", "Great Wyrm's", "Grinding", "Guardian's", "Dark", "Hallowed", "Hexing", "Hibernal", "Holy", "Howling", "Jade", "Jagged", "King's", "Knight's", "Lapis", "Lord's", "Lunar", "Master's", "Mercilless", "Meteoric", "Mnemonic", "Noxious", "Ocher", "Pestilent", "Prismatic", "Psychic", "Pure", "Resonant", "Ruby", "Rugged", "Russet", "Sacred", "Sapphire", "Savage", "Septic", "Serpent's", "Shadow", "Sharp", "Shimmering", "Shocking", "Soldier's", "Strong", "Sturdy", "Tireless", "Triumphant", "Unearthly", "Valkyrie's", "Venomous", "Veteran's", "Vicious", "Victorious", "Vigorous", "Viridian", "Volcanic", "Wailing", "Warrior's", "Wyrm's", "Quality", "Poetic"}
	suffixes = []string{"Maiming", "Destruction", "Brutality", "Crushing", "Fire", "Lava", "Ice", "Poison", "Pestilence", "Death", "Deliverance", "Chastity", "Rock", "Metal", "Death", "Damnation", "Strength", "Skill", "Dismemberment", "Spines", "the Whale", "the Bear", "Thunder", "Lightning", "the Owl", "the Shark", "the Moon", "the Sun", "the Cosmos", "the Elephant", "the Tiger", "the Snake", "Suffering", "Rainbows", "Reversal", "Eternity", "Rending", "the Idol", "the Narhorse", "the Narwhal", "the Dolphin", "the Ages", "Alacrity", "the Atlas", "Balance", "Bashing", "the Bat", "Blight", "Blocking", "Brilliance", "Burning", "Butchery", "Carnage", "the Centaur", "Chance", "the Kraken", "the Colossus", "Craftmanship", "Defiance", "Ease", "Energy", "Enlightenment", "Equilibrium", "Evisceration", "Excellence", "Flame", "Fortune", "the Fox", "Frost", "the Gargantuan", "the Giant", "the Glacier", "Gore", "Greed", "Guarding", "Incineration", "the Jackal", "the Lamprey", "the Leech", "Life", "the Locust", "Luck", "the Magus", "the Mammoth", "Might", "the Mind", "the Ox", "Pacing", "Perfection", "Radiance", "Protection", "Regeneration", "the Sentinel", "Speed", "Slaying", "Spikes", "the Squid", "Stability", "Storms", "Thawing", "Thorns", "the Titan", "Transcendence", "the Vampire", "the Wolf", "Venom", "Warding", "Vileness", "Winter", "the Wraith", "Benevolence", "Malevolence", "Justice"}
	uniques = []string{"Eagles Mane", "Dragontaint", "Abortious", "Jessicer", "Torsionrod", "Brainpan", "Hell's Wrath", "Furious Expulsion", "Clutterspork", "Bekludgeon", "Bloodwood", "Frostmourne", "Doombringer", "Hyperion", "The Redeemer", "Blood Fell", "Reaper's Toll", "Stormwrath", "Widowmaker", "Fleshtaster", "Ghostwail", "Bloodcrust", "Plaguesnot", "Mindender", "Fungal Growth", "Earth's Edge", "Zealbringer", "Soul's Blessing", "Ripjaw", "The Patriarch", "Silencer", "Battletorrent", "Angel's Song", "Rustwarden"}
	itemNames = [][]string{
		//ITEM_WEAPON
		[]string{"Sword", "Axe", "Broadsword", "Two Handed Sword", "Pike", "Knife", "Dagger", "Polearm", "Mace", "Mallet", "Whip", "Longsword", "Battle Axe", "Two Handed Axe", "Blade", "Glaive", "Club", "Morning Star", "Flail", "War Hammer", "Maul", "Great Maul", "Scythe", "Poleaxe", "Halberd", "Scepter", "Staff", "Spear", "Trident", "Short Sword", "Scimitar", "Sabre", "Claymore", "Bastard Sword", "Cestus"},
		//ITEM_HEAD
		[]string{"Cap", "Skull Cap", "Helm", "Full Helm", "Great Helm", "Mask", "Crown", "Bone Helm", "Circlet", "Coronet", "Diadem", "Casque", "Armet"},
		//ITEM_BODY
		[]string{"Quilted Armor", "Leather Armor", "Hard Leather Armor", "Studded Leather Armor", "Ring Mail", "Scale Mail", "Chain Mail", "Splint Mail", "Light Plate", "Field Plate", "Plate Mail", "Full Plate Mail", "Mesh Armor", "Linked Mail"},
	}

	bannedPrefixes = []string{"Plastic", "Paper", "Cracked", "Blunt", "Broken", "Fragile", "Icey"}
	bannedItemNames = [][]string{
		[]string{"Scabbard"},
		[]string{},
		[]string{},
	}

	levelGroup := AchievementGroup("level")
	achievements.add(NewAchievement(AchievementID("level1"), levelGroup, "Fresh meat", "Reach level 1", NewGoal(STAT_LEVEL, 1)))
	achievements.add(NewAchievement(AchievementID("level5"), levelGroup, "Rookie", "Reach level 5", NewGoal(STAT_LEVEL, 5)))
	achievements.add(NewAchievement(AchievementID("level10"), levelGroup, "Veteran", "Reach level 10", NewGoal(STAT_LEVEL, 10)))
	achievements.add(NewAchievement(AchievementID("level50"), levelGroup, "Hero", "Reach level 50", NewGoal(STAT_LEVEL, 50)))
	achievements.add(NewAchievement(AchievementID("level100"), levelGroup, "God", "Reach level 100", NewGoal(STAT_LEVEL, 100)))
	defeatedGroup := AchievementGroup("defeated")
	achievements.add(NewAchievement(AchievementID("defeated1"), defeatedGroup, "Known in battle", "Defeat 1 monster", NewGoal(STAT_DEFEATED, 1)))
	achievements.add(NewAchievement(AchievementID("defeated10"), defeatedGroup, "Dignified in battle", "Defeat 10 monsters", NewGoal(STAT_DEFEATED, 10)))
	achievements.add(NewAchievement(AchievementID("defeated50"), defeatedGroup, "Honored in battle", "Defeat 50 monsters", NewGoal(STAT_DEFEATED, 50)))
	achievements.add(NewAchievement(AchievementID("defeated100"), defeatedGroup, "Revered in battle", "Defeat 100 monsters", NewGoal(STAT_DEFEATED, 100)))
	achievements.add(NewAchievement(AchievementID("defeated500"), defeatedGroup, "Exalted in battle", "Defeat 500 monsters", NewGoal(STAT_DEFEATED, 500)))
	achievements.add(NewAchievement(AchievementID("defeated1000"), defeatedGroup, "Infamous in battle", "Defeat 1000 monsters", NewGoal(STAT_DEFEATED, 1000)))
	achievements.add(NewAchievement(AchievementID("defeated10000"), defeatedGroup, "Beyond battle", "Defeat 10,000 monsters", NewGoal(STAT_DEFEATED, 10000)))
	achievements.add(NewAchievement(AchievementID("defeated100000"), defeatedGroup, "Ascended in battle", "Defeat 100,000 monsters", NewGoal(STAT_DEFEATED, 100000)))
	defeatedLessThan10Group := AchievementGroup("defeatedlessthan10")
	achievements.add(NewAchievement(AchievementID("defeatedlessthan101"), defeatedLessThan10Group, "Fast", "Defeat a monster in less than 10 minutes", NewGoal(STAT_DEFEATED_LESS_THAN_10, 1)))
	achievements.add(NewAchievement(AchievementID("defeatedlessthan10100"), defeatedLessThan10Group, "Quick", "Defeat a huge monster in less than 10 minutes", NewGoal(STAT_DEFEATED_LESS_THAN_10, 100)))
	achievements.add(NewAchievement(AchievementID("defeatedlessthan101000"), defeatedLessThan10Group, "Instant", "Defeat a gigantic monster in less than 10 minutes", NewGoal(STAT_DEFEATED_LESS_THAN_10, 1000)))
	slayedGroup := AchievementGroup("slayed")
	achievements.add(NewAchievement(AchievementID("slayed1"), slayedGroup, "Slaughter", "Slayed 1 monster", NewGoal(STAT_SLAYED, 1)))
	achievements.add(NewAchievement(AchievementID("slayed5"), slayedGroup, "Massacre", "Slayed 10 monsters", NewGoal(STAT_SLAYED, 10)))
	achievements.add(NewAchievement(AchievementID("slayed50"), slayedGroup, "Bloodbath", "Slayed 50 monsters", NewGoal(STAT_SLAYED, 50)))
	achievements.add(NewAchievement(AchievementID("slayed100"), slayedGroup, "Decimation", "Slayed 100 monsters", NewGoal(STAT_SLAYED, 100)))
	achievements.add(NewAchievement(AchievementID("slayed1000"), slayedGroup, "Eradication", "Slayed 1000 monsters", NewGoal(STAT_SLAYED, 1000)))
	raidSizeGroup := AchievementGroup("raidsize")
	achievements.add(NewAchievement(AchievementID("raid5"), raidSizeGroup, "Crowd", "Defeat a monster in a raid of at least 5 people", NewGoal(STAT_RAID_SIZE, 5)))
	achievements.add(NewAchievement(AchievementID("raid10"), raidSizeGroup, "Gang bang", "Defeat a monster in a raid of at least 10 people", NewGoal(STAT_RAID_SIZE, 10)))
	achievements.add(NewAchievement(AchievementID("raid20"), raidSizeGroup, "Zerg rush", "Defeat a monster in a raid of at least 20 people", NewGoal(STAT_RAID_SIZE, 20)))
	itemRarityGroup := AchievementGroup("itemrarity")
	achievements.add(NewAchievement(AchievementID("itemrarity0"), itemRarityGroup, "Junk", "Find an item", NewGoal(STAT_ITEM_RARITY, 1)))
	achievements.add(NewAchievement(AchievementID("itemrarity1"), itemRarityGroup, "Special", "Find a special item", NewGoal(STAT_ITEM_RARITY, 2)))
	achievements.add(NewAchievement(AchievementID("itemrarity2"), itemRarityGroup, "Magic", "Find a magic item", NewGoal(STAT_ITEM_RARITY, 3)))
	achievements.add(NewAchievement(AchievementID("itemrarity3"), itemRarityGroup, "Rare", "Find a rare item", NewGoal(STAT_ITEM_RARITY, 4)))
	achievements.add(NewAchievement(AchievementID("itemrarity4"), itemRarityGroup, "Legendary", "Find a legendary item", NewGoal(STAT_ITEM_RARITY, 5)))
	sizeRarityGroup := AchievementGroup("size")
	achievements.add(NewAchievement(AchievementID("sizesmall"), sizeRarityGroup, "Candy from a baby", "Defeat a small monster", NewGoal(STAT_SMALL_DEFEATED, 1)))
	achievements.add(NewAchievement(AchievementID("sizelarge"), sizeRarityGroup, "The bigger they are", "Defeat a large monster", NewGoal(STAT_LARGE_DEFEATED, 1)))
	achievements.add(NewAchievement(AchievementID("sizeunique"), sizeRarityGroup, "Pulling teeth", "Defeat a unique monster", NewGoal(STAT_UNIQUE_DEFEATED, 1)))
	achievements.add(NewAchievement(AchievementID("sizerare"), sizeRarityGroup, "Impossible!", "Defeat a rare monster", NewGoal(STAT_RARE_DEFEATED, 1)))
	dkpRarityGroup := AchievementGroup("dkp")
	achievements.add(NewAchievement(AchievementID("dkp1"), dkpRarityGroup, "Dragonslayer", "Slay a dragon", NewGoal(STAT_DKP, 1)))
	achievements.add(NewAchievement(AchievementID("dkp100"), dkpRarityGroup, "Lord of the dragon", "Slay 100 dragons", NewGoal(STAT_DKP, 100)))
}

func NewRPGPlugin(settings *PluginSettings) *RPGPlugin {
	if settings == nil {
		settings = DefaultSettings
	}
	return &RPGPlugin{settings: settings}
}

func (rpg *RPGPlugin) Init(bot *Bot) {
	joinchan := FilterSelf(rpg.settings.GetEventHandler(bot, client.JOIN))

	for {
		select {
		case event, ok := <-joinchan:
			if !ok {
				return
			}
			go rpg.game(bot, event.Server, RoomName(event.Line.Target()))
		}
	}
}

func (rpg *RPGPlugin) game(bot *Bot, server *Server, room RoomName) {
	logging.Info("Creating rpg in", server.Name, room)
	defer logging.Info("Stopped rpg in", server.Name, room)

	game := &Game{}

	game.Load(server.Name, room)

	// If we have heard this event, we can assume that we should be listenening to this room, don't filter through settings.
	disconnectchan := bot.GetEventHandler(client.DISCONNECTED)
	partchan := FilterSelfRoom(bot.GetEventHandler(client.PART), server.Name, room)
	messagechan := FilterRoom(bot.GetEventHandler(client.PRIVMSG), server.Name, room)
	listenchan := FilterSimpleCommand(FilterServer(bot.GetEventHandler(client.PRIVMSG), server.Name), "!rpglisten")
	statschan := FilterSimpleCommand(FilterServer(bot.GetEventHandler(client.PRIVMSG), server.Name), "!rpgstats")
	fightchan := FilterSimpleCommand(FilterRoom(bot.GetEventHandler(client.PRIVMSG), server.Name, room), "!rpgfight")

	hasQuit := false
	quit := func() {
		if !hasQuit {
			bot.RemoveEventHandler(disconnectchan)
			bot.RemoveEventHandler(partchan)
			bot.RemoveEventHandler(messagechan)
			hasQuit = true
		}

	}

	save := func() {
		game.Save()
		game.Upload()
	}
	save()

	saveticker := time.NewTicker(5 * time.Minute).C
	savequit := make(chan bool)
	// Save in a goroutine so it does not block the RPG, but only do one save at a time
	go func() {
		for {
			select {
			case <-saveticker:
				save()
			case <-savequit:
				return
			}
		}
	}()

	for {
		select {
		// On a disconnect or a part, we need to close our handlers, otherwise a second join would trigger another copy of this function.
		case _, ok := <-disconnectchan:
			if !ok {
				return
			}
			quit()
		case _, ok := <-partchan:
			if !ok {
				return
			}
			quit()
		case event, ok := <-messagechan:
			if !ok {
				return
			}
			game.Attack(event)
		case <-time.After(1 * time.Minute):
			game.Heal()
		case event, ok := <-listenchan:
			if !ok {
				return
			}
			game.ListenCommand(event)
		case event, ok := <-statschan:
			if !ok {
				return
			}
			game.StatsCommand(event)
		case event, ok := <-fightchan:
			if !ok {
				return
			}
			game.FightCommand(event)
		}
	}

	savequit <- true
}

func (game *Game) ListenCommand(event *Event) {
	game.Lock()
	defer game.Unlock()

	char := game.GetCharacter(event.Line.Nick, false)
	if char == nil {
		return
	}
	fields := strings.Fields(event.Line.Text())
	if event.Line.Target() == event.Line.Nick {
		// Private message to us, must include a room
		if (len(fields) == 2 || len(fields) == 3) && fields[1] == string(game.Room) {
			if len(fields) == 3 {
				char.Listening = fields[2] == "true"
			}
		} else {
			// Don't send status update if message is targeting from the wrong room
			return
		}
	} else {
		if event.Room == game.Room {
			if len(fields) == 2 {
				char.Listening = fields[1] == "true"
			}
		} else {
			// Don't send status update if message is coming from the wrong room
			return
		}
	}
	if char.Listening {
		event.Server.Conn.Privmsg(event.Line.Nick, "Listening in "+string(game.Room))
	} else {
		event.Server.Conn.Privmsg(event.Line.Nick, "Not listening in "+string(game.Room))
	}
}

func (game *Game) FightCommand(event *Event) {
	fields := strings.Fields(event.Line.Text())
	if len(fields) != 2 {
		return
	}
	msg := game.Fight(event.Line.Nick, fields[1])
	if msg != "" {
		event.Server.Conn.Privmsg(string(game.Room), msg)
	}
}

func (game *Game) StatsCommand(event *Event) {
	game.Lock()
	defer game.Unlock()

	fields := strings.Fields(event.Line.Text())
	target := event.Line.Target()
	if target == event.Line.Nick {
		// Private message to us, must include a room
		if !(len(fields) == 2 && fields[1] == string(game.Room)) {
			// Don't send status update if message is targeting from the wrong room
			return
		}
	} else {
		if event.Room != game.Room {
			// Don't send status update if message is coming from the wrong room
			return
		}
	}
	event.Server.Conn.Privmsg(target, game.Stats())
}

func (game *Game) Stats() string {
	raid := ""
	for key, _ := range game.Monster.Characters {
		if len(raid) > 0 {
			raid += ", "
		}
		raid += game.GetCharacter(key, true).Name
	}
	return fmt.Sprintf("%v [%v]", game.Monster.Stats(), raid)
}

func (monster *Monster) Stats() string {
	return fmt.Sprintf("%v (%d/%d)", monster.Name, monster.Health, monster.MaxHealth)
}

func (game *Game) Load(server ServerName, room RoomName) {
	game.Lock()
	defer game.Unlock()

	filename := "rpg/" + string(server) + string(room) + ".json"

	if file, err := os.Open(filename); err == nil {
		defer file.Close()
		dec := json.NewDecoder(file)
		if err := dec.Decode(game); err != nil {
			logging.Info("Error loading game", server, room, err)
		} else {
			logging.Info("Loaded game for", server, room)
		}
	} else {
		logging.Info("Error loading file", server, room, filename, err)
	}

	game.Init(server, room)
}

func (game *Game) Save() {
	game.Lock()
	defer game.Unlock()

	filename := "rpg/" + string(game.Server) + string(game.Room) + ".json"

	if file, err := os.Create(filename); err == nil {
		defer file.Close()
		enc := json.NewEncoder(file)
		if err := enc.Encode(game); err != nil {
			logging.Info("Error saving game", game.Server, game.Room, err)
		} else {
			logging.Info("Saved game", game.Server, game.Room)
		}
	} else {
		logging.Info("Error creating file", game.Server, game.Room, filename, err)
	}
}

var gameTemplate = template.Must(template.New("root").Parse(gameTemplateSource))

const gameTemplateSource = `<!DOCTYPE HTML PUBLIC "-//W3C//DTD HTML 4.01//EN" "http://www.w3.org/TR/html4/strict.dtd">
<html>
	<head>
		<title>Septapus RPG: {{.Server}}/{{.Room}}</title>
		<meta http-equiv="Content-Type" content="text/html; charset=UTF-8">
		<link rel="stylesheet" href="../css/septapus.css" type="text/css" media="screen">
		<link rel="shortcut icon" href="../images/favicon.png">
	</head>
	<script src="//ajax.googleapis.com/ajax/libs/jquery/2.0.0/jquery.min.js"></script>
	<style type="text/css">
	{{.GenerateStyles}}
	.moreinfo {
		display: none;
	}
	</style>
	<body>
		<div class="title"><img src="../images/Septapus.png" alt="Septapus"></div>
		<p>
		<h2>Current Fight:</h2>
		<table class="currentfight">
		<tr><th>Name</th><th>Health</th><th>Raid</th></tr>
		{{with .Monster}}
		<tr><td class="name">{{.Name}}</td><td class="health bar{{.HealthBarPercentage}}">{{.Health}}/{{.MaxHealth}}</td><td class="raid">{{.CharacterList $}}</td>
		{{end}}
		</table>
		{{if .Characters}}
		<p>
		<h2>Characters:</h2>
		<table class="characters">
			<tr><th>Name</th><th>Level</th><th>XP</th><th>Items</th></tr>
			{{range $index, $element := .GetSortedCharacters}}
			<tr id="button{{$index}}" class="moreinfobutton"><td class="name">{{$element.NameStyle false}}</td><td class="level level{{$element.LevelPercentage $}}">{{$element.Level}}</td><td class="xp bar{{$element.XPPercentage}}">{{$element.XP}}/{{$element.MaxXP}}</td><td class="items">{{$element.ItemsList}}</td></tr>
			<tr id="div{{$index}}" class="moreinfo"><td colspan="4"><h2>{{$element.NameStyle true}}</h2><h3>Achievements</h3>{{$element.AchievementsList}}{{if $element.OldItems}}<p><h3>Item History</h3>{{$element.OldItemsList}}{{end}}<p></td></tr>
			{{end}}
		</table>
		{{end}}
		{{if .Defeated}}
		<p>
		<h2>Previous Fights:</h2>
		<table class="previousfights">
			<tr><th>Name</th><th>Health</th><th>Slayed By</th><th>Raid</th></tr>
			{{range .DefeatedReverse}}
			<tr><td class="name">{{.Name}}</td><td class="health health{{.HealthPercentage}}">{{.Health}}/{{.MaxHealth}}</td><td class="slayed">{{.SlayedList $}}</td><td class="raid">{{.CharacterList $}}</td></tr>
			{{end}}
		</table>
		{{end}}
		<script type="text/javascript">
			$(".moreinfobutton").each(function(index) {
				var id = $(this).attr('id');
				id = "div" + id.substring(6)
			  $(this).click(function() {
			  	$(".moreinfo").each(function(index) {
						if ($(this).attr("id") != id) {
							$(this).hide();
						}
					});
			  	$("#" + id).toggle();
			  });
			});
		</script>
		<p>
                                <table><tr><td>
                                                        <a href="http://validator.w3.org/check?uri=referer"><img src="http://www.w3.org/html/logo/badge/html5-badge-h-css3-semantics.png" width="165" height="64" alt="HTML5 Powered with CSS3 / Styling, and Semantics" title="HTML5 Powered with CSS3 / Styling, and Semantics"></a>
                                                        </td><td>
                                                        <form action="https://www.paypal.com/cgi-bin/webscr" method="post" target="_top">
                                                        <input type="hidden" name="cmd" value="_donations">
                                                        <input type="hidden" name="business" value="iopred+uspred@gmail.com">
                                                        <input type="hidden" name="lc" value="US">
                                                        <input type="hidden" name="item_name" value="Septapus">
                                                        <input type="hidden" name="no_note" value="0">
                                                        <input type="hidden" name="currency_code" value="USD">
                                                        <input type="hidden" name="bn" value="PP-DonationsBF:btn_donateCC_LG.gif:NonHostedGuest">
                                                        <input type="image" src="https://www.paypalobjects.com/en_US/i/btn/btn_donateCC_LG.gif" name="submit" alt="PayPal - The safer, easier way to pay online!">
                                                        <img alt="" src="https://www.paypalobjects.com/en_US/i/scr/pixel.gif" width="1" height="1">
                                                        </form>
                                                        </td></tr></table>
	</body>
</html>
`

func colorString(color color.Color) string {
	r, g, b, _ := color.RGBA()
	return fmt.Sprintf("rgb(%d, %d, %d)", r>>8, g>>8, b>>8)
}

func lerp(ratio float64, colors []color.Color, ratios []float64) color.Color {
	if len(ratios) < 2 || ratio < ratios[0] || ratio > ratios[len(ratios)-1] {
		return nil
	}
	for i := 0; i < len(ratios)-1; i++ {
		if ratio >= ratios[i] && ratio <= ratios[i+1] {
			r := (ratio - ratios[i]) / (ratios[i+1] - ratios[i])
			a, ok := hsv.HSVModel.Convert(colors[i]).(hsv.HSV)
			if !ok {
				return nil
			}
			b, ok := hsv.HSVModel.Convert(colors[i+1]).(hsv.HSV)
			if !ok {
				return nil
			}
			h := hsv.HSV{a.H + (b.H-a.H)*r, a.S + (b.S-a.S)*r, a.V + (b.V-a.V)*r}
			return h
		}
	}
	return nil
}

func lerpColorString(ratio float64, colors []color.Color, ratios []float64) template.CSS {
	color := lerp(ratio, colors, ratios)
	if color == nil {
		color = image.Black
	}
	return template.CSS(colorString(color))
}

func barColor(ratio float64, colorratio float64, colors []color.Color, ratios []float64) template.CSS {
	color := lerp(colorratio, colors, ratios)
	if color == nil {
		color = image.Black
	}
	p := func(bit string, color string, ratio float64) string {
		return fmt.Sprintf("background-image: %s(left , %s 0%%, %s %.0f%%, rgba(0,0,0,0) %.0f%%, rgba(0, 0, 0, 0) 100%%);", bit, color, color, ratio, ratio)
	}
	cs := colorString(color)
	return template.CSS(p("linear-gradient", cs, ratio*100.0) + p("-o-linear-gradient", cs, ratio*100.0) + p("-moz-linear-gradient", cs, ratio*100.0) + p("-webkit-linear-gradient", cs, ratio*100.0) + p("-ms-linear-gradient", cs, ratio*100.0))
}

func (game *Game) GenerateStyles() template.CSS {
	str := ""
	for i := 0; i <= 200; i++ {
		str += fmt.Sprintf(".health%d { color: %v; }\n", i, lerpColorString(float64(i)/float64(200), healthColors, healthRatios))
	}
	for i := 0; i <= 100; i++ {
		str += fmt.Sprintf(".raid%d { color: %v; }\n", i, lerpColorString(float64(i)/float64(100), raidColors, raidRatios))
		str += fmt.Sprintf(".level%d { color: %v; }\n", i, lerpColorString(float64(i)/float64(100), levelColors, levelRatios))
		barRatio := float64(i) / float64(100)
		str += fmt.Sprintf(".bar%d { %v }\n", i, barColor(barRatio, 0.5+barRatio/2.0, healthColors, healthRatios))
	}
	for i := 0; i < len(itemColors); i++ {
		str += fmt.Sprintf(".item%d { color: %v; }\n", i, colorString(itemColors[i]))
	}
	return template.CSS(str)
}

func (game *Game) Upload() {
	game.Lock()
	defer game.Unlock()

	filename := strings.Replace(string(game.Server)+string(game.Room)+".html", "#", ":", -1)

	b := &bytes.Buffer{}

	w := multipart.NewWriter(b)
	defer w.Close()

	if err := w.WriteField("key", *rpgkey); err != nil {
		logging.Error("Error creating key:", err)
		return
	}

	if err := w.WriteField("filename", filename); err != nil {
		logging.Error("Error creating filename:", err)
		return
	}

	formfile, err := w.CreateFormFile("rpg", filename)
	if err != nil {
		logging.Error("Error creating form file:", err)
		return
	}

	if err := gameTemplate.Execute(formfile, game); err != nil {
		logging.Error("Error executing template:", err)
	}

	w.Close()

	logging.Info("Uploading rpg", filename, *rpgurl, *rpgkey)

	if _, err := http.Post(*rpgurl, w.FormDataContentType(), b); err != nil {
		logging.Error("Error posting comic to server:", err)
		return
	}
}

func (game *Game) Init(server ServerName, room RoomName) {
	if game.Server == "" {
		game.Server = server
	}
	if game.Room == "" {
		game.Room = room
	}
	if game.Characters == nil {
		game.Characters = make(map[string]*Character)
	} else {
		for _, character := range game.Characters {
			character.Migrate()
		}
	}
	if game.Monster == nil {
		game.Monster = game.NewMonster()
	}
	if game.Defeated == nil {
		game.Defeated = make(Monsters, 0)
	}
	// Construct achievement stat
	for _, character := range game.Characters {
		for _, monster := range game.Defeated {
			monster.assignStats(character)
		}
		achievements.check(character.stats, character.Achievements)
	}
}

func NameKey(name string) string {
	return strings.ToLower(name)
}

func (character *Character) Migrate() {
	for {
		if character.XP >= character.MaxXP() {
			character.XP -= character.MaxXP()
			character.Level++
		} else {
			break
		}
	}
	if character.Items == nil {
		character.Items = make(Items, NUM_SLOTS)
	}
	character.AddItems()
	if character.Achievements == nil {
		character.Achievements = make(AchievementsEarned)
	}
	character.stats = make(Stats)
	character.stats[STAT_LEVEL] = character.Level
	for _, item := range character.OldItems {
		item.Migrate()
		if item.Rarity+1 > character.stats[STAT_ITEM_RARITY] {
			character.stats[STAT_ITEM_RARITY] = item.Rarity + 1
		}
	}
	for _, item := range character.Items {
		if item != nil {
			item.Migrate()
			if item.Rarity+1 > character.stats[STAT_ITEM_RARITY] {
				character.stats[STAT_ITEM_RARITY] = item.Rarity + 1
			}
		}
	}
}

func (item *Item) Migrate() {
	name := item.Name
	for _, str := range bannedPrefixes {
		name = strings.Replace(name, str, prefixes[rand.Intn(len(prefixes))], -1)
	}
	for i, _ := range bannedItemNames {
		for _, str := range bannedItemNames[i] {
			name = strings.Replace(name, str, itemNames[i][rand.Intn(len(itemNames[i]))], -1)
		}
	}
	item.Name = name
}

func (character *Character) ItemLevel() int64 {
	count := int64(0)
	for i := 0; i < NUM_SLOTS; i++ {
		if character.Items[i] != nil {
			count += character.Items[i].Level
		}
	}
	return count
}

func (character *Character) WeaponLevel() int64 {
	if character.Items[SLOT_WEAPON] == nil {
		return 0
	}
	return character.Items[SLOT_WEAPON].Level
}

func (character *Character) ArmorLevel() int64 {
	count := int64(0)
	for i := 0; i < NUM_SLOTS; i++ {
		if character.Items[i] != nil && i != SLOT_WEAPON {
			count += character.Items[i].Level
		}
	}
	return count
}

func (character *Character) AddItems() {
	for i := character.ItemLevel(); i < character.Level; i++ {
		slot := rand.Intn(NUM_SLOTS)
		item := character.Items[slot]
		itemLevel := int64(1)
		if item != nil {
			itemLevel = item.Level + 1
			if item.Rarity >= ITEM_NORMAL {
				character.OldItems = append(character.OldItems, item)
			}
		}
		item = NewItem(slot, itemLevel)
		character.Items[slot] = item
		if item.Rarity+1 > character.stats[STAT_ITEM_RARITY] {
			character.stats[STAT_ITEM_RARITY] = item.Rarity + 1
		}

	}
}

func itemList(items Items) template.HTML {
	str := ""
	for _, item := range items {
		if item != nil {
			str += fmt.Sprintf("<span class=\"item%d\">%v</span> (%d), ", item.Rarity, item.Name, item.Level)
		}
	}
	if str == "" {
		return template.HTML(str)
	}
	return template.HTML(str[:len(str)-2])
}

func (character *Character) ItemsList() template.HTML {
	return itemList(character.Items)
}

func (character *Character) OldItemsList() template.HTML {
	return itemList(character.OldItems)
}

func RandomItemName(slot int, level int64) (string, int64) {
	if slot == SLOT_WEAPON && level >= 10 && rand.Float64() > 0.95 {
		return uniques[rand.Intn(len(uniques))], ITEM_UNIQUE
	}

	names := itemNames[slot]
	name := names[rand.Intn(len(names))]

	chance := int64(4)

	rarity := int64(0)
	if level > 9 {
		rarity++
	}

	prefix := rand.Float64() > 0.5
	for i := 0; i < 2; i++ {
		if rand.Float64() < float64(level-chance)/float64(chance) {
			if prefix {
				name = prefixes[rand.Intn(len(prefixes))] + " " + name
			} else {
				name = name + " of " + suffixes[rand.Intn(len(suffixes))]
			}
			level -= chance
			prefix = !prefix
			rarity++
		}
	}

	return name, rarity
}

func NewItem(slot int, level int64) *Item {
	name, rarity := RandomItemName(slot, level)
	return &Item{name, level, rarity}
}

func XPNeededForLevel(level int64) int64 {
	if level < 0 {
		return 0
	}
	return int64(20 + math.Pow(1.4, float64(level)))
}

func (character *Character) MaxXP() int64 {
	return XPNeededForLevel(character.Level)
}

func (character *Character) GainXP(xp int64) bool {
	character.XP += xp
	levelled := false
	for character.XP >= character.MaxXP() {
		character.XP -= character.MaxXP()
		character.Level++
		character.AddItems()
		levelled = true
	}
	return levelled
}

func (game *Game) GetCharacter(name string, create bool) *Character {
	key := NameKey(name)
	character := game.Characters[key]
	if character == nil && create {
		character = &Character{
			Name:         name,
			Items:        make(Items, NUM_SLOTS),
			stats:        make(Stats),
			Achievements: make(AchievementsEarned),
		}
		game.Characters[key] = character
	}
	return character
}

func (game *Game) GetSortedCharacters() Characters {
	characters := make(Characters, 0)
	for _, value := range game.Characters {
		characters = append(characters, value)
	}
	sort.Sort(characters)
	return characters
}

func (game *Game) NewMonster() *Monster {
	health := int64(len(game.Defeated))
	difficulty := 1.0
	name := monsterNames[rand.Intn(len(monsterNames))]
	prefix := "a"
	r := rand.Float64()
	if r > 0.99 && health > 100 {
		difficulty += 4 + rand.Float64()*5
		name = monsterRare[rand.Intn(len(monsterRare))]
		prefix = ""
	} else if r > 0.94 {
		difficulty += 1 + rand.Float64()
		first := monsterUnique[rand.Intn(len(monsterUnique))]
		second := ""
		for second == "" || second == first {
			second = monsterUnique[rand.Intn(len(monsterUnique))]
		}
		name = strings.ToUpper(string(first[0])) + first[1:] + second
		prefix = ""
	} else if r > 0.74 {
		difficulty += rand.Float64()
		name = monsterLarge[rand.Intn(len(monsterLarge))] + " " + name
	} else if r > 0.54 {
		difficulty -= rand.Float64() / 2.0
		name = monsterSmall[rand.Intn(len(monsterSmall))] + " " + name
	}
	health = int64(float64(health) * difficulty)
	if health < 1 {
		health = 1
	}

	if len(prefix) > 0 && (name[0] == 'a' || name[0] == 'e' || name[0] == 'i' || name[0] == 'o' || name[0] == 'u') {
		prefix = "an"
	}

	monster := &Monster{
		Name:       name,
		MaxHealth:  health,
		Health:     health,
		Difficulty: difficulty,
		Characters: make(map[string]int64),
		Prefix:     prefix,
		Born:       time.Now(),
	}
	return monster
}

func (monster *Monster) AddCharacter(name string) {
	key := NameKey(name)
	monster.Characters[key]++
}

func (monster *Monster) Heal(health int64) {
	monster.Health += health
	if monster.Health > monster.MaxHealth {
		monster.Health = monster.MaxHealth
		monster.Characters = make(map[string]int64)
	}
}

func (game *Game) DefeatedReverse() Monsters {
        num := len(game.Defeated)
        if num > 100 {
          num = 100
        }
	defeated := make(Monsters, num)
	for i := 0; i < num; i++ {
		defeated[i] = game.Defeated[len(game.Defeated)-1-i]
	}
	return defeated
}

func (monster *Monster) HealthPercentage() int {
	health := monster.Health + monster.MaxHealth
	if health < 0 {
		health = 0
	}
	if health > monster.MaxHealth*2 {
		health = monster.MaxHealth * 2
	}
	return int((float64(health) / float64(monster.MaxHealth*2)) * 200.0)
}

func (monster *Monster) HealthBarPercentage() int {
	health := monster.Health
	if health < 0 {
		health = 0
	}
	if health > monster.MaxHealth {
		health = monster.MaxHealth
	}
	return int((float64(health) / float64(monster.MaxHealth)) * 100.0)
}

// Following methods are for the template.
func (monster *Monster) CharacterList(game *Game) template.HTML {
	max := int64(1)
	for _, c := range monster.Characters {
		if c > max {
			max = c
		}
	}
	str := ""
	for name, c := range monster.Characters {
		str += fmt.Sprintf("<span class=\"raid%d\">%s</span>, ", int((float64(c)/float64(max))*100), game.GetCharacter(name, true).Name)
	}
	if str == "" {
		return template.HTML(str)
	}
	return template.HTML(str[:len(str)-2])
}

func (monster *Monster) SlayedList(game *Game) string {
	return game.GetCharacter(monster.Slayed, true).Name
}

func (character *Character) XPPercentage() int {
	return int((float64(character.XP) / float64(XPNeededForLevel(character.Level))) * 100.0)
}

func (character *Character) LevelPercentage(game *Game) int {
	max := int64(0)
	for _, c := range game.Characters {
		if c.Level > max {
			max = c.Level
		}
	}
	return int((float64(character.Level) / float64(max)) * 100.0)
}

func (character *Character) NameStyle(includeTitle bool) template.HTML {
	name := character.Name
	prefix := ""
	title := ""
	if !character.Achievements["dkp100"].IsZero() {
		title = "<span class=\"raid100\">, Lord of the Dragon</span>"
		prefix = "<span class=\"level100\">♛</span>"
	} else if !character.Achievements["dkp1"].IsZero() {
		title = "<span class=\"raid100\"> the Dragonslayer</span>"
		prefix = "<span class=\"level0\">♛</span>"
	}
	if includeTitle {
		return template.HTML(fmt.Sprintf("%v%v%v", prefix, name, title))
	}
	return template.HTML(fmt.Sprintf("%v%v", prefix, name))
}

func (character *Character) AchievementsList() template.HTML {
	str := ""

	var lastGroup AchievementGroup
	shownNext := false
	for _, achievement := range achievements {
		if achievement.Group != lastGroup {
			if len(str) != 0 {
				str += "<p>"
			}
			lastGroup = achievement.Group
			shownNext = false
		}
		time := character.Achievements[achievement.ID]
		if !time.IsZero() {
			str += fmt.Sprintf("<div class=\"achievement earned\"><span class=\"name earned\">%v</span><br><span class=\"date earned\">Earned %v</span><br><span class=\"description earned\">%v</span></div>", achievement.Name, time.Format("02 Jan 2006"), achievement.Description)
		} else if !shownNext {
			str += fmt.Sprintf("<div class=\"achievement unearned\"><span class=\"name unearned\">%v</span><br><span class=\"date unearned\">Not earned</span><br><span class=\"description unearned\">%v</span></div>", achievement.Name, achievement.Description)
			shownNext = true
		}
	}

	return template.HTML(str)
}

func (game *Game) Heal() {
	game.Monster.Heal(1)
}

func (monster *Monster) assignStats(character *Character) {
	key := NameKey(character.Name)
	if _, ok := monster.Characters[key]; !ok {
		return
	}
	stats := character.stats
	if key == monster.Slayed {
		stats[STAT_SLAYED]++
		if strings.Index(monster.Name, "Dragon") != -1 {
			stats[STAT_DKP]++
		}
	}
	stats[STAT_DEFEATED]++
	if !monster.Born.IsZero() && !monster.Died.IsZero() && monster.Died.Before(monster.Born.Add(10*time.Minute)) {
		stats[STAT_DEFEATED_LESS_THAN_10] = monster.MaxHealth
	}
	raidSize := int64(len(monster.Characters))
	if raidSize > stats[STAT_RAID_SIZE] {
		stats[STAT_RAID_SIZE] = raidSize
	}
	difficulty := monster.Difficulty
	switch {
	case difficulty < 1:
		stats[STAT_SMALL_DEFEATED]++
	case difficulty > 1 && difficulty < 2:
		stats[STAT_LARGE_DEFEATED]++
	case difficulty >= 2 && difficulty < 5:
		stats[STAT_UNIQUE_DEFEATED]++
	case difficulty >= 5:
		stats[STAT_RARE_DEFEATED]++
	}
}

func (game *Game) Attack(event *Event) {
	game.Lock()
	name := event.Line.Nick
	key := NameKey(name)

	// Create the character if it doesn't exist
	char := game.GetCharacter(name, true)
	char.Name = name

	if key == NameKey(event.Server.Conn.Me().Nick) || (key == game.Last && !*rpgallowrepeats) {
		game.Unlock()
		return
	}
	game.Last = key
	monster := game.Monster
	monster.AddCharacter(name)
	monster.Health -= int64(len(monster.Characters))
	if monster.Health <= 0 {
		game.Defeated = append(game.Defeated, monster)
		game.Monster = game.NewMonster()

		monster.Died = time.Now()
		xp := int64(float64(len(monster.Characters)) * monster.Difficulty)
		if xp < 1 {
			xp = 1
		}

		slayed := rand.Intn(len(monster.Characters))
		maxLevel := int64(0)
		average := 0.0
		max := int64(0)
		for n, count := range monster.Characters {
			char := game.GetCharacter(n, true)
			if char.Level > maxLevel {
				maxLevel = char.Level
			}
			if slayed == 0 {
				monster.Slayed = n
			}
			slayed--
			average += float64(count)
			if count > max {
				max = count
			}
		}
		average /= float64(len(monster.Characters))
		slayedName := game.GetCharacter(monster.Slayed, true).Name

		prefix := monster.Prefix
		if prefix != "" {
			prefix = prefix + " "
		}
		newprefix := game.Monster.Prefix
		if newprefix != "" {
			newprefix = newprefix + " "
		}
		for n, count := range monster.Characters {
			char := game.GetCharacter(n, true)

			exp := xp
			// You have to beat the average amount of talking to get full XP.
			if count < int64(average) {
				exp = int64(float64(exp) * float64(count) / float64(max))
			}
			extra := maxLevel - char.Level
			if extra > exp {
				extra = exp
			}
			exp += extra

			levelled := char.GainXP(exp)
			monster.assignStats(char)
			achievements.check(char.stats, char.Achievements)
			if char.Listening {
				if n == monster.Slayed {
					event.Server.Conn.Privmsg(n, fmt.Sprintf("You just slayed %v%v in %v and gained %d xp.", prefix, monster.Name, game.Room, exp))
				} else {
					event.Server.Conn.Privmsg(n, fmt.Sprintf("You helped %v slay %v%v in %v and gained %d xp.", slayedName, prefix, monster.Name, game.Room, exp))
				}
				if levelled {
					event.Server.Conn.Privmsg(n, fmt.Sprintf("You just levelled up in %v to level %d!", game.Room, char.Level))
				}
				event.Server.Conn.Privmsg(n, fmt.Sprintf("You see %v%v approaching.", newprefix, game.Monster.Stats()))
			}
		}
		game.Unlock()
		game.Save()
		game.Upload()
	} else {
		game.Unlock()
	}
}

// Returns true when attacker makes a hit.
func (game *Game) fight(attacker, defender *Character) bool {
	return rand.Int63n(attacker.WeaponLevel()+20) > rand.Int63n(defender.ArmorLevel()+20)
}

func (game *Game) Fight(attackerName, defenderName string) string {
	game.Lock()
	defer game.Unlock()

	attacker := game.GetCharacter(attackerName, false)
	defender := game.GetCharacter(defenderName, false)

	attackerHits := 0
	defenderHits := 0

	if attacker == nil || defender == nil || attacker == defender {
		return ""
	}

	for i := 0; i < 5; i++ {
		if game.fight(attacker, defender) {
			attackerHits++
		}
		if game.fight(defender, attacker) {
			defenderHits++
		}
	}

	description := fmt.Sprintf("%v (%v atk, %v def) vs %v (%v atk, %v def). ", attacker.Name, attacker.WeaponLevel(), attacker.ArmorLevel(), defender.Name, defender.WeaponLevel(), defender.ArmorLevel())
	switch {
	case attackerHits == defenderHits:
		if attackerHits == 0 {
			return fmt.Sprintf("%vEveryone fell asleep.", description)
		}
		return fmt.Sprintf("%vTie.", description)
	case attackerHits > defenderHits:
		if defenderHits == 0 {
			return fmt.Sprintf("%v%v Wins. Flawless Victory!", description, attacker.Name)
		}
		return fmt.Sprintf("%v%v Wins. (%v to %v)", description, attacker.Name, attackerHits, defenderHits)
	case defenderHits > attackerHits:
		if attackerHits == 0 {
			return fmt.Sprintf("%v%v Wins. Flawless Victory!", description, defender.Name)
		}
		return fmt.Sprintf("%v%v Wins. (%v to %v)", description, defender.Name, defenderHits, attackerHits)
	}
	return ""
}
