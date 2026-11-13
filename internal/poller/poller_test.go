package poller

import (
	"bytes"
	"database/sql"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"sensors-temp/internal/alerts"
	"sensors-temp/internal/logging"
	"sensors-temp/internal/metrics"
	"sensors-temp/internal/sensor"
	"sensors-temp/internal/store"
)

func TestObservedTemperatureMissingWarnsOncePerOutage(t *testing.T) {
	var output bytes.Buffer
	old := logging.Logger
	logging.Logger = logging.New(&output, "INFO")
	t.Cleanup(func() { logging.Logger = old })
	st, db := newDB(t)
	reader := &fakeSensor{}
	p := New(reader, st, db)
	for i := 0; i < 3; i++ {
		p.Poll(24, func([]metrics.MetricPoint) {})
	}
	reader.temps = map[string]float64{"selected": 60}
	p.Poll(24, func([]metrics.MetricPoint) {})
	reader.temps = nil
	p.Poll(24, func([]metrics.MetricPoint) {})
	if got := strings.Count(output.String(), "No temperature sensors found"); got != 2 {
		t.Fatalf("warnings = %d, logs: %s", got, output.String())
	}
}

func TestMissingThermalZonesDoNotFloodLogs(t *testing.T) {
	var output bytes.Buffer
	old := logging.Logger
	logging.Logger = logging.New(&output, "INFO")
	t.Cleanup(func() { logging.Logger = old })
	st, db := newDB(t)
	root := t.TempDir()
	p := New(sensor.NewLinuxThermalSensorAt(root), st, db)
	for i := 0; i < 3; i++ {
		p.Poll(24, func([]metrics.MetricPoint) {})
	}
	zone := filepath.Join(root, "class/thermal/thermal_zone0")
	if err := os.MkdirAll(zone, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(zone, "temp"), []byte("65000"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(zone, "type"), []byte("selected"), 0600); err != nil {
		t.Fatal(err)
	}
	p.Poll(24, func([]metrics.MetricPoint) {})
	if err := os.Remove(filepath.Join(zone, "temp")); err != nil {
		t.Fatal(err)
	}
	p.Poll(24, func([]metrics.MetricPoint) {})
	p.Poll(24, func([]metrics.MetricPoint) {})
	got := output.String()
	if strings.Count(got, "No thermal zones found") != 1 || strings.Count(got, "Cannot read thermal_zone0/temp") != 1 || strings.Count(got, "No temperature sensors found") != 2 {
		t.Fatalf("unexpected logs: %s", got)
	}
}

// fakeSensor satisfies sensor.Reader (Go interfaces are structural, like
// Python's duck typing / typing.Protocol), so no real hardware is needed.
type fakeSensor struct{ temps map[string]float64 }

func (f fakeSensor) Read() map[string]float64 { return f.temps }

type timedSensor struct{ readings map[string]sensor.Reading }

func (s timedSensor) Read() map[string]float64             { panic("expected timed readings") }
func (s timedSensor) ReadTimed() map[string]sensor.Reading { return s.readings }

func TestTemperatureUsesEachReadingTime(t *testing.T) {
	st, db := newDB(t)
	start := time.Now()
	reader := &timedSensor{}
	p := New(reader, st, db)
	var events []alerts.Event
	e := alerts.NewEvaluator([]alerts.Rule{{ID: "hot", MetricType: "temperature", Sensor: "selected", Threshold: 65, Duration: 10 * time.Second}}, func(event alerts.Event) { events = append(events, event) })
	for _, seconds := range []int{0, 5, 9} {
		delay := 5
		if seconds == 0 {
			delay = 0
		}
		reader.readings = map[string]sensor.Reading{
			"selected": {Value: 65, At: start.Add(time.Duration(seconds) * time.Second)},
			"slow":     {Value: 40, At: start.Add(time.Duration(seconds+delay) * time.Second)},
		}
		p.Poll(24, func(points []metrics.MetricPoint) {
			e.Observe("temperature", start.Add(time.Duration(seconds+delay)*time.Second), points)
		})
		if len(events) != 0 {
			t.Fatalf("early activation: %+v", events)
		}
	}
	reader.readings = map[string]sensor.Reading{"selected": {Value: 65, At: start.Add(10 * time.Second)}}
	p.Poll(24, func(points []metrics.MetricPoint) { e.Observe("temperature", start.Add(19*time.Second), points) })
	if len(events) != 1 || !events[0].At.Equal(start.Add(10*time.Second)) {
		t.Fatalf("events = %+v", events)
	}
}

func TestTemperatureSuspendBreaksPendingThroughPoller(t *testing.T) {
	st, db := newDB(t)
	start := time.Now()
	reader := &timedSensor{}
	p := New(reader, st, db)
	var events []string
	e := alerts.NewEvaluator([]alerts.Rule{{ID: "hot", MetricType: "temperature", Sensor: "selected", Threshold: 65, Duration: 10 * time.Second}}, func(event alerts.Event) { events = append(events, event.Kind) })
	for _, step := range []struct{ sec, boot int }{{0, 100}, {5, 105}, {10, 3710}, {15, 3715}, {20, 3720}} {
		at := start.Add(time.Duration(step.sec) * time.Second)
		reader.readings = map[string]sensor.Reading{"selected": {Value: 65, At: at, BootAt: time.Duration(step.boot) * time.Second}}
		p.Poll(24, func(points []metrics.MetricPoint) { e.Observe("temperature", at, points) })
		if step.sec < 20 && len(events) != 0 {
			t.Fatalf("early activation after suspension: %v", events)
		}
	}
	if !reflect.DeepEqual(events, []string{"activation"}) {
		t.Fatalf("events = %v", events)
	}
}

func TestTemperatureLegacyReaderIncludesSuspendClock(t *testing.T) {
	st, db := newDB(t)
	p := New(fakeSensor{map[string]float64{"selected": 65}}, st, db)
	p.Poll(24, func(points []metrics.MetricPoint) {
		if len(points) != 1 || points[0].BootAt <= 0 {
			t.Fatalf("missing suspend-aware timestamp: %+v", points)
		}
	})
}

// newDB builds a throwaway SQLite store in t.TempDir(); temps.db is never touched.
func newDB(t *testing.T) (store.Store, *sql.DB) {
	t.Helper()
	st := store.NewSQLiteStore(filepath.Join(t.TempDir(), "test.db"))
	db := st.InitDB()
	if db == nil {
		t.Fatal("InitDB returned nil")
	}
	t.Cleanup(func() { db.Close() })
	return st, db
}

func TestCPUAlertFlow(t *testing.T) {
	st, db := newDB(t)
	var events []alerts.Event
	rules := []alerts.Rule{{ID: "high", MetricType: "cpu", Sensor: "cpu/total", Threshold: 50, Duration: time.Minute}}
	eval := alerts.NewEvaluator(rules, func(e alerts.Event) { events = append(events, e) })
	start := time.Now()
	for i := 0; i <= 12; i++ {
		now := start.Add(time.Duration(i) * 5 * time.Second)
		PollMetric(st, db, "cpu", 24, func() []metrics.MetricPoint {
			return []metrics.MetricPoint{{Sensor: "cpu/total", Value: 50, Unit: "%"}}
		}, func(points []metrics.MetricPoint) { eval.Observe("cpu", now, points) })
		if i < 12 && len(events) != 0 {
			t.Fatal("activated before full duration")
		}
	}
	if len(events) != 1 || events[0].Kind != "activation" {
		t.Fatalf("events: %+v", events)
	}
	PollMetric(st, db, "cpu", 24, func() []metrics.MetricPoint {
		return []metrics.MetricPoint{{Sensor: "cpu/total", Value: 49, Unit: "%"}}
	}, func(points []metrics.MetricPoint) { eval.Observe("cpu", start.Add(65*time.Second), points) })
	if len(events) != 2 || events[1].Kind != "recovery" {
		t.Fatalf("events: %+v", events)
	}
	rows, err := st.Query(db, store.TimeWindow{Hours: 1}, "cpu")
	if err != nil || len(rows) != 14 {
		t.Fatalf("readings = %d, error = %v", len(rows), err)
	}
}

func TestCPUAlertUsesAcquisitionTimeInsteadOfDelayedProcessing(t *testing.T) {
	st, db := newDB(t)
	start := time.Now()
	var events []alerts.Event
	e := alerts.NewEvaluator([]alerts.Rule{{ID: "cpu", MetricType: "cpu", Sensor: "cpu/total", Threshold: 50, Duration: 10 * time.Second}}, func(event alerts.Event) { events = append(events, event) })
	for _, step := range []struct{ acquired, processed int }{{0, 0}, {5, 10}, {9, 14}, {10, 15}} {
		point := metrics.MetricPoint{Sensor: "cpu/total", Value: 50, Unit: "%", At: start.Add(time.Duration(step.acquired) * time.Second), BootAt: time.Duration(100+step.acquired) * time.Second}
		PollMetric(st, db, "cpu", 24, func() []metrics.MetricPoint { return []metrics.MetricPoint{point} }, func(points []metrics.MetricPoint) {
			e.Observe("cpu", start.Add(time.Duration(step.processed)*time.Second), points)
		})
		if step.acquired < 10 && len(events) != 0 {
			t.Fatalf("early activation: %+v", events)
		}
	}
	if len(events) != 1 || !events[0].At.Equal(start.Add(10*time.Second)) {
		t.Fatalf("events = %+v", events)
	}
}

func TestTemperatureAndCPURulesStayIndependent(t *testing.T) {
	st, db := newDB(t)
	reader := &fakeSensor{}
	p := New(reader, st, db)
	rules := []alerts.Rule{
		{ID: "package", MetricType: "temperature", Sensor: "coretemp/Package id 0", Threshold: 65, Duration: 2 * time.Minute},
		{ID: "other", MetricType: "temperature", Sensor: "acpitz", Threshold: 70, Duration: 10 * time.Second},
		{ID: "same-name", MetricType: "temperature", Sensor: "cpu/total", Threshold: 90, Duration: 10 * time.Second},
		{ID: "cpu", MetricType: "cpu", Sensor: "cpu/total", Threshold: 50, Duration: 10 * time.Second},
	}
	var events []string
	e := alerts.NewEvaluator(rules, func(event alerts.Event) { events = append(events, event.Rule.ID+":"+event.Kind) })
	start := time.Now()
	for sec := 0; sec <= 250; sec += 5 {
		reader.temps = map[string]float64{"coretemp/Package id 0": 65, "coretemp/Core 0": 90, "cpu/total": 0}
		if sec == 125 {
			reader.temps["coretemp/Package id 0"] = 64
		}
		switch sec {
		case 0, 10, 20, 30, 35, 40:
			reader.temps["acpitz"] = 80
		case 15:
			reader.temps["acpitz"] = math.NaN()
		case 25, 55:
			reader.temps["acpitz"] = 60
		case 50:
			reader.temps["acpitz"] = math.Inf(1)
		}
		now := start.Add(time.Duration(sec) * time.Second)
		p.Poll(24, func(points []metrics.MetricPoint) { e.Observe("temperature", now, points) })
		cpu := 50.0
		if sec >= 125 {
			cpu = 49
		}
		PollMetric(st, db, "cpu", 24, func() []metrics.MetricPoint {
			return []metrics.MetricPoint{{Sensor: "cpu/total", Value: cpu, Unit: "%"}}
		}, func(points []metrics.MetricPoint) { e.Observe("cpu", now, points) })
		if sec == 115 && len(events) != 3 {
			t.Fatalf("early package activation: %v", events)
		}
	}
	want := []string{"cpu:activation", "other:activation", "other:recovery", "package:activation", "package:recovery", "cpu:recovery", "package:activation"}
	if !reflect.DeepEqual(events, want) {
		t.Fatalf("events = %v, want %v", events, want)
	}
}

func TestConcurrentTemperatureAndCPUObservation(t *testing.T) {
	st, db := newDB(t)
	db.SetMaxOpenConns(1)
	reader := fakeSensor{map[string]float64{"cpu/total": 65}}
	p := New(reader, st, db)
	rules := []alerts.Rule{
		{ID: "temperature", MetricType: "temperature", Sensor: "cpu/total", Threshold: 65, Duration: 10 * time.Second},
		{ID: "cpu", MetricType: "cpu", Sensor: "cpu/total", Threshold: 50, Duration: 10 * time.Second},
	}
	counts := map[string]int{}
	e := alerts.NewEvaluator(rules, func(event alerts.Event) { counts[event.Rule.ID+":"+event.Kind]++ })
	start := time.Now()
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		for i := 0; i < 10; i++ {
			now := start.Add(time.Duration(i) * 5 * time.Second)
			p.Poll(24, func(points []metrics.MetricPoint) { e.Observe("temperature", now, points) })
		}
	}()
	go func() {
		defer wg.Done()
		for i := 0; i < 10; i++ {
			now := start.Add(time.Duration(i) * 5 * time.Second)
			PollMetric(st, db, "cpu", 24, func() []metrics.MetricPoint {
				return []metrics.MetricPoint{{Sensor: "cpu/total", Value: 50, Unit: "%"}}
			}, func(points []metrics.MetricPoint) { e.Observe("cpu", now, points) })
		}
	}()
	wg.Wait()
	if !reflect.DeepEqual(counts, map[string]int{"temperature:activation": 1, "cpu:activation": 1}) {
		t.Fatalf("events: %v", counts)
	}
}

