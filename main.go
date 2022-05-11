package main

import (
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/gif"
	"io/ioutil"
	"log"
	"math"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/fogleman/gg"
	"github.com/gonutz/framebuffer"
	_ "github.com/kubesail/pibox-framebuffer/statik"
	"github.com/rakyll/statik/fs"

	"github.com/shirou/gopsutil/cpu"
	"github.com/shirou/gopsutil/mem"
	qrcode "github.com/skip2/go-qrcode"
	"github.com/stianeikeland/go-rpio/v4"
)

var fbNum string
var statsOff = false

const SCREEN_SIZE = 240

type RGB struct {
	R uint8
	G uint8
	B uint8
}

func rgb(w http.ResponseWriter, req *http.Request) {

	var c RGB
	err := json.NewDecoder(req.Body).Decode(&c)
	if err != nil {
		http.Error(w, "Requires json body with R, G, and B keys! Values must be 0-255\n", http.StatusBadRequest)
		return
	}

	drawSolidColor(c)
	fmt.Fprintf(w, "parsed color: R%v G%v B%v\n", c.R, c.G, c.B)
	fmt.Fprintf(w, "wrote to framebuffer!\n")
}

func drawSolidColor(c RGB) {
	fb, err := framebuffer.Open("/dev/" + fbNum)
	if err != nil {
		panic(err)
	}
	defer fb.Close()
	magenta := image.NewUniform(color.RGBA{c.R, c.G, c.B, 255})
	draw.Draw(fb, fb.Bounds(), magenta, image.Point{}, draw.Src)
	statsOff = true
}

func qr(w http.ResponseWriter, req *http.Request) {
	query := req.URL.Query()
	content, present := query["content"]
	if !present {
		http.Error(w, "Pass ?content= to render a QR code\n", http.StatusBadRequest)
		return
	}

	fb, err := framebuffer.Open("/dev/" + fbNum)
	if err != nil {
		panic(err)
	}
	defer fb.Close()

	// var q qrcode.QRCode
	q, err := qrcode.New(strings.Join(content, ""), qrcode.Low)
	q.DisableBorder = true
	// q.ForegroundColor = color.RGBA{236, 57, 99, 255}
	if err != nil {
		panic(err)
	}
	// var qr image.Image
	img := q.Image(180)

	draw.Draw(fb,
		image.Rectangle{Min: image.Point{X: 30, Y: 47}, Max: image.Point{X: 210, Y: 227}},
		img,
		image.Point{},
		draw.Src)

	fmt.Println("QR Code printed to screen")
	statsOff = true
}

func textRequest(w http.ResponseWriter, req *http.Request) {
	query := req.URL.Query()
	content := query["content"]
	if len(content) == 0 {
		content = append(content, "no content param")
	}
	size := query["size"]
	if len(size) == 0 {
		size = append(size, "22")
	}
	sizeInt, _ := strconv.Atoi(size[0])
	x := query["x"]
	xInt := SCREEN_SIZE / 2
	if len(x) > 0 {
		xInt, _ = strconv.Atoi(x[0])
	}
	y := query["y"]
	yInt := SCREEN_SIZE / 2
	if len(y) > 0 {
		yInt, _ = strconv.Atoi(y[0])
	}
	dc := gg.NewContext(SCREEN_SIZE, SCREEN_SIZE)
	dc.DrawRectangle(0, 0, 240, 240)
	dc.SetColor(color.RGBA{51, 51, 51, 255})
	dc.Fill()
	textOnContext(dc, float64(xInt), float64(yInt), float64(sizeInt), content[0], RGB{R: 0, G: 0, B: 0}, true, gg.AlignCenter)
	flushTextToScreen(dc)
	statsOff = true
}

func textOnContext(dc *gg.Context, x float64, y float64, size float64, content string, c RGB, bold bool, align gg.Align) {
	const S = 240
	// dc.SetRGB(float64(c.R), float64(c.G), float64(c.B))
	if bold {
		if err := dc.LoadFontFace("/usr/share/fonts/truetype/piboto/Piboto-Bold.ttf", float64(size)); err != nil {
			panic(err)
		}
	} else {
		if err := dc.LoadFontFace("/usr/share/fonts/truetype/piboto/Piboto-Regular.ttf", float64(size)); err != nil {
			panic(err)
		}
	}

	dc.SetColor(color.RGBA{c.R, c.G, c.B, 255})
	dc.DrawStringWrapped(content, x, y, 0.5, 0.5, 240, 1.5, align)
	// dc.Clip()
}

func flushTextToScreen(dc *gg.Context) {
	fb, err := framebuffer.Open("/dev/" + fbNum)
	if err != nil {
		panic(err)
	}
	draw.Draw(fb, fb.Bounds(), dc.Image(), image.Point{}, draw.Src)
}

func drawImage(w http.ResponseWriter, req *http.Request) {
	fb, err := framebuffer.Open("/dev/" + fbNum)
	if err != nil {
		panic(err)
	}
	defer fb.Close()
	img, _, err := image.Decode(req.Body)
	if err != nil {
		panic(err)
	}
	draw.Draw(fb, fb.Bounds(), img, image.Point{}, draw.Src)
	fmt.Fprintf(w, "Image drawn\n")
	statsOff = true
}

