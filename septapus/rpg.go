package septapus

import (
	"encoding/json"
	"fmt"
	"math/rand"
	"os"
	"strings"
	"time"

	"github.com/fluffle/goirc/client"
	"github.com/fluffle/golog/logging"
)

type Character struct {
	Name      string
	XP        int
	Listening bool
}

type Monster struct {
	Name       string
	MaxHealth  int
	Health     int
	Difficulty float64
	Characters map[string]bool
	Prefix     string
	Slayed     string
}

type Game struct {
	Server     ServerName
	Room       RoomName
	Characters map[string]*Character
	Monster    *Monster
	Defeated   []*Monster
	Last       string
}

type RPGPlugin struct {
	settings *PluginSettings
}

var MonsterNames []string
var MonsterSmall []string
var MonsterLarge []string
var MonsterUnique []string

func init() {
	MonsterSmall = []string{"Tiny", "Small", "Weak", "Infected", "Sick", "Fragile", "Impaired", "Blind"}
	MonsterLarge = []string{"Large", "Giant", "Huge", "Epic", "King", "Champion", "Queen", "Master", "Lord"}
	MonsterUnique = []string{"blood", "death", "rot", "sneeze", "pus", "spit", "puke", "burn", "shot", "rend", "slice", "maim"}
	MonsterNames = []string{"Skeleton", "Zombie", "Slime", "Kobold", "Ant", "Cockatrice", "Pyrolisk", "Werewolf", "Wolf", "Warg", "Hell-hound", "Gas Spore", "Gremlin", "Gargoyle", "Mind Flayer", "Imp", "Mimic", "Nymph", "Goblin", "Orc", "Mastodon", "Kraken", "Spider", "Scorpion", "Unicorn", "Narwhal", "Narhorse", "Worm", "Angel", "Archon", "Bat", "Centaur", "Dragon", "Elemental", "Minotaur", "Lich", "Mummy", "Naga", "Ogre", "Snake", "Troll", "Ghoul", "Golem", "Doppelganger", "Ghost", "Shade", "Demon", "Pit Fiend", "Balrog"}
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
	defer game.Save()

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
		case <-time.After(5 * time.Minute):
			game.Save()
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
				if len(fields) == 3 && fields[1] == string(room) {
					char.Listening = fields[2] == "true"
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
}

func (game *Game) Load(server ServerName, room RoomName) {
	filename := "rpg/" + string(server) + string(room)

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
	filename := "rpg/" + string(game.Server) + string(game.Room)

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

func (game *Game) Init(server ServerName, room RoomName) {
	if game.Server == "" {
		game.Server = server
	}
	if game.Room == "" {
		game.Room = room
	}
	if game.Characters == nil {
		game.Characters = make(map[string]*Character)
	}
	if game.Monster == nil {
		game.Monster = game.NewMonster()
	}
	if game.Defeated == nil {
		game.Defeated = make([]*Monster, 0)
	}
}

func NameKey(name string) string {
	return strings.ToLower(name)
}

func (game *Game) GetCharacter(name string, create bool) *Character {
	key := NameKey(name)
	character := game.Characters[key]
	if character == nil && create {
		character = &Character{Name: name}
		game.Characters[key] = character
	}
	return character
}

func (game *Game) NewMonster() *Monster {
	health := 1 + len(game.Defeated)
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
	health = int(float64(health) * difficulty)

	if len(prefix) > 0 && (name[0] == 'a' || name[0] == 'e' || name[0] == 'i' || name[0] == 'o' || name[0] == 'u') {
		prefix = "an"
	}

	monster := &Monster{
		Name:       name,
		MaxHealth:  health,
		Health:     health,
		Difficulty: difficulty,
		Characters: make(map[string]bool),
		Prefix:     prefix,
	}
	return monster
}

func (monster *Monster) AddCharacter(name string) {
	key := NameKey(name)
	monster.Characters[key] = true
}

func (monster *Monster) Heal(health int) {
	monster.Health += health
	if monster.Health > monster.MaxHealth {
		monster.Health = monster.MaxHealth
	}
}

func (game *Game) Heal() {
	game.Monster.Heal(1)
}

func (game *Game) Attack(event *Event) {
	name := NameKey(event.Line.Nick)

	// Create the character if it doesn't exist
	game.GetCharacter(name, true)
	if name == NameKey(event.Server.Conn.Me().Nick) || name == game.Last {
		return
	}
	game.Last = name
	monster := game.Monster
	monster.AddCharacter(name)
	monster.Health -= len(monster.Characters)
	if monster.Health <= 0 {
		monster.Slayed = name
		xp := int(float64(len(monster.Characters)) * monster.Difficulty)
		if xp < 1 {
			xp = 1
		}
		prefix := monster.Prefix
		if prefix != "" {
			prefix = prefix + " "
		}
		for n, _ := range monster.Characters {
			char := game.GetCharacter(n, true)
			char.XP += xp
			if char.Listening {
				if n == name {
					event.Server.Conn.Privmsg(event.Line.Nick, fmt.Sprintf("You just slayed %v%v in %v and gained %d xp!", prefix, monster.Name, game.Room, xp))
				} else {
					event.Server.Conn.Privmsg(event.Line.Nick, fmt.Sprintf("You helped %v slay %v%v in %v and gained %d xp!", event.Line.Nick, prefix, monster.Name, game.Room, xp))
				}
			}

		}
		game.Defeated = append(game.Defeated, monster)
		game.Monster = game.NewMonster()
	}
}