func TestTemperatureContinuityWithMissingInvalidAndGaps(t *testing.T) {
	st, db := newDB(t)
	reader := &fakeSensor{}
	p := New(reader, st, db)
	var events []string
	e := alerts.NewEvaluator([]alerts.Rule{{ID: "hot", MetricType: "temperature", Sensor: "selected", Threshold: 65, Duration: 10 * time.Second}}, func(event alerts.Event) {
		events = append(events, event.Kind)
	})
	start := time.Now()
	steps := []struct {
		sec    int
		values map[string]float64
		want   []string
	}{
		{0, map[string]float64{"selected": 65}, nil},
		{11, map[string]float64{"selected": 65}, nil},
		{16, map[string]float64{"selected": 65}, nil},
		{21, map[string]float64{"selected": 65}, []string{"activation"}},
		{26, nil, nil},
		{31, map[string]float64{"selected": math.NaN()}, nil},
		{36, map[string]float64{"selected": math.Inf(-1)}, nil},
		{41, map[string]float64{"selected": 64}, []string{"recovery"}},
		{46, map[string]float64{"selected": 65}, nil},
		{51, map[string]float64{"selected": 64}, nil},
		{56, map[string]float64{"selected": 65}, nil},
		{61, map[string]float64{"selected": 65}, nil},
		{66, map[string]float64{"selected": 65}, []string{"activation"}},
	}
	for _, step := range steps {
		events = nil
		reader.temps = step.values
		now := start.Add(time.Duration(step.sec) * time.Second)
		p.Poll(24, func(points []metrics.MetricPoint) { e.Observe("temperature", now, points) })
		if !reflect.DeepEqual(events, step.want) {
			t.Fatalf("at %ds: events %v, want %v", step.sec, events, step.want)
		}
	}
}

