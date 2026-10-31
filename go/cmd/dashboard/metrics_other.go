//go:build !windows && !linux

package main

// collectPlatform and collectGPUPlatform are stubs on platforms without a backend; the
// dashboard then shows only the Go runtime statistics.
func collectPlatform(m *Metrics)    {}
func collectGPUPlatform(m *Metrics) {}
