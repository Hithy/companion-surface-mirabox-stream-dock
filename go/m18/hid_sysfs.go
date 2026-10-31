package m18

import (
	"encoding/binary"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// sysfsRoot is where the hidraw class is mounted. It is a variable so the discovery logic
// can be tested with a fake tree on any platform.
var sysfsRoot = "/sys/class/hidraw"

// parseHidrawUevent extracts the device description from a hidraw uevent file:
//
//	DRIVER=hid-generic
//	HID_ID=0003:00006603:00001012
//	HID_NAME=HOTSPOTEKUSB HID DEMO
//	HID_PHYS=usb-0000:00:14.0-1/input0
//	HID_UNIQ=08A903783D06
func parseHidrawUevent(data []byte, path string) Info {
	info := Info{Path: path}
	for _, line := range strings.Split(string(data), "\n") {
		switch {
		case strings.HasPrefix(line, "HID_ID="):
			// bus:vendor:product, all 8 hex digits
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
	return info
}

// reportDescriptorUsage extracts the first Usage Page / Usage pair of a HID report
// descriptor, encoded as 06 <page:2> 09 <usage>. The M18 exposes two collections: the
// control interface starts with usage page 0xFFA0 / usage 1, the keyboard emulation with
// usage page 1 / usage 6.
func reportDescriptorUsage(data []byte) (usagePage, usage uint16, ok bool) {
	if len(data) < 5 || data[0] != 0x06 || data[3] != 0x09 {
		return 0, 0, false
	}
	return binary.LittleEndian.Uint16(data[1:3]), uint16(data[4]), true
}

// listHidraw scans a hidraw sysfs class directory and returns one Info per node.
func listHidraw(root string) ([]Info, error) {
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, err
	}

	var out []Info
	for _, entry := range entries {
		base := filepath.Join(root, entry.Name(), "device")
		uevent, err := os.ReadFile(filepath.Join(base, "uevent"))
		if err != nil {
			continue
		}

		info := parseHidrawUevent(uevent, "/dev/"+entry.Name())
		if descriptor, err := os.ReadFile(filepath.Join(base, "report_descriptor")); err == nil {
			if usagePage, usage, ok := reportDescriptorUsage(descriptor); ok {
				info.UsagePage, info.Usage = usagePage, usage
				info.IsVendorIF = usagePage == vendorHIDUsagePage && usage == vendorHIDUsage
			}
		}
		out = append(out, info)
	}
	return out, nil
}
