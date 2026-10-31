// Command probe exercises an M18 without an upper layer application.
//
//	go run ./cmd/probe list
//	go run ./cmd/probe info
//	go run ./cmd/probe listen -duration 20s
//	go run ./cmd/probe pattern -hold 30s
//	go run ./cmd/probe led-scan
//	go run ./cmd/probe selftest        # no device needed
//
// The vendor software has to be closed, otherwise the device is busy.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"image"
	"image/color"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/Hithy/companion-surface-mirabox-stream-dock/go/m18"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(1)
	}

	cmd := os.Args[1]
	fs := flag.NewFlagSet(cmd, flag.ExitOnError)
	serial := fs.String("serial", "", "open the device with this serial number")
	path := fs.String("path", "", "open the device at this HID path")
	hold := fs.Duration("hold", 10*time.Second, "keep the device open after drawing")
	raw := fs.Bool("raw", false, "print every raw input report")
	duration := fs.Duration("duration", 15*time.Second, "listen duration")
	delay := fs.Duration("delay", 500*time.Millisecond, "led-scan step delay")
	colorArg := fs.String("color", "#3050ff", "colour as #rrggbb (solid / led)")
	_ = fs.Parse(os.Args[2:])

	if cmd == "selftest" {
		os.Exit(selftest())
	}

	if cmd == "list" {
		if err := list(); err != nil {
			fail(err)
		}
		return
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	device, err := m18.Open(m18.Options{Serial: *serial, Path: *path})
	if err != nil {
		fail(err)
	}
	defer func() {
		if cmd == "pattern" || cmd == "solid" || cmd == "led" || cmd == "led-rainbow" {
			_ = device.CloseKeepImage()
		} else {
			_ = device.Close()
		}
	}()

	go func() {
		for err := range device.Errors() {
			fmt.Fprintln(os.Stderr, "warning:", err)
		}
	}()

	if *raw {
		go func() {
			for report := range device.Raw() {
				head := report
				if len(head) > 32 {
					head = head[:32]
				}
				fmt.Printf("RAW   len=%d %x\n", len(report), head)
			}
		}()
	}

	switch cmd {
	case "info":
		info := device.Info()
		fmt.Printf("Model        Mirabox Stream Dock M18\n")
		fmt.Printf("Serial       %s\n", info.Serial)
		fmt.Printf("Path         %s\n", info.Path)
		fmt.Printf("VID:PID      %04x:%04x\n", info.VendorID, info.ProductID)
		fmt.Printf("Usage        page=%#x usage=%#x\n", info.UsagePage, info.Usage)
		fmt.Printf("Panel        %dx%d, max %d bytes/key, %d LEDs\n", m18.ImageWidth, m18.ImageHeight, m18.MaxImageBytes, m18.LEDCount)
		fmt.Printf("Protocol     %d byte reports\n", m18.PacketSize)
		for _, k := range m18.Keys {
			fmt.Printf("  %-8s index %2d  id %-3s  input %#02x  slot %#02x\n", k.Name, k.Index, k.ID, k.InputID, k.Slot)
		}
		for _, b := range m18.Buttons {
			fmt.Printf("  %-8s index %2d  id %-3s  code  %#02x\n", b.Name, b.Index, b.ID, b.Code)
		}

	case "listen":
		fmt.Printf("Listening for %s, press keys/buttons...\n", *duration)
		timeout := time.After(*duration)
		for {
			select {
			case event := <-device.Events():
				dir := "UP  "
				if event.Pressed {
					dir = "DOWN"
				}
				fmt.Printf("%s  %-8s kind=%v id=%s code=%#02x\n", dir, event.Name, event.Kind, event.ID, event.Code)
			case <-timeout:
				fmt.Println("Done.")
				return
			case <-ctx.Done():
				return
			}
		}

	case "pattern":
		var batch []m18.KeyImage
		for _, k := range m18.Keys {
			batch = append(batch, m18.KeyImage{Index: k.Index, Image: testTile(k.Index)})
		}
		if err := device.DrawKeys(ctx, batch); err != nil {
			fail(err)
		}
		fmt.Printf("Drew %d test tiles. Expected: key N shows N, white marker top-left,\nred top-right, green bottom-left, blue bottom-right.\n", len(batch))
		waitOrInterrupt(ctx, *hold)

	case "solid":
		c, err := parseColor(*colorArg)
		if err != nil {
			fail(err)
		}
		var batch []m18.KeyImage
		for _, k := range m18.Keys {
			batch = append(batch, m18.KeyImage{Index: k.Index, Image: solid(m18.ImageWidth, m18.ImageHeight, c)})
		}
		if err := device.DrawKeys(ctx, batch); err != nil {
			fail(err)
		}
		fmt.Printf("Filled every key with rgb(%d, %d, %d)\n", c.R, c.G, c.B)
		waitOrInterrupt(ctx, *hold)

	case "led":
		c, err := parseColor(*colorArg)
		if err != nil {
			fail(err)
		}
		if err := device.SetAllLeds(ctx, c); err != nil {
			fail(err)
		}
		fmt.Printf("Set %d LEDs to rgb(%d, %d, %d)\n", m18.LEDCount, c.R, c.G, c.B)
		waitOrInterrupt(ctx, *hold)

	case "led-rainbow":
		colors := make([]color.RGBA, m18.LEDCount)
		for i := range colors {
			colors[i] = hsv(float64(i)*360/float64(m18.LEDCount), 1, 1)
		}
		if err := device.SetLedColors(ctx, colors); err != nil {
			fail(err)
		}
		fmt.Printf("Set %d LEDs to a rainbow\n", len(colors))
		waitOrInterrupt(ctx, *hold)

	case "led-off":
		if err := device.SetAllLeds(ctx, color.RGBA{}); err != nil {
			fail(err)
		}
		fmt.Println("LEDs off")

	case "led-scan":
		black := make([]color.RGBA, m18.LEDCount)
		fmt.Printf("Scanning %d LEDs, one at a time. Every step must light exactly one LED.\n", m18.LEDCount)
		for i := 0; i < m18.LEDCount; i++ {
			colors := append([]color.RGBA(nil), black...)
			colors[i] = color.RGBA{255, 255, 255, 255}
			if err := device.SetLedColors(ctx, colors); err != nil {
				fail(err)
			}
			fmt.Printf("LED %2d  ", i)
			time.Sleep(*delay)
		}
		if err := device.SetLedColors(ctx, black); err != nil {
			fail(err)
		}
		fmt.Println("\nDone.")

	case "brightness":
		percent := atoiFlag(fs.Args(), 100)
		if err := device.SetBrightness(ctx, percent); err != nil {
			fail(err)
		}
		fmt.Printf("Display brightness %d%%\n", percent)

	case "led-brightness":
		percent := atoiFlag(fs.Args(), 100)
		if err := device.SetLedBrightness(ctx, percent); err != nil {
			fail(err)
		}
		fmt.Printf("LED brightness %d%%\n", percent)

	case "clear":
		if err := device.ClearAll(ctx); err != nil {
			fail(err)
		}
		fmt.Println("Cleared all keys")

	case "wake":
		if err := device.Wake(ctx); err != nil {
			fail(err)
		}
		fmt.Println("Sent DIS")

	case "sleep":
		if err := device.Sleep(ctx); err != nil {
			fail(err)
		}
		fmt.Println("Sent HAN")

	default:
		usage()
		os.Exit(1)
	}
}

