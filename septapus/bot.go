package septapus

import (
	"sync"
	"time"

	"github.com/fluffle/goirc/client"
	"github.com/fluffle/golog/logging"
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
	logging.InitFromFlags()
	bot := &Bot{}
	bot.AddPlugin(NewSimplePlugin(ConnectPlugin, nil))
	bot.AddPlugin(NewSimplePlugin(DisconnectPlugin, nil))
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
	bot.RLock()
	defer bot.RUnlock()

	for _, events := range bot.events {
		events.RemoveEventHandler(event)
	}
}

func (bot *Bot) BroadcastEvent(name EventName, event *Event) {
	bot.RLock()
	defer bot.RUnlock()

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

func (b *Bot) AddPlugin(plugin Plugin) {
	b.Lock()
	defer b.Unlock()

	b.plugins = append(b.plugins, plugin)
	go plugin.Init(b)
}

type Plugin interface {
	Init(bot *Bot)
}

const (
	ALL_SERVERS ServerName = "*"
	ALL_ROOMS   RoomName   = "*"
)

// The default state is that a plugin will not block events from any server or room.
// Banning a server or room will cause events from that server or room to be filtered in PluginSettings.GetEventHandler.
// Forcing a server or room will ignore any banned state.
// It is possible to not ban a server, but ban all the rooms. This will allow plugins to recieve server level events.
type PluginSettings struct {
	bannedServers map[ServerName]bool
	bannedRooms   map[ServerName]map[RoomName]bool
	forcedServers map[ServerName]bool
	forcedRooms   map[ServerName]map[RoomName]bool

	sync.RWMutex
}

func NewPluginSettings() *PluginSettings {
	s := &PluginSettings{
		bannedServers: make(map[ServerName]bool),
		bannedRooms:   make(map[ServerName]map[RoomName]bool),
		forcedServers: make(map[ServerName]bool),
		forcedRooms:   make(map[ServerName]map[RoomName]bool),
	}
	return s
}

func (s *PluginSettings) GetEventHandler(bot *Bot, event EventName) chan *Event {
	channel := bot.GetEventHandler(event)
	filteredchannel := make(chan *Event, cap(channel))
	go func() {
		defer close(filteredchannel)
		for {
			event, ok := <-channel
			if !ok {
				return
			}
			server := event.Server.Name
			room := event.Room
			if (s.IsForcedServer(server) || s.IsForcedRoom(server, room)) || !(s.IsBannedServer(server) || s.IsBannedRoom(server, room)) {
				filteredchannel <- event
			}
		}
	}()
	return filteredchannel
}

func (s *PluginSettings) IsBannedServer(server ServerName) bool {
	s.RLock()
	defer s.RUnlock()

	return (s.bannedServers[ALL_SERVERS] || s.bannedServers[server])
}

func (s *PluginSettings) isBannedRoom(server ServerName, room RoomName) bool {
	return (s.bannedRooms[server] != nil && s.bannedRooms[server][room])
}

func (s *PluginSettings) IsBannedRoom(server ServerName, room RoomName) bool {
	s.RLock()
	defer s.RUnlock()

	return (s.isBannedRoom(ALL_SERVERS, ALL_ROOMS) || s.isBannedRoom(ALL_SERVERS, room) || s.isBannedRoom(server, ALL_ROOMS) || s.isBannedRoom(server, room))
}

func (s *PluginSettings) IsForcedServer(server ServerName) bool {
	s.RLock()
	defer s.RUnlock()

	return (s.forcedServers[ALL_SERVERS] || s.forcedServers[server])
}

func (s *PluginSettings) isForcedRoom(server ServerName, room RoomName) bool {
	return (s.forcedRooms[server] != nil && s.forcedRooms[server][room])
}

func (s *PluginSettings) IsForcedRoom(server ServerName, room RoomName) bool {
	s.RLock()
	defer s.RUnlock()

	return (s.isForcedRoom(ALL_SERVERS, ALL_ROOMS) || s.isForcedRoom(ALL_SERVERS, room) || s.isForcedRoom(server, ALL_ROOMS) || s.isForcedRoom(server, room))
}

func (s *PluginSettings) AddBannedServer(server ServerName) {
	s.Lock()
	defer s.Unlock()

	s.bannedServers[server] = true
}

func (s *PluginSettings) AddBannedRoom(server ServerName, room RoomName) {
	s.Lock()
	defer s.Unlock()

	if s.bannedRooms[server] == nil {
		s.bannedRooms[server] = make(map[RoomName]bool)
	}
	s.bannedRooms[server][room] = true
}

func (s *PluginSettings) AddForcedServer(server ServerName) {
	s.Lock()
	defer s.Unlock()

	s.forcedServers[server] = true
}

func (s *PluginSettings) AddForcedRoom(server ServerName, room RoomName) {
	s.Lock()
	defer s.Unlock()

	if s.forcedRooms[server] == nil {
		s.forcedRooms[server] = make(map[RoomName]bool)
	}
	s.forcedRooms[server][room] = true
}

var DefaultSettings *PluginSettings = NewPluginSettings()

type SimplePluginInit func(bot *Bot, settings *PluginSettings)

type SimplePlugin struct {
	settings *PluginSettings
	init     SimplePluginInit
}

func NewSimplePlugin(init SimplePluginInit, settings *PluginSettings) Plugin {
	if settings == nil {
		settings = DefaultSettings
	}
	return &SimplePlugin{
		settings: settings,
		init:     init,
	}
}

func (plugin *SimplePlugin) Init(bot *Bot) {
	plugin.init(bot, plugin.settings)
}

func ConnectPlugin(bot *Bot, settings *PluginSettings) {
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
		logging.Info("Connected to", event.Server.Name)
		joinAll(event.Server)
	}
}

func DisconnectPlugin(bot *Bot, settings *PluginSettings) {
	channel := bot.GetEventHandler(client.DISCONNECTED)
	for {
		event, ok := <-channel
		if !ok {
			break
		}
		logging.Info("Disconnected from", event.Server.Name)
		event.Server.Conn.Connect()
	}
}
