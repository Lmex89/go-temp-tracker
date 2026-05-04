package main

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"strconv"
)

func handleTemps(store Store, db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		hours := 1
		if h := r.URL.Query().Get("hours"); h != "" {
			if v, err := strconv.Atoi(h); err == nil && v > 0 {
				hours = v
			} else {
				Logger.Warn("Invalid hours parameter: %s", h)
			}
		}

		Logger.Debug("GET /api/temps?hours=%d from %s", hours, r.RemoteAddr)

		readings := store.Query(db, hours)

		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(readings); err != nil {
			Logger.Error("Failed to encode JSON response: %v", err)
		}

		Logger.Debug("Returned %d reading(s) for last %dh", len(readings), hours)
	}
}

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
