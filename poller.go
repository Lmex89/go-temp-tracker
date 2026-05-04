package main

import (
	"database/sql"
	"time"
)

// Poller is a struct that ties together a sensor reader, a database store, and a db connection.
// It runs an infinite loop: read → insert → prune → sleep.
// In Python you'd write a class with __init__ setting self.sensor, self.store, self.db.
type Poller struct {
	sensor SensorReader
	store  Store
	db     *sql.DB
}

// NewPoller constructor — like Python's __init__, returns a pointer to a new Poller.
// The struct literal Poller{...} fills fields by name (keyword arguments in Python).
func NewPoller(sensor SensorReader, store Store, db *sql.DB) *Poller {
	return &Poller{sensor: sensor, store: store, db: db}
}

// Run starts the infinite polling loop. It's meant to be called with "go poller.Run(...)"
// so it runs in a separate goroutine (like a thread).
// In Python you'd do threading.Thread(target=poller.run, args=(interval, retain)).start().
func (p *Poller) Run(intervalSec, retainHours int) {
	Logger.Info("Polling loop started (interval=%ds, retain=%dh)", intervalSec, retainHours)

	// Infinite loop — same as "while True:" in Python.
	for {
		Logger.Debug("Reading CPU temperatures...")
		temps := p.sensor.Read()  // Calls the Read() method via the interface

		if len(temps) == 0 {
			Logger.Warn("No temperature sensors found")
		}

		// range over a map gives (key, value) — like Python's dict.items().
		for sensor, temp := range temps {
			Logger.Debug("Recording %s: %.2f°C", sensor, temp)
			p.store.Insert(p.db, sensor, temp)
		}

		Logger.Info("Recorded %d temperature reading(s)", len(temps))
		p.store.Prune(p.db, retainHours)  // Delete old readings

		Logger.Debug("Sleeping for %d seconds...", intervalSec)
		// time.Sleep pauses the goroutine — like Python's time.sleep().
		// time.Duration(intervalSec) * time.Second converts int to a Duration.
		time.Sleep(time.Duration(intervalSec) * time.Second)
	}
}
