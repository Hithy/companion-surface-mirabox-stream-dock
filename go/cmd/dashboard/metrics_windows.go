//go:build windows

package main

import (
	"syscall"
	"time"
	"unsafe"
)

var (
	kernel32 = syscall.NewLazyDLL("kernel32.dll")

	procGetSystemTimes       = kernel32.NewProc("GetSystemTimes")
	procGlobalMemoryStatusEx = kernel32.NewProc("GlobalMemoryStatusEx")
	procGetTickCount64       = kernel32.NewProc("GetTickCount64")
)

type filetime struct {
	Low  uint32
	High uint32
}

func (f filetime) value() uint64 { return uint64(f.High)<<32 | uint64(f.Low) }

type memoryStatusEx struct {
	Length               uint32
	MemoryLoad           uint32
	TotalPhys            uint64
	AvailPhys            uint64
	TotalPageFile        uint64
	AvailPageFile        uint64
	TotalVirtual         uint64
	AvailVirtual         uint64
	AvailExtendedVirtual uint64
}

var (
	lastIdle   uint64
	lastKernel uint64
	lastUser   uint64
	lastValid  bool
)

// collectPlatform reads CPU load, memory load and uptime through kernel32, no cgo and no
// extra dependencies.
func collectPlatform(m *Metrics) {
	var idle, kernel, user filetime
	ok, _, _ := procGetSystemTimes.Call(
		uintptr(unsafe.Pointer(&idle)),
		uintptr(unsafe.Pointer(&kernel)),
		uintptr(unsafe.Pointer(&user)),
	)
	if ok != 0 {
		i, k, u := idle.value(), kernel.value(), user.value()
		if lastValid {
			// kernel time includes idle time
			idleDelta := float64(i - lastIdle)
			total := float64(k-lastKernel) + float64(u-lastUser)
			if total > 0 {
				m.CPU = clampPercent((total - idleDelta) / total * 100)
			}
		}
		lastIdle, lastKernel, lastUser = i, k, u
		lastValid = true
	}

	status := memoryStatusEx{Length: uint32(unsafe.Sizeof(memoryStatusEx{}))}
	if ok, _, _ := procGlobalMemoryStatusEx.Call(uintptr(unsafe.Pointer(&status))); ok != 0 {
		m.Mem = float64(status.MemoryLoad)
	}

	if ticks, _, _ := procGetTickCount64.Call(); ticks != 0 {
		m.Uptime = time.Duration(ticks) * time.Millisecond
	}
}
