package septapus

import (
	"encoding/json"
	"fmt"
	"github.com/fluffle/golog/logging"
	"os"
	"regexp"
	"strconv"
	"strings"
	"sync"

	client "github.com/fluffle/goirc/client"
)

type PRName string

type PR string

type PRMap map[string]*PR

type PRS struct {
	sync.RWMutex
	PRMaps map[string]PRMap
}

const (
	Bench       PRName = "bench"
	Squat       PRName = "squat"
	Ohp         PRName = "ohp"
	Deadlift    PRName = "deadlift"
	CJ          PRName = "c&j"
	Snatch      PRName = "snatch"
	PowerClean  PRName = "powerclean"
	PowerSnatch PRName = "powersnatch"
)

var prNames = map[PRName]string{
	Bench:       "Bench Press",
	Squat:       "Squat",
	Ohp:         "Overhead Press",
	Deadlift:    "Deadlift",
	CJ:          "Clean & Jerk",
	Snatch:      "Snatch",
	PowerClean:  "Power Clean",
	PowerSnatch: "Power Snatch",
}

func NewPRPlugin(settings *PluginSettings) Plugin {
	return NewSimplePlugin(PRPlugin, settings)
}

func PRPlugin(bot *Bot, settings *PluginSettings) {
	for _, server := range bot.servers {
		if server.Conn.Connected() {
			go PRListener(bot, settings, server)
		}
	}
	channel := bot.GetEventHandler(client.CONNECTED)
	for event := range channel {
		go PRListener(bot, settings, event.Server)
	}
}

func PRListener(bot *Bot, settings *PluginSettings, server *Server) {
	prs := &PRS{PRMaps: make(map[string]PRMap)}
	prs.Load(server.Name)

	prchan := FilterSimpleCommand(FilterServer(settings.GetEventHandler(bot, client.PRIVMSG), server.Name), "!pr")
	prsetchan := FilterSimpleCommand(FilterServer(settings.GetEventHandler(bot, client.PRIVMSG), server.Name), "!prset")
	prhelpchan := FilterSimpleCommand(FilterServer(settings.GetEventHandler(bot, client.PRIVMSG), server.Name), "!prhelp")

	for {
		select {
		case event, ok := <-prchan:
			if !ok {
				return
			}

			fields := strings.Fields(event.Line.Text())
			message := ""
			if len(fields) == 2 {
				message = prs.List(fields[1])
			} else if len(fields) == 3 {
				prName := PRName(fields[2])
				pr := prs.Get(fields[1], prName)
				if pr != nil {
					message = fmt.Sprintf("%v %v", prNames[prName], *pr)
				} else {
					server.Conn.Privmsg(event.Line.Nick, "Bad command: !pr <nick> [lift]")
				}
			}
			if message != "" {
				server.Conn.Privmsg(event.Line.Target(), message)
			} else {
				server.Conn.Privmsg(event.Line.Nick, "Bad command: !pr <nick> [lift]")
			}
		case event, ok := <-prsetchan:
			if !ok {
				return
			}

			fields := strings.Fields(event.Line.Text())
			if len(fields) == 3 {
				pr := prs.Set(event.Line.Nick, PRName(fields[1]), PR(fields[2]))
				if pr != nil {
					prs.Save(server.Name)
				} else {
					server.Conn.Privmsg(event.Line.Nick, "Bad command. !prset [lift] [weight]")
				}
			} else {
				server.Conn.Privmsg(event.Line.Nick, "Bad command. !prset [lift] [weight]")
			}
		case event, ok := <-prhelpchan:
			if !ok {
				return
			}

			message := ""
			for prName := range prNames {
				if len(message) > 0 {
					message += ", "
				}
				message += string(prName)
			}
			server.Conn.Privmsg(event.Line.Nick, "Commands:")
			server.Conn.Privmsg(event.Line.Nick, "!pr <nick> [lift] - Prints the all the PR's for a nick, or just the chosen lift.")
			server.Conn.Privmsg(event.Line.Nick, "!prset [lift] [weight] - Sets a PR for a lift.")
			server.Conn.Privmsg(event.Line.Nick, "Valid lifts: "+message)
			server.Conn.Privmsg(event.Line.Nick, "Valid weights can be in kgs or lbs with optional reps. eg: 1kg, 100lbs, 32x225lbs, 1x25kgs")
		}
	}
}

func (prs *PRS) Load(server ServerName) {
	prs.Lock()
	defer prs.Unlock()

	filename := "prs/" + string(server) + ".json"

	if file, err := os.Open(filename); err == nil {
		defer file.Close()
		dec := json.NewDecoder(file)
		if err := dec.Decode(prs); err != nil {
			logging.Info("Error loading prs", server, err)
		} else {
			logging.Info("Loaded prs for", server)
		}
	} else {
		logging.Info("Error loading file", server, filename, err)
	}
}

func (prs *PRS) Save(server ServerName) {
	prs.Lock()
	defer prs.Unlock()

	filename := "prs/" + string(server) + ".json"

	if file, err := os.Create(filename); err == nil {
		defer file.Close()
		enc := json.NewEncoder(file)
		if err := enc.Encode(prs); err != nil {
			logging.Info("Error saving prs", server, err)
		} else {
			logging.Info("Saved prs", server)
		}
	} else {
		logging.Info("Error creating file", server, filename, err)
	}
}

func (prs *PRS) Get(name string, prName PRName) *PR {
	prs.RLock()
	defer prs.RUnlock()

	prm := prs.PRMaps[name]
	if prm == nil {
		return nil
	}
	return prm[string(prName)]
}

func (prs *PRS) List(name string) string {
	prs.RLock()
	defer prs.RUnlock()

	str := ""
	prm := prs.PRMaps[name]
	if prm == nil {
		return str
	}
	for prName, pr := range prm {
		if len(str) > 0 {
			str += ", "
		}
		str += fmt.Sprintf("%v %v", prNames[PRName(prName)], *pr)
	}
	return str
}

func (prs *PRS) Set(name string, prName PRName, pr PR) *PR {
	prs.Lock()
	defer prs.Unlock()

	if !prName.IsValid() || !pr.IsValid() {
		return nil
	}

	prm := prs.PRMaps[name]
	if prm == nil {
		prm = make(PRMap)
		prs.PRMaps[name] = prm
	}
	prm[string(prName)] = &pr
	return &pr
}

func (prName PRName) IsValid() bool {
	return prNames[prName] != ""
}

func (pr PR) IsValid() bool {
	parts := strings.Split(string(pr), "x")

	if len(parts) == 1 {
		return Weight(parts[0]).IsValid()
	} else if len(parts) == 2 {
		if !Weight(parts[1]).IsValid() {
			return false
		}
		_, ok := strconv.Atoi(parts[0])
		return ok == nil
	}
	return true
}

type Weight string

var weightRegex string = "^[0-9]+lb|lbs|kg|kgs$"

func (weight Weight) IsValid() bool {
	if regex, err := regexp.Compile(weightRegex); err == nil {
		return regex.MatchString(string(weight))
	}
	return false
}
