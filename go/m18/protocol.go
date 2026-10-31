package m18

import (
	"fmt"
	"image/color"
)

// Protocol framing.
//
// Every write is a 1025 byte HID report: one report id byte (0x00) followed by up to
// PacketSize payload bytes, zero padded. Commands are "CRT" (0x43 0x52 0x54) + 0x00 0x00
// followed by a three letter opcode and its arguments.
var cmdPrefix = [5]byte{0x43, 0x52, 0x54, 0x00, 0x00}

// Opcodes without the CRT prefix.
var (
	opWake      = []byte{0x44, 0x49, 0x53}                         // DIS
	opSleep     = []byte{0x48, 0x41, 0x4e}                         // HAN
	opRefresh   = []byte{0x53, 0x54, 0x50}                         // STP
	opKeepAlive = []byte{0x43, 0x4f, 0x4e, 0x4e, 0x45, 0x43, 0x54} // CONNECT
	opShutdown  = []byte{0x43, 0x4c, 0x45, 0x00, 0x00, 0x44, 0x43} // CLE .. DC
)

// frame builds a single padded report from a CRT command.
func frame(cmd ...byte) []byte {
	if len(cmd)+len(cmdPrefix) > PacketSize {
		panic(fmt.Sprintf("m18: command of %d bytes exceeds the report size", len(cmd)))
	}
	b := make([]byte, PacketSize+1)
	copy(b[1:], cmdPrefix[:])
	copy(b[1+len(cmdPrefix):], cmd)
	return b
}

func wakeFrame() []byte      { return frame(opWake...) }
func sleepFrame() []byte     { return frame(opSleep...) }
func refreshFrame() []byte   { return frame(opRefresh...) }
func keepAliveFrame() []byte { return frame(opKeepAlive...) }

func clearFrame(slot byte) []byte {
	return frame(0x43, 0x4c, 0x45, 0x00, 0x00, 0x00, slot)
}

func brightnessFrame(percent int) []byte {
	return frame(0x4c, 0x49, 0x47, 0x00, 0x00, clampPercent(percent))
}

func ledBrightnessFrame(percent int) []byte {
	return frame(0x4c, 0x42, 0x4c, 0x49, 0x47, clampPercent(percent))
}

// ledFrame builds SETLB + one RGB triple per LED.
func ledFrame(colors []color.RGBA) ([]byte, error) {
	if len(colors) != LEDCount {
		return nil, fmt.Errorf("m18: expected %d LED colours, got %d", LEDCount, len(colors))
	}
	cmd := make([]byte, 0, 5+LEDCount*3)
	cmd = append(cmd, 0x53, 0x45, 0x54, 0x4c, 0x42) // SETLB
	for _, c := range colors {
		cmd = append(cmd, c.R, c.G, c.B)
	}
	return frame(cmd...), nil
}

// imageBatch builds the frames for one key image: the BAT header followed by the JPEG
// payload split into report sized chunks. The caller appends a refresh frame if needed.
func imageBatch(slot byte, jpeg []byte) [][]byte {
	size := len(jpeg)
	bat := frame(
		0x42, 0x41, 0x54, // BAT
		byte(size>>24), byte(size>>16), byte(size>>8), byte(size),
		slot,
	)

	frames := make([][]byte, 0, 1+(size/PacketSize)+1)
	frames = append(frames, bat)
	for offset := 0; offset < size; offset += PacketSize {
		end := offset + PacketSize
		if end > size {
			end = size
		}
		chunk := make([]byte, PacketSize+1)
		copy(chunk[1:], jpeg[offset:end])
		frames = append(frames, chunk)
	}
	return frames
}

func shutdownBatch() [][]byte {
	return [][]byte{frame(opShutdown...), frame(opSleep...)}
}

func clampPercent(percent int) byte {
	if percent < 0 {
		percent = 0
	}
	if percent > 100 {
		percent = 100
	}
	return byte(percent)
}
