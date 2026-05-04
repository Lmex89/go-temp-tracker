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

// Read reads CPU temperature sensors from multiple Linux interfaces.
// Strategy: Try hwmon first (better labels), fall back to thermal zones (always available).
// This is like Python's "try hwmon except: use thermal" pattern.
// The (s *LinuxThermalSensor) before the name is the *receiver* — like "self" in Python methods.
func (s *LinuxThermalSensor) Read() map[string]float64 {
	// Try hwmon first — provides better sensor names like "Core 0", "Package id 0"
	// This is like Python's EAFP: Easier to Ask for Forgiveness than Permission
	temps := s.readHwmonSensors()

	// If hwmon returned sensors, use them
	// In Python: if temps: return temps
	if len(temps) > 0 {
		Logger.Debug("Using hwmon sensors (%d found)", len(temps))
		return temps
	}

	// Fallback to thermal zones — works on ALL Linux systems
	// Like a Python fallback: return read_thermal_zones() if not temps
	Logger.Debug("No hwmon sensors found, falling back to thermal zones")
	return s.readThermalZones()
}

// readHwmonSensors reads from /sys/class/hwmon/ — the hardware monitoring interface.
// This provides detailed sensor labels like "Core 0" instead of generic "thermal_zone0".
// Works when kernel modules like coretemp (Intel) or k10temp (AMD) are loaded.
// Returns empty map if no hwmon sensors are available.
// In Python terms: scans /sys/class/hwmon/hwmon*/temp*_input files.
func (s *LinuxThermalSensor) readHwmonSensors() map[string]float64 {
	// make(map[string]float64) creates an empty map — like {} in Python.
	temps := make(map[string]float64)

	// filepath.Glob finds all hwmon directories — like Python's glob.glob('/sys/class/hwmon/hwmon*')
	// Go returns TWO values: result AND error. This is Go's error handling instead of try/except.
	hwmons, err := filepath.Glob("/sys/class/hwmon/hwmon*")
	if err != nil {
		Logger.Debug("Failed to glob hwmon directories: %v", err)
		return temps
	}

	if len(hwmons) == 0 {
		Logger.Debug("No hwmon directories found")
		return temps
	}

	Logger.Debug("Found %d hwmon directorie(s)", len(hwmons))

	// Iterate over each hwmon device — like Python's for hwmon in hwmons:
	// We use _ for the index because Go doesn't allow unused variables.
	for _, hwmon := range hwmons {
		hwmonName := filepath.Base(hwmon)

		// Read the device name (e.g., "coretemp", "k10temp") from the "name" file.
		// This identifies what hardware this hwmon represents.
		nameRaw, err := os.ReadFile(filepath.Join(hwmon, "name"))
		if err != nil {
			Logger.Debug("Cannot read %s/name: %v", hwmonName, err)
			continue
		}
		deviceName := strings.TrimSpace(string(nameRaw))
		Logger.Debug("Found hwmon device: %s (%s)", hwmonName, deviceName)

		// Look for temperature input files: temp1_input, temp2_input, etc.
		// filepath.Glob returns matching files sorted alphabetically.
		tempInputs, err := filepath.Glob(filepath.Join(hwmon, "temp*_input"))
		if err != nil {
			Logger.Debug("Failed to glob temp inputs in %s: %v", hwmonName, err)
			continue
		}

		// Process each temperature sensor file.
		// In Python: for temp_file in temp_inputs:
		for _, tempInput := range tempInputs {
			// Extract the sensor number from filename: "temp1_input" → "1"
			// filepath.Base gets the filename, strings.TrimSuffix removes "_input"
			baseName := filepath.Base(tempInput)           // "temp1_input"
			numStr := strings.TrimPrefix(baseName, "temp") // "1_input"
			numStr = strings.TrimSuffix(numStr, "_input")  // "1"

			// Read the temperature value from tempN_input file.
			// Value is in millidegrees Celsius (e.g., 45000 = 45°C).
			raw, err := os.ReadFile(tempInput)
			if err != nil {
				Logger.Debug("Cannot read %s: %v", baseName, err)
				continue
			}

			// strconv.ParseFloat converts string to float64 — like Python's float().
			val, err := strconv.ParseFloat(strings.TrimSpace(string(raw)), 64)
			if err != nil {
				Logger.Debug("Cannot parse temperature from %s: %v", baseName, err)
				continue
			}

			// Try to read the sensor label from tempN_label file.
			// Labels provide human-readable names like "Core 0" or "Package id 0".
			labelFile := filepath.Join(hwmon, "temp"+numStr+"_label")
			labelRaw, err := os.ReadFile(labelFile)
			label := ""
			if err == nil {
				label = strings.TrimSpace(string(labelRaw))
			}

			// Calculate temperature in Celsius (divide millidegrees by 1000).
			tempC := val / 1000.0

			// Create a descriptive sensor name: "coretemp/Package id 0" or "coretemp/temp1"
			// If we have a label, use it; otherwise use the generic tempN name.
			sensorName := deviceName + "/" + baseName
			if label != "" {
				sensorName = deviceName + "/" + label
			}

			Logger.Debug("Sensor %s: %.2f°C", sensorName, tempC)
			temps[sensorName] = tempC  // Like Python: temps[sensor_name] = temp_c
		}
	}

	return temps
}

// readThermalZones reads from /sys/class/thermal/ — the thermal zone interface.
// This is the fallback method that works on ALL Linux systems with kernel 2.6+.
// Thermal zones are more generic ("thermal_zone0", "x86_pkg_temp") but always available.
// In Python terms: scans /sys/class/thermal/thermal_zone*/temp files.
func (s *LinuxThermalSensor) readThermalZones() map[string]float64 {
	// make(map[string]float64) creates an empty map — like {} in Python.
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