package store

import (
	"database/sql"
	"fmt"
	"sensors-temp/internal/logging"
	"sensors-temp/internal/timeutil"
	"strings"
	"time"

	// Register the pure-Go "sqlite" driver (side-effect import, like a Python plugin import).
	_ "modernc.org/sqlite"
)

// SQLiteStore implements the Store interface using SQLite.
// It holds a TimeConverter for converting UTC timestamps to local time.
type SQLiteStore struct {
	converter timeutil.TimeConverter
	path      string // database file, e.g. "temps.db" (chosen by the caller)
}

// NewSQLiteStore creates a store bound to the given database file.
// The path is a parameter (not hardcoded) so tests can point at t.TempDir().
// Python analogy: sqlite3.connect(path) where path comes from the caller.
func NewSQLiteStore(path string) *SQLiteStore {
	return &SQLiteStore{converter: timeutil.NewMeridaTimeConverter(), path: path}
}

// InitDB opens (or creates) the SQLite file given to NewSQLiteStore and ensures the readings table exists.
// Also runs schema migration to add metric_type and unit columns if needed.
// In Python with sqlite3 you'd do: conn = sqlite3.connect(path) then conn.execute(CREATE TABLE...).
func (s *SQLiteStore) InitDB() *sql.DB {
	logging.Logger.Info("Initializing SQLite database (%s)", s.path)

	// sql.Open opens a database driver -- it doesn't actually connect yet (like Python's sqlite3.connect).
	db, err := sql.Open("sqlite", s.path)
	if err != nil {
		logging.Logger.Error("Failed to open database: %v", err)
		return nil
	}

	// Ping actually tests the connection -- similar to conn = sqlite3.connect() which connects immediately.
	if err := db.Ping(); err != nil {
		logging.Logger.Error("Failed to ping database: %v", err)
		return nil
	}

	logging.Logger.Debug("Database connection established")

	// Enable WAL (Write-Ahead Logging) mode for concurrent read/write access.
	// Multiple goroutines write to the DB simultaneously (CPU poll, mem poll, etc.).
	// WAL allows a writer to proceed while readers read the old snapshot -- no locks.
	// In Python: conn.execute("PRAGMA journal_mode=WAL")
	if _, err := db.Exec("PRAGMA journal_mode=WAL"); err != nil {
		logging.Logger.Warn("Failed to enable WAL mode: %v", err)
	}

	// Set busy timeout to 5 seconds -- when SQLITE_BUSY, wait and retry instead of failing.
	// This gives time for other writers to finish before giving up.
	// In Python: conn.execute("PRAGMA busy_timeout=5000")
	if _, err := db.Exec("PRAGMA busy_timeout=5000"); err != nil {
		logging.Logger.Warn("Failed to set busy timeout: %v", err)
	}

	// SQL schema: CREATE TABLE IF NOT EXISTS is like Python's "CREATE TABLE IF NOT EXISTS".
	// In Python's sqlite3 you'd run the same SQL via cursor.execute().
	// Order matters: create the table, migrate (adds metric_type/unit on old
	// files), and only then create the indexes that reference metric_type.
	// Creating the indexes first fails on a brand-new file.
	tableSchema := `CREATE TABLE IF NOT EXISTS readings (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		sensor TEXT NOT NULL,
		temp_c REAL NOT NULL,
		created_at TEXT NOT NULL DEFAULT (datetime('now'))
	);`
	if _, err := db.Exec(tableSchema); err != nil {
		logging.Logger.Error("Failed to create schema: %v", err)
		return nil
	}

	// Run schema migration to add metric_type and unit columns.
	// This is like Python's Alembic migration or manual ALTER TABLE.
	// We check if the column exists first to make migration idempotent.
	s.migrateSchema(db)

	indexSchema := `CREATE INDEX IF NOT EXISTS idx_readings_created_at ON readings(created_at);
	CREATE INDEX IF NOT EXISTS idx_readings_metric_type_created_at ON readings(metric_type, created_at);
	CREATE INDEX IF NOT EXISTS idx_readings_metric_type_sensor_id ON readings(metric_type, sensor, id);`
	if _, err := db.Exec(indexSchema); err != nil {
		logging.Logger.Error("Failed to create indexes: %v", err)
		return nil
	}

	logging.Logger.Info("Database schema ready")
	return db
}

