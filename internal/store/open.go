package store

import (
	"database/sql"
	"os"
)

// DefaultPostgresDSN matches the project's docker-compose.yml
// (user tracker, db sensors_temp, port 5432).
const DefaultPostgresDSN = "postgres://tracker:tracker@localhost:5432/sensors_temp?sslmode=disable"

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

// ResolveSQLitePath returns spec, or "temps.db" when spec is empty.
func ResolveSQLitePath(spec string) string {
	if spec == "" {
		return "temps.db"
	}
	return spec
}

// Open returns a raw *sql.DB for tools that run their own read-only SQL
// (spike-report, migrate-to-postgres). driver "postgres" (or "") uses the
// pgx driver and a DSN; any other value is treated as "sqlite" with a file
// path. Unlike Store.InitDB it creates no schema and runs no migration.
func Open(driver, spec string) (*sql.DB, error) {
	switch driver {
	case "postgres", "":
		return sql.Open("pgx", ResolvePostgresDSN(spec))
	default:
		return sql.Open("sqlite", ResolveSQLitePath(spec))
	}
}
