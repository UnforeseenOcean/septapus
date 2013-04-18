package septapus

import (
	"encoding/json"
	"fmt"
	client "github.com/fluffle/goirc/client"
	"html"
	"io/ioutil"
	"net/http"
	"regexp"
	"strings"
)

type youTubeVideo struct {
	Entry struct {
		Info struct {
			Title struct {
				Text string `json:"$t"`
			} `json:"media$title"`
			Description struct {
				Text string `json:"$t"`
			} `json:"media$description"`
		} `json:"media$group"`
		Rating struct {
			Likes    string `json:"numLikes"`
			Dislikes string `json:"numDislikes"`
		} `json:"yt$rating"`
		Statistics struct {
			Views string `json:"viewCount"`
		} `json:"yt$statistics"`
	} `json:entry`
}

const (
	UrlRegex     string = `(\s|^)(http://|https://|www.)(.*?)(\s|$)`
	YouTubeRegex string = `(\s|^)(http://|https://)?(www.)?(youtube.com/watch\?v=|youtu.be/)(.*?)(\s|$|\&|#)`
)

func isYouTubeURL(text string) []string {
	if regex, err := regexp.Compile(YouTubeRegex); err == nil {
		if regex.MatchString(text) {
			return regex.FindStringSubmatch(text)
		}
	}
	return nil
}

func isUrl(text string) string {
	if regex, err := regexp.Compile(UrlRegex); err == nil {
		url := strings.TrimSpace(regex.FindString(text))
		if url != "" {
			if strings.Index(url, "http") != 0 {
				url = "http://" + url
			}
			return url
		}
	}
	return ""
}

func NewYouTubePlugin() Plugin {
	return NewSimplePlugin(YouTubePlugin)
}

func YouTubePlugin(bot *Bot) {
	channel := bot.GetAllEventHandler(client.PRIVMSG)
	for {
		event, ok := <-channel
		if !ok {
			break
		}
		matches := isYouTubeURL(event.Line.Text())
		if matches != nil {
			id := matches[len(matches)-2]
			url := fmt.Sprintf("https://gdata.youtube.com/feeds/api/videos/%s?v=2&alt=json", id)
			if resp, err := http.Get(url); err == nil {
				defer resp.Body.Close()
				if contents, err := ioutil.ReadAll(resp.Body); err == nil {
					var data youTubeVideo
					if err := json.Unmarshal(contents, &data); err == nil {
						event.Server.Conn.Privmsg(event.Line.Target(), fmt.Sprintf("%s - %s views (%s likes, %s dislikes)", data.Entry.Info.Title.Text, data.Entry.Statistics.Views, data.Entry.Rating.Likes, data.Entry.Rating.Dislikes))
					}
				}
			}
		}
	}
}

func NewURLPlugin() Plugin {
	return NewSimplePlugin(URLPlugin)
}

func URLPlugin(bot *Bot) {
	channel := bot.GetAllEventHandler(client.PRIVMSG)
	for {
		event, ok := <-channel
		if !ok {
			break
		}
		url := isUrl(event.Line.Text())
		if url != "" {
			if isYouTubeURL(url) == nil {
				if resp, err := http.Get(url); err == nil {
					defer resp.Body.Close()
					if strings.HasPrefix(resp.Header.Get("Content-Type"), "text/html") {
						if content, err := ioutil.ReadAll(resp.Body); err == nil {
							contents := string(content)
							contents = html.UnescapeString(strings.Replace(contents, "\n", "", -1))
							if regex, err := regexp.Compile(`<title>(.*?)</title>`); err == nil {
								if regex.MatchString(contents) {
									event.Server.Conn.Privmsg(event.Line.Target(), strings.TrimSpace(regex.FindStringSubmatch(contents)[1]))
								}
							}
						}
					}
				}
			}
		}
	}
}