// migrateSchema adds metric_type and unit columns to the readings table.
// In SQLite, ALTER TABLE ADD COLUMN is limited -- we can only add columns, not modify existing ones.
// The NOT NULL with DEFAULT ensures existing rows get the correct default values.
// This is idempotent: it only adds columns if they don't already exist.
func (s *SQLiteStore) migrateSchema(db *sql.DB) {
	// Check if metric_type column exists.
	// pragma_table_info returns column metadata -- like Python's PRAGMA table_info().
	var count int
	err := db.QueryRow("SELECT COUNT(*) FROM pragma_table_info('readings') WHERE name='metric_type'").Scan(&count)
	if err != nil {
		logging.Logger.Warn("Migration check failed (metric_type): %v", err)
	}
	if count == 0 {
		logging.Logger.Info("Migrating schema: adding metric_type column")
		// NOT NULL DEFAULT 'temperature' -- existing temp rows get the correct type.
		if _, err := db.Exec("ALTER TABLE readings ADD COLUMN metric_type TEXT NOT NULL DEFAULT 'temperature'"); err != nil {
			logging.Logger.Error("Migration failed (metric_type): %v", err)
		}
	}

	// Check if unit column exists.
	count = 0
	err = db.QueryRow("SELECT COUNT(*) FROM pragma_table_info('readings') WHERE name='unit'").Scan(&count)
	if err != nil {
		logging.Logger.Warn("Migration check failed (unit): %v", err)
	}
	if count == 0 {
		logging.Logger.Info("Migrating schema: adding unit column")
		if _, err := db.Exec("ALTER TABLE readings ADD COLUMN unit TEXT NOT NULL DEFAULT 'C'"); err != nil {
			logging.Logger.Error("Migration failed (unit): %v", err)
		}
	}
}

// Insert adds a new reading to the database.
// execWithRetry wraps db.Exec with exponential backoff retry on SQLITE_BUSY.
// Even with SetMaxOpenConns(1), a safety net prevents transient lock failures.
// Retries up to 5 times: 50ms, 100ms, 200ms, 400ms, 800ms (total ~1.5s).
// In Python: a decorator with time.sleep(backoff) around cursor.execute().
func execWithRetry(db *sql.DB, query string, args ...interface{}) (sql.Result, error) {
	maxRetries := 5
	backoff := 50 * time.Millisecond

	var result sql.Result
	var err error

	for i := 0; i <= maxRetries; i++ {
		result, err = db.Exec(query, args...)
		if err == nil {
			return result, nil
		}

		if !isBusyError(err) || i == maxRetries {
			return nil, err
		}

		logging.Logger.Debug("DB busy, retrying in %v (attempt %d/%d)", backoff, i+1, maxRetries)
		time.Sleep(backoff)
		backoff *= 2
	}

	return result, err
}

// isBusyError checks if an error is a SQLite busy/locked error.
// Matches both "database is locked" and "SQLITE_BUSY" patterns.
func isBusyError(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "database is locked") ||
		strings.Contains(msg, "SQLITE_BUSY")
}

// Now includes metric_type and unit for multi-metric support.
// Uses parameterized queries (the ? placeholders) -- like Python's cursor.execute("...", (sensor, temp)).
// This prevents SQL injection (same as using ? or %s in Python).
func (s *SQLiteStore) Insert(db *sql.DB, sensor string, temp float64, metricType string, unit string) {
	// Insert into the readings table with metric_type and unit columns.
	_, err := execWithRetry(
		db,
		"INSERT INTO readings (sensor, temp_c, metric_type, unit) VALUES (?, ?, ?, ?)",
		sensor, temp, metricType, unit,
	)
	if err != nil {
		logging.Logger.Error("Failed to insert reading (sensor=%s, type=%s, value=%.2f): %v", sensor, metricType, temp, err)
	} else {
		logging.Logger.Debug("Inserted reading: %s [%s] = %.2f%s", sensor, metricType, temp, unit)
	}
}