func usage() {
	fmt.Print(`Stream Dock M18 probe

  go run ./cmd/probe <command> [flags]

Commands
  list                     list devices
  info                     model, interface, key map
  listen                   print key/button events (-duration 20s, -raw)
  pattern                  numbered test tiles on every key
  solid                    fill every key (-color '#rrggbb')
  led                      set every LED (-color '#rrggbb')
  led-rainbow              rainbow over all LEDs
  led-scan                 light one LED at a time
  led-off                  switch the LEDs off
  brightness <0-100>       display brightness
  led-brightness <0-100>   LED brightness
  clear / wake / sleep     CLE / DIS / HAN
  selftest                 offline parser checks (no device)

Flags
  -serial <sn>   open by serial number
  -path <path>   open by HID path
  -hold <dur>    keep the device open after drawing (default 10s)
  -duration <d>  listen duration (default 15s)
  -delay <d>     led-scan step delay (default 500ms)
`)
}

func list() error {
	infos, err := m18.List()
	if err != nil {
		return err
	}
	if len(infos) == 0 {
		fmt.Println("No Stream Dock found (is the vendor software running?)")
		return nil
	}
	for _, info := range infos {
		kind := "secondary"
		if info.IsVendorIF {
			kind = "control"
		}
		fmt.Printf("Mirabox Stream Dock M18 [%s]\n", kind)
		fmt.Printf("  vid/pid   %04x:%04x\n", info.VendorID, info.ProductID)
		fmt.Printf("  serial    %s\n", info.Serial)
		fmt.Printf("  usage     page=%#x usage=%#x\n", info.UsagePage, info.Usage)
		fmt.Printf("  path      %s\n", info.Path)
	}
	return nil
}

// selftest replays input reports captured from an M18.
func selftest() int {
	report := func(code, state byte) []byte {
		b := make([]byte, 512)
		copy(b, []byte{0x41, 0x43, 0x4b, 0x00, 0x00, 0x4f, 0x4b, 0x00, 0x00, code, state})
		return b
	}

	cases := []struct {
		name  string
		data  []byte
		code  byte
		press bool
		ok    bool
	}{
		{"key 1 down", report(0x01, 0x01), 0x01, true, true},
		{"key 1 up", report(0x01, 0x00), 0x01, false, true},
		{"key 15 down", report(0x0f, 0x01), 0x0f, true, true},
		{"button left down", report(0x25, 0x01), 0x25, true, true},
		{"button middle up", report(0x30, 0x00), 0x30, false, true},
		{"button right down", report(0x31, 0x01), 0x31, true, true},
		{"command ack", report(0x00, 0x00), 0, false, false},
		{"unknown code", report(0x7f, 0x01), 0, false, false},
		{"truncated", report(0x01, 0x01)[:10], 0, false, false},
	}

	failures := 0
	for _, c := range cases {
		event, err := m18.DecodeInput(c.data)
		ok := err == nil
		code, pressed := event.Code, event.Pressed
		if ok != c.ok || (ok && (code != c.code || pressed != c.press)) {
			failures++
			fmt.Printf("FAIL %-20s got ok=%v code=%#02x pressed=%v\n", c.name, ok, code, pressed)
			continue
		}
		fmt.Printf("ok   %-20s\n", c.name)
	}
	if failures > 0 {
		fmt.Printf("%d check(s) failed\n", failures)
		return 1
	}
	fmt.Printf("All %d parser checks passed.\n", len(cases))
	return 0
}

