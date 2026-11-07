package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"time"

	"database/sql"

	"github.com/jackc/pgx/v5"
	_ "modernc.org/sqlite"
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

	ts, err := time.Parse("2006-01-02 15:04:05", createdAt)
	if err != nil {
		return nil, fmt.Errorf("parse created_at %q: %w", createdAt, err)
	}

	return []interface{}{sensor, tempC, metricType, unit, ts.UTC()}, nil
}

// Err returns any iteration error encountered by Next().
func (s *sqliteCopySource) Err() error {
	return s.err
}

func main() {
	sqlitePath := flag.String("sqlite", "temps.db", "Source SQLite database file")
	postgresURL := flag.String("postgres", "", "Target PostgreSQL URL (defaults to DATABASE_URL, then docker-compose defaults)")
	flag.Parse()

	if *postgresURL == "" {
		*postgresURL = os.Getenv("DATABASE_URL")
		if *postgresURL == "" {
			*postgresURL = "postgres://tracker:tracker@localhost:5432/sensors_temp?sslmode=disable"
		}
	}

	fmt.Printf("Migrating %s -> %s\n", *sqlitePath, *postgresURL)

	// Open SQLite -- same driver the main app uses.
	sqliteDB, err := sql.Open("sqlite", *sqlitePath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to open SQLite: %v\n", err)
		os.Exit(1)
	}
	defer sqliteDB.Close()

	if err := sqliteDB.Ping(); err != nil {
		fmt.Fprintf(os.Stderr, "Failed to connect to SQLite: %v\n", err)
		os.Exit(1)
	}

	// Count source rows first so the user knows what to expect.
	var total int64
	if err := sqliteDB.QueryRow("SELECT COUNT(*) FROM readings").Scan(&total); err != nil {
		fmt.Fprintf(os.Stderr, "Failed to count SQLite rows: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("Source rows: %d\n", total)

	// Connect to Postgres using the native pgx API (not database/sql) so we can
	// use CopyFrom for high-throughput bulk loading.
	ctx := context.Background()
	pgConn, err := pgx.Connect(ctx, *postgresURL)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to connect to PostgreSQL: %v\n", err)
		os.Exit(1)
	}
	defer pgConn.Close(ctx)

	// Ensure the target schema exists.
	if _, err := pgConn.Exec(ctx, `CREATE TABLE IF NOT EXISTS readings (
		id BIGSERIAL PRIMARY KEY,
		sensor TEXT NOT NULL,
		temp_c DOUBLE PRECISION NOT NULL,
		metric_type TEXT NOT NULL DEFAULT 'temperature',
		unit TEXT NOT NULL DEFAULT 'C',
		created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
	);
	CREATE INDEX IF NOT EXISTS idx_readings_created_at ON readings(created_at);
	CREATE INDEX IF NOT EXISTS idx_readings_sensor ON readings(sensor);
	CREATE INDEX IF NOT EXISTS idx_readings_metric_type_created_at ON readings(metric_type, created_at);
	CREATE INDEX IF NOT EXISTS idx_readings_metric_type_sensor_id ON readings(metric_type, sensor, id);
	CREATE INDEX IF NOT EXISTS idx_readings_disk_sensor_id ON readings(sensor, id DESC) WHERE metric_type = 'disk';
	CREATE INDEX IF NOT EXISTS idx_readings_temp_sensor_id ON readings(sensor, id DESC) WHERE metric_type = 'temperature';
	CREATE INDEX IF NOT EXISTS idx_readings_cpu_sensor_id ON readings(sensor, id DESC) WHERE metric_type = 'cpu';
	CREATE INDEX IF NOT EXISTS idx_readings_memory_sensor_id ON readings(sensor, id DESC) WHERE metric_type = 'memory';
	CREATE INDEX IF NOT EXISTS idx_readings_swap_sensor_id ON readings(sensor, id DESC) WHERE metric_type = 'swap';
	CREATE INDEX IF NOT EXISTS idx_readings_load_sensor_id ON readings(sensor, id DESC) WHERE metric_type = 'load';
	CREATE INDEX IF NOT EXISTS idx_readings_load_created_at ON readings(created_at) WHERE metric_type = 'load';
	CREATE INDEX IF NOT EXISTS idx_readings_temp_created_at ON readings(created_at) WHERE metric_type = 'temperature';
	CREATE INDEX IF NOT EXISTS idx_readings_cpu_created_at ON readings(created_at) WHERE metric_type = 'cpu';
	CREATE INDEX IF NOT EXISTS idx_readings_memory_created_at ON readings(created_at) WHERE metric_type = 'memory';
	CREATE INDEX IF NOT EXISTS idx_readings_swap_created_at ON readings(created_at) WHERE metric_type = 'swap';
	CREATE INDEX IF NOT EXISTS idx_readings_disk_created_at ON readings(created_at) WHERE metric_type = 'disk';`); err != nil {
		fmt.Fprintf(os.Stderr, "Failed to create Postgres schema: %v\n", err)
		os.Exit(1)
	}

	// Truncate the target table so the migration is idempotent.
	// In a production move you would back up Postgres first; here we assume the
	// Postgres container is fresh or the user has already backed it up.
	if _, err := pgConn.Exec(ctx, "TRUNCATE TABLE readings RESTART IDENTITY"); err != nil {
		fmt.Fprintf(os.Stderr, "Failed to truncate Postgres readings: %v\n", err)
		os.Exit(1)
	}
	fmt.Println("Truncated target readings table")

	// Stream rows from SQLite. Order by id keeps the migration deterministic.
	rows, err := sqliteDB.Query("SELECT sensor, temp_c, metric_type, unit, created_at FROM readings ORDER BY id")
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to query SQLite: %v\n", err)
		os.Exit(1)
	}
	defer rows.Close()

	src := &sqliteCopySource{rows: rows}

	start := time.Now()
	copied, err := pgConn.CopyFrom(ctx, pgx.Identifier{"readings"},
		[]string{"sensor", "temp_c", "metric_type", "unit", "created_at"}, src)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to copy rows to PostgreSQL: %v\n", err)
		os.Exit(1)
	}

	elapsed := time.Since(start)
	fmt.Printf("Copied %d rows in %v (%.0f rows/sec)\n", copied, elapsed, float64(copied)/elapsed.Seconds())

	// Verify counts match.
	var pgCount int64
	if err := pgConn.QueryRow(ctx, "SELECT COUNT(*) FROM readings").Scan(&pgCount); err != nil {
		fmt.Fprintf(os.Stderr, "Failed to count Postgres rows: %v\n", err)
		os.Exit(1)
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
