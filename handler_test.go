package main

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// TestCPUViewFilter tests the /api/cpu endpoint with the view parameter.
// This is a table-driven integration test that verifies the handler filters
// CPU readings correctly based on the view query parameter.
func TestCPUViewFilter(t *testing.T) {
	// Set up a temporary SQLite database for testing.
	// Like Python's tempfile.NamedTemporaryFile() -- created fresh for each test run.
	store := NewSQLiteStore()
	db := store.InitDB()
	defer db.Close()

	// Seed the database with test data.
	// We insert readings with different sensor names to test filtering.
	seedCPUData(t, db, store)

	// Create the handler -- this is what we're testing.
	handler := handleMetricByType(store, db, "cpu")

	// Table-driven tests -- like pytest.mark.parametrize in Python.
	// Each test case is independent and verifies one aspect of the behavior.
	tests := []struct {
		name           string
		query          string
		wantSensors    []string
		dontWantSensor []string
	}{
		{
			name:           "view=total returns only cpu/total",
			query:          "?view=total",
			wantSensors:    []string{"cpu/total"},
			dontWantSensor: []string{"cpu/Core 0", "cpu/Core 1"},
		},
		{
			name:           "view=per-core returns only per-core sensors",
			query:          "?view=per-core",
			wantSensors:    []string{"cpu/Core 0", "cpu/Core 1"},
			dontWantSensor: []string{"cpu/total"},
		},
		{
			name:        "view=both returns all sensors",
			query:       "?view=both",
			wantSensors: []string{"cpu/total", "cpu/Core 0", "cpu/Core 1"},
		},
		{
			name:        "no view parameter returns all sensors (backward compat)",
			query:       "",
			wantSensors: []string{"cpu/total", "cpu/Core 0", "cpu/Core 1"},
		},
		{
			name:        "invalid view parameter returns all sensors",
			query:       "?view=invalid",
			wantSensors: []string{"cpu/total", "cpu/Core 0", "cpu/Core 1"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create an HTTP test request -- like Python's requests.get() but in-memory.
			req := httptest.NewRequest("GET", "/api/cpu"+tt.query, nil)
			w := httptest.NewRecorder()

			// Call the handler.
			handler.ServeHTTP(w, req)

			// Check the response status.
			if w.Code != http.StatusOK {
				t.Errorf("expected status 200, got %d", w.Code)
			}

			// Parse the JSON response.
			var readings []Reading
			if err := json.NewDecoder(w.Body).Decode(&readings); err != nil {
				t.Fatalf("failed to decode response: %v", err)
			}

			// Verify the expected sensors are present.
			sensorSet := make(map[string]bool)
			for _, r := range readings {
				sensorSet[r.Sensor] = true
			}

			for _, want := range tt.wantSensors {
				if !sensorSet[want] {
					t.Errorf("expected sensor %q in response, but not found. Got sensors: %v", want, getSensorNames(readings))
				}
			}

			// Verify the unwanted sensors are NOT present.
			for _, dontWant := range tt.dontWantSensor {
				if sensorSet[dontWant] {
					t.Errorf("sensor %q should not be in response, but was found. Got sensors: %v", dontWant, getSensorNames(readings))
				}
			}
		})
	}
}

// TestCPUViewFilterWithTimeRange tests that view parameter works with hours and from/to.
// Verifies acceptance criterion: "The existing ?hours= and ?from=/to= range parameters
// continue to work in combination with view".
func TestCPUViewFilterWithTimeRange(t *testing.T) {
	store := NewSQLiteStore()
	db := store.InitDB()
	defer db.Close()

	seedCPUData(t, db, store)

	handler := handleMetricByType(store, db, "cpu")

	tests := []struct {
		name        string
		query       string
		wantSensors []string
	}{
		{
			name:        "view=total with hours parameter",
			query:       "?view=total&hours=2",
			wantSensors: []string{"cpu/total"},
		},
		{
			name:        "view=per-core with hours parameter",
			query:       "?view=per-core&hours=2",
			wantSensors: []string{"cpu/Core 0", "cpu/Core 1"},
		},
		{
			name:        "view=both with hours parameter",
			query:       "?view=both&hours=2",
			wantSensors: []string{"cpu/total", "cpu/Core 0", "cpu/Core 1"},
		},
		{
			name:        "view=total with from/to range",
			query:       "?view=total&from=2020-01-01T00:00:00Z&to=2030-12-31T23:59:59Z",
			wantSensors: []string{"cpu/total"},
		},
		{
			name:        "view=per-core with from/to range",
			query:       "?view=per-core&from=2020-01-01T00:00:00Z&to=2030-12-31T23:59:59Z",
			wantSensors: []string{"cpu/Core 0", "cpu/Core 1"},
		},
		{
			name:        "view=both with from/to range",
			query:       "?view=both&from=2020-01-01T00:00:00Z&to=2030-12-31T23:59:59Z",
			wantSensors: []string{"cpu/total", "cpu/Core 0", "cpu/Core 1"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest("GET", "/api/cpu"+tt.query, nil)
			w := httptest.NewRecorder()

			handler.ServeHTTP(w, req)

			if w.Code != http.StatusOK {
				t.Errorf("expected status 200, got %d", w.Code)
			}

			var readings []Reading
			if err := json.NewDecoder(w.Body).Decode(&readings); err != nil {
				t.Fatalf("failed to decode response: %v", err)
			}

			sensorSet := make(map[string]bool)
			for _, r := range readings {
				sensorSet[r.Sensor] = true
			}

			for _, want := range tt.wantSensors {
				if !sensorSet[want] {
					t.Errorf("expected sensor %q in response, but not found. Got sensors: %v", want, getSensorNames(readings))
				}
			}
		})
	}
}

// seedCPUData inserts test CPU readings into the database.
// This is a test helper -- like a pytest fixture in Python.
func seedCPUData(t *testing.T, db *sql.DB, store Store) {
	t.Helper()

	// Insert per-core readings.
	store.Insert(db, "cpu/Core 0", 45.2, "cpu", "%")
	store.Insert(db, "cpu/Core 1", 32.1, "cpu", "%")

	// Insert total reading.
	store.Insert(db, "cpu/total", 38.6, "cpu", "%")

	// Give the database a moment to persist (SQLite WAL mode).
	time.Sleep(10 * time.Millisecond)
}

// getSensorNames extracts sensor names from a slice of readings for error messages.
func getSensorNames(readings []Reading) []string {
	names := make([]string, len(readings))
	for i, r := range readings {
		names[i] = r.Sensor
	}
	return names
}
