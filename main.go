package main

import (
	"flag"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"sensors-temp/internal/alerts"
	"sensors-temp/internal/httpapi"
	"sensors-temp/internal/logging"
	"sensors-temp/internal/metrics"
	"sensors-temp/internal/poller"
	"sensors-temp/internal/sensor"
	"sensors-temp/internal/store"
	"strconv"
	"strings"
	"sync/atomic"
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

func pollingIntervals(alertsEnabled bool, temperature, cpu int) (int, int) {
	if alertsEnabled {
		return 5, 5
	}
	return temperature, cpu
}

type staticFiles struct {
	root       *os.Root
	configPath string
	configInfo os.FileInfo
	changed    *atomic.Bool
	watch      *configWatch
	initial    map[string]os.FileInfo
}

func (s staticFiles) configValid() bool {
	if s.configInfo == nil {
		return true
	}
	if s.watch == nil || s.watch.changed() {
		s.changed.Store(true)
	}
	current, err := os.Stat(s.configPath)
	if err != nil {
		s.changed.Store(true)
		return false
	}
	if !os.SameFile(s.configInfo, current) || !sameStaticFile(s.configInfo, current) || !singleLink(current) {
		s.changed.Store(true)
	}
	return true
}

func (s staticFiles) Open(name string) (fs.File, error) {
	if !fs.ValidPath(name) {
		return nil, fs.ErrNotExist
	}
	base, err := filepath.EvalSymlinks(s.root.Name())
	if err != nil {
		return nil, fs.ErrNotExist
	}
	target, err := filepath.EvalSymlinks(filepath.Join(base, name))
	if err != nil {
		return nil, fs.ErrNotExist
	}
	rel, err := filepath.Rel(base, target)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return nil, fs.ErrNotExist
	}
	f, err := s.root.Open(rel)
	if err != nil {
		return nil, fs.ErrNotExist
	}
	info, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, fs.ErrNotExist
	}
	if s.configInfo != nil && info.Mode().IsRegular() {
		original, exists := s.initial[rel]
		current, statErr := os.Stat(s.configPath)
		// shortcut: new or replaced assets need a restart after config changes, refresh the trusted set on restart.
		if !s.configValid() || statErr != nil || !singleLink(info) || os.SameFile(info, s.configInfo) || os.SameFile(info, current) || (s.changed.Load() && (!exists || !os.SameFile(original, info))) {
			f.Close()
			return nil, fs.ErrNotExist
		}
	}
	return f, nil
}

func staticHandler(publicDir, configPath string, configInfo os.FileInfo) http.Handler {
	changed := &atomic.Bool{}
	initial := make(map[string]os.FileInfo)
	var watch *configWatch
	if configInfo != nil {
		var err error
		watch, err = newConfigWatch(configPath)
		if err != nil {
			changed.Store(true)
		}
		staticFiles{configPath: configPath, configInfo: configInfo, changed: changed, watch: watch}.configValid()
		base, err := filepath.EvalSymlinks(publicDir)
		if err == nil {
			walkErr := filepath.WalkDir(base, func(path string, entry fs.DirEntry, walkErr error) error {
				if walkErr != nil {
					logging.Logger.Debug("Skipping inaccessible static entry")
					return nil
				}
				if entry.Type()&os.ModeSymlink != 0 {
					return nil
				}
				info, err := entry.Info()
				if err != nil {
					return nil
				}
				if info.Mode().IsRegular() && singleLink(info) && !os.SameFile(info, configInfo) {
					rel, err := filepath.Rel(base, path)
					if err != nil {
						return nil
					}
					initial[rel] = info
				}
				return nil
			})
			if walkErr != nil {
				logging.Logger.Warn("Cannot inspect all static entries")
			}
		}
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		files := staticFiles{configPath: configPath, configInfo: configInfo, changed: changed, watch: watch, initial: initial}
		if !files.configValid() {
			http.NotFound(w, r)
			return
		}
		root, err := os.OpenRoot(publicDir)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		defer root.Close()
		files.root = root
		http.FileServerFS(files).ServeHTTP(w, r)
	})
}

// newStore returns a Store implementation based on the requested driver.
// NOTE: unlike store.Open (used by spike-report, where "" means postgres), an
// empty driver means SQLite here, as in the original service.
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
	alertConfig := flag.String("alerts-config", "", "Private alerts configuration file")
	flag.Parse()

	logging.Logger.Info("Starting System Monitor")
	logging.Logger.Debug("Config: port=%d, interval=%ds, retain=%dh", *port, *interval, *retain)
	alertToken := os.Getenv("TELEGRAM_BOT_TOKEN")
	brevoKey := os.Getenv("BREVO_API_KEY")
	alertCfg, err := alerts.LoadConfig(*alertConfig, "static", alertToken, brevoKey)
	if err != nil {
		logging.Logger.Fatal("Invalid alerts configuration: %v", err)
	}
	var evaluator *alerts.Evaluator
	if alertCfg.Enabled {
		host, err := os.Hostname()
		if err != nil {
			host = "unknown"
		}
		delivery := alerts.NewTelegram(alertToken, alertCfg.ChatID, host)
		email := alerts.NewBrevo(brevoKey, alertCfg.BrevoSenderEmail, alertCfg.BrevoSenderName, alertCfg.BrevoRecipientEmail, host)
		evaluator = alerts.NewEvaluator(alertCfg.Rules, func(event alerts.Event) { delivery.Enqueue(event); email.Enqueue(event) })
		logging.Logger.Info("Alerts enabled: target interval 5s overrides -interval and CPU_POLL_INTERVAL")
	}

	// Choose the database backend. Defaults to SQLite for backward compatibility.
	// DB_DRIVER env var or -db-driver flag selects postgres; DATABASE_URL configures
	// the Postgres connection (defaults to the project's docker-compose.yml settings).
	reader := sensor.NewLinuxThermalSensor()
	st := newStore(*dbDriver, store.DefaultSQLitePath)
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
	if alertCfg.Enabled {
		sysMetrics.SuppressRepeatedCPUWarnings()
	}

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
	metricDefaultInterval := 60
	tempInterval, cpuInterval := pollingIntervals(alertCfg.Enabled, *interval, getEnvInt("CPU_POLL_INTERVAL", metricDefaultInterval))
	tempPoller := poller.New(reader, st, db)
	go func() {
		time.Sleep(0 * time.Second)
		if evaluator == nil {
			tempPoller.Run(tempInterval, *retain)
		} else {
			tempPoller.RunObserved(tempInterval, *retain, func(points []metrics.MetricPoint) { evaluator.Observe("temperature", time.Now(), points) })
		}
	}()

	go func() {
		time.Sleep(3 * time.Second)
		if evaluator == nil {
			poller.RunMetric(st, db, "cpu", cpuInterval, *retain, sysMetrics.ReadCPU)
		} else {
			poller.RunMetricObserved(st, db, "cpu", cpuInterval, *retain, sysMetrics.ReadCPU, func(points []metrics.MetricPoint) { evaluator.Observe("cpu", time.Now(), points) })
		}
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

	mux.Handle("/", staticHandler("static", *alertConfig, alertCfg.Source))

	addr := fmt.Sprintf(":%d", *port)
	logging.Logger.Info("HTTP server listening on %s", addr)
	logging.Logger.Info("Open http://localhost%s in your browser", addr)

	if err := http.ListenAndServe(addr, mux); err != nil {
		logging.Logger.Fatal("HTTP server error: %v", err)
	}
}
