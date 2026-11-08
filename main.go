package main

import (
	"flag"
	"fmt"
	"net/http"
	"os"
	"sensors-temp/internal/httpapi"
	"sensors-temp/internal/logging"
	"sensors-temp/internal/metrics"
	"sensors-temp/internal/poller"
	"sensors-temp/internal/sensor"
	"sensors-temp/internal/store"
	"strconv"
	"time"
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
		logging.Logger.Warn("Invalid value for %s=%q, using default %d", key, val, defaultVal)
		return defaultVal
	}
	return parsed
}

// newStore returns a Store implementation based on the requested driver.
// "sqlite" (or empty string) keeps the existing SQLite behavior; "postgres"
// connects to PostgreSQL via the pgx driver. This is a tiny factory function --
// like Python's if driver == "sqlite": return SQLiteStore() else: return PostgresStore().
func newStore(driver string, sqlitePath string) store.Store {
	switch driver {
	case "postgres":
		logging.Logger.Info("Using PostgreSQL store (DATABASE_URL=%s)", os.Getenv("DATABASE_URL"))
		return store.NewPostgresStore()
	case "sqlite", "":
		logging.Logger.Info("Using SQLite store")
		return store.NewSQLiteStore(sqlitePath)
	default:
		logging.Logger.Warn("Unknown db-driver %q, falling back to SQLite", driver)
		return store.NewSQLiteStore(sqlitePath)
	}
}

// main is the entry point -- analogous to Python's if __name__ == "__main__": main()
// Go's main() takes no args -- use os.Args or the flag package instead.
func main() {
	port := flag.Int("port", 8080, "HTTP server port")
	interval := flag.Int("interval", 60, "Temperature polling interval in seconds")
	retain := flag.Int("retain", 8760, "Delete readings older than N hours for ALL metric types (default: 12 months)")
	dbDriver := flag.String("db-driver", os.Getenv("DB_DRIVER"), "Database driver: sqlite or postgres (also env DB_DRIVER)")
	flag.Parse()

	logging.Logger.Info("Starting System Monitor")
	logging.Logger.Debug("Config: port=%d, interval=%ds, retain=%dh", *port, *interval, *retain)

	// Choose the database backend. Defaults to SQLite for backward compatibility.
	// DB_DRIVER env var or -db-driver flag selects postgres; DATABASE_URL configures
	// the Postgres connection (defaults to the project's docker-compose.yml settings).
	sensor := sensor.NewLinuxThermalSensor()
	st := newStore(*dbDriver, "temps.db")
	db := st.InitDB()
	defer func() {
		if err := db.Close(); err != nil {
			logging.Logger.Error("Failed to close database: %v", err)
		} else {
			logging.Logger.Info("Database connection closed")
		}
	}()

	// SystemMetrics for CPU, memory, swap, disk, load (gopsutil-based).
	sysMetrics := metrics.NewSystemMetrics()

	// SQLite only allows one writer at a time (even in WAL mode). A single connection
	// makes Go's database/sql queue all operations, eliminating lock contention.
	// PostgreSQL is a real server and can handle multiple concurrent connections, so
	// leave its pool at the default size.
	if _, ok := st.(*store.SQLiteStore); ok {
		db.SetMaxOpenConns(1)
		db.SetMaxIdleConns(1)
	}

	// Stagger pollers by 3s each so they don't all hit the DB simultaneously.
	// Without staggering, 6 goroutines sharing 3 conns create a thundering herd
	// against SQLite's internal lock, causing 5s busy_timeout waits on every cycle.
	// In Python: time.sleep(offset) before entering the while loop.
	tempPoller := poller.New(sensor, st, db)
	go func() {
		time.Sleep(0 * time.Second)
		tempPoller.Run(*interval, *retain)
	}()

	metricDefaultInterval := 60

	cpuInterval := getEnvInt("CPU_POLL_INTERVAL", metricDefaultInterval)
	go func() {
		time.Sleep(3 * time.Second)
		poller.RunMetric(st, db, "cpu", cpuInterval, *retain, sysMetrics.ReadCPU)
	}()
	memInterval := getEnvInt("MEMORY_POLL_INTERVAL", metricDefaultInterval)
	go func() {
		time.Sleep(6 * time.Second)
		poller.RunMetric(st, db, "memory", memInterval, *retain, sysMetrics.ReadMemory)
	}()
	swapInterval := getEnvInt("SWAP_POLL_INTERVAL", metricDefaultInterval)
	go func() {
		time.Sleep(9 * time.Second)
		poller.RunMetric(st, db, "swap", swapInterval, *retain, sysMetrics.ReadSwap)
	}()
	diskInterval := getEnvInt("DISK_POLL_INTERVAL", metricDefaultInterval)
	go func() {
		time.Sleep(12 * time.Second)
		poller.RunMetric(st, db, "disk", diskInterval, *retain, sysMetrics.ReadDisk)
	}()
	loadInterval := getEnvInt("LOAD_POLL_INTERVAL", metricDefaultInterval)
	go func() {
		time.Sleep(15 * time.Second)
		poller.RunMetric(st, db, "load", loadInterval, *retain, sysMetrics.ReadLoad)
	}()

	// Setting up HTTP routes -- ServeMux is like Flask's app or Django's urlpatterns.
	mux := http.NewServeMux()

	// All /api/* routes (temps, cpu, memory, swap, disk, load, current/*).
	httpapi.Register(mux, st, db)

	mux.Handle("/", http.FileServer(http.Dir("static")))

	addr := fmt.Sprintf(":%d", *port)
	logging.Logger.Info("HTTP server listening on %s", addr)
	logging.Logger.Info("Open http://localhost%s in your browser", addr)

	if err := http.ListenAndServe(addr, mux); err != nil {
		logging.Logger.Fatal("HTTP server error: %v", err)
	}
}
