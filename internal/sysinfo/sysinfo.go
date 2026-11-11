// Package sysinfo reads lightweight system information from procfs/sysfs.
package sysinfo

import (
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

// CPUInfo describes the processor and thermal state.
type CPUInfo struct {
	Cores      int     `json:"cores"`
	TempC      float64 `json:"temp_c"`
	FreqMHz    int     `json:"freq_mhz"`
	MaxFreqMHz int     `json:"max_freq_mhz"`
	Governor   string  `json:"governor"`
	Model      string  `json:"model"`
}

// MemoryInfo is in bytes.
type MemoryInfo struct {
	Total     uint64 `json:"total"`
	Used      uint64 `json:"used"`
	Free      uint64 `json:"free"`
	Available uint64 `json:"available"`
	SwapTotal uint64 `json:"swap_total"`
	SwapUsed  uint64 `json:"swap_used"`
}

// BatteryInfo is optional.
type BatteryInfo struct {
	Present  bool    `json:"present"`
	Capacity int     `json:"capacity"`
	Status   string  `json:"status"`
	VoltageV float64 `json:"voltage_v"`
}

// Snapshot is one system information sample.
type Snapshot struct {
	Hostname  string      `json:"hostname"`
	OS        string      `json:"os"`
	OSVersion string      `json:"os_version"`
	Kernel    string      `json:"kernel"`
	Arch      string      `json:"arch"`
	Model     string      `json:"model"`
	UptimeSec int64       `json:"uptime_seconds"`
	BootTime  int64       `json:"boot_time"`
	Load1     float64     `json:"load1"`
	Load5     float64     `json:"load5"`
	Load15    float64     `json:"load15"`
	CPU       CPUInfo     `json:"cpu"`
	Memory    MemoryInfo  `json:"memory"`
	Battery   BatteryInfo `json:"battery"`
	Timezone  string      `json:"timezone"`
	Time      int64       `json:"time"`
}

// ReadText reads a small text file, returning "" on any error.
func ReadText(path string, max int64) string {
	if max <= 0 {
		max = 64 * 1024
	}
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()
	buf := make([]byte, max)
	n, _ := f.Read(buf)
	return strings.TrimSpace(strings.TrimRight(string(buf[:n]), "\x00"))
}

// Collect gathers a system snapshot.
func Collect() Snapshot {
	now := time.Now()
	s := Snapshot{
		Hostname:  hostname(),
		OSVersion: "",
		Kernel:    ReadText("/proc/sys/kernel/osrelease", 256),
		Arch:      runtime.GOARCH,
		Model:     ReadText("/proc/device-tree/model", 256),
		Timezone:  timezone(),
		Time:      now.Unix(),
	}
	if s.Kernel == "" {
		s.Kernel = runtime.GOOS
	}
	if s.Model == "" {
		s.Model = ReadText("/sys/firmware/devicetree/base/model", 256)
	}

	osr := ParseOSRelease(ReadText("/etc/os-release", 16*1024))
	s.OS = osr["PRETTY_NAME"]
	if s.OS == "" {
		s.OS = osr["NAME"]
	}
	s.OSVersion = osr["VERSION_ID"]

	if up := ParseUptime(ReadText("/proc/uptime", 256)); up > 0 {
		s.UptimeSec = up
		s.BootTime = now.Add(-time.Duration(up) * time.Second).Unix()
	}
	s.Load1, s.Load5, s.Load15 = ParseLoadAvg(ReadText("/proc/loadavg", 256))
	s.Memory = ParseMeminfo(ReadText("/proc/meminfo", 64*1024))
	s.CPU = collectCPU()
	s.Battery = collectBattery()
	return s
}

func hostname() string {
	h, err := os.Hostname()
	if err != nil {
		return ""
	}
	return h
}

func timezone() string {
	if tz := ReadText("/etc/timezone", 256); tz != "" {
		return tz
	}
	return time.Local.String()
}

// ParseOSRelease parses /etc/os-release.
func ParseOSRelease(text string) map[string]string {
	out := map[string]string{}
	for _, line := range strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		v = strings.TrimSpace(v)
		v = strings.Trim(v, `"'`)
		out[strings.TrimSpace(k)] = v
	}
	return out
}

// ParseUptime parses the first field of /proc/uptime.
func ParseUptime(text string) int64 {
	fields := strings.Fields(text)
	if len(fields) == 0 {
		return 0
	}
	f, _ := strconv.ParseFloat(fields[0], 64)
	return int64(f)
}

// ParseLoadAvg parses /proc/loadavg.
func ParseLoadAvg(text string) (float64, float64, float64) {
	fields := strings.Fields(text)
	if len(fields) < 3 {
		return 0, 0, 0
	}
	a, _ := strconv.ParseFloat(fields[0], 64)
	b, _ := strconv.ParseFloat(fields[1], 64)
	c, _ := strconv.ParseFloat(fields[2], 64)
	return a, b, c
}

