package main

import (
	"flag"
	"fmt"
	"net/http"

	_ "modernc.org/sqlite"
)

func main() {
	port := flag.Int("port", 8080, "HTTP server port")
	interval := flag.Int("interval", 30, "Polling interval in seconds")
	retain := flag.Int("retain", 8760, "Delete readings older than N hours (default: 12 months)")
	flag.Parse()

	Logger.Info("Starting Sensor Temperature Tracker")
	Logger.Debug("Config: port=%d, interval=%ds, retain=%dh", *port, *interval, *retain)

	sensor := NewLinuxThermalSensor()
	store := NewSQLiteStore()
	db := store.InitDB()
	defer func() {
		if err := db.Close(); err != nil {
			Logger.Error("Failed to close database: %v", err)
		} else {
			Logger.Info("Database connection closed")
		}
	}()

	poller := NewPoller(sensor, store, db)
	go poller.Run(*interval, *retain)

	mux := http.NewServeMux()
	mux.HandleFunc("/api/temps", handleTemps(store, db))
	mux.HandleFunc("/api/current", handleCurrent(store, db))
	mux.Handle("/", http.FileServer(http.Dir("static")))

	addr := fmt.Sprintf(":%d", *port)
	Logger.Info("HTTP server listening on %s", addr)
	Logger.Info("Open http://localhost%s in your browser", addr)

	if err := http.ListenAndServe(addr, mux); err != nil {
		Logger.Fatal("HTTP server error: %v", err)
	}
}
