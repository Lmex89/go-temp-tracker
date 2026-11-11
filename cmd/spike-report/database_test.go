package main

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"sensors-temp/internal/store"
)

type reportFixture struct {
	sensor, metricType, unit string
	value                    float64
	at                       time.Time
}

func TestSpikeReportDatabaseSelection(t *testing.T) {
	binary := buildSpikeReport(t)
	dbPath := filepath.Join(t.TempDir(), "readings.sqlite")
	fixtures := spikeReportFixtures(time.Now().UTC().Truncate(time.Second))
	seedSQLiteReportDB(t, dbPath, fixtures)

	tests := []struct {
		name string
		args []string
		env  map[string]string
	}{
		{
			name: "explicit SQLite overrides environment",
			args: []string{"-driver=sqlite", "-db", dbPath, "-days=1", "-baseline-days=30", "-format=json"},
			env:  map[string]string{"DB_DRIVER": "postgres"},
		},
		{
			name: "environment selects SQLite",
			args: []string{"-db", dbPath, "-days=1", "-baseline-days=30", "-format=json"},
			env:  map[string]string{"DB_DRIVER": "sqlite"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			stdout, stderr, err := runSpikeReport(binary, tt.args, tt.env)
			if err != nil {
				t.Fatalf("spike-report failed: %v\n%s", err, stderr)
			}
			if !strings.Contains(stderr, "driver=sqlite") {
				t.Fatalf("effective driver not reported:\n%s", stderr)
			}
			if !strings.Contains(stderr, "database connection verified") {
				t.Fatalf("connection verification not reported:\n%s", stderr)
			}
			assertReportFixture(t, stdout)
		})
	}

	t.Run("does not initialize an empty SQLite database", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "empty.sqlite")
		_, stderr, err := runSpikeReport(binary, []string{"-driver=sqlite", "-db", path}, nil)
		if err == nil {
			t.Fatal("expected the report query to fail because readings is absent")
		}
		db, err := store.Open("sqlite", path)
		if err != nil {
			t.Fatalf("open temporary SQLite database: %v", err)
		}
		defer db.Close()
		var tableCount int
		if err := db.QueryRow("SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = 'readings'").Scan(&tableCount); err != nil {
			t.Fatalf("check report schema: %v", err)
		}
		if tableCount != 0 || !strings.Contains(stderr, "spike detection failed") {
			t.Fatalf("report initialized its database or failed unexpectedly (tables=%d):\n%s", tableCount, stderr)
		}
	})

	t.Run("empty default selects PostgreSQL", func(t *testing.T) {
		dsn := "postgres://report:secret@127.0.0.1:1/report"
		_, stderr, err := runSpikeReport(binary, []string{"-db", dsn}, nil)
		if err == nil {
			t.Fatal("expected the unreachable PostgreSQL connection to fail")
		}
		assertPostgresFailureIsSafe(t, stderr)
	})

	t.Run("explicit PostgreSQL overrides environment", func(t *testing.T) {
		dsn := "postgres://report:secret@127.0.0.1:1/report"
		_, stderr, err := runSpikeReport(binary, []string{"-driver=postgres", "-db", dsn}, map[string]string{"DB_DRIVER": "sqlite"})
		if err == nil {
			t.Fatal("expected the unreachable PostgreSQL connection to fail")
		}
		assertPostgresFailureIsSafe(t, stderr)
	})

	t.Run("explicit empty driver overrides environment without creating SQLite", func(t *testing.T) {
		unexpectedSQLite := filepath.Join(t.TempDir(), "must-not-exist.sqlite")
		_, stderr, err := runSpikeReport(binary, []string{"-driver=", "-db", unexpectedSQLite}, map[string]string{"DB_DRIVER": "sqlite"})
		if err == nil {
			t.Fatal("expected the unreachable PostgreSQL connection to fail")
		}
		if !strings.Contains(stderr, "driver=postgres") || (!strings.Contains(stderr, "failed to open postgres database") && !strings.Contains(stderr, "failed to connect to postgres database")) {
			t.Fatalf("expected the empty option to select PostgreSQL:\n%s", stderr)
		}
		if _, statErr := os.Stat(unexpectedSQLite); !os.IsNotExist(statErr) {
			t.Fatalf("unexpected SQLite database was created: %v", statErr)
		}
	})

	t.Run("unknown driver warns and uses PostgreSQL without creating SQLite", func(t *testing.T) {
		unexpectedSQLite := filepath.Join(t.TempDir(), "must-not-exist.sqlite")
		_, stderr, err := runSpikeReport(binary, []string{"-driver=unknown", "-db", unexpectedSQLite}, nil)
		if err == nil {
			t.Fatal("expected the PostgreSQL connection to fail")
		}
		if !strings.Contains(stderr, "unknown database driver; falling back to postgres") {
			t.Fatalf("missing unknown-driver warning:\n%s", stderr)
		}
		if !strings.Contains(stderr, "driver=postgres") {
			t.Fatalf("effective PostgreSQL driver not reported:\n%s", stderr)
		}
		if _, statErr := os.Stat(unexpectedSQLite); !os.IsNotExist(statErr) {
			t.Fatalf("unexpected SQLite database was created: %v", statErr)
		}
	})

	t.Run("environment selects PostgreSQL", func(t *testing.T) {
		dsn := "postgres://report:secret@127.0.0.1:1/report"
		_, stderr, err := runSpikeReport(binary, nil, map[string]string{"DB_DRIVER": "postgres", "DATABASE_URL": dsn})
		if err == nil {
			t.Fatal("expected the unreachable PostgreSQL connection to fail")
		}
		assertPostgresFailureIsSafe(t, stderr)
	})

	t.Run("unknown environment value warns and uses PostgreSQL without creating SQLite", func(t *testing.T) {
		unexpectedSQLite := filepath.Join(t.TempDir(), "must-not-exist.sqlite")
		_, stderr, err := runSpikeReport(binary, []string{"-db", unexpectedSQLite}, map[string]string{"DB_DRIVER": "mysql"})
		if err == nil {
			t.Fatal("expected the PostgreSQL connection to fail")
		}
		if !strings.Contains(stderr, "unknown database driver; falling back to postgres") || !strings.Contains(stderr, "driver=postgres") {
			t.Fatalf("expected warning and PostgreSQL fallback:\n%s", stderr)
		}
		if _, statErr := os.Stat(unexpectedSQLite); !os.IsNotExist(statErr) {
			t.Fatalf("unexpected SQLite database was created: %v", statErr)
		}
	})
}