// ParseMeminfo parses /proc/meminfo into bytes.
func ParseMeminfo(text string) MemoryInfo {
	vals := map[string]uint64{}
	for _, line := range strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n") {
		k, v, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		fields := strings.Fields(v)
		if len(fields) == 0 {
			continue
		}
		n, err := strconv.ParseUint(fields[0], 10, 64)
		if err != nil {
			continue
		}
		// /proc/meminfo values are in kB except HugePages_*.
		vals[strings.TrimSpace(k)] = n * 1024
	}
	m := MemoryInfo{
		Total:     vals["MemTotal"],
		Free:      vals["MemFree"],
		Available: vals["MemAvailable"],
		SwapTotal: vals["SwapTotal"],
	}
	if m.Available == 0 {
		m.Available = m.Free + vals["Buffers"] + vals["Cached"]
	}
	if m.Total > m.Available {
		m.Used = m.Total - m.Available
	}
	if m.SwapTotal > vals["SwapFree"] {
		m.SwapUsed = m.SwapTotal - vals["SwapFree"]
	}
	return m
}

// ParseDF parses `df -Pk`.
func ParseDF(text string) (total, used, free uint64, ok bool) {
	lines := strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
	for i, line := range lines {
		if i == 0 || strings.TrimSpace(line) == "" {
			continue
		}
		f := strings.Fields(line)
		if len(f) < 6 {
			continue
		}
		t, e1 := strconv.ParseUint(f[1], 10, 64)
		u, e2 := strconv.ParseUint(f[2], 10, 64)
		a, e3 := strconv.ParseUint(f[3], 10, 64)
		if e1 != nil || e2 != nil || e3 != nil {
			continue
		}
		return t * 1024, u * 1024, a * 1024, true
	}
	return 0, 0, 0, false
}

func collectCPU() CPUInfo {
	c := CPUInfo{Cores: runtime.NumCPU()}
	c.Model = cpuModel()
	c.TempC = maxThermal()
	c.FreqMHz, c.MaxFreqMHz, c.Governor = cpuFreq()
	return c
}

func cpuModel() string {
	text := ReadText("/proc/cpuinfo", 64*1024)
	for _, line := range strings.Split(text, "\n") {
		if k, v, ok := strings.Cut(line, ":"); ok {
			if strings.TrimSpace(k) == "model name" || strings.TrimSpace(k) == "Processor" {
				return strings.TrimSpace(v)
			}
		}
	}
	return ""
}

func maxThermal() float64 {
	var best float64
	try := func(path string) {
		t := parseTemp(ReadText(path, 256))
		if t > best && t > -20 && t < 150 {
			best = t
		}
	}
	zones, _ := filepath.Glob("/sys/class/thermal/thermal_zone*")
	for _, z := range zones {
		typ := strings.ToLower(ReadText(filepath.Join(z, "type"), 256))
		if typ == "" {
			continue
		}
		if strings.Contains(typ, "cpu") || strings.Contains(typ, "soc") || strings.Contains(typ, "tsens") || strings.Contains(typ, "pm") {
			try(filepath.Join(z, "temp"))
		}
	}
	if best == 0 {
		for _, z := range zones {
			try(filepath.Join(z, "temp"))
		}
	}
	if best == 0 {
		hwmons, _ := filepath.Glob("/sys/class/hwmon/hwmon*")
		for _, h := range hwmons {
			inputs, _ := filepath.Glob(filepath.Join(h, "temp*_input"))
			for _, in := range inputs {
				try(in)
			}
		}
	}
	return best
}

func parseTemp(s string) float64 {
	f, err := strconv.ParseFloat(strings.TrimSpace(s), 64)
	if err != nil {
		return 0
	}
	if f > 200 {
		f /= 1000
	}
	return f
}

func cpuFreq() (curMHz, maxMHz int, governor string) {
	base := "/sys/devices/system/cpu/cpu0/cpufreq"
	if cur := ReadText(base+"/scaling_cur_freq", 64); cur != "" {
		if f, err := strconv.ParseInt(cur, 10, 64); err == nil {
			curMHz = int(f / 1000)
		}
	}
	if max := ReadText(base+"/cpuinfo_max_freq", 64); max != "" {
		if f, err := strconv.ParseInt(max, 10, 64); err == nil {
			maxMHz = int(f / 1000)
		}
	}
	governor = ReadText(base+"/scaling_governor", 64)
	return
}

func collectBattery() BatteryInfo {
	dirs, _ := filepath.Glob("/sys/class/power_supply/*")
	for _, d := range dirs {
		if !strings.EqualFold(ReadText(filepath.Join(d, "type"), 64), "Battery") {
			continue
		}
		cap, err := strconv.Atoi(ReadText(filepath.Join(d, "capacity"), 32))
		if err != nil {
			continue
		}
		b := BatteryInfo{
			Present:  true,
			Capacity: cap,
			Status:   ReadText(filepath.Join(d, "status"), 64),
		}
		if v := ReadText(filepath.Join(d, "voltage_now"), 64); v != "" {
			if uv, err := strconv.ParseFloat(v, 64); err == nil && uv > 0 {
				b.VoltageV = uv / 1_000_000
			}
		}
		return b
	}
	return BatteryInfo{}
}
