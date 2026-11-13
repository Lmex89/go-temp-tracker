package alerts_test

import (
	"fmt"
	"math"
	"reflect"
	"testing"
	"time"

	"sensors-temp/internal/alerts"
	"sensors-temp/internal/metrics"
	"sensors-temp/internal/timeutil"
)

func TestRuleContinuity(t *testing.T) {
	rules := []alerts.Rule{
		{ID: "high", MetricType: "cpu", Sensor: "cpu/total", Threshold: 50, Duration: 10 * time.Second},
		{ID: "saturated", MetricType: "cpu", Sensor: "cpu/total", Threshold: 95, Duration: 5 * time.Second},
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
			e := alerts.NewEvaluator(rules, func(event alerts.Event) { got = append(got, event.Rule.ID+":"+string(event.Kind)) })
			start := time.Now()
			for _, s := range tt.steps {
				got = nil
				var points []metrics.MetricPoint
				if !s.missing {
					points = []metrics.MetricPoint{{Sensor: "cpu/total", Value: s.value, Unit: "%"}}
				}
				e.Observe("cpu", start.Add(time.Duration(s.sec)*time.Second), points)
				if !reflect.DeepEqual(got, s.want) {
					t.Fatalf("at %ds: got %v, want %v", s.sec, got, s.want)
				}
			}
		})
	}
}

func TestHundredPercentThresholdIsInclusive(t *testing.T) {
	var events []alerts.Event
	e := alerts.NewEvaluator([]alerts.Rule{{ID: "max", MetricType: "cpu", Sensor: "cpu/total", Threshold: 100, Duration: 5 * time.Second}}, func(event alerts.Event) { events = append(events, event) })
	start := time.Now()
	point := []metrics.MetricPoint{{Sensor: "cpu/total", Value: 100, Unit: "%"}}
	e.Observe("cpu", start, point)
	e.Observe("cpu", start.Add(5*time.Second), point)
	if len(events) != 1 || events[0].Kind != "activation" {
		t.Fatalf("events = %+v", events)
	}
}

func TestRestartDoesNotRecoverOrCreditTime(t *testing.T) {
	rules := []alerts.Rule{{ID: "high", MetricType: "cpu", Sensor: "cpu/total", Threshold: 50, Duration: 10 * time.Second}}
	var events []alerts.Event
	newEval := func() *alerts.Evaluator {
		return alerts.NewEvaluator(rules, func(e alerts.Event) { events = append(events, e) })
	}
	start := time.Now()
	e := newEval()
	point := []metrics.MetricPoint{{Sensor: "cpu/total", Value: 50, Unit: "%"}}
	e.Observe("cpu", start, point)
	e.Observe("cpu", start.Add(10*time.Second), point)
	e = newEval()
	e.Observe("cpu", start.Add(15*time.Second), []metrics.MetricPoint{{Sensor: "cpu/total", Value: 49}})
	e.Observe("cpu", start.Add(20*time.Second), point)
	e.Observe("cpu", start.Add(25*time.Second), point)
	if len(events) != 1 {
		t.Fatalf("restart generated events: %+v", events)
	}
	e.Observe("cpu", start.Add(30*time.Second), point)
	if len(events) != 2 || events[1].Kind != "activation" {
		t.Fatalf("events: %+v", events)
	}
}

