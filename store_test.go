package main

import (
	"database/sql"
	"sort"
	"strings"
	"testing"
	"time"
)

// TestSQLiteQuery checks the TimeWindow x metricType contract.
// Python analogy: pytest.mark.parametrize over (window, metric_type) pairs.
// t.Chdir moves into a fresh temp dir, so InitDB (which hardcodes "temps.db")
// creates a throwaway database instead of touching the real one.
func TestSQLiteQuery(t *testing.T) {
	t.Chdir(t.TempDir())

	// Known bug: InitDB on a brand-new file fails (it creates indexes on
	// metric_type before migrateSchema adds that column). Work around it by
	// pre-creating the table with the full schema in the temp dir.
	pre, err := sql.Open("sqlite", "temps.db")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	_, err = pre.Exec(`CREATE TABLE readings (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		sensor TEXT NOT NULL,
		temp_c REAL NOT NULL,
		metric_type TEXT NOT NULL DEFAULT 'temperature',
		unit TEXT NOT NULL DEFAULT 'C',
		created_at TEXT NOT NULL DEFAULT (datetime('now'))
	)`)
	if err != nil {
		t.Fatalf("pre-create: %v", err)
	}
	pre.Close()

	store := NewSQLiteStore()
	db := store.InitDB()
	defer db.Close()

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
			s.sensor, s.metric, s.unit, s.at.Format(dbTimeLayout),
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
		{"range all metrics", rangeWin, "", []string{"cpu/r1", "temp/r1"}},
		{"range temperature", rangeWin, "temperature", []string{"temp/r1"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := store.Query(db, tt.window, tt.metricType)
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

// newTempSQLite builds a throwaway SQLite store in a temp dir (same workaround
// as TestSQLiteQuery for the fresh-DB InitDB bug).
func newTempSQLite(t *testing.T) (*SQLiteStore, *sql.DB) {
	t.Helper()
	t.Chdir(t.TempDir())
	pre, err := sql.Open("sqlite", "temps.db")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	_, err = pre.Exec(`CREATE TABLE readings (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		sensor TEXT NOT NULL,
		temp_c REAL NOT NULL,
		metric_type TEXT NOT NULL DEFAULT 'temperature',
		unit TEXT NOT NULL DEFAULT 'C',
		created_at TEXT NOT NULL DEFAULT (datetime('now'))
	)`)
	if err != nil {
		t.Fatalf("pre-create: %v", err)
	}
	pre.Close()
	store := NewSQLiteStore()
	db := store.InitDB()
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
			s.sensor, s.val, s.metric, base.Add(s.at).Format(dbTimeLayout),
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
			got := store.QueryLatest(db, tt.metricType)
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
					s.sensor, s.metric, s.at.Format(dbTimeLayout),
				); err != nil {
					t.Fatalf("seed: %v", err)
				}
			}
			store.Prune(db, tt.metricType, 3)
			var left []string
			for _, r := range store.Query(db, TimeWindow{Hours: 100}, "") {
				left = append(left, r.Sensor)
			}
			sort.Strings(left)
			if strings.Join(left, ",") != strings.Join(tt.want, ",") {
				t.Fatalf("got %v, want %v", left, tt.want)
			}
		})
	}
}
