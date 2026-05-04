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
	// Returns a closure function — this function "captures" store and db from the outer scope.
	// In Python you'd write a nested function that uses nonlocal variables.
	return func(w http.ResponseWriter, r *http.Request) {
		// r.URL.Query() parses the query string (?hours=6 or ?from=...&to=...) into a map.
		// Like Python's urllib.parse.parse_qs(request.query_string).
		q := r.URL.Query()

		// Get "from" and "to" query parameters — like request.args.get("from") in Flask.
		// strings.TrimSpace removes leading/trailing whitespace — like Python's .strip().
		from := strings.TrimSpace(q.Get("from"))
		to := strings.TrimSpace(q.Get("to"))

		var readings []Reading

		// Check if user provided absolute date range (?from=...&to=...).
		// In Go, we check both because they must come together (unlike Python where you might use **kwargs).
		if from != "" || to != "" {
			// Validate: both must be present — can't have only one.
			if from == "" || to == "" {
				http.Error(w, "from and to must be provided together", http.StatusBadRequest)
				return
			}

			// Log the raw timestamps received from frontend (for debugging).
			Logger.Info("GET /api/temps range from=%s to=%s", from, to)

			// ParseTimestampInput converts various timestamp formats to UTC time.Time.
			// It returns (time.Time, error) — Go's way of handling errors instead of exceptions.
			// You MUST check err != nil before using the time value.
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

			// Validate: from must be before to (can't query backwards in time).
			if fromTime.After(toTime) {
				http.Error(w, "from must be before to", http.StatusBadRequest)
				return
			}

			// Convert to UTC and format for SQLite query.
			// .UTC() ensures we're querying against UTC timestamps stored in the database.
			Logger.Debug("Querying range UTC from=%s to=%s", fromTime.UTC().Format(dbTimeLayout), toTime.UTC().Format(dbTimeLayout))
			readings = store.QueryByRange(
				db,
				fromTime.UTC().Format(dbTimeLayout),
				toTime.UTC().Format(dbTimeLayout),
			)
		} else {
			// Fallback to relative time range (?hours=N) — e.g., "last 6 hours".
			hours := 1
			if h := q.Get("hours"); h != "" {
				// strconv.Atoi parses string to int — like Python's int().
				// Returns (int, error) — again, Go's error handling pattern.
				if v, err := strconv.Atoi(h); err == nil && v > 0 {
					hours = v
				} else {
					Logger.Warn("Invalid hours parameter: %s", h)
				}
			}
			Logger.Debug("GET /api/temps?hours=%d from %s", hours, r.RemoteAddr)
			readings = store.Query(db, hours)
		}

		// Set response header to JSON — like Flask's jsonify() or Django's JsonResponse.
		w.Header().Set("Content-Type", "application/json")
		// json.NewEncoder(w).Encode() serializes the slice to JSON and writes to response.
		// Like Python's json.dumps(readings) then return Response(json_string, mimetype='application/json').
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
