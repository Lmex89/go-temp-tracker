package main

import (
	"database/sql"
	"fmt"
	"os"

	// Register the pgx driver with database/sql.
	// The blank import is like Python's `importlib.import_module` for side effects:
	// it registers the "pgx" driver name so sql.Open("pgx", ...) works.
	_ "github.com/jackc/pgx/v5/stdlib"
)

// PostgresStore implements the Store interface using PostgreSQL.
// It holds a TimeConverter for converting UTC timestamps to local time,
// just like SQLiteStore.
type PostgresStore struct {
	converter TimeConverter
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
		converter: NewMeridaTimeConverter(),
		dsn:       dsn,
	}
}

// InitDB opens a connection pool to PostgreSQL and ensures the readings table exists.
// Unlike SQLite, PostgreSQL is a real server: it handles concurrent writers itself,
// so we do NOT need PRAGMA journal_mode or busy_timeout, and we do NOT force
// SetMaxOpenConns(1). The connection pool is left at sensible defaults.
func (s *PostgresStore) InitDB() *sql.DB {
	Logger.Info("Initializing PostgreSQL database")

	// sql.Open only validates the DSN format; the first real connection happens on Ping().
	db, err := sql.Open("pgx", s.dsn)
	if err != nil {
		Logger.Error("Failed to open PostgreSQL database: %v", err)
		return nil
	}

	if err := db.Ping(); err != nil {
		Logger.Error("Failed to ping PostgreSQL database: %v", err)
		return nil
	}

	Logger.Debug("PostgreSQL connection established")

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
		Logger.Error("Failed to create PostgreSQL schema: %v", err)
		return nil
	}

	// PostgreSQL does not need the SQLite-style migration for metric_type/unit:
	// the CREATE TABLE above already includes them with defaults.
	Logger.Info("PostgreSQL schema ready")
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
		Logger.Error("Failed to insert reading (sensor=%s, type=%s, value=%.2f): %v", sensor, metricType, temp, err)
	} else {
		Logger.Debug("Inserted reading: %s [%s] = %.2f%s", sensor, metricType, temp, unit)
	}
}

// Query retrieves temperature readings from the last N hours (backward compat).
// INTERVAL '1 hour' * $1 converts the integer hour count to a Postgres interval.
func (s *PostgresStore) Query(db *sql.DB, hours int) []Reading {
	Logger.Debug("Querying temperature readings from last %d hour(s)", hours)

	rows, err := db.Query(
		fmt.Sprintf(`SELECT sensor, temp_c, metric_type, unit, %s FROM readings
		 WHERE metric_type = 'temperature' AND created_at >= NOW() - INTERVAL '1 hour' * $1
		 ORDER BY created_at ASC`, s.formatCreatedAt()),
		hours,
	)
	if err != nil {
		Logger.Error("Query failed: %v", err)
		return nil
	}
	defer rows.Close()

	return s.scanRows(rows, "Query", hours)
}

// QueryByRange retrieves temperature readings between two UTC timestamps.
func (s *PostgresStore) QueryByRange(db *sql.DB, from, to string) []Reading {
	Logger.Debug("Querying temperature readings from %s to %s", from, to)

	rows, err := db.Query(
		fmt.Sprintf(`SELECT sensor, temp_c, metric_type, unit, %s FROM readings
		 WHERE metric_type = 'temperature' AND created_at >= $1::TIMESTAMPTZ AND created_at <= $2::TIMESTAMPTZ
		 ORDER BY created_at ASC`, s.formatCreatedAt()),
		from, to,
	)
	if err != nil {
		Logger.Error("Query by range failed: %v", err)
		return nil
	}
	defer rows.Close()

	return s.scanRows(rows, "QueryByRange", from, to)
}

// QueryLatestPerSensor gets the most recent temperature reading per sensor.
// Delegates to QueryLatestByType since the logic is identical for temperature.
func (s *PostgresStore) QueryLatestPerSensor(db *sql.DB) []Reading {
	return s.QueryLatestByType(db, "temperature")
}

// QueryByType retrieves readings for a specific metric type from the last N hours.
func (s *PostgresStore) QueryByType(db *sql.DB, metricType string, hours int) []Reading {
	Logger.Debug("Querying [%s] readings from last %d hour(s)", metricType, hours)

	rows, err := db.Query(
		fmt.Sprintf(`SELECT sensor, temp_c, metric_type, unit, %s FROM readings
		 WHERE metric_type = $1 AND created_at >= NOW() - INTERVAL '1 hour' * $2
		 ORDER BY created_at ASC`, s.formatCreatedAt()),
		metricType, hours,
	)
	if err != nil {
		Logger.Error("QueryByType failed: %v", err)
		return nil
	}
	defer rows.Close()

	return s.scanRows(rows, "QueryByType", metricType, hours)
}

