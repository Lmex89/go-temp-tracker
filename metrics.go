package main

import (
	"fmt"
	"strings"
	"time"

	// gopsutil v3 -- pure Go system metrics library (like Python's psutil, but for Go).
	// No CGo required -- reads from /proc and /sys under the hood.
	"github.com/shirou/gopsutil/v3/cpu"
	"github.com/shirou/gopsutil/v3/disk"
	"github.com/shirou/gopsutil/v3/load"
	"github.com/shirou/gopsutil/v3/mem"
)

// MetricPoint holds one sensor reading value.
// This is like a lightweight dataclass -- sensor name, numeric value, and unit string.
// In Python: @dataclass class MetricPoint: sensor: str; value: float; unit: str
type MetricPoint struct {
	Sensor string
	Value  float64
	Unit   string
}

// SystemMetrics wraps gopsutil calls for CPU, memory, swap, disk, and load.
// This is a struct with no fields (like a module/namespace in Python).
// Each method returns a slice of MetricPoint (like a list of dataclass instances).
type SystemMetrics struct{}

// NewSystemMetrics constructor -- returns a pointer to a new SystemMetrics.
func NewSystemMetrics() *SystemMetrics {
	return &SystemMetrics{}
}

// ReadCPU measures CPU usage per core and total.
// Blocks for ~1 second to measure actual recent usage (like `top` in real-time mode).
// Returns: cpu/Core 0=45.2%, cpu/Core 1=32.1%, cpu/total=38.6%
// In Python: psutil.cpu_percent(interval=1, percpu=True)
func (sm *SystemMetrics) ReadCPU() []MetricPoint {
	// cpu.Percent with interval > 0 blocks for that duration to measure recent CPU usage.
	// percpu=true returns one float per logical core + total as the last element.
	// Wait -- actually without percpu=false, the total is NOT included.
	// Let's call with percpu=true and compute total manually.
	percents, err := cpu.Percent(time.Second, true)
	if err != nil {
		Logger.Warn("Failed to read CPU: %v", err)
		return nil
	}
	// Build MetricPoint slice -- like a list comprehension in Python.
	// In Python: [MetricPoint(f"cpu/Core {i}", p, "%") for i, p in enumerate(percents)]
	points := make([]MetricPoint, 0, len(percents)+1)
	var total float64
	for i, p := range percents {
		points = append(points, MetricPoint{
			Sensor: fmt.Sprintf("cpu/Core %d", i),
			Value:  p,
			Unit:   "%",
		})
		total += p
	}
	// Add the average as total CPU usage.
	if len(percents) > 0 {
		avg := total / float64(len(percents))
		points = append(points, MetricPoint{
			Sensor: "cpu/total",
			Value:  avg,
			Unit:   "%",
		})
	}
	return points
}

// ReadMemory returns virtual memory stats (RAM).
// Returns: memory/used_percent=52.5%, memory/total_bytes=16GB, etc.
// In Python: psutil.virtual_memory()
func (sm *SystemMetrics) ReadMemory() []MetricPoint {
	// mem.VirtualMemory returns a struct with Total, Used, Free, UsedPercent, etc.
	// In Python: svmem(total=..., used=..., free=..., percent=..., ...)
	v, err := mem.VirtualMemory()
	if err != nil {
		Logger.Warn("Failed to read memory: %v", err)
		return nil
	}
	// Return multiple points -- each is like a separate "sensor" reading.
	// The chart will show used_percent; bytes are for the gauge display.
	return []MetricPoint{
		{Sensor: "memory/used_percent", Value: clampPercent(v.UsedPercent), Unit: "%"},
		{Sensor: "memory/total_bytes", Value: float64(v.Total), Unit: "bytes"},
		{Sensor: "memory/used_bytes", Value: float64(v.Used), Unit: "bytes"},
		{Sensor: "memory/free_bytes", Value: float64(v.Free), Unit: "bytes"},
	}
}

