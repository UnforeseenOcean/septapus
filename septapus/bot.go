package septapus

import (
	"fmt"
	"sync"
	"time"

	"github.com/fluffle/goirc/client"
)

type ServerName string
type RoomName string
type EventName string

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

	channels map[chan *Event]bool
}

func NewEventDispatcher() *EventDispatcher {
	return &EventDispatcher{channels: make(map[chan *Event]bool)}
}

func (e *EventDispatcher) GetEventHandler() chan *Event {
	e.Lock()
	defer e.Unlock()

	channel := make(chan *Event, 10)
	e.channels[channel] = true
	return channel
}

func (e *EventDispatcher) RemoveEventHandler(channel chan *Event) {
	e.Lock()
	defer e.Unlock()

	close(channel)
	delete(e.channels, channel)
}

func (e *EventDispatcher) Broadcast(event *Event) {
	e.RLock()
	defer e.RUnlock()

	for channel, _ := range e.channels {
		channel <- event
	}
}

func (e *EventDispatcher) Close() {
	e.Lock()
	defer e.Unlock()

	for channel, _ := range e.channels {
		close(channel)
	}
}

type Bot struct {
	sync.RWMutex

	servers  map[ServerName]*Server
	removers []client.Remover
	events   map[EventName]*EventDispatcher
	plugins  []Plugin
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
	for event, _ := range bot.events {
		bot.makeEvents(server, event)
	}

	conn := client.Client(server.Config)
	server.Conn = conn

	return server, conn.Connect()
}

func (bot *Bot) makeEvents(server *Server, event EventName) {
	events := bot.events[event]
	bot.removers = append(bot.removers, server.Conn.HandleFunc(string(event), func(conn *client.Conn, line *client.Line) {
		events.Broadcast(&Event{server, RoomName(line.Target()), line})
	}))
}

func (bot *Bot) GetEventHandler(event EventName) chan *Event {
	bot.Lock()
	defer bot.Unlock()

	if bot.events == nil {
		bot.events = make(map[EventName]*EventDispatcher)
	}

	events := bot.events[event]
	if events == nil {
		events = NewEventDispatcher()
		bot.events[event] = events
		for _, server := range bot.servers {
			bot.makeEvents(server, event)
		}
	}
	return events.GetEventHandler()
}

func (bot *Bot) RemoveEventHandler(event chan *Event) {
	for _, events := range bot.events {
		events.RemoveEventHandler(event)
	}
}

func (bot *Bot) BroadcastEvent(name EventName, event *Event) {
	if bot.events == nil {
		return
	}
	events := bot.events[name]
	if events != nil {
		events.Broadcast(event)
	}
}

// Filters a channel to only return the events that targets our nick.
func FilterSelf(channel chan *Event) chan *Event {
	filteredchannel := make(chan *Event, cap(channel))
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

// Filters a channel to only return the events that are fired from a server.
func FilterServer(channel chan *Event, server ServerName) chan *Event {
	filteredchannel := make(chan *Event, cap(channel))
	go func() {
		defer close(filteredchannel)
		for {
			event, ok := <-channel
			if !ok {
				return
			}
			if event.Server.Name == server {
				filteredchannel <- event
			}
		}
	}()
	return filteredchannel
}

// Filters a channel to only return the events that are fired from a room.
func FilterRoom(channel chan *Event, server ServerName, room RoomName) chan *Event {
	filteredchannel := make(chan *Event, cap(channel))
	go func() {
		defer close(filteredchannel)
		for {
			event, ok := <-channel
			if !ok {
				return
			}
			if event.Server.Name == server && event.Room == room {
				filteredchannel <- event
			}
		}
	}()
	return filteredchannel
}

// Filters a channel to only return the events that target our nick in a room.
func FilterSelfRoom(channel chan *Event, server ServerName, room RoomName) chan *Event {
	filteredchannel := make(chan *Event, cap(channel))
	go func() {
		defer close(filteredchannel)
		for {
			event, ok := <-channel
			if !ok {
				return
			}
			if event.Line.Nick == event.Server.Conn.Me().Nick && event.Server.Name == server && event.Room == room {
				filteredchannel <- event
			}
		}
	}()
	return filteredchannel
}

func (bot *Bot) Disconnect() {
	bot.Lock()
	defer bot.Unlock()

	for _, remover := range bot.removers {
		remover.Remove()
	}
	for _, events := range bot.events {
		events.Close()
	}
	for _, server := range bot.servers {
		server.Conn.Quit()
	}

	<-time.After(500 * time.Millisecond)
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
	joinAll := func(server *Server) {
		for _, channel := range server.Rooms {
			server.Conn.Join(string(channel))
		}
	}
	// If we're added after the servers are connected, we need to join.
	for _, server := range bot.servers {
		if server.Conn.Connected() {
			joinAll(server)
		}
	}
	channel := bot.GetEventHandler(client.CONNECTED)
	for {
		event, ok := <-channel
		if !ok {
			break
		}
		fmt.Println(event.Server.Name, "Connected")
		joinAll(event.Server)
	}
}

func DisconnectPlugin(bot *Bot) {
	channel := bot.GetEventHandler(client.DISCONNECTED)
	for {
		event, ok := <-channel
		if !ok {
			break
		}
		fmt.Println(event.Server.Name, "Disconnected")
		event.Server.Conn.Connect()
	}
}
