// Package poller holds the polling loops: read -> insert -> prune -> sleep.
// Python analogy: a poller.py module with one class and one function.
package poller

import (
	"database/sql"
	"sensors-temp/internal/logging"
	"sensors-temp/internal/metrics"
	"sensors-temp/internal/sensor"
	"sensors-temp/internal/store"
	"time"
)

// Poller is a struct that ties together a sensor reader, a database store, and a db connection.
// It runs an infinite loop: read -> insert -> prune -> sleep.
// In Python you'd write a class with __init__ setting self.sensor, self.store, self.db.
type Poller struct {
	reader sensor.Reader // named reader (not sensor) so it does not shadow the sensor package
	store  store.Store
	db     *sql.DB
}

// New is the constructor (called as poller.New from main) -- like Python's __init__,
// returns a pointer to a new Poller.
// The struct literal Poller{...} fills fields by name (keyword arguments in Python).
func New(reader sensor.Reader, st store.Store, db *sql.DB) *Poller {
	return &Poller{reader: reader, store: st, db: db}
}

// Run starts the infinite polling loop for temperature sensors.
// It's meant to be called with "go poller.Run(...)" so it runs in a separate goroutine.
// In Python you'd do threading.Thread(target=poller.run, args=(interval, retain)).start().
func (p *Poller) Run(intervalSec, retainHours int) {
	logging.Logger.Info("Temperature polling loop started (interval=%ds, retain=%dh)", intervalSec, retainHours)

	// Infinite loop -- same as "while True:" in Python.
	for {
		p.poll(retainHours)

		logging.Logger.Debug("Sleeping for %d seconds...", intervalSec)
		time.Sleep(time.Duration(intervalSec) * time.Second)
	}
}

// poll runs ONE cycle (read -> insert -> prune) without sleeping.
// Split out of Run so tests can exercise a cycle without an infinite loop.
func (p *Poller) poll(retainHours int) {
	p.Poll(retainHours, nil)
}

// Poll observes fresh temperature readings before storing every sensor.
func (p *Poller) Poll(retainHours int, observe func([]metrics.MetricPoint)) {
	logging.Logger.Debug("Reading CPU temperatures...")
	temps := p.reader.Read()

	if observe != nil {
		points := make([]metrics.MetricPoint, 0, len(temps))
		for name, temp := range temps {
			points = append(points, metrics.MetricPoint{Sensor: name, Value: temp, Unit: "C"})
		}
		observe(points)
	}
	if len(temps) == 0 && observe == nil {
		logging.Logger.Warn("No temperature sensors found")
	}

	// range over a map gives (key, value) -- like Python's dict.items().
	for name, temp := range temps {
		logging.Logger.Debug("Recording %s: %.2fC", name, temp)
		p.store.Insert(p.db, name, temp, "temperature", "C")
	}

	logging.Logger.Info("Recorded %d temperature reading(s)", len(temps))
	p.store.Prune(p.db, store.AllMetrics, retainHours)
}

// RunObserved schedules temperature cycles between their starts.
func (p *Poller) RunObserved(intervalSec, retainHours int, observe func([]metrics.MetricPoint)) {
	p.runObserved(intervalSec, retainHours, observe, time.Now, func(d time.Duration) bool { time.Sleep(d); return true })
}

func (p *Poller) runObserved(intervalSec, retainHours int, observe func([]metrics.MetricPoint), now func() time.Time, sleep func(time.Duration) bool) {
	runObserved("temperature", intervalSec, retainHours, func() { p.Poll(retainHours, observe) }, now, sleep)
}

// RunMetric is a generic polling loop for any system metric type.
// It reads metrics via readFunc, inserts them with st, prunes, then sleeps.
// Multiple goroutines call this with different intervals and metric types.
// In Python: threading.Thread(target=run_metric_poller, args=(interval, read_fn)).start()
func RunMetric(st store.Store, db *sql.DB, metricType string, intervalSec, retainHours int, readFunc func() []metrics.MetricPoint) {
	logging.Logger.Info("[%s] polling loop started (interval=%ds, retain=%dh)", metricType, intervalSec, retainHours)

	for {
		pollMetric(st, db, metricType, retainHours, readFunc)

		logging.Logger.Debug("[%s] sleeping for %d seconds...", metricType, intervalSec)
		time.Sleep(time.Duration(intervalSec) * time.Second)
	}
}

// RunMetricObserved schedules cycles between their starts, including read time.
func RunMetricObserved(st store.Store, db *sql.DB, metricType string, intervalSec, retainHours int, readFunc func() []metrics.MetricPoint, observe func([]metrics.MetricPoint)) {
	runMetricObserved(st, db, metricType, intervalSec, retainHours, readFunc, observe, time.Now, func(d time.Duration) bool { time.Sleep(d); return true })
}

func runMetricObserved(st store.Store, db *sql.DB, metricType string, intervalSec, retainHours int, readFunc func() []metrics.MetricPoint, observe func([]metrics.MetricPoint), now func() time.Time, sleep func(time.Duration) bool) {
	runObserved(metricType, intervalSec, retainHours, func() { PollMetric(st, db, metricType, retainHours, readFunc, observe) }, now, sleep)
}

func runObserved(metricType string, intervalSec, retainHours int, poll func(), now func() time.Time, sleep func(time.Duration) bool) {
	logging.Logger.Info("[%s] observed polling loop started (target interval=%ds, retain=%dh)", metricType, intervalSec, retainHours)
	delayed := false
	for {
		start := now()
		poll()
		wait := time.Duration(intervalSec)*time.Second - now().Sub(start)
		if wait <= 0 {
			if !delayed {
				logging.Logger.Warn("[%s] polling cycle exceeded target interval", metricType)
			}
			delayed = true
			wait = 0
		} else {
			delayed = false
		}
		if !sleep(wait) {
			return
		}
	}
}

// pollMetric runs ONE cycle (read -> insert -> prune) for a system metric.
func pollMetric(st store.Store, db *sql.DB, metricType string, retainHours int, readFunc func() []metrics.MetricPoint) {
	PollMetric(st, db, metricType, retainHours, readFunc, nil)
}

// PollMetric runs one cycle and observes the fresh readings before persistence.
func PollMetric(st store.Store, db *sql.DB, metricType string, retainHours int, readFunc func() []metrics.MetricPoint, observe func([]metrics.MetricPoint)) {
	logging.Logger.Debug("Reading [%s] metrics...", metricType)
	points := readFunc()
	if observe != nil {
		observe(points)
	}

	if len(points) == 0 && observe == nil {
		logging.Logger.Warn("[%s] No metrics returned", metricType)
	}

	for _, p := range points {
		logging.Logger.Debug("Recording [%s] %s: %.2f%s", metricType, p.Sensor, p.Value, p.Unit)
		st.Insert(db, p.Sensor, p.Value, metricType, p.Unit)
	}

	if len(points) > 0 {
		logging.Logger.Info("Recorded %d [%s] reading(s)", len(points), metricType)
	}

	st.Prune(db, metricType, retainHours)
}
