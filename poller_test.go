package main

import (
	"sort"
	"testing"
	"time"
)

// fakeSensorReader satisfies SensorReader with canned data.
// In Python this would be a stub class with a read() method returning a dict.
type fakeSensorReader struct{ data map[string]float64 }

func (f fakeSensorReader) Read() map[string]float64 { return f.data }

func TestWrapSensorReader(t *testing.T) {
	tests := []struct {
		name string
		data map[string]float64
		want []MetricPoint // sorted by Sensor, because map order is random
	}{
		{"empty map", map[string]float64{}, []MetricPoint{}},
		{"single sensor", map[string]float64{"Core 0": 45.5},
			[]MetricPoint{{Sensor: "Core 0", Value: 45.5, Unit: "celsius"}}},
		{"multiple sensors preserved", map[string]float64{"acpitz": 30, "Core 0": 55.25},
			[]MetricPoint{
				{Sensor: "Core 0", Value: 55.25, Unit: "celsius"},
				{Sensor: "acpitz", Value: 30, Unit: "celsius"},
			}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := wrapSensorReader(fakeSensorReader{tt.data})()
			sort.Slice(got, func(i, j int) bool { return got[i].Sensor < got[j].Sensor })
			if len(got) != len(tt.want) {
				t.Fatalf("len = %d, want %d", len(got), len(tt.want))
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Errorf("point %d = %+v, want %+v", i, got[i], tt.want[i])
				}
			}
		})
	}
}

func TestPollSpecFields(t *testing.T) {
	called := false
	spec := pollSpec{
		metricType: "cpu",
		interval:   30 * time.Second,
		readFunc:   func() []MetricPoint { called = true; return nil },
	}
	spec.readFunc()
	if spec.metricType != "cpu" || spec.interval != 30*time.Second || !called {
		t.Errorf("pollSpec fields not preserved: %+v", spec)
	}
}

// TestPollOnce drives one iteration of the polling loop against a throwaway
// SQLite store (newTempSQLite chdirs into a temp dir, so the real temps.db is
// never touched -- like pytest's tmp_path fixture).
func TestPollOnce(t *testing.T) {
	tests := []struct {
		name       string
		metricType string
		points     []MetricPoint
	}{
		{"temperature", "temperature", []MetricPoint{
			{Sensor: "Core 0", Value: 55.5, Unit: "celsius"},
			{Sensor: "acpitz", Value: 30, Unit: "celsius"},
		}},
		{"cpu", "cpu", []MetricPoint{{Sensor: "cpu/total", Value: 12.5, Unit: "%"}}},
		{"memory empty read", "memory", nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store, db := newTempSQLite(t)

			calls := 0
			read := func() []MetricPoint { calls++; return tt.points }

			pollOnce(store, db, tt.metricType, 24, read)

			if calls != 1 {
				t.Errorf("readFunc calls = %d, want 1", calls)
			}
			got, err := store.Query(db, TimeWindow{Hours: 1}, tt.metricType)
			if err != nil {
				t.Fatalf("Query: %v", err)
			}
			if len(got) != len(tt.points) {
				t.Fatalf("stored %d rows, want %d", len(got), len(tt.points))
			}
			for _, r := range got {
				if r.MetricType != tt.metricType {
					t.Errorf("metric_type = %q, want %q", r.MetricType, tt.metricType)
				}
			}
		})
	}
}

// TestPollOncePrunesOnlyOwnType checks the prune step is scoped to the
// poller's metric type: an old "cpu" row must survive a "memory" poll.
func TestPollOncePrunesOnlyOwnType(t *testing.T) {
	store, db := newTempSQLite(t)

	old := "2000-01-01 00:00:00"
	for _, mt := range []string{"cpu", "memory"} {
		if _, err := db.Exec(`INSERT INTO readings (sensor, temp_c, metric_type, unit, created_at) VALUES (?, 1, ?, '%', ?)`, mt+"/x", mt, old); err != nil {
			t.Fatal(err)
		}
	}

	pollOnce(store, db, "memory", 1, func() []MetricPoint { return nil })

	var n int
	db.QueryRow(`SELECT COUNT(*) FROM readings WHERE metric_type='cpu'`).Scan(&n)
	if n != 1 {
		t.Errorf("old cpu rows = %d, want 1 (untouched)", n)
	}
	db.QueryRow(`SELECT COUNT(*) FROM readings WHERE metric_type='memory'`).Scan(&n)
	if n != 0 {
		t.Errorf("old memory rows = %d, want 0 (pruned)", n)
	}
}