func TestSpikeReportPostgresQueries(t *testing.T) {
	dsn := os.Getenv("SPIKE_REPORT_TEST_POSTGRES_URL")
	if dsn == "" {
		t.Skip("PostgreSQL integration omitted: SPIKE_REPORT_TEST_POSTGRES_URL is not set")
	}
	parsed, err := url.Parse(dsn)
	if err != nil || (parsed.Scheme != "postgres" && parsed.Scheme != "postgresql") {
		t.Skip("PostgreSQL integration omitted: test URL must use postgres:// or postgresql://")
	}

	admin, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Skip("PostgreSQL integration omitted: PostgreSQL is unavailable")
	}
	if err := admin.Ping(); err != nil {
		if closeErr := admin.Close(); closeErr != nil {
			t.Errorf("close PostgreSQL admin connection: %v", closeErr)
		}
		t.Skip("PostgreSQL integration omitted: PostgreSQL is unavailable")
	}
	schema := fmt.Sprintf("spike_report_test_%d", time.Now().UnixNano())
	if _, err := admin.Exec("CREATE SCHEMA " + schema); err != nil {
		if closeErr := admin.Close(); closeErr != nil {
			t.Errorf("close PostgreSQL admin connection: %v", closeErr)
		}
		t.Skip("PostgreSQL integration omitted: cannot create an isolated schema")
	}
	t.Cleanup(func() {
		if _, err := admin.Exec("DROP SCHEMA " + schema + " CASCADE"); err != nil {
			t.Errorf("drop isolated PostgreSQL schema %s: %v", schema, err)
		}
		if err := admin.Close(); err != nil {
			t.Errorf("close PostgreSQL admin connection: %v", err)
		}
	})

	query := parsed.Query()
	query.Set("search_path", schema)
	query.Set("timezone", "UTC")
	parsed.RawQuery = query.Encode()
	isolatedDSN := parsed.String()
	db, err := store.Open("postgres", isolatedDSN)
	if err != nil {
		t.Fatal("could not open isolated PostgreSQL schema")
	}
	defer db.Close()
	if _, err := db.Exec(`CREATE TABLE readings (
		id BIGSERIAL PRIMARY KEY,
		sensor TEXT NOT NULL,
		temp_c DOUBLE PRECISION NOT NULL,
		metric_type TEXT NOT NULL,
		unit TEXT NOT NULL,
		created_at TIMESTAMPTZ NOT NULL
	)`); err != nil {
		t.Fatal("could not create isolated readings table")
	}
	fixtures := spikeReportFixtures(time.Now().UTC().Truncate(time.Second))
	for _, row := range fixtures {
		if _, err := db.Exec(`INSERT INTO readings (sensor, temp_c, metric_type, unit, created_at)
			VALUES ($1, $2, $3, $4, $5)`, row.sensor, row.value, row.metricType, row.unit, row.at); err != nil {
			t.Fatal("could not seed isolated PostgreSQL schema")
		}
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close isolated PostgreSQL test connection: %v", err)
	}

	binary := buildSpikeReport(t)
	stdout, stderr, err := runSpikeReport(binary, []string{"-days=1", "-baseline-days=30", "-format=json"}, map[string]string{
		"DB_DRIVER":    "postgres",
		"DATABASE_URL": isolatedDSN,
	})
	if err != nil {
		t.Fatalf("spike-report failed: %v\n%s", err, stderr)
	}
	if !strings.Contains(stderr, "driver=postgres") {
		t.Fatalf("effective PostgreSQL driver not reported:\n%s", stderr)
	}
	assertReportFixture(t, stdout)
}

