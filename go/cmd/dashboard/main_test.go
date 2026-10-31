package main

import (
	"image/png"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func testState() state {
	return state{
		now:       time.Date(2026, 10, 31, 21, 47, 8, 0, time.Local),
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
}

func TestRenderTiles(t *testing.T) {
	st := testState()
	tiles := buildTiles(st)
	for index := range [15]struct{}{} {
		tile := renderTile(index, tiles[index], st)
		if tile.Bounds().Dx() != tileW || tile.Bounds().Dy() != tileH {
			t.Fatalf("tile %d is %v", index, tile.Bounds())
		}
		// Every tile must contain something brighter than the background.
		bright := false
		for y := 0; y < tileH && !bright; y++ {
			for x := 0; x < tileW; x++ {
				c := tile.RGBAAt(x, y)
				if c.R > 0x40 || c.G > 0x40 || c.B > 0x60 {
					bright = true
					break
				}
			}
		}
		if !bright {
			t.Fatalf("tile %d is empty", index)
		}
	}
}

func TestFlashBorder(t *testing.T) {
	st := testState()
	st.flashKey = 3
	st.flashTill = st.now.Add(time.Second)

	tiles := buildTiles(st)
	if c := renderTile(3, tiles[3], st).RGBAAt(0, 0); c != colAccent {
		t.Fatalf("border pixel = %v, want %v", c, colAccent)
	}
	if c := renderTile(4, tiles[4], st).RGBAAt(0, 0); c == colAccent {
		t.Fatal("a tile without focus must not have the accent border")
	}
}

func TestDumpTiles(t *testing.T) {
	dir := t.TempDir()
	if err := dumpTiles(dir, false); err != nil {
		t.Fatal(err)
	}

	for index := range [15]struct{}{} {
		name := filepath.Join(dir, "tile-"+twoDigits(index)+".png")
		if _, err := os.Stat(name); err != nil {
			t.Fatalf("missing %s: %v", name, err)
		}
	}

	file, err := os.Open(filepath.Join(dir, "dashboard.png"))
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	img, err := png.Decode(file)
	if err != nil {
		t.Fatal(err)
	}
	wantW := 5*(tileW+4) - 4
	wantH := 3*(tileH+4) - 4
	if img.Bounds().Dx() != wantW || img.Bounds().Dy() != wantH {
		t.Fatalf("sheet = %v, want %dx%d", img.Bounds(), wantW, wantH)
	}
}

func TestTextWidth(t *testing.T) {
	if got := textWidth("CPU", 2); got != 22 {
		t.Fatalf("textWidth(CPU,2) = %d, want 22", got)
	}
	if got := textWidth("", 3); got != 0 {
		t.Fatalf("textWidth(empty) = %d", got)
	}
}

func twoDigits(v int) string {
	if v < 10 {
		return "0" + string(rune('0'+v))
	}
	return string(rune('0'+v/10)) + string(rune('0'+v%10))
}
