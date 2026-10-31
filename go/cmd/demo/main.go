// Command demo is a small reference application on top of the m18 driver.
//
//	go run ./cmd/demo
//
// It shows batched key drawing (only the changed keys are re-sent), key feedback, a LED
// chase and a graceful shutdown on Ctrl+C. Press one of the three bottom buttons to pause.
package main

import (
	"context"
	"image"
	"image/color"
	"log"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/Hithy/companion-surface-mirabox-stream-dock/go/m18"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	device, err := m18.Open(m18.Options{})
	if err != nil {
		log.Fatal(err)
	}
	defer device.Close()

	log.Printf("connected: %s (%s)", device.Info().Product, device.Info().Serial)
	go func() {
		for err := range device.Errors() {
			log.Printf("warning: %v", err)
		}
	}()

	dim := solid(color.RGBA{8, 8, 14, 255})
	hot := solid(color.RGBA{240, 240, 255, 255})
	pressed := solid(color.RGBA{0, 220, 120, 255})

	initial := make([]m18.KeyImage, len(m18.Keys))
	for i, key := range m18.Keys {
		initial[i] = m18.KeyImage{Index: key.Index, Image: dim}
	}
	if err := device.DrawKeys(ctx, initial); err != nil {
		log.Fatal(err)
	}
	if err := device.SetBrightness(ctx, 90); err != nil {
		log.Fatal(err)
	}
	if err := device.SetLedBrightness(ctx, 80); err != nil {
		log.Fatal(err)
	}

	var mu sync.Mutex
	litUntil := map[int]time.Time{}
	paused := false

	go func() {
		for event := range device.Events() {
			log.Printf("%v %s (%s)", event.Pressed, event.Name, event.ID)
			if !event.Pressed {
				continue
			}
			mu.Lock()
			if event.Kind == m18.KindButton {
				paused = !paused
				log.Printf("paused: %v", paused)
			} else {
				litUntil[event.Index] = time.Now().Add(300 * time.Millisecond)
			}
			mu.Unlock()
		}
	}()

	ticker := time.NewTicker(120 * time.Millisecond)
	defer ticker.Stop()

	head := 0
	for {
		select {
		case <-ctx.Done():
			log.Println("closing")
			return
		case <-ticker.C:
			mu.Lock()
			wasPaused := paused
			mu.Unlock()
			if wasPaused {
				continue
			}

			previous := head
			head = (head + 1) % len(m18.Keys)

			batch := []m18.KeyImage{
				{Index: previous, Image: dim},
				{Index: head, Image: hot},
			}

			now := time.Now()
			mu.Lock()
			for index, until := range litUntil {
				if until.After(now) {
					batch = append(batch, m18.KeyImage{Index: index, Image: pressed})
				} else {
					delete(litUntil, index)
				}
			}
			mu.Unlock()

			if err := device.DrawKeys(ctx, batch); err != nil {
				log.Printf("draw: %v", err)
			}

			colors := make([]color.RGBA, m18.LEDCount)
			for i := range colors {
				if i == head {
					colors[i] = color.RGBA{255, 255, 255, 255}
				} else {
					colors[i] = color.RGBA{0, 20, 60, 255}
				}
			}
			if err := device.SetLedColors(ctx, colors); err != nil {
				log.Printf("led: %v", err)
			}
		}
	}
}

func solid(c color.RGBA) image.Image {
	img := image.NewRGBA(image.Rect(0, 0, m18.ImageWidth, m18.ImageHeight))
	for y := 0; y < m18.ImageHeight; y++ {
		for x := 0; x < m18.ImageWidth; x++ {
			img.SetRGBA(x, y, c)
		}
	}
	return img
}
