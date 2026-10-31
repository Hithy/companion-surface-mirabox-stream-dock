package m18

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
)

// Fit controls how a source image is placed on the square key.
type Fit int

const (
	// FitContain scales the image to fit inside the key and pads the rest with
	// Background (default).
	FitContain Fit = iota
	// FitCover scales to fill the key and crops the overflow.
	FitCover
	// FitStretch scales without keeping the aspect ratio.
	FitStretch
)

// ImageOptions tunes the key image pipeline.
type ImageOptions struct {
	Fit        Fit
	Background color.RGBA
	// Rotate is applied clockwise before fitting: 0, 90, 180 or 270.
	Rotate  int
	MirrorX bool
	MirrorY bool
	// Quality is the starting JPEG quality, 1..100 (default 90). It is lowered
	// automatically until the payload fits MaxImageBytes.
	Quality int
}

// EncodeKeyImage turns an arbitrary image into a JPEG the device accepts: exactly
// ImageWidth x ImageHeight, at most MaxImageBytes bytes.
func EncodeKeyImage(src image.Image, opts ImageOptions) ([]byte, error) {
	quality := opts.Quality
	if quality <= 0 {
		quality = 90
	}

	canvas := prepare(src, opts)

	var buf bytes.Buffer
	for attempt := 0; attempt < 12; attempt++ {
		buf.Reset()
		if err := jpeg.Encode(&buf, canvas, &jpeg.Options{Quality: quality}); err != nil {
			return nil, err
		}
		if buf.Len() <= MaxImageBytes {
			return buf.Bytes(), nil
		}
		if quality <= 10 {
			break
		}
		quality -= 10
		if quality < 10 {
			quality = 10
		}
	}
	return nil, errTooLarge(buf.Len())
}

type tooLargeError struct{ size int }

func (e tooLargeError) Error() string {
	return fmt.Sprintf("m18: could not compress the key image below %d bytes (got %d)", MaxImageBytes, e.size)
}

func errTooLarge(size int) error { return tooLargeError{size: size} }

// prepare renders the source into an ImageWidth x ImageHeight RGB canvas.
func prepare(src image.Image, opts ImageOptions) *image.RGBA {
	bg := opts.Background
	if bg.A == 0 {
		bg.A = 0xff
	}

	// Work on an RGBA copy with the alpha channel composited over the background, so
	// scaling does not pull transparent pixels towards black.
	img := composite(src, bg)
	img = mirror(img, opts.MirrorX, opts.MirrorY)
	img = rotate(img, opts.Rotate)

	if opts.Fit == FitStretch {
		return resize(img, ImageWidth, ImageHeight)
	}

	sw, sh := img.Bounds().Dx(), img.Bounds().Dy()
	scale := float64(ImageWidth) / float64(sw)
	if float64(ImageHeight)/float64(sh) < scale {
		scale = float64(ImageHeight) / float64(sh)
	}
	if opts.Fit == FitCover {
		scale = float64(ImageWidth) / float64(sw)
		if float64(ImageHeight)/float64(sh) > scale {
			scale = float64(ImageHeight) / float64(sh)
		}
	}

	dw := max(1, int(float64(sw)*scale+0.5))
	dh := max(1, int(float64(sh)*scale+0.5))
	resized := resize(img, dw, dh)

	canvas := image.NewRGBA(image.Rect(0, 0, ImageWidth, ImageHeight))
	for y := 0; y < ImageHeight; y++ {
		for x := 0; x < ImageWidth; x++ {
			canvas.SetRGBA(x, y, bg)
		}
	}

	if opts.Fit == FitCover && (dw > ImageWidth || dh > ImageHeight) {
		// centre crop
		sx := (dw - ImageWidth) / 2
		sy := (dh - ImageHeight) / 2
		for y := 0; y < ImageHeight; y++ {
			for x := 0; x < ImageWidth; x++ {
				canvas.Set(x, y, resized.At(sx+x, sy+y))
			}
		}
		return canvas
	}

	offsetX := (ImageWidth - dw) / 2
	offsetY := (ImageHeight - dh) / 2
	for y := 0; y < dh; y++ {
		for x := 0; x < dw; x++ {
			canvas.Set(offsetX+x, offsetY+y, resized.At(x, y))
		}
	}
	return canvas
}

// composite returns an opaque RGBA copy of src, blending alpha over bg.
func composite(src image.Image, bg color.RGBA) *image.RGBA {
	b := src.Bounds()
	out := image.NewRGBA(image.Rect(0, 0, b.Dx(), b.Dy()))
	for y := 0; y < b.Dy(); y++ {
		for x := 0; x < b.Dx(); x++ {
			// NRGBA is not alpha premultiplied, which makes the blend straightforward.
			c := color.NRGBAModel.Convert(src.At(b.Min.X+x, b.Min.Y+y)).(color.NRGBA)
			a := uint32(c.A)
			inv := 255 - a
			out.SetRGBA(x, y, color.RGBA{
				R: uint8((uint32(c.R)*a + uint32(bg.R)*inv + 127) / 255),
				G: uint8((uint32(c.G)*a + uint32(bg.G)*inv + 127) / 255),
				B: uint8((uint32(c.B)*a + uint32(bg.B)*inv + 127) / 255),
				A: 0xff,
			})
		}
	}
	return out
}

