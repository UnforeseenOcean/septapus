package septapus

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"html/template"
	"image"
	"image/color"
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

type Character struct {
	Name      string
	XP        int
	Listening bool
}

type Characters []*Character

func (c Characters) Len() int           { return len(c) }
func (c Characters) Swap(i, j int)      { c[i], c[j] = c[j], c[i] }
func (c Characters) Less(i, j int) bool { return c[i].XP > c[j].XP }

type Monster struct {
	Name       string
	MaxHealth  int
	Health     int
	Difficulty float64
	Characters map[string]int
	Prefix     string
	Slayed     string
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

var HealthColors []color.Color
var HealthRatios []float64
var XPColors []color.Color
var XPRatios []float64
var RaidColors []color.Color
var RaidRatios []float64

func init() {
	MonsterSmall = []string{"Tiny", "Small", "Weak", "Infected", "Sick", "Fragile", "Impaired", "Blind"}
	MonsterLarge = []string{"Large", "Giant", "Huge", "Epic", "King", "Champion", "Queen", "Master", "Lord"}
	MonsterUnique = []string{"blood", "death", "rot", "sneeze", "pus", "spit", "puke", "burn", "shot", "rend", "slice", "maim"}
	MonsterNames = []string{"Skeleton", "Zombie", "Slime", "Kobold", "Ant", "Cockatrice", "Pyrolisk", "Werewolf", "Wolf", "Warg", "Hell-hound", "Gas Spore", "Gremlin", "Gargoyle", "Mind Flayer", "Imp", "Mimic", "Nymph", "Goblin", "Orc", "Mastodon", "Kraken", "Spider", "Scorpion", "Unicorn", "Narwhal", "Narhorse", "Worm", "Angel", "Archon", "Bat", "Centaur", "Dragon", "Elemental", "Minotaur", "Lich", "Mummy", "Naga", "Ogre", "Snake", "Troll", "Ghoul", "Golem", "Doppelganger", "Ghost", "Shade", "Demon", "Pit Fiend", "Balrog"}

	HealthColors = []color.Color{color.RGBA{0, 0, 0, 1}, color.RGBA{153, 0, 0, 1}, color.RGBA{204, 0, 0, 1}, color.RGBA{255, 153, 0, 1}, color.RGBA{255, 204, 0, 1}, color.RGBA{0, 204, 0, 1}}
	HealthRatios = []float64{0, 0.5, 0.625, 0.75, 0.875, 1}
	XPColors = []color.Color{color.RGBA{255, 204, 0, 1}, color.RGBA{255, 51, 0, 1}}
	XPRatios = []float64{0, 1}
	RaidColors = []color.Color{color.RGBA{204, 204, 204, 1}, color.RGBA{153, 153, 153, 1}}
	RaidRatios = []float64{0, 1}
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

	game.Upload()

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

	save := time.NewTicker(5 * time.Minute).C
	savequit := make(chan bool)
	// Save in a goroutine so it does not block the RPG, but only do one save at a time
	go func() {
		for {
			select {
			case <-save:
				game.Save()
				game.Upload()
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
	<body>
		<div class="title"><img src="../images/Septapus.png" alt="Septapus"></div>
		{{if .Characters}}
		<p>
			<h2>Characters:</h2>
			<table class="characters">
				<tr><th>Name</th><th>XP</th></tr>
				{{range .GetSortedCharacters}}
				<tr><td class="name">{{.Name}}</td><td class="xp" style="color: {{.XPColor $}};">{{.XP}}</td></tr>
				{{end}}
			</table>
		</p>
		{{end}}
		<p>
			<h2>Current Fight:</h2>
			<table class="currentfight">
			<tr><th>Name</th><th>Health</th><th>Raid</th></tr>
			{{with .Monster}}
			<tr><td class="name">{{.Name}}</td><td class="health" style="color: {{.HealthColor}};">{{.Health}}/{{.MaxHealth}}</td><td class="raid">{{.CharacterList $}}</td>
			{{end}}
			</table>
		</p>
		{{if .Defeated}}
		<p>
		<h2>Previous Fights:</h2>
			<table class="previousfights">
				<tr><th>Name</th><th>Health</th><th>Slayed By</th><th>Raid</th></tr>
				{{range .Defeated}}
				<tr><td class="name">{{.Name}}</td><td class="health" style="color: {{.HealthColor}};">{{.Health}}/{{.MaxHealth}}</td><td class="slayed">{{.SlayedList $}}</td><td class="raid">{{.CharacterList $}}</td></tr>
				{{end}}
			</table>
		</p>
		{{end}}
		<p>
			<a href="http://validator.w3.org/check?uri=referer"><img src="http://www.w3.org/Icons/valid-html401" alt="Valid HTML 4.01 Strict" height="31" width="88"></a>
		</p>
	</body>
</html>
`

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

func (game *Game) GetSortedCharacters() Characters {
	characters := make(Characters, 0)
	for _, value := range game.Characters {
		characters = append(characters, value)
	}
	sort.Sort(characters)
	return characters
}

func (game *Game) NewMonster() *Monster {
	health := len(game.Defeated)
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
		Characters: make(map[string]int),
		Prefix:     prefix,
	}
	return monster
}

func (monster *Monster) AddCharacter(name string) {
	key := NameKey(name)
	monster.Characters[key]++
}

func (monster *Monster) Heal(health int) {
	monster.Health += health
	if monster.Health > monster.MaxHealth {
		monster.Health = monster.MaxHealth
	}
}

func (monster *Monster) HealthColor() template.CSS {
	health := monster.Health + monster.MaxHealth
	if health < 0 {
		health = 0
	}
	if health > monster.MaxHealth*2 {
		health = monster.MaxHealth * 2
	}
	color := lerp(float64(health)/float64(monster.MaxHealth*2), HealthColors, HealthRatios)
	if color == nil {
		color = image.Black
	}
	r, g, b, _ := color.RGBA()
	return template.CSS(fmt.Sprintf("rgb(%d, %d, %d)", r>>8, g>>8, b>>8))
}

// Following methods are for the template.
func (monster *Monster) CharacterList(game *Game) template.HTML {
	max := 1
	for _, c := range monster.Characters {
		if c > max {
			max = c
		}
	}
	str := ""
	for name, c := range monster.Characters {
		color := lerp(float64(c)/float64(max), RaidColors, RaidRatios)
		if color == nil {
			color = image.Black
		}
		r, g, b, _ := color.RGBA()
		str += fmt.Sprintf("<span style=\"color: rgb(%d, %d, %d);\">%s</span>, ", r>>8, g>>8, b>>8, game.GetCharacter(name, true).Name)
	}
	if str == "" {
		return template.HTML(str)
	}
	return template.HTML(str[:len(str)-2])
}

func (monster *Monster) SlayedList(game *Game) string {
	return game.GetCharacter(monster.Slayed, true).Name
}

func (character *Character) XPColor(game *Game) template.CSS {
	max := 1
	for _, c := range game.Characters {
		if c.XP > max {
			max = c.XP
		}
	}
	color := lerp(float64(character.XP)/float64(max), XPColors, XPRatios)
	if color == nil {
		color = image.Black
	}
	r, g, b, _ := color.RGBA()
	return template.CSS(fmt.Sprintf("rgb(%d, %d, %d)", r>>8, g>>8, b>>8))
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
	monster.Health -= len(monster.Characters)
	if monster.Health <= 0 {
		monster.Slayed = key
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
				if n == key {
					event.Server.Conn.Privmsg(event.Line.Nick, fmt.Sprintf("You just slayed %v%v in %v and gained %d xp!", prefix, monster.Name, game.Room, xp))
				} else {
					event.Server.Conn.Privmsg(event.Line.Nick, fmt.Sprintf("You helped %v slay %v%v in %v and gained %d xp!", event.Line.Nick, prefix, monster.Name, game.Room, xp))
				}
			}

		}
		game.Defeated = append(game.Defeated, monster)
		game.Monster = game.NewMonster()
		game.Save()
		game.Upload()
	}
}