func TestTemperatureAlertsDoNotDependOnPersistence(t *testing.T) {
	st, db := newDB(t)
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	p := New(fakeSensor{map[string]float64{"selected": 65}}, st, db)
	var events []string
	e := alerts.NewEvaluator([]alerts.Rule{{ID: "hot", MetricType: "temperature", Sensor: "selected", Threshold: 65, Duration: 5 * time.Second}}, func(event alerts.Event) { events = append(events, event.Kind) })
	start := time.Now()
	for i := 0; i < 2; i++ {
		now := start.Add(time.Duration(i) * 5 * time.Second)
		p.Poll(24, func(points []metrics.MetricPoint) { e.Observe("temperature", now, points) })
	}
	if !reflect.DeepEqual(events, []string{"activation"}) {
		t.Fatalf("events = %v", events)
	}
}

func TestObservedCadenceIncludesReadTime(t *testing.T) {
	st, db := newDB(t)
	now := time.Now()
	var starts []time.Time
	var waits []time.Duration
	read := func() []metrics.MetricPoint {
		starts = append(starts, now)
		if len(starts) == 1 {
			now = now.Add(time.Second)
		} else {
			now = now.Add(6 * time.Second)
		}
		return []metrics.MetricPoint{{Sensor: "cpu/total", Value: 50, Unit: "%"}}
	}
	runMetricObserved(st, db, "cpu", 5, 24, read, func([]metrics.MetricPoint) {}, func() time.Time { return now }, func(d time.Duration) bool {
		waits = append(waits, d)
		now = now.Add(d)
		return len(waits) < 2
	})
	if len(starts) != 2 || starts[1].Sub(starts[0]) != 5*time.Second || waits[0] != 4*time.Second || waits[1] != 0 {
		t.Fatalf("starts %v, waits %v", starts, waits)
	}
}