// Query returns readings inside a TimeWindow, optionally filtered by
// metric type (AllMetrics = all metrics). Go returns errors as values, like a
// Python function that raises ValueError for a bad window.
func (s *SQLiteStore) Query(db *sql.DB, window TimeWindow, metricType string) ([]Reading, error) {
	if err := window.Validate(); err != nil {
		return nil, err
	}
	logging.Logger.Debug("Query window=%+v metric=%q", window, metricType)

	// Build the WHERE clause piece by piece; args line up with the "?" marks.
	var conds []string
	var args []any
	if metricType != AllMetrics {
		conds = append(conds, "metric_type = ?")
		args = append(args, metricType)
	}
	if window.Hours > 0 {
		conds = append(conds, "created_at >= datetime('now', ?)")
		args = append(args, fmt.Sprintf("-%d hours", window.Hours))
	} else {
		conds = append(conds, "created_at >= ? AND created_at <= ?")
		args = append(args, window.From.UTC().Format(timeutil.DBLayout), window.To.UTC().Format(timeutil.DBLayout))
	}

	rows, err := db.Query(
		`SELECT sensor, temp_c, metric_type, unit, created_at FROM readings
		 WHERE `+strings.Join(conds, " AND ")+` ORDER BY created_at ASC`,
		args...,
	)
	if err != nil {
		logging.Logger.Error("Query failed: %v", err)
		return nil, err
	}
	readings, err := s.scanRows(rows)
	if err != nil {
		return nil, err
	}

	logging.Logger.Info("Query returned %d reading(s)", len(readings))
	return readings, nil
}

// QueryLatest returns the newest reading per sensor for ONE metric type.
// metricType is required (no "all metrics" variant): AllMetrics returns an
// error. The SQL groups by sensor and takes MAX(id).
func (s *SQLiteStore) QueryLatest(db *sql.DB, metricType string) ([]Reading, error) {
	if metricType == AllMetrics {
		return nil, fmt.Errorf("QueryLatest: metricType is required")
	}
	logging.Logger.Debug("Querying latest [%s] per sensor", metricType)

	rows, err := db.Query(
		`SELECT sensor, temp_c, metric_type, unit, created_at FROM readings
		 WHERE metric_type = ?
		 AND id IN (SELECT MAX(id) FROM readings WHERE metric_type = ? GROUP BY sensor)
		 ORDER BY sensor`,
		metricType, metricType,
	)
	if err != nil {
		logging.Logger.Error("QueryLatest failed: %v", err)
		return nil, err
	}
	return s.scanRows(rows)
}

// scanRows drains rows into Reading values (written once, shared by every
// query). It closes rows itself, like Python's "with cursor:" block, and
// converts created_at from UTC to local time.
func (s *SQLiteStore) scanRows(rows *sql.Rows) ([]Reading, error) {
	defer func() {
		if err := rows.Close(); err != nil {
			logging.Logger.Error("Failed to close rows: %v", err)
		}
	}()

	var readings []Reading
	for rows.Next() {
		var r Reading
		if err := rows.Scan(&r.Sensor, &r.TempC, &r.MetricType, &r.Unit, &r.CreatedAt); err != nil {
			logging.Logger.Error("Failed to scan row: %v", err)
			return nil, err
		}
		r.CreatedAt = s.converter.ToLocal(r.CreatedAt)
		readings = append(readings, r)
	}
	if err := rows.Err(); err != nil {
		logging.Logger.Error("Row iteration error: %v", err)
		return nil, err
	}
	return readings, nil
}

// Prune removes readings older than the given number of hours. AllMetrics
// as metricType prunes every metric type (what the temperature Poller needs);
// otherwise only that type is pruned, so metric types can have their own
// retention. Python analogy: a DELETE with an optional extra WHERE clause.
func (s *SQLiteStore) Prune(db *sql.DB, metricType string, hours int) {
	logging.Logger.Debug("Pruning [%s] readings older than %d hour(s)", metricType, hours)

	query := "DELETE FROM readings WHERE created_at < datetime('now', ?)"
	args := []any{fmt.Sprintf("-%d hours", hours)}
	if metricType != AllMetrics {
		query += " AND metric_type = ?"
		args = append(args, metricType)
	}

	result, err := execWithRetry(db, query, args...)
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
