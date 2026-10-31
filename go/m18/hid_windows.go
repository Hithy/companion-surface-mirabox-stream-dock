//go:build windows

package m18

import (
	"fmt"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
	"unsafe"
)

var (
	modCfgMgr32 = syscall.NewLazyDLL("cfgmgr32.dll")
	modHid      = syscall.NewLazyDLL("hid.dll")
	modKernel32 = syscall.NewLazyDLL("kernel32.dll")

	procCMGetDeviceInterfaceListSizeW = modCfgMgr32.NewProc("CM_Get_Device_Interface_List_SizeW")
	procCMGetDeviceInterfaceListW     = modCfgMgr32.NewProc("CM_Get_Device_Interface_ListW")

	procHidDGetAttributes         = modHid.NewProc("HidD_GetAttributes")
	procHidDGetSerialNumberString = modHid.NewProc("HidD_GetSerialNumberString")
	procHidDGetProductString      = modHid.NewProc("HidD_GetProductString")
	procHidDGetPreparsedData      = modHid.NewProc("HidD_GetPreparsedData")
	procHidDFreePreparsedData     = modHid.NewProc("HidD_FreePreparsedData")
	procHidPGetCaps               = modHid.NewProc("HidP_GetCaps")

	procCreateEventW        = modKernel32.NewProc("CreateEventW")
	procGetOverlappedResult = modKernel32.NewProc("GetOverlappedResult")
)

// GUID_DEVINTERFACE_HID
var hidClassGUID = syscall.GUID{
	Data1: 0x4d1e55b2,
	Data2: 0xf16f,
	Data3: 0x11cf,
	Data4: [8]byte{0x88, 0xcb, 0x00, 0x11, 0x11, 0x00, 0x00, 0x30},
}

const (
	crSuccess       = 0
	waitObject0     = 0x00000000
	waitTimeoutCode = 0x00000102
	infinite        = 0xffffffff
)

type hidAttributes struct {
	Size          uint32
	VendorID      uint16
	ProductID     uint16
	VersionNumber uint16
}

type hidpCaps struct {
	Usage                   uint16
	UsagePage               uint16
	InputReportByteLength   uint16
	OutputReportByteLength  uint16
	FeatureReportByteLength uint16
	Reserved                [17]uint16
}

// listPlatform enumerates HID interfaces via the device interface list, then queries the
// vendor id / usage page of every interface with a zero-access handle.
func listPlatform() ([]Info, error) {
	var length uint32
	r, _, err := procCMGetDeviceInterfaceListSizeW.Call(
		uintptr(unsafe.Pointer(&length)),
		uintptr(unsafe.Pointer(&hidClassGUID)),
		0,
		0,
	)
	if r != crSuccess {
		return nil, fmt.Errorf("m18: CM_Get_Device_Interface_List_Size failed: %w", err)
	}
	if length == 0 {
		return nil, nil
	}

	buf := make([]uint16, length)
	r, _, err = procCMGetDeviceInterfaceListW.Call(
		uintptr(unsafe.Pointer(&hidClassGUID)),
		0,
		uintptr(unsafe.Pointer(&buf[0])),
		uintptr(length),
		0,
	)
	if r != crSuccess {
		return nil, fmt.Errorf("m18: CM_Get_Device_Interface_List failed: %w", err)
	}

	var infos []Info
	for _, path := range splitMultiSz(buf) {
		info, err := describe(path)
		if err != nil {
			continue
		}
		infos = append(infos, info)
	}
	return infos, nil
}

// describe opens the interface without any access rights to read its attributes.
func describe(path string) (Info, error) {
	p, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		return Info{}, err
	}
	h, err := syscall.CreateFile(p, 0, syscall.FILE_SHARE_READ|syscall.FILE_SHARE_WRITE, nil, syscall.OPEN_EXISTING, 0, 0)
	if err != nil {
		return Info{}, err
	}
	defer syscall.CloseHandle(h)

	var attrs hidAttributes
	attrs.Size = uint32(unsafe.Sizeof(attrs))
	if r, _, _ := procHidDGetAttributes.Call(uintptr(h), uintptr(unsafe.Pointer(&attrs))); r == 0 {
		return Info{}, fmt.Errorf("m18: HidD_GetAttributes failed")
	}

	info := Info{
		Path:      path,
		VendorID:  attrs.VendorID,
		ProductID: attrs.ProductID,
		Serial:    hidString(procHidDGetSerialNumberString, h),
		Product:   hidString(procHidDGetProductString, h),
	}

	if usagePage, usage, ok := hidUsage(h); ok {
		info.UsagePage, info.Usage = usagePage, usage
		info.IsVendorIF = usagePage == vendorHIDUsagePage && usage == vendorHIDUsage
	}
	return info, nil
}

func hidString(proc *syscall.LazyProc, h syscall.Handle) string {
	buf := make([]uint16, 256)
	if r, _, _ := proc.Call(uintptr(h), uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)*2)); r == 0 {
		return ""
	}
	return syscall.UTF16ToString(buf)
}

