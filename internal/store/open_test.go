package store

import (
	"path/filepath"
	"testing"
)

// TestOpen checks the raw-connection helper used by the cmd/ tools: an
// "sqlite" spec is a file path, and an empty spec falls back to temps.db
// (checked through ResolveSQLitePath so the test never opens the real file).
func TestOpen(t *testing.T) {
	if got := ResolveSQLitePath(""); got != "temps.db" {
		t.Errorf("ResolveSQLitePath(\"\") = %q, want temps.db", got)
	}
	path := filepath.Join(t.TempDir(), "open.db")
	db, err := Open("sqlite", path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer db.Close()
	if err := db.Ping(); err != nil {
		t.Fatalf("Ping: %v", err)
	}
}

// TestResolvePostgresDSN: explicit spec wins, then DATABASE_URL, then the
// docker-compose default. Python analogy: spec or os.getenv(...) or DEFAULT.
func TestResolvePostgresDSN(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://env")
	if got := ResolvePostgresDSN("postgres://flag"); got != "postgres://flag" {
		t.Errorf("explicit spec: got %q", got)
	}
	if got := ResolvePostgresDSN(""); got != "postgres://env" {
		t.Errorf("env: got %q", got)
	}
	t.Setenv("DATABASE_URL", "")
	if got := ResolvePostgresDSN(""); got != DefaultPostgresDSN {
		t.Errorf("default: got %q", got)
	}
}

// TestOpenPostgresIsDeferredAndUnknownIsRejected: opening PostgreSQL only
// builds a pool (no connection until Ping, like a lazy SQLAlchemy engine), and
// an unknown driver is an error instead of a silent SQLite file.
func TestOpenPostgresIsDeferredAndUnknownIsRejected(t *testing.T) {
	db, err := Open(DriverPostgres, "postgres://user:secret@127.0.0.1:1/x?connect_timeout=1")
	if err != nil {
		t.Fatalf("Open postgres should defer connecting: %v", err)
	}
	defer db.Close()
	if err := db.Ping(); err == nil {
		t.Fatal("expected Ping to fail against an unreachable server")
	}
	if _, err := Open("mysql", filepath.Join(t.TempDir(), "x.db")); err == nil {
		t.Fatal("expected an unknown driver to be rejected")
	}
}

// TestResolveDriver: canonical names pass through, empty uses the binary's
// fallback, unknown uses it too but reports known=false so callers warn.
func TestResolveDriver(t *testing.T) {
	tests := []struct {
		requested, fallback, want string
		known                     bool
	}{
		{"sqlite", DriverPostgres, DriverSQLite, true},
		{"postgres", DriverSQLite, DriverPostgres, true},
		{"", DriverSQLite, DriverSQLite, true},
		{"", DriverPostgres, DriverPostgres, true},
		{"mysql", DriverSQLite, DriverSQLite, false},
		{"mysql", DriverPostgres, DriverPostgres, false},
	}
	for _, tt := range tests {
		got, known := ResolveDriver(tt.requested, tt.fallback)
		if got != tt.want || known != tt.known {
			t.Errorf("ResolveDriver(%q, %q) = (%q, %t), want (%q, %t)", tt.requested, tt.fallback, got, known, tt.want, tt.known)
		}
	}
}
