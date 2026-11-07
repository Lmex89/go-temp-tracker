package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
)

// handleTemps returns an http.HandlerFunc (a function that handles HTTP requests).
// This is a *closure* -- like a Python nested function that captures store and db from the outer scope.
// In Flask you'd write @app.route("/api/temps") with a view function.
// In Django you'd write a class-based view or a function.
// Here we use a "factory" pattern: handleTemps(store, db) returns the actual handler function.
// Supports ?hours=N (relative) OR ?from=FROM&to=TO (absolute range).
func handleTemps(store Store, db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()

		from := strings.TrimSpace(q.Get("from"))
		to := strings.TrimSpace(q.Get("to"))

		var readings []Reading

		if from != "" || to != "" {
			if from == "" || to == "" {
				http.Error(w, "from and to must be provided together", http.StatusBadRequest)
				return
			}

			Logger.Info("GET /api/temps range from=%s to=%s", from, to)

			fromTime, err := ParseTimestampInput(from)
			if err != nil {
				Logger.Warn("Invalid from timestamp: %s -> %v", from, err)
				http.Error(w, fmt.Sprintf("invalid from timestamp: %v", err), http.StatusBadRequest)
				return
			}

			toTime, err := ParseTimestampInput(to)
			if err != nil {
				Logger.Warn("Invalid to timestamp: %s -> %v", to, err)
				http.Error(w, fmt.Sprintf("invalid to timestamp: %v", err), http.StatusBadRequest)
				return
			}

			if fromTime.After(toTime) {
				http.Error(w, "from must be before to", http.StatusBadRequest)
				return
			}

			Logger.Debug("Querying range UTC from=%s to=%s", fromTime.UTC().Format(dbTimeLayout), toTime.UTC().Format(dbTimeLayout))
			readings = store.QueryByRange(
				db,
				fromTime.UTC().Format(dbTimeLayout),
				toTime.UTC().Format(dbTimeLayout),
			)
		} else {
			hours := 1
			if h := q.Get("hours"); h != "" {
				if v, err := strconv.Atoi(h); err == nil && v > 0 {
					hours = v
				} else {
					Logger.Warn("Invalid hours parameter: %s", h)
				}
			}
			Logger.Debug("GET /api/temps?hours=%d from %s", hours, r.RemoteAddr)
			readings = store.Query(db, hours)
		}

		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(readings); err != nil {
			Logger.Error("Failed to encode JSON response: %v", err)
		}

		Logger.Debug("Returned %d temperature reading(s)", len(readings))
	}
}

// handleCurrent returns the latest temperature reading per sensor as JSON.
func handleCurrent(store Store, db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		Logger.Debug("GET /api/current from %s", r.RemoteAddr)

		readings := store.QueryLatestPerSensor(db)

		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(readings); err != nil {
			Logger.Error("Failed to encode JSON response: %v", err)
		}

		Logger.Debug("Returned %d current temperature reading(s)", len(readings))
	}
}

// handleMetricByType returns an HTTP handler for querying historical data of a specific metric type.
// This is a generic factory -- used for cpu, memory, disk, load, swap.
// In Python: def make_metric_handler(metric_type): return lambda request: ...
// For CPU metrics, supports ?view=total|per-core|both to filter by sensor type.
func handleMetricByType(store Store, db *sql.DB, metricType string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		from := strings.TrimSpace(q.Get("from"))
		to := strings.TrimSpace(q.Get("to"))

		var readings []Reading

		if from != "" || to != "" {
			if from == "" || to == "" {
				http.Error(w, "from and to must be provided together", http.StatusBadRequest)
				return
			}

			fromTime, err := ParseTimestampInput(from)
			if err != nil {
				http.Error(w, fmt.Sprintf("invalid from timestamp: %v", err), http.StatusBadRequest)
				return
			}

			toTime, err := ParseTimestampInput(to)
			if err != nil {
				http.Error(w, fmt.Sprintf("invalid to timestamp: %v", err), http.StatusBadRequest)
				return
			}

			if fromTime.After(toTime) {
				http.Error(w, "from must be before to", http.StatusBadRequest)
				return
			}

			readings = store.QueryByRangeAndType(
				db,
				fromTime.UTC().Format(dbTimeLayout),
				toTime.UTC().Format(dbTimeLayout),
				metricType,
			)
		} else {
			hours := 1
			if h := q.Get("hours"); h != "" {
				if v, err := strconv.Atoi(h); err == nil && v > 0 {
					hours = v
				}
			}
			readings = store.QueryByType(db, metricType, hours)
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
			Logger.Error("Failed to encode JSON response: %v", err)
		}
	}
}

// filterCPUReadingsByView filters CPU readings based on the view parameter.
// This is like Python's list comprehension with a conditional filter.
// view=total keeps only cpu/total, view=per-core keeps only cpu/Core N,
// anything else (including empty/invalid) returns all readings unchanged.
func filterCPUReadingsByView(readings []Reading, view string) []Reading {
	switch view {
	case "total":
		// Keep only the total sensor.
		filtered := make([]Reading, 0, 1)
		for _, r := range readings {
			if r.Sensor == "cpu/total" {
				filtered = append(filtered, r)
			}
		}
		return filtered
	case "per-core":
		// Keep only per-core sensors (cpu/Core 0, cpu/Core 1, etc.).
		filtered := make([]Reading, 0, len(readings))
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
func handleCurrentByType(store Store, db *sql.DB, metricType string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		readings := store.QueryLatestByType(db, metricType)

		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(readings); err != nil {
			Logger.Error("Failed to encode JSON response: %v", err)
		}
	}
}
