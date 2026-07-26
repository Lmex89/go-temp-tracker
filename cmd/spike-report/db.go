package main

import (
	"database/sql"
	"fmt"
	"os"
	"time"
)

// dbDriver holds the active backend for this run ("sqlite" or "postgres").
// It is set by main() from the -driver flag / DB_DRIVER env var.
// Package-level variables are like Python module globals -- use them sparingly.
var dbDriver string

// setDBDriver records the selected driver so query functions can emit the right
// SQL dialect. In Python you'd set a module-level constant.
func setDBDriver(driver string) {
	switch driver {
	case "postgres":
		dbDriver = "postgres"
	case "sqlite", "":
		dbDriver = "sqlite"
	default:
		logger.warn("unknown driver %q, falling back to sqlite", driver)
		dbDriver = "sqlite"
	}
}

// openReportDB opens either a SQLite file or a PostgreSQL connection.
// For SQLite, spec is a file path (default temps.db). For Postgres, spec is a
// connection string; if empty it falls back to DATABASE_URL, then to the
// project's docker-compose defaults.
func openReportDB(driver, spec string) (*sql.DB, error) {
	switch driver {
	case "postgres":
		dsn := spec
		if dsn == "" {
			dsn = os.Getenv("DATABASE_URL")
		}
		if dsn == "" {
			dsn = "postgres://tracker:tracker@localhost:5432/sensors_temp?sslmode=disable"
		}
		return sql.Open("pgx", dsn)
	default:
		path := spec
		if path == "" {
			path = "temps.db"
		}
		return sql.Open("sqlite", path)
	}
}

// formatDBTime formats a time.Time for database comparisons.
// Both SQLite and PostgreSQL accept "2006-01-02 15:04:05" as an unambiguous
// UTC literal when compared against TIMESTAMPTZ columns.
func formatDBTime(t time.Time) string {
	return t.Format("2006-01-02 15:04:05")
}

// parseDBTimestamp parses timestamps returned by either backend.
// SQLite returns "2006-01-02 15:04:05"; PostgreSQL, via to_char(...), returns
// the same layout. The function also accepts RFC3339 as a safety net.
func parseDBTimestamp(s string) (time.Time, error) {
	layouts := []string{
		"2006-01-02 15:04:05",
		"2006-01-02T15:04:05Z",
		time.RFC3339,
		time.RFC3339Nano,
	}
	for _, layout := range layouts {
		if t, err := time.Parse(layout, s); err == nil {
			return t.UTC(), nil
		}
	}
	return time.Time{}, fmt.Errorf("unsupported timestamp: %s", s)
}

// placeholder returns the dialect-specific parameter placeholder for 1-based index.
// SQLite uses ? for every parameter; PostgreSQL uses $1, $2, etc.
func placeholder(index int) string {
	if dbDriver == "postgres" {
		return fmt.Sprintf("$%d", index)
	}
	return "?"
}
