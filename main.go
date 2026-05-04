// Package main — the entry point package (like Python's if __name__ == "__main__").
// In Go, the program starts executing from the main() function in package main.
package main

import (
	"flag"  // flag — built-in CLI argument parser (like argparse in Python)
	"fmt"
	"net/http"

	// Blank import: we import sqlite driver for its side effects (it registers itself with database/sql).
	// In Python this is like "import sqlite_driver" even if you don't call it directly.
	_ "modernc.org/sqlite"
)

// main is the entry point — analogous to Python's if __name__ == "__main__": main()
// Go's main() takes no args — use os.Args or the flag package instead.
func main() {
	// flag.Type() returns a *pointer* to the value (like returning an "address" of a box).
	// Later we dereference with *port to get the actual int. This is Go's way of saying
	// "here's where I'll store the parsed value." Python just returns the value directly.
	port := flag.Int("port", 8080, "HTTP server port")
	interval := flag.Int("interval", 30, "Polling interval in seconds")
	retain := flag.Int("retain", 8760, "Delete readings older than N hours (default: 12 months)")
	flag.Parse()

	Logger.Info("Starting Sensor Temperature Tracker")
	Logger.Debug("Config: port=%d, interval=%ds, retain=%dh", *port, *interval, *retain)

	// Creating struct instances — in Python we'd call ClassName(), here it's NewXxx().
	// The returned *LinuxThermalSensor is a pointer (like a reference in Python).
	sensor := NewLinuxThermalSensor()
	store := NewSQLiteStore()
	db := store.InitDB()
	// defer runs this function when main() returns — like Python's context manager (with ...).
	// It's the Go way to ensure cleanup (close files, db connections, etc.).
	defer func() {
		if err := db.Close(); err != nil {
			Logger.Error("Failed to close database: %v", err)
		} else {
			Logger.Info("Database connection closed")
		}
	}()

	poller := NewPoller(sensor, store, db)
	// "go" launches poller.Run in a NEW THREAD (called a goroutine).
	// Like Python's threading.Thread(target=poller.run).start() but much lighter.
	go poller.Run(*interval, *retain)

	// Setting up HTTP routes — ServeMux is like Flask's app or Django's urlpatterns.
	mux := http.NewServeMux()
	mux.HandleFunc("/api/temps", handleTemps(store, db))
	mux.HandleFunc("/api/current", handleCurrent(store, db))
	mux.Handle("/", http.FileServer(http.Dir("static")))

	// Sprintf works like Python's f-string or "string % args" formatting.
	addr := fmt.Sprintf(":%d", *port)
	Logger.Info("HTTP server listening on %s", addr)
	Logger.Info("Open http://localhost%s in your browser", addr)

	// ListenAndServe blocks (like Flask's app.run()). If it fails, we exit.
	if err := http.ListenAndServe(addr, mux); err != nil {
		Logger.Fatal("HTTP server error: %v", err)
	}
}
