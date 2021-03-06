package septapus

import (
	"bufio"
	"bytes"
	"flag"
	"image"
	"image/color"
	"image/draw"
	_ "image/jpeg"
	"image/png"
	"io"
	"io/ioutil"
	"math"
	"math/rand"
	"mime/multipart"
	"net/http"
	"os"
	"regexp"
	"strings"
	"time"

	"code.google.com/p/draw2d/draw2d"
	"code.google.com/p/freetype-go/freetype/raster"
	"code.google.com/p/freetype-go/freetype/truetype"
	"github.com/fluffle/goirc/client"
	"github.com/fluffle/golog/logging"
)

var comickey = flag.String("comickey", "", "Private key for uploading comics")
var comicurl = flag.String("comicurl", "http://septapus.com/comics/comics.php", "Url to upload the generated comics")
var comicallowrepeats = flag.Bool("comicallowrepeats", false, "Can one person laugh repeatedly to trigger comic.")

const (
	arrowHeight float64 = 5
	laughRegex  string  = `(?i)\b((o*lo+l(l|o)*)|(ro+fl(l|o)*e*)|(b*a*h(h|a)+(h|a)+)|(e*he(h|e)+)|(e*ke(k|e)+)|lf*mao+)\b`
)

const (
	TEXT_ALIGN_LEFT int = iota
	TEXT_ALIGN_CENTER
	TEXT_ALIGN_RIGHT
)

type ComicPlugin struct {
	avatars   []image.Image
	renderers []CellRenderer
	settings  *PluginSettings
	fontData  *draw2d.FontData
}

func init() {
	draw2d.SetFontFolder("fonts")
}

func NewComicPlugin(settings *PluginSettings) *ComicPlugin {
	if settings == nil {
		settings = DefaultSettings
	}
	return &ComicPlugin{settings: settings}
}

type Speaker int
type Text string

type Message struct {
	Speaker Speaker
	Text    Text
}

type Script struct {
	Messages []*Message
	Room     RoomName
}

func (comic *ComicPlugin) Init(bot *Bot) {
	joinchan := FilterSelf(comic.settings.GetEventHandler(bot, client.JOIN))
	scriptchan := make(chan *Script, 100)
	defer close(scriptchan)
	comicchan := make(chan image.Image, 100)
	defer close(comicchan)

	var avatarFiles []os.FileInfo
	var err error
	if avatarFiles, err = ioutil.ReadDir("avatars"); err != nil {
		logging.Error("Could not open avatars directory.")
		return
	}

	avatars := make([]image.Image, 0)
	for _, avatarFile := range avatarFiles {
		if avatarFile.IsDir() {
			continue
		}
		if file, err := os.Open("avatars/" + avatarFile.Name()); err == nil {
			if avatar, _, err := image.Decode(bufio.NewReader(file)); err == nil {
				avatars = append(avatars, avatar)
			}
		}
	}
	comic.avatars = avatars

	comic.renderers = []CellRenderer{
		&OneSpeakerCellRenderer{},
		&FlippedOneSpeakerCellRenderer{},
		&OneSpeakerMonologueCellRenderer{},
		&TwoSpeakerCellRenderer{},
	}

	comic.fontData = &draw2d.FontData{"DigitalStrip2BB", draw2d.FontFamilySans, draw2d.FontStyleNormal}

	for {
		select {
		case script := <-scriptchan:
			go comic.makeComic(comicchan, script.Messages, script.Room)
		case image := <-comicchan:
			go comic.uploadComic(image)
		case event, ok := <-joinchan:
			if !ok {
				return
			}
			go comic.makeScripts(scriptchan, bot, event.Server, RoomName(event.Line.Target()))
		}
	}
}

func isLaugh(text string) bool {
	if regex, err := regexp.Compile(laughRegex); err == nil {
		return regex.MatchString(strings.ToLower(text))
	}
	return false
}

func stripLaugh(text string) string {
	if regex, err := regexp.Compile(laughRegex); err == nil {
		return strings.TrimSpace(regex.ReplaceAllString(text, ""))
	}
	return text
}

func randomLaugh() string {
	r := rand.Float32()
	if r < 0.25 {
		return "lol"
	} else if r < 0.5 {
		return "haha"
	} else if r < 0.75 {
		return "hehe"
	}
	return "rofl"
}

