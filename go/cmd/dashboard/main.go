// Command dashboard is a 15-key status panel for the M18.
//
//	go run ./cmd/dashboard
//
// It shows the time, date, CPU/memory load, uptime, Go runtime stats and a CPU history
// row, using throttled partial redraws (only keys whose content changed are sent).
//
// Bottom buttons: 1 = LEDs on/off, 2 = LED mode (load / rainbow / off), 3 = brightness.
// Ctrl+C restores the LEDs and closes the device.
package main

import (
	"context"
	"flag"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"log"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/Hithy/companion-surface-mirabox-stream-dock/go/m18"
)

const (
	historyLength  = 5
	tickInterval   = 250 * time.Millisecond
	metricInterval = time.Second
	flashDuration  = 220 * time.Millisecond
)

type ledMode int

const (
	ledLoad ledMode = iota
	ledRainbow
	ledOff
)

func (m ledMode) String() string {
	switch m {
	case ledRainbow:
		return "rain"
	case ledOff:
		return "off"
	default:
		return "load"
	}
}

type state struct {
	metrics   Metrics
	history   [historyLength]float64
	now       time.Time
	lastEvent string
	flashKey  int
	flashTill time.Time
	ledOn     bool
	ledMode   ledMode
	bright    int
	tick      int
}

func main() {
	duration := flag.Duration("duration", 0, "exit after this duration (0 = run until Ctrl+C)")
	dump := flag.String("dump", "", "write the 15 tiles as PNGs to this directory and exit (no device needed)")
	flag.Parse()

	if *dump != "" {
		if err := dumpTiles(*dump); err != nil {
			log.Fatal(err)
		}
		return
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if *duration > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, *duration)
		defer cancel()
	}

	dock, err := m18.Open(m18.Options{})
	if err != nil {
		log.Fatal(err)
	}
	defer func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = dock.SetAllLeds(cleanup, color.RGBA{})
		_ = dock.Close()
	}()

	log.Printf("connected: %s (%s)", dock.Info().Product, dock.Info().Serial)
	go func() {
		for err := range dock.Errors() {
			log.Printf("warning: %v", err)
		}
	}()

	st := state{ledOn: true, ledMode: ledLoad, bright: 90, flashKey: -1}
	var mu sync.Mutex

	go func() {
		for event := range dock.Events() {
			mu.Lock()
			st.lastEvent = shortName(event)
			if event.Pressed {
				st.flashKey = event.Index
				st.flashTill = time.Now().Add(flashDuration)
				if event.Kind == m18.KindButton {
					switch event.Index {
					case 0:
						st.ledOn = !st.ledOn
					case 1:
						st.ledMode = (st.ledMode + 1) % 3
					case 2:
						if st.bright == 100 {
							st.bright = 40
						} else {
							st.bright = 100
						}
						_ = dock.SetBrightness(ctx, st.bright)
					}
				}
			}
			mu.Unlock()
			log.Printf("%v %s (%s)", event.Pressed, event.Name, event.ID)
		}
	}()

	_ = dock.SetBrightness(ctx, st.bright)
	_ = dock.SetLedBrightness(ctx, 80)

	var lastSig [15]string
	frames := 0
	windowStart := time.Now()
	lastMetrics := time.Time{}

	ticker := time.NewTicker(tickInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			log.Println("closing")
			return
		case <-ticker.C:
		}

		now := time.Now()
		frames++

		mu.Lock()
		st.now = now
		st.tick++
		if now.Sub(lastMetrics) >= metricInterval {
			lastMetrics = now
			collect(&st.metrics)
			if elapsed := now.Sub(windowStart).Seconds(); elapsed > 0 {
				st.metrics.FPS = float64(frames) / elapsed
			}
			frames = 0
			windowStart = now

			// newest CPU sample first
			copy(st.history[1:], st.history[:historyLength-1])
			st.history[0] = st.metrics.CPU

			if st.flashKey >= 0 && now.After(st.flashTill) {
				st.flashKey = -1
			}
		}
		snapshot := st
		mu.Unlock()

		var batch []m18.KeyImage
		for index := range m18.Keys {
			signature := tileSignature(index, snapshot)
			if signature == lastSig[index] {
				continue
			}
			lastSig[index] = signature
			batch = append(batch, m18.KeyImage{Index: index, Image: renderTile(index, snapshot)})
		}
		if len(batch) > 0 {
			if err := dock.DrawKeys(ctx, batch); err != nil {
				log.Printf("draw: %v", err)
			}
		}

		if snapshot.ledOn && snapshot.ledMode != ledOff {
			if err := dock.SetLedColors(ctx, ledColors(snapshot)); err != nil {
				log.Printf("led: %v", err)
			}
		}
	}
}