// QueryByRangeAndType retrieves readings for a metric type between two timestamps.
func (s *PostgresStore) QueryByRangeAndType(db *sql.DB, from, to, metricType string) []Reading {
	Logger.Debug("Querying [%s] readings from %s to %s", metricType, from, to)

	rows, err := db.Query(
		fmt.Sprintf(`SELECT sensor, temp_c, metric_type, unit, %s FROM readings
		 WHERE metric_type = $1 AND created_at >= $2::TIMESTAMPTZ AND created_at <= $3::TIMESTAMPTZ
		 ORDER BY created_at ASC`, s.formatCreatedAt()),
		metricType, from, to,
	)
	if err != nil {
		Logger.Error("QueryByRangeAndType failed: %v", err)
		return nil
	}
	defer rows.Close()

	return s.scanRows(rows, "QueryByRangeAndType", metricType, from, to)
}

// QueryLatestByType gets the most recent reading per sensor for a metric type.
// Uses a recursive CTE "loose index scan" to jump through the partial index
// (sensor, id DESC) per metric type, avoiding a full scan of all rows.
// In Python terms: instead of scanning all 250K rows to find 3 max values,
// we hop directly to the latest row per sensor via the index -- ~500x faster.
func (s *PostgresStore) QueryLatestByType(db *sql.DB, metricType string) []Reading {
	Logger.Debug("Querying latest [%s] per sensor", metricType)

	rows, err := db.Query(
		fmt.Sprintf(`WITH RECURSIVE t AS (
			(SELECT sensor, id FROM readings WHERE metric_type = '%s' ORDER BY sensor, id DESC LIMIT 1)
			UNION ALL
			SELECT (SELECT sensor FROM readings WHERE metric_type = '%s' AND sensor > t.sensor ORDER BY sensor LIMIT 1),
			       (SELECT id FROM readings WHERE metric_type = '%s' AND sensor = (SELECT sensor FROM readings WHERE metric_type = '%s' AND sensor > t.sensor ORDER BY sensor LIMIT 1) ORDER BY id DESC LIMIT 1)
			FROM t
			WHERE t.sensor IS NOT NULL
		)
		SELECT r.sensor, r.temp_c, r.metric_type, r.unit, %s
		FROM t JOIN readings r ON r.id = t.id
		WHERE t.sensor IS NOT NULL
		ORDER BY r.sensor`, metricType, metricType, metricType, metricType, s.formatCreatedAt()),
	)
	if err != nil {
		Logger.Error("QueryLatestByType failed: %v", err)
		return nil
	}
	defer rows.Close()

	return s.scanRows(rows, "QueryLatestByType", metricType)
}

// Prune removes ALL readings older than the specified number of hours.
func (s *PostgresStore) Prune(db *sql.DB, hours int) {
	Logger.Debug("Pruning all readings older than %d hour(s)", hours)

	result, err := db.Exec(
		"DELETE FROM readings WHERE created_at < NOW() - INTERVAL '1 hour' * $1",
		hours,
	)
	if err != nil {
		Logger.Error("Prune failed: %v", err)
		return
	}

	deleted, _ := result.RowsAffected()
	if deleted > 0 {
		Logger.Info("Pruned %d old reading(s) (>%dh)", deleted, hours)
	} else {
		Logger.Debug("No old readings to prune")
	}
}

// PruneByType removes readings older than N hours for a specific metric type.
func (s *PostgresStore) PruneByType(db *sql.DB, metricType string, hours int) {
	Logger.Debug("Pruning [%s] readings older than %d hour(s)", metricType, hours)

	result, err := db.Exec(
		"DELETE FROM readings WHERE metric_type = $1 AND created_at < NOW() - INTERVAL '1 hour' * $2",
		metricType, hours,
	)
	if err != nil {
		Logger.Error("PruneByType failed for %s: %v", metricType, err)
		return
	}

	deleted, _ := result.RowsAffected()
	if deleted > 0 {
		Logger.Info("Pruned %d old [%s] reading(s) (>%dh)", deleted, metricType, hours)
	}
}

// scanRows reads rows into Reading values, converting created_at to local time.
// The variadic "context" args are only used for logging (query name, parameters).
func (s *PostgresStore) scanRows(rows *sql.Rows, queryName string, context ...interface{}) []Reading {
	var readings []Reading
	for rows.Next() {
		var r Reading
		if err := rows.Scan(&r.Sensor, &r.TempC, &r.MetricType, &r.Unit, &r.CreatedAt); err != nil {
			Logger.Error("Failed to scan row: %v", err)
			continue
		}
		r.CreatedAt = s.converter.ToLocal(r.CreatedAt)
		readings = append(readings, r)
	}

	if err := rows.Err(); err != nil {
		Logger.Error("Row iteration error: %v", err)
	}

	Logger.Info("Postgres %s returned %d reading(s)", queryName, len(readings))
	return readings
}
