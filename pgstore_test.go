package main

import (
	"database/sql"
	"fmt"
	"os"
	"sort"
	"strings"
	"testing"
	"time"
)

// pgTestDSN returns the DSN tests should use (DATABASE_URL or the
// docker-compose default), mirroring NewPostgresStore.
func pgTestDSN() string {
	if dsn := os.Getenv("DATABASE_URL"); dsn != "" {
		return dsn
	}
	return "postgres://tracker:tracker@localhost:5432/sensors_temp?sslmode=disable"
}

// newPGTestStore builds a PostgresStore bound to a throwaway schema, so tests
// never touch the real "readings" table. Python analogy: a pytest fixture that
// creates a schema and drops it in the finalizer. If Postgres is unreachable
// the test is skipped (like pytest.skip).
func newPGTestStore(t *testing.T) (*PostgresStore, *sql.DB) {
	t.Helper()
	base := pgTestDSN()

	admin, err := sql.Open("pgx", base)
	if err != nil {
		t.Skipf("postgres unavailable: %v", err)
	}
	if err := admin.Ping(); err != nil {
		admin.Close()
		t.Skipf("postgres unavailable: %v", err)
	}

	schema := fmt.Sprintf("qw_test_%d", time.Now().UnixNano())
	if _, err := admin.Exec("CREATE SCHEMA " + schema); err != nil {
		admin.Close()
		t.Skipf("cannot create test schema: %v", err)
	}
	// Only our own schema is dropped; user tables are never touched.
	t.Cleanup(func() {
		admin.Exec("DROP SCHEMA " + schema + " CASCADE")
		admin.Close()
	})

	// Force a non-UTC session timezone: absolute ranges must still be
	// interpreted as UTC regardless of the server/session setting.
	sep := "?"
	if strings.Contains(base, "?") {
		sep = "&"
	}
	dsn := base + sep + "search_path=" + schema + "&timezone=America/Merida"

	store := NewPostgresStore()
	store.dsn = dsn
	db := store.InitDB()
	if db == nil {
		t.Fatalf("InitDB returned nil for schema %s", schema)
	}
	t.Cleanup(func() { db.Close() })
	return store, db
}

// TestPostgresQueryWindow checks every TimeWindow x metricType combination.
func TestPostgresQueryWindow(t *testing.T) {
	store, db := newPGTestStore(t)

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
		if _, err := db.Exec(
			`INSERT INTO readings (sensor, temp_c, metric_type, unit, created_at) VALUES ($1, 1.0, $2, $3, $4)`,
			s.sensor, s.metric, s.unit, s.at,
		); err != nil {
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
		{"hours wide includes old", TimeWindow{Hours: 4}, "temperature", []string{"temp/a", "temp/old"}},
		{"hours unknown metric", TimeWindow{Hours: 1}, "disk", nil},
		{"range all metrics", rangeWin, "", []string{"cpu/r1", "temp/r1"}},
		{"range temperature", rangeWin, "temperature", []string{"temp/r1"}},
		{"range cpu", rangeWin, "cpu", []string{"cpu/r1"}},
		{"range unknown metric", rangeWin, "disk", nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var sensors []string
			for _, r := range store.QueryWindow(db, tt.window, tt.metricType) {
				sensors = append(sensors, r.Sensor)
			}
			sort.Strings(sensors)
			if strings.Join(sensors, ",") != strings.Join(tt.want, ",") {
				t.Fatalf("got %v, want %v", sensors, tt.want)
			}
		})
	}
}
