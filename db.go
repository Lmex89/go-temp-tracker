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
type Reading struct {
	Sensor    string  `json:"sensor"`
	TempC     float64 `json:"temp_c"`
	CreatedAt string  `json:"created_at"`
}

// Store is the interface for database operations — like a Python ABC/Protocol.
// Any type that has all these methods automatically satisfies Store (no "extends" keyword).
// This lets us swap out SQLite for another DB later without changing other code.
type Store interface {
	InitDB() *sql.DB
	Insert(db *sql.DB, sensor string, temp float64)
	Query(db *sql.DB, hours int) []Reading
	QueryLatestPerSensor(db *sql.DB) []Reading
	Prune(db *sql.DB, hours int)
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

	Logger.Info("Database schema ready")
	return db
}

// Insert adds a new temperature reading to the database.
// Uses parameterized queries (the ? placeholders) — like Python's cursor.execute("...", (sensor, temp)).
// This prevents SQL injection (same as using ? or %s in Python).
func (s *SQLiteStore) Insert(db *sql.DB, sensor string, temp float64) {
	_, err := db.Exec(
		"INSERT INTO readings (sensor, temp_c) VALUES (?, ?)",
		sensor, temp,
	)
	if err != nil {
		Logger.Error("Failed to insert reading (sensor=%s, temp=%.2f): %v", sensor, temp, err)
	} else {
		Logger.Debug("Inserted reading: %s = %.2f°C", sensor, temp)
	}
}

// Query retrieves temperature readings from the last N hours.
// Converts UTC timestamps to America/Merida local time (via the converter).
// Returns a slice ([]Reading) — like a Python list of Reading objects.
func (s *SQLiteStore) Query(db *sql.DB, hours int) []Reading {
	Logger.Debug("Querying readings from last %d hour(s)", hours)

	// db.Query returns rows (*sql.Rows) and error — like cursor.execute() then cursor.fetchall().
	// The ? placeholder is filled with fmt.Sprintf("-%d hours", hours) — e.g. "-6 hours".
	rows, err := db.Query(
		`SELECT sensor, temp_c, created_at FROM readings
		 WHERE created_at >= datetime('now', ?)
		 ORDER BY created_at ASC`,
		fmt.Sprintf("-%d hours", hours),
	)
	if err != nil {
		Logger.Error("Query failed: %v", err)
		return nil
	}
	// Defer closing rows — like Python's "with rows:" or "finally: rows.close()".
	defer func() {
		if err := rows.Close(); err != nil {
			Logger.Error("Failed to close rows: %v", err)
		}
	}()

	// var readings []Reading declares an empty slice (like [] in Python).
	// It's nil initially — nillable just like Python's None for a list.
	var readings []Reading
	for rows.Next() {
		var r Reading
		// Scan reads column values into the struct fields — like unpacking a tuple in Python.
		// The & means "address of" — we pass pointers so Scan can write into them.
		// In Python you'd use: r = Reading(*cursor.fetchone()) or similar.
		if err := rows.Scan(&r.Sensor, &r.TempC, &r.CreatedAt); err != nil {
			Logger.Error("Failed to scan row: %v", err)
			continue
		}
		r.CreatedAt = s.converter.ToLocal(r.CreatedAt)
		// append to a slice — like Python's list.append(). Go creates a new slice if needed.
		readings = append(readings, r)
	}

	if err := rows.Err(); err != nil {
		Logger.Error("Row iteration error: %v", err)
	}

	Logger.Info("Queried %d reading(s) from last %d hour(s)", len(readings), hours)
	return readings
}

// QueryLatestPerSensor gets the most recent temperature reading for each unique sensor.
// Uses a subquery: WHERE id IN (SELECT MAX(id) FROM readings GROUP BY sensor).
// Like Python: cursor.execute("SELECT ...") then fetch and convert times.
func (s *SQLiteStore) QueryLatestPerSensor(db *sql.DB) []Reading {
	Logger.Debug("Querying latest reading per sensor")

	rows, err := db.Query(
		`SELECT sensor, temp_c, created_at FROM readings
		 WHERE id IN (SELECT MAX(id) FROM readings GROUP BY sensor)
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
		if err := rows.Scan(&r.Sensor, &r.TempC, &r.CreatedAt); err != nil {
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

// Prune removes readings older than the specified number of hours to save space.
// Like Python: cursor.execute("DELETE FROM readings WHERE created_at < datetime('now', '-6 hours')").
func (s *SQLiteStore) Prune(db *sql.DB, hours int) {
	Logger.Debug("Pruning readings older than %d hour(s)", hours)

	result, err := db.Exec(
		"DELETE FROM readings WHERE created_at < datetime('now', ?)",
		fmt.Sprintf("-%d hours", hours),
	)
	if err != nil {
		Logger.Error("Prune failed: %v", err)
		return
	}

	// RowsAffected() returns how many rows were deleted — like Python's cursor.rowcount.
	deleted, _ := result.RowsAffected()
	if deleted > 0 {
		Logger.Info("Pruned %d old reading(s) (>%dh)", deleted, hours)
	} else {
		Logger.Debug("No old readings to prune")
	}
}
