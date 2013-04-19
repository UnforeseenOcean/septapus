package septapus

import (
	"fmt"
	"sync"
	"time"

	"github.com/fluffle/goirc/client"
)

var eventList = [...]string{client.REGISTER, client.CONNECTED, client.DISCONNECTED, client.ACTION, client.AWAY, client.CTCP, client.CTCPREPLY, client.INVITE, client.JOIN, client.KICK, client.MODE, client.NICK, client.NOTICE, client.OPER, client.PART, client.PASS, client.PING, client.PONG, client.PRIVMSG, client.QUIT, client.TOPIC, client.USER, client.VERSION, client.VHOST, client.WHO, client.WHOIS}

type ServerName string
type RoomName string
type EventName string

var (
	AllServers ServerName = "*"
	AllRooms   RoomName   = "*"
)

type Server struct {
	Name   ServerName
	Config *client.Config
	Rooms  []RoomName
	Conn   *client.Conn
}

func NewServer(server ServerName, config *client.Config, rooms []RoomName) *Server {
	return &Server{ServerName(server), config, rooms, nil}
}

func NewServerSimple(servername, host, nick, ident, name string, rooms []string) *Server {
	config := client.NewConfig(nick, ident, name)
	config.Server = host
	config.Version = name
	config.QuitMessage = "Lates"
	config.Recover = func(conn *client.Conn, line *client.Line) {}
	r := make([]RoomName, len(rooms))
	for i, value := range rooms {
		r[i] = RoomName(value)
	}
	return NewServer(ServerName(servername), config, r)
}

type Event struct {
	Server *Server
	Room   RoomName
	Line   *client.Line
}

type EventDispatcher struct {
	sync.RWMutex

	channels []chan *Event
}

func (e *EventDispatcher) AddHandler() chan *Event {
	e.Lock()
	defer e.Unlock()

	channel := make(chan *Event)
	e.channels = append(e.channels, channel)
	return channel
}

func (e *EventDispatcher) Broadcast(event *Event) {
	e.RLock()
	defer e.RUnlock()

	for _, channel := range e.channels {
		channel <- event
	}
}

func (e *EventDispatcher) Close() {
	e.Lock()
	defer e.Unlock()

	for _, channel := range e.channels {
		close(channel)
	}
}

type Bot struct {
	sync.RWMutex

	servers                map[ServerName]*Server
	handlers               map[EventName]bool
	removers               []client.Remover
	serverEventDispatchers map[ServerName]map[EventName]*EventDispatcher
	roomEventDispatchers   map[ServerName]map[RoomName]map[EventName]*EventDispatcher
	plugins                []Plugin
}

func NewBot() *Bot {
	bot := &Bot{}
	bot.AddPlugin(NewSimplePlugin(ConnectPlugin))
	bot.AddPlugin(NewSimplePlugin(DisconnectPlugin))
	return bot
}

func (bot *Bot) AddServer(server *Server) (*Server, error) {
	bot.Lock()
	defer bot.Unlock()

	if bot.servers == nil {
		bot.servers = make(map[ServerName]*Server)
	}
	if bot.servers[server.Name] != nil {
		return bot.servers[server.Name], nil
	}
	bot.servers[server.Name] = server

	conn := client.Client(server.Config)
	server.Conn = conn

	for _, event := range eventList {
		eventName := EventName(event)
		bot.removers = append(bot.removers, conn.HandleFunc(event, func(conn *client.Conn, line *client.Line) {
			room := RoomName(line.Target())
			bot.RLock()
			defer bot.RUnlock()

			if servers := bot.serverEventDispatchers[AllServers]; servers != nil {
				if events := servers[eventName]; events != nil {
					events.Broadcast(&Event{server, room, line})
				}
			}
			if servers := bot.serverEventDispatchers[server.Name]; servers != nil {
				if events := servers[eventName]; events != nil {
					events.Broadcast(&Event{server, room, line})
				}
			}
			if servers := bot.roomEventDispatchers[server.Name]; servers != nil {
				if rooms := servers[room]; rooms != nil {
					if events := rooms[eventName]; events != nil {
						events.Broadcast(&Event{server, room, line})
					}
				}
			}
			if servers := bot.roomEventDispatchers[AllServers]; servers != nil {
				if rooms := servers[room]; rooms != nil {
					if events := rooms[eventName]; events != nil {
						events.Broadcast(&Event{server, room, line})
					}
				}
			}
			if servers := bot.roomEventDispatchers[AllServers]; servers != nil {
				if rooms := servers[AllRooms]; rooms != nil {
					if events := rooms[eventName]; events != nil {
						events.Broadcast(&Event{server, room, line})
					}
				}
			}

		}))
	}

	return server, conn.Connect()
}

