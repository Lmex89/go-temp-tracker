package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"sensors-temp/internal/logging"
	"sensors-temp/internal/store"
	"sensors-temp/internal/timeutil"
	"time"

	"database/sql"

	"github.com/jackc/pgx/v5"
)

// sqliteCopySource streams rows from SQLite into pgx's CopyFrom protocol.
// It implements pgx.CopyFromSource so the migration never holds the entire
// table in memory. Like a Python generator/iterator that yields one row at a time.
type sqliteCopySource struct {
	rows *sql.Rows
	err  error
}

// Next advances to the next row. Returns false when done or on error.
func (s *sqliteCopySource) Next() bool {
	if s.err != nil {
		return false
	}
	next := s.rows.Next()
	if !next {
		s.err = s.rows.Err()
	}
	return next
}

// Values returns the current row as PostgreSQL-compatible values.
// SQLite stores created_at as a UTC string; we parse it to time.Time so
// Postgres inserts it as a proper TIMESTAMPTZ.
func (s *sqliteCopySource) Values() ([]interface{}, error) {
	var sensor, metricType, unit, createdAt string
	var tempC float64
	if err := s.rows.Scan(&sensor, &tempC, &metricType, &unit, &createdAt); err != nil {
		return nil, err
	}

	ts, err := time.Parse(timeutil.DBLayout, createdAt)
	if err != nil {
		return nil, fmt.Errorf("parse created_at %q: %w", createdAt, err)
	}

	return []interface{}{sensor, tempC, metricType, unit, ts.UTC()}, nil
}

// Err returns any iteration error encountered by Next().
func (s *sqliteCopySource) Err() error {
	return s.err
}

// logger writes to stderr (errors stay off stdout, as before); level from LOG_LEVEL.
var logger = logging.New(os.Stderr, os.Getenv("LOG_LEVEL"))

func main() {
	sqlitePath := flag.String("sqlite", "temps.db", "Source SQLite database file")
	postgresURL := flag.String("postgres", "", "Target PostgreSQL URL (defaults to DATABASE_URL, then docker-compose defaults)")
	flag.Parse()

	// Flag, then DATABASE_URL, then docker-compose default (shared with the service).
	*postgresURL = store.ResolvePostgresDSN(*postgresURL)

	fmt.Printf("Migrating %s -> %s\n", *sqlitePath, *postgresURL)

	// Open SQLite -- same driver the main app uses.
	sqliteDB, err := store.Open("sqlite", *sqlitePath)
	if err != nil {
		logger.Fatal("Failed to open SQLite: %v", err)
	}
	defer sqliteDB.Close()

	if err := sqliteDB.Ping(); err != nil {
		logger.Fatal("Failed to connect to SQLite: %v", err)
	}

	// Count source rows first so the user knows what to expect.
	var total int64
	if err := sqliteDB.QueryRow("SELECT COUNT(*) FROM readings").Scan(&total); err != nil {
		logger.Fatal("Failed to count SQLite rows: %v", err)
	}
	fmt.Printf("Source rows: %d\n", total)

	// Connect to Postgres using the native pgx API (not database/sql) so we can
	// use CopyFrom for high-throughput bulk loading.
	ctx := context.Background()
	pgConn, err := pgx.Connect(ctx, *postgresURL)
	if err != nil {
		logger.Fatal("Failed to connect to PostgreSQL: %v", err)
	}
	defer pgConn.Close(ctx)

	// Ensure the target schema exists.
	if _, err := pgConn.Exec(ctx, store.PostgresSchema); err != nil {
		logger.Fatal("Failed to create Postgres schema: %v", err)
	}

	// Truncate the target table so the migration is idempotent.
	// In a production move you would back up Postgres first; here we assume the
	// Postgres container is fresh or the user has already backed it up.
	if _, err := pgConn.Exec(ctx, "TRUNCATE TABLE readings RESTART IDENTITY"); err != nil {
		logger.Fatal("Failed to truncate Postgres readings: %v", err)
	}
	fmt.Println("Truncated target readings table")

	// Stream rows from SQLite. Order by id keeps the migration deterministic.
	rows, err := sqliteDB.Query("SELECT sensor, temp_c, metric_type, unit, created_at FROM readings ORDER BY id")
	if err != nil {
		logger.Fatal("Failed to query SQLite: %v", err)
	}
	defer rows.Close()

	src := &sqliteCopySource{rows: rows}

	start := time.Now()
	copied, err := pgConn.CopyFrom(ctx, pgx.Identifier{"readings"},
		[]string{"sensor", "temp_c", "metric_type", "unit", "created_at"}, src)
	if err != nil {
		logger.Fatal("Failed to copy rows to PostgreSQL: %v", err)
	}

	elapsed := time.Since(start)
	fmt.Printf("Copied %d rows in %v (%.0f rows/sec)\n", copied, elapsed, float64(copied)/elapsed.Seconds())

	// Verify counts match.
	var pgCount int64
	if err := pgConn.QueryRow(ctx, "SELECT COUNT(*) FROM readings").Scan(&pgCount); err != nil {
		logger.Fatal("Failed to count Postgres rows: %v", err)
	}
	fmt.Printf("Postgres row count: %d\n", pgCount)

	if pgCount != total {
		// The source SQLite database may still be receiving writes from a running
		// temp-tracker service. If so, the initial count and the copied count will
		// differ by the number of rows inserted during the migration. This is not
		// a corruption error; report it so the user can decide whether to re-run.
		fmt.Printf("Note: SQLite was modified during migration. SQLite count at start: %d, copied to Postgres: %d\n", total, pgCount)
		fmt.Println("Migration completed. If you want the very latest rows, stop temp-tracker and re-run the migration.")
		return
	}
	fmt.Println("Migration complete and counts match.")
}
