//go:build linux

package main

import (
	"bufio"
	"os"
	"strconv"
	"strings"
	"time"
)

var (
	lastCPUIdle  uint64
	lastCPUTotal uint64
	lastValid    bool
)

func collectPlatform(m *Metrics) {
	if file, err := os.Open("/proc/stat"); err == nil {
		scanner := bufio.NewScanner(file)
		if scanner.Scan() {
			fields := strings.Fields(scanner.Text())
			if len(fields) >= 5 && fields[0] == "cpu" {
				var values []uint64
				for _, field := range fields[1:] {
					v, err := strconv.ParseUint(field, 10, 64)
					if err != nil {
						values = nil
						break
					}
					values = append(values, v)
				}
				// user nice system idle iowait irq softirq steal
				if len(values) >= 4 {
					var total uint64
					for _, v := range values {
						total += v
					}
					idle := values[3]
					if len(values) > 4 {
						idle += values[4]
					}
					if lastValid && total > lastCPUTotal {
						totalDelta := float64(total - lastCPUTotal)
						idleDelta := float64(idle - lastCPUIdle)
						m.CPU = clampPercent((totalDelta - idleDelta) / totalDelta * 100)
					}
					lastCPUTotal, lastCPUIdle, lastValid = total, idle, true
				}
			}
		}
		file.Close()
	}

	if file, err := os.Open("/proc/meminfo"); err == nil {
		var total, available uint64
		scanner := bufio.NewScanner(file)
		for scanner.Scan() {
			fields := strings.Fields(scanner.Text())
			if len(fields) < 2 {
				continue
			}
			value, err := strconv.ParseUint(fields[1], 10, 64)
			if err != nil {
				continue
			}
			switch fields[0] {
			case "MemTotal:":
				total = value
			case "MemAvailable:":
				available = value
			}
		}
		file.Close()
		if total > 0 {
			m.Mem = clampPercent(float64(total-available) / float64(total) * 100)
		}
	}

	if data, err := os.ReadFile("/proc/uptime"); err == nil {
		if fields := strings.Fields(string(data)); len(fields) > 0 {
			if seconds, err := strconv.ParseFloat(fields[0], 64); err == nil {
				m.Uptime = time.Duration(seconds * float64(time.Second))
			}
		}
	}
}