// ------------------------------------------------------------------ drawing

var digits = map[rune][5]string{
	'0': {"111", "101", "101", "101", "111"},
	'1': {"010", "110", "010", "010", "111"},
	'2': {"111", "001", "111", "100", "111"},
	'3': {"111", "001", "111", "001", "111"},
	'4': {"101", "101", "111", "001", "001"},
	'5': {"111", "100", "111", "001", "111"},
	'6': {"111", "100", "111", "101", "111"},
	'7': {"111", "001", "001", "001", "001"},
	'8': {"111", "101", "111", "101", "111"},
	'9': {"111", "101", "111", "001", "111"},
}

// testTile draws the key number plus four corner markers so rotation and the slot map
// can be verified by eye.
func testTile(index int) image.Image {
	img := image.NewRGBA(image.Rect(0, 0, m18.ImageWidth, m18.ImageHeight))
	bg := hsv(float64(index*24%360), 0.55, 0.45)
	fillRect(img, 0, 0, m18.ImageWidth, m18.ImageHeight, bg)

	marker := m18.ImageWidth / 8
	fillRect(img, 0, 0, marker, marker, color.RGBA{255, 255, 255, 255})
	fillRect(img, m18.ImageWidth-marker, 0, marker, marker, color.RGBA{255, 0, 0, 255})
	fillRect(img, 0, m18.ImageHeight-marker, marker, marker, color.RGBA{0, 255, 0, 255})
	fillRect(img, m18.ImageWidth-marker, m18.ImageHeight-marker, marker, marker, color.RGBA{0, 0, 255, 255})

	drawText(img, strconv.Itoa(index+1), 4, color.RGBA{255, 255, 255, 255})
	return img
}

func solid(w, h int, c color.RGBA) image.Image {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	fillRect(img, 0, 0, w, h, c)
	return img
}

func fillRect(img *image.RGBA, x0, y0, w, h int, c color.RGBA) {
	for y := y0; y < y0+h && y < img.Bounds().Dy(); y++ {
		for x := x0; x < x0+w && x < img.Bounds().Dx(); x++ {
			img.SetRGBA(x, y, c)
		}
	}
}

func drawText(img *image.RGBA, text string, scale int, c color.RGBA) {
	glyphW := 3 * scale
	gap := scale
	width := len(text)*glyphW + (len(text)-1)*gap
	startX := (img.Bounds().Dx() - width) / 2
	startY := (img.Bounds().Dy() - 5*scale) / 2

	cursor := startX
	for _, r := range text {
		glyph, ok := digits[r]
		if !ok {
			continue
		}
		for row := 0; row < 5; row++ {
			for column := 0; column < 3; column++ {
				if glyph[row][column] != '1' {
					continue
				}
				fillRect(img, cursor+column*scale, startY+row*scale, scale, scale, c)
			}
		}
		cursor += glyphW + gap
	}
}

func hsv(h, s, v float64) color.RGBA {
	c := v * s
	hp := h / 60
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
		R: uint8((r + m) * 255), G: uint8((g + m) * 255), B: uint8((b + m) * 255), A: 255,
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

// ---------------------------------------------------------------- utilities

func parseColor(value string) (color.RGBA, error) {
	hex := strings.TrimPrefix(value, "#")
	if len(hex) != 6 {
		return color.RGBA{}, fmt.Errorf("invalid colour %q, expected #rrggbb", value)
	}
	v, err := strconv.ParseUint(hex, 16, 32)
	if err != nil {
		return color.RGBA{}, fmt.Errorf("invalid colour %q: %w", value, err)
	}
	return color.RGBA{R: uint8(v >> 16), G: uint8(v >> 8), B: uint8(v), A: 255}, nil
}

func atoiFlag(args []string, fallback int) int {
	if len(args) == 0 {
		return fallback
	}
	v, err := strconv.Atoi(args[0])
	if err != nil {
		return fallback
	}
	return v
}

func waitOrInterrupt(ctx context.Context, d time.Duration) {
	select {
	case <-time.After(d):
	case <-ctx.Done():
	}
}

func fail(err error) {
	if errors.Is(err, context.Canceled) {
		return
	}
	fmt.Fprintln(os.Stderr, err)
	os.Exit(1)
}
