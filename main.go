package main

import (
	"database/sql"
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

// resolveDBDriver applies the CLI-over-environment precedence and returns one
// canonical database engine for the whole run. flagWasSet distinguishes
// "-db-driver=" (explicit empty, still wins) from "flag not given".
// Python analogy: args.driver if args.driver is not None else os.getenv(...).
func resolveDBDriver(flagDriver, envDriver string, flagWasSet bool) string {
	requested := envDriver
	if flagWasSet {
		requested = flagDriver
	}
	driver, known := store.ResolveDriver(requested, store.DriverSQLite)
	if !known {
		logging.Logger.Warn("Unknown database driver, using SQLite")
	}
	return driver
}

// openDatabase builds the Store for the already-resolved driver, opens it and
// tunes the pool before polling starts. Like Python's
// store = PostgresStore() if driver == "postgres" else SQLiteStore(path).
// It returns a nil *sql.DB when initialization fails (already logged).
func openDatabase(driver, sqlitePath string) (store.Store, *sql.DB) {
	var st store.Store
	if driver == store.DriverPostgres {
		logging.Logger.Info("Using PostgreSQL database")
		st = store.NewPostgresStore()
	} else {
		logging.Logger.Info("Using SQLite database")
		st = store.NewSQLiteStore(sqlitePath)
	}
	db := st.InitDB()

	// SQLite only allows one writer at a time (even in WAL mode). A single connection
	// makes Go's database/sql queue all operations, eliminating lock contention --
	// like sharing one sqlite3.Connection behind a lock in Python.
	// PostgreSQL is a real server and can handle multiple concurrent connections, so
	// leave its pool at the default size.
	if db != nil && driver == store.DriverSQLite {
		db.SetMaxOpenConns(1)
		db.SetMaxIdleConns(1)
	}
	return st, db
}

// main is the entry point -- analogous to Python's if __name__ == "__main__": main()
// Go's main() takes no args -- use os.Args or the flag package instead.
func main() {
	port := flag.Int("port", 8080, "HTTP server port")
	interval := flag.Int("interval", 60, "Temperature polling interval in seconds")
	retain := flag.Int("retain", 8760, "Delete readings older than N hours for ALL metric types (default: 12 months)")
	dbDriver := flag.String("db-driver", "", "Database driver: sqlite or postgres (overrides DB_DRIVER; empty defaults to sqlite)")
	flag.Parse()
	dbDriverFlagSet := false
	flag.Visit(func(f *flag.Flag) {
		if f.Name == "db-driver" {
			dbDriverFlagSet = true
		}
	})

	logging.Logger.Info("Starting System Monitor")
	logging.Logger.Debug("Config: port=%d, interval=%ds, retain=%dh", *port, *interval, *retain)

	// Resolve the database engine once before opening it. DATABASE_URL configures PostgreSQL
	// (defaults to the project's docker-compose.yml settings).
	effectiveDBDriver := resolveDBDriver(*dbDriver, os.Getenv("DB_DRIVER"), dbDriverFlagSet)
	reader := sensor.NewLinuxThermalSensor()
	st, db := openDatabase(effectiveDBDriver, store.DefaultSQLitePath)
	if db == nil {
		logging.Logger.Fatal("Unable to start: %s database initialization failed", effectiveDBDriver)
		return
	}
	defer func() {
		if err := db.Close(); err != nil {
			logging.Logger.Error("Failed to close database: %v", err)
		} else {
			logging.Logger.Info("Database connection closed")
		}
	}()

	// SystemMetrics for CPU, memory, swap, disk, load (gopsutil-based).
	sysMetrics := metrics.NewSystemMetrics()

	// Stagger pollers by 3s each so they don't all hit the DB simultaneously.
	// Without staggering, 6 goroutines sharing 3 conns create a thundering herd
	// against SQLite's internal lock, causing 5s busy_timeout waits on every cycle.
	// In Python: time.sleep(offset) before entering the while loop.
	tempPoller := poller.New(reader, st, db)
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
