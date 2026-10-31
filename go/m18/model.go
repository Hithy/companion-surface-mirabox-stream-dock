// Package m18 drives the Mirabox Stream Dock M18 over USB HID.
//
// Only the M18 (0x6603:0x1012 / 0x6603:0x1009) is supported. The protocol facts in this
// package were verified on hardware; see docs/DRIVER.zh-CN.md in the repository root.
package m18

import (
	"fmt"
	"image/color"
	"time"
)

const (
	VendorID         = 0x6603
	ProductIDEN      = 0x1012 // international firmware
	ProductIDCN      = 0x1009 // chinese firmware, same hardware
	PacketSize       = 1024   // write payload size, without the report id byte
	ImageWidth       = 64
	ImageHeight      = 64
	MaxImageBytes    = 10240
	LEDCount         = 24
	DefaultHeartbeat = 8 * time.Second

	// vendorHIDUsagePage is the usage page of the control interface. The second
	// interface of the device is a keyboard emulation (usage page 1) and must not be
	// opened for writing.
	vendorHIDUsagePage = 0xffa0
	vendorHIDUsage     = 0x01
)

// Kind distinguishes LCD keys from the physical buttons below the grid.
type Kind int

const (
	KindKey Kind = iota
	KindButton
)

// Key describes one LCD key.
type Key struct {
	Index   int    // 0-based index, used for drawing
	InputID byte   // key code in input reports
	Slot    byte   // image slot id used by the BAT command
	Name    string // "Key 1" .. "Key 15"
	Row     int
	Column  int
	ID      string // "row/column", same as the Companion surface control id
}

// Button describes one of the three non-LCD buttons below the grid.
type Button struct {
	Index  int
	Code   byte
	Name   string
	Row    int
	Column int
	ID     string
}

// imageSlots is the slot id per key, row major, top-left first. It is not the key code.
var imageSlots = [15]byte{
	0x0b, 0x0c, 0x0d, 0x0e, 0x0f,
	0x06, 0x07, 0x08, 0x09, 0x0a,
	0x01, 0x02, 0x03, 0x04, 0x05,
}

// Keys is the fixed M18 layout, read only.
var Keys = buildKeys()

// Buttons is the fixed M18 button layout, read only.
var Buttons = []Button{
	{Index: 0, Code: 0x25, Name: "Button 1", Row: 3, Column: 1, ID: "3/1"},
	{Index: 1, Code: 0x30, Name: "Button 2", Row: 3, Column: 2, ID: "3/2"},
	{Index: 2, Code: 0x31, Name: "Button 3", Row: 3, Column: 3, ID: "3/3"},
}

func buildKeys() []Key {
	keys := make([]Key, 15)
	for i := range keys {
		row, column := i/5, i%5
		keys[i] = Key{
			Index:   i,
			InputID: byte(i + 1),
			Slot:    imageSlots[i],
			Name:    fmt.Sprintf("Key %d", i+1),
			Row:     row,
			Column:  column,
			ID:      fmt.Sprintf("%d/%d", row, column),
		}
	}
	return keys
}

// Event is emitted for every key/button transition.
type Event struct {
	Kind    Kind
	Index   int
	ID      string
	Name    string
	Row     int
	Column  int
	Code    byte
	Pressed bool
}

// KeyForCode resolves an input code to a key or button event.
func KeyForCode(code byte, pressed bool) (Event, bool) {
	for _, k := range Keys {
		if k.InputID == code {
			return Event{
				Kind: KindKey, Index: k.Index, ID: k.ID, Name: k.Name,
				Row: k.Row, Column: k.Column, Code: code, Pressed: pressed,
			}, true
		}
	}
	for _, b := range Buttons {
		if b.Code == code {
			return Event{
				Kind: KindButton, Index: b.Index, ID: b.ID, Name: b.Name,
				Row: b.Row, Column: b.Column, Code: code, Pressed: pressed,
			}, true
		}
	}
	return Event{}, false
}

// Rgb is the LED colour type.
type Rgb = color.RGBA