func mirror(src *image.RGBA, mx, my bool) *image.RGBA {
	if !mx && !my {
		return src
	}
	w, h := src.Bounds().Dx(), src.Bounds().Dy()
	out := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		sy := y
		if my {
			sy = h - 1 - y
		}
		for x := 0; x < w; x++ {
			sx := x
			if mx {
				sx = w - 1 - x
			}
			out.Set(x, y, src.At(sx, sy))
		}
	}
	return out
}

func rotate(src *image.RGBA, degrees int) *image.RGBA {
	degrees = ((degrees % 360) + 360) % 360
	if degrees == 0 {
		return src
	}
	w, h := src.Bounds().Dx(), src.Bounds().Dy()

	outW, outH := w, h
	if degrees != 180 {
		outW, outH = h, w
	}
	out := image.NewRGBA(image.Rect(0, 0, outW, outH))
	for y := 0; y < outH; y++ {
		for x := 0; x < outW; x++ {
			var sx, sy int
			switch degrees {
			case 90:
				sx, sy = y, h-1-x
			case 180:
				sx, sy = w-1-x, h-1-y
			case 270:
				sx, sy = w-1-y, x
			default:
				sx, sy = x, y
			}
			out.Set(x, y, src.At(sx, sy))
		}
	}
	return out
}

// resize scales with bilinear sampling when upscaling and an area average when downscaling.
func resize(src *image.RGBA, width, height int) *image.RGBA {
	sw, sh := src.Bounds().Dx(), src.Bounds().Dy()
	if sw == width && sh == height {
		return src
	}
	out := image.NewRGBA(image.Rect(0, 0, width, height))

	if width < sw && height < sh {
		for y := 0; y < height; y++ {
			y0 := y * sh / height
			y1 := (y + 1) * sh / height
			if y1 <= y0 {
				y1 = y0 + 1
			}
			for x := 0; x < width; x++ {
				x0 := x * sw / width
				x1 := (x + 1) * sw / width
				if x1 <= x0 {
					x1 = x0 + 1
				}
				var r, g, b, n uint32
				for yy := y0; yy < y1 && yy < sh; yy++ {
					for xx := x0; xx < x1 && xx < sw; xx++ {
						c := src.RGBAAt(xx, yy)
						r += uint32(c.R)
						g += uint32(c.G)
						b += uint32(c.B)
						n++
					}
				}
				out.SetRGBA(x, y, color.RGBA{
					R: uint8(r / n), G: uint8(g / n), B: uint8(b / n), A: 0xff,
				})
			}
		}
		return out
	}

	sx := float64(sw) / float64(width)
	sy := float64(sh) / float64(height)
	for y := 0; y < height; y++ {
		fy := (float64(y)+0.5)*sy - 0.5
		y0 := floorClamp(fy, sh)
		y1 := y0 + 1
		if y1 >= sh {
			y1 = sh - 1
		}
		dy := fy - float64(y0)
		for x := 0; x < width; x++ {
			fx := (float64(x)+0.5)*sx - 0.5
			x0 := floorClamp(fx, sw)
			x1 := x0 + 1
			if x1 >= sw {
				x1 = sw - 1
			}
			dx := fx - float64(x0)

			c00 := src.RGBAAt(x0, y0)
			c10 := src.RGBAAt(x1, y0)
			c01 := src.RGBAAt(x0, y1)
			c11 := src.RGBAAt(x1, y1)
			out.SetRGBA(x, y, color.RGBA{
				R: blend4(c00.R, c10.R, c01.R, c11.R, dx, dy),
				G: blend4(c00.G, c10.G, c01.G, c11.G, dx, dy),
				B: blend4(c00.B, c10.B, c01.B, c11.B, dx, dy),
				A: 0xff,
			})
		}
	}
	return out
}

func blend4(p00, p10, p01, p11 uint8, fx, fy float64) uint8 {
	top := float64(p00) + (float64(p10)-float64(p00))*fx
	bottom := float64(p01) + (float64(p11)-float64(p01))*fx
	return uint8(top + (bottom-top)*fy + 0.5)
}

func floorClamp(v float64, size int) int {
	if v < 0 {
		return 0
	}
	maxIdx := size - 1
	if v >= float64(maxIdx) {
		return maxIdx
	}
	return int(v)
}
