package main

import (
	"database/sql"
	"fmt"
	"math"
	"time"
)

// Spike represents a single temperature reading that exceeds the baseline threshold.
// In Python you'd use a @dataclass or NamedTuple.
type Spike struct {
	Sensor       string
	Timestamp    time.Time
	TempC        float64
	BaselineMean float64
	BaselineMin  float64
	BaselineMax  float64
	BaselineStd  float64
	Deviation    float64
	Severity     string
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
	logger.debug("querying baseline readings from %s to %s",
		baselineSince.Format("2006-01-02 15:04:05"), now.Format("2006-01-02 15:04:05"))
	baselineRows, err := queryTemperatureRange(db, baselineSince, now)
	if err != nil {
		return nil, fmt.Errorf("baseline query failed: %w", err)
	}
	logger.debug("loaded %d baseline readings", len(baselineRows))

	// Group by sensor and accumulate values — like Python's collections.defaultdict(list).
	sensorTemps := make(map[string][]float64)
	for _, r := range baselineRows {
		sensorTemps[r.Sensor] = append(sensorTemps[r.Sensor], r.TempC)
	}

	// Calculate stats per sensor.
	type stats struct {
		mean float64
		min  float64
		max  float64
		std  float64
	}
	means := make(map[string]stats)
	for sensor, temps := range sensorTemps {
		if len(temps) == 0 {
			continue
		}
		var sum float64
		mn, mx := temps[0], temps[0]
		for _, t := range temps {
			sum += t
			if t < mn {
				mn = t
			}
			if t > mx {
				mx = t
			}
		}
		mean := sum / float64(len(temps))
		var sqDiff float64
		for _, t := range temps {
			d := t - mean
			sqDiff += d * d
		}
		std := math.Sqrt(sqDiff / float64(len(temps)))
		means[sensor] = stats{mean: mean, min: mn, max: mx, std: std}
	}
	logger.debug("computed baseline stats for %d sensor(s)", len(means))
	for sensor, st := range means {
		logger.debug("  %s: mean=%.1f min=%.1f max=%.1f std=%.1f", sensor, st.mean, st.min, st.max, st.std)
	}

	// --- Step 2: Find spikes in the report window ---
	logger.debug("querying report readings from %s to %s",
		reportSince.Format("2006-01-02 15:04:05"), now.Format("2006-01-02 15:04:05"))
	reportRows, err := queryTemperatureRange(db, reportSince, now)
	if err != nil {
		return nil, fmt.Errorf("report window query failed: %w", err)
	}
	logger.debug("loaded %d report readings, scanning for spikes (threshold=%.1f)", len(reportRows), deviationThreshold)

	var spikes []Spike
	for _, r := range reportRows {
		st, ok := means[r.Sensor]
		if !ok {
			continue
		}
		if r.TempC > st.mean+deviationThreshold {
			dev := r.TempC - st.mean
			spikes = append(spikes, Spike{
				Sensor:       r.Sensor,
				Timestamp:    r.Timestamp,
				TempC:        r.TempC,
				BaselineMean: st.mean,
				BaselineMin:  st.min,
				BaselineMax:  st.max,
				BaselineStd:  st.std,
				Deviation:    dev,
				Severity:     classifySeverity(dev, deviationThreshold),
			})
		}
	}

	logger.debug("detected %d spike(s)", len(spikes))
	for _, s := range spikes {
		logger.debug("  %s: %.1fC at %s (dev=+%.1f, severity=%s)",
			s.Sensor, s.TempC, s.Timestamp.Format("2006-01-02 15:04:05"), s.Deviation, s.Severity)
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

// classifySeverity categorizes a spike based on how far it exceeds the threshold.
func classifySeverity(deviation, threshold float64) string {
	ratio := deviation / threshold
	switch {
	case ratio >= 2.0:
		return "severe"
	case ratio >= 1.5:
		return "high"
	case ratio >= 1.0:
		return "moderate"
	default:
		return "mild"
	}
}