func drawGIF(w http.ResponseWriter, req *http.Request) {
	fb, err := framebuffer.Open("/dev/" + fbNum)
	if err != nil {
		panic(err)
	}
	defer fb.Close()
	imgGif, err := gif.DecodeAll(req.Body)
	if err != nil {
		panic(err)
	}
	for i, frame := range imgGif.Image {
		draw.Draw(fb, fb.Bounds(), frame, image.Point{}, draw.Src)
		time.Sleep(time.Millisecond * 3 * time.Duration(imgGif.Delay[i]))
	}
	fmt.Fprintf(w, "GIF drawn\n")
	statsOff = true
}

func exit(w http.ResponseWriter, req *http.Request) {
	fmt.Println("Received exit request, shutting down...")
	c := RGB{R: 0, G: 0, B: 255}
	drawSolidColor(c)
	os.Exit(0)
}

func setFramebuffer() {
	items, _ := ioutil.ReadDir("/sys/class/graphics")
	for _, item := range items {
		data, err := ioutil.ReadFile("/sys/class/graphics/" + item.Name() + "/name")
		if item.Name() == "fbcon" {
			continue
		}
		if err != nil {
			log.Fatalf("Could not enumerate framebuffers %v", err)
			return
		}
		if string(data) == "fb_st7789v\n" {
			fbNum = item.Name()
			fmt.Println("Displaying on " + fbNum)
		}
	}
}

func splash() {
	fb, err := framebuffer.Open("/dev/" + fbNum)
	if err != nil {
		panic(err)
	}
	statikFS, err := fs.New()
	if err != nil {
		panic(err)
	}
	r, err := statikFS.Open("/pibox-splash.png")
	if err != nil {
		panic(err)
	}
	img, _, err := image.Decode(r)
	if err != nil {
		panic(err)
	}
	draw.Draw(fb, fb.Bounds(), img, image.ZP, draw.Src)
	dc := gg.NewContext(SCREEN_SIZE, SCREEN_SIZE)
	textOnContext(dc, 120, 210, 20, "starting services", RGB{R: 100, G: 100, B: 100}, true, gg.AlignCenter)
	flushTextToScreen(dc)
}

func statsOn(w http.ResponseWriter, req *http.Request) {
	fmt.Fprintf(w, "Stats on\n")
	statsOff = false
}
func Native() string {
    cmd, err := exec.Command("/bin/sh", "/home/pi/pitemp-2.sh").Output()
    if err != nil {
    fmt.Printf("error %s", err)
    }
    output := string(cmd)
    return output
}
func stats() {
	defer time.AfterFunc(3*time.Second, stats)
	if statsOff {
		return
	}

	var cpuUsage, _ = cpu.Percent(0, false)
	v, _ := mem.VirtualMemory()
	temps := Native()
	dc := gg.NewContext(SCREEN_SIZE, SCREEN_SIZE)
	dc.DrawRectangle(0, 0, 240, 240)
	dc.SetColor(color.RGBA{51, 51, 51, 255})
	dc.Fill()
	textOnContext(dc, 70, 28, 22, "CPU", RGB{R: 160, G: 160, B: 160}, false, gg.AlignCenter)
	cpuPercent := cpuUsage[0]
	colorCpu := RGB{R: 183, G: 225, B: 205}
	if cpuPercent > 40 {
		colorCpu = RGB{R: 252, G: 232, B: 178}
	}
	if cpuPercent > 70 {
		colorCpu = RGB{R: 244, G: 199, B: 195}
	}
	textOnContext(dc, 70, 66, 30, fmt.Sprintf("%v%%", math.Round(cpuPercent)), colorCpu, true, gg.AlignCenter)
	textOnContext(dc, 170, 28, 22, "MEM", RGB{R: 160, G: 160, B: 160}, false, gg.AlignCenter)
	colorMem := RGB{R: 183, G: 225, B: 205}
	if cpuPercent > 40 {
		colorMem = RGB{R: 252, G: 232, B: 178}
	}
	if cpuPercent > 70 {
		colorMem = RGB{R: 244, G: 199, B: 195}
	}
	textOnContext(dc, 180,28,33, temps,RGB{R: 160, G: 160, B: 160}, false, gg.AlignCenter)
	textOnContext(dc, 170, 66, 30, fmt.Sprintf("%v%%", math.Round(v.UsedPercent)), colorMem, true, gg.AlignCenter)
	flushTextToScreen(dc)
}

func main() {
	err := rpio.Open()
	if err != nil {
		panic(err)
	}
	backlight := rpio.Pin(22)
	backlight.Output() // Output mode
	backlight.High()   // Set pin High

	http.HandleFunc("/rgb", rgb)
	http.HandleFunc("/image", drawImage)
	http.HandleFunc("/gif", drawGIF)
	http.HandleFunc("/text", textRequest)
	http.HandleFunc("/stats/on", statsOn)
	http.HandleFunc("/qr", qr)
	http.HandleFunc("/exit", exit)

	setFramebuffer()
	splash()

	time.AfterFunc(6*time.Second, stats)

	os.MkdirAll("/var/run/pibox/", 0755)
	file := "/var/run/pibox/framebuffer.sock"
	os.Remove(file)
	fmt.Printf("Listening on socket: %s\n", file)
	listener, err := net.Listen("unix", file)
	os.Chmod(file, 0777)
	if err != nil {
		log.Fatalf("Could not listen on %s: %v", file, err)
		return
	}
	defer listener.Close()
	if err = http.Serve(listener, nil); err != nil {
		log.Fatalf("Could not start HTTP server: %v", err)
	}
}
