package main

import (
	"database/sql"
	"time"
)

type Poller struct {
	sensor SensorReader
	store  Store
	db     *sql.DB
}

func NewPoller(sensor SensorReader, store Store, db *sql.DB) *Poller {
	return &Poller{sensor: sensor, store: store, db: db}
}

func (p *Poller) Run(intervalSec, retainHours int) {
	Logger.Info("Polling loop started (interval=%ds, retain=%dh)", intervalSec, retainHours)

	for {
		Logger.Debug("Reading CPU temperatures...")
		temps := p.sensor.Read()

		if len(temps) == 0 {
			Logger.Warn("No temperature sensors found")
		}

		for sensor, temp := range temps {
			Logger.Debug("Recording %s: %.2f°C", sensor, temp)
			p.store.Insert(p.db, sensor, temp)
		}

		Logger.Info("Recorded %d temperature reading(s)", len(temps))
		p.store.Prune(p.db, retainHours)

		Logger.Debug("Sleeping for %d seconds...", intervalSec)
		time.Sleep(time.Duration(intervalSec) * time.Second)
	}
}
