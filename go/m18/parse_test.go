package m18

import "testing"

// report builds an input report exactly as captured from an M18 (6603:1012) on Linux /
// through hidapi: 512 bytes, no leading report id.
//
//	41434b 0000 4f4b 0000 <code> <state> 0000...
func report(code, state byte) []byte {
	b := make([]byte, 512)
	copy(b, []byte{0x41, 0x43, 0x4b, 0x00, 0x00, 0x4f, 0x4b, 0x00, 0x00, code, state})
	return b
}

func TestDecodeInput(t *testing.T) {
	cases := []struct {
		name   string
		data   []byte
		wantOK bool
		want   Event
	}{
		{
			name:   "key 1 down",
			data:   report(0x01, 0x01),
			wantOK: true,
			want:   Event{Kind: KindKey, Index: 0, ID: "0/0", Name: "Key 1", Code: 0x01, Pressed: true},
		},
		{
			name:   "key 1 up",
			data:   report(0x01, 0x00),
			wantOK: true,
			want:   Event{Kind: KindKey, Index: 0, ID: "0/0", Name: "Key 1", Code: 0x01},
		},
		{
			name:   "key 15 down",
			data:   report(0x0f, 0x01),
			wantOK: true,
			want:   Event{Kind: KindKey, Index: 14, ID: "2/4", Name: "Key 15", Row: 2, Column: 4, Code: 0x0f, Pressed: true},
		},
		{
			name:   "button left down",
			data:   report(0x25, 0x01),
			wantOK: true,
			want:   Event{Kind: KindButton, Index: 0, ID: "3/1", Name: "Button 1", Row: 3, Column: 1, Code: 0x25, Pressed: true},
		},
		{
			name:   "button middle up",
			data:   report(0x30, 0x00),
			wantOK: true,
			want:   Event{Kind: KindButton, Index: 1, ID: "3/2", Name: "Button 2", Row: 3, Column: 2, Code: 0x30},
		},
		{
			name:   "button right down",
			data:   report(0x31, 0x01),
			wantOK: true,
			want:   Event{Kind: KindButton, Index: 2, ID: "3/3", Name: "Button 3", Row: 3, Column: 3, Code: 0x31, Pressed: true},
		},
		{name: "command ack", data: report(0x00, 0x00)},
		{name: "unknown code", data: report(0x7f, 0x01)},
		{name: "truncated", data: report(0x01, 0x01)[:10]},
	}

	// The same transitions must decode identically with and without the report id byte
	// that the Windows HID stack prepends.
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			checkDecode(t, c.data, c.wantOK, c.want)
			checkDecode(t, append([]byte{0x00}, c.data...), c.wantOK, c.want)
		})
	}
}

func checkDecode(t *testing.T, data []byte, wantOK bool, want Event) {
	t.Helper()
	event, err := DecodeInput(data)
	if !wantOK {
		if err == nil {
			t.Fatalf("expected an error, got %+v", event)
		}
		return
	}
	if err != nil {
		t.Fatal(err)
	}
	if event != want {
		t.Fatalf("event = %+v, want %+v", event, want)
	}
}
