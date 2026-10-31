//go:build windows

package main

import (
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"
	"unsafe"
)

var (
	kernel32 = syscall.NewLazyDLL("kernel32.dll")
	ntdll    = syscall.NewLazyDLL("ntdll.dll")
	pdhDLL   = syscall.NewLazyDLL("pdh.dll")

	powrprof = syscall.NewLazyDLL("powrprof.dll")

	procCallNtPowerInformation     = powrprof.NewProc("CallNtPowerInformation")
	procGetSystemTimes             = kernel32.NewProc("GetSystemTimes")
	procGlobalMemoryStatusEx       = kernel32.NewProc("GlobalMemoryStatusEx")
	procGetTickCount64             = kernel32.NewProc("GetTickCount64")
	procGetActiveProcessorCount    = kernel32.NewProc("GetActiveProcessorCount")
	procGetSystemCpuSetInformation = kernel32.NewProc("GetSystemCpuSetInformation")
	procNtQuerySystemInformation   = ntdll.NewProc("NtQuerySystemInformation")

	procPdhOpenQueryW                = pdhDLL.NewProc("PdhOpenQueryW")
	procPdhAddEnglishCounterW        = pdhDLL.NewProc("PdhAddEnglishCounterW")
	procPdhCollectQueryData          = pdhDLL.NewProc("PdhCollectQueryData")
	procPdhGetFormattedCounterArrayW = pdhDLL.NewProc("PdhGetFormattedCounterArrayW")
)

const (
	systemProcessorPerformanceInformation = 8
	processorInformationLevel             = 11
	pdhFmtDouble                          = 0x00000200
	// PDH_MORE_DATA is returned by the sizing call, it is not an error here.
	pdhMoreData        = 0x800007D2
	allProcessorGroups = 0xffff
	maxProcessors      = 256
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

type processorPerf struct {
	IdleTime       uint64
	KernelTime     uint64
	UserTime       uint64
	DpcTime        uint64
	InterruptTime  uint64
	InterruptCount uint32
	_              uint32
}

// cpuSampler aggregates the per-processor times into clusters, using the efficiency class
// the scheduler reports (P-cores / E-cores on hybrid CPUs).
type cpuSampler struct {
	ready    bool
	count    int
	prevIdle [maxProcessors]uint64
	prevKern [maxProcessors]uint64
	prevUser [maxProcessors]uint64

	class   [maxProcessors]int
	cluster [maxProcessors]int
	names   []string
}

var cpuStats cpuSampler

func (s *cpuSampler) init() {
	s.count = int(activeProcessorCount())

	var returned uint32
	buf := make([]byte, 16*1024)
	ok, _, _ := procGetSystemCpuSetInformation.Call(
		uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)), uintptr(unsafe.Pointer(&returned)), 0, 0,
	)
	if ok == 0 {
		return
	}

	cores := map[int]int{}
	for offset := 0; offset+20 <= int(returned); {
		size := *(*uint32)(unsafe.Pointer(&buf[offset]))
		if size == 0 || offset+int(size) > int(returned) {
			break
		}
		if *(*uint32)(unsafe.Pointer(&buf[offset+4])) == 0 { // CpuSet entries only
			logical := int(buf[offset+14])
			if logical < maxProcessors {
				s.class[logical] = int(buf[offset+18])
				cores[logical] = int(buf[offset+15])
			}
		}
		offset += int(size)
	}
	if len(cores) == 0 {
		return
	}

	byClass := map[int][]int{}
	for logical := range cores {
		class := s.class[logical]
		byClass[class] = append(byClass[class], logical)
	}
	classes := make([]int, 0, len(byClass))
	for class := range byClass {
		classes = append(classes, class)
	}
	sort.Ints(classes)

	// A cluster that exposes more logical processors than physical cores uses SMT, which
	// is the performance cluster on hybrid parts.
	smt := map[int]bool{}
	for _, class := range classes {
		distinct := map[int]struct{}{}
		for _, logical := range byClass[class] {
			distinct[cores[logical]] = struct{}{}
		}
		smt[class] = len(byClass[class]) > len(distinct)
	}
	smtClusters := 0
	for _, class := range classes {
		if smt[class] {
			smtClusters++
		}
	}

	s.names = make([]string, len(classes))
	for index, class := range classes {
		switch {
		case len(classes) == 1:
			s.names[index] = "CPU"
		case smtClusters == 1 && smt[class]:
			s.names[index] = "P"
		case smtClusters == 1:
			s.names[index] = "E"
		default:
			s.names[index] = "C" + string(rune('0'+index))
		}
		for _, logical := range byClass[class] {
			s.cluster[logical] = index
		}
	}

	s.ready = true
}

