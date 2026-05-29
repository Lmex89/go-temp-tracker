package main

import (
	"database/sql"
	"fmt"
	"time"
)

// Spike represents a single temperature reading that exceeds the baseline threshold.
// In Python you'd use a @dataclass or NamedTuple.
type Spike struct {
	Sensor       string
	Timestamp    time.Time // Parsed from DB (assumed UTC)
	TempC        float64
	BaselineMean float64 // The sensor's 30-day average
	Deviation    float64 // How many degrees above baseline (TempC - BaselineMean)
}

// detectSpikes queries the database, computes per-sensor baselines, and returns spike events.
//
// Steps:
// 1. Query temperature readings from baselineSince to now (baseline window).
// 2. Group by sensor and compute mean temperature per sensor.
// 3. Query temperature readings from reportSince to now (report window).
// 4. Filter readings where temp_c > (mean + deviationThreshold).
//
// This is like Python's pandas groupby + mean, then boolean indexing.
func detectSpikes(
	db *sql.DB,
	baselineSince time.Time,
	reportSince time.Time,
	now time.Time,
	deviationThreshold float64,
) ([]Spike, error) {

	// --- Step 1: Compute per-sensor baseline mean ---
	// Query all temperature readings in the baseline window.
	// In Python: df[(df.created_at >= baseline_since) & (df.created_at <= now)]
	baselineRows, err := queryTemperatureRange(db, baselineSince, now)
	if err != nil {
		return nil, fmt.Errorf("baseline query failed: %w", err)
	}

	// Group by sensor and accumulate values — like Python's collections.defaultdict(list).
	sensorTemps := make(map[string][]float64)
	for _, r := range baselineRows {
		sensorTemps[r.Sensor] = append(sensorTemps[r.Sensor], r.TempC)
	}

	// Calculate mean per sensor — like np.mean(values) in Python.
	means := make(map[string]float64)
	for sensor, temps := range sensorTemps {
		if len(temps) == 0 {
			continue
		}
		var sum float64
		for _, t := range temps {
			sum += t
		}
		means[sensor] = sum / float64(len(temps))
	}

	// --- Step 2: Find spikes in the report window ---
	reportRows, err := queryTemperatureRange(db, reportSince, now)
	if err != nil {
		return nil, fmt.Errorf("report window query failed: %w", err)
	}

	var spikes []Spike
	for _, r := range reportRows {
		mean, ok := means[r.Sensor]
		if !ok {
			continue // No baseline for this sensor (shouldn't happen).
		}
		if r.TempC > mean+deviationThreshold {
			spikes = append(spikes, Spike{
				Sensor:       r.Sensor,
				Timestamp:    r.Timestamp,
				TempC:        r.TempC,
				BaselineMean: mean,
				Deviation:    r.TempC - mean,
			})
		}
	}

	return spikes, nil
}

// tempReading is a lightweight struct for raw DB rows.
// The DB stores created_at as UTC text strings.
type tempReading struct {
	Sensor    string
	TempC     float64
	Timestamp time.Time
}

// queryTemperatureRange fetches temperature readings between two UTC timestamps.
// This is like Python: cursor.execute("SELECT ... WHERE created_at >= ? AND ...", (since, until))
func queryTemperatureRange(db *sql.DB, since, until time.Time) ([]tempReading, error) {
	// SQLite datetime format for the query — "2006-01-02 15:04:05" is Go's reference time layout.
	// In Python you'd use strftime("%Y-%m-%d %H:%M:%S").
	layout := "2006-01-02 15:04:05"
	rows, err := db.Query(
		`SELECT sensor, temp_c, created_at FROM readings
		 WHERE metric_type = 'temperature'
		   AND created_at >= ?
		   AND created_at <= ?
		 ORDER BY sensor, created_at ASC`,
		since.Format(layout),
		until.Format(layout),
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close() // Ensure rows are closed — like Python's `with cursor:`.

	var readings []tempReading
	for rows.Next() {
		var r tempReading
		var tsStr string
		if err := rows.Scan(&r.Sensor, &r.TempC, &tsStr); err != nil {
			return nil, err
		}
		// Parse the UTC timestamp from DB.
		// In Python: datetime.strptime(ts_str, "%Y-%m-%d %H:%M:%S").replace(tzinfo=timezone.utc)
		ts, err := time.ParseInLocation(layout, tsStr, time.UTC)
		if err != nil {
			return nil, err
		}
		r.Timestamp = ts
		readings = append(readings, r)
	}

	if err := rows.Err(); err != nil {
		return nil, err
	}

	return readings, nil
}
