package store

import (
	"sensors-temp/internal/timeutil"

	"database/sql"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"
)

// TestSQLiteQuery checks the TimeWindow x metricType contract.
// Python analogy: pytest.mark.parametrize over (window, metric_type) pairs.
// The store gets a path inside t.TempDir(), so the real temps.db is never touched.
func TestSQLiteQuery(t *testing.T) {
	store, db := newTempSQLite(t)

	now := time.Now().UTC()
	fixed := time.Date(2024, 1, 15, 12, 0, 0, 0, time.UTC)
	seed := []struct {
		sensor, metric, unit string
		at                   time.Time
	}{
		{"temp/a", "temperature", "C", now.Add(-30 * time.Minute)},
		{"cpu/total", "cpu", "%", now.Add(-20 * time.Minute)},
		{"temp/old", "temperature", "C", now.Add(-3 * time.Hour)},
		{"temp/r1", "temperature", "C", fixed},
		{"cpu/r1", "cpu", "%", fixed.Add(time.Minute)},
		{"temp/r2", "temperature", "C", fixed.Add(2 * time.Hour)},
	}
	for _, s := range seed {
		_, err := db.Exec(
			`INSERT INTO readings (sensor, temp_c, metric_type, unit, created_at) VALUES (?, 1.0, ?, ?, ?)`,
			s.sensor, s.metric, s.unit, s.at.Format(timeutil.DBLayout),
		)
		if err != nil {
			t.Fatalf("seed: %v", err)
		}
	}

	rangeWin := TimeWindow{From: fixed.Add(-time.Minute), To: fixed.Add(time.Hour)}
	tests := []struct {
		name       string
		window     TimeWindow
		metricType string
		want       []string
	}{
		{"hours all metrics", TimeWindow{Hours: 1}, "", []string{"cpu/total", "temp/a"}},
		{"hours temperature", TimeWindow{Hours: 1}, "temperature", []string{"temp/a"}},
		{"hours cpu", TimeWindow{Hours: 1}, "cpu", []string{"cpu/total"}},
		{"hours empty", TimeWindow{Hours: 1}, "disk", nil},
		{"range all metrics", rangeWin, "", []string{"cpu/r1", "temp/r1"}},
		{"range temperature", rangeWin, "temperature", []string{"temp/r1"}},
		{"range empty", rangeWin, "disk", nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := mustQuery(t, store, db, tt.window, tt.metricType)
			var sensors []string
			for _, r := range got {
				sensors = append(sensors, r.Sensor)
			}
			sort.Strings(sensors)
			if len(sensors) != len(tt.want) {
				t.Fatalf("got %v, want %v", sensors, tt.want)
			}
			for i := range sensors {
				if sensors[i] != tt.want[i] {
					t.Fatalf("got %v, want %v", sensors, tt.want)
				}
			}
		})
	}
}

// newTempSQLite builds a throwaway SQLite store in a fresh temp dir.
// Python analogy: a pytest tmp_path fixture passed to sqlite3.connect().
func newTempSQLite(t *testing.T) (*SQLiteStore, *sql.DB) {
	t.Helper()
	store := NewSQLiteStore(filepath.Join(t.TempDir(), "test.db"))
	db := store.InitDB()
	if db == nil {
		t.Fatal("InitDB returned nil on a brand-new file")
	}
	t.Cleanup(func() { db.Close() })
	return store, db
}

