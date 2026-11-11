package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"sensors-temp/internal/store"
)

// TestMigrationOpensBothEnginesExplicitly runs the real binary against a
// temporary SQLite file and an unreachable PostgreSQL server: it must open
// and count SQLite, then fail on the PostgreSQL connect before any TRUNCATE or
// COPY. Like a pytest subprocess test with a fake target host.
func TestMigrationOpensBothEnginesExplicitly(t *testing.T) {
	dir := t.TempDir()
	binary := filepath.Join(dir, "migrate-to-postgres")
	if out, err := exec.Command("go", "build", "-o", binary, ".").CombinedOutput(); err != nil {
		t.Fatalf("build migrate-to-postgres: %v\n%s", err, out)
	}

	sqlitePath := filepath.Join(dir, "source.sqlite")
	db, err := store.Open(store.DriverSQLite, sqlitePath)
	if err != nil {
		t.Fatalf("open temporary SQLite: %v", err)
	}
	if _, err := db.Exec(`CREATE TABLE readings (id INTEGER PRIMARY KEY, sensor TEXT, temp_c REAL, metric_type TEXT, unit TEXT, created_at TEXT);
		INSERT INTO readings (sensor, temp_c, metric_type, unit, created_at) VALUES ('temp/core', 40, 'temperature', 'C', '2026-01-01 00:00:00')`); err != nil {
		t.Fatalf("seed temporary SQLite: %v", err)
	}
	db.Close()

	cmd := exec.Command(binary, "-sqlite", sqlitePath, "-postgres", "postgres://u:p@127.0.0.1:1/x?connect_timeout=1")
	cmd.Env = append(os.Environ(), "DATABASE_URL=")
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("expected the unreachable PostgreSQL target to fail:\n%s", out)
	}
	output := string(out)
	if !strings.Contains(output, "Source rows: 1") || !strings.Contains(output, "Failed to connect to PostgreSQL") {
		t.Fatalf("expected SQLite to open and PostgreSQL connect to fail:\n%s", output)
	}
	if strings.Contains(output, "truncate") || strings.Contains(output, "Copied") {
		t.Fatalf("migration ran past the failed connection:\n%s", output)
	}
}
