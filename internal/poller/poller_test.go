package poller

import (
	"database/sql"
	"path/filepath"
	"testing"

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

// TestPollerPoll: one temperature cycle stores every sensor reading.
func TestPollerPoll(t *testing.T) {
	st, db := newDB(t)
	p := New(fakeSensor{map[string]float64{"Core 0": 41.5, "acpitz": 30}}, st, db)

	p.Poll(24)

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

	PollMetric(st, db, "cpu", 24, read)

	got, err := st.QueryLatest(db, "cpu")
	if err != nil {
		t.Fatalf("QueryLatest: %v", err)
	}
	if len(got) != 1 || got[0].Sensor != "cpu/total" || got[0].TempC != 12.5 {
		t.Fatalf("unexpected rows: %+v", got)
	}
}