func (bot *Bot) GetAllEventHandler(event EventName) chan *Event {
	return bot.GetServerEventHandler(AllServers, event)
}

func (bot *Bot) GetServerEventHandler(server ServerName, event EventName) chan *Event {
	bot.Lock()
	defer bot.Unlock()

	if bot.serverEventDispatchers == nil {
		bot.serverEventDispatchers = make(map[ServerName]map[EventName]*EventDispatcher)
	}
	servers := bot.serverEventDispatchers[server]
	if servers == nil {
		servers = make(map[EventName]*EventDispatcher)
		bot.serverEventDispatchers[server] = servers
	}
	events := servers[event]
	if events == nil {
		events = &EventDispatcher{}
		servers[event] = events
	}
	return events.AddHandler()
}

func (bot *Bot) GetAllRoomsEventHandler(server ServerName, event EventName) chan *Event {
	return bot.GetRoomEventHandler(server, AllRooms, event)
}

func (bot *Bot) GetRoomEventHandler(server ServerName, room RoomName, event EventName) chan *Event {
	bot.Lock()
	defer bot.Unlock()

	if bot.roomEventDispatchers == nil {
		bot.roomEventDispatchers = make(map[ServerName]map[RoomName]map[EventName]*EventDispatcher)
	}
	servers := bot.roomEventDispatchers[server]
	if servers == nil {
		servers = make(map[RoomName]map[EventName]*EventDispatcher)
		bot.roomEventDispatchers[server] = servers
	}
	rooms := servers[room]
	if rooms == nil {
		rooms = make(map[EventName]*EventDispatcher)
		servers[room] = rooms
	}
	events := rooms[event]
	if events == nil {
		events = &EventDispatcher{}
		rooms[event] = events
	}
	return events.AddHandler()
}

// Filters a channel to only return the events that targets our nick.
func FilterChannel(channel chan *Event) chan *Event {
	filteredchannel := make(chan *Event)
	go func() {
		defer close(filteredchannel)
		for {
			event, ok := <-channel
			if !ok {
				return
			}
			if event.Line.Nick == event.Server.Conn.Me().Nick {
				filteredchannel <- event
			}
		}
	}()
	return filteredchannel
}

func (bot *Bot) Disconnect() {
	bot.Lock()
	defer bot.Unlock()

	for _, servers := range bot.serverEventDispatchers {
		for _, events := range servers {
			events.Close()
		}
	}
	for _, servers := range bot.roomEventDispatchers {
		for _, rooms := range servers {
			for _, events := range rooms {
				events.Close()
			}
		}
	}
	for _, remover := range bot.removers {
		remover.Remove()
	}
	for _, server := range bot.servers {
		server.Conn.Quit()
	}

	<-time.After(1 * time.Second)
}

type Plugin interface {
	Init(bot *Bot)
}

type SimplePluginInit func(bot *Bot)

type SimplePlugin struct {
	init SimplePluginInit
}

func NewSimplePlugin(init SimplePluginInit) Plugin {
	return &SimplePlugin{init}
}

func (plugin *SimplePlugin) Init(bot *Bot) {
	plugin.init(bot)
}

func (b *Bot) AddPlugin(plugin Plugin) {
	b.Lock()
	defer b.Unlock()

	b.plugins = append(b.plugins, plugin)
	go plugin.Init(b)
}

func ConnectPlugin(bot *Bot) {
	channel := bot.GetAllEventHandler(client.CONNECTED)
	for {
		event, ok := <-channel
		if !ok {
			break
		}
		fmt.Println(event.Server.Name, "Connected")
		for _, channel := range event.Server.Rooms {
			event.Server.Conn.Join(string(channel))
		}
	}
}

func DisconnectPlugin(bot *Bot) {
	channel := bot.GetAllEventHandler(client.DISCONNECTED)
	for {
		event, ok := <-channel
		if !ok {
			break
		}
		fmt.Println(event.Server.Name, "Disconnected")
		event.Server.Conn.Connect()
	}
}
