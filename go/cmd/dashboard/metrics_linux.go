//go:build linux

package main

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

var (
	linuxClusters  []ClusterStat
	linuxClusterOf []int
	linuxPrevIdle  []uint64
	linuxPrevTotal []uint64
	linuxCPUReady  bool

	gpuLinuxValid   bool
	gpuLinuxAt      time.Time
	gpuLinuxUtil    float64
	gpuLinuxUsedMB  float64
	gpuLinuxTotalMB float64
)

// initClusters groups the logical CPUs by cpu_capacity, which is how the kernel exposes
// asymmetric (big/little) cores. Without it everything ends up in a single cluster.
func initClusters(count int) {
	capacities := make([]int, count)
	for i := 0; i < count; i++ {
		data, err := os.ReadFile(fmt.Sprintf("/sys/devices/system/cpu/cpu%d/cpu_capacity", i))
		if err != nil {
			capacities[i] = -1
			continue
		}
		value, err := strconv.Atoi(strings.TrimSpace(string(data)))
		if err != nil {
			capacities[i] = -1
			continue
		}
		capacities[i] = value
	}

	unique := map[int]struct{}{}
	for _, capacity := range capacities {
		if capacity >= 0 {
			unique[capacity] = struct{}{}
		}
	}

	if len(unique) <= 1 {
		linuxClusters = []ClusterStat{{Name: "CPU"}}
		linuxClusterOf = make([]int, count)
		return
	}

	values := make([]int, 0, len(unique))
	for capacity := range unique {
		values = append(values, capacity)
	}
	sort.Sort(sort.Reverse(sort.IntSlice(values)))

	linuxClusters = make([]ClusterStat, len(values))
	for index := range values {
		switch {
		case len(values) == 2 && index == 0:
			linuxClusters[index].Name = "P"
		case len(values) == 2:
			linuxClusters[index].Name = "E"
		default:
			linuxClusters[index].Name = "C" + strconv.Itoa(index)
		}
	}

	linuxClusterOf = make([]int, count)
	for i, capacity := range capacities {
		for index, value := range values {
			if capacity == value {
				linuxClusterOf[i] = index
				break
			}
		}
	}
}

func readProcStat() (perCPU [][2]uint64, ok bool) {
	file, err := os.Open("/proc/stat")
	if err != nil {
		return nil, false
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "cpu") || strings.HasPrefix(line, "cpu ") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 5 {
			continue
		}
		var values []uint64
		for _, field := range fields[1:] {
			value, err := strconv.ParseUint(field, 10, 64)
			if err != nil {
				values = nil
				break
			}
			values = append(values, value)
		}
		if len(values) < 4 {
			continue
		}
		var total uint64
		for _, value := range values {
			total += value
		}
		idle := values[3]
		if len(values) > 4 {
			idle += values[4]
		}
		perCPU = append(perCPU, [2]uint64{idle, total})
	}
	return perCPU, len(perCPU) > 0
}

func collectPlatform(m *Metrics) {
	perCPU, ok := readProcStat()
	if ok {
		if !linuxCPUReady {
			initClusters(len(perCPU))
			linuxPrevIdle = make([]uint64, len(perCPU))
			linuxPrevTotal = make([]uint64, len(perCPU))
			linuxCPUReady = true
		}

		idleDelta := make([]float64, len(linuxClusters))
		totalDelta := make([]float64, len(linuxClusters))
		var totalIdle, totalAll float64

		for i, sample := range perCPU {
			if i >= len(linuxPrevIdle) {
				break
			}
			if linuxPrevTotal[i] != 0 {
				dIdle := float64(sample[0] - linuxPrevIdle[i])
				dTotal := float64(sample[1] - linuxPrevTotal[i])
				totalIdle += dIdle
				totalAll += dTotal
				cluster := linuxClusterOf[i]
				if cluster < len(idleDelta) {
					idleDelta[cluster] += dIdle
					totalDelta[cluster] += dTotal
				}
			}
			linuxPrevIdle[i], linuxPrevTotal[i] = sample[0], sample[1]
		}

		if totalAll > 0 {
			m.CPU = clampPercent((totalAll - totalIdle) / totalAll * 100)
		}
		m.Clusters = make([]ClusterStat, len(linuxClusters))
		for index, cluster := range linuxClusters {
			percent := 0.0
			if totalDelta[index] > 0 {
				percent = clampPercent((totalDelta[index] - idleDelta[index]) / totalDelta[index] * 100)
			}
			m.Clusters[index] = ClusterStat{Name: cluster.Name, Percent: percent}
		}
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

// ---------------------------------------------------------------- GPU

func collectGPUPlatform(m *Metrics) {
	// nvidia-smi is a process spawn, do not run it on every tick.
	if gpuLinuxValid && time.Since(gpuLinuxAt) < 2*time.Second {
		m.GPU, m.VRAMUsedMB, m.VRAMTotalMB = gpuLinuxUtil, gpuLinuxUsedMB, gpuLinuxTotalMB
		return
	}

	util, usedMB, totalMB, ok := gpuFromNvidiaSMI()
	if !ok {
		util, usedMB, totalMB, ok = gpuFromAmdgpu()
	}
	if !ok {
		return
	}

	gpuLinuxUtil, gpuLinuxUsedMB, gpuLinuxTotalMB, gpuLinuxValid, gpuLinuxAt = util, usedMB, totalMB, true, time.Now()
	m.GPU, m.VRAMUsedMB, m.VRAMTotalMB = util, usedMB, totalMB
}

func gpuFromNvidiaSMI() (util, usedMB, totalMB float64, ok bool) {
	out, err := exec.Command("nvidia-smi",
		"--query-gpu=utilization.gpu,memory.used,memory.total",
		"--format=csv,noheader,nounits").Output()
	if err != nil {
		return 0, 0, 0, false
	}
	line := strings.SplitN(strings.TrimSpace(string(out)), "\n", 2)[0]
	fields := strings.Split(line, ",")
	if len(fields) < 3 {
		return 0, 0, 0, false
	}
	parse := func(s string) (float64, bool) {
		value, err := strconv.ParseFloat(strings.TrimSpace(s), 64)
		return value, err == nil
	}
	u, ok1 := parse(fields[0])
	used, ok2 := parse(fields[1])
	total, ok3 := parse(fields[2])
	if !ok1 || !ok2 || !ok3 {
		return 0, 0, 0, false
	}
	return clampPercent(u), used, total, true
}

func gpuFromAmdgpu() (util, usedMB, totalMB float64, ok bool) {
	busyFiles, err := filepath.Glob("/sys/class/drm/card[0-9]*/device/gpu_busy_percent")
	if err != nil {
		return 0, 0, 0, false
	}
	for _, busyFile := range busyFiles {
		dir := filepath.Dir(busyFile)
		busy, err := readFloatFile(busyFile)
		if err != nil {
			continue
		}
		used, _ := readFloatFile(filepath.Join(dir, "mem_info_vram_used"))
		total, _ := readFloatFile(filepath.Join(dir, "mem_info_vram_total"))
		return clampPercent(busy), used / 1024 / 1024, total / 1024 / 1024, true
	}
	return 0, 0, 0, false
}

func readFloatFile(path string) (float64, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}
	return strconv.ParseFloat(strings.TrimSpace(string(data)), 64)
}
