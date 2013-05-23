package septapus

import (
	"encoding/json"
	"errors"
	"fmt"
	"github.com/fluffle/golog/logging"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	client "github.com/fluffle/goirc/client"
)

type OldPRs map[string]*string

type LiftName string

func (liftName LiftName) String() string {
	return liftNames[liftName]
}

func (liftName LiftName) IsValid() bool {
	return liftName.String() != ""
}

type Unit int

func (unit Unit) String() string {
	switch {
	case unit == UNIT_LBS:
		return "lbs"
	case unit == UNIT_KGS:
		return "kgs"
	}
	return ""
}

type Weight struct {
	Value int
	Unit  Unit
}

func (weight *Weight) String() string {
	return fmt.Sprintf("%d%v", weight.Value, weight.Unit.String())
}

func (weight *Weight) IsValid() bool {
	return weight.Unit != UNIT_UNDEFINED
}

func (weight *Weight) Normalise() float64 {
	if weight.Unit == UNIT_KGS {
		return float64(weight.Value) * 2.20462
	}
	return float64(weight.Value)
}

func (weight *Weight) Compare(other *Weight) int {
	if weight.Normalise() > other.Normalise() {
		return 1
	} else if other.Normalise() > weight.Normalise() {
		return -1
	}
	return 0
}

var lbsRegex string = "lbs|lb"
var kgsRegex string = "kgs|kg"
var unitRegex string = lbsRegex + "|" + kgsRegex
var weightRegex string = "^([0-9]+)" + unitRegex + "$"

func NewWeight(str string) (*Weight, error) {
	weight := &Weight{}
	if wRegex, err := regexp.Compile(weightRegex); err == nil {
		if wRegex.MatchString(str) {
			if lRegex, err := regexp.Compile(lbsRegex); err == nil {
				if lRegex.MatchString(str) {
					weight.Unit = UNIT_LBS
				}
			} else {
				return nil, err
			}
			if kRegex, err := regexp.Compile(kgsRegex); err == nil {
				if kRegex.MatchString(str) {
					weight.Unit = UNIT_KGS
				}
			} else {
				return nil, err
			}
			if weight.Unit == UNIT_UNDEFINED {
				return nil, errors.New("Undefined unit. Use lbs or kgs.")
			}
			if uRegex, err := regexp.Compile(unitRegex); err == nil {
				str := uRegex.ReplaceAllString(str, "")
				if value, err := strconv.Atoi(str); err == nil {
					weight.Value = value
				} else {
					return nil, err
				}
			} else {
				return nil, err
			}
		}
	} else {
		return nil, err
	}
	return weight, nil
}

const (
	UNIT_UNDEFINED Unit = iota
	UNIT_LBS
	UNIT_KGS
)

type Lift struct {
	Name   LiftName
	Date   time.Time
	Reps   int
	Weight *Weight
}

func (lift *Lift) String() string {
	if lift.Reps < 2 {
		return fmt.Sprintf("%v (%v)", lift.Weight.String(), lift.Date.Format("02 Jan 2006"))
	}
	return fmt.Sprintf("%dx%v (%v)", lift.Reps, lift.Weight.String(), lift.Date.Format("02 Jan 2006"))
}

func NewLift(liftNameString string, liftString string) (*Lift, error) {
	lift := &Lift{}
	lift.Date = time.Now()

	liftName := LiftName(liftNameString)
	if !liftName.IsValid() {
		return nil, errors.New("Bad lift name. !prhelp for valid lift names.")
	}
	lift.Name = liftName

	parts := strings.Split(liftString, "x")

	if len(parts) == 1 {
		if weight, err := NewWeight(parts[0]); err == nil {
			lift.Weight = weight
		} else {
			return nil, err
		}
	} else if len(parts) == 2 {
		if weight, err := NewWeight(parts[1]); err == nil {
			lift.Weight = weight
		} else {
			return nil, err
		}
		if reps, err := strconv.Atoi(parts[0]); err == nil {
			lift.Reps = reps
		} else {
			return nil, err
		}
	}
	if lift.Name == BodyWeight && lift.Reps != 0 {
		return nil, errors.New("Cannot set reps for your bodyweight.")
	}
	return lift, nil
}