// processorPowerInformation mirrors PROCESSOR_POWER_INFORMATION from powrprof.h.
type processorPowerInformation struct {
	Number           uint32
	MaxMhz           uint32
	CurrentMhz       uint32
	MhzLimit         uint32
	MaxIdleState     uint32
	CurrentIdleState uint32
}

// nominalMHz returns the nominal clock of every logical processor (powrprof).
func nominalMHz() []uint32 {
	count := int(activeProcessorCount())
	if count <= 0 || count > maxProcessors {
		return nil
	}

	buf := make([]processorPowerInformation, count)
	status, _, _ := procCallNtPowerInformation.Call(
		processorInformationLevel,
		0, 0,
		uintptr(unsafe.Pointer(&buf[0])),
		uintptr(len(buf))*unsafe.Sizeof(buf[0]),
	)
	if status != 0 {
		return nil
	}

	out := make([]uint32, count)
	for _, info := range buf {
		if int(info.Number) >= count {
			continue
		}
		out[info.Number] = info.MaxMhz
	}
	return out
}

// freqSampler turns "\Processor Information(*)\% Processor Performance" into MHz.
// The counter is relative to the nominal clock and includes the turbo states, which is
// what users expect to see as the current frequency.
type freqSampler struct {
	ready       bool
	query       uintptr
	counter     uintptr
	collections int
	percent     map[int]float64
	nominal     []uint32
}

var freqStats freqSampler

func (f *freqSampler) init() {
	if status, _, _ := procPdhOpenQueryW.Call(0, 0, uintptr(unsafe.Pointer(&f.query))); status != 0 {
		return
	}
	path := syscall.StringToUTF16Ptr(`\Processor Information(*)\% Processor Performance`)
	procPdhAddEnglishCounterW.Call(f.query, uintptr(unsafe.Pointer(path)), 0, uintptr(unsafe.Pointer(&f.counter)))
	f.nominal = nominalMHz()
	f.ready = f.counter != 0 && len(f.nominal) > 0
}

// sample returns the clock of every logical processor in MHz.
func (f *freqSampler) sample() []uint32 {
	if !f.ready {
		f.init()
		if !f.ready {
			return f.nominal
		}
	}

	procPdhCollectQueryData.Call(f.query)
	f.collections++
	if f.collections >= 2 {
		values := map[int]float64{}
		for _, item := range pdhReadArray(f.counter) {
			// instance names look like "0,12" (group, logical processor)
			index := strings.LastIndex(item.name, ",")
			if index < 0 {
				continue
			}
			logical, err := strconv.Atoi(item.name[index+1:])
			if err != nil {
				continue
			}
			values[logical] = item.value
		}
		if len(values) > 0 {
			f.percent = values
		}
	}

	out := make([]uint32, len(f.nominal))
	for index, mhz := range f.nominal {
		factor := 1.0
		if percent, ok := f.percent[index]; ok && percent > 0 {
			factor = percent / 100
		}
		out[index] = uint32(float64(mhz)*factor + 0.5)
	}
	return out
}

func activeProcessorCount() uint32 {
	count, _, _ := procGetActiveProcessorCount.Call(allProcessorGroups)
	return uint32(count)
}