// TestSQLiteQueryLatest checks QueryLatest returns the newest row per sensor
// for exactly one metric type. Python analogy: pytest.mark.parametrize.
func TestSQLiteQueryLatest(t *testing.T) {
	store, db := newTempSQLite(t)

	base := time.Date(2024, 1, 15, 12, 0, 0, 0, time.UTC)
	seed := []struct {
		sensor, metric string
		val            float64
		at             time.Duration
	}{
		{"temp/a", "temperature", 40, 0},
		{"temp/a", "temperature", 55, time.Minute}, // newest for temp/a
		{"temp/b", "temperature", 30, 0},
		{"cpu/total", "cpu", 10, 0},
		{"cpu/total", "cpu", 90, time.Minute}, // newest for cpu/total
		{"mem/used", "memory", 50, 0},
	}
	for _, s := range seed {
		_, err := db.Exec(
			`INSERT INTO readings (sensor, temp_c, metric_type, unit, created_at) VALUES (?, ?, ?, 'x', ?)`,
			s.sensor, s.val, s.metric, base.Add(s.at).Format(timeutil.DBLayout),
		)
		if err != nil {
			t.Fatalf("seed: %v", err)
		}
	}

	tests := []struct {
		name       string
		metricType string
		want       map[string]float64 // sensor -> latest value
	}{
		{"temperature", "temperature", map[string]float64{"temp/a": 55, "temp/b": 30}},
		{"cpu", "cpu", map[string]float64{"cpu/total": 90}},
		{"memory", "memory", map[string]float64{"mem/used": 50}},
		{"unknown metric", "nope", map[string]float64{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := mustLatest(t, store, db, tt.metricType)
			if len(got) != len(tt.want) {
				t.Fatalf("got %d rows (%v), want %d", len(got), got, len(tt.want))
			}
			for _, r := range got {
				if v, ok := tt.want[r.Sensor]; !ok || v != r.TempC {
					t.Fatalf("sensor %s value %v, want %v", r.Sensor, r.TempC, v)
				}
			}
		})
	}
}

// TestSQLitePrune checks that Prune(metricType) only deletes old rows of that
// type, and that an empty metricType deletes old rows of every type (the
// behavior the temperature Poller relied on). Python analogy: parametrize.
func TestSQLitePrune(t *testing.T) {
	tests := []struct {
		name       string
		metricType string
		want       []string // sensors left, sorted
	}{
		{"one metric", "cpu", []string{"cpu/new", "temp/new", "temp/old"}},
		{"all metrics", "", []string{"cpu/new", "temp/new"}},
		{"unknown metric", "disk", []string{"cpu/new", "cpu/old", "temp/new", "temp/old"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store, db := newTempSQLite(t)
			now := time.Now().UTC()
			seed := []struct {
				sensor, metric string
				at             time.Time
			}{
				{"temp/new", "temperature", now.Add(-time.Hour)},
				{"temp/old", "temperature", now.Add(-5 * time.Hour)},
				{"cpu/new", "cpu", now.Add(-time.Hour)},
				{"cpu/old", "cpu", now.Add(-5 * time.Hour)},
			}
			for _, s := range seed {
				if _, err := db.Exec(
					`INSERT INTO readings (sensor, temp_c, metric_type, unit, created_at) VALUES (?, 1, ?, 'x', ?)`,
					s.sensor, s.metric, s.at.Format(timeutil.DBLayout),
				); err != nil {
					t.Fatalf("seed: %v", err)
				}
			}
			store.Prune(db, tt.metricType, 3)
			var left []string
			for _, r := range mustQuery(t, store, db, TimeWindow{Hours: 100}, AllMetrics) {
				left = append(left, r.Sensor)
			}
			sort.Strings(left)
			if strings.Join(left, ",") != strings.Join(tt.want, ",") {
				t.Fatalf("got %v, want %v", left, tt.want)
			}
		})
	}
}

// mustQuery / mustLatest call the store and fail the test on error, so table
// tests can keep their one-line style. Python analogy: a small helper that
// calls the function and does pytest.fail() on exception.
func mustQuery(t *testing.T, s Store, db *sql.DB, w TimeWindow, metric string) []Reading {
	t.Helper()
	got, err := s.Query(db, w, metric)
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	return got
}

func mustLatest(t *testing.T, s Store, db *sql.DB, metric string) []Reading {
	t.Helper()
	got, err := s.QueryLatest(db, metric)
	if err != nil {
		t.Fatalf("QueryLatest: %v", err)
	}
	return got
}

// checkInvalidInputs asserts the validation errors shared by both adapters:
// a zero/half-set/reversed TimeWindow and an empty QueryLatest metric type.
func checkInvalidInputs(t *testing.T, s Store, db *sql.DB) {
	t.Helper()
	now := time.Now()
	bad := []struct {
		name string
		win  TimeWindow
	}{
		{"zero window", TimeWindow{}},
		{"negative hours", TimeWindow{Hours: -1}},
		{"only From", TimeWindow{From: now}},
		{"only To", TimeWindow{To: now}},
		{"From after To", TimeWindow{From: now, To: now.Add(-time.Hour)}},
	}
	for _, tt := range bad {
		t.Run("Query "+tt.name, func(t *testing.T) {
			if _, err := s.Query(db, tt.win, AllMetrics); err == nil {
				t.Fatalf("expected error for %+v", tt.win)
			}
		})
	}
	t.Run("QueryLatest empty metric", func(t *testing.T) {
		if _, err := s.QueryLatest(db, AllMetrics); err == nil {
			t.Fatal("expected error for empty metricType")
		}
	})
}

