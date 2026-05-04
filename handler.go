package main

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"strconv"
)

// handleTemps returns an http.HandlerFunc (a function that handles HTTP requests).
// This is a *closure* — like a Python nested function that captures store and db from the outer scope.
// In Flask you'd write @app.route("/api/temps") with a view function.
// In Django you'd write a class-based view or a function.
// Here we use a "factory" pattern: handleTemps(store, db) returns the actual handler function.
func handleTemps(store Store, db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// Parse the ?hours=N query parameter — like request.args.get("hours") in Flask.
		hours := 1
		if h := r.URL.Query().Get("hours"); h != "" {
			// strconv.Atoi converts string to int — like Python's int().
			if v, err := strconv.Atoi(h); err == nil && v > 0 {
				hours = v
			} else {
				Logger.Warn("Invalid hours parameter: %s", h)
			}
		}

		Logger.Debug("GET /api/temps?hours=%d from %s", hours, r.RemoteAddr)

		readings := store.Query(db, hours)

		// Set Content-Type header — like Flask's Response(content_type="application/json").
		w.Header().Set("Content-Type", "application/json")
		// json.NewEncoder(w).Encode writes JSON directly to the response — like json.dump() in Python.
		if err := json.NewEncoder(w).Encode(readings); err != nil {
			Logger.Error("Failed to encode JSON response: %v", err)
		}

		Logger.Debug("Returned %d reading(s) for last %dh", len(readings), hours)
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
