package main

import (
	"image"
	"image/png"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Hithy/companion-surface-mirabox-stream-dock/go/m18"
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
			Cores: syntheticCores(20),
		},
		history:  [historyLength]float64{37, 51, 23, 80, 12},
		ledOn:    true,
		ledMode:  ledLoad,
		bright:   90,
		flashKey: -1,
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
		if !hasContent(tile) {
			t.Fatalf("tile %d is empty", index)
		}
	}
}

func TestRenderCorePage(t *testing.T) {
	st := testState()
	st.page = pageCPU
	tiles := buildCPUPage(st)
	for index := range [15]struct{}{} {
		tile := renderTile(index, tiles[index], st)
		if !hasContent(tile) {
			t.Fatalf("core page tile %d is empty", index)
		}
	}
}

func hasContent(img *image.RGBA) bool {
	for y := 0; y < tileH; y++ {
		for x := 0; x < tileW; x++ {
			c := img.RGBAAt(x, y)
			if c.R > 0x40 || c.G > 0x40 || c.B > 0x60 {
				return true
			}
		}
	}
	return false
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

func TestHandleEventNavigation(t *testing.T) {
	st := testState()
	now := st.now
	key := func(index int) m18.Event {
		return m18.Event{Kind: m18.KindKey, Index: index, Pressed: true}
	}
	button := func(index int) m18.Event {
		return m18.Event{Kind: m18.KindButton, Index: index, Pressed: true}
	}

	if result := st.handleEvent(key(cpuKeyIndex), now); !result.pageChanged || st.page != pageCPU {
		t.Fatalf("CPU key should open the core page, page=%v", st.page)
	}

	// right button = next page (20 cores = 2 pages)
	if result := st.handleEvent(button(2), now); !result.pageChanged || st.cpuPage != 1 {
		t.Fatalf("right button should go to page 2, cpuPage=%d", st.cpuPage)
	}
	// right again stays on the last page
	if result := st.handleEvent(button(2), now); result.pageChanged || st.cpuPage != 1 {
		t.Fatalf("page must not exceed the last page, cpuPage=%d", st.cpuPage)
	}
	// left button = previous page
	if result := st.handleEvent(button(0), now); !result.pageChanged || st.cpuPage != 0 {
		t.Fatalf("left button should go back a page, cpuPage=%d", st.cpuPage)
	}
	// middle button = back to the main page
	if result := st.handleEvent(button(1), now); !result.pageChanged || st.page != pageMain {
		t.Fatalf("middle button should return, page=%v", st.page)
	}

	// buttons on the main page keep their LED meaning
	ledBefore := st.ledOn
	if result := st.handleEvent(button(0), now); result.pageChanged || st.ledOn == ledBefore {
		t.Fatal("left button should toggle the LEDs on the main page")
	}
	if result := st.handleEvent(button(2), now); !result.setBrightness || result.newBrightness == 90 {
		t.Fatal("right button should toggle the brightness on the main page")
	}
}

func TestBuildCPUPage(t *testing.T) {
	st := testState() // 20 synthetic cores
	st.page = pageCPU

	tiles := buildCPUPage(st)
	if tiles[0].kind != tileCore || tiles[0].label != "C0" {
		t.Fatalf("first tile = %+v", tiles[0])
	}
	if tiles[14].kind != tileCore || tiles[14].label != "C14" {
		t.Fatalf("15th tile = %+v", tiles[14])
	}
	if tiles[0].note != "3.2G" && tiles[0].note != "3.5G" && tiles[0].note != "3.8G" &&
		tiles[0].note != "4.1G" && tiles[0].note != "4.4G" && tiles[0].note != "4.7G" {
		t.Fatalf("frequency note = %q", tiles[0].note)
	}

	st.cpuPage = 1
	tiles = buildCPUPage(st)
	if tiles[0].label != "C15" {
		t.Fatalf("second page starts with %q", tiles[0].label)
	}
	if tiles[5].label != "PAGE" || tiles[5].value != "2/2" {
		t.Fatalf("page indicator = %+v", tiles[5])
	}
	if tiles[6].label != "BTN2" || tiles[6].value != "BACK" {
		t.Fatalf("back hint = %+v", tiles[6])
	}
}

func TestBuildCPUPageSinglePage(t *testing.T) {
	st := testState()
	st.metrics.Cores = syntheticCores(8)
	st.page = pageCPU

	tiles := buildCPUPage(st)
	if tiles[8].label != "PAGE" || tiles[8].value != "1/1" {
		t.Fatalf("page indicator = %+v", tiles[8])
	}
	if tiles[9].label != "BTN2" {
		t.Fatalf("back hint = %+v", tiles[9])
	}
}

func TestDumpTiles(t *testing.T) {
	dir := t.TempDir()
	if err := dumpTiles(dir, false); err != nil {
		t.Fatal(err)
	}

	for _, name := range []string{"dashboard.png", "cpu-page1.png", "cpu-page2.png"} {
		if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
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
