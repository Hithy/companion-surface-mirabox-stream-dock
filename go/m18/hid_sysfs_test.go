package m18

import (
	"os"
	"path/filepath"
	"testing"
)

const vendorUevent = `DRIVER=hid-generic
HID_ID=0003:00006603:00001012
HID_NAME=HOTSPOTEKUSB HID DEMO
HID_PHYS=usb-0000:00:14.0-1/input0
HID_UNIQ=08A903783D06
`

const keyboardUevent = `DRIVER=hid-generic
HID_ID=0003:00006603:00001012
HID_NAME=HOTSPOTEKUSB HID DEMO
HID_PHYS=usb-0000:00:14.0-1/input1
HID_UNIQ=08A903783D06
`

func TestParseHidrawUevent(t *testing.T) {
	info := parseHidrawUevent([]byte(vendorUevent), "/dev/hidraw3")

	if info.Path != "/dev/hidraw3" {
		t.Fatalf("path = %q", info.Path)
	}
	if info.VendorID != 0x6603 || info.ProductID != 0x1012 {
		t.Fatalf("vid/pid = %04x:%04x", info.VendorID, info.ProductID)
	}
	if info.Serial != "08A903783D06" {
		t.Fatalf("serial = %q", info.Serial)
	}
	if info.Product != "HOTSPOTEKUSB HID DEMO" {
		t.Fatalf("product = %q", info.Product)
	}
}

func TestParseHidrawUeventGarbage(t *testing.T) {
	info := parseHidrawUevent([]byte("HID_ID=nonsense\nHID_NAME=\n"), "/dev/hidraw9")
	if info.VendorID != 0 || info.ProductID != 0 {
		t.Fatalf("expected zero ids, got %04x:%04x", info.VendorID, info.ProductID)
	}
	if info.Product != "" {
		t.Fatalf("product = %q", info.Product)
	}
}

func TestReportDescriptorUsage(t *testing.T) {
	// Vendor collection: usage page 0xFFA0, usage 1.
	vendor := []byte{0x06, 0xa0, 0xff, 0x09, 0x01, 0xa1, 0x01}
	if page, usage, ok := reportDescriptorUsage(vendor); !ok || page != 0xffa0 || usage != 1 {
		t.Fatalf("vendor descriptor -> %#x/%#x ok=%v", page, usage, ok)
	}

	// Keyboard collection: usage page 1, usage 6.
	keyboard := []byte{0x05, 0x01, 0x09, 0x06, 0xa1, 0x01}
	if _, _, ok := reportDescriptorUsage(keyboard); ok {
		t.Fatal("the short-form usage page (0x05) must not be reported as 0xffa0")
	}

	if _, _, ok := reportDescriptorUsage([]byte{0x06, 0xa0}); ok {
		t.Fatal("a truncated descriptor must not parse")
	}
}

func TestListHidraw(t *testing.T) {
	root := t.TempDir()

	write := func(name string, files map[string][]byte) {
		dir := filepath.Join(root, name, "device")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		for file, data := range files {
			if err := os.WriteFile(filepath.Join(dir, file), data, 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}

	write("hidraw0", map[string][]byte{
		"uevent":            []byte(vendorUevent),
		"report_descriptor": {0x06, 0xa0, 0xff, 0x09, 0x01, 0xa1, 0x01},
	})
	write("hidraw1", map[string][]byte{
		"uevent":            []byte(keyboardUevent),
		"report_descriptor": {0x05, 0x01, 0x09, 0x06, 0xa1, 0x01},
	})
	// A node without the expected files must be skipped.
	write("hidraw2", map[string][]byte{})

	infos, err := listHidraw(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(infos) != 2 {
		t.Fatalf("got %d interfaces, want 2: %+v", len(infos), infos)
	}

	byPath := map[string]Info{}
	for _, info := range infos {
		byPath[info.Path] = info
	}

	control, ok := byPath["/dev/hidraw0"]
	if !ok || !control.IsVendorIF || control.UsagePage != 0xffa0 || control.Usage != 1 {
		t.Fatalf("control interface = %+v", control)
	}
	secondary, ok := byPath["/dev/hidraw1"]
	if !ok || secondary.IsVendorIF {
		t.Fatalf("secondary interface should not be the control one: %+v", secondary)
	}
}
