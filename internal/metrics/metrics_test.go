package metrics

import (
	"bytes"
	"errors"
	"strings"
	"testing"
	"time"

	"sensors-temp/internal/logging"
)

func TestCPUFailuresAreReportedOnceUntilChangedOrRecovered(t *testing.T) {
	var output bytes.Buffer
	old := logging.Logger
	logging.Logger = logging.New(&output, "DEBUG")
	t.Cleanup(func() { logging.Logger = old })
	err := errors.New("first failure")
	sm := NewSystemMetrics()
	sm.SuppressRepeatedCPUWarnings()
	sm.cpuPercent = func(time.Duration, bool) ([]float64, error) { return []float64{25}, err }
	for i := 0; i < 3; i++ {
		sm.ReadCPU()
	}
	err = errors.New("different failure")
	sm.ReadCPU()
	err = nil
	sm.ReadCPU()
	err = errors.New("first failure")
	sm.ReadCPU()
	got := output.String()
	if strings.Count(got, "Failed to read CPU: first failure") != 2 || strings.Count(got, "Failed to read CPU: different failure") != 1 || !strings.Contains(got, "CPU reading recovered") {
		t.Fatalf("unexpected logs: %s", got)
	}
}

func TestCPUFailuresKeepLegacyWarningsWithoutAlerts(t *testing.T) {
	var output bytes.Buffer
	old := logging.Logger
	logging.Logger = logging.New(&output, "INFO")
	t.Cleanup(func() { logging.Logger = old })
	sm := NewSystemMetrics()
	sm.cpuPercent = func(time.Duration, bool) ([]float64, error) { return nil, errors.New("failure") }
	sm.ReadCPU()
	sm.ReadCPU()
	if got := strings.Count(output.String(), "Failed to read CPU: failure"); got != 2 {
		t.Fatalf("warnings = %d", got)
	}
}

func TestCPUObservationTimePrecedesProcessingDelay(t *testing.T) {
	observed := time.Now()
	clock := observed
	clockRead := make(chan struct{})
	sm := NewSystemMetrics()
	sm.cpuPercent = func(time.Duration, bool) ([]float64, error) { return []float64{40, 60}, nil }
	calls := 0
	sm.now = func() time.Time {
		at := clock
		calls++
		if calls == 2 {
			close(clockRead)
		}
		return at
	}
	sm.cpuMu.Lock()
	result := make(chan []MetricPoint, 1)
	go func() { result <- sm.ReadCPU() }()
	// The clock read is synchronized before releasing processing below.
	<-clockRead
	clock = observed.Add(time.Hour)
	sm.cpuMu.Unlock()
	points := <-result
	for _, point := range points {
		if !point.At.Equal(observed) {
			t.Fatalf("reading time = %v, want %v", point.At, observed)
		}
		if point.BootAt <= 0 {
			t.Fatal("CPU reading lacks suspend-aware clock")
		}
	}
}

func TestCPUReadingBoundsDelayedReturn(t *testing.T) {
	start := time.Now()
	clock := start
	sm := NewSystemMetrics()
	sm.now = func() time.Time { return clock }
	sm.cpuPercent = func(time.Duration, bool) ([]float64, error) {
		clock = start.Add(5 * time.Second)
		return []float64{50}, nil
	}
	points := sm.ReadCPU()
	for _, point := range points {
		if !point.StartedAt.Equal(start) || !point.At.Equal(start.Add(5*time.Second)) {
			t.Fatalf("acquisition bounds = %v to %v", point.StartedAt, point.At)
		}
	}
}

// These tests hit the real host (gopsutil), so they assert shape, not values.
func TestReadLoad_ReturnsThreeWindows(t *testing.T) {
	points := NewSystemMetrics().ReadLoad()
	want := []string{"load/1min", "load/5min", "load/15min"}
	if len(points) != len(want) {
		t.Fatalf("got %d points, want %d", len(points), len(want))
	}
	for i, p := range points {
		if p.Sensor != want[i] {
			t.Errorf("point %d sensor = %q, want %q", i, p.Sensor, want[i])
		}
	}
}
