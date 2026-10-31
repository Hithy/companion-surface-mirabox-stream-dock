package m18

import (
	"bytes"
	"context"
	"image/color"
	"sync"
	"testing"
	"time"
)

type mockTransport struct {
	mu     sync.Mutex
	writes [][]byte
	reads  chan []byte
	closed bool
}

func newMockTransport() *mockTransport {
	return &mockTransport{reads: make(chan []byte, 8)}
}

func (m *mockTransport) Write(report []byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return ErrClosed
	}
	m.writes = append(m.writes, append([]byte(nil), report...))
	return nil
}

func (m *mockTransport) Read(buf []byte) (int, error) {
	report, ok := <-m.reads
	if !ok {
		return 0, ErrClosed
	}
	return copy(buf, report), nil
}

func (m *mockTransport) Close() error {
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return nil
	}
	m.closed = true
	m.mu.Unlock()
	close(m.reads) // wake the blocked Read
	return nil
}

func (m *mockTransport) snapshot() [][]byte {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([][]byte, len(m.writes))
	copy(out, m.writes)
	return out
}

func waitWrites(t *testing.T, m *mockTransport, n int) [][]byte {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if writes := m.snapshot(); len(writes) >= n {
			return writes
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("only %d writes arrived, want %d", len(m.snapshot()), n)
	return nil
}

func newTestDevice(t *testing.T) (*Device, *mockTransport) {
	t.Helper()
	mock := newMockTransport()
	device, err := newDevice(Info{Serial: "test"}, mock, -1) // heartbeat disabled
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = device.CloseKeepImage() })
	return device, mock
}

func TestOpenSendsWake(t *testing.T) {
	_, mock := newTestDevice(t)
	writes := waitWrites(t, mock, 1)
	if string(writes[0][6:9]) != "DIS" {
		t.Fatalf("first frame = % x, want DIS", writes[0][6:9])
	}
}

func TestDrawKeysIsOneBatch(t *testing.T) {
	device, mock := newTestDevice(t)
	waitWrites(t, mock, 1) // wake

	ctx := context.Background()
	img := solid(8, 8, color.RGBA{0, 128, 255, 255})
	if err := device.DrawKeys(ctx, []KeyImage{
		{Index: 0, Image: img},
		{Index: 14, Image: img},
	}); err != nil {
		t.Fatal(err)
	}

	writes := waitWrites(t, mock, 1+2+1) // wake + 2x(BAT+chunk) + STP
	bats := 0
	for _, w := range writes {
		switch string(w[6:9]) {
		case "BAT":
			bats++
		case "STP":
			if bats != 2 {
				t.Fatalf("STP after %d BAT frames, want 2", bats)
			}
		}
	}
	if bats != 2 {
		t.Fatalf("got %d BAT frames, want 2", bats)
	}
	if slot := writes[1][13]; slot != Keys[0].Slot {
		t.Fatalf("first BAT slot = %#x, want %#x", slot, Keys[0].Slot)
	}
}

func TestEventsAndDedupe(t *testing.T) {
	device, mock := newTestDevice(t)
	waitWrites(t, mock, 1)

	mock.reads <- report(0x03, 0x01) // key 3 down
	mock.reads <- report(0x03, 0x01) // duplicate, must be filtered
	mock.reads <- report(0x03, 0x00) // key 3 up

	var got []Event
	timeout := time.After(3 * time.Second)
	for len(got) < 2 {
		select {
		case event := <-device.Events():
			got = append(got, event)
		case <-timeout:
			t.Fatalf("got %d events, want 2: %+v", len(got), got)
		}
	}

	if got[0].Index != 2 || !got[0].Pressed || got[0].Kind != KindKey {
		t.Fatalf("first event = %+v", got[0])
	}
	if got[1].Index != 2 || got[1].Pressed {
		t.Fatalf("second event = %+v", got[1])
	}
}

func TestButtonEvent(t *testing.T) {
	device, mock := newTestDevice(t)
	waitWrites(t, mock, 1)

	mock.reads <- report(0x31, 0x01)

	select {
	case event := <-device.Events():
		if event.Kind != KindButton || event.Index != 2 || event.ID != "3/3" {
			t.Fatalf("event = %+v", event)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("no event")
	}
}

func TestCloseSendsShutdown(t *testing.T) {
	device, mock := newTestDevice(t)
	waitWrites(t, mock, 1)

	if err := device.Close(); err != nil {
		t.Fatal(err)
	}

	writes := mock.snapshot()
	if len(writes) != 3 {
		t.Fatalf("got %d writes, want 3 (wake + CLE DC + HAN)", len(writes))
	}
	if !bytes.Equal(writes[1][6:13], []byte{0x43, 0x4c, 0x45, 0x00, 0x00, 0x44, 0x43}) {
		t.Fatalf("shutdown frame = % x", writes[1][6:13])
	}
	if string(writes[2][6:9]) != "HAN" {
		t.Fatalf("sleep frame = % x, want HAN", writes[2][6:9])
	}

	// Events must be closed once the device is closed.
	select {
	case _, ok := <-device.Events():
		if ok {
			t.Fatal("expected the events channel to be closed")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("events channel was not closed")
	}

	// Second close is a no-op.
	if err := device.Close(); err != nil {
		t.Fatal(err)
	}
	if got := len(mock.snapshot()); got != 3 {
		t.Fatalf("close wrote more frames (%d)", got)
	}
}

func TestLedState(t *testing.T) {
	device, mock := newTestDevice(t)
	waitWrites(t, mock, 1)

	ctx := context.Background()
	if err := device.SetLed(ctx, 3, color.RGBA{255, 0, 0, 255}); err != nil {
		t.Fatal(err)
	}
	colors := device.LedColors()
	if colors[3].R != 255 || colors[0].R != 0 {
		t.Fatalf("led state = %v", colors)
	}

	if err := device.SetLed(ctx, 99, color.RGBA{}); err == nil {
		t.Fatal("expected an error for an out of range LED")
	}

	writes := waitWrites(t, mock, 2)
	ledFrame := writes[1]
	if string(ledFrame[6:11]) != "SETLB" {
		t.Fatalf("opcode = %q", ledFrame[6:11])
	}
	if ledFrame[11+3*3] != 255 {
		t.Fatalf("led 3 red = %d", ledFrame[11+3*3])
	}
}

func TestClosedDeviceRejectsCommands(t *testing.T) {
	device, mock := newTestDevice(t)
	waitWrites(t, mock, 1)
	_ = device.CloseKeepImage()

	if err := device.SetBrightness(context.Background(), 50); err == nil {
		t.Fatal("expected ErrClosed")
	}
}
