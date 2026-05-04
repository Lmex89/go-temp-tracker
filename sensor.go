package main

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

type SensorReader interface {
	Read() map[string]float64
}

type LinuxThermalSensor struct{}

func NewLinuxThermalSensor() *LinuxThermalSensor {
	return &LinuxThermalSensor{}
}

func (s *LinuxThermalSensor) Read() map[string]float64 {
	temps := make(map[string]float64)

	zones, err := filepath.Glob("/sys/class/thermal/thermal_zone*")
	if err != nil {
		Logger.Warn("Failed to glob thermal zones: %v", err)
		return temps
	}

	if len(zones) == 0 {
		Logger.Warn("No thermal zones found at /sys/class/thermal/thermal_zone*")
		return temps
	}

	Logger.Debug("Found %d thermal zone(s)", len(zones))

	for _, zone := range zones {
		zoneName := filepath.Base(zone)

		raw, err := os.ReadFile(filepath.Join(zone, "temp"))
		if err != nil {
			Logger.Warn("Cannot read %s/temp: %v", zoneName, err)
			continue
		}

		val, err := strconv.ParseFloat(strings.TrimSpace(string(raw)), 64)
		if err != nil {
			Logger.Warn("Cannot parse temperature from %s: %v", zoneName, err)
			continue
		}

		nameRaw, err := os.ReadFile(filepath.Join(zone, "type"))
		name := zoneName
		if err == nil {
			name = strings.TrimSpace(string(nameRaw))
		} else {
			Logger.Warn("Cannot read %s/type, using zone name: %v", zoneName, err)
		}

		tempC := val / 1000.0
		Logger.Debug("Sensor %s (%s): %.2f°C", zoneName, name, tempC)
		temps[name] = tempC
	}

	return temps
}