func (s *cpuSampler) sample(m *Metrics) {
	if !s.ready {
		s.init()
		if !s.ready {
			return
		}
	}
	if s.count <= 0 {
		return
	}

	stride := int(unsafe.Sizeof(processorPerf{}))
	buf := make([]byte, s.count*stride)
	var returned uint32
	status, _, _ := procNtQuerySystemInformation.Call(
		systemProcessorPerformanceInformation,
		uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)), uintptr(unsafe.Pointer(&returned)),
	)
	if status != 0 {
		return
	}
	count := int(returned) / stride
	if count > s.count {
		count = s.count
	}

	idleDelta := make([]float64, len(s.names))
	totalDelta := make([]float64, len(s.names))
	var totalIdle, totalAll float64

	cores := make([]CoreStat, 0, count)
	for i := 0; i < count; i++ {
		perf := *(*processorPerf)(unsafe.Pointer(&buf[i*stride]))
		percent := 0.0
		if s.prevIdle[i] != 0 || s.prevKern[i] != 0 {
			// kernel time includes idle time
			dIdle := float64(perf.IdleTime - s.prevIdle[i])
			dTotal := float64(perf.KernelTime-s.prevKern[i]) + float64(perf.UserTime-s.prevUser[i])
			totalIdle += dIdle
			totalAll += dTotal
			if dTotal > 0 {
				percent = clampPercent((dTotal - dIdle) / dTotal * 100)
			}
			if cluster := s.cluster[i]; cluster < len(idleDelta) {
				idleDelta[cluster] += dIdle
				totalDelta[cluster] += dTotal
			}
		}
		cores = append(cores, CoreStat{Index: i, Percent: percent})
		s.prevIdle[i] = perf.IdleTime
		s.prevKern[i] = perf.KernelTime
		s.prevUser[i] = perf.UserTime
	}

	if frequencies := freqStats.sample(); len(frequencies) > 0 {
		for index := range cores {
			if index < len(frequencies) {
				cores[index].MHz = frequencies[index]
			}
		}
	}
	m.Cores = cores

	if totalAll > 0 {
		m.CPU = clampPercent((totalAll - totalIdle) / totalAll * 100)
	}

	m.Clusters = make([]ClusterStat, 0, len(s.names))
	for index, name := range s.names {
		percent := 0.0
		if totalDelta[index] > 0 {
			percent = clampPercent((totalDelta[index] - idleDelta[index]) / totalDelta[index] * 100)
		}
		m.Clusters = append(m.Clusters, ClusterStat{Name: name, Percent: percent})
	}
}

func collectPlatform(m *Metrics) {
	cpuStats.sample(m)

	status := memoryStatusEx{Length: uint32(unsafe.Sizeof(memoryStatusEx{}))}
	if ok, _, _ := procGlobalMemoryStatusEx.Call(uintptr(unsafe.Pointer(&status))); ok != 0 {
		m.Mem = float64(status.MemoryLoad)
	}

	if ticks, _, _ := procGetTickCount64.Call(); ticks != 0 {
		m.Uptime = time.Duration(ticks) * time.Millisecond
	}
}

// ---------------------------------------------------------------- GPU (PDH)

type pdhCounterValue struct {
	status uint32
	_      uint32
	value  float64
}

type pdhCounterItem struct {
	name  *uint16
	value pdhCounterValue
}

type gpuSampler struct {
	ready       bool
	query       uintptr
	engine      uintptr
	memory      uintptr
	collections int
	util        float64
	usedMB      float64
	totalMB     float64
}

var gpuStats gpuSampler

type pdhSample struct {
	name  string
	value float64
}

func (g *gpuSampler) init() {
	if g.ready {
		return
	}

	if status, _, _ := procPdhOpenQueryW.Call(0, 0, uintptr(unsafe.Pointer(&g.query))); status != 0 {
		return
	}
	enginePath := syscall.StringToUTF16Ptr(`\GPU Engine(*)\Utilization Percentage`)
	memoryPath := syscall.StringToUTF16Ptr(`\GPU Adapter Memory(*)\Dedicated Usage`)
	procPdhAddEnglishCounterW.Call(g.query, uintptr(unsafe.Pointer(enginePath)), 0, uintptr(unsafe.Pointer(&g.engine)))
	procPdhAddEnglishCounterW.Call(g.query, uintptr(unsafe.Pointer(memoryPath)), 0, uintptr(unsafe.Pointer(&g.memory)))

	g.totalMB = registryVRAMTotalMB()
	g.ready = g.engine != 0 || g.memory != 0
}

