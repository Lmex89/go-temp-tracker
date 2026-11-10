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
