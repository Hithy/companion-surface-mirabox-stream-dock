//go:build linux

package m18

import (
	"encoding/binary"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
)

// listPlatform reads the hidraw class, which exposes the vendor/product id, the serial
// number and the report descriptor (used to tell the control interface from the keyboard
// emulation interface).
func listPlatform() ([]Info, error) {
	entries, err := os.ReadDir("/sys/class/hidraw")
	if err != nil {
		return nil, err
	}

	var out []Info
	for _, entry := range entries {
		base := filepath.Join("/sys/class/hidraw", entry.Name(), "device")
		uevent, err := os.ReadFile(filepath.Join(base, "uevent"))
		if err != nil {
			continue
		}

		info := Info{Path: "/dev/" + entry.Name()}
		for _, line := range strings.Split(string(uevent), "\n") {
			switch {
			case strings.HasPrefix(line, "HID_ID="):
				// 0003:00006603:00001012
				parts := strings.Split(strings.TrimPrefix(line, "HID_ID="), ":")
				if len(parts) == 3 {
					if v, err := strconv.ParseUint(parts[1], 16, 32); err == nil {
						info.VendorID = uint16(v)
					}
					if v, err := strconv.ParseUint(parts[2], 16, 32); err == nil {
						info.ProductID = uint16(v)
					}
				}
			case strings.HasPrefix(line, "HID_NAME="):
				info.Product = strings.TrimPrefix(line, "HID_NAME=")
			case strings.HasPrefix(line, "HID_UNIQ="):
				info.Serial = strings.TrimPrefix(line, "HID_UNIQ=")
			}
		}

		if usagePage, usage, ok := reportDescriptorUsage(filepath.Join(base, "report_descriptor")); ok {
			info.UsagePage, info.Usage = usagePage, usage
			info.IsVendorIF = usagePage == vendorHIDUsagePage && usage == vendorHIDUsage
		}
		out = append(out, info)
	}
	return out, nil
}

// reportDescriptorUsage extracts the first Usage Page / Usage pair of a HID report
// descriptor: 06 <page:2> 09 <usage>.
func reportDescriptorUsage(path string) (uint16, uint16, bool) {
	b, err := os.ReadFile(path)
	if err != nil || len(b) < 5 {
		return 0, 0, false
	}
	if b[0] != 0x06 || b[3] != 0x09 {
		return 0, 0, false
	}
	return binary.LittleEndian.Uint16(b[1:3]), uint16(b[4]), true
}

type linuxTransport struct {
	fd     int
	readMu sync.Mutex
	wrMu   sync.Mutex
	closed atomic.Bool
}

func openPlatform(path string) (Transport, error) {
	fd, err := syscall.Open(path, syscall.O_RDWR|syscall.O_NONBLOCK|syscall.O_CLOEXEC, 0)
	if err != nil {
		return nil, fmt.Errorf("m18: open %s: %w (check the udev rules)", path, err)
	}
	return &linuxTransport{fd: fd}, nil
}

func (t *linuxTransport) Read(buf []byte, timeout time.Duration) (int, error) {
	t.readMu.Lock()
	defer t.readMu.Unlock()

	if t.closed.Load() {
		return 0, ErrClosed
	}

	var set syscall.FdSet
	set.Bits[t.fd/64] |= int64(1) << (uint(t.fd) % 64)
	tv := syscall.NsecToTimeval(timeout.Nanoseconds())

	n, err := syscall.Select(t.fd+1, &set, nil, nil, &tv)
	if err != nil {
		if t.closed.Load() {
			return 0, ErrClosed
		}
		if err == syscall.EINTR {
			return 0, ErrTimeout
		}
		return 0, err
	}
	if n == 0 {
		return 0, ErrTimeout
	}

	read, err := syscall.Read(t.fd, buf)
	if err != nil {
		if err == syscall.EAGAIN || err == syscall.EWOULDBLOCK {
			return 0, ErrTimeout
		}
		if t.closed.Load() {
			return 0, ErrClosed
		}
		return 0, err
	}
	return read, nil
}

func (t *linuxTransport) Write(report []byte) error {
	t.wrMu.Lock()
	defer t.wrMu.Unlock()

	if t.closed.Load() {
		return ErrClosed
	}
	for offset := 0; offset < len(report); {
		n, err := syscall.Write(t.fd, report[offset:])
		if err != nil {
			if err == syscall.EAGAIN || err == syscall.EWOULDBLOCK {
				time.Sleep(time.Millisecond)
				continue
			}
			return err
		}
		offset += n
	}
	return nil
}

func (t *linuxTransport) Close() error {
	if t.closed.Swap(true) {
		return nil
	}
	return syscall.Close(t.fd)
}