func (lift *Lift) Compare(other *Lift) int {
	if comparison := lift.Weight.Compare(other.Weight); comparison != 0 {
		return comparison
	}
	if lift.Reps > other.Reps {
		return 1
	} else if other.Reps > lift.Reps {
		return -1
	}
	if lift.Date.Before(other.Date) {
		return 1
	} else if other.Date.Before(lift.Date) {
		return -1
	}
	return 0
}

type Lifts []*Lift

func (l Lifts) Len() int      { return len(l) }
func (l Lifts) Swap(i, j int) { l[i], l[j] = l[j], l[i] }
func (l Lifts) Less(i, j int) bool {
	return l[i].Compare(l[j]) == 1
}

type Lifter struct {
	Nick string
	// PRName is the string, but must be string for unmarshalling.
	Lifts     map[string]Lifts
	bestLifts map[string]*Lift
}

func (lifter *Lifter) CalculateBest() {
	if lifter.bestLifts == nil {
		lifter.bestLifts = make(map[string]*Lift)
	}

	for liftName, lifts := range lifter.Lifts {
		if len(lifts) > 0 {
			max := lifts[0]
			for _, lift := range lifts {
				if lift.Compare(max) == 1 {
					max = lift
				}
			}
			lifter.bestLifts[liftName] = max
		}
	}
}

func (lifter *Lifter) Best(liftName LiftName) *Lift {
	if lifter.bestLifts == nil {
		return nil
	}
	return lifter.bestLifts[string(liftName)]
}

type PRS struct {
	sync.RWMutex
	OldPRs  map[string]OldPRs `json:"PRMaps"`
	Lifters map[string]*Lifter
}

func (prs *PRS) Migrate() {
	prs.Lock()
	defer prs.Unlock()

	prs.Lifters = make(map[string]*Lifter)
	for nick, prMap := range prs.OldPRs {
		lifter := prs.GetLifter(nick, true)
		for liftName, pr := range prMap {
			if lift, err := NewLift(liftName, *pr); err == nil {
				lifter.AddLift(lift)
			}
		}
	}
	prs.OldPRs = nil
}

func (prs *PRS) Init() {
	prs.Lock()
	defer prs.Unlock()

	if prs.Lifters == nil {
		prs.Lifters = make(map[string]*Lifter)
	} else {
		for _, lifter := range prs.Lifters {
			lifter.CalculateBest()
		}
	}
}

func (prs *PRS) GetLifter(nick string, create bool) (lifter *Lifter) {
	key := strings.ToLower(nick)
	lifter = prs.Lifters[key]
	if lifter == nil && create {
		lifter = &Lifter{
			Nick:      nick,
			Lifts:     make(map[string]Lifts),
			bestLifts: make(map[string]*Lift),
		}
		prs.Lifters[key] = lifter
	}
	return
}

func (lifter *Lifter) AddLift(lift *Lift) {
	key := string(lift.Name)
	lifter.Lifts[key] = append(lifter.Lifts[key], lift)
	if lifter.bestLifts[key] != nil {
		if lift.Compare(lifter.bestLifts[key]) == 1 {
			lifter.bestLifts[key] = lift
		}
	} else {
		lifter.bestLifts[key] = lift
	}
}

func (lifter *Lifter) List() string {
	str := ""
	if lifter.Lifts == nil {
		return str
	}
	for _, lift := range lifter.bestLifts {
		if len(str) != 0 {
			str += ", "
		}
		str += lift.Name.String() + ": " + lift.String()
	}
	return str
}

func (lifter *Lifter) ListLift(liftName LiftName, cap bool) string {
	str := ""
	if !liftName.IsValid() {
		return str
	}
	key := string(liftName)
	total := len(lifter.Lifts[key])
	if total == 0 {
		return str
	}
	count := total
	switch {
	case count > 3 && cap:
		count = 3
		str += fmt.Sprintf("Last 3 %vs: ", liftName.String())
	case count == 1:
		str += fmt.Sprintf("%v:", liftName.String())
	default:
		str += fmt.Sprintf("Last %d %vs: ", count, liftName.String())
	}
	for i := total - count; i < total; i++ {
		str += lifter.Lifts[key][i].String()
		if i+1 < total {
			str += ", "
		}
	}
	return str
}