func TestSuspendBreaksPendingButDoesNotRecoverActiveRule(t *testing.T) {
	start := time.Now()
	var events []string
	e := alerts.NewEvaluator([]alerts.Rule{{ID: "high", MetricType: "cpu", Sensor: "cpu/total", Threshold: 50, Duration: 10 * time.Second}}, func(event alerts.Event) { events = append(events, string(event.Kind)) })
	observe := func(monotonicSeconds, bootSeconds int, value float64) {
		at := start.Add(time.Duration(monotonicSeconds) * time.Second)
		e.ObserveAt("cpu", at, time.Duration(bootSeconds)*time.Second, []metrics.MetricPoint{{Sensor: "cpu/total", Value: value, Observation: timeutil.Observation{At: at, BootAt: time.Duration(bootSeconds) * time.Second}}})
	}
	observe(0, 100, 50)
	observe(5, 105, 50)
	observe(10, 3710, 50)
	if len(events) != 0 {
		t.Fatalf("suspension credited as continuity: %v", events)
	}
	observe(15, 3715, 50)
	observe(20, 3720, 50)
	if !reflect.DeepEqual(events, []string{"activation"}) {
		t.Fatalf("events = %v", events)
	}
	observe(25, 7325, 50)
	if len(events) != 1 {
		t.Fatalf("suspension recovered active rule: %v", events)
	}
	observe(30, 7330, 49)
	if !reflect.DeepEqual(events, []string{"activation", "recovery"}) {
		t.Fatalf("events = %v", events)
	}
}

func TestUncertainAcquisitionDoesNotCreditTimeOrRecover(t *testing.T) {
	start := time.Now()
	var events []string
	e := alerts.NewEvaluator([]alerts.Rule{{ID: "hot", MetricType: "temperature", Sensor: "selected", Threshold: 65, Duration: 10 * time.Second}}, func(event alerts.Event) { events = append(events, string(event.Kind)) })
	observe := func(sec int, boot time.Duration, value float64) {
		at := start.Add(time.Duration(sec) * time.Second)
		e.ObserveAt("temperature", at, time.Duration(100+sec)*time.Second, []metrics.MetricPoint{{Sensor: "selected", Value: value, Observation: timeutil.Observation{At: at, BootAt: boot}}})
	}
	observe(0, 100*time.Second, 65)
	observe(5, -1, 64)
	observe(10, 110*time.Second, 65)
	observe(15, 115*time.Second, 65)
	if len(events) != 0 {
		t.Fatalf("credited unknown time: %v", events)
	}
	observe(20, 120*time.Second, 65)
	observe(25, -1, 65)
	if !reflect.DeepEqual(events, []string{"activation"}) {
		t.Fatalf("invalid clock recovered active rule: %v", events)
	}
	observe(30, -1, 64)
	if !reflect.DeepEqual(events, []string{"activation"}) {
		t.Fatalf("uncertain reading recovered active rule: %v", events)
	}
	observe(32, -2, 64)
	if !reflect.DeepEqual(events, []string{"activation"}) {
		t.Fatalf("unavailable clock recovered active rule: %v", events)
	}
	observe(35, 135*time.Second, 64)
	if !reflect.DeepEqual(events, []string{"activation", "recovery"}) {
		t.Fatalf("events = %v", events)
	}
}

func TestChangingClockAvailabilityBreaksContinuity(t *testing.T) {
	start := time.Now()
	var events []string
	e := alerts.NewEvaluator([]alerts.Rule{{ID: "cpu", MetricType: "cpu", Sensor: "cpu/total", Threshold: 50, Duration: 10 * time.Second}}, func(event alerts.Event) { events = append(events, string(event.Kind)) })
	for _, step := range []struct {
		sec  int
		boot time.Duration
	}{{0, 100 * time.Second}, {5, 105 * time.Second}, {10, 0}, {15, 115 * time.Second}, {20, 120 * time.Second}, {25, 125 * time.Second}} {
		at := start.Add(time.Duration(step.sec) * time.Second)
		e.ObserveAt("cpu", at, time.Duration(100+step.sec)*time.Second, []metrics.MetricPoint{{Sensor: "cpu/total", Value: 50, Observation: timeutil.Observation{At: at, BootAt: step.boot}}})
		if step.sec < 25 && len(events) != 0 {
			t.Fatalf("credited time across clock change: %v", events)
		}
	}
	if !reflect.DeepEqual(events, []string{"activation"}) {
		t.Fatalf("events = %v", events)
	}
}

