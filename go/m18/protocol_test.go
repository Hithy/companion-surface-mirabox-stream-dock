package m18

import (
	"bytes"
	"testing"
)

func TestFrameLayout(t *testing.T) {
	f := wakeFrame()
	if len(f) != PacketSize+1 {
		t.Fatalf("frame length = %d, want %d", len(f), PacketSize+1)
	}
	if f[0] != 0x00 {
		t.Fatalf("report id = %#x, want 0", f[0])
	}
	want := []byte{0x43, 0x52, 0x54, 0x00, 0x00, 0x44, 0x49, 0x53}
	if !bytes.Equal(f[1:1+len(want)], want) {
		t.Fatalf("prefix = % x, want % x", f[1:1+len(want)], want)
	}
	for i := 1 + len(want); i < len(f); i++ {
		if f[i] != 0 {
			t.Fatalf("padding at %d = %#x, want 0", i, f[i])
		}
	}
}

func TestClearFrame(t *testing.T) {
	f := clearFrame(0x0b)
	want := []byte{0x00, 0x43, 0x52, 0x54, 0x00, 0x00, 0x43, 0x4c, 0x45, 0x00, 0x00, 0x00, 0x0b}
	if !bytes.Equal(f[:len(want)], want) {
		t.Fatalf("clear frame = % x, want % x", f[:len(want)], want)
	}
}

func TestBrightnessClamp(t *testing.T) {
	cases := []struct {
		in   int
		want byte
	}{{-5, 0}, {0, 0}, {55, 55}, {100, 100}, {140, 100}}
	for _, c := range cases {
		f := brightnessFrame(c.in)
		if got := f[11]; got != c.want {
			t.Fatalf("brightness(%d) = %d, want %d", c.in, got, c.want)
		}
	}
}

func TestLEDFrame(t *testing.T) {
	colors := make([]Rgb, LEDCount)
	for i := range colors {
		colors[i] = Rgb{R: byte(i), G: 0x10, B: 0x20, A: 0xff}
	}

	f, err := ledFrame(colors)
	if err != nil {
		t.Fatal(err)
	}
	if string(f[6:11]) != "SETLB" {
		t.Fatalf("opcode = %q, want SETLB", f[6:11])
	}
	for i, c := range colors {
		offset := 11 + i*3
		if f[offset] != c.R || f[offset+1] != c.G || f[offset+2] != c.B {
			t.Fatalf("led %d = % x, want % x", i, f[offset:offset+3], []byte{c.R, c.G, c.B})
		}
	}

	if _, err := ledFrame(colors[:10]); err == nil {
		t.Fatal("expected an error for a wrong LED count")
	}
}

func TestImageBatch(t *testing.T) {
	jpeg := bytes.Repeat([]byte{0xab}, 2500)
	frames := imageBatch(0x07, jpeg)

	// 2500 bytes = 3 chunks of 1024/1024/452, plus the BAT header.
	if len(frames) != 4 {
		t.Fatalf("got %d frames, want 4", len(frames))
	}

	bat := frames[0]
	want := []byte{0x00, 0x43, 0x52, 0x54, 0x00, 0x00, 0x42, 0x41, 0x54, 0x00, 0x00, 0x09, 0xc4, 0x07}
	if !bytes.Equal(bat[:len(want)], want) {
		t.Fatalf("BAT header = % x, want % x", bat[:len(want)], want)
	}

	var rebuilt []byte
	for _, frame := range frames[1:] {
		if len(frame) != PacketSize+1 {
			t.Fatalf("chunk length = %d, want %d", len(frame), PacketSize+1)
		}
		if frame[0] != 0x00 {
			t.Fatalf("chunk report id = %#x, want 0", frame[0])
		}
		rebuilt = append(rebuilt, frame[1:]...)
	}
	if !bytes.HasPrefix(rebuilt, jpeg) {
		t.Fatal("chunks do not reconstruct the JPEG payload")
	}
}
