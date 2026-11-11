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
// canonical backend. Like Python's args.driver if set else os.getenv(...).
func resolveDBDriver(flagDriver, envDriver string, flagWasSet bool) string {
	driver := envDriver
	if flagWasSet {
		driver = flagDriver
	}
	switch driver {
	case "postgres":
		return "postgres"
	case "", "sqlite":
		return "sqlite"
	default:
		logging.Logger.Warn("Unknown database driver, using SQLite")
		return "sqlite"
	}
}

// newStore builds the backend selected by resolveDBDriver. Python analogy:
// an if/elif factory that uses an already-resolved configuration value.
func newStore(driver string, sqlitePath string) store.Store {
	switch driver {
	case "postgres":
		logging.Logger.Info("Using PostgreSQL database")
		return store.NewPostgresStore()
	case "sqlite":
		logging.Logger.Info("Using SQLite database")
		return store.NewSQLiteStore(sqlitePath)
	default:
		return nil
	}
}

// openDatabase opens and configures the effective backend before polling starts.
// It preserves SQLite's single connection without changing PostgreSQL's pool.
func openDatabase(driver, sqlitePath string) (store.Store, *sql.DB) {
	st := newStore(driver, sqlitePath)
	if st == nil {
		return nil, nil
	}
	db := st.InitDB()
	if db != nil && driver == "sqlite" {
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

	// Resolve the backend once before opening it. DATABASE_URL configures PostgreSQL
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
