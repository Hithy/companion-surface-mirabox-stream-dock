package m18

import "fmt"

// ParseInput extracts the key code and the pressed state from an input report.
//
// Input reports are 512 bytes on the M18 and share the "ACK .. OK" prefix with command
// acknowledgements, so the prefix must not be used to filter them:
//
//	0..2  'ACK'   3..4 00 00   5..6 'OK'   7..8 00 00
//	9     key code (0x01..0x0f keys, 0x25/0x30/0x31 buttons)
//	10    state (0x01 pressed, 0x00 released)
func ParseInput(b []byte) (code byte, pressed bool, ok bool) {
	if len(b) < 11 {
		return 0, false, false
	}
	if b[9] == 0 {
		return 0, false, false
	}
	return b[9], b[10] != 0, true
}

// ErrUnknownInput is returned when a report carries a code that is not part of the M18
// layout (usually a command acknowledgement).
var ErrUnknownInput = fmt.Errorf("m18: input code not part of the layout")

// DecodeInput parses a report and resolves it into an Event.
func DecodeInput(b []byte) (Event, error) {
	code, pressed, ok := ParseInput(b)
	if !ok {
		return Event{}, ErrUnknownInput
	}
	event, ok := KeyForCode(code, pressed)
	if !ok {
		return Event{}, ErrUnknownInput
	}
	return event, nil
}
