package main

import (
	"sensors-temp/internal/timeutil"

	"database/sql"
	"fmt"
	"os"
	"sensors-temp/internal/logging"
	"strings"

	// Register the pgx driver with database/sql.
	// The blank import is like Python's `importlib.import_module` for side effects:
	// it registers the "pgx" driver name so sql.Open("pgx", ...) works.
	_ "github.com/jackc/pgx/v5/stdlib"
)

// PostgresStore implements the Store interface using PostgreSQL.
// It holds a TimeConverter for converting UTC timestamps to local time,
// just like SQLiteStore.
type PostgresStore struct {
	converter timeutil.TimeConverter
	dsn       string // connection string, e.g. postgres://user:pass@host/db?sslmode=disable
}

// NewPostgresStore creates a PostgresStore.
// It reads DATABASE_URL from the environment, falling back to the defaults used
// by the project's docker-compose.yml (user tracker, db sensors_temp, port 5432).
// In Python this is like:
//
//	os.getenv("DATABASE_URL", "postgres://tracker:tracker@localhost:5432/sensors_temp?sslmode=disable")
func NewPostgresStore() *PostgresStore {
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		dsn = "postgres://tracker:tracker@localhost:5432/sensors_temp?sslmode=disable"
	}
	return &PostgresStore{
		converter: timeutil.NewMeridaTimeConverter(),
		dsn:       dsn,
	}
}

// InitDB opens a connection pool to PostgreSQL and ensures the readings table exists.
// Unlike SQLite, PostgreSQL is a real server: it handles concurrent writers itself,
// so we do NOT need PRAGMA journal_mode or busy_timeout, and we do NOT force
// SetMaxOpenConns(1). The connection pool is left at sensible defaults.
func (s *PostgresStore) InitDB() *sql.DB {
	logging.Logger.Info("Initializing PostgreSQL database")

	// sql.Open only validates the DSN format; the first real connection happens on Ping().
	db, err := sql.Open("pgx", s.dsn)
	if err != nil {
		logging.Logger.Error("Failed to open PostgreSQL database: %v", err)
		return nil
	}

	if err := db.Ping(); err != nil {
		logging.Logger.Error("Failed to ping PostgreSQL database: %v", err)
		return nil
	}

	logging.Logger.Debug("PostgreSQL connection established")

	// PostgreSQL schema equivalent to the SQLite readings table.
	// BIGSERIAL is the Postgres way to say "auto-incrementing 64-bit integer".
	// TIMESTAMPTZ stores UTC timestamps with timezone awareness.
	schema := `CREATE TABLE IF NOT EXISTS readings (
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
	CREATE INDEX IF NOT EXISTS idx_readings_disk_created_at ON readings(created_at) WHERE metric_type = 'disk';`

	if _, err := db.Exec(schema); err != nil {
		logging.Logger.Error("Failed to create PostgreSQL schema: %v", err)
		return nil
	}

	// PostgreSQL does not need the SQLite-style migration for metric_type/unit:
	// the CREATE TABLE above already includes them with defaults.
	logging.Logger.Info("PostgreSQL schema ready")
	return db
}

// formatCreatedAt returns the UTC timestamp in the same string layout the rest
// of the app expects ("2006-01-02 15:04:05"), so MeridaTimeConverter.ToLocal()
// can parse it unchanged. We use to_char(... AT TIME ZONE 'UTC', ...) so the
// value is unambiguously UTC, matching the old SQLite TEXT column.
func (s *PostgresStore) formatCreatedAt() string {
	return "to_char(created_at AT TIME ZONE 'UTC', 'YYYY-MM-DD HH24:MI:SS')"
}

// Insert adds a new reading to PostgreSQL.
// Postgres uses $1, $2, ... placeholders instead of SQLite's ?.
func (s *PostgresStore) Insert(db *sql.DB, sensor string, temp float64, metricType string, unit string) {
	_, err := db.Exec(
		"INSERT INTO readings (sensor, temp_c, metric_type, unit) VALUES ($1, $2, $3, $4)",
		sensor, temp, metricType, unit,
	)
	if err != nil {
		logging.Logger.Error("Failed to insert reading (sensor=%s, type=%s, value=%.2f): %v", sensor, metricType, temp, err)
	} else {
		logging.Logger.Debug("Inserted reading: %s [%s] = %.2f%s", sensor, metricType, temp, unit)
	}
}

