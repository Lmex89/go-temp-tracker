package main

import (
	"database/sql"
	"fmt"
	"time"
)

func initDB() *sql.DB {
	Logger.Info("Initializing SQLite database (temps.db)")

	db, err := sql.Open("sqlite", "temps.db")
	if err != nil {
		Logger.Error("Failed to open database: %v", err)
		return nil
	}

	if err := db.Ping(); err != nil {
		Logger.Error("Failed to ping database: %v", err)
		return nil
	}

	Logger.Debug("Database connection established")

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

func insertReading(db *sql.DB, sensor string, temp float64) {
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

type Reading struct {
	Sensor    string  `json:"sensor"`
	TempC     float64 `json:"temp_c"`
	CreatedAt string  `json:"created_at"`
}

func queryReadings(db *sql.DB, hours int) []Reading {
	Logger.Debug("Querying readings from last %d hour(s)", hours)

	// Load the America/Merida timezone
	meridaTZ, err := time.LoadLocation("America/Merida")
	if err != nil {
		Logger.Error("Failed to load America/Merida timezone: %v", err)
		meridaTZ = time.UTC
	}

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
		
		// Convert UTC timestamp to America/Merida local time
		if utcTime, err := time.Parse("2006-01-02 15:04:05", r.CreatedAt); err == nil {
			localTime := utcTime.In(meridaTZ)
			r.CreatedAt = localTime.Format("2006-01-02 15:04:05")
		}
		
		readings = append(readings, r)
	}

	if err := rows.Err(); err != nil {
		Logger.Error("Row iteration error: %v", err)
	}

	Logger.Info("Queried %d reading(s) from last %d hour(s)", len(readings), hours)
	return readings
}

func queryLatestPerSensor(db *sql.DB) []Reading {
	Logger.Debug("Querying latest reading per sensor")

	// Load the America/Merida timezone
	meridaTZ, err := time.LoadLocation("America/Merida")
	if err != nil {
		Logger.Error("Failed to load America/Merida timezone: %v", err)
		meridaTZ = time.UTC
	}

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
		
		// Convert UTC timestamp to America/Merida local time
		if utcTime, err := time.Parse("2006-01-02 15:04:05", r.CreatedAt); err == nil {
			localTime := utcTime.In(meridaTZ)
			r.CreatedAt = localTime.Format("2006-01-02 15:04:05")
		}
		
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

func pruneOldReadings(db *sql.DB, hours int) {
	Logger.Debug("Pruning readings older than %d hour(s)", hours)

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