func TestCPUDelayedReturnDoesNotCreditUnobservedTime(t *testing.T) {
	start := time.Now()
	var events []string
	e := alerts.NewEvaluator([]alerts.Rule{{ID: "cpu", MetricType: "cpu", Sensor: "cpu/total", Threshold: 50, Duration: 10 * time.Second}}, func(event alerts.Event) { events = append(events, string(event.Kind)) })
	for _, step := range []struct{ begin, end int }{{0, 1}, {5, 6}, {10, 15}, {15, 16}} {
		begin := start.Add(time.Duration(step.begin) * time.Second)
		end := start.Add(time.Duration(step.end) * time.Second)
		e.Observe("cpu", end, []metrics.MetricPoint{{Sensor: "cpu/total", Value: 50, Observation: timeutil.Observation{StartedAt: begin, At: end}}})
		if step.begin < 15 && len(events) != 0 {
			t.Fatalf("early activation after delayed return: %v", events)
		}
	}
	if !reflect.DeepEqual(events, []string{"activation"}) {
		t.Fatalf("events = %v", events)
	}
}

func TestCPUAcquisitionGapBoundary(t *testing.T) {
	for _, tt := range []struct {
		name                  string
		gap                   time.Duration
		wantSecond, wantThird bool
	}{
		{"exactly ten seconds", 10 * time.Second, false, true},
		{"more than ten seconds", 10*time.Second + time.Nanosecond, false, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			start := time.Now()
			var events []string
			e := alerts.NewEvaluator([]alerts.Rule{{ID: "cpu", MetricType: "cpu", Sensor: "cpu/total", Threshold: 50, Duration: 10 * time.Second}}, func(event alerts.Event) { events = append(events, string(event.Kind)) })
			for i := 0; i < 3; i++ {
				begin := time.Duration(i) * tt.gap
				end := begin + time.Second
				at := start.Add(end)
				point := metrics.MetricPoint{Sensor: "cpu/total", Value: 50, Observation: timeutil.Observation{StartedAt: start.Add(begin), At: at, BootStart: 100*time.Second + begin, BootAt: 100*time.Second + end}}
				e.ObserveAt("cpu", at, point.BootAt, []metrics.MetricPoint{point})
				if i == 1 && (len(events) > 0) != tt.wantSecond {
					t.Fatalf("second reading: %v", events)
				}
				if i == 2 && (len(events) > 0) != tt.wantThird {
					t.Fatalf("third reading: %v", events)
				}
			}
		})
	}
}

func TestCPUAcquisitionGapChecksBothBounds(t *testing.T) {
	for _, tt := range []struct {
		name                   string
		secondStart, secondEnd time.Duration
	}{
		{"start exceeds ten seconds", 10*time.Second + time.Nanosecond, 11 * time.Second},
		{"end exceeds ten seconds", 10 * time.Second, 11*time.Second + time.Nanosecond},
	} {
		t.Run(tt.name, func(t *testing.T) {
			start := time.Now()
			var events []string
			e := alerts.NewEvaluator([]alerts.Rule{{ID: "cpu", MetricType: "cpu", Sensor: "cpu/total", Threshold: 50, Duration: 10 * time.Second}}, func(event alerts.Event) { events = append(events, string(event.Kind)) })
			for _, interval := range [][2]time.Duration{{0, time.Second}, {tt.secondStart, tt.secondEnd}, {tt.secondStart + 10*time.Second, tt.secondEnd + 10*time.Second}} {
				point := metrics.MetricPoint{Sensor: "cpu/total", Value: 50, Observation: timeutil.Observation{StartedAt: start.Add(interval[0]), At: start.Add(interval[1]), BootStart: 100*time.Second + interval[0], BootAt: 100*time.Second + interval[1]}}
				e.ObserveAt("cpu", point.At, point.BootAt, []metrics.MetricPoint{point})
			}
			if len(events) != 0 {
				t.Fatalf("gap credited: %v", events)
			}
		})
	}
}

