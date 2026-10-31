package main

import (
	"image"
	"image/color"
)

// A tiny 3x5 pixel font, enough for the dashboard. Each glyph is 5 rows of 3 bits.
var glyphs = map[rune][5]string{
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

	'A': {"111", "101", "111", "101", "101"},
	'B': {"110", "101", "110", "101", "110"},
	'C': {"111", "100", "100", "100", "111"},
	'D': {"110", "101", "101", "101", "110"},
	'E': {"111", "100", "111", "100", "111"},
	'F': {"111", "100", "111", "100", "100"},
	'G': {"111", "100", "101", "101", "111"},
	'H': {"101", "101", "111", "101", "101"},
	'I': {"111", "010", "010", "010", "111"},
	'J': {"001", "001", "001", "101", "111"},
	'K': {"101", "101", "110", "101", "101"},
	'L': {"100", "100", "100", "100", "111"},
	'M': {"101", "111", "111", "101", "101"},
	'N': {"110", "101", "101", "101", "101"},
	'O': {"111", "101", "101", "101", "111"},
	'P': {"111", "101", "111", "100", "100"},
	'Q': {"111", "101", "101", "111", "001"},
	'R': {"111", "101", "110", "101", "101"},
	'S': {"111", "100", "111", "001", "111"},
	'T': {"111", "010", "010", "010", "010"},
	'U': {"101", "101", "101", "101", "111"},
	'V': {"101", "101", "101", "101", "010"},
	'W': {"101", "101", "111", "111", "101"},
	'X': {"101", "101", "010", "101", "101"},
	'Y': {"101", "101", "010", "010", "010"},
	'Z': {"111", "001", "010", "100", "111"},

	':': {"000", "010", "000", "010", "000"},
	'/': {"001", "001", "010", "100", "100"},
	'%': {"101", "001", "010", "100", "101"},
	'.': {"000", "000", "000", "000", "010"},
	'-': {"000", "000", "111", "000", "000"},
	' ': {"000", "000", "000", "000", "000"},
}

var (
	colBackground = color.RGBA{R: 0x0d, G: 0x0d, B: 0x16, A: 0xff}
	colLabel      = color.RGBA{R: 0x7a, G: 0x7a, B: 0x8c, A: 0xff}
	colValue      = color.RGBA{R: 0xf2, G: 0xf2, B: 0xf8, A: 0xff}
	colAccent     = color.RGBA{R: 0x38, G: 0xd0, B: 0xf0, A: 0xff}
	colGood       = color.RGBA{R: 0x3c, G: 0xd6, B: 0x7a, A: 0xff}
	colWarn       = color.RGBA{R: 0xf0, G: 0xc0, B: 0x40, A: 0xff}
	colBad        = color.RGBA{R: 0xf0, G: 0x50, B: 0x50, A: 0xff}
	colTrack      = color.RGBA{R: 0x22, G: 0x22, B: 0x30, A: 0xff}
)

func newTile() *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, tileW, tileH))
	fillRect(img, 0, 0, tileW, tileH, colBackground)
	return img
}

func fillRect(img *image.RGBA, x, y, w, h int, c color.RGBA) {
	for yy := y; yy < y+h; yy++ {
		if yy < 0 || yy >= img.Bounds().Dy() {
			continue
		}
		for xx := x; xx < x+w; xx++ {
			if xx < 0 || xx >= img.Bounds().Dx() {
				continue
			}
			img.SetRGBA(xx, yy, c)
		}
	}
}

func textWidth(text string, scale int) int {
	n := len([]rune(text))
	if n == 0 {
		return 0
	}
	return n*3*scale + (n-1)*scale
}

func drawText(img *image.RGBA, x, y int, text string, scale int, c color.RGBA) {
	cursor := x
	for _, r := range text {
		glyph, ok := glyphs[r]
		if !ok {
			glyph = glyphs[' ']
		}
		for row := 0; row < 5; row++ {
			for column := 0; column < 3; column++ {
				if glyph[row][column] != '1' {
					continue
				}
				fillRect(img, cursor+column*scale, y+row*scale, scale, scale, c)
			}
		}
		cursor += 4 * scale
	}
}

// centerText draws text horizontally centred in the tile.
func centerText(img *image.RGBA, y int, text string, scale int, c color.RGBA) {
	drawText(img, (tileW-textWidth(text, scale))/2, y, text, scale, c)
}

// fitScale returns the largest scale that keeps text inside maxWidth (and <= maxScale).
func fitScale(text string, maxWidth, maxScale int) int {
	for scale := maxScale; scale > 1; scale-- {
		if textWidth(text, scale) <= maxWidth {
			return scale
		}
	}
	return 1
}

func drawBar(img *image.RGBA, x, y, w, h int, fraction float64, c color.RGBA) {
	if fraction < 0 {
		fraction = 0
	}
	if fraction > 1 {
		fraction = 1
	}
	fillRect(img, x, y, w, h, colTrack)
	fillRect(img, x, y, int(float64(w)*fraction), h, c)
}

// levelColor maps 0..1 to green/yellow/red.
func levelColor(fraction float64) color.RGBA {
	switch {
	case fraction < 0.6:
		return colGood
	case fraction < 0.85:
		return colWarn
	default:
		return colBad
	}
}
