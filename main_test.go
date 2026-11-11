package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"sensors-temp/internal/logging"
	"sensors-temp/internal/store"
)

// TestGetEnvInt checks the env-var parsing contract: unset or invalid values
// fall back to the default (like int(os.getenv(k) or d) but without raising).
func TestGetEnvInt(t *testing.T) {
	tests := []struct {
		name string
		env  string
		want int
	}{
		{"unset uses default", "", 60},
		{"valid value", "15", 15},
		{"invalid falls back", "abc", 60},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// t.Setenv restores the variable after the test (like monkeypatch.setenv).
			t.Setenv("TEST_POLL_INTERVAL", tt.env)
			if got := getEnvInt("TEST_POLL_INTERVAL", 60); got != tt.want {
				t.Errorf("got %d, want %d", got, tt.want)
			}
		})
	}
}

func TestResolveDBDriver(t *testing.T) {
	tests := []struct {
		name        string
		flag        string
		env         string
		flagSet     bool
		want        string
		wantWarning bool
	}{
		{"explicit postgres overrides env", "postgres", "sqlite", true, "postgres", false},
		{"explicit sqlite overrides env", "sqlite", "postgres", true, "sqlite", false},
		{"explicit empty overrides env", "", "postgres", true, "sqlite", false},
		{"env selects postgres", "", "postgres", false, "postgres", false},
		{"empty defaults to sqlite", "", "", false, "sqlite", false},
		{"unknown env warns and defaults to sqlite", "", "mysql", false, "sqlite", true},
		{"unknown flag warns and defaults to sqlite", "mysql", "postgres", true, "sqlite", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			output := captureLogs(t)

			if got := resolveDBDriver(tt.flag, tt.env, tt.flagSet); got != tt.want {
				t.Fatalf("resolveDBDriver() = %q, want %q", got, tt.want)
			}
			if got := strings.Contains(output.String(), "Unknown database driver"); got != tt.wantWarning {
				t.Errorf("warning logged = %t, want %t; output: %s", got, tt.wantWarning, output.String())
			}
		})
	}
}

func TestMonitorExecutableUsesCLIAndEnvironmentDriverSelection(t *testing.T) {
	binary := filepath.Join(t.TempDir(), "temp-tracker")
	build := exec.Command("go", "build", "-o", binary, ".")
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build monitor: %v\n%s", err, output)
	}

	t.Run("unknown environment value warns and falls back to SQLite", func(t *testing.T) {
		workDir := t.TempDir()
		if err := os.Mkdir(filepath.Join(workDir, store.DefaultSQLitePath), 0o700); err != nil {
			t.Fatal(err)
		}
		dsn := "postgres://monitor:secret@db.example/sensors"
		output, err := runMonitorExecutable(binary, workDir, nil, map[string]string{
			"DB_DRIVER":    dsn,
			"DATABASE_URL": "postgres://monitor:secret@127.0.0.1:1/sensors?connect_timeout=1",
		})
		if err == nil {
			t.Fatal("expected initialization to fail on the temporary SQLite directory")
		}
		if !strings.Contains(output, "Unknown database driver") || !strings.Contains(output, "Using SQLite database") {
			t.Fatalf("expected warning and SQLite fallback:\n%s", output)
		}
		if strings.Contains(output, dsn) || strings.Contains(output, "secret") {
			t.Fatalf("untrusted driver value leaked:\n%s", output)
		}
	})

	t.Run("CLI option overrides environment", func(t *testing.T) {
		workDir := t.TempDir()
		if err := os.Mkdir(filepath.Join(workDir, store.DefaultSQLitePath), 0o700); err != nil {
			t.Fatal(err)
		}
		dsn := "postgres://monitor:secret@127.0.0.1:1/sensors?connect_timeout=1"
		output, err := runMonitorExecutable(binary, workDir, []string{"-db-driver=postgres"}, map[string]string{
			"DB_DRIVER":    "sqlite",
			"DATABASE_URL": dsn,
		})
		if err == nil {
			t.Fatal("expected the controlled PostgreSQL connection to fail")
		}
		if !strings.Contains(output, "Using PostgreSQL database") || !strings.Contains(output, "Failed to connect to PostgreSQL database") {
			t.Fatalf("CLI driver did not override the environment:\n%s", output)
		}
		if !strings.Contains(output, "connection refused") {
			t.Fatalf("connection cause not reported:\n%s", output)
		}
		if strings.Contains(output, dsn) || strings.Contains(output, "secret") {
			t.Fatalf("database credentials or DSN leaked:\n%s", output)
		}
	})

	t.Run("environment selects PostgreSQL", func(t *testing.T) {
		dsn := "postgres://monitor:secret@127.0.0.1:1/sensors?connect_timeout=1"
		output, err := runMonitorExecutable(binary, t.TempDir(), nil, map[string]string{
			"DB_DRIVER":    "postgres",
			"DATABASE_URL": dsn,
		})
		if err == nil {
			t.Fatal("expected the controlled PostgreSQL connection to fail")
		}
		if !strings.Contains(output, "Using PostgreSQL database") || strings.Contains(output, "secret") {
			t.Fatalf("expected safe PostgreSQL selection from DB_DRIVER:\n%s", output)
		}
	})

	t.Run("explicit empty option overrides environment and selects SQLite", func(t *testing.T) {
		workDir := t.TempDir()
		if err := os.Mkdir(filepath.Join(workDir, store.DefaultSQLitePath), 0o700); err != nil {
			t.Fatal(err)
		}
		output, err := runMonitorExecutable(binary, workDir, []string{"-db-driver="}, map[string]string{
			"DB_DRIVER":    "postgres",
			"DATABASE_URL": "postgres://monitor:secret@127.0.0.1:1/sensors?connect_timeout=1",
		})
		if err == nil {
			t.Fatal("expected initialization to fail on the temporary SQLite directory")
		}
		if !strings.Contains(output, "Using SQLite database") || strings.Contains(output, "Unknown database driver") {
			t.Fatalf("explicit empty option did not select SQLite silently:\n%s", output)
		}
	})
}

