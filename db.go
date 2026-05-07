package main

import (
	"database/sql"
	"fmt"
)

// Reading is a *struct* — like a Python dataclass or a NamedTuple.
// Fields with capital letters are "exported" (public) — like Python's public attributes.
// Lowercase fields would be private (only accessible within this package).
// The backtick tags (`json:"sensor"`) tell the JSON encoder what key names to use.
// In Python you'd use @dataclass or just a dict.
// MetricType and Unit were added for multi-metric support (CPU, memory, disk, etc.).
type Reading struct {
	Sensor     string  `json:"sensor"`
	TempC      float64 `json:"temp_c"`      // Renamed to "value" later? Kept for backward compat.
	MetricType string  `json:"metric_type"` // "temperature", "cpu", "memory", "disk", "load"
	Unit       string  `json:"unit"`        // "°C", "%", "bytes", ""
	CreatedAt  string  `json:"created_at"`
}

// Store is the interface for database operations — like a Python ABC/Protocol.
// Any type that has all these methods automatically satisfies Store (no "extends" keyword).
// This lets us swap out SQLite for another DB later without changing other code.
type Store interface {
	InitDB() *sql.DB
	Insert(db *sql.DB, sensor string, temp float64, metricType string, unit string)
	Query(db *sql.DB, hours int) []Reading
	QueryByRange(db *sql.DB, from, to string) []Reading
	QueryLatestPerSensor(db *sql.DB) []Reading
	QueryByType(db *sql.DB, metricType string, hours int) []Reading
	QueryByRangeAndType(db *sql.DB, from, to, metricType string) []Reading
	QueryLatestByType(db *sql.DB, metricType string) []Reading
	Prune(db *sql.DB, hours int)
	PruneByType(db *sql.DB, metricType string, hours int)
}

// SQLiteStore implements the Store interface using SQLite.
// It holds a TimeConverter for converting UTC timestamps to local time.
type SQLiteStore struct {
	converter TimeConverter
}

func NewSQLiteStore() *SQLiteStore {
	return &SQLiteStore{converter: NewMeridaTimeConverter()}
}

// InitDB opens (or creates) the temps.db SQLite file and ensures the readings table exists.
// Also runs schema migration to add metric_type and unit columns if needed.
// In Python with sqlite3 you'd do: conn = sqlite3.connect("temps.db") then conn.execute(CREATE TABLE...).
func (s *SQLiteStore) InitDB() *sql.DB {
	Logger.Info("Initializing SQLite database (temps.db)")

	// sql.Open opens a database driver — it doesn't actually connect yet (like Python's sqlite3.connect).
	db, err := sql.Open("sqlite", "temps.db")
	if err != nil {
		Logger.Error("Failed to open database: %v", err)
		return nil
	}

	// Ping actually tests the connection — similar to conn = sqlite3.connect() which connects immediately.
	if err := db.Ping(); err != nil {
		Logger.Error("Failed to ping database: %v", err)
		return nil
	}

	Logger.Debug("Database connection established")

	// Enable WAL (Write-Ahead Logging) mode for concurrent read/write access.
	// Multiple goroutines write to the DB simultaneously (CPU poll, mem poll, etc.).
	// WAL allows a writer to proceed while readers read the old snapshot — no locks.
	// In Python: conn.execute("PRAGMA journal_mode=WAL")
	if _, err := db.Exec("PRAGMA journal_mode=WAL"); err != nil {
		Logger.Warn("Failed to enable WAL mode: %v", err)
	}

	// Set busy timeout to 5 seconds — when SQLITE_BUSY, wait and retry instead of failing.
	// This gives time for other writers to finish before giving up.
	// In Python: conn.execute("PRAGMA busy_timeout=5000")
	if _, err := db.Exec("PRAGMA busy_timeout=5000"); err != nil {
		Logger.Warn("Failed to set busy timeout: %v", err)
	}

	// SQL schema: CREATE TABLE IF NOT EXISTS is like Python's "CREATE TABLE IF NOT EXISTS".
	// In Python's sqlite3 you'd run the same SQL via cursor.execute().
	schema := `CREATE TABLE IF NOT EXISTS readings (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		sensor TEXT NOT NULL,
		temp_c REAL NOT NULL,
		created_at TEXT NOT NULL DEFAULT (datetime('now'))
	);
	CREATE INDEX IF NOT EXISTS idx_readings_created_at ON readings(created_at);`

	if _, err := db.Exec(schema); err != nil {
		Logger.Error("Failed to create schema: %v", err)
		return nil
	}

	// Run schema migration to add metric_type and unit columns.
	// This is like Python's Alembic migration or manual ALTER TABLE.
	// We check if the column exists first to make migration idempotent.
	s.migrateSchema(db)

	Logger.Info("Database schema ready")
	return db
}

