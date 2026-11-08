package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sensors-temp/internal/store"
	"testing"
)

// TestRegisterRoutes checks the public seam: a mux built by Register serves
// every historical and /api/current/* route against a TempDir SQLite store.
// Python analogy: hitting a Flask test_client on every registered route.
func TestRegisterRoutes(t *testing.T) {
	st, db := newTempSQLite(t)
	st.Insert(db, "zz-test/temp", 55.5, "temperature", "C")
	st.Insert(db, "zz-test/cpu", 12.0, "cpu", "%")

	mux := http.NewServeMux()
	Register(mux, st, db)

	tests := []struct {
		path       string
		wantSensor string // "" means no row expected
	}{
		{"/api/temps", "zz-test/temp"},
		{"/api/temps?from=2000-01-01T00:00:00Z&to=2100-01-01T00:00:00Z", "zz-test/temp"},
		{"/api/current", "zz-test/temp"},
		{"/api/cpu?hours=2", "zz-test/cpu"},
		{"/api/cpu?from=2000-01-01T00:00:00Z&to=2100-01-01T00:00:00Z", "zz-test/cpu"},
		{"/api/current/cpu", "zz-test/cpu"},
		{"/api/memory", ""},
		{"/api/swap", ""},
		{"/api/disk", ""},
		{"/api/load", ""},
		{"/api/current/memory", ""},
		{"/api/current/swap", ""},
		{"/api/current/disk", ""},
		{"/api/current/load", ""},
	}
	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			rec := httptest.NewRecorder()
			mux.ServeHTTP(rec, httptest.NewRequest("GET", tt.path, nil))
			if rec.Code != 200 {
				t.Fatalf("status = %d, want 200", rec.Code)
			}
			var got []store.Reading
			if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
				t.Fatalf("decode: %v", err)
			}
			if tt.wantSensor == "" {
				if len(got) != 0 {
					t.Fatalf("expected no rows, got %v", getSensorNames(got))
				}
				return
			}
			if len(got) != 1 || got[0].Sensor != tt.wantSensor {
				t.Fatalf("rows = %v, want only %s", getSensorNames(got), tt.wantSensor)
			}
		})
	}
}