func shortName(event m18.Event) string {
	prefix := "K"
	if event.Kind == m18.KindButton {
		prefix = "B"
	}
	return prefix + strconv.Itoa(event.Index+1)
}

// ---------------------------------------------------------------- rendering

func tileSignature(index int, st state) string {
	flash := ""
	if st.flashKey == index && st.now.Before(st.flashTill) {
		flash = "!"
	}
	switch index {
	case 0:
		return "clock " + st.now.Format("15:04:05") + flash
	case 1:
		return "date " + st.now.Format("01/02 Mon") + flash
	case 2:
		return fmt.Sprintf("cpu %.0f%s", st.metrics.CPU, flash)
	case 3:
		return fmt.Sprintf("mem %.0f%s", st.metrics.Mem, flash)
	case 4:
		return fmt.Sprintf("upt %d %s", st.metrics.Uptime/time.Minute, flash)
	case 5:
		return fmt.Sprintf("gor %d %s", st.metrics.Goroutines, flash)
	case 6:
		return fmt.Sprintf("heap %.1f %s", st.metrics.HeapMB, flash)
	case 7:
		return fmt.Sprintf("fps %.0f %s", st.metrics.FPS, flash)
	case 8:
		return "evt " + st.lastEvent + flash
	case 9:
		return fmt.Sprintf("led %v %v %d %s", st.ledOn, st.ledMode, st.bright, flash)
	default:
		return fmt.Sprintf("hist %d %.0f %s", index-10, st.history[index-10], flash)
	}
}

func renderTile(index int, st state) *image.RGBA {
	img := newTile()

	switch index {
	case 0: // clock
		centerText(img, 10, st.now.Format("15:04"), 3, colValue)
		centerText(img, 34, st.now.Format("05"), 4, colAccent)

	case 1: // date
		centerText(img, 14, st.now.Format("01/02"), 3, colValue)
		centerText(img, 42, weekday(st.now), 2, colLabel)

	case 2:
		metricTile(img, "CPU", fmt.Sprintf("%.0f", st.metrics.CPU), st.metrics.CPU/100, levelColor(st.metrics.CPU/100))
	case 3:
		metricTile(img, "MEM", fmt.Sprintf("%.0f", st.metrics.Mem), st.metrics.Mem/100, levelColor(st.metrics.Mem/100))
	case 4:
		metricTile(img, "UPT", formatUptime(st.metrics.Uptime), -1, colAccent)
	case 5:
		metricTile(img, "GOR", strconv.Itoa(st.metrics.Goroutines), -1, colAccent)
	case 6:
		metricTile(img, "HEAP", fmt.Sprintf("%.1f", st.metrics.HeapMB), -1, colAccent)
	case 7:
		metricTile(img, "FPS", fmt.Sprintf("%.0f", st.metrics.FPS), -1, colAccent)
	case 8:
		metricTile(img, "EVT", st.lastEvent, -1, colAccent)
	case 9:
		mode := strings.ToUpper(st.ledMode.String())
		fraction := float64(st.bright) / 100
		if !st.ledOn {
			mode = "OFF"
			fraction = 0
		}
		metricTile(img, "LED", mode, fraction, colAccent)

	default: // CPU history, newest at key 11
		sample := st.history[index-10]
		centerText(img, 4, "CPU", 2, colLabel)
		barHeight := int(sample / 100 * 32)
		fillRect(img, 24, 16, 16, 32, colTrack)
		fillRect(img, 24, 16+32-barHeight, 16, barHeight, levelColor(sample/100))
		centerText(img, 51, fmt.Sprintf("%.0f", sample), 2, colValue)
	}

	if st.flashKey == index && st.now.Before(st.flashTill) {
		drawBorder(img, colAccent)
	}
	return img
}

func metricTile(img *image.RGBA, label, value string, fraction float64, barColor color.RGBA) {
	centerText(img, 4, label, 2, colLabel)

	scale := fitScale(value, 60, 6)
	centerText(img, 20, value, scale, colValue)

	if fraction >= 0 {
		drawBar(img, 8, 56, 48, 6, fraction, barColor)
	}
}

