package main

import (
	"database/sql"
	"sort"
	"testing"
	"time"
)

// TestSQLiteQueryWindow checks the TimeWindow x metricType contract.
// Python analogy: pytest.mark.parametrize over (window, metric_type) pairs.
// t.Chdir moves into a fresh temp dir, so InitDB (which hardcodes "temps.db")
// creates a throwaway database instead of touching the real one.
func TestSQLiteQueryWindow(t *testing.T) {
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
			got := store.QueryWindow(db, tt.window, tt.metricType)
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