func TestStaleTemperatureCannotRecoverActiveRule(t *testing.T) {
	for _, suspended := range []bool{false, true} {
		t.Run(fmt.Sprintf("suspended=%v", suspended), func(t *testing.T) {
			start := time.Now()
			var events []string
			e := alerts.NewEvaluator([]alerts.Rule{{ID: "hot", MetricType: "temperature", Sensor: "selected", Threshold: 65, Duration: 10 * time.Second}}, func(event alerts.Event) { events = append(events, string(event.Kind)) })
			for _, sec := range []int{0, 5, 10} {
				at := start.Add(time.Duration(sec) * time.Second)
				boot := time.Duration(100+sec) * time.Second
				e.ObserveAt("temperature", at, boot, []metrics.MetricPoint{{Sensor: "selected", Value: 65, Observation: timeutil.Observation{At: at, BootAt: boot}}})
			}
			acquired := start.Add(15 * time.Second)
			processed := acquired.Add(time.Hour)
			if suspended {
				processed = acquired
			}
			bootNow := time.Duration(3715) * time.Second
			e.ObserveAt("temperature", processed, bootNow, []metrics.MetricPoint{{Sensor: "selected", Value: 64, Observation: timeutil.Observation{At: acquired, BootAt: 115 * time.Second}}})
			if !reflect.DeepEqual(events, []string{"activation"}) {
				t.Fatalf("stale reading recovered active rule: %v", events)
			}
			e.ObserveAt("temperature", processed, bootNow, []metrics.MetricPoint{{Sensor: "selected", Value: 64, Observation: timeutil.Observation{At: processed, BootAt: bootNow}}})
			if !reflect.DeepEqual(events, []string{"activation", "recovery"}) {
				t.Fatalf("events = %v", events)
			}
		})
	}
}

func TestStaleReadingResetsPendingPeriod(t *testing.T) {
	start := time.Now()
	var events []string
	e := alerts.NewEvaluator([]alerts.Rule{{ID: "hot", MetricType: "temperature", Sensor: "selected", Threshold: 65, Duration: 10 * time.Second}}, func(event alerts.Event) { events = append(events, string(event.Kind)) })
	for _, step := range []struct{ acquired, processed int }{{0, 0}, {5, 5}, {10, 21}, {22, 22}, {27, 27}, {32, 32}} {
		at := start.Add(time.Duration(step.acquired) * time.Second)
		now := start.Add(time.Duration(step.processed) * time.Second)
		boot := time.Duration(100+step.acquired) * time.Second
		e.ObserveAt("temperature", now, time.Duration(100+step.processed)*time.Second, []metrics.MetricPoint{{Sensor: "selected", Value: 65, Observation: timeutil.Observation{At: at, BootAt: boot}}})
		if step.processed < 32 && len(events) != 0 {
			t.Fatalf("stale reading credited: %v", events)
		}
	}
	if !reflect.DeepEqual(events, []string{"activation"}) {
		t.Fatalf("events = %v", events)
	}
}

func TestFreshnessUsesEarliestPossibleAcquisition(t *testing.T) {
	start := time.Now()
	var events []string
	e := alerts.NewEvaluator([]alerts.Rule{{ID: "cpu", MetricType: "cpu", Sensor: "cpu/total", Threshold: 50, Duration: 10 * time.Second}}, func(event alerts.Event) { events = append(events, string(event.Kind)) })
	for _, sec := range []int{0, 5, 10} {
		at := start.Add(time.Duration(sec) * time.Second)
		boot := time.Duration(100+sec) * time.Second
		e.ObserveAt("cpu", at, boot, []metrics.MetricPoint{{Sensor: "cpu/total", Value: 50, Observation: timeutil.Observation{At: at, BootAt: boot}}})
	}
	point := metrics.MetricPoint{Sensor: "cpu/total", Value: 49, Observation: timeutil.Observation{StartedAt: start.Add(15 * time.Second), At: start.Add(23 * time.Second), BootStart: 115 * time.Second, BootAt: 123 * time.Second}}
	e.ObserveAt("cpu", start.Add(26*time.Second), 126*time.Second, []metrics.MetricPoint{point})
	if !reflect.DeepEqual(events, []string{"activation"}) {
		t.Fatalf("uncertain old acquisition recovered rule: %v", events)
	}
}