const (
	Bench       LiftName = "bench"
	Squat       LiftName = "squat"
	Ohp         LiftName = "ohp"
	Deadlift    LiftName = "deadlift"
	CJ          LiftName = "c&j"
	Snatch      LiftName = "snatch"
	PowerClean  LiftName = "powerclean"
	PowerSnatch LiftName = "powersnatch"
	PushPress   LiftName = "pushpress"
	BodyWeight  LiftName = "bodyweight"
)

var liftNames = map[LiftName]string{
	Bench:       "Bench Press",
	Squat:       "Squat",
	Ohp:         "Overhead Press",
	Deadlift:    "Deadlift",
	CJ:          "Clean & Jerk",
	Snatch:      "Snatch",
	PowerClean:  "Power Clean",
	PowerSnatch: "Power Snatch",
	PushPress:   "Push Press",
	BodyWeight:  "Body Weight",
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
	prs := &PRS{}
	prs.Load(server.Name)

	defer prs.Save(server.Name)

	prchan := FilterSimpleCommand(FilterServer(settings.GetEventHandler(bot, client.PRIVMSG), server.Name), "!pr")
	prhistorychan := FilterSimpleCommand(FilterServer(settings.GetEventHandler(bot, client.PRIVMSG), server.Name), "!prhistory")
	praddchan := FilterSimpleCommand(FilterServer(settings.GetEventHandler(bot, client.PRIVMSG), server.Name), "!pradd")
	prclearchan := FilterSimpleCommand(FilterServer(settings.GetEventHandler(bot, client.PRIVMSG), server.Name), "!prclear")
	prrankchan := FilterSimpleCommand(FilterServer(settings.GetEventHandler(bot, client.PRIVMSG), server.Name), "!prrank")
	prhelpchan := FilterSimpleCommand(FilterServer(settings.GetEventHandler(bot, client.PRIVMSG), server.Name), "!prhelp")

	for {
		select {
		case event, ok := <-prchan:
			if !ok {
				return
			}

			fields := strings.Fields(event.Line.Text())
			message := ""

			if len(fields) == 2 || len(fields) == 3 {
				lifter := prs.GetLifter(fields[1], false)

				if lifter != nil {
					if len(fields) == 2 {
						message = lifter.List()
					} else {
						if liftName := LiftName(strings.ToLower(fields[2])); liftName.IsValid() {
							if lift := lifter.Best(liftName); lift != nil {
								message = liftName.String() + ": " + lift.String()
							}
						} else {
							message = "Bad lift. !prhelp to get a list of valid lifts."
						}
					}
					if message == "" {
						message = "No lifts for that nick."
						break
					}
				} else {
					message = "Bad Nick."
					break
				}

			}
			if message != "" {
				server.Conn.Privmsg(event.Line.Target(), message)
			} else {
				server.Conn.Privmsg(event.Line.Nick, "Bad command: !pr <nick> [lift]")
			}
		case event, ok := <-prhistorychan:
			if !ok {
				return
			}

			fields := strings.Fields(event.Line.Text())
			message := ""
			if len(fields) == 3 {
				lifter := prs.GetLifter(fields[1], false)
				if lifter != nil {
					liftName := LiftName(strings.ToLower(fields[2]))
					if liftName.IsValid() {
						message = lifter.ListLift(liftName, event.Line.Target() != event.Line.Nick)
					} else {
						message = "Bad lift. !prhelp to get a list of valid lifts."
					}
					if message == "" {
						message = "No lifts for that nick."
						break
					}
				} else {
					message = "Bad Nick."
					break
				}
			}
			if message != "" {
				server.Conn.Privmsg(event.Line.Target(), message)
			} else {
				server.Conn.Privmsg(event.Line.Nick, "Bad command: !pr <nick> <lift>")
			}
		case event, ok := <-praddchan:
			if !ok {
				return
			}

			fields := strings.Fields(event.Line.Text())
			if len(fields) == 3 {
				lifter := prs.GetLifter(event.Line.Nick, true)
				lift, err := NewLift(fields[1], fields[2])
				if err == nil {
					lifter.AddLift(lift)
					if lift == lifter.Best(lift.Name) {
						server.Conn.Privmsg(event.Line.Nick, fmt.Sprintf("Added lift, New PR!! %v: %v", lift.Name.String(), lift.String()))
					} else {
						server.Conn.Privmsg(event.Line.Nick, fmt.Sprintf("Added lift, %v: %v", lift.Name.String(), lift.String()))
					}
					break
				} else {
					server.Conn.Privmsg(event.Line.Nick, err.Error())
					break
				}
			} else {
				server.Conn.Privmsg(event.Line.Nick, "Bad command. !prset [lift] [weight]")
			}
		case event, ok := <-prclearchan:
			if !ok {
				return
			}

			fields := strings.Fields(event.Line.Text())
			if len(fields) == 2 {
				lifter := prs.GetLifter(event.Line.Nick, false)

				if lifter != nil {
					liftName := LiftName(strings.ToLower(fields[1]))
					key := string(liftName)
					if !liftName.IsValid() {
						server.Conn.Privmsg(event.Line.Nick, "Bad lift. !prhelp to get a list of valid lifts.")
						break
					} else if lifter.Lifts[key] == nil {
						server.Conn.Privmsg(event.Line.Nick, "No PR's found")
						break
					} else {
						delete(lifter.Lifts, key)
					}
				} else {
					server.Conn.Privmsg(event.Line.Nick, "No PR's found.")
				}

			} else {
				server.Conn.Privmsg(event.Line.Nick, "Bad command. !prclear [lift]")
			}
		case event, ok := <-prrankchan:
			if !ok {
				return
			}
			fields := strings.Fields(event.Line.Text())
			if len(fields) >= 4 {
				liftName := LiftName(strings.ToLower(fields[1]))
				if !liftName.IsValid() {
					break
				}
				people := fields[2:]
				if len(people) {
					liftToLifter := make(map[*Lift]*Lifter)
					bests := make(Lifts, 0)
					for _, name := range people {
						if lifter := prs.GetLifter(name, false); lifter != nil {
							if best := lifter.Best(liftName); best != nil {
								liftToLifter[best] = lifter
								bests = append(bests, best)
							}
						}
					}
					if len(bests) {
						sort.Sort(bests)
						msg := ""
						for _, lift := range bests {
							if len(msg) != 0 {
								msg += ", "
							}
							msg += fmt.Sprintf("%v (%v)", liftToLifter[lift].Nick, lift.Weight.String())
						}
						msg = fmt.Sprintf("%v: %v", liftName.String(), msg)
						server.Conn.Privmsg(event.Line.Target(), msg)
					}
				}
			}

		case event, ok := <-prhelpchan:
			if !ok {
				return
			}

			message := ""
			for liftName := range liftNames {
				if len(message) > 0 {
					message += ", "
				}
				message += liftName.String()
			}
			server.Conn.Privmsg(event.Line.Nick, "Commands:")
			server.Conn.Privmsg(event.Line.Nick, "!pr <nick> [lift] - Prints the all the PR's for a nick, or just the chosen lift.")
			server.Conn.Privmsg(event.Line.Nick, "!prset [lift] [weight] - Sets a PR for a lift.")
			server.Conn.Privmsg(event.Line.Nick, "!prrank <lift> <nick> <othernick> [nick,] - Ranks a list of nicks on their PR's.")
			server.Conn.Privmsg(event.Line.Nick, "!prhistory <nick> <lift> - Prints the PR history for a nick's lift.")
			server.Conn.Privmsg(event.Line.Nick, "!prclear [lift] - Clears all PR's for a lift.")
			server.Conn.Privmsg(event.Line.Nick, "Valid lifts: "+message)
			server.Conn.Privmsg(event.Line.Nick, "Valid weights can be in kgs or lbs with optional reps. eg: 1kg, 100lbs, 32x225lbs, 1x25kgs")
		case <-time.After(1 * time.Minute):
			prs.Save(server.Name)
		}
	}

}

func (prs *PRS) Load(server ServerName) {
	prs.Lock()

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
	prs.Unlock()
	prs.Init()
	if prs.OldPRs != nil {
		prs.Migrate()
		prs.Save(server)
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
