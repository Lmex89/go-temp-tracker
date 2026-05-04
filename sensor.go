package main

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// SensorReader is an *interface* (like a Python ABC or Protocol, but implicit).
// Any type that has a Read() method returning map[string]float64 automatically satisfies it.
// In Python you'd write "class SensorReader(ABC): @abstractmethod def read() -> dict[str, float]".
// Here, you just define the method set — no explicit "implements" keyword needed.
type SensorReader interface {
	Read() map[string]float64
}

// LinuxThermalSensor is a *struct* (like a Python class with only attributes, no methods defined inline).
// Struct{} means it has no fields — just an empty "bag". Methods are attached separately below.
type LinuxThermalSensor struct{}

// NewLinuxThermalSensor is a *constructor* — returns a pointer (*LinuxThermalSensor).
// In Python you'd do this in __init__ and return self. Here, we manually return &LinuxThermalSensor{}.
// The & means "take the address of" — like creating an object and getting a reference to it.
func NewLinuxThermalSensor() *LinuxThermalSensor {
	return &LinuxThermalSensor{}
}

// Read reads CPU temperature sensors from Linux's virtual filesystem at /sys/class/thermal/.
// This is the method that satisfies the SensorReader interface.
// The (s *LinuxThermalSensor) before the name is the *receiver* — like "self" in Python methods.
// *LinuxThermalSensor means "pointer to LinuxThermalSensor" — similar to "self" being a reference.
func (s *LinuxThermalSensor) Read() map[string]float64 {
	// make(map[string]float64) creates an empty map — like {} in Python.
	// But in Go, you MUST use make() to create maps, slices, and channels before using them.
	temps := make(map[string]float64)

	// filepath.Glob finds files matching a pattern — like Python's glob.glob().
	// Go returns TWO values: the result AND an error. This is Go's way of handling errors
	// instead of try/except. You ALWAYS check if err != nil before using the result.
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

	// range over a slice (like Python's list) gives you (index, value) pairs.
	// We use _ for the index because Go doesn't let you declare variables you don't use.
	// In Python you'd just write "for zone in zones:" and ignore the index.
	for _, zone := range zones {
		zoneName := filepath.Base(zone)

		// os.ReadFile reads a whole file into memory — like Python's open().read().
		// Returns []byte (byte slice, similar to Python's bytes) and error.
		raw, err := os.ReadFile(filepath.Join(zone, "temp"))
		if err != nil {
			Logger.Warn("Cannot read %s/temp: %v", zoneName, err)
			continue
		}

		// strconv.ParseFloat converts string to float64 — like Python's float().
		// TrimSpace removes whitespace — like Python's .strip().
		val, err := strconv.ParseFloat(strings.TrimSpace(string(raw)), 64)
		if err != nil {
			Logger.Warn("Cannot parse temperature from %s: %v", zoneName, err)
			continue
		}

		// Read the sensor type name from the "type" file (e.g. "x86_pkg_temp").
		nameRaw, err := os.ReadFile(filepath.Join(zone, "type"))
		name := zoneName
		if err == nil {
			name = strings.TrimSpace(string(nameRaw))
		} else {
			Logger.Warn("Cannot read %s/type, using zone name: %v", zoneName, err)
		}

		// Linux reports temperature in millidegrees (e.g. 45000 = 45°C).
		tempC := val / 1000.0
		Logger.Debug("Sensor %s (%s): %.2f°C", zoneName, name, tempC)
		temps[name] = tempC  // Assigning to a map — like Python dict: temps[name] = tempC
	}

	return temps  // Returns the map (Go passes maps by reference, like Python dicts)
}