func (g *gpuSampler) sample(m *Metrics) {
	g.init()
	if !g.ready {
		return
	}

	// PDH needs two collections before it can compute a rate.
	procPdhCollectQueryData.Call(g.query)
	g.collections++
	if g.collections < 2 {
		return
	}

	if g.engine != 0 {
		// Sum per engine type (3D, compute, video decode, ...) and take the busiest one,
		// which is what the Windows task manager reports as "GPU utilisation".
		engines := map[string]float64{}
		for _, item := range pdhReadArray(g.engine) {
			lower := strings.ToLower(item.name)
			index := strings.Index(lower, "engtype_")
			if index < 0 {
				continue
			}
			engines[lower[index+len("engtype_"):]] += item.value
		}
		var best float64
		for _, value := range engines {
			if value > best {
				best = value
			}
		}
		g.util = clampPercent(best)
	}

	if g.memory != 0 {
		// One instance per adapter; use the one with the highest dedicated usage.
		var best float64
		for _, item := range pdhReadArray(g.memory) {
			if item.value > best {
				best = item.value
			}
		}
		g.usedMB = best / 1024 / 1024
	}

	m.GPU = g.util
	m.VRAMUsedMB = g.usedMB
	m.VRAMTotalMB = g.totalMB
}

// pdhReadArray returns the value (and instance name) of every instance of a PDH counter.
func pdhReadArray(counter uintptr) []pdhSample {
	var size, items uint32
	status, _, _ := procPdhGetFormattedCounterArrayW.Call(
		counter, pdhFmtDouble, uintptr(unsafe.Pointer(&size)), uintptr(unsafe.Pointer(&items)), 0,
	)
	// The sizing call returns PDH_MORE_DATA, that is expected.
	if size == 0 || items == 0 || (status != 0 && status != pdhMoreData) {
		return nil
	}

	buf := make([]byte, size)
	status, _, _ = procPdhGetFormattedCounterArrayW.Call(
		counter, pdhFmtDouble, uintptr(unsafe.Pointer(&size)), uintptr(unsafe.Pointer(&items)),
		uintptr(unsafe.Pointer(&buf[0])),
	)
	if status != 0 {
		return nil
	}

	stride := int(unsafe.Sizeof(pdhCounterItem{}))
	out := make([]pdhSample, 0, items)
	for i := 0; i < int(items); i++ {
		item := (*pdhCounterItem)(unsafe.Pointer(&buf[i*stride]))
		name := ""
		if item.name != nil {
			name = syscall.UTF16ToString(unsafe.Slice(item.name, 512))
		}
		out = append(out, pdhSample{name: name, value: item.value.value})
	}
	return out
}

func collectGPUPlatform(m *Metrics) {
	gpuStats.sample(m)
}

// registryVRAMTotalMB returns the largest HardwareInformation.qwMemorySize of the display
// adapters, which is where the driver publishes the physical VRAM (best effort).
func registryVRAMTotalMB() float64 {
	const classPath = `SYSTEM\CurrentControlSet\Control\Class\{4d36e968-e325-11ce-bfc1-08002be10318}`

	var key syscall.Handle
	if err := syscall.RegOpenKeyEx(syscall.HKEY_LOCAL_MACHINE, syscall.StringToUTF16Ptr(classPath), 0, syscall.KEY_READ, &key); err != nil {
		return 0
	}
	defer syscall.RegCloseKey(key)

	var maxBytes uint64
	for index := uint32(0); ; index++ {
		name := make([]uint16, 256)
		nameLen := uint32(len(name))
		if err := syscall.RegEnumKeyEx(key, index, &name[0], &nameLen, nil, nil, nil, nil); err != nil {
			break
		}
		subkeyName := syscall.UTF16ToString(name[:nameLen])

		var subkey syscall.Handle
		if err := syscall.RegOpenKeyEx(key, syscall.StringToUTF16Ptr(subkeyName), 0, syscall.KEY_READ, &subkey); err != nil {
			continue
		}
		var raw [8]byte
		size := uint32(len(raw))
		var valueType uint32
		err := syscall.RegQueryValueEx(subkey, syscall.StringToUTF16Ptr("HardwareInformation.qwMemorySize"), nil, &valueType, &raw[0], &size)
		syscall.RegCloseKey(subkey)
		if err != nil || size != 8 {
			continue
		}
		value := *(*uint64)(unsafe.Pointer(&raw[0]))
		if value > maxBytes {
			maxBytes = value
		}
	}
	return float64(maxBytes) / 1024 / 1024
}
