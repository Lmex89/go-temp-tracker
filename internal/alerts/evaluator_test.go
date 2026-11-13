package alerts_test

import (
	"math"
	"reflect"
	"testing"
	"time"

	"sensors-temp/internal/alerts"
	"sensors-temp/internal/metrics"
)

func TestRuleContinuity(t *testing.T) {
	rules := []alerts.Rule{
		{ID: "high", Sensor: "cpu/total", Threshold: 50, Duration: 10 * time.Second},
		{ID: "saturated", Sensor: "cpu/total", Threshold: 95, Duration: 5 * time.Second},
	}
	type step struct {
		sec     int
		value   float64
		missing bool
		want    []string
	}
	tests := []struct {
		name  string
		steps []step
	}{
		{"independent rules and reactivation", []step{
			{0, 95, false, nil}, {5, 95, false, []string{"saturated:activation"}},
			{10, 95, false, []string{"high:activation"}}, {15, 95, false, nil},
			{20, 70, false, []string{"saturated:recovery"}}, {25, 49, false, []string{"high:recovery"}},
			{30, 95, false, nil}, {35, 95, false, []string{"saturated:activation"}},
			{40, 95, false, []string{"high:activation"}},
		}},
		{"drop resets pending", []step{{0, 50, false, nil}, {5, 49, false, nil}, {10, 50, false, nil}, {15, 50, false, nil}, {20, 50, false, []string{"high:activation"}}}},
		{"missing resets pending", []step{{0, 50, false, nil}, {5, 0, true, nil}, {10, 50, false, nil}, {15, 50, false, nil}, {20, 50, false, []string{"high:activation"}}}},
		{"invalid does not recover active", []step{{0, 50, false, nil}, {5, 50, false, nil}, {10, 50, false, []string{"high:activation"}}, {15, math.NaN(), false, nil}, {20, math.Inf(1), false, nil}, {25, 49, false, []string{"high:recovery"}}}},
		{"gap resets pending", []step{{0, 50, false, nil}, {11, 50, false, nil}, {16, 50, false, nil}, {21, 50, false, []string{"high:activation"}}}},
		{"ten second gap allowed", []step{{0, 50, false, nil}, {10, 50, false, []string{"high:activation"}}}},
		{"active gap does not recover", []step{{0, 50, false, nil}, {5, 50, false, nil}, {10, 50, false, []string{"high:activation"}}, {21, 50, false, nil}, {26, 49, false, []string{"high:recovery"}}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var got []string
			e := alerts.NewEvaluator(rules, func(event alerts.Event) { got = append(got, event.Rule.ID+":"+event.Kind) })
			start := time.Now()
			for _, s := range tt.steps {
				got = nil
				var points []metrics.MetricPoint
				if !s.missing {
					points = []metrics.MetricPoint{{Sensor: "cpu/total", Value: s.value, Unit: "%"}}
				}
				e.Observe(start.Add(time.Duration(s.sec)*time.Second), points)
				if !reflect.DeepEqual(got, s.want) {
					t.Fatalf("at %ds: got %v, want %v", s.sec, got, s.want)
				}
			}
		})
	}
}

func TestHundredPercentThresholdIsInclusive(t *testing.T) {
	var events []alerts.Event
	e := alerts.NewEvaluator([]alerts.Rule{{ID: "max", Sensor: "cpu/total", Threshold: 100, Duration: 5 * time.Second}}, func(event alerts.Event) { events = append(events, event) })
	start := time.Now()
	point := []metrics.MetricPoint{{Sensor: "cpu/total", Value: 100, Unit: "%"}}
	e.Observe(start, point)
	e.Observe(start.Add(5*time.Second), point)
	if len(events) != 1 || events[0].Kind != "activation" {
		t.Fatalf("events = %+v", events)
	}
}

func TestRestartDoesNotRecoverOrCreditTime(t *testing.T) {
	rules := []alerts.Rule{{ID: "high", Sensor: "cpu/total", Threshold: 50, Duration: 10 * time.Second}}
	var events []alerts.Event
	newEval := func() *alerts.Evaluator {
		return alerts.NewEvaluator(rules, func(e alerts.Event) { events = append(events, e) })
	}
	start := time.Now()
	e := newEval()
	point := []metrics.MetricPoint{{Sensor: "cpu/total", Value: 50, Unit: "%"}}
	e.Observe(start, point)
	e.Observe(start.Add(10*time.Second), point)
	e = newEval()
	e.Observe(start.Add(15*time.Second), []metrics.MetricPoint{{Sensor: "cpu/total", Value: 49}})
	e.Observe(start.Add(20*time.Second), point)
	e.Observe(start.Add(25*time.Second), point)
	if len(events) != 1 {
		t.Fatalf("restart generated events: %+v", events)
	}
	e.Observe(start.Add(30*time.Second), point)
	if len(events) != 2 || events[1].Kind != "activation" {
		t.Fatalf("events: %+v", events)
	}
}