func (comic *ComicPlugin) makeScripts(scriptchan chan *Script, bot *Bot, server *Server, room RoomName) {
	logging.Info("Creating comics in", server.Name, room)
	defer logging.Info("Stopped creating comics in", server.Name, room)

	// If we have heard this event, we can assume that we should be listenening to this room, don't filter through settings.
	disconnectchan := bot.GetEventHandler(client.DISCONNECTED)
	partchan := FilterSelfRoom(bot.GetEventHandler(client.PART), server.Name, room)
	messagechan := FilterRoom(bot.GetEventHandler(client.PRIVMSG), server.Name, room)

	var (
		script    []*Message
		speakers  map[string]Speaker
		avatars   map[Speaker]bool
		speaker   Speaker
		laughs    int
		lastLaugh string
		timeout   bool
	)

	reset := func() {
		script = nil
		speakers = make(map[string]Speaker)
		avatars = make(map[Speaker]bool)
		laughs = 0
		lastLaugh = ""
		timeout = false
	}
	reset()
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
			text := event.Line.Text()
			if strings.HasPrefix(event.Line.Text(), "!") {
				reset()
				break
			} else if isUrl(text) != "" {
				reset()
				break
			} else if isLaugh(text) {
				if lastLaugh != event.Line.Nick || *comicallowrepeats {
					justLaugh := stripLaugh(text) == ""

					lastLaugh = event.Line.Nick
					if laughs <= 0 {
						if justLaugh {
							laughs = 2
						} else {
							laughs = 1
						}
					} else {
						laughs++
					}

					if laughs > 3 {
						server.Conn.Privmsg(string(room), randomLaugh())
						scriptchan <- &Script{script, room}
						reset()
						break
					}
				}
				break
			} else {
				if laughs > 0 {
					laughs--
					timeout = false
				} else if timeout {
					reset()
				}
			}
			if _, ok := speakers[event.Line.Nick]; !ok {
				for {
					speaker = Speaker(rand.Intn(len(comic.avatars)))
					if _, ok := avatars[speaker]; !ok {
						avatars[speaker] = true
						break
					}
				}
				speakers[event.Line.Nick] = speaker
			} else {
				speaker = speakers[event.Line.Nick]
			}

			script = append(script, &Message{speaker, Text(text)})
		case <-time.After(5 * time.Minute):
			timeout = true
		}
	}
}

func (comic *ComicPlugin) makeComic(comicchan chan image.Image, script []*Message, room RoomName) {
	// Our plan can only be 3 panels long
	maxComicLength := 3

	// Determine the longest script possible
	maxLines := 0
	for _, renderer := range comic.renderers {
		if renderer.Lines() > maxLines {
			maxLines = renderer.Lines()
		}
	}
	maxLines *= maxComicLength

	if len(script) > maxLines {
		logging.Info("Script is too long, trimming")
		script = script[len(script)-maxLines:]
	}

	// Create all plans that are sufficient, and pick a random one.
	plans := make([][]CellRenderer, 0)
	planchan := make(chan []CellRenderer, len(comic.renderers)*len(comic.renderers))
	go createPlans(planchan, comic.renderers, maxComicLength, make([]CellRenderer, 0), script, 0)
	for {
		plan, ok := <-planchan
		if !ok || plan == nil {
			break
		}
		plans = append(plans, plan)
	}

	if len(plans) == 0 {
		logging.Error("No plans available to render script:", script)
		return
	}
	plan := plans[rand.Intn(len(plans))]

	width := len(plan)*240 - 10

	// Initialize the context.
	rgba := image.NewRGBA(image.Rect(0, 0, width, 225))
	draw.Draw(rgba, rgba.Bounds(), image.White, image.ZP, draw.Src)

	gc := draw2d.NewGraphicContext(rgba)
	gc.SetDPI(72)
	gc.SetFontData(*comic.fontData)

	for i, c := 0, 0; i < len(plan); i++ {
		renderer := plan[i]
		renderer.Render(gc, comic.avatars, script[c:c+renderer.Lines()], 5+240*float64(i), 5, 220, 200)
		c += renderer.Lines()
	}
	DrawTextInRect(gc, color.RGBA{0xdd, 0xdd, 0xdd, 0xff}, TEXT_ALIGN_RIGHT, 0.8, "A comic by Septapus ("+string(room)+")", 0, 5, 205, float64(width-10), 20)

	comicchan <- rgba
}

