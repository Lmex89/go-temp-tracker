package main

import (
	"fmt"
	"regexp"
	"sensors-temp/internal/timeutil"
	"time"
)

// SQL dialect helpers for the report queries. Opening the database lives in
// internal/store (store.Open); only the placeholder/time formatting differences
// between SQLite and PostgreSQL stay here, because the report writes its own SQL.

// dbDriver holds the active backend for this run ("sqlite" or "postgres").
// It is set by main() from the -driver flag / DB_DRIVER env var.
// Package-level variables are like Python module globals -- use them sparingly.
var dbDriver string

var (
	postgresURLPattern        = regexp.MustCompile(`(?i)postgres(?:ql)?://[^\s"'<>]+`)
	postgresCredentialPattern = regexp.MustCompile(`(?i)\b(user|password|passfile)\s*=\s*('[^']*'|"[^"]*"|[^\s]+)`)
	postgresUserErrorPattern  = regexp.MustCompile(`(?i)(\bfor user\s+)("[^"]*"|'[^']*'|[^\s]+)`)
)

// NOTE: an empty driver means postgres in spike-report (original behavior), unlike
// the service where "" means sqlite; this matches store.Open.
// setDBDriver records the selected driver so query functions can emit the right
// SQL dialect and returns the same driver for database opening. In Python you'd
// set a module-level constant.
func setDBDriver(driver string) string {
	switch driver {
	case "postgres", "":
		dbDriver = "postgres"
	case "sqlite":
		dbDriver = "sqlite"
	default:
		logger.Warn("unknown database driver; falling back to postgres")
		dbDriver = "postgres"
	}
	return dbDriver
}

// Driver errors may include the connection details, so redact them but keep the cause.
func safeDatabaseError(err error) string {
	message := postgresURLPattern.ReplaceAllString(err.Error(), "PostgreSQL connection details redacted")
	message = postgresCredentialPattern.ReplaceAllString(message, "$1=<redacted>")
	return postgresUserErrorPattern.ReplaceAllString(message, "$1<redacted>")
}

// formatDBTime formats a time.Time for database comparisons.
// Both SQLite and PostgreSQL accept "2006-01-02 15:04:05" as an unambiguous
// UTC literal when compared against TIMESTAMPTZ columns.
func formatDBTime(t time.Time) string {
	return t.Format(timeutil.DBLayout)
}

// parseDBTimestamp parses timestamps returned by either backend.
// SQLite returns "2006-01-02 15:04:05"; PostgreSQL, via to_char(...), returns
// the same layout. The function also accepts RFC3339 as a safety net.
func parseDBTimestamp(s string) (time.Time, error) {
	layouts := []string{
		timeutil.DBLayout,
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
