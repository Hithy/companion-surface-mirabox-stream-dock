package main

import (
	"runtime"
	"time"
)

const (
	tileW = 64
	tileH = 64
)

// ClusterStat is one aggregated CPU group (e.g. P-cores and E-cores on a hybrid chip).
type ClusterStat struct {
	Name    string
	Percent float64
}

// CoreStat is one logical CPU: utilisation plus the current clock in MHz (0 = unknown).
type CoreStat struct {
	Index   int
	Percent float64
	MHz     uint32
}

// Metrics is what the dashboard shows. GPU/VRAM are negative when unavailable.
type Metrics struct {
	CPU         float64
	Clusters    []ClusterStat
	Cores       []CoreStat
	Mem         float64
	GPU         float64
	VRAMUsedMB  float64
	VRAMTotalMB float64
	Uptime      time.Duration
	Goroutines  int
	HeapMB      float64
	FPS         float64
}

// collect fills the portable fields, then asks the platform for CPU/memory/GPU/uptime.
func collect(m *Metrics) {
	var memStats runtime.MemStats
	runtime.ReadMemStats(&memStats)

	m.Goroutines = runtime.NumGoroutine()
	m.HeapMB = float64(memStats.HeapAlloc) / (1024 * 1024)

	m.GPU = -1
	m.VRAMUsedMB = -1
	m.VRAMTotalMB = 0

	collectPlatform(m)
	collectGPUPlatform(m)
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
