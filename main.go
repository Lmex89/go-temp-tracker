package main

import (
	"database/sql"
	"flag"
	"fmt"
	"net/http"
	"os"
	"time"

	_ "modernc.org/sqlite"
)

func main() {
	port := flag.Int("port", 8080, "HTTP server port")
	interval := flag.Int("interval", 30, "Polling interval in seconds")
	retain := flag.Int("retain", 720, "Delete readings older than N hours")
	flag.Parse()

	Logger.Info("Starting Sensor Temperature Tracker")
	Logger.Debug("Config: port=%d, interval=%ds, retain=%dh", *port, *interval, *retain)

	db := initDB()
	defer func() {
		if err := db.Close(); err != nil {
			Logger.Error("Failed to close database: %v", err)
		} else {
			Logger.Info("Database connection closed")
		}
	}()

	go pollLoop(db, *interval, *retain)

	mux := http.NewServeMux()
	mux.HandleFunc("/api/temps", handleTemps(db))
	mux.HandleFunc("/api/current", handleCurrent(db))
	mux.Handle("/", http.FileServer(http.Dir("static")))

	addr := fmt.Sprintf(":%d", *port)
	Logger.Info("HTTP server listening on %s", addr)
	Logger.Info("Open http://localhost%s in your browser", addr)

	if err := http.ListenAndServe(addr, mux); err != nil {
		Logger.Fatal("HTTP server error: %v", err)
	}
}

func (l *LeveledLogger) Fatal(format string, args ...interface{}) {
	l.log(ERROR, format, args...)
	os.Exit(1)
}

func pollLoop(db *sql.DB, intervalSec, retainHours int) {
	Logger.Info("Polling loop started (interval=%ds, retain=%dh)", intervalSec, retainHours)

	for {
		Logger.Debug("Reading CPU temperatures...")
		temps := readCPUTemps()

		if len(temps) == 0 {
			Logger.Warn("No temperature sensors found")
		}

		for sensor, temp := range temps {
			Logger.Debug("Recording %s: %.2f°C", sensor, temp)
			insertReading(db, sensor, temp)
		}

		Logger.Info("Recorded %d temperature reading(s)", len(temps))
		pruneOldReadings(db, retainHours)

		Logger.Debug("Sleeping for %d seconds...", intervalSec)
		time.Sleep(time.Duration(intervalSec) * time.Second)
	}
}