// migrateSchema adds metric_type and unit columns to the readings table.
// In SQLite, ALTER TABLE ADD COLUMN is limited — we can only add columns, not modify existing ones.
// The NOT NULL with DEFAULT ensures existing rows get the correct default values.
// This is idempotent: it only adds columns if they don't already exist.
func (s *SQLiteStore) migrateSchema(db *sql.DB) {
	// Check if metric_type column exists.
	// pragma_table_info returns column metadata — like Python's PRAGMA table_info().
	var count int
	err := db.QueryRow("SELECT COUNT(*) FROM pragma_table_info('readings') WHERE name='metric_type'").Scan(&count)
	if err != nil {
		Logger.Warn("Migration check failed (metric_type): %v", err)
	}
	if count == 0 {
		Logger.Info("Migrating schema: adding metric_type column")
		// NOT NULL DEFAULT 'temperature' — existing temp rows get the correct type.
		if _, err := db.Exec("ALTER TABLE readings ADD COLUMN metric_type TEXT NOT NULL DEFAULT 'temperature'"); err != nil {
			Logger.Error("Migration failed (metric_type): %v", err)
		}
	}

	// Check if unit column exists.
	count = 0
	err = db.QueryRow("SELECT COUNT(*) FROM pragma_table_info('readings') WHERE name='unit'").Scan(&count)
	if err != nil {
		Logger.Warn("Migration check failed (unit): %v", err)
	}
	if count == 0 {
		Logger.Info("Migrating schema: adding unit column")
		if _, err := db.Exec("ALTER TABLE readings ADD COLUMN unit TEXT NOT NULL DEFAULT '°C'"); err != nil {
			Logger.Error("Migration failed (unit): %v", err)
		}
	}
}

// Insert adds a new reading to the database.
// Now includes metric_type and unit for multi-metric support.
// Uses parameterized queries (the ? placeholders) — like Python's cursor.execute("...", (sensor, temp)).
// This prevents SQL injection (same as using ? or %s in Python).
func (s *SQLiteStore) Insert(db *sql.DB, sensor string, temp float64, metricType string, unit string) {
	// Insert into the readings table with metric_type and unit columns.
	_, err := db.Exec(
		"INSERT INTO readings (sensor, temp_c, metric_type, unit) VALUES (?, ?, ?, ?)",
		sensor, temp, metricType, unit,
	)
	if err != nil {
		Logger.Error("Failed to insert reading (sensor=%s, type=%s, value=%.2f): %v", sensor, metricType, temp, err)
	} else {
		Logger.Debug("Inserted reading: %s [%s] = %.2f%s", sensor, metricType, temp, unit)
	}
}

// Query retrieves temperature readings from the last N hours (backward compat — temperature only).
// Now filters by metric_type = 'temperature' so it doesn't mix in CPU/memory/etc.
func (s *SQLiteStore) Query(db *sql.DB, hours int) []Reading {
	Logger.Debug("Querying temperature readings from last %d hour(s)", hours)

	rows, err := db.Query(
		`SELECT sensor, temp_c, metric_type, unit, created_at FROM readings
		 WHERE metric_type = 'temperature' AND created_at >= datetime('now', ?)
		 ORDER BY created_at ASC`,
		fmt.Sprintf("-%d hours", hours),
	)
	if err != nil {
		Logger.Error("Query failed: %v", err)
		return nil
	}
	defer func() {
		if err := rows.Close(); err != nil {
			Logger.Error("Failed to close rows: %v", err)
		}
	}()

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

	Logger.Info("Queried %d temperature reading(s) from last %d hour(s)", len(readings), hours)
	return readings
}

// QueryByRange retrieves temperature readings between two UTC timestamps (backward compat).
func (s *SQLiteStore) QueryByRange(db *sql.DB, from, to string) []Reading {
	Logger.Debug("Querying temperature readings from %s to %s", from, to)

	rows, err := db.Query(
		`SELECT sensor, temp_c, metric_type, unit, created_at FROM readings
		 WHERE metric_type = 'temperature' AND created_at >= ? AND created_at <= ?
		 ORDER BY created_at ASC`,
		from, to,
	)
	if err != nil {
		Logger.Error("Query by range failed: %v", err)
		return nil
	}
	defer func() {
		if err := rows.Close(); err != nil {
			Logger.Error("Failed to close rows: %v", err)
		}
	}()

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

	Logger.Info("Queried %d temperature reading(s) from %s to %s", len(readings), from, to)
	return readings
}

