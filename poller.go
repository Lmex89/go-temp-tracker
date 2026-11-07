package main

import (
	"database/sql"
	"time"
)

// pollSpec bundles the three per-goroutine variables of a polling loop.
// In Python this would be a dataclass(metric_type, interval, read_func).
type pollSpec struct {
	metricType string
	interval   time.Duration
	readFunc   func() []MetricPoint
}

// wrapSensorReader adapts SensorReader.Read() (a map) to func() []MetricPoint,
// so temperature looks like every other metric. Like a Python
// [MetricPoint(s, v, "celsius") for s, v in reader.read().items()].
func wrapSensorReader(reader SensorReader) func() []MetricPoint {
	return func() []MetricPoint {
		readings := reader.Read()
		points := make([]MetricPoint, 0, len(readings))
		for sensor, value := range readings {
			points = append(points, MetricPoint{Sensor: sensor, Value: value, Unit: "celsius"})
		}
		return points
	}
}

// buildPollSpecs returns the six polling specs in start order (index * 3s is
// the stagger delay). Temperature uses the -interval flag; the system metrics
// read their *_POLL_INTERVAL env vars (default 60s). Extracted from main() so
// tests can check it. Python analogy: a factory returning a list of dataclasses.
func buildPollSpecs(sensor SensorReader, metrics *SystemMetrics, tempIntervalSec int) []pollSpec {
	const defaultSec = 60
	sec := func(d int) time.Duration { return time.Duration(d) * time.Second }
	return []pollSpec{
		{"temperature", sec(tempIntervalSec), wrapSensorReader(sensor)},
		{"cpu", sec(getEnvInt("CPU_POLL_INTERVAL", defaultSec)), metrics.ReadCPU},
		{"memory", sec(getEnvInt("MEMORY_POLL_INTERVAL", defaultSec)), metrics.ReadMemory},
		{"swap", sec(getEnvInt("SWAP_POLL_INTERVAL", defaultSec)), metrics.ReadSwap},
		{"disk", sec(getEnvInt("DISK_POLL_INTERVAL", defaultSec)), metrics.ReadDisk},
		{"load", sec(getEnvInt("LOAD_POLL_INTERVAL", defaultSec)), metrics.ReadLoad},
	}
}

// pollOnce runs ONE iteration of the polling loop: read -> insert -> prune.
// It is split out of RunPoller so tests can call it without the infinite loop
// or the sleep. Python analogy: the body of "while True:" extracted into a
// function you can call directly from pytest.
func pollOnce(store Store, db *sql.DB, metricType string, retainHours int, readFunc func() []MetricPoint) {
	Logger.Debug("Reading [%s] metrics...", metricType)
	points := readFunc()

	if len(points) == 0 {
		Logger.Warn("[%s] No metrics returned", metricType)
	}

	for _, p := range points {
		Logger.Debug("Recording [%s] %s: %.2f%s", metricType, p.Sensor, p.Value, p.Unit)
		store.Insert(db, p.Sensor, p.Value, metricType, p.Unit)
	}

	if len(points) > 0 {
		Logger.Info("Recorded %d [%s] reading(s)", len(points), metricType)
	}

	store.Prune(db, metricType, retainHours)
}

// RunPoller is the unified polling loop for ANY metric type (temperature
// included). Replaced the old RunMetricPoller and Poller.Run.
// Meant to be started with "go RunPoller(...)", like a Python daemon thread.
func RunPoller(store Store, db *sql.DB, metricType string, intervalSec, retainHours int, readFunc func() []MetricPoint) {
	Logger.Info("[%s] polling loop started (interval=%ds, retain=%dh)", metricType, intervalSec, retainHours)

	for {
		pollOnce(store, db, metricType, retainHours, readFunc)

		Logger.Debug("[%s] sleeping for %d seconds...", metricType, intervalSec)
		time.Sleep(time.Duration(intervalSec) * time.Second)
	}
}
