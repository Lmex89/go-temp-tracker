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
