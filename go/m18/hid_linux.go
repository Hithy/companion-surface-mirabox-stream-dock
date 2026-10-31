//go:build linux

package m18

import (
	"fmt"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
	"unsafe"
)

// listPlatform reads the hidraw class; see hid_sysfs.go for the parsing.
func listPlatform() ([]Info, error) {
	return listHidraw(sysfsRoot)
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

func (t *linuxTransport) Read(buf []byte) (int, error) {
	t.readMu.Lock()
	defer t.readMu.Unlock()

	if t.closed.Load() {
		return 0, ErrClosed
	}

	var set syscall.FdSet
	fdSetAdd(&set, t.fd)

	// nil timeout: block until a report arrives, or until Close closes the fd.
	n, err := syscall.Select(t.fd+1, &set, nil, nil, nil)
	if err != nil {
		if t.closed.Load() || err == syscall.EINTR {
			return 0, ErrClosed
		}
		return 0, err
	}
	if n == 0 {
		return 0, ErrClosed
	}

	read, err := syscall.Read(t.fd, buf)
	if err != nil {
		if err == syscall.EAGAIN || err == syscall.EWOULDBLOCK {
			return 0, ErrClosed
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

// fdSetAdd sets the bit for fd in a syscall.FdSet.
//
// FdSet.Bits is [16]int64 on 64 bit and [32]int32 on 32 bit Linux, so the bitmap is
// addressed byte wise. That is correct for the little endian architectures we target
// (amd64, arm64, arm, 386).
func fdSetAdd(set *syscall.FdSet, fd int) {
	bytes := (*[unsafe.Sizeof(syscall.FdSet{})]byte)(unsafe.Pointer(set))
	bytes[fd/8] |= 1 << (uint(fd) % 8)
}