// TestSQLiteInvalidInputs covers window validation and the required metric type.
func TestSQLiteInvalidInputs(t *testing.T) {
	store, db := newTempSQLite(t)
	checkInvalidInputs(t, store, db)
}

func TestSQLiteQueryExecutionErrors(t *testing.T) {
	st, db := newTempSQLite(t)
	if _, err := db.Exec("DROP TABLE readings"); err != nil {
		t.Fatal(err)
	}
	var s Store = st
	for _, query := range []struct {
		name string
		run  func() ([]Reading, error)
	}{
		{"historical", func() ([]Reading, error) { return s.Query(db, TimeWindow{Hours: 1}, "temperature") }},
		{"latest", func() ([]Reading, error) { return s.QueryLatest(db, "temperature") }},
	} {
		t.Run(query.name, func(t *testing.T) {
			got, err := query.run()
			if err == nil || len(got) != 0 {
				t.Fatalf("got readings %v, error %v; want error and no readings", got, err)
			}
		})
	}
}

func TestSQLiteQueryScanErrorsDiscardReadings(t *testing.T) {
	st, db := newTempSQLite(t)
	if _, err := db.Exec(`INSERT INTO readings (sensor, temp_c, metric_type, unit, created_at) VALUES
		('a-good', 42, 'temperature', 'C', datetime('now', '-1 minute')),
		('b-bad', 'not-a-number', 'temperature', 'C', datetime('now'))`); err != nil {
		t.Fatal(err)
	}
	var s Store = st
	for _, query := range []struct {
		name string
		run  func() ([]Reading, error)
	}{
		{"historical", func() ([]Reading, error) { return s.Query(db, TimeWindow{Hours: 1}, "temperature") }},
		{"latest", func() ([]Reading, error) { return s.QueryLatest(db, "temperature") }},
	} {
		t.Run(query.name, func(t *testing.T) {
			got, err := query.run()
			if err == nil || len(got) != 0 {
				t.Fatalf("got readings %v, error %v; want error and no partial readings", got, err)
			}
		})
	}
}

// TestSQLiteMigratesLegacySchema builds a file with the OLD readings table
// (no metric_type/unit), runs InitDB and checks the columns, the old row's
// defaults and the indexes. Python analogy: an Alembic upgrade test on a
// fixture database created with the previous schema.
func TestSQLiteMigratesLegacySchema(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy.db")
	old, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	_, err = old.Exec(`CREATE TABLE readings (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		sensor TEXT NOT NULL,
		temp_c REAL NOT NULL,
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP
	); INSERT INTO readings (sensor, temp_c) VALUES ('legacy/core', 42.0);`)
	if err != nil {
		t.Fatal(err)
	}
	old.Close()

	db := NewSQLiteStore(path).InitDB()
	if db == nil {
		t.Fatal("InitDB returned nil on a legacy file")
	}
	defer db.Close()

	var metric, unit string
	if err := db.QueryRow("SELECT metric_type, unit FROM readings WHERE sensor='legacy/core'").Scan(&metric, &unit); err != nil {
		t.Fatalf("legacy row not readable after migration: %v", err)
	}
	if metric != "temperature" || unit != "C" {
		t.Errorf("defaults = (%q, %q), want (temperature, C)", metric, unit)
	}

	for _, idx := range []string{
		"idx_readings_created_at",
		"idx_readings_metric_type_created_at",
		"idx_readings_metric_type_sensor_id",
	} {
		var n int
		if err := db.QueryRow("SELECT COUNT(*) FROM sqlite_master WHERE type='index' AND name=?", idx).Scan(&n); err != nil || n != 1 {
			t.Errorf("index %s missing (n=%d, err=%v)", idx, n, err)
		}
	}
}
