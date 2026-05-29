package main

import (
	"database/sql"
	"fmt"
	"time"
)

// ReportRow represents one spike with correlated system metrics.
// In Python this would be a @dataclass or a Pandas DataFrame row.
type ReportRow struct {
	Sensor       string  `json:"sensor"`
	Timestamp    string  `json:"timestamp"`     // Local time string (America/Merida)
	TempC        float64 `json:"temp_c"`        // Temperature in Celsius
	AboveMean    float64 `json:"above_mean"`    // Deviation from baseline
	CPUPercent   float64 `json:"cpu_percent"`   // CPU total % at nearest timestamp
	Load1Min     float64 `json:"load_1min"`     // System load 1m average
	Load5Min     float64 `json:"load_5min"`     // System load 5m average
}

// Report holds all metadata and rows for formatting.
type Report struct {
	Days         int         `json:"days"`
	BaselineDays int         `json:"baseline_days"`
	Deviation    float64     `json:"deviation_threshold"`
	GeneratedAt  string      `json:"generated_at"`
	SpikeCount   int         `json:"spike_count"`
	SensorCount  int         `json:"sensor_count"`
	Rows         []ReportRow `json:"rows"`
}

// correlateMetrics finds the nearest CPU and load readings for each spike.
// It searches within a +/- 60 second window around the spike timestamp.
//
// In Python terms:
//   for spike in spikes:
//       cpu = find_nearest(cpu_df, spike.timestamp, window=60)
//       load = find_nearest(load_df, spike.timestamp, window=60)
func correlateMetrics(db *sql.DB, spikes []Spike, loc *time.Location) ([]ReportRow, error) {
	if len(spikes) == 0 {
		return nil, nil
	}

	// Find the time range covering all spikes so we can batch-query CPU/load.
	// In Python: min(s.Timestamp for s in spikes)
	var minTime, maxTime time.Time
	for i, s := range spikes {
		if i == 0 || s.Timestamp.Before(minTime) {
			minTime = s.Timestamp
		}
		if i == 0 || s.Timestamp.After(maxTime) {
			maxTime = s.Timestamp
		}
	}

	// Query all CPU and load readings in the spike window (with 60s padding).
	// This avoids N+1 queries — like doing a JOIN or batch fetch in SQLAlchemy.
	padding := time.Minute
	cpuReadings, err := queryMetricRange(db, "cpu", minTime.Add(-padding), maxTime.Add(padding))
	if err != nil {
		return nil, fmt.Errorf("CPU query failed: %w", err)
	}

	loadReadings, err := queryMetricRange(db, "load", minTime.Add(-padding), maxTime.Add(padding))
	if err != nil {
		return nil, fmt.Errorf("load query failed: %w", err)
	}

	// Build a lookup map for CPU total by timestamp string.
	// In Python: {row.timestamp: row.value for row in cpu_readings if row.sensor == "cpu/total"}
	cpuByTime := make(map[string]float64)
	for _, r := range cpuReadings {
		if r.Sensor == "cpu/total" {
			cpuByTime[r.Timestamp.Format(time.RFC3339)] = r.Value
		}
	}

	// Build lookup maps for load 1m and 5m by timestamp.
	load1mByTime := make(map[string]float64)
	load5mByTime := make(map[string]float64)
	for _, r := range loadReadings {
		key := r.Timestamp.Format(time.RFC3339)
		if r.Sensor == "load/1min" {
			load1mByTime[key] = r.Value
		} else if r.Sensor == "load/5min" {
			load5mByTime[key] = r.Value
		}
	}

	// For each spike, find the nearest timestamp within 60s.
	var rows []ReportRow
	for _, spike := range spikes {
		cpuVal := findNearestValue(spike.Timestamp, cpuByTime)
		load1m := findNearestValue(spike.Timestamp, load1mByTime)
		load5m := findNearestValue(spike.Timestamp, load5mByTime)

		rows = append(rows, ReportRow{
			Sensor:     spike.Sensor,
			Timestamp:  spike.Timestamp.In(loc).Format("2006-01-02 15:04:05"),
			TempC:      round1(spike.TempC),
			AboveMean:  round1(spike.Deviation),
			CPUPercent: round1(cpuVal),
			Load1Min:   round1(load1m),
			Load5Min:   round1(load5m),
		})
	}

	return rows, nil
}

// metricReading is a generic struct for any metric row from the DB.
type metricReading struct {
	Sensor    string
	Value     float64
	Timestamp time.Time
}

// queryMetricRange fetches all readings for a specific metric_type within a UTC time range.
// Like Python: df[(df.metric_type == metric_type) & (df.created_at >= since) & (df.created_at <= until)]
func queryMetricRange(db *sql.DB, metricType string, since, until time.Time) ([]metricReading, error) {
	layout := "2006-01-02 15:04:05"
	rows, err := db.Query(
		`SELECT sensor, temp_c, created_at FROM readings
		 WHERE metric_type = ?
		   AND created_at >= ?
		   AND created_at <= ?
		 ORDER BY created_at ASC`,
		metricType,
		since.Format(layout),
		until.Format(layout),
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var readings []metricReading
	for rows.Next() {
		var r metricReading
		var tsStr string
		if err := rows.Scan(&r.Sensor, &r.Value, &tsStr); err != nil {
			return nil, err
		}
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

// findNearestValue searches a map of RFC3339 timestamp strings to float values,
// returning the value whose timestamp is closest to the target within 60 seconds.
// Returns -1 if nothing is found within the window (sentinel for "missing").
//
// In Python this would be:
//   candidates = {k: v for k, v in lookup.items() if abs(parse(k) - target) <= 60}
//   return min(candidates, key=lambda k: abs(parse(k) - target)) if candidates else -1
func findNearestValue(target time.Time, lookup map[string]float64) float64 {
	window := time.Minute // +/- 60 seconds
	var bestKey string
	var bestDiff time.Duration

	for key := range lookup {
		ts, err := time.Parse(time.RFC3339, key)
		if err != nil {
			continue
		}
		diff := target.Sub(ts)
		if diff < 0 {
			diff = -diff // abs() — Go doesn't have math.Abs for time.Duration.
		}
		if diff > window {
			continue
		}
		if bestKey == "" || diff < bestDiff {
			bestKey = key
			bestDiff = diff
		}
	}

	if bestKey == "" {
		return -1 // Sentinel for "no matching reading within 60s".
	}
	return lookup[bestKey]
}

// round1 rounds a float64 to 1 decimal place.
// In Python: round(val, 1)
// Returns the original value if it's the sentinel -1 (so formatFloatOrDash still works).
func round1(v float64) float64 {
	if v < 0 { // Preserve sentinel -1 for missing values.
		return v
	}
	return float64(int(v*10+0.5)) / 10 // Simple rounding to 1 decimal.
}
