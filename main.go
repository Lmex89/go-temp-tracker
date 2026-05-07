package main

import (
	"flag"
	"fmt"
	"net/http"

	_ "modernc.org/sqlite"
)

// main is the entry point — analogous to Python's if __name__ == "__main__": main()
// Go's main() takes no args — use os.Args or the flag package instead.
func main() {
	port := flag.Int("port", 8080, "HTTP server port")
	interval := flag.Int("interval", 30, "Temperature polling interval in seconds")
	retain := flag.Int("retain", 8760, "Delete temp readings older than N hours (default: 12 months)")
	flag.Parse()

	Logger.Info("Starting System Monitor")
	Logger.Debug("Config: port=%d, interval=%ds, retain=%dh", *port, *interval, *retain)

	// Temperature sensor setup (existing behaviour).
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

	// SystemMetrics for CPU, memory, swap, disk, load (gopsutil-based).
	metrics := NewSystemMetrics()

	// Force single connection to SQLite — prevents SQLITE_BUSY from concurrent goroutines.
	// All 6 poller goroutines share this one connection (like a global lock in Python).
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)

	// Temperature polling goroutine (existing).
	poller := NewPoller(sensor, store, db)
	go poller.Run(*interval, *retain)

	// System metric polling goroutines — each at an appropriate interval.
	// CPU: 5s interval, retain 24h (fast-changing, short retention).
	go RunMetricPoller(store, db, "cpu", 5, 24, metrics.ReadCPU)
	// Memory: 10s interval, retain 24h.
	go RunMetricPoller(store, db, "memory", 10, 24, metrics.ReadMemory)
	// Swap: 60s interval, retain 168h (7 days) — changes slowly.
	go RunMetricPoller(store, db, "swap", 60, 168, metrics.ReadSwap)
	// Disk: 60s interval, retain 168h (7 days) — changes slowly.
	go RunMetricPoller(store, db, "disk", 60, 168, metrics.ReadDisk)
	// Load: 10s interval, retain 24h.
	go RunMetricPoller(store, db, "load", 10, 24, metrics.ReadLoad)

	// Setting up HTTP routes — ServeMux is like Flask's app or Django's urlpatterns.
	mux := http.NewServeMux()

	// Existing temperature endpoints (backward compatible).
	mux.HandleFunc("/api/temps", handleTemps(store, db))
	mux.HandleFunc("/api/current", handleCurrent(store, db))

	// New metric-specific endpoints.
	mux.HandleFunc("/api/cpu", handleMetricByType(store, db, "cpu"))
	mux.HandleFunc("/api/memory", handleMetricByType(store, db, "memory"))
	mux.HandleFunc("/api/swap", handleMetricByType(store, db, "swap"))
	mux.HandleFunc("/api/disk", handleMetricByType(store, db, "disk"))
	mux.HandleFunc("/api/load", handleMetricByType(store, db, "load"))

	// Latest-value endpoints for each metric type (for gauge display).
	mux.HandleFunc("/api/current/cpu", handleCurrentByType(store, db, "cpu"))
	mux.HandleFunc("/api/current/memory", handleCurrentByType(store, db, "memory"))
	mux.HandleFunc("/api/current/swap", handleCurrentByType(store, db, "swap"))
	mux.HandleFunc("/api/current/disk", handleCurrentByType(store, db, "disk"))
	mux.HandleFunc("/api/current/load", handleCurrentByType(store, db, "load"))

	mux.Handle("/", http.FileServer(http.Dir("static")))

	addr := fmt.Sprintf(":%d", *port)
	Logger.Info("HTTP server listening on %s", addr)
	Logger.Info("Open http://localhost%s in your browser", addr)

	if err := http.ListenAndServe(addr, mux); err != nil {
		Logger.Fatal("HTTP server error: %v", err)
	}
}
