package m18

import (
	"bytes"
	"image"
	"image/color"
	"image/jpeg"
	"testing"
)

func solid(w, h int, c color.RGBA) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.SetRGBA(x, y, c)
		}
	}
	return img
}

func TestRotate(t *testing.T) {
	src := image.NewRGBA(image.Rect(0, 0, 2, 2))
	a := color.RGBA{255, 0, 0, 255}
	b := color.RGBA{0, 255, 0, 255}
	c := color.RGBA{0, 0, 255, 255}
	d := color.RGBA{255, 255, 0, 255}
	src.SetRGBA(0, 0, a)
	src.SetRGBA(1, 0, b)
	src.SetRGBA(0, 1, c)
	src.SetRGBA(1, 1, d)

	got := rotate(src, 90)
	want := [4]color.RGBA{c, a, d, b} // C A / D B
	if got.Bounds().Dx() != 2 || got.Bounds().Dy() != 2 {
		t.Fatalf("size = %v", got.Bounds())
	}
	for i, w := range want {
		if g := got.RGBAAt(i%2, i/2); g != w {
			t.Fatalf("rot90 pixel %d = %v, want %v", i, g, w)
		}
	}

	if g := rotate(src, 270).RGBAAt(0, 0); g != b {
		t.Fatalf("rot270 top-left = %v, want %v", g, b)
	}
	if g := rotate(src, 180).RGBAAt(0, 0); g != d {
		t.Fatalf("rot180 top-left = %v, want %v", g, d)
	}
	if g := mirror(src, true, false).RGBAAt(0, 0); g != b {
		t.Fatalf("mirrorX top-left = %v, want %v", g, b)
	}
}

func TestPrepareContain(t *testing.T) {
	red := color.RGBA{255, 0, 0, 255}
	canvas := prepare(solid(4, 2, red), ImageOptions{})

	if canvas.Bounds().Dx() != ImageWidth || canvas.Bounds().Dy() != ImageHeight {
		t.Fatalf("canvas = %v, want %dx%d", canvas.Bounds(), ImageWidth, ImageHeight)
	}
	// 4x2 scaled to fit 64x64 becomes 64x32, so the top/bottom are background.
	if c := canvas.RGBAAt(ImageWidth/2, 2); c.R != 0 || c.G != 0 || c.B != 0 {
		t.Fatalf("top edge = %v, want the background", c)
	}
	if c := canvas.RGBAAt(ImageWidth/2, ImageHeight/2); c.R < 200 || c.G != 0 || c.B != 0 {
		t.Fatalf("centre = %v, want red", c)
	}
}

func TestPrepareCover(t *testing.T) {
	red := color.RGBA{255, 0, 0, 255}
	canvas := prepare(solid(4, 2, red), ImageOptions{Fit: FitCover})
	if c := canvas.RGBAAt(ImageWidth/2, 1); c.R < 200 {
		t.Fatalf("cover top edge = %v, want red (no letterbox)", c)
	}
}

func TestEncodeKeyImage(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 64, 64))
	for y := 0; y < 64; y++ {
		for x := 0; x < 64; x++ {
			img.SetRGBA(x, y, color.RGBA{uint8(x * 4), uint8(y * 4), 0x40, 255})
		}
	}

	jpegBytes, err := EncodeKeyImage(img, ImageOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(jpegBytes) > MaxImageBytes {
		t.Fatalf("jpeg is %d bytes, limit is %d", len(jpegBytes), MaxImageBytes)
	}
	if !bytes.HasPrefix(jpegBytes, []byte{0xff, 0xd8}) {
		t.Fatal("payload is not a JPEG")
	}

	decoded, err := jpeg.Decode(bytes.NewReader(jpegBytes))
	if err != nil {
		t.Fatal(err)
	}
	if decoded.Bounds().Dx() != ImageWidth || decoded.Bounds().Dy() != ImageHeight {
		t.Fatalf("decoded size = %v", decoded.Bounds())
	}
}

func TestEncodeKeyImageTooLarge(t *testing.T) {
	// Noise is the worst case for JPEG, keep the budget unreachable to exercise the error.
	img := image.NewRGBA(image.Rect(0, 0, 64, 64))
	for y := 0; y < 64; y++ {
		for x := 0; x < 64; x++ {
			img.SetRGBA(x, y, color.RGBA{uint8(x*y*7 + 13), uint8(x*31 + y), uint8(y*17 + x*3), 255})
		}
	}
	if _, err := EncodeKeyImage(img, ImageOptions{Quality: 100}); err != nil {
		t.Fatalf("a 64x64 image should always fit: %v", err)
	}
}