func hidUsage(h syscall.Handle) (usagePage, usage uint16, ok bool) {
	var preparsed unsafe.Pointer
	if r, _, _ := procHidDGetPreparsedData.Call(uintptr(h), uintptr(unsafe.Pointer(&preparsed))); r == 0 || preparsed == nil {
		return 0, 0, false
	}
	defer procHidDFreePreparsedData.Call(uintptr(preparsed))

	var caps hidpCaps
	if r, _, _ := procHidPGetCaps.Call(uintptr(preparsed), uintptr(unsafe.Pointer(&caps))); r == 0 {
		return 0, 0, false
	}
	return caps.UsagePage, caps.Usage, true
}

func splitMultiSz(buf []uint16) []string {
	var out []string
	start := 0
	for i, c := range buf {
		if c != 0 {
			continue
		}
		if i == start {
			break // double NUL terminates the list
		}
		out = append(out, syscall.UTF16ToString(buf[start:i]))
		start = i + 1
	}
	return out
}

type winTransport struct {
	h       syscall.Handle
	readEv  syscall.Handle
	writeEv syscall.Handle

	readMu  sync.Mutex
	writeMu sync.Mutex
	closed  atomic.Bool
}

func openPlatform(path string) (Transport, error) {
	p, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		return nil, err
	}
	h, err := syscall.CreateFile(
		p,
		syscall.GENERIC_READ|syscall.GENERIC_WRITE,
		syscall.FILE_SHARE_READ|syscall.FILE_SHARE_WRITE,
		nil,
		syscall.OPEN_EXISTING,
		syscall.FILE_FLAG_OVERLAPPED,
		0,
	)
	if err != nil {
		return nil, fmt.Errorf("m18: CreateFile(%s): %w", path, err)
	}

	readEv, err := createEvent()
	if err != nil {
		syscall.CloseHandle(h)
		return nil, err
	}
	writeEv, err := createEvent()
	if err != nil {
		syscall.CloseHandle(h)
		syscall.CloseHandle(readEv)
		return nil, err
	}

	return &winTransport{h: h, readEv: readEv, writeEv: writeEv}, nil
}

func createEvent() (syscall.Handle, error) {
	// auto-reset, initially non-signalled
	r, _, err := procCreateEventW.Call(0, 0, 0, 0)
	if r == 0 {
		return 0, fmt.Errorf("m18: CreateEvent failed: %w", err)
	}
	return syscall.Handle(r), nil
}

func (t *winTransport) Read(buf []byte, timeout time.Duration) (int, error) {
	t.readMu.Lock()
	defer t.readMu.Unlock()

	if t.closed.Load() {
		return 0, ErrClosed
	}

	var ov syscall.Overlapped
	ov.HEvent = t.readEv
	var n uint32
	err := syscall.ReadFile(t.h, buf, &n, &ov)
	if err == nil {
		return int(n), nil
	}
	if err != syscall.ERROR_IO_PENDING {
		return 0, err
	}

	ms := uint32(timeout / time.Millisecond)
	res, werr := syscall.WaitForSingleObject(t.readEv, ms)
	if werr != nil {
		return 0, werr
	}
	if res == waitTimeoutCode {
		_ = syscall.CancelIoEx(t.h, &ov)
		syscall.WaitForSingleObject(t.readEv, infinite)
		return 0, ErrTimeout
	}

	var done uint32
	if err := getOverlappedResult(t.h, &ov, &done); err != nil {
		return 0, err
	}
	return int(done), nil
}

func (t *winTransport) Write(report []byte) error {
	t.writeMu.Lock()
	defer t.writeMu.Unlock()

	if t.closed.Load() {
		return ErrClosed
	}

	var ov syscall.Overlapped
	ov.HEvent = t.writeEv
	var n uint32
	err := syscall.WriteFile(t.h, report, &n, &ov)
	if err == nil {
		if int(n) != len(report) {
			return fmt.Errorf("m18: short HID write: %d of %d bytes", n, len(report))
		}
		return nil
	}
	if err != syscall.ERROR_IO_PENDING {
		return err
	}

	res, werr := syscall.WaitForSingleObject(t.writeEv, 5000)
	if werr != nil {
		return werr
	}
	if res == waitTimeoutCode {
		_ = syscall.CancelIoEx(t.h, &ov)
		syscall.WaitForSingleObject(t.writeEv, infinite)
		return ErrWriteTimeout
	}

	var done uint32
	if err := getOverlappedResult(t.h, &ov, &done); err != nil {
		return err
	}
	if int(done) != len(report) {
		return fmt.Errorf("m18: short HID write: %d of %d bytes", done, len(report))
	}
	return nil
}

func (t *winTransport) Close() error {
	if t.closed.Swap(true) {
		return nil
	}
	_ = syscall.CancelIoEx(t.h, nil)
	syscall.CloseHandle(t.h)
	syscall.CloseHandle(t.readEv)
	syscall.CloseHandle(t.writeEv)
	return nil
}

func getOverlappedResult(h syscall.Handle, ov *syscall.Overlapped, done *uint32) error {
	r, _, err := procGetOverlappedResult.Call(
		uintptr(h),
		uintptr(unsafe.Pointer(ov)),
		uintptr(unsafe.Pointer(done)),
		0,
	)
	if r == 0 {
		return err
	}
	return nil
}
