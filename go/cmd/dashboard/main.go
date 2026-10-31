// Command dashboard is a 15-key status panel for the M18.
//
//	go run ./cmd/dashboard
//
// Layout: clock, date, CPU, memory, GPU, VRAM, one key per CPU cluster (P/E cores on
// hybrid chips, or a single "CPU" cluster), runtime stats and a five sample CPU history.
// Only keys whose content changed are redrawn (250 ms tick).
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
	tileCount      = 15
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

type tileKind int

const (
	tileMetric tileKind = iota
	tileClock
	tileDate
	tileHistory
)

// tile is the content of one key; `sig` changes whenever the key has to be redrawn.
type tile struct {
	kind     tileKind
	sig      string
	label    string
	value    string
	fraction float64
	color    color.RGBA
}

func main() {
	duration := flag.Duration("duration", 0, "exit after this duration (0 = run until Ctrl+C)")
	dump := flag.String("dump", "", "write the 15 tiles as PNGs to this directory and exit (no device needed)")
	live := flag.Bool("live", false, "with -dump, collect real metrics instead of synthetic values")
	flag.Parse()

	if *dump != "" {
		if err := dumpTiles(*dump, *live); err != nil {
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

	var lastSig [tileCount]string
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
		if st.flashKey >= 0 && now.After(st.flashTill) {
			st.flashKey = -1
		}
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
		}
		snapshot := st
		mu.Unlock()

		tiles := buildTiles(snapshot)
		var batch []m18.KeyImage
		for index := range tiles {
			if tiles[index].sig == lastSig[index] {
				continue
			}
			lastSig[index] = tiles[index].sig
			batch = append(batch, m18.KeyImage{Index: index, Image: renderTile(index, tiles[index], snapshot)})
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

// buildTiles lays out the 15 keys. The cluster keys grow from index 6 and are padded with
// runtime statistics; the last five keys are always the CPU history.
func buildTiles(st state) [tileCount]tile {
	var tiles [tileCount]tile

	tiles[0] = tile{kind: tileClock, sig: "clock " + st.now.Format("15:04:05")}
	tiles[1] = tile{kind: tileDate, sig: "date " + st.now.Format("01/02 Mon")}
	tiles[2] = percentTile("CPU", st.metrics.CPU)
	tiles[3] = percentTile("MEM", st.metrics.Mem)
	tiles[4] = percentTile("GPU", st.metrics.GPU)
	tiles[5] = vramTile(st.metrics)

	next := 6
	for _, cluster := range st.metrics.Clusters {
		if next > 9 {
			break
		}
		// A single cluster is the same number as the total CPU, keep key 2 only.
		if len(st.metrics.Clusters) == 1 && cluster.Name == "CPU" {
			continue
		}
		tiles[next] = percentTile(cluster.Name, cluster.Percent)
		next++
	}

	extras := []tile{
		{
			kind: tileMetric, sig: ledSignature(st),
			label: "LED", value: ledValue(st), fraction: ledFraction(st), color: colAccent,
		},
		{
			kind: tileMetric, sig: "evt " + st.lastEvent,
			label: "EVT", value: st.lastEvent, fraction: -1, color: colAccent,
		},
		{
			kind: tileMetric, sig: fmt.Sprintf("fps %.0f", st.metrics.FPS),
			label: "FPS", value: fmt.Sprintf("%.0f", st.metrics.FPS), fraction: -1, color: colAccent,
		},
		{
			kind: tileMetric, sig: fmt.Sprintf("gor %d", st.metrics.Goroutines),
			label: "GOR", value: strconv.Itoa(st.metrics.Goroutines), fraction: -1, color: colAccent,
		},
		{
			kind: tileMetric, sig: fmt.Sprintf("heap %.1f", st.metrics.HeapMB),
			label: "HEAP", value: fmt.Sprintf("%.1f", st.metrics.HeapMB), fraction: -1, color: colAccent,
		},
	}
	for _, extra := range extras {
		if next > 9 {
			break
		}
		tiles[next] = extra
		next++
	}

	for index := 0; index < historyLength; index++ {
		sample := st.history[index]
		tiles[10+index] = tile{
			kind:     tileHistory,
			sig:      fmt.Sprintf("hist %d %.0f", index, sample),
			value:    fmt.Sprintf("%.0f", sample),
			fraction: sample / 100,
			color:    levelColor(sample / 100),
		}
	}

	if st.flashKey >= 0 && st.flashKey < tileCount && st.now.Before(st.flashTill) {
		tiles[st.flashKey].sig += " !"
	}

	return tiles
}

func percentTile(label string, value float64) tile {
	if value < 0 {
		return tile{kind: tileMetric, sig: label + " n/a", label: label, value: "N/A", fraction: -1, color: colLabel}
	}
	return tile{
		kind:     tileMetric,
		sig:      fmt.Sprintf("%s %.0f", label, value),
		label:    label,
		value:    fmt.Sprintf("%.0f", value),
		fraction: value / 100,
		color:    levelColor(value / 100),
	}
}

func vramTile(m Metrics) tile {
	if m.VRAMUsedMB < 0 {
		return tile{kind: tileMetric, sig: "vram n/a", label: "VRAM", value: "N/A", fraction: -1, color: colLabel}
	}
	fraction := -1.0
	if m.VRAMTotalMB > 0 {
		fraction = m.VRAMUsedMB / m.VRAMTotalMB
	}
	return tile{
		kind:     tileMetric,
		sig:      fmt.Sprintf("vram %.0f/%.0f", m.VRAMUsedMB, m.VRAMTotalMB),
		label:    "VRAM",
		value:    formatMemory(m.VRAMUsedMB),
		fraction: fraction,
		color:    colAccent,
	}
}

func formatMemory(mb float64) string {
	if mb >= 1024 {
		return fmt.Sprintf("%.1fG", mb/1024)
	}
	return fmt.Sprintf("%.0fM", mb)
}

func ledSignature(st state) string {
	return fmt.Sprintf("led %v %v %d", st.ledOn, st.ledMode, st.bright)
}

func ledValue(st state) string {
	if !st.ledOn {
		return "OFF"
	}
	return strings.ToUpper(st.ledMode.String())
}

func ledFraction(st state) float64 {
	if !st.ledOn {
		return 0
	}
	return float64(st.bright) / 100
}

// ---------------------------------------------------------------- rendering

func renderTile(index int, t tile, st state) *image.RGBA {
	img := newTile()

	switch t.kind {
	case tileClock:
		centerText(img, 10, st.now.Format("15:04"), 3, colValue)
		centerText(img, 34, st.now.Format("05"), 4, colAccent)

	case tileDate:
		centerText(img, 14, st.now.Format("01/02"), 3, colValue)
		centerText(img, 42, weekday(st.now), 2, colLabel)

	case tileHistory:
		centerText(img, 4, "CPU", 2, colLabel)
		height := int(t.fraction * 32)
		fillRect(img, 24, 16, 16, 32, colTrack)
		fillRect(img, 24, 16+32-height, 16, height, t.color)
		centerText(img, 51, t.value, 2, colValue)

	default:
		centerText(img, 4, t.label, 2, colLabel)
		scale := fitScale(t.value, 60, 6)
		centerText(img, 20, t.value, scale, colValue)
		if t.fraction >= 0 {
			drawBar(img, 8, 56, 48, 6, t.fraction, t.color)
		}
	}

	if index == st.flashKey && st.now.Before(st.flashTill) {
		drawBorder(img, colAccent)
	}
	return img
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

func shortName(event m18.Event) string {
	prefix := "K"
	if event.Kind == m18.KindButton {
		prefix = "B"
	}
	return prefix + strconv.Itoa(event.Index+1)
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
				colors[i] = color.RGBA{B: 24, A: 0xff}
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

// ---------------------------------------------------------------- preview

// dumpTiles renders the dashboard with synthetic values to PNG files, so the layout can be
// checked without a device (go run ./cmd/dashboard -dump ./preview).
func dumpTiles(dir string, live bool) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}

	now := time.Date(2026, 10, 31, 21, 47, 8, 0, time.Local)
	st := state{
		now:       now,
		lastEvent: "K7",
		metrics: Metrics{
			CPU: 37, Mem: 68, GPU: 42,
			VRAMUsedMB: 2415, VRAMTotalMB: 23.98 * 1024,
			Clusters:   []ClusterStat{{Name: "P", Percent: 31}, {Name: "E", Percent: 55}},
			Uptime:     51*time.Hour + 12*time.Minute,
			Goroutines: 12, HeapMB: 4.6, FPS: 4,
		},
		history: [historyLength]float64{37, 51, 23, 80, 12},
		ledOn:   true,
		ledMode: ledLoad,
		bright:  90,
	}

	if live {
		collect(&st.metrics)
		time.Sleep(1100 * time.Millisecond)
		collect(&st.metrics)
		st.metrics.FPS = 4
		for index := 0; index < historyLength; index++ {
			st.history[index] = st.metrics.CPU
		}
		log.Printf("live metrics: cpu=%.1f mem=%.1f gpu=%.1f vram=%.0f/%.0f MB clusters=%v",
			st.metrics.CPU, st.metrics.Mem, st.metrics.GPU,
			st.metrics.VRAMUsedMB, st.metrics.VRAMTotalMB, st.metrics.Clusters)
	}

	sheet := image.NewRGBA(image.Rect(0, 0, 5*(tileW+4)-4, 3*(tileH+4)-4))
	fillRect(sheet, 0, 0, sheet.Bounds().Dx(), sheet.Bounds().Dy(), color.RGBA{R: 0x30, G: 0x30, B: 0x38, A: 0xff})

	tiles := buildTiles(st)
	for index := 0; index < tileCount; index++ {
		tileImage := renderTile(index, tiles[index], st)
		if err := writePNG(fmt.Sprintf("%s/tile-%02d.png", dir, index), tileImage); err != nil {
			return err
		}
		x := (index%5)*(tileW+4) + 0
		y := (index/5)*(tileH+4) + 0
		for ty := 0; ty < tileH; ty++ {
			for tx := 0; tx < tileW; tx++ {
				sheet.Set(x+tx, y+ty, tileImage.At(tx, ty))
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
