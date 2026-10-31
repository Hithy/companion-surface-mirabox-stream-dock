package m18

import (
	"errors"
	"time"
)

// Common transport errors.
var (
	// ErrClosed means the device handle is already closed.
	ErrClosed = errors.New("m18: device is closed")
	// ErrWriteTimeout means the device did not accept a report in time.
	ErrWriteTimeout = errors.New("m18: HID write timed out")
	// ErrNotFound means no matching device is connected.
	ErrNotFound = errors.New("m18: no Stream Dock M18 found (is the vendor software running?)")
)

// Info describes one HID interface of a Stream Dock device.
type Info struct {
	Path       string
	VendorID   uint16
	ProductID  uint16
	Serial     string
	Product    string
	UsagePage  uint16
	Usage      uint16
	IsVendorIF bool // control interface we can write to

	// Report lengths reported by the device (0 when unknown). The Windows HID driver
	// rejects a read buffer that is larger than the input report length.
	InputReportLength  int
	OutputReportLength int
}

// Transport is a minimal HID interface, implemented per platform.
type Transport interface {
	// Write sends one padded report (report id + payload).
	Write([]byte) error
	// Read blocks until one input report arrives, then fills buf and returns the number
	// of bytes read. It returns ErrClosed when the device is closed.
	Read(buf []byte) (int, error)
	Close() error
}

// List returns the HID interfaces of known Stream Dock devices.
func List() ([]Info, error) {
	infos, err := listPlatform()
	if err != nil {
		return nil, err
	}
	out := infos[:0]
	for _, info := range infos {
		if info.VendorID == VendorID && (info.ProductID == ProductIDEN || info.ProductID == ProductIDCN) {
			out = append(out, info)
		}
	}
	return out, nil
}

// Find returns the interface matching the options. An empty Serial and Path returns the
// first control interface found.
//
// The device can disappear for a moment while it re-enumerates, so the lookup is retried
// for about a second before giving up.
func Find(opts Options) (Info, error) {
	var lastErr error = ErrNotFound
	for attempt := 0; attempt < 5; attempt++ {
		infos, err := List()
		if err != nil {
			lastErr = err
		} else {
			for _, info := range infos {
				if opts.Path != "" && info.Path != opts.Path {
					continue
				}
				if opts.Serial != "" && info.Serial != opts.Serial {
					continue
				}
				if !info.IsVendorIF {
					continue
				}
				return info, nil
			}
		}
		if attempt < 4 {
			time.Sleep(200 * time.Millisecond)
		}
	}
	return Info{}, lastErr
}

func openTransport(path string) (Transport, error) { return openPlatform(path) }
