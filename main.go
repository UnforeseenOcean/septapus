package main

import (
	"bufio"
	"flag"
	"fmt"
	"math/rand"
	"os"
	"time"

	"github.com/iopred/septapus/septapus"
)

func main() {
	flag.Parse()
	rand.Seed(time.Now().UTC().UnixNano())

	bot := septapus.NewBot()
	bot.AddPlugin(septapus.NewYouTubePlugin())
	bot.AddPlugin(septapus.NewURLPlugin())
	bot.AddPlugin(&septapus.ComicPlugin{})
	bot.AddServer(septapus.NewServerSimple("synirc", "irc.synirc.net", "SeptapusTest", "Septapus", "Septapus v9", []string{"#septapus", "#septapustest"}))
	bot.AddServer(septapus.NewServerSimple("freenode", "irc.freenode.net", "SeptapusTest", "Septapus", "Septapus v9", []string{"#septapus"}))
	defer bot.Disconnect()

	quit := make(chan bool)

	// set up a goroutine to read commands from stdin
	in := make(chan string, 4)
	go func() {
		con := bufio.NewReader(os.Stdin)
		for {
			s, err := con.ReadString('\n')
			if err != nil {
				close(in)
				quit <- true
				break
			}
			if len(s) > 2 {
				fmt.Println(s[0 : len(s)-1])
			}
		}
	}()

	<-quit

	fmt.Println("Shutting down.")
}