func buildSpikeReport(t *testing.T) string {
	t.Helper()
	binary := filepath.Join(t.TempDir(), "spike-report")
	cmd := exec.Command("go", "build", "-o", binary, ".")
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build spike-report: %v\n%s", err, output)
	}
	return binary
}

func runSpikeReport(binary string, args []string, overrides map[string]string) (string, string, error) {
	cmd := exec.Command(binary, args...)
	cmd.Env = reportTestEnv(overrides)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	return stdout.String(), stderr.String(), err
}

func reportTestEnv(overrides map[string]string) []string {
	var env []string
	for _, entry := range os.Environ() {
		key, _, _ := strings.Cut(entry, "=")
		if key == "DB_DRIVER" || key == "DATABASE_URL" || key == "LOG_LEVEL" {
			continue
		}
		env = append(env, entry)
	}
	for key, value := range overrides {
		env = append(env, key+"="+value)
	}
	return env
}

func spikeReportFixtures(now time.Time) []reportFixture {
	spikeAt := now.Add(-30 * time.Second)
	return []reportFixture{
		{sensor: "temp/core", metricType: "temperature", unit: "C", value: 20, at: now.Add(-72 * time.Hour)},
		{sensor: "temp/core", metricType: "temperature", unit: "C", value: 20, at: now.Add(-48 * time.Hour)},
		{sensor: "temp/core", metricType: "temperature", unit: "C", value: 80, at: spikeAt},
		{sensor: "cpu/total", metricType: "cpu", unit: "%", value: 25, at: spikeAt},
		{sensor: "memory/used_percent", metricType: "memory", unit: "%", value: 40, at: spikeAt},
		{sensor: "swap/used_percent", metricType: "swap", unit: "%", value: 10, at: spikeAt},
		{sensor: "disk//", metricType: "disk", unit: "%", value: 30, at: spikeAt},
		{sensor: "load/1min", metricType: "load", value: 1.2, at: spikeAt},
		{sensor: "load/5min", metricType: "load", value: 1.1, at: spikeAt},
		{sensor: "load/15min", metricType: "load", value: 1.0, at: spikeAt},
	}
}

func seedSQLiteReportDB(t *testing.T, path string, fixtures []reportFixture) {
	t.Helper()
	db, err := store.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open temporary SQLite database: %v", err)
	}
	defer db.Close()
	if _, err := db.Exec(`CREATE TABLE readings (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		sensor TEXT NOT NULL,
		temp_c REAL NOT NULL,
		metric_type TEXT NOT NULL,
		unit TEXT NOT NULL,
		created_at TEXT NOT NULL
	)`); err != nil {
		t.Fatalf("create temporary readings table: %v", err)
	}
	for _, row := range fixtures {
		if _, err := db.Exec(`INSERT INTO readings (sensor, temp_c, metric_type, unit, created_at)
			VALUES (?, ?, ?, ?, ?)`, row.sensor, row.value, row.metricType, row.unit, row.at.Format("2006-01-02 15:04:05")); err != nil {
			t.Fatalf("seed temporary SQLite database: %v", err)
		}
	}
}

func assertReportFixture(t *testing.T, output string) {
	t.Helper()
	var report Report
	if err := json.Unmarshal([]byte(output), &report); err != nil {
		t.Fatalf("decode report JSON: %v\n%s", err, output)
	}
	if report.SpikeCount != 1 || len(report.Rows) != 1 {
		t.Fatalf("got %d spikes and %d rows, want one spike and row: %+v", report.SpikeCount, len(report.Rows), report)
	}
	row := report.Rows[0]
	if row.Sensor != "temp/core" || row.BaselineMean != 40 || row.TempC != 80 || row.CPUPercent != 25 || row.MemPercent != 40 || row.SwapPercent != 10 || row.DiskPercent != 30 || row.Load1Min != 1.2 || row.Load5Min != 1.1 || row.Load15Min != 1 {
		t.Fatalf("unexpected spike or correlated metrics: %+v", row)
	}
}

func assertPostgresFailureIsSafe(t *testing.T, stderr string) {
	t.Helper()
	if !strings.Contains(stderr, "driver=postgres") || !strings.Contains(stderr, "failed to connect") {
		t.Fatalf("expected a PostgreSQL connection failure:\n%s", stderr)
	}
	if !strings.Contains(stderr, "connection not yet verified") {
		t.Fatalf("deferred database opening was not distinguished from connection verification:\n%s", stderr)
	}
	if strings.Contains(stderr, "postgres://") || strings.Contains(stderr, "secret") {
		t.Fatalf("database connection details leaked:\n%s", stderr)
	}
}
