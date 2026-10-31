package m18

import (
	"context"
	"errors"
	"fmt"
	"image"
	"image/color"
	"sync"
	"sync/atomic"
	"time"
)

// Options selects the device to open.
type Options struct {
	// Path opens a specific HID interface (wins over Serial).
	Path string
	// Serial opens the device with this serial number.
	Serial string
	// Heartbeat interval: 0 uses DefaultHeartbeat, a negative value disables it.
	Heartbeat time.Duration
}

// KeyImage is one key of a batch passed to Device.DrawKeys.
type KeyImage struct {
	Index   int
	Image   image.Image
	Options ImageOptions
}

// Device is an open Stream Dock M18.
type Device struct {
	info Info
	t    Transport

	out    chan writeRequest
	events chan Event
	raw    chan []byte
	errs   chan error

	done chan struct{}
	wg   sync.WaitGroup

	writeMu   sync.Mutex
	closeOnce sync.Once
	closed    atomic.Bool

	ledMu sync.Mutex
	leds  [LEDCount]color.RGBA

	pressedMu sync.Mutex
	pressed   map[byte]bool

	errMu sync.Mutex
	err   error
}

// Open finds and opens an M18.
//
// If the first report cannot be written (the device re-enumerated right after the previous
// process closed it) the open is retried once.
func Open(opts Options) (*Device, error) {
	var lastErr error
	for attempt := 0; attempt < 2; attempt++ {
		info, err := Find(opts)
		if err != nil {
			return nil, err
		}
		t, err := openTransport(info.Path)
		if err != nil {
			return nil, err
		}

		device, err := newDevice(info, t, opts.Heartbeat)
		if err == nil {
			return device, nil
		}
		_ = t.Close()
		lastErr = err
		if !errors.Is(err, ErrWriteTimeout) {
			return nil, err
		}
		time.Sleep(300 * time.Millisecond)
	}
	return nil, lastErr
}

// newDevice wires a transport into a Device; it is also used by the tests.
func newDevice(info Info, t Transport, heartbeat time.Duration) (*Device, error) {
	d := &Device{
		info:    info,
		t:       t,
		out:     make(chan writeRequest, 32),
		events:  make(chan Event, 64),
		raw:     make(chan []byte, 16),
		errs:    make(chan error, 8),
		done:    make(chan struct{}),
		pressed: make(map[byte]bool),
	}

	if heartbeat == 0 {
		heartbeat = DefaultHeartbeat
	}

	d.wg.Add(2)
	go d.writer()
	go d.reader()
	if heartbeat > 0 {
		d.wg.Add(1)
		go d.heartbeatLoop(heartbeat)
	}

	// Wake the device, otherwise some firmware revisions do not report inputs.
	if err := d.writeBatch([][]byte{wakeFrame()}); err != nil {
		_ = d.closeWith(false)
		return nil, err
	}

	return d, nil
}

// Info returns the HID interface that was opened.
func (d *Device) Info() Info { return d.info }

// Events is closed when the device is gone or closed. Always drain it:
//
//	for ev := range dock.Events() { ... }
func (d *Device) Events() <-chan Event { return d.events }

// Raw exposes every input report as received, for reverse engineering. Reports are
// dropped when the buffer is full.
func (d *Device) Raw() <-chan []byte { return d.raw }

// Errors reports non-fatal write problems (heartbeat, dropped batch).
func (d *Device) Errors() <-chan error { return d.errs }

// Err returns the fatal error that ended the read loop, if any.
func (d *Device) Err() error {
	d.errMu.Lock()
	defer d.errMu.Unlock()
	return d.err
}

// Close clears the screen and puts the device to sleep, like the vendor software does.
func (d *Device) Close() error { return d.closeWith(true) }

// CloseKeepImage closes the handle but leaves the current picture on the device.
func (d *Device) CloseKeepImage() error { return d.closeWith(false) }

