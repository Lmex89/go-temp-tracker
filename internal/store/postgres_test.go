package store

import (
	"database/sql"
	"fmt"
	"sort"
	"strings"
	"testing"
	"time"
)

// pgTestDSN returns the DSN tests should use (DATABASE_URL or the
// docker-compose default), mirroring NewPostgresStore.
func pgTestDSN() string {
	return ResolvePostgresDSN("")
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

// TestPostgresQuery checks every TimeWindow x metricType combination.
func TestPostgresQuery(t *testing.T) {
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
			for _, r := range mustQuery(t, store, db, tt.window, tt.metricType) {
				sensors = append(sensors, r.Sensor)
			}
			sort.Strings(sensors)
			if strings.Join(sensors, ",") != strings.Join(tt.want, ",") {
				t.Fatalf("got %v, want %v", sensors, tt.want)
			}
		})
	}
}

// TestPostgresQueryLatest checks QueryLatest returns the newest row per sensor
// for each metric type (the recursive-CTE loose index scan).
func TestPostgresQueryLatest(t *testing.T) {
	store, db := newPGTestStore(t)

	base := time.Now().UTC().Add(-time.Hour)
	// Insert order defines id order; the last row per sensor must win.
	seed := []struct {
		sensor, metric, unit string
		value                float64
	}{
		{"temp/a", "temperature", "C", 40},
		{"temp/b", "temperature", "C", 50},
		{"temp/a", "temperature", "C", 41},
		{"cpu/total", "cpu", "%", 10},
		{"cpu/total", "cpu", "%", 20},
		{"mem/used", "memory", "%", 70},
		{"load/1m", "load", "", 1.5},
		{"load/5m", "load", "", 2.5},
	}
	for i, s := range seed {
		if _, err := db.Exec(
			`INSERT INTO readings (sensor, temp_c, metric_type, unit, created_at) VALUES ($1, $2, $3, $4, $5)`,
			s.sensor, s.value, s.metric, s.unit, base.Add(time.Duration(i)*time.Minute),
		); err != nil {
			t.Fatalf("seed: %v", err)
		}
	}

	tests := []struct {
		name       string
		metricType string
		want       map[string]float64 // sensor -> latest value
	}{
		{"temperature", "temperature", map[string]float64{"temp/a": 41, "temp/b": 50}},
		{"cpu", "cpu", map[string]float64{"cpu/total": 20}},
		{"memory", "memory", map[string]float64{"mem/used": 70}},
		{"load", "load", map[string]float64{"load/1m": 1.5, "load/5m": 2.5}},
		{"unknown metric", "disk", map[string]float64{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := mustLatest(t, store, db, tt.metricType)
			if len(got) != len(tt.want) {
				t.Fatalf("got %d readings, want %d: %+v", len(got), len(tt.want), got)
			}
			for _, r := range got {
				if r.MetricType != tt.metricType {
					t.Errorf("sensor %s has metric %q, want %q", r.Sensor, r.MetricType, tt.metricType)
				}
				if v, ok := tt.want[r.Sensor]; !ok || r.TempC != v {
					t.Errorf("sensor %s value %v, want %v (present=%v)", r.Sensor, r.TempC, v, ok)
				}
			}
		})
	}
}

// TestPostgresPrune mirrors TestSQLitePrune: typed prune and empty = all types.
func TestPostgresPrune(t *testing.T) {
	tests := []struct {
		name       string
		metricType string
		want       []string
	}{
		{"one metric", "cpu", []string{"cpu/new", "temp/new", "temp/old"}},
		{"all metrics", "", []string{"cpu/new", "temp/new"}},
		{"unknown metric", "disk", []string{"cpu/new", "cpu/old", "temp/new", "temp/old"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store, db := newPGTestStore(t)
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
					`INSERT INTO readings (sensor, temp_c, metric_type, unit, created_at) VALUES ($1, 1, $2, 'x', $3)`,
					s.sensor, s.metric, s.at,
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

// TestPostgresInvalidInputs covers window validation and the required metric type.
func TestPostgresInvalidInputs(t *testing.T) {
	store, db := newPGTestStore(t)
	checkInvalidInputs(t, store, db)
}

func TestPostgresQueryFailures(t *testing.T) {
	for _, failure := range []string{"scan", "execution"} {
		t.Run(failure, func(t *testing.T) {
			st, db := newPGTestStore(t)
			if failure == "scan" {
				if _, err := db.Exec(`ALTER TABLE readings ALTER COLUMN temp_c TYPE text;
					INSERT INTO readings (sensor, temp_c, metric_type, unit, created_at) VALUES
					('a', '1', 'temperature', 'C', NOW() - INTERVAL '1 minute'),
					('b', 'invalid', 'temperature', 'C', NOW())`); err != nil {
					t.Fatal(err)
				}
			} else if _, err := db.Exec("DROP TABLE readings"); err != nil {
				t.Fatal(err)
			}
			for _, query := range []struct {
				name string
				run  func() ([]Reading, error)
			}{
				{"historical", func() ([]Reading, error) { return st.Query(db, TimeWindow{Hours: 1}, "temperature") }},
				{"latest", func() ([]Reading, error) { return st.QueryLatest(db, "temperature") }},
			} {
				t.Run(query.name, func(t *testing.T) {
					readings, err := query.run()
					if err == nil || readings != nil {
						t.Fatalf("got readings %v, error %v; want nil readings and error", readings, err)
					}
				})
			}
		})
	}
}