// clampPercent ensures a percentage value stays within [0, 100].
// gopsutil can return negative percentages in edge cases (e.g., disabled swap),
// so we clamp defensively. Like max(0, min(100, val)) in Python.
func clampPercent(val float64) float64 {
	if val < 0 {
		return 0
	}
	if val > 100 {
		return 100
	}
	return val
}

// ReadSwap returns swap memory stats.
// Returns: swap/used_percent=12.0%, swap/total_bytes=2GB, etc.
// In Python: psutil.swap_memory()
func (sm *SystemMetrics) ReadSwap() []MetricPoint {
	v, err := mem.SwapMemory()
	if err != nil {
		Logger.Warn("Failed to read swap: %v", err)
		return nil
	}
	return []MetricPoint{
		{Sensor: "swap/used_percent", Value: clampPercent(v.UsedPercent), Unit: "%"},
		{Sensor: "swap/total_bytes", Value: float64(v.Total), Unit: "bytes"},
		{Sensor: "swap/used_bytes", Value: float64(v.Used), Unit: "bytes"},
		{Sensor: "swap/free_bytes", Value: float64(v.Free), Unit: "bytes"},
	}
}

// ReadDisk returns disk usage per partition.
// Returns: disk//=65.2%, disk//home=45.0%, plus byte variants.
// Filters out snap/squashfs loopback mounts (read-only, always show 100%).
// In Python: [psutil.disk_usage(part.mountpoint) for part in psutil.disk_partitions()]
func (sm *SystemMetrics) ReadDisk() []MetricPoint {
	// disk.Partitions(false) lists mount points -- false means "physical" only.
	// In Python: psutil.disk_partitions()
	partitions, err := disk.Partitions(false)
	if err != nil {
		Logger.Warn("Failed to list partitions: %v", err)
		return nil
	}
	var points []MetricPoint
	for _, p := range partitions {
		// Skip snap loopback mounts and squashfs filesystems.
		// These are read-only squashfs images at /snap/ -- always show 100% used,
		// which is not useful for monitoring. Like filtering out /snap/ in Python.
		if strings.HasPrefix(p.Mountpoint, "/snap/") || p.Fstype == "squashfs" {
			continue
		}

		// disk.Usage reads stats for a mountpoint -- total, used, free, percent.
		// In Python: psutil.disk_usage(mountpoint)
		usage, err := disk.Usage(p.Mountpoint)
		if err != nil {
			Logger.Debug("Cannot read disk usage for %s: %v", p.Mountpoint, err)
			continue
		}
		// Sensor name: "disk//" for root, "disk//home" for /home, etc.
		// Forward slash in mountpoint is OK -- it's unique in the sensor name.
		points = append(points, MetricPoint{
			Sensor: fmt.Sprintf("disk/%s", p.Mountpoint),
			Value:  clampPercent(usage.UsedPercent),
			Unit:   "%",
		})
		// Also store raw bytes for the gauge text display.
		points = append(points, MetricPoint{
			Sensor: fmt.Sprintf("disk/%s/total_bytes", p.Mountpoint),
			Value:  float64(usage.Total),
			Unit:   "bytes",
		})
		points = append(points, MetricPoint{
			Sensor: fmt.Sprintf("disk/%s/used_bytes", p.Mountpoint),
			Value:  float64(usage.Used),
			Unit:   "bytes",
		})
		points = append(points, MetricPoint{
			Sensor: fmt.Sprintf("disk/%s/free_bytes", p.Mountpoint),
			Value:  float64(usage.Free),
			Unit:   "bytes",
		})
	}
	return points
}

// ReadLoad returns system load averages for 1, 5, and 15 minutes.
// Returns: load/1min=1.5, load/5min=2.1, load/15min=1.8
// In Python: psutil.getloadavg()
func (sm *SystemMetrics) ReadLoad() []MetricPoint {
	avg, err := load.Avg()
	if err != nil {
		Logger.Warn("Failed to read load: %v", err)
		return nil
	}
	return []MetricPoint{
		{Sensor: "load/1min", Value: avg.Load1, Unit: ""},
		{Sensor: "load/5min", Value: avg.Load5, Unit: ""},
		{Sensor: "load/15min", Value: avg.Load15, Unit: ""},
	}
}