func (comic *ComicPlugin) uploadComic(image image.Image) {
	file, err := os.Create("comic.png")
	defer file.Close()
	if err != nil {
		logging.Error("Error creating file:", err)
		return
	}

	filewriter := bufio.NewWriter(file)

	b := &bytes.Buffer{}

	w := multipart.NewWriter(b)
	defer w.Close()

	if err = w.WriteField("key", *comickey); err != nil {
		logging.Error("Error creating key:", err)
		return
	}

	formfile, err := w.CreateFormFile("comic", "comic.png")
	if err != nil {
		logging.Error("Error creating form file:", err)
		return
	}

	if err = png.Encode(io.MultiWriter(filewriter, formfile), image); err != nil {
		logging.Error("Error encoding PNG:", err)
		return
	}

	if err = filewriter.Flush(); err != nil {
		logging.Error("Error flushing to disk:", err)
		return
	}
	logging.Info("Wrote comic to disk")

	w.Close()

	if resp, err := http.Post(*comicurl, w.FormDataContentType(), b); err != nil {
		logging.Error("Error posting comic to server:", err)
		return
	} else {
		defer resp.Body.Close()
	}
}

func countSpeakers(script []*Message, lines int) int {
	seenMap := make(map[Speaker]bool)
	for i := 0; i < lines; i++ {
		seenMap[script[i].Speaker] = true
	}
	return len(seenMap)
}

func createPlans(planchan chan []CellRenderer, renderers []CellRenderer, comicLength int, currentPlan []CellRenderer, remainingScript []*Message, currentLength int) {
	if currentLength > comicLength {
		return
	} else if len(remainingScript) == 0 {
		planchan <- currentPlan
		return
	}
	for _, renderer := range renderers {
		lines := renderer.Lines()
		if lines <= len(remainingScript) {
			if renderer.Speakers() == countSpeakers(remainingScript[:lines], lines) {
				createPlans(planchan, renderers, comicLength, append(currentPlan, renderer), remainingScript[lines:], currentLength+1)
			}
		}
	}
	if currentLength == 0 {
		planchan <- nil
	}
}

func DrawSpeech(gc *draw2d.ImageGraphicContext, border, radius, x, y, width, height, pointX, pointY float64) {
	gc.Save()
	color := color.Black
	gc.SetLineCap(draw2d.RoundCap)
	gc.SetLineJoin(draw2d.RoundJoin)
	gc.SetLineWidth(border * 2)
	gc.SetStrokeColor(color)
	gc.SetFillColor(color)

	gc.MoveTo(x+radius, y)
	gc.LineTo(x+width-radius, y)
	// top right corner
	gc.QuadCurveTo(x+width, y, x+width, y+radius)
	gc.LineTo(x+width, y+height-radius)
	// botttom right corner
	gc.QuadCurveTo(x+width, y+height, x+width-radius, y+height)
	gc.LineTo(x+radius, y+height)
	// bottom left corner
	gc.QuadCurveTo(x, y+height, x, y+height-radius)
	gc.LineTo(x, y+radius)
	// top left corner
	gc.QuadCurveTo(x, y, x+radius, y)
	// save the bubble area, stroke it, then save it again (so it can be filled with white)
	gc.Save()
	gc.FillStroke()
	gc.Restore()
	gc.Save()

	cx := x + width/2
	cy := y + height/2

	dx := pointX - cx
	dy := pointY - cy

	d := float64(math.Sqrt(dx*dx + dy*dy))

	nx := dx / d
	ny := dy / d

	var r float64
	if width > height {
		r = height / 2
	} else {
		r = width / 2
	}
	r *= 0.9

	sx := cx + r*nx
	sy := cy + r*ny

	arrowWidth := d * 0.2

	gc.MoveTo(pointX, pointY)
	gc.LineTo(sx+ny*arrowWidth, sy+-nx*arrowWidth)
	gc.LineTo(sx+-ny*arrowWidth, sy+nx*arrowWidth)
	gc.LineTo(pointX, pointY)

	// Save the arrow, then fill it with the outline color
	gc.Save()
	gc.FillStroke()
	gc.Restore()

	// Finally draw the arrow in white, then restore back to our bubble, draw it in white
	gc.SetFillColor(image.White)
	gc.Fill()
	gc.Restore()
	gc.SetFillColor(image.White)
	gc.Fill()

	gc.Restore()
}

