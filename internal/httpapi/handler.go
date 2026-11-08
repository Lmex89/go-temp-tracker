package httpapi

import (
	"sensors-temp/internal/timeutil"

	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"sensors-temp/internal/logging"
	"sensors-temp/internal/store"
	"strconv"
	"strings"
)

// Register wires every API route onto mux.
// Python analogy: a Flask Blueprint / Django urls.py -- main only calls this
// one function instead of knowing each handler.
func Register(mux *http.ServeMux, st store.Store, db *sql.DB) {
	mux.HandleFunc("/api/temps", handleTemps(st, db))
	mux.HandleFunc("/api/current", handleCurrent(st, db))

	for _, mt := range []string{"cpu", "memory", "swap", "disk", "load"} {
		mux.HandleFunc("/api/"+mt, handleMetricByType(st, db, mt))
		mux.HandleFunc("/api/current/"+mt, handleCurrentByType(st, db, mt))
	}
}

// handleTemps returns an http.HandlerFunc (a function that handles HTTP requests).
// This is a *closure* -- like a Python nested function that captures store and db from the outer scope.
// In Flask you'd write @app.route("/api/temps") with a view function.
// In Django you'd write a class-based view or a function.
// Here we use a "factory" pattern: handleTemps(st, db) returns the actual handler function.
// Supports ?hours=N (relative) OR ?from=FROM&to=TO (absolute range).
func handleTemps(st store.Store, db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()

		from := strings.TrimSpace(q.Get("from"))
		to := strings.TrimSpace(q.Get("to"))

		var readings []store.Reading

		if from != "" || to != "" {
			if from == "" || to == "" {
				http.Error(w, "from and to must be provided together", http.StatusBadRequest)
				return
			}

			logging.Logger.Info("GET /api/temps range from=%s to=%s", from, to)

			fromTime, err := timeutil.ParseTimestampInput(from)
			if err != nil {
				logging.Logger.Warn("Invalid from timestamp: %s -> %v", from, err)
				http.Error(w, fmt.Sprintf("invalid from timestamp: %v", err), http.StatusBadRequest)
				return
			}

			toTime, err := timeutil.ParseTimestampInput(to)
			if err != nil {
				logging.Logger.Warn("Invalid to timestamp: %s -> %v", to, err)
				http.Error(w, fmt.Sprintf("invalid to timestamp: %v", err), http.StatusBadRequest)
				return
			}

			if fromTime.After(toTime) {
				http.Error(w, "from must be before to", http.StatusBadRequest)
				return
			}

			logging.Logger.Debug("Querying range UTC from=%s to=%s", fromTime.UTC().Format(timeutil.DBLayout), toTime.UTC().Format(timeutil.DBLayout))
			readings, err = st.Query(
				db,
				store.TimeWindow{From: fromTime, To: toTime},
				"temperature",
			)
			if err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
		} else {
			hours := 1
			if h := q.Get("hours"); h != "" {
				if v, err := strconv.Atoi(h); err == nil && v > 0 {
					hours = v
				} else {
					logging.Logger.Warn("Invalid hours parameter: %s", h)
				}
			}
			logging.Logger.Debug("GET /api/temps?hours=%d from %s", hours, r.RemoteAddr)
			var err error
			readings, err = st.Query(db, store.TimeWindow{Hours: hours}, "temperature")
			if err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
		}

		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(readings); err != nil {
			logging.Logger.Error("Failed to encode JSON response: %v", err)
		}

		logging.Logger.Debug("Returned %d temperature reading(s)", len(readings))
	}
}

// handleCurrent returns the latest temperature reading per sensor as JSON.
func handleCurrent(st store.Store, db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		logging.Logger.Debug("GET /api/current from %s", r.RemoteAddr)

		readings, err := st.QueryLatest(db, "temperature")
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(readings); err != nil {
			logging.Logger.Error("Failed to encode JSON response: %v", err)
		}

		logging.Logger.Debug("Returned %d current temperature reading(s)", len(readings))
	}
}

// handleMetricByType returns an HTTP handler for querying historical data of a specific metric type.
// This is a generic factory -- used for cpu, memory, disk, load, swap.
// In Python: def make_metric_handler(metric_type): return lambda request: ...
// For CPU metrics, supports ?view=total|per-core|both to filter by sensor type.
func handleMetricByType(st store.Store, db *sql.DB, metricType string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		from := strings.TrimSpace(q.Get("from"))
		to := strings.TrimSpace(q.Get("to"))

		var readings []store.Reading

		if from != "" || to != "" {
			if from == "" || to == "" {
				http.Error(w, "from and to must be provided together", http.StatusBadRequest)
				return
			}

			fromTime, err := timeutil.ParseTimestampInput(from)
			if err != nil {
				http.Error(w, fmt.Sprintf("invalid from timestamp: %v", err), http.StatusBadRequest)
				return
			}

			toTime, err := timeutil.ParseTimestampInput(to)
			if err != nil {
				http.Error(w, fmt.Sprintf("invalid to timestamp: %v", err), http.StatusBadRequest)
				return
			}

			if fromTime.After(toTime) {
				http.Error(w, "from must be before to", http.StatusBadRequest)
				return
			}

			readings, err = st.Query(
				db,
				store.TimeWindow{From: fromTime, To: toTime},
				metricType,
			)
			if err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
		} else {
			hours := 1
			if h := q.Get("hours"); h != "" {
				if v, err := strconv.Atoi(h); err == nil && v > 0 {
					hours = v
				}
			}
			var err error
			readings, err = st.Query(db, store.TimeWindow{Hours: hours}, metricType)
			if err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
		}

		// For CPU metrics, apply view filter if specified.
		// view=total: only cpu/total sensor
		// view=per-core: only cpu/Core N sensors
		// view=both or invalid/missing: all sensors (backward compat)
		if metricType == "cpu" {
			view := q.Get("view")
			readings = filterCPUReadingsByView(readings, view)
		}

		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(readings); err != nil {
			logging.Logger.Error("Failed to encode JSON response: %v", err)
		}
	}
}

// filterCPUReadingsByView filters CPU readings based on the view parameter.
// This is like Python's list comprehension with a conditional filter.
// view=total keeps only cpu/total, view=per-core keeps only cpu/Core N,
// anything else (including empty/invalid) returns all readings unchanged.
func filterCPUReadingsByView(readings []store.Reading, view string) []store.Reading {
	switch view {
	case "total":
		// Keep only the total sensor.
		filtered := make([]store.Reading, 0, 1)
		for _, r := range readings {
			if r.Sensor == "cpu/total" {
				filtered = append(filtered, r)
			}
		}
		return filtered
	case "per-core":
		// Keep only per-core sensors (cpu/Core 0, cpu/Core 1, etc.).
		filtered := make([]store.Reading, 0, len(readings))
		for _, r := range readings {
			if strings.HasPrefix(r.Sensor, "cpu/Core ") {
				filtered = append(filtered, r)
			}
		}
		return filtered
	default:
		// view=both, empty, or invalid: return all readings unchanged.
		return readings
	}
}

// handleCurrentByType returns an HTTP handler for getting the latest reading of a specific metric type.
func handleCurrentByType(st store.Store, db *sql.DB, metricType string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		readings, err := st.QueryLatest(db, metricType)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(readings); err != nil {
			logging.Logger.Error("Failed to encode JSON response: %v", err)
		}
	}
}
