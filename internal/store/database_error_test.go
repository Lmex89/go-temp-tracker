package store

import (
	"errors"
	"strings"
	"testing"
)

// TestSafeDatabaseError keeps the useful cause and drops connection details,
// like asserting on str(exc) after a redaction helper in pytest.
func TestSafeDatabaseError(t *testing.T) {
	tests := []struct {
		name     string
		err      string
		dsn      string
		keep     string
		mustDrop []string
	}{
		{
			name:     "URL, keyword pairs and user name",
			err:      `connection refused for postgres://report:secret@db.example:5432/readings?sslmode=require; user=report password=secret; password authentication failed for user "report"`,
			keep:     "connection refused",
			mustDrop: []string{"postgres://", "secret", "report", "db.example"},
		},
		{
			name:     "keyword DSN host and dbname",
			err:      "failed to connect to `host=db.internal dbname=sensors_prod`: connection refused",
			keep:     "connection refused",
			mustDrop: []string{"db.internal", "sensors_prod"},
		},
		{
			name:     "explicit DSN replaced verbatim",
			err:      "cannot parse `tracker@weird-dsn`: invalid",
			dsn:      "tracker@weird-dsn",
			keep:     "invalid",
			mustDrop: []string{"tracker@weird-dsn"},
		},
		{
			name: "SQLite path stays visible",
			err:  "unable to open database file: /tmp/readings.sqlite",
			keep: "/tmp/readings.sqlite",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := SafeDatabaseError(errors.New(tt.err), tt.dsn)
			if !strings.Contains(got, tt.keep) {
				t.Fatalf("useful detail %q lost: %q", tt.keep, got)
			}
			for _, secret := range tt.mustDrop {
				if strings.Contains(got, secret) {
					t.Errorf("%q leaked: %q", secret, got)
				}
			}
		})
	}
}