func DrawTextInRect(gc *draw2d.ImageGraphicContext, color color.Color, align int, spacing float64, text string, border, x, y, width, height float64) {
	gc.Save()
	gc.SetStrokeColor(color)
	gc.SetFillColor(color)

	wrapText, fontSize, _, textHeight := Fit(float64(gc.GetDPI()), draw2d.GetFont(gc.GetFontData()), spacing, text, width-border*2, height-border*2)
	gc.SetFontSize(fontSize)

	center := (height - textHeight) / 2

	// Draw the text.
	lines := strings.Split(wrapText, "\n")
	for i, line := range lines {
		textWidth, _, _ := Bounds(float64(gc.GetDPI()), draw2d.GetFont(gc.GetFontData()), gc.GetFontSize(), spacing, line)
		var px float64
		switch align {
		case TEXT_ALIGN_LEFT:
			px = x + border
		case TEXT_ALIGN_CENTER:
			px = x + (width-textWidth)/2
		case TEXT_ALIGN_RIGHT:
			px = width - textWidth - border
		}
		py := y + center + fontSize*0.8 + fontSize*spacing*(float64(i))

		gc.MoveTo(px, py)
		gc.FillString(line)
	}
	gc.Restore()
}

func Bounds(dpi float64, font *truetype.Font, fontSize, spacing float64, text string) (width, height float64, err error) {
	var maxWidth float64
	height = fontSize
	scale := int32(fontSize * dpi * (64.0 / 72.0))
	prev, hasPrev := truetype.Index(0), false
	for _, rune := range text {
		if rune == '\n' {
			prev, hasPrev = truetype.Index(0), false
			width = 0
			height += fontSize * spacing
			continue
		}
		index := font.Index(rune)
		if hasPrev {
			fixedWidth := raster.Fix32(font.Kerning(scale, prev, index)) << 2
			width += float64(fixedWidth) / 256
			if width > maxWidth {
				maxWidth = width
			}
		}
		fixedWidth := raster.Fix32(font.HMetric(scale, index).AdvanceWidth) << 2
		width += float64(fixedWidth) / 256
		if width > maxWidth {
			maxWidth = width
		}
		prev, hasPrev = index, true
	}
	return maxWidth, height, nil
}

func Fit(dpi float64, font *truetype.Font, spacing float64, text string, width, height float64) (wrapText string, fontSize, wrapWidth, wrapHeight float64) {
	// Match aspect ratios, favoring width.
	aspect := width / height
	for low, high := 1.0, 100.0; high > low+0.1; {
		fontSize = low + (high-low)/2
		wrapText, _ = WrapText(dpi, font, fontSize, spacing, text, width)
		wrapWidth, wrapHeight, _ = Bounds(dpi, font, fontSize, spacing, wrapText)
		newTextAspect := wrapWidth / wrapHeight
		if newTextAspect > aspect {
			low = fontSize
		} else if newTextAspect < aspect {
			high = fontSize
		}
	}
	// Scale the contents to fit the window (as its possible the font size is too large, but satisfies the aspect ratio better)
	scale := width / wrapWidth
	if wrapHeight*scale > height {
		scale = height / wrapHeight
	}
	fontSize *= scale
	wrapWidth *= scale
	wrapHeight *= scale
	return
}

func WrapText(dpi float64, font *truetype.Font, fontSize, spacing float64, text string, wrapWidth float64) (string, float64) {
	var buffer bytes.Buffer
	var maxWidth float64
	lines := strings.Split(text, "\n")
	for i, line := range lines {
		width := wrapLine(&buffer, dpi, font, fontSize, spacing, line, wrapWidth)
		if width > maxWidth {
			maxWidth = width
		}
		if i < len(lines)-1 {
			buffer.WriteString("\n")
		}
	}
	return buffer.String(), maxWidth
}

func wrapLine(buffer *bytes.Buffer, dpi float64, font *truetype.Font, fontSize, spacing float64, line string, wrapWidth float64) float64 {
	var width float64
	var runningWidth float64
	var maxWidth float64
	words := strings.Split(line, " ")
	for i, word := range words {
		if i != 0 {
			width, _, _ = Bounds(dpi, font, fontSize, spacing, " "+word)
		} else {
			width, _, _ = Bounds(dpi, font, fontSize, spacing, word)
		}
		if width > maxWidth {
			maxWidth = width
		}
		runningWidth += width
		if runningWidth >= wrapWidth && i != 0 {
			runningWidth = width
			buffer.WriteString("\n")
		} else if i != 0 {
			buffer.WriteString(" ")
		}
		buffer.WriteString(word)
	}
	return maxWidth
}

