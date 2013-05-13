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
	ITEM_JUNK = iota
	ITEM_NORMAL
	ITEM_MAGIC
	ITEM_RARE
	ITEM_UNIQUE
)

type Item struct {
	Name   string
	Level  int64
	Rarity int
}

type Character struct {
	Name      string
	XP        int64
	Level     int64
	Items     Items
	OldItems  Items
	Listening bool
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
	MonsterNames  []string
	MonsterSmall  []string
	MonsterLarge  []string
	MonsterUnique []string

	HealthColors []color.Color
	HealthRatios []float64
	LevelColors  []color.Color
	LevelRatios  []float64
	RaidColors   []color.Color
	RaidRatios   []float64
	ItemColors   []color.Color

	ItemNames [][]string
	Prefixes  []string
	Suffixes  []string
	Uniques   []string
)

func init() {
	MonsterSmall = []string{"Tiny", "Small", "Weak", "Infected", "Sick", "Fragile", "Impaired", "Blind"}
	MonsterLarge = []string{"Large", "Giant", "Huge", "Epic", "King", "Champion", "Queen", "Master", "Lord"}
	MonsterUnique = []string{"blood", "death", "rot", "sneeze", "pus", "spit", "puke", "burn", "shot", "rend", "slice", "maim"}
	MonsterNames = []string{"Skeleton", "Zombie", "Slime", "Kobold", "Ant", "Cockatrice", "Pyrolisk", "Werewolf", "Wolf", "Warg", "Hell-hound", "Gas Spore", "Gremlin", "Gargoyle", "Mind Flayer", "Imp", "Mimic", "Nymph", "Goblin", "Orc", "Mastodon", "Kraken", "Spider", "Scorpion", "Unicorn", "Narwhal", "Narhorse", "Worm", "Angel", "Archon", "Bat", "Centaur", "Dragon", "Elemental", "Minotaur", "Lich", "Mummy", "Naga", "Ogre", "Snake", "Troll", "Ghoul", "Golem", "Doppelganger", "Ghost", "Shade", "Demon", "Pit Fiend", "Balrog"}

	HealthColors = []color.Color{color.RGBA{0, 0, 0, 1}, color.RGBA{153, 0, 0, 1}, color.RGBA{204, 0, 0, 1}, color.RGBA{255, 153, 0, 1}, color.RGBA{255, 204, 0, 1}, color.RGBA{0, 204, 0, 1}}
	HealthRatios = []float64{0, 0.5, 0.625, 0.75, 0.875, 1}
	LevelColors = []color.Color{color.RGBA{255, 204, 0, 1}, color.RGBA{255, 51, 0, 1}}
	LevelRatios = []float64{0, 1}
	RaidColors = []color.Color{color.RGBA{204, 204, 204, 1}, color.RGBA{153, 153, 153, 1}}
	RaidRatios = []float64{0, 1}

	ItemColors = []color.Color{color.RGBA{0, 0, 0, 1}, color.RGBA{0, 204, 0, 1}, color.RGBA{0, 0, 204, 1}, color.RGBA{132, 37, 201, 1}, color.RGBA{255, 153, 0, 1}}

	Prefixes = []string{"Iron", "Wooden", "Plastic", "Bronze", "Tin", "Golden", "Silver", "Platinum", "Titanium", "Irradiated", "Liquid", "Steel", "Chilling", "Icey", "Fiery", "Frozen", "Poisoned", "Toxic", "Concrete", "Slippery", "Metal", "Pointy", "Blunt", "Broken", "Fragile", "Huge", "Massive", "Chrome", "Glass", "Transparent", "Black", "Paper", "Cracked", "Universal", "Sticky", "Heavy", "Epic", "Eternal", "Ethereal", "Stainless", "Radiant", "Gleaming", "Smoldering", "Charged", "Static", "Roaring", "Talking", "Singing", "Imaginary", "Quintissential", "Glowing", "Raging", "Acrobat's", "Amber", "Angel's", "Archangel's", "Arching", "Arcadian", "Artisan's", "Astral", "Azure", "Beserker", "Beryl", "Blazing", "Blessed", "Blighting", "Boreal", "Brutal", "Burgundy", "Buzzing", "Celestial", "Chromatic", "Cobalt", "Condensing", "Consecrated", "Coral", "Corrosive", "Crimson", "Cruel", "Cunning", "Deadly", "Dense", "Devious", "Divine", "Echoing", "Elysian", "Emerald", "Faithful", "Fanatic", "Feral", "Ferocious", "Fine", "Flaming", "Foul", "Freezing", "Furious", "Garnet", "Glacial", "Glimmering", "Glorious", "Great Wyrm's", "Grinding", "Guardian's", "Dark", "Hallowed", "Hexing", "Hibernal", "Holy", "Howling", "Jade", "Jagged", "King's", "Knight's", "Lapis", "Lord's", "Lunar", "Master's", "Mercilless", "Meteoric", "Mnemonic", "Noxious", "Ocher", "Pestilent", "Prismatic", "Psychic", "Pure", "Resonant", "Ruby", "Rugged", "Russet", "Sacred", "Sapphire", "Savage", "Septic", "Serpent's", "Shadow", "Sharp", "Shimmering", "Shocking", "Soldier's", "Strong", "Sturdy", "Tireless", "Triumphant", "Unearthly", "Valkyrie's", "Venomous", "Veteran's", "Vicious", "Victorious", "Vigorous", "Viridian", "Volcanic", "Wailing", "Warrior's", "Wyrm's", "Quality", "Poetic"}
	Suffixes = []string{"Maiming", "Destruction", "Brutality", "Crushing", "Fire", "Lava", "Ice", "Poison", "Pestilence", "Death", "Deliverance", "Chastity", "Rock", "Metal", "Death", "Damnation", "Strength", "Skill", "Dismemberment", "Spines", "the Whale", "the Bear", "Thunder", "Lightning", "the Owl", "the Shark", "the Moon", "the Sun", "the Cosmos", "the Elephant", "the Tiger", "the Snake", "Suffering", "Rainbows", "Reversal", "Eternity", "Rending", "the Idol", "the Narhorse", "the Narwhal", "the Dolphin", "the Ages", "Alacrity", "the Atlas", "Balance", "Bashing", "the Bat", "Blight", "Blocking", "Brilliance", "Burning", "Butchery", "Carnage", "the Centaur", "Chance", "the Kraken", "the Colossus", "Craftmanship", "Defiance", "Ease", "Energy", "Enlightenment", "Equilibrium", "Evisceration", "Excellence", "Flame", "Fortune", "the Fox", "Frost", "the Gargantuan", "the Giant", "the Glacier", "Gore", "Greed", "Guarding", "Incineration", "the Jackal", "the Lamprey", "the Leech", "Life", "the Locust", "Luck", "the Magus", "the Mammoth", "Might", "the Mind", "the Ox", "Pacing", "Perfection", "Radiance", "Protection", "Regeneration", "the Sentinel", "Speed", "Slaying", "Spikes", "the Squid", "Stability", "Storms", "Thawing", "Thorns", "the Titan", "Transcendence", "the Vampire", "the Wolf", "Venom", "Warding", "Vileness", "Winter", "the Wraith", "Benevolence", "Malevolence", "Justice"}
	Uniques = []string{"Eagles Mane", "Dragontaint", "Abortious", "Jessicer", "Torsionrod", "Brainpan", "Hell's Wrath", "Furious Expulsion", "Clutterspork", "Bekludgeon", "Bloodwood", "Frostmourne", "Doombringer", "Hyperion", "The Redeemer", "Blood Fell", "Reaper's Toll", "Stormwrath", "Widowmaker", "Fleshtaster", "Ghostwail", "Bloodcrust", "Plaguesnot", "Mindender", "Fungal Growth", "Earth's Edge", "Zealbringer", "Soul's Blessing", "Ripjaw", "The Patriarch", "Silencer", "Battletorrent", "Angel's Song", "Rustwarden"}
	ItemNames = [][]string{
		//ITEM_WEAPON
		[]string{"Sword", "Axe", "Broadsword", "Two Handed Sword", "Pike", "Scabbard", "Knife", "Dagger", "Polearm", "Mace", "Mallet", "Whip", "Longsword", "Battle Axe", "Two Handed Axe", "Blade", "Glaive", "Club", "Morning Star", "Flail", "War Hammer", "Maul", "Great Maul", "Scythe", "Poleaxe", "Halberd", "Scepter", "Staff", "Spear", "Trident", "Short Sword", "Scimitar", "Sabre", "Claymore", "Bastard Sword", "Cestus"},
		//ITEM_HEAD
		[]string{"Cap", "Skull Cap", "Helm", "Full Helm", "Great Helm", "Mask", "Crown", "Bone Helm", "Circlet", "Coronet", "Diadem", "Casque", "Armet"},
		//ITEM_BODY
		[]string{"Quilted Armor", "Leather Armor", "Hard Leather Armor", "Studded Leather Armor", "Ring Mail", "Scale Mail", "Chain Mail", "Splint Mail", "Light Plate", "Field Plate", "Plate Mail", "Full Plate Mail", "Mesh Armor", "Linked Mail"},
	}
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

	quit := func() {
		bot.RemoveEventHandler(disconnectchan)
		bot.RemoveEventHandler(partchan)
		bot.RemoveEventHandler(messagechan)
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
			char := game.GetCharacter(event.Line.Nick, false)
			if char == nil {
				break
			}
			fields := strings.Fields(event.Line.Text())
			if event.Line.Target() == event.Line.Nick {
				// Private message to us, must include a room
				if (len(fields) == 2 || len(fields) == 3) && fields[1] == string(room) {
					if len(fields) == 3 {
						char.Listening = fields[2] == "true"
					}
				} else {
					// Don't send status update if message is targeting from the wrong room
					break
				}
			} else {
				if event.Room == room {
					if len(fields) == 2 {
						char.Listening = fields[1] == "true"
					}
				} else {
					// Don't send status update if message is coming from the wrong room
					break
				}
			}
			if char.Listening {
				event.Server.Conn.Privmsg(event.Line.Nick, "Listening in "+string(room))
			} else {
				event.Server.Conn.Privmsg(event.Line.Nick, "Not listening in "+string(room))
			}
		}
	}

	savequit <- true
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
	<style type="text/css">
	{{.GenerateStyles}}
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
			{{range .GetSortedCharacters}}
			<tr><td class="name">{{.Name}}</td><td class="level level{{.LevelPercentage $}}">{{.Level}}</td><td class="xp bar{{.XPPercentage}}">{{.XP}}/{{.MaxXP}}</td><td class="items">{{.ItemList}}</td></tr>
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
		<p>
			<a href="http://validator.w3.org/check?uri=referer"><img src="http://www.w3.org/Icons/valid-html401" alt="Valid HTML 4.01 Strict" height="31" width="88"></a>
		</p>
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
		str += fmt.Sprintf(".health%d { color: %v; }\n", i, lerpColorString(float64(i)/float64(200), HealthColors, HealthRatios))
	}
	for i := 0; i <= 100; i++ {
		str += fmt.Sprintf(".raid%d { color: %v; }\n", i, lerpColorString(float64(i)/float64(100), RaidColors, RaidRatios))
		str += fmt.Sprintf(".level%d { color: %v; }\n", i, lerpColorString(float64(i)/float64(100), LevelColors, LevelRatios))
		barRatio := float64(i) / float64(100)
		str += fmt.Sprintf(".bar%d { %v }\n", i, barColor(barRatio, 0.5+barRatio/2.0, HealthColors, HealthRatios))
	}
	for i := 0; i < len(ItemColors); i++ {
		str += fmt.Sprintf(".item%d { color: %v; }\n", i, colorString(ItemColors[i]))
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

func (character *Character) AddItems() {
	for i := character.ItemLevel(); i < character.Level; i++ {
		slot := rand.Intn(NUM_SLOTS)
		item := character.Items[slot]
		itemLevel := int64(1)
		if item != nil {
			itemLevel = item.Level + 1
			if item.Rarity > ITEM_NORMAL {
				character.OldItems = append(character.OldItems, item)
			}
		}
		character.Items[slot] = NewItem(slot, itemLevel)
	}
}

func (character *Character) ItemList() template.HTML {
	str := ""
	for _, item := range character.Items {
		if item != nil {
			str += fmt.Sprintf("<span class=\"item%d\">%v</span> (%d), ", item.Rarity, item.Name, item.Level)
		}
	}
	if str == "" {
		return template.HTML(str)
	}
	return template.HTML(str[:len(str)-2])
}

func RandomItemName(slot int, level int64) (string, int) {
	if slot == SLOT_WEAPON && level >= 10 && rand.Float64() > 0.95 {
		return Uniques[rand.Intn(len(Uniques))], ITEM_UNIQUE
	}

	names := ItemNames[slot]
	name := names[rand.Intn(len(names))]

	chance := int64(4)

	rarity := 0
	if level > 9 {
		rarity++
	}

	prefix := rand.Float64() > 0.5
	for i := 0; i < 2; i++ {
		if rand.Float64() < float64(level-chance)/float64(chance) {
			if prefix {
				name = Prefixes[rand.Intn(len(Prefixes))] + " " + name
			} else {
				name = name + " of " + Suffixes[rand.Intn(len(Suffixes))]
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
	if character.XP >= character.MaxXP() {
		character.Level++
		character.XP = 0
		character.AddItems()
		return true
	}
	return false
}

func (game *Game) GetCharacter(name string, create bool) *Character {
	key := NameKey(name)
	character := game.Characters[key]
	if character == nil && create {
		character = &Character{Name: name, Items: make(Items, NUM_SLOTS)}
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
	name := MonsterNames[rand.Intn(len(MonsterNames))]
	prefix := "a"
	r := rand.Float64()
	if r > 0.95 {
		difficulty += rand.Float64() * 2
		first := MonsterUnique[rand.Intn(len(MonsterUnique))]
		second := ""
		for second == "" || second == first {
			second = MonsterUnique[rand.Intn(len(MonsterUnique))]
		}
		name = strings.ToUpper(string(first[0])) + first[1:] + second
		prefix = ""
	} else if r > 0.75 {
		difficulty += rand.Float64()
		name = MonsterLarge[rand.Intn(len(MonsterLarge))] + " " + name
	} else if r > 0.5 {
		difficulty -= rand.Float64() / 2.0
		name = MonsterSmall[rand.Intn(len(MonsterSmall))] + " " + name
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
	defeated := make(Monsters, len(game.Defeated))
	for i := 0; i < len(game.Defeated); i++ {
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

func (game *Game) Heal() {
	game.Monster.Heal(1)
}

func (game *Game) Attack(event *Event) {
	name := event.Line.Nick
	key := NameKey(name)

	// Create the character if it doesn't exist
	char := game.GetCharacter(name, true)
	char.Name = name

	if key == NameKey(event.Server.Conn.Me().Nick) || (key == game.Last && !*rpgallowrepeats) {
		return
	}
	game.Last = key
	monster := game.Monster
	monster.AddCharacter(name)
	monster.Health -= int64(len(monster.Characters))
	if monster.Health <= 0 {
		monster.Slayed = key
		monster.Died = time.Now()
		xp := int64(float64(len(monster.Characters)) * monster.Difficulty)
		if xp < 1 {
			xp = 1
		}
		prefix := monster.Prefix
		if prefix != "" {
			prefix = prefix + " "
		}
		for n, _ := range monster.Characters {
			char := game.GetCharacter(n, true)
			levelled := char.GainXP(xp)
			if char.Listening {
				if n == key {
					event.Server.Conn.Privmsg(name, fmt.Sprintf("You just slayed %v%v in %v and gained %d xp!", prefix, monster.Name, game.Room, xp))
				} else {
					event.Server.Conn.Privmsg(name, fmt.Sprintf("You helped %v slay %v%v in %v and gained %d xp!", event.Line.Nick, prefix, monster.Name, game.Room, xp))
				}
				if levelled {
					event.Server.Conn.Privmsg(name, fmt.Sprintf("You just levelled up in %v to level %d!", game.Room, char.Level))
				}
			}

		}
		game.Defeated = append(game.Defeated, monster)
		game.Monster = game.NewMonster()
		game.Save()
		game.Upload()
	}
}
