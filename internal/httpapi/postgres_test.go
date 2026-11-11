package httpapi

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"sensors-temp/internal/store"
	"strings"
	"testing"
	"time"
)

func TestPostgresRegisteredRoutes(t *testing.T) {
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("PostgreSQL tests skipped: DATABASE_URL not set")
	}
	admin, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { admin.Close() })
	if err := admin.Ping(); err != nil {
		t.Skipf("PostgreSQL tests skipped: unavailable: %v", err)
	}
	schema := fmt.Sprintf("http_test_%d", time.Now().UnixNano())
	if _, err := admin.Exec("CREATE SCHEMA " + schema); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if _, err := admin.Exec("DROP SCHEMA " + schema + " CASCADE"); err != nil {
			t.Errorf("drop test schema: %v", err)
		}
	})
	sep := "?"
	if strings.Contains(dsn, "?") {
		sep = "&"
	}
	db, err := sql.Open("pgx", dsn+sep+"search_path="+schema)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if _, err := db.Exec(store.PostgresSchema); err != nil {
		t.Fatal(err)
	}
	st := store.NewPostgresStore()
	mux := http.NewServeMux()
	Register(mux, st, db)
	paths := []string{
		"/api/temps", "/api/temps?from=2000-01-01T00:00:00Z&to=2100-01-01T00:00:00Z", "/api/current",
		"/api/cpu", "/api/cpu?from=2000-01-01T00:00:00Z&to=2100-01-01T00:00:00Z", "/api/current/cpu",
		"/api/memory", "/api/current/memory", "/api/swap", "/api/current/swap",
		"/api/disk", "/api/current/disk", "/api/load", "/api/current/load",
	}
	request := func(path string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		return rec
	}
	for _, path := range paths {
		rec := request(path)
		if rec.Code != http.StatusOK || rec.Body.String() != "null\n" {
			t.Errorf("empty %s: status %d body %q", path, rec.Code, rec.Body.String())
		}
	}
	for _, metric := range []string{"temperature", "cpu", "memory", "swap", "disk", "load"} {
		st.Insert(db, "test/"+metric, 42, metric, "x")
	}
	for _, path := range paths {
		rec := request(path)
		var readings []store.Reading
		if err := json.Unmarshal(rec.Body.Bytes(), &readings); err != nil || rec.Code != http.StatusOK || len(readings) != 1 {
			t.Errorf("success %s: status %d body %q error %v", path, rec.Code, rec.Body.String(), err)
		} else if !strings.HasPrefix(readings[0].Sensor, "test/") || readings[0].TempC != 42 {
			t.Errorf("success %s: unexpected reading %+v", path, readings[0])
		}
	}
	if _, err := db.Exec("DROP TABLE readings"); err != nil {
		t.Fatal(err)
	}
	for _, path := range paths {
		rec := request(path)
		if rec.Code != http.StatusInternalServerError || rec.Body.String() != "Internal Server Error\n" {
			t.Errorf("failure %s: status %d body %q", path, rec.Code, rec.Body.String())
		}
	}
	for _, base := range []string{"/api/temps", "/api/cpu"} {
		for _, query := range []string{
			"?from=2000-01-01T00:00:00Z", "?to=2000-01-01T00:00:00Z",
			"?from=bad&to=bad", "?from=2000-01-01T00:00:00Z&to=bad",
			"?from=2100-01-01T00:00:00Z&to=2000-01-01T00:00:00Z",
		} {
			rec := request(base + query)
			if rec.Code != http.StatusBadRequest {
				t.Errorf("invalid %s%s: status %d", base, query, rec.Code)
			}
		}
	}
}
