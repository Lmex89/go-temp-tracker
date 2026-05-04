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
// This is a *closure* — like a Python nested function that captures store and db from the outer scope.
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

		Logger.Debug("Returned %d reading(s)", len(readings))
	}
}

// handleCurrent returns the latest reading per sensor as JSON.
// Same pattern as handleTemps but uses QueryLatestPerSensor instead.
func handleCurrent(store Store, db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		Logger.Debug("GET /api/current from %s", r.RemoteAddr)

		readings := store.QueryLatestPerSensor(db)

		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(readings); err != nil {
			Logger.Error("Failed to encode JSON response: %v", err)
		}

		Logger.Debug("Returned %d current reading(s)", len(readings))
	}
}