func InsetRectangle(x, y, width, height, inset float64) (float64, float64, float64, float64) {
	return InsetRectangle2(x, y, width, height, inset, inset)
}

func InsetRectangle2(x, y, width, height, horizontal, vertical float64) (float64, float64, float64, float64) {
	return InsetRectangle4(x, y, width, height, horizontal, horizontal, vertical, vertical)
}

func InsetRectangle4(x, y, width, height, left, right, top, bottom float64) (float64, float64, float64, float64) {
	return x + left, y + top, width - left - right, height - top - bottom
}

type CellRenderer interface {
	// The number of text lines this Cell will render
	Lines() int
	// The number of speakers that this Cell will render. If the number of speakers is one, all lines will be spoken by the same speaker, otherwise it can be any number of speakers.
	Speakers() int
	Render(gc *draw2d.ImageGraphicContext, avatars []image.Image, messages []*Message, x, y, width, height float64)
}

type Outliner struct{}

func (c *Outliner) Outline(gc *draw2d.ImageGraphicContext, x, y, width, height float64) {
	gc.Save()
	color := color.RGBA{0xdd, 0xdd, 0xdd, 0xff}
	gc.SetLineCap(draw2d.RoundCap)
	gc.SetLineJoin(draw2d.RoundJoin)
	gc.SetLineWidth(2)
	gc.SetStrokeColor(color)
	gc.MoveTo(x, y)
	gc.LineTo(x+width, y)
	gc.LineTo(x+width, y+height)
	gc.LineTo(x, y+height)
	gc.LineTo(x, y)
	gc.Stroke()
	gc.Restore()
}

type OneSpeakerCellRenderer struct {
	Outliner
}

func (c *OneSpeakerCellRenderer) Lines() int {
	return 1
}

func (c *OneSpeakerCellRenderer) Speakers() int {
	return 1
}

func (c *OneSpeakerCellRenderer) Render(gc *draw2d.ImageGraphicContext, avatars []image.Image, messages []*Message, x, y, width, height float64) {
	c.Outline(gc, x, y, width, height)

	if len(messages) != c.Lines() {
		return
	}

	border := float64(5)

	avatar := avatars[messages[0].Speaker]
	bounds := avatar.Bounds()
	gc.SetMatrixTransform(draw2d.NewTranslationMatrix(x+border, y+height-border-float64(bounds.Dy())))
	gc.DrawImage(avatar)
	gc.SetMatrixTransform(draw2d.NewIdentityMatrix())

	bX, bY, bWidth, bHeight := InsetRectangle4(x, y, width, height, border, border, border, border+float64(bounds.Dy())+arrowHeight*2)

	DrawSpeech(gc, 2, border, bX, bY, bWidth, bHeight, bX+rand.Float64()*float64(bounds.Dx()), bY+bHeight+arrowHeight)
	DrawTextInRect(gc, image.Black, TEXT_ALIGN_CENTER, 0.8, string(messages[0].Text), arrowHeight, bX, bY, bWidth, bHeight)
}

type FlippedOneSpeakerCellRenderer struct {
	Outliner
}

func (c *FlippedOneSpeakerCellRenderer) Lines() int {
	return 1
}

func (c *FlippedOneSpeakerCellRenderer) Speakers() int {
	return 1
}

func (c *FlippedOneSpeakerCellRenderer) Render(gc *draw2d.ImageGraphicContext, avatars []image.Image, messages []*Message, x, y, width, height float64) {
	c.Outline(gc, x, y, width, height)

	if len(messages) != c.Lines() {
		return
	}

	border := float64(5)

	avatar := avatars[messages[0].Speaker]
	bounds := avatar.Bounds()
	gc.SetMatrixTransform(draw2d.NewTranslationMatrix(x+border, y+border))
	gc.DrawImage(avatar)
	gc.SetMatrixTransform(draw2d.NewIdentityMatrix())

	bX, bY, bWidth, bHeight := InsetRectangle4(x, y, width, height, border, border, border+float64(bounds.Dy())+arrowHeight*2, border)

	DrawSpeech(gc, 2, border, bX, bY, bWidth, bHeight, bX+rand.Float64()*float64(bounds.Dx()), bY-arrowHeight)
	DrawTextInRect(gc, image.Black, TEXT_ALIGN_CENTER, 0.8, string(messages[0].Text), arrowHeight, bX, bY, bWidth, bHeight)
}

