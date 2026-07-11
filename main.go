package main

import (
	"flag"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"time"

	_ "modernc.org/sqlite"
)

// getEnvInt reads an environment variable and parses it as int, returning default if not set or invalid.
// Like Python: int(os.getenv("VAR_NAME", "default")) but with error handling.
// In Go, env vars are strings, so we need strconv.Atoi() to convert to int.
func getEnvInt(key string, defaultVal int) int {
	val := os.Getenv(key)
	if val == "" {
		return defaultVal
	}
	parsed, err := strconv.Atoi(val)
	if err != nil {
		Logger.Warn("Invalid value for %s=%q, using default %d", key, val, defaultVal)
		return defaultVal
	}
	return parsed
}

// main is the entry point — analogous to Python's if __name__ == "__main__": main()
// Go's main() takes no args — use os.Args or the flag package instead.
func main() {
	port := flag.Int("port", 8080, "HTTP server port")
	interval := flag.Int("interval", 60, "Temperature polling interval in seconds")
	retain := flag.Int("retain", 8760, "Delete readings older than N hours for ALL metric types (default: 12 months)")
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

	// Allow up to 10 connections to SQLite — WAL mode supports concurrent reads.
	// Poller goroutines and HTTP handlers share these connections without serializing.
	// With WAL + modernc internal locking, writers still serialize safely,
	// but the extra headroom prevents HTTP handlers from queueing behind pollers.
	db.SetMaxOpenConns(10)
	db.SetMaxIdleConns(3)

	// Stagger pollers by 3s each so they don't all hit the DB simultaneously.
	// Without staggering, 6 goroutines sharing 3 conns create a thundering herd
	// against SQLite's internal lock, causing 5s busy_timeout waits on every cycle.
	// In Python: time.sleep(offset) before entering the while loop.
	poller := NewPoller(sensor, store, db)
	go func() {
		time.Sleep(0 * time.Second)
		poller.Run(*interval, *retain)
	}()

	metricDefaultInterval := 60

	cpuInterval := getEnvInt("CPU_POLL_INTERVAL", metricDefaultInterval)
	go func() {
		time.Sleep(3 * time.Second)
		RunMetricPoller(store, db, "cpu", cpuInterval, *retain, metrics.ReadCPU)
	}()
	memInterval := getEnvInt("MEMORY_POLL_INTERVAL", metricDefaultInterval)
	go func() {
		time.Sleep(6 * time.Second)
		RunMetricPoller(store, db, "memory", memInterval, *retain, metrics.ReadMemory)
	}()
	swapInterval := getEnvInt("SWAP_POLL_INTERVAL", metricDefaultInterval)
	go func() {
		time.Sleep(9 * time.Second)
		RunMetricPoller(store, db, "swap", swapInterval, *retain, metrics.ReadSwap)
	}()
	diskInterval := getEnvInt("DISK_POLL_INTERVAL", metricDefaultInterval)
	go func() {
		time.Sleep(12 * time.Second)
		RunMetricPoller(store, db, "disk", diskInterval, *retain, metrics.ReadDisk)
	}()
	loadInterval := getEnvInt("LOAD_POLL_INTERVAL", metricDefaultInterval)
	go func() {
		time.Sleep(15 * time.Second)
		RunMetricPoller(store, db, "load", loadInterval, *retain, metrics.ReadLoad)
	}()

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