func drawBorder(img *image.RGBA, c color.RGBA) {
	fillRect(img, 0, 0, tileW, 2, c)
	fillRect(img, 0, tileH-2, tileW, 2, c)
	fillRect(img, 0, 0, 2, tileH, c)
	fillRect(img, tileW-2, 0, 2, tileH, c)
}

func weekday(t time.Time) string {
	return strings.ToUpper(t.Weekday().String()[:2])
}

func formatUptime(d time.Duration) string {
	hours := int(d.Hours())
	minutes := int(d.Minutes()) % 60
	return fmt.Sprintf("%d:%02d", hours, minutes)
}

// ---------------------------------------------------------------- LEDs

func ledColors(st state) []color.RGBA {
	colors := make([]color.RGBA, m18.LEDCount)

	switch st.ledMode {
	case ledRainbow:
		for i := range colors {
			colors[i] = hsv(float64(i)*360/float64(m18.LEDCount)+float64(st.tick)*6, 1, 1)
		}
	default: // load
		lit := int(st.metrics.CPU / 100 * float64(m18.LEDCount))
		if lit > m18.LEDCount {
			lit = m18.LEDCount
		}
		level := levelColor(st.metrics.CPU / 100)
		for i := range colors {
			if i < lit {
				colors[i] = level
			} else {
				colors[i] = color.RGBA{R: 0, G: 0, B: 24, A: 0xff}
			}
		}
	}
	return colors
}

func hsv(h, s, v float64) color.RGBA {
	c := v * s
	deg := float64(((int(h) % 360) + 360) % 360)
	hp := deg / 60
	x := c * (1 - abs(mod(hp, 2)-1))

	var r, g, b float64
	switch {
	case hp < 1:
		r, g, b = c, x, 0
	case hp < 2:
		r, g, b = x, c, 0
	case hp < 3:
		r, g, b = 0, c, x
	case hp < 4:
		r, g, b = 0, x, c
	case hp < 5:
		r, g, b = x, 0, c
	default:
		r, g, b = c, 0, x
	}
	m := v - c
	return color.RGBA{
		R: uint8((r + m) * 255),
		G: uint8((g + m) * 255),
		B: uint8((b + m) * 255),
		A: 0xff,
	}
}

func mod(a, b float64) float64 {
	for a >= b {
		a -= b
	}
	return a
}

func abs(v float64) float64 {
	if v < 0 {
		return -v
	}
	return v
}

// dumpTiles renders the dashboard with synthetic values to PNG files, so the layout can be
// checked without a device (go run ./cmd/dashboard -dump ./preview).
func dumpTiles(dir string) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}

	now := time.Date(2026, 10, 31, 21, 47, 8, 0, time.Local)
	st := state{
		now:       now,
		lastEvent: "K7",
		metrics: Metrics{
			CPU: 37, Mem: 68, Uptime: 51*time.Hour + 12*time.Minute,
			Goroutines: 12, HeapMB: 4.6, FPS: 4,
		},
		history: [historyLength]float64{37, 51, 23, 80, 12},
		ledOn:   true,
		ledMode: ledLoad,
		bright:  90,
	}

	sheet := image.NewRGBA(image.Rect(0, 0, 5*(tileW+4)-4, 3*(tileH+4)-4))
	fillRect(sheet, 0, 0, sheet.Bounds().Dx(), sheet.Bounds().Dy(), color.RGBA{R: 0x30, G: 0x30, B: 0x38, A: 0xff})

	for index := range m18.Keys {
		tile := renderTile(index, st)
		name := fmt.Sprintf("%s/tile-%02d.png", dir, index)
		if err := writePNG(name, tile); err != nil {
			return err
		}
		x := (index%5)*(tileW+4) + 0
		y := (index/5)*(tileH+4) + 0
		for ty := 0; ty < tileH; ty++ {
			for tx := 0; tx < tileW; tx++ {
				sheet.Set(x+tx, y+ty, tile.At(tx, ty))
			}
		}
	}

	return writePNG(dir+"/dashboard.png", sheet)
}

func writePNG(path string, img image.Image) error {
	file, err := os.Create(path)
	if err != nil {
		return err
	}
	defer file.Close()
	return png.Encode(file, img)
}
