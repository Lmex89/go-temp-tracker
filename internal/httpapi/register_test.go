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
	st.Insert(db, "zz-test/memory", 40.0, "memory", "%")
	st.Insert(db, "zz-test/swap", 5.0, "swap", "%")
	st.Insert(db, "zz-test/disk", 70.0, "disk", "%")
	st.Insert(db, "zz-test/load", 1.5, "load", "")

	mux := http.NewServeMux()
	Register(mux, st, db)

	tests := []struct {
		path       string
		wantSensor string // "" means no row expected
	}{
		{"/api/temps", "zz-test/temp"},
		{"/api/temps?from=2000-01-01T00:00:00Z&to=2100-01-01T00:00:00Z", "zz-test/temp"},
		{"/api/temps?hours=bad", "zz-test/temp"},
		{"/api/temps?hours=100&from=2000-01-01T00:00:00Z&to=2000-01-02T00:00:00Z", ""},
		{"/api/current", "zz-test/temp"},
		{"/api/cpu?hours=2", "zz-test/cpu"},
		{"/api/cpu?from=2000-01-01T00:00:00Z&to=2100-01-01T00:00:00Z", "zz-test/cpu"},
		{"/api/cpu?hours=bad", "zz-test/cpu"},
		{"/api/cpu?hours=100&from=2000-01-01T00:00:00Z&to=2000-01-02T00:00:00Z", ""},
		{"/api/current/cpu", "zz-test/cpu"},
		{"/api/memory", "zz-test/memory"},
		{"/api/swap", "zz-test/swap"},
		{"/api/disk", "zz-test/disk"},
		{"/api/load", "zz-test/load"},
		{"/api/current/memory", "zz-test/memory"},
		{"/api/current/swap", "zz-test/swap"},
		{"/api/current/disk", "zz-test/disk"},
		{"/api/current/load", "zz-test/load"},
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

func TestRegisteredRoutesEmptyResults(t *testing.T) {
	st, db := newTempSQLite(t)
	mux := http.NewServeMux()
	Register(mux, st, db)
	for _, path := range []string{"/api/temps", "/api/current", "/api/cpu", "/api/current/cpu"} {
		t.Run(path, func(t *testing.T) {
			rec := httptest.NewRecorder()
			mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
			if rec.Code != http.StatusOK || rec.Body.String() != "null\n" {
				t.Fatalf("status = %d, body = %q; want 200 and null", rec.Code, rec.Body.String())
			}
		})
	}
}

func TestRegisteredRoutesQueryErrors(t *testing.T) {
	st, db := newTempSQLite(t)
	if _, err := db.Exec("DROP TABLE readings"); err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	Register(mux, st, db)
	for _, path := range []string{
		"/api/temps", "/api/temps?from=2000-01-01T00:00:00Z&to=2100-01-01T00:00:00Z",
		"/api/current", "/api/cpu", "/api/cpu?from=2000-01-01T00:00:00Z&to=2100-01-01T00:00:00Z",
		"/api/current/cpu", "/api/memory", "/api/current/memory", "/api/swap", "/api/current/swap",
		"/api/disk", "/api/current/disk", "/api/load", "/api/current/load",
	} {
		t.Run(path, func(t *testing.T) {
			rec := httptest.NewRecorder()
			mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
			if rec.Code != http.StatusInternalServerError || rec.Body.String() != "Internal Server Error\n" {
				t.Fatalf("status = %d, body = %q", rec.Code, rec.Body.String())
			}
		})
	}
	for _, base := range []string{"/api/temps", "/api/cpu"} {
		for _, query := range []string{
			"?from=2000-01-01T00:00:00Z", "?to=2000-01-01T00:00:00Z",
			"?from=", "?to=", "?from=&to=", "?from=+&to=+", "?from=&to=&hours=2",
			"?from=bad&to=bad", "?from=2000-01-01T00:00:00Z&to=bad",
			"?from=2100-01-01T00:00:00Z&to=2000-01-01T00:00:00Z",
		} {
			t.Run(base+query, func(t *testing.T) {
				rec := httptest.NewRecorder()
				mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, base+query, nil))
				if rec.Code != http.StatusBadRequest {
					t.Fatalf("status = %d, want 400", rec.Code)
				}
			})
		}
	}
}