// QueryLatestPerSensor gets the most recent temperature reading for each unique sensor (backward compat).
func (s *SQLiteStore) QueryLatestPerSensor(db *sql.DB) []Reading {
	Logger.Debug("Querying latest temperature per sensor")

	rows, err := db.Query(
		`SELECT sensor, temp_c, metric_type, unit, created_at FROM readings
		 WHERE metric_type = 'temperature'
		 AND id IN (SELECT MAX(id) FROM readings WHERE metric_type = 'temperature' GROUP BY sensor)
		 ORDER BY sensor`,
	)
	if err != nil {
		Logger.Error("Query latest per sensor failed: %v", err)
		return nil
	}
	defer func() {
		if err := rows.Close(); err != nil {
			Logger.Error("Failed to close rows: %v", err)
		}
	}()

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

	for _, r := range readings {
		Logger.Debug("Latest %s: %.2f°C at %s", r.Sensor, r.TempC, r.CreatedAt)
	}

	return readings
}

// QueryByType retrieves readings for a specific metric type from the last N hours.
// Like Query() but filters by metric_type.
func (s *SQLiteStore) QueryByType(db *sql.DB, metricType string, hours int) []Reading {
	Logger.Debug("Querying [%s] readings from last %d hour(s)", metricType, hours)

	rows, err := db.Query(
		`SELECT sensor, temp_c, metric_type, unit, created_at FROM readings
		 WHERE metric_type = ? AND created_at >= datetime('now', ?)
		 ORDER BY created_at ASC`,
		metricType, fmt.Sprintf("-%d hours", hours),
	)
	if err != nil {
		Logger.Error("QueryByType failed: %v", err)
		return nil
	}
	defer func() {
		if err := rows.Close(); err != nil {
			Logger.Error("Failed to close rows: %v", err)
		}
	}()

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

	Logger.Info("Queried %d [%s] reading(s) from last %d hour(s)", len(readings), metricType, hours)
	return readings
}

// QueryByRangeAndType retrieves readings for a specific metric type between two timestamps.
func (s *SQLiteStore) QueryByRangeAndType(db *sql.DB, from, to, metricType string) []Reading {
	Logger.Debug("Querying [%s] readings from %s to %s", metricType, from, to)

	rows, err := db.Query(
		`SELECT sensor, temp_c, metric_type, unit, created_at FROM readings
		 WHERE metric_type = ? AND created_at >= ? AND created_at <= ?
		 ORDER BY created_at ASC`,
		metricType, from, to,
	)
	if err != nil {
		Logger.Error("QueryByRangeAndType failed: %v", err)
		return nil
	}
	defer func() {
		if err := rows.Close(); err != nil {
			Logger.Error("Failed to close rows: %v", err)
		}
	}()

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

	Logger.Info("Queried %d [%s] reading(s)", len(readings), metricType)
	return readings
}

// QueryLatestByType gets the most recent reading per unique sensor for a specific metric type.
func (s *SQLiteStore) QueryLatestByType(db *sql.DB, metricType string) []Reading {
	Logger.Debug("Querying latest [%s] per sensor", metricType)

	rows, err := db.Query(
		`SELECT sensor, temp_c, metric_type, unit, created_at FROM readings
		 WHERE metric_type = ?
		 AND id IN (SELECT MAX(id) FROM readings WHERE metric_type = ? GROUP BY sensor)
		 ORDER BY sensor`,
		metricType, metricType,
	)
	if err != nil {
		Logger.Error("QueryLatestByType failed: %v", err)
		return nil
	}
	defer func() {
		if err := rows.Close(); err != nil {
			Logger.Error("Failed to close rows: %v", err)
		}
	}()

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

	return readings
}

// Prune removes ALL readings older than the specified number of hours (any metric type).
// Like Python: cursor.execute("DELETE FROM readings WHERE created_at < datetime('now', '-6 hours')").
func (s *SQLiteStore) Prune(db *sql.DB, hours int) {
	Logger.Debug("Pruning all readings older than %d hour(s)", hours)

	result, err := db.Exec(
		"DELETE FROM readings WHERE created_at < datetime('now', ?)",
		fmt.Sprintf("-%d hours", hours),
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
// Different metric types can have different retention periods.
// CPU data might only need 24h, while temperature data is kept for a year.
func (s *SQLiteStore) PruneByType(db *sql.DB, metricType string, hours int) {
	Logger.Debug("Pruning [%s] readings older than %d hour(s)", metricType, hours)

	result, err := db.Exec(
		"DELETE FROM readings WHERE metric_type = ? AND created_at < datetime('now', ?)",
		metricType, fmt.Sprintf("-%d hours", hours),
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
