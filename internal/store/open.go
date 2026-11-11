package store

import (
	"database/sql"
	"fmt"
	"os"
)

// Canonical database driver names. Each binary resolves the requested value
// once with ResolveDriver and passes one of these everywhere after that.
// Python analogy: module-level constants instead of repeating string literals.
const (
	DriverSQLite   = "sqlite"
	DriverPostgres = "postgres"
)

// ResolveDriver maps a requested driver to a canonical one. An empty or unknown
// value returns fallback (each binary keeps its historical default: SQLite for
// the monitor, PostgreSQL for spike-report); known is false only for an unknown
// non-empty value so the caller can warn. The value itself is never echoed,
// because operators sometimes paste a DSN into DB_DRIVER by mistake.
// Python analogy: {"sqlite": "sqlite", "postgres": "postgres"}.get(v, fallback).
func ResolveDriver(requested, fallback string) (driver string, known bool) {
	switch requested {
	case DriverSQLite, DriverPostgres:
		return requested, true
	case "":
		return fallback, true
	default:
		return fallback, false
	}
}

// DefaultPostgresDSN matches the project's docker-compose.yml
// (user tracker, db sensors_temp, port 5432).
const DefaultPostgresDSN = "postgres://tracker:tracker@localhost:5432/sensors_temp?sslmode=disable"

// DefaultSQLitePath is the SQLite file used when no path is given.
// Python analogy: a module-level DEFAULT_DB = "temps.db".
const DefaultSQLitePath = "temps.db"

// ResolvePostgresDSN picks the connection string: explicit spec first, then
// DATABASE_URL, then DefaultPostgresDSN.
// Python analogy: spec or os.getenv("DATABASE_URL") or DEFAULT.
func ResolvePostgresDSN(spec string) string {
	if spec != "" {
		return spec
	}
	if dsn := os.Getenv("DATABASE_URL"); dsn != "" {
		return dsn
	}
	return DefaultPostgresDSN
}

// ResolveSQLitePath returns spec, or DefaultSQLitePath when spec is empty.
func ResolveSQLitePath(spec string) string {
	if spec == "" {
		return DefaultSQLitePath
	}
	return spec
}

// Open returns a raw *sql.DB for tools that run their own read-only SQL
// (spike-report, migrate-to-postgres). driver must already be canonical
// (see ResolveDriver): "postgres" uses pgx and a DSN, "sqlite" a file path.
// Any other value is an error instead of a silent SQLite fallback, so the
// opened engine can never diverge from the caller's SQL dialect.
// Unlike Store.InitDB it creates no schema and runs no migration.
func Open(driver, spec string) (*sql.DB, error) {
	switch driver {
	case DriverPostgres:
		return sql.Open("pgx", ResolvePostgresDSN(spec))
	case DriverSQLite:
		return sql.Open("sqlite", ResolveSQLitePath(spec))
	default:
		return nil, fmt.Errorf("unknown database driver")
	}
}