// captureLogs swaps the global logger for a buffer and restores it after the
// test -- like pytest's caplog fixture.
func captureLogs(t *testing.T) *bytes.Buffer {
	t.Helper()
	oldLogger := logging.Logger
	var output bytes.Buffer
	logging.Logger = logging.New(&output, "INFO")
	t.Cleanup(func() { logging.Logger = oldLogger })
	return &output
}

func runMonitorExecutable(binary, workDir string, args []string, overrides map[string]string) (string, error) {
	cmd := exec.Command(binary, args...)
	cmd.Dir = workDir
	cmd.Env = monitorTestEnv(overrides)
	output, err := cmd.CombinedOutput()
	return string(output), err
}

func monitorTestEnv(overrides map[string]string) []string {
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

func TestOpenDatabaseUsesEffectiveDriverAndSQLitePool(t *testing.T) {
	tests := []struct {
		name    string
		flag    string
		env     string
		flagSet bool
	}{
		{"empty driver", "", "", false},
		{"unknown driver", "", "mysql", false},
		{"explicit sqlite", "sqlite", "postgres", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "monitor.db")
			driver := resolveDBDriver(tt.flag, tt.env, tt.flagSet)
			st, db := openDatabase(driver, path)
			if db == nil {
				t.Fatal("openDatabase returned a nil database")
			}
			defer db.Close()
			if _, ok := st.(*store.SQLiteStore); !ok {
				t.Fatalf("openDatabase selected %T, want SQLiteStore", st)
			}
			if err := db.Ping(); err != nil {
				t.Fatalf("Ping: %v", err)
			}
			if got := db.Stats().MaxOpenConnections; got != 1 {
				t.Errorf("SQLite MaxOpenConnections = %d, want 1", got)
			}
		})
	}
}

func TestOpenDatabaseUsesPostgresWithoutLoggingDSN(t *testing.T) {
	dsn := "postgres://user:secret@127.0.0.1:1/sensors?connect_timeout=1"
	t.Setenv("DATABASE_URL", dsn)
	output := captureLogs(t)

	driver := resolveDBDriver("postgres", "sqlite", true)
	st, db := openDatabase(driver, "")
	if _, ok := st.(*store.PostgresStore); !ok {
		t.Fatalf("openDatabase selected %T for %q, want PostgresStore", st, driver)
	}
	if db != nil {
		db.Close()
		t.Fatal("expected the isolated PostgreSQL connection to fail")
	}
	logs := output.String()
	if !strings.Contains(logs, "Using PostgreSQL database") || !strings.Contains(logs, "Failed to connect to PostgreSQL database") {
		t.Fatalf("PostgreSQL selection or safe connection error was not logged: %s", logs)
	}
	if !strings.Contains(logs, "connection refused") {
		t.Fatalf("underlying PostgreSQL connection error was not logged: %s", logs)
	}
	if strings.Contains(logs, "secret") || strings.Contains(logs, dsn) || strings.Contains(logs, "DATABASE_URL=") {
		t.Fatalf("database credentials or DSN were logged: %s", output.String())
	}
}

func TestSQLiteInitializationLogsUsefulError(t *testing.T) {
	path := filepath.Join(t.TempDir(), "missing-dir", "readings.sqlite")
	output := captureLogs(t)

	if db := store.NewSQLiteStore(path).InitDB(); db != nil {
		db.Close()
		t.Fatal("expected SQLite initialization to fail for a missing parent directory")
	}
	logs := output.String()
	if !strings.Contains(logs, "Failed to connect to SQLite database") || !strings.Contains(logs, "unable to open database file") {
		t.Fatalf("expected useful SQLite connection error:\n%s", logs)
	}
	if !strings.Contains(logs, path) {
		t.Fatalf("SQLite path missing from the error, operator cannot tell which file failed:\n%s", logs)
	}
}