type TwoSpeakerCellRenderer struct {
	Outliner
}

func (c *TwoSpeakerCellRenderer) Lines() int {
	return 2
}

func (c *TwoSpeakerCellRenderer) Speakers() int {
	return 2
}

func (c *TwoSpeakerCellRenderer) Render(gc *draw2d.ImageGraphicContext, avatars []image.Image, messages []*Message, x, y, width, height float64) {
	c.Outline(gc, x, y, width, height)

	if len(messages) != c.Lines() {
		return
	}

	border := float64(5)
	flipped := rand.Float64() >= 0.5
	// get a rectangle for half the area
	aX, aY, aWidth, aHeight := InsetRectangle4(x, y, width, height, 0, 0, 0, height/2)
	for i := 0; i < 2; i++ {

		avatar := avatars[messages[i].Speaker]
		bounds := avatar.Bounds()

		if flipped {
			gc.SetMatrixTransform(draw2d.NewTranslationMatrix(aX+aWidth-border-float64(bounds.Dx()), aY+aHeight-border-float64(bounds.Dy())))
		} else {
			gc.SetMatrixTransform(draw2d.NewTranslationMatrix(aX+border, aY+border))
		}
		gc.DrawImage(avatar)
		gc.SetMatrixTransform(draw2d.NewIdentityMatrix())

		bX, bY, bWidth, bHeight := InsetRectangle4(aX, aY, aWidth, aHeight, border, border+float64(bounds.Dx())+arrowHeight*3, border, border)

		if !flipped {
			bX += aWidth - bWidth - (bX - x) - border
		}

		arrowX := -arrowHeight * 2
		if flipped {
			arrowX = bWidth + arrowHeight*2
		}

		DrawSpeech(gc, 2, border, bX, bY, bWidth, bHeight, bX+arrowX, bY+rand.Float64()*float64(bounds.Dx()))
		DrawTextInRect(gc, image.Black, TEXT_ALIGN_CENTER, 0.8, string(messages[i].Text), 10, bX, bY, bWidth, bHeight)

		flipped = !flipped
		aY += aHeight
	}
}

type OneSpeakerMonologueCellRenderer struct {
	Outliner
}

func (c *OneSpeakerMonologueCellRenderer) Lines() int {
	return 2
}

func (c *OneSpeakerMonologueCellRenderer) Speakers() int {
	return 1
}

func (c *OneSpeakerMonologueCellRenderer) Render(gc *draw2d.ImageGraphicContext, avatars []image.Image, messages []*Message, x, y, width, height float64) {
	c.Outline(gc, x, y, width, height)

	if len(messages) != c.Lines() {
		return
	}

	border := float64(5)

	avatar := avatars[messages[0].Speaker]
	bounds := avatar.Bounds()
	gc.SetMatrixTransform(draw2d.NewTranslationMatrix(x+border, y+height-border-float64(bounds.Dy())))
	gc.DrawImage(avatar)
	gc.SetMatrixTransform(draw2d.NewIdentityMatrix())

	bX, bY, bWidth, bHeight := InsetRectangle4(x, y, width, height, border, border, border, border+float64(bounds.Dy())+arrowHeight*2)

	DrawSpeech(gc, 2, border, bX, bY, bWidth, bHeight, bX+rand.Float64()*float64(bounds.Dx()), bY+bHeight+arrowHeight)
	DrawTextInRect(gc, image.Black, TEXT_ALIGN_CENTER, 0.8, string(messages[0].Text), arrowHeight, bX, bY, bWidth, bHeight)

	bX, bY, bWidth, bHeight = InsetRectangle4(x, y, width, height, border+float64(bounds.Dx())+arrowHeight*3, border, y+height-border*2-float64(bounds.Dy()), border)

	DrawSpeech(gc, 2, border, bX, bY, bWidth, bHeight, bX-arrowHeight*2, bY+rand.Float64()*float64(bounds.Dy()))
	DrawTextInRect(gc, image.Black, TEXT_ALIGN_CENTER, 0.8, string(messages[1].Text), arrowHeight, bX, bY, bWidth, bHeight)

}
