//go:build !windows && !linux

package main

// collectPlatform is a stub on platforms without a metrics backend.
func collectPlatform(m *Metrics) {}
