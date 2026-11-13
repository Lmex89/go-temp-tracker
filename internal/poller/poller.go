// Package poller holds the polling loops: read -> insert -> prune -> sleep.
// Python analogy: a poller.py module with one class and one function.
package poller

import (
	"database/sql"
	"sensors-temp/internal/logging"
	"sensors-temp/internal/metrics"
	"sensors-temp/internal/sensor"
	"sensors-temp/internal/store"
	"sync"
	"time"
)

// Poller is a struct that ties together a sensor reader, a database store, and a db connection.
// It runs an infinite loop: read -> insert -> prune -> sleep.
// In Python you'd write a class with __init__ setting self.sensor, self.store, self.db.
type Poller struct {
	reader sensor.Reader // named reader (not sensor) so it does not shadow the sensor package
	store  store.Store
	db     *sql.DB
	// warnMu guards noSensorsWarned (like a threading.Lock around a flag).
	warnMu          sync.Mutex
	noSensorsWarned bool
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
		p.Poll(retainHours, nil)

		logging.Logger.Debug("Sleeping for %d seconds...", intervalSec)
		time.Sleep(time.Duration(intervalSec) * time.Second)
	}
}

// Poll runs ONE cycle (read -> observe -> insert -> prune) without sleeping.
// Split out of Run so tests can exercise a cycle without an infinite loop.
// observe may be nil (no alerts); in Python it would be an Optional[Callable].
// It is exported because the alerts package drives its end-to-end tests
// (Poller -> rules -> notifications) through it.
func (p *Poller) Poll(retainHours int, observe func([]metrics.MetricPoint)) {
	logging.Logger.Debug("Reading CPU temperatures...")
	var temps map[string]float64
	var points []metrics.MetricPoint
	// Type assertion with ", ok": like isinstance(p.reader, TimedReader) in Python.
	// A reader without acquisition times yields no points, which the evaluator
	// logs as lost observation instead of guessing when the value was read.
	timed, ok := p.reader.(sensor.TimedReader)
	if observe != nil && ok {
		readings := timed.ReadTimed()
		temps = make(map[string]float64, len(readings))
		for name, reading := range readings {
			temps[name] = reading.Value
			points = append(points, metrics.MetricPoint{Sensor: name, Value: reading.Value, Unit: "C", Observation: reading.Observation})
		}
	} else {
		temps = p.reader.Read()
	}

	// Alerts see the fresh values even if the database insert below fails.
	if observe != nil {
		observe(points)
	}
	// With alerts on, the 5s cadence would repeat this warning constantly,
	// so it is logged once until sensors come back.
	p.warnMu.Lock()
	if len(temps) == 0 {
		if observe == nil || !p.noSensorsWarned {
			logging.Logger.Warn("No temperature sensors found")
		}
		p.noSensorsWarned = true
	} else {
		p.noSensorsWarned = false
	}
	p.warnMu.Unlock()

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
	runObserved("temperature", intervalSec, retainHours, func() { p.Poll(retainHours, observe) }, time.Now, sleepAlways)
}

// RunMetric is a generic polling loop for any system metric type.
// It reads metrics via readFunc, inserts them with st, prunes, then sleeps.
// Multiple goroutines call this with different intervals and metric types.
// In Python: threading.Thread(target=run_metric_poller, args=(interval, read_fn)).start()
func RunMetric(st store.Store, db *sql.DB, metricType string, intervalSec, retainHours int, readFunc func() []metrics.MetricPoint) {
	logging.Logger.Info("[%s] polling loop started (interval=%ds, retain=%dh)", metricType, intervalSec, retainHours)

	for {
		PollMetric(st, db, metricType, retainHours, readFunc, nil)

		logging.Logger.Debug("[%s] sleeping for %d seconds...", metricType, intervalSec)
		time.Sleep(time.Duration(intervalSec) * time.Second)
	}
}

// RunMetricObserved schedules cycles between their starts, including read time.
func RunMetricObserved(st store.Store, db *sql.DB, metricType string, intervalSec, retainHours int, readFunc func() []metrics.MetricPoint, observe func([]metrics.MetricPoint)) {
	runObserved(metricType, intervalSec, retainHours, func() { PollMetric(st, db, metricType, retainHours, readFunc, observe) }, time.Now, sleepAlways)
}

// sleepAlways is the production sleep: it never asks the loop to stop.
func sleepAlways(d time.Duration) bool { time.Sleep(d); return true }

// runObserved keeps a fixed cadence measured from the START of each cycle, so
// slow reads (CPU blocks ~1s) do not stretch the interval. now and sleep are
// injected (like passing a fake clock to a Python function) so tests can stop
// the loop by returning false from sleep.
func runObserved(metricType string, intervalSec, retainHours int, poll func(), now func() time.Time, sleep func(time.Duration) bool) {
	logging.Logger.Info("[%s] observed polling loop started (target interval=%ds, retain=%dh)", metricType, intervalSec, retainHours)
	overrunWarned := false
	for {
		start := now()
		poll()
		wait := time.Duration(intervalSec)*time.Second - now().Sub(start)
		if wait <= 0 {
			if !overrunWarned {
				logging.Logger.Warn("[%s] polling cycle exceeded target interval", metricType)
			}
			overrunWarned = true
			wait = 0
		} else {
			overrunWarned = false
		}
		if !sleep(wait) {
			return
		}
	}
}

// PollMetric runs ONE cycle (read -> observe -> insert -> prune) for a system
// metric. observe may be nil; it sees the fresh readings before persistence.
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
