package m18

import "fmt"

// Input report layout (no leading report id byte):
//
//	0..2  'ACK'   3..4 00 00   5..6 'OK'   7..8 00 00
//	9     key code (0x01..0x0f keys, 0x25/0x30/0x31 buttons)
//	10    state (0x01 pressed, 0x00 released)
//
// The prefix must not be used to filter reports: key events use it too, only command
// acknowledgements have a zero code.
//
// Transport differences:
//   - Linux hidraw returns 512 bytes starting with 'A'
//   - Windows ReadFile returns 513 bytes because the HID stack prepends the report id
//     byte (0x00, the device has no numbered reports). hidapi strips it, so node-hid
//     reported 512 bytes; this driver accepts both forms.
const (
	inputHeaderLength = 11
	inputSignature    = "ACK"
)

// inputOffset returns the offset of the 'ACK' signature, accepting an optional report id
// byte in front of it.
func inputOffset(b []byte) (int, bool) {
	if len(b) >= len(inputSignature) && string(b[:3]) == inputSignature {
		return 0, true
	}
	if len(b) >= len(inputSignature)+1 && b[0] == 0x00 && string(b[1:4]) == inputSignature {
		return 1, true
	}
	return 0, false
}

// ParseInput extracts the key code and the pressed state from an input report.
func ParseInput(b []byte) (code byte, pressed bool, ok bool) {
	offset, hasSignature := inputOffset(b)
	if !hasSignature || len(b) < offset+inputHeaderLength {
		return 0, false, false
	}
	code = b[offset+9]
	if code == 0 {
		return 0, false, false
	}
	return code, b[offset+10] != 0, true
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
