package poller

import (
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	"sensors-temp/internal/alerts"
	"sensors-temp/internal/metrics"
	"sensors-temp/internal/store"
)

// fakeSensor satisfies sensor.Reader (Go interfaces are structural, like
// Python's duck typing / typing.Protocol), so no real hardware is needed.
type fakeSensor struct{ temps map[string]float64 }

func (f fakeSensor) Read() map[string]float64 { return f.temps }

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
		}, func(points []metrics.MetricPoint) { eval.Observe(now, points) })
		if i < 12 && len(events) != 0 {
			t.Fatal("activated before full duration")
		}
	}
	if len(events) != 1 || events[0].Kind != "activation" {
		t.Fatalf("events: %+v", events)
	}
	PollMetric(st, db, "cpu", 24, func() []metrics.MetricPoint {
		return []metrics.MetricPoint{{Sensor: "cpu/total", Value: 49, Unit: "%"}}
	}, func(points []metrics.MetricPoint) { eval.Observe(start.Add(65*time.Second), points) })
	if len(events) != 2 || events[1].Kind != "recovery" {
		t.Fatalf("events: %+v", events)
	}
	rows, err := st.Query(db, store.TimeWindow{Hours: 1}, "cpu")
	if err != nil || len(rows) != 14 {
		t.Fatalf("readings = %d, error = %v", len(rows), err)
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

func TestAlertsDoNotDependOnPersistence(t *testing.T) {
	st, db := newDB(t)
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	var events []alerts.Event
	e := alerts.NewEvaluator([]alerts.Rule{{ID: "high", Sensor: "cpu/total", Threshold: 50, Duration: 5 * time.Second}}, func(event alerts.Event) { events = append(events, event) })
	start := time.Now()
	for i := 0; i < 2; i++ {
		now := start.Add(time.Duration(i) * 5 * time.Second)
		PollMetric(st, db, "cpu", 24, func() []metrics.MetricPoint {
			return []metrics.MetricPoint{{Sensor: "cpu/total", Value: 50, Unit: "%"}}
		}, func(points []metrics.MetricPoint) { e.Observe(now, points) })
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