// Query returns readings inside a TimeWindow, optionally filtered by
// metric type (AllMetrics = all). Postgres placeholders are numbered ($1, $2...).
// An invalid window returns an error instead of querying year 1.
func (s *PostgresStore) Query(db *sql.DB, window TimeWindow, metricType string) ([]Reading, error) {
	if err := window.Validate(); err != nil {
		return nil, err
	}
	var conds []string
	var args []any
	if metricType != AllMetrics {
		args = append(args, metricType)
		conds = append(conds, fmt.Sprintf("metric_type = $%d", len(args)))
	}
	if window.Hours > 0 {
		args = append(args, window.Hours)
		conds = append(conds, fmt.Sprintf("created_at >= NOW() - INTERVAL '1 hour' * $%d", len(args)))
	} else {
		// Pass time.Time (not a formatted string): pgx sends an unambiguous
		// instant, so the session TimeZone cannot shift the range. A string
		// like "2024-01-15 12:00:00" cast to TIMESTAMPTZ would be read in the
		// session timezone (a classic Postgres gotcha, unlike Python's
		// explicit tzinfo).
		args = append(args, window.From.UTC(), window.To.UTC())
		conds = append(conds, fmt.Sprintf("created_at >= $%d::TIMESTAMPTZ AND created_at <= $%d::TIMESTAMPTZ", len(args)-1, len(args)))
	}

	rows, err := db.Query(
		fmt.Sprintf(`SELECT sensor, temp_c, metric_type, unit, %s FROM readings
		 WHERE %s ORDER BY created_at ASC`, s.formatCreatedAt(), strings.Join(conds, " AND ")),
		args...,
	)
	if err != nil {
		logging.Logger.Error("Query failed: %v", err)
		return nil, nil
	}
	defer rows.Close()

	return s.scanRows(rows, "Query", metricType, window.Hours), nil
}

// QueryLatest returns the newest reading per sensor for one metric type.
// Uses a recursive CTE "loose index scan" to jump through the partial index
// (sensor, id DESC) per metric type, avoiding a full scan of all rows.
// In Python terms: instead of scanning all 250K rows to find 3 max values,
// we hop directly to the latest row per sensor via the index -- ~500x faster.
// The metric type is passed as bind parameter $1 (like a DB-API "%s" placeholder).
// metricType is required: AllMetrics returns an error.
func (s *PostgresStore) QueryLatest(db *sql.DB, metricType string) ([]Reading, error) {
	if metricType == AllMetrics {
		return nil, fmt.Errorf("QueryLatest: metricType is required")
	}
	logging.Logger.Debug("Querying latest [%s] per sensor", metricType)

	rows, err := db.Query(
		fmt.Sprintf(`WITH RECURSIVE t AS (
			(SELECT sensor, id FROM readings WHERE metric_type = $1 ORDER BY sensor, id DESC LIMIT 1)
			UNION ALL
			SELECT (SELECT sensor FROM readings WHERE metric_type = $1 AND sensor > t.sensor ORDER BY sensor LIMIT 1),
			       (SELECT id FROM readings WHERE metric_type = $1 AND sensor = (SELECT sensor FROM readings WHERE metric_type = $1 AND sensor > t.sensor ORDER BY sensor LIMIT 1) ORDER BY id DESC LIMIT 1)
			FROM t
			WHERE t.sensor IS NOT NULL
		)
		SELECT r.sensor, r.temp_c, r.metric_type, r.unit, %s
		FROM t JOIN readings r ON r.id = t.id
		WHERE t.sensor IS NOT NULL
		ORDER BY r.sensor`, s.formatCreatedAt()),
		metricType,
	)
	if err != nil {
		logging.Logger.Error("QueryLatest failed: %v", err)
		return nil, nil
	}
	defer rows.Close()

	return s.scanRows(rows, "QueryLatest", metricType), nil
}

// Prune removes readings older than N hours. AllMetrics prunes every
// metric type; otherwise only that type. Placeholders are numbered ($1, $2).
func (s *PostgresStore) Prune(db *sql.DB, metricType string, hours int) {
	logging.Logger.Debug("Pruning [%s] readings older than %d hour(s)", metricType, hours)

	query := "DELETE FROM readings WHERE created_at < NOW() - INTERVAL '1 hour' * $1"
	args := []any{hours}
	if metricType != AllMetrics {
		query += " AND metric_type = $2"
		args = append(args, metricType)
	}

	result, err := db.Exec(query, args...)
	if err != nil {
		logging.Logger.Error("Prune failed for [%s]: %v", metricType, err)
		return
	}

	deleted, _ := result.RowsAffected()
	if deleted > 0 {
		logging.Logger.Info("Pruned %d old [%s] reading(s) (>%dh)", deleted, metricType, hours)
	} else {
		logging.Logger.Debug("No old readings to prune")
	}
}

// scanRows reads rows into Reading values, converting created_at to local time.
// The variadic "context" args are only used for logging (query name, parameters).
func (s *PostgresStore) scanRows(rows *sql.Rows, queryName string, context ...interface{}) []Reading {
	var readings []Reading
	for rows.Next() {
		var r Reading
		if err := rows.Scan(&r.Sensor, &r.TempC, &r.MetricType, &r.Unit, &r.CreatedAt); err != nil {
			logging.Logger.Error("Failed to scan row: %v", err)
			continue
		}
		r.CreatedAt = s.converter.ToLocal(r.CreatedAt)
		readings = append(readings, r)
	}

	if err := rows.Err(); err != nil {
		logging.Logger.Error("Row iteration error: %v", err)
	}

	logging.Logger.Info("Postgres %s returned %d reading(s)", queryName, len(readings))
	return readings
}
