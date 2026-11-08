package metrics

import "testing"

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
