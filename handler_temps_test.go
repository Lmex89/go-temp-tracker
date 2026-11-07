package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestTempsAndCurrentHandlers checks the HTTP contract of /api/temps and
// /api/current/* after the handlers moved to Query / QueryLatest.
// It runs against a throwaway SQLite database (newTempSQLite does
// t.Chdir(t.TempDir()) and registers t.Cleanup), so the real temps.db is
// never touched -- like a pytest tmp_path fixture.
func TestTempsAndCurrentHandlers(t *testing.T) {
	store, db := newTempSQLite(t)

	store.Insert(db, "zz-test/temp", 55.5, "temperature", "C")
	store.Insert(db, "zz-test/cpu", 12.0, "cpu", "%")

	// get runs a handler and decodes the JSON body, keeping only our rows.
	get := func(h http.HandlerFunc, query string) (int, []Reading) {
		rec := httptest.NewRecorder()
		h(rec, httptest.NewRequest("GET", "/x"+query, nil))
		var all, mine []Reading
		_ = json.Unmarshal(rec.Body.Bytes(), &all)
		for _, r := range all {
			if strings.HasPrefix(r.Sensor, "zz-test/") {
				mine = append(mine, r)
			}
		}
		return rec.Code, mine
	}

	tests := []struct {
		name       string
		handler    http.HandlerFunc
		query      string
		wantCode   int
		wantSensor string // "" means no row of ours expected
	}{
		{"temps default hours", handleTemps(store, db), "", 200, "zz-test/temp"},
		{"temps hours=2", handleTemps(store, db), "?hours=2", 200, "zz-test/temp"},
		{"temps invalid hours falls back", handleTemps(store, db), "?hours=abc", 200, "zz-test/temp"},
		{"temps absolute range", handleTemps(store, db), "?from=2000-01-01T00:00:00Z&to=2100-01-01T00:00:00Z", 200, "zz-test/temp"},
		{"temps range far past is empty", handleTemps(store, db), "?from=2000-01-01T00:00:00Z&to=2000-01-02T00:00:00Z", 200, ""},
		{"temps only from is 400", handleTemps(store, db), "?from=2000-01-01T00:00:00Z", 400, ""},
		{"temps from after to is 400", handleTemps(store, db), "?from=2100-01-01T00:00:00Z&to=2000-01-01T00:00:00Z", 400, ""},
		{"temps bad timestamp is 400", handleTemps(store, db), "?from=nope&to=nope", 400, ""},
		{"current temps", handleCurrent(store, db), "", 200, "zz-test/temp"},
		{"current cpu", handleCurrentByType(store, db, "cpu"), "", 200, "zz-test/cpu"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			code, rows := get(tt.handler, tt.query)
			if code != tt.wantCode {
				t.Fatalf("status = %d, want %d", code, tt.wantCode)
			}
			if tt.wantSensor == "" {
				if len(rows) != 0 {
					t.Fatalf("expected no rows, got %v", getSensorNames(rows))
				}
				return
			}
			if len(rows) != 1 || rows[0].Sensor != tt.wantSensor {
				t.Fatalf("rows = %v, want only %s", getSensorNames(rows), tt.wantSensor)
			}
		})
	}
}