func TestTemperatureObservedCadenceStoresEachCycle(t *testing.T) {
	st, db := newDB(t)
	p := New(fakeSensor{map[string]float64{"coretemp/Package id 0": 65}}, st, db)
	now := time.Now()
	var starts []time.Time
	p.runObserved(5, 24, func([]metrics.MetricPoint) {
		starts = append(starts, now)
		now = now.Add(time.Second)
	}, func() time.Time { return now }, func(d time.Duration) bool {
		if d != 4*time.Second {
			t.Errorf("wait = %v", d)
		}
		now = now.Add(d)
		return len(starts) < 3
	})
	if len(starts) != 3 || starts[1].Sub(starts[0]) != 5*time.Second || starts[2].Sub(starts[1]) != 5*time.Second {
		t.Fatalf("starts = %v", starts)
	}
	rows, err := st.Query(db, store.TimeWindow{Hours: 1}, "temperature")
	if err != nil || len(rows) != 3 {
		t.Fatalf("stored %d rows: %v", len(rows), err)
	}
}

func TestAlertsDoNotDependOnPersistence(t *testing.T) {
	st, db := newDB(t)
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	var events []alerts.Event
	e := alerts.NewEvaluator([]alerts.Rule{{ID: "high", MetricType: "cpu", Sensor: "cpu/total", Threshold: 50, Duration: 5 * time.Second}}, func(event alerts.Event) { events = append(events, event) })
	start := time.Now()
	for i := 0; i < 2; i++ {
		now := start.Add(time.Duration(i) * 5 * time.Second)
		PollMetric(st, db, "cpu", 24, func() []metrics.MetricPoint {
			return []metrics.MetricPoint{{Sensor: "cpu/total", Value: 50, Unit: "%"}}
		}, func(points []metrics.MetricPoint) { e.Observe("cpu", now, points) })
	}
	if len(events) != 1 || events[0].Kind != "activation" {
		t.Fatalf("events: %+v", events)
	}
}

// TestPollerPoll: one temperature cycle stores every sensor reading.
func TestPollerPoll(t *testing.T) {
	st, db := newDB(t)
	p := New(fakeSensor{map[string]float64{"Core 0": 41.5, "acpitz": 30}}, st, db)

	p.poll(24)

	got, err := st.Query(db, store.TimeWindow{Hours: 1}, "temperature")
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("want 2 temperature rows, got %d", len(got))
	}
	for _, r := range got {
		if r.Unit != "C" {
			t.Errorf("sensor %s: unit %q, want C", r.Sensor, r.Unit)
		}
	}
}

// TestPollMetric: one system-metric cycle stores points under the given type.
func TestPollMetric(t *testing.T) {
	st, db := newDB(t)
	read := func() []metrics.MetricPoint {
		return []metrics.MetricPoint{{Sensor: "cpu/total", Value: 12.5, Unit: "%"}}
	}

	pollMetric(st, db, "cpu", 24, read)

	got, err := st.QueryLatest(db, "cpu")
	if err != nil {
		t.Fatalf("QueryLatest: %v", err)
	}
	if len(got) != 1 || got[0].Sensor != "cpu/total" || got[0].TempC != 12.5 {
		t.Fatalf("unexpected rows: %+v", got)
	}
}