func (d *Device) closeWith(shutdown bool) error {
	d.closeOnce.Do(func() {
		d.closed.Store(true)
		close(d.done) // stops the writer and heartbeat

		// Send the shutdown while the device is still open, then close the handle so the
		// reader (parked in a blocking read) wakes up.
		if shutdown {
			_ = d.writeBatch(shutdownBatch())
		}
		_ = d.t.Close()
		d.wg.Wait()
	})
	return nil
}

// ---------------------------------------------------------------- commands

// DrawKey renders one key image and refreshes the display.
func (d *Device) DrawKey(ctx context.Context, index int, img image.Image, opts ImageOptions) error {
	return d.DrawKeys(ctx, []KeyImage{{Index: index, Image: img, Options: opts}})
}

// DrawKeys renders several keys and sends them as one batch with a single refresh.
func (d *Device) DrawKeys(ctx context.Context, keys []KeyImage) error {
	if len(keys) == 0 {
		return nil
	}

	batch := make([][]byte, 0, len(keys)*4+1)
	for _, k := range keys {
		if k.Index < 0 || k.Index >= len(Keys) {
			return fmt.Errorf("m18: key index %d out of range (0..%d)", k.Index, len(Keys)-1)
		}
		jpeg, err := EncodeKeyImage(k.Image, k.Options)
		if err != nil {
			return err
		}
		batch = append(batch, imageBatch(Keys[k.Index].Slot, jpeg)...)
	}
	batch = append(batch, refreshFrame())
	return d.enqueue(ctx, batch)
}

// ClearKey blanks one key.
func (d *Device) ClearKey(ctx context.Context, index int) error {
	if index < 0 || index >= len(Keys) {
		return fmt.Errorf("m18: key index %d out of range (0..%d)", index, len(Keys)-1)
	}
	return d.enqueue(ctx, [][]byte{clearFrame(Keys[index].Slot), refreshFrame()})
}

// ClearAll blanks every key.
func (d *Device) ClearAll(ctx context.Context) error {
	return d.enqueue(ctx, [][]byte{clearFrame(0xff), refreshFrame()})
}

// SetBrightness sets the display brightness, 0..100.
func (d *Device) SetBrightness(ctx context.Context, percent int) error {
	return d.enqueue(ctx, [][]byte{brightnessFrame(percent)})
}

// SetLedBrightness sets the LED backlight brightness, 0..100.
func (d *Device) SetLedBrightness(ctx context.Context, percent int) error {
	return d.enqueue(ctx, [][]byte{ledBrightnessFrame(percent)})
}

// SetLedColors sets all LEDCount colours at once.
func (d *Device) SetLedColors(ctx context.Context, colors []color.RGBA) error {
	f, err := ledFrame(colors)
	if err != nil {
		return err
	}
	d.ledMu.Lock()
	copy(d.leds[:], colors)
	d.ledMu.Unlock()
	return d.enqueue(ctx, [][]byte{f})
}

// SetLed changes one LED, the others keep their colour.
func (d *Device) SetLed(ctx context.Context, index int, c color.RGBA) error {
	return d.UpdateLeds(ctx, map[int]color.RGBA{index: c})
}

// UpdateLeds changes the given LEDs, the others keep their colour.
func (d *Device) UpdateLeds(ctx context.Context, colors map[int]color.RGBA) error {
	d.ledMu.Lock()
	for index, c := range colors {
		if index < 0 || index >= LEDCount {
			d.ledMu.Unlock()
			return fmt.Errorf("m18: LED index %d out of range (0..%d)", index, LEDCount-1)
		}
		d.leds[index] = c
	}
	next := d.leds
	d.ledMu.Unlock()

	f, err := ledFrame(next[:])
	if err != nil {
		return err
	}
	return d.enqueue(ctx, [][]byte{f})
}

// SetAllLeds sets every LED to the same colour.
func (d *Device) SetAllLeds(ctx context.Context, c color.RGBA) error {
	return d.SetLedColors(ctx, filled(c))
}

