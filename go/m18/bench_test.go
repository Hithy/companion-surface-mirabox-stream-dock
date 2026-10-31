package m18

import (
	"image"
	"image/color"
	"testing"
)

func benchImage() image.Image {
	img := image.NewRGBA(image.Rect(0, 0, 64, 64))
	for y := 0; y < 64; y++ {
		for x := 0; x < 64; x++ {
			img.SetRGBA(x, y, color.RGBA{uint8(x * 4), uint8(y * 4), 0x40, 255})
		}
	}
	return img
}

func BenchmarkEncodeKeyImage(b *testing.B) {
	img := benchImage()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := EncodeKeyImage(img, ImageOptions{}); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkParseInput(b *testing.B) {
	data := report(0x03, 0x01)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, _, ok := ParseInput(data); !ok {
			b.Fatal("parse failed")
		}
	}
}
