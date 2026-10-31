package main

import (
	"runtime"
	"time"
)

const (
	tileW = 64
	tileH = 64
)

// Metrics is what the dashboard shows. CPU/Mem are 0..100, Uptime is system uptime.
type Metrics struct {
	CPU        float64
	Mem        float64
	Uptime     time.Duration
	Goroutines int
	HeapMB     float64
	FPS        float64
}

// collect fills the portable fields, then asks the platform for CPU/memory/uptime.
func collect(m *Metrics) {
	var memStats runtime.MemStats
	runtime.ReadMemStats(&memStats)

	m.Goroutines = runtime.NumGoroutine()
	m.HeapMB = float64(memStats.HeapAlloc) / (1024 * 1024)

	collectPlatform(m)
}

func clampPercent(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 100 {
		return 100
	}
	return v
}
