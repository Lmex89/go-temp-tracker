package main

import (
	"database/sql"
	"sensors-temp/internal/logging"
	"sensors-temp/internal/store"
	"time"
)

// Poller is a struct that ties together a sensor reader, a database store, and a db connection.
// It runs an infinite loop: read -> insert -> prune -> sleep.
// In Python you'd write a class with __init__ setting self.sensor, self.store, self.db.
type Poller struct {
	sensor SensorReader
	store  store.Store
	db     *sql.DB
}

// NewPoller constructor -- like Python's __init__, returns a pointer to a new Poller.
// The struct literal Poller{...} fills fields by name (keyword arguments in Python).
func NewPoller(sensor SensorReader, store store.Store, db *sql.DB) *Poller {
	return &Poller{sensor: sensor, store: store, db: db}
}

// Run starts the infinite polling loop for temperature sensors.
// It's meant to be called with "go poller.Run(...)" so it runs in a separate goroutine.
// In Python you'd do threading.Thread(target=poller.run, args=(interval, retain)).start().
func (p *Poller) Run(intervalSec, retainHours int) {
	logging.Logger.Info("Temperature polling loop started (interval=%ds, retain=%dh)", intervalSec, retainHours)

	// Infinite loop -- same as "while True:" in Python.
	for {
		logging.Logger.Debug("Reading CPU temperatures...")
		temps := p.sensor.Read()

		if len(temps) == 0 {
			logging.Logger.Warn("No temperature sensors found")
		}

		// range over a map gives (key, value) -- like Python's dict.items().
		for sensor, temp := range temps {
			logging.Logger.Debug("Recording %s: %.2fC", sensor, temp)
			p.store.Insert(p.db, sensor, temp, "temperature", "C")
		}

		logging.Logger.Info("Recorded %d temperature reading(s)", len(temps))
		p.store.Prune(p.db, store.AllMetrics, retainHours)

		logging.Logger.Debug("Sleeping for %d seconds...", intervalSec)
		time.Sleep(time.Duration(intervalSec) * time.Second)
	}
}

// RunMetricPoller is a generic polling loop for any system metric type.
// It reads metrics via readFunc, inserts them with store, prunes, then sleeps.
// Multiple goroutines call this with different intervals and metric types.
// In Python: threading.Thread(target=run_metric_poller, args=(interval, read_fn)).start()
func RunMetricPoller(store store.Store, db *sql.DB, metricType string, intervalSec, retainHours int, readFunc func() []MetricPoint) {
	logging.Logger.Info("[%s] polling loop started (interval=%ds, retain=%dh)", metricType, intervalSec, retainHours)

	for {
		logging.Logger.Debug("Reading [%s] metrics...", metricType)
		points := readFunc()

		if len(points) == 0 {
			logging.Logger.Warn("[%s] No metrics returned", metricType)
		}

		for _, p := range points {
			logging.Logger.Debug("Recording [%s] %s: %.2f%s", metricType, p.Sensor, p.Value, p.Unit)
			store.Insert(db, p.Sensor, p.Value, metricType, p.Unit)
		}

		if len(points) > 0 {
			logging.Logger.Info("Recorded %d [%s] reading(s)", len(points), metricType)
		}

		store.Prune(db, metricType, retainHours)

		logging.Logger.Debug("[%s] sleeping for %d seconds...", metricType, intervalSec)
		time.Sleep(time.Duration(intervalSec) * time.Second)
	}
}
