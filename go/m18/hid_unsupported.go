//go:build !windows && !linux

package m18

import "errors"

// errUnsupportedOS is returned on platforms without a HID backend.
//
// macOS needs an IOHIDManager (or hidapi) backend, which requires cgo. Windows and Linux
// are implemented in pure Go, see hid_windows.go and hid_linux.go.
var errUnsupportedOS = errors.New(
	"m18: this platform is not supported yet (macOS requires an IOHIDManager backend with cgo)",
)

func listPlatform() ([]Info, error)          { return nil, errUnsupportedOS }
func openPlatform(string) (Transport, error) { return nil, errUnsupportedOS }