// LedColors returns the last colours sent to the device.
func (d *Device) LedColors() []color.RGBA {
	d.ledMu.Lock()
	defer d.ledMu.Unlock()
	out := make([]color.RGBA, LEDCount)
	copy(out, d.leds[:])
	return out
}

// Wake sends DIS.
func (d *Device) Wake(ctx context.Context) error {
	return d.enqueue(ctx, [][]byte{wakeFrame()})
}

// Sleep sends HAN.
func (d *Device) Sleep(ctx context.Context) error {
	return d.enqueue(ctx, [][]byte{sleepFrame()})
}

// KeepAlive sends CONNECT manually; the driver does this on its own.
func (d *Device) KeepAlive(ctx context.Context) error {
	return d.enqueue(ctx, [][]byte{keepAliveFrame()})
}

func filled(c color.RGBA) []color.RGBA {
	out := make([]color.RGBA, LEDCount)
	for i := range out {
		out[i] = c
	}
	return out
}

// ---------------------------------------------------------------- internals

// enqueue hands a batch to the writer and waits until it has been written, so callers see
// real write errors and cannot outrun the device.
func (d *Device) enqueue(ctx context.Context, frames [][]byte) error {
	if d.closed.Load() {
		return ErrClosed
	}

	request := writeRequest{frames: frames, done: make(chan error, 1)}
	select {
	case d.out <- request:
	case <-ctx.Done():
		return ctx.Err()
	case <-d.done:
		return ErrClosed
	}

	select {
	case err := <-request.done:
		return err
	case <-ctx.Done():
		return ctx.Err()
	case <-d.done:
		return ErrClosed
	}
}

func (d *Device) writeBatch(batch [][]byte) error {
	d.writeMu.Lock()
	defer d.writeMu.Unlock()
	for _, report := range batch {
		if err := d.t.Write(report); err != nil {
			return err
		}
	}
	return nil
}

type writeRequest struct {
	frames [][]byte
	// done receives the result; nil for fire-and-forget writes (heartbeat).
	done chan error
}

func (d *Device) writer() {
	defer d.wg.Done()
	for {
		select {
		case request := <-d.out:
			err := d.writeBatch(request.frames)
			if request.done != nil {
				select {
				case request.done <- err:
				default:
				}
			}
			if err != nil && !errors.Is(err, ErrClosed) {
				d.reportError(err)
			}
		case <-d.done:
			return
		}
	}
}

func (d *Device) reader() {
	defer d.wg.Done()
	defer close(d.events)
	defer close(d.raw)

	readLen := d.info.InputReportLength
	if readLen < 64 {
		readLen = 1024
	}
	buf := make([]byte, readLen)
	for {
		n, err := d.t.Read(buf)
		if err != nil {
			if errors.Is(err, ErrClosed) || d.closed.Load() {
				return
			}
			d.setErr(err)
			return
		}

		raw := make([]byte, n)
		copy(raw, buf[:n])
		select {
		case d.raw <- raw:
		default:
		}

		event, err := DecodeInput(buf[:n])
		if err != nil {
			continue // command acknowledgement or an unknown code
		}

		// Some firmware revisions repeat the last state, only forward transitions.
		d.pressedMu.Lock()
		if d.pressed[event.Code] == event.Pressed {
			d.pressedMu.Unlock()
			continue
		}
		d.pressed[event.Code] = event.Pressed
		d.pressedMu.Unlock()

		select {
		case d.events <- event:
		case <-d.done:
			return
		}
	}
}

func (d *Device) heartbeatLoop(interval time.Duration) {
	defer d.wg.Done()
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			select {
			case d.out <- writeRequest{frames: [][]byte{keepAliveFrame()}}:
			default: // queue full, skip this beat
			}
		case <-d.done:
			return
		}
	}
}

func (d *Device) reportError(err error) {
	select {
	case d.errs <- err:
	default:
	}
}

func (d *Device) setErr(err error) {
	d.errMu.Lock()
	d.err = err
	d.errMu.Unlock()
	d.reportError(err)
}
