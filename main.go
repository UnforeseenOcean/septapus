package main

import (
	"bufio"
	"flag"
	"fmt"
	irc "github.com/fluffle/goirc/client"
	"os"
	"strings"
)

func main() {
	flag.Parse()

	bot("irc.synirc.net", "#septapus", "SeptapusTest", "Septapus", "Septapus v9")
}

func bot(host, channels, nick, ident, name string) {

	// create new IRC connection
	c := irc.SimpleClient(nick, ident, name)
	c.Config().Version = name
	c.Config().QuitMessage = "Lates"
	c.EnableStateTracking()
	c.HandleFunc(irc.CONNECTED,
		func(conn *irc.Conn, line *irc.Line) {
			channels := strings.Split(channels, ",")
			for _, channel := range channels {
				conn.Join(channel)
			}
		})

	// Set up a handler to notify of disconnect events.
	quit := make(chan bool)
	c.HandleFunc(irc.DISCONNECTED,
		func(conn *irc.Conn, line *irc.Line) { quit <- true })

	// set up a goroutine to read commands from stdin
	in := make(chan string, 4)
	reallyquit := false
	go func() {
		con := bufio.NewReader(os.Stdin)
		for {
			s, err := con.ReadString('\n')
			if err != nil {
				close(in)
				reallyquit = true
				c.Quit("")
				break
			}
			if len(s) > 2 {
				c.Raw(s[0 : len(s)-1])
			}
		}
	}()

	for !reallyquit {
		// connect to server
		if err := c.ConnectTo(host); err != nil {
			fmt.Printf("Error %v", err)
			return
		}
		// wait on quit channel
		<-quit
	}
}
