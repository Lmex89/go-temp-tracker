package main

import (
	"bytes"
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
			oldLogger := logging.Logger
			var output bytes.Buffer
			logging.Logger = logging.New(&output, "INFO")
			t.Cleanup(func() { logging.Logger = oldLogger })

			if got := resolveDBDriver(tt.flag, tt.env, tt.flagSet); got != tt.want {
				t.Fatalf("resolveDBDriver() = %q, want %q", got, tt.want)
			}
			if got := strings.Contains(output.String(), "Unknown database driver"); got != tt.wantWarning {
				t.Errorf("warning logged = %t, want %t; output: %s", got, tt.wantWarning, output.String())
			}
		})
	}
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

func TestNewStoreUsesPostgresWithoutLoggingDSN(t *testing.T) {
	dsn := "postgres://user:secret@127.0.0.1:1/sensors?connect_timeout=1"
	t.Setenv("DATABASE_URL", dsn)
	oldLogger := logging.Logger
	var output bytes.Buffer
	logging.Logger = logging.New(&output, "INFO")
	t.Cleanup(func() { logging.Logger = oldLogger })

	driver := resolveDBDriver("postgres", "sqlite", true)
	st, ok := newStore(driver, "").(*store.PostgresStore)
	if !ok {
		t.Fatalf("newStore selected the wrong backend for %q", driver)
	}
	if db := st.InitDB(); db != nil {
		db.Close()
		t.Fatal("expected the isolated PostgreSQL connection to fail")
	}
	logs := output.String()
	if !strings.Contains(logs, "Using PostgreSQL database") || !strings.Contains(logs, "Failed to connect to PostgreSQL database") {
		t.Fatalf("PostgreSQL selection or safe connection error was not logged: %s", logs)
	}
	if strings.Contains(logs, "secret") || strings.Contains(logs, dsn) || strings.Contains(logs, "DATABASE_URL=") {
		t.Fatalf("database credentials or DSN were logged: %s", output.String())
	}
}
