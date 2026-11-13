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
