package alerts_test

import (
	"os"
	"path/filepath"
	"testing"

	"sensors-temp/internal/alerts"
)

func TestLoadConfig(t *testing.T) {
	dir := t.TempDir()
	public := filepath.Join(dir, "static")
	if err := os.Mkdir(public, 0700); err != nil {
		t.Fatal(err)
	}
	valid := `{"enabled":true,"chat_id":"123","rules":[{"id":"cpu-high","metric_type":"cpu","sensor":"cpu/total","threshold":100,"duration_minutes":1}]}`
	tests := []struct {
		name, body, token string
		public, wantErr   bool
	}{
		{"disabled", `{"enabled":false}`, "", false, false},
		{"valid", valid, "123:abc", false, false},
		{"fractional duration", `{"enabled":true,"chat_id":"123","rules":[{"id":"x","metric_type":"cpu","sensor":"cpu/total","threshold":0,"duration_minutes":0.5}]}`, "123:abc", false, false},
		{"missing token", valid, "", false, true},
		{"public file", valid, "123:abc", true, true},
		{"missing chat", `{"enabled":true,"rules":[]}`, "123:abc", false, true},
		{"invalid threshold", `{"enabled":true,"chat_id":"123","rules":[{"id":"x","metric_type":"cpu","sensor":"cpu/total","threshold":101,"duration_minutes":1}]}`, "123:abc", false, true},
		{"zero duration", `{"enabled":true,"chat_id":"123","rules":[{"id":"x","metric_type":"cpu","sensor":"cpu/total","threshold":50,"duration_minutes":0}]}`, "123:abc", false, true},
		{"overflow duration", `{"enabled":true,"chat_id":"123","rules":[{"id":"x","metric_type":"cpu","sensor":"cpu/total","threshold":50,"duration_minutes":999999999999999999}]}`, "123:abc", false, true},
		{"duplicate", `{"enabled":true,"chat_id":"123","rules":[{"id":"x","metric_type":"cpu","sensor":"cpu/total","threshold":50,"duration_minutes":1},{"id":"x","metric_type":"cpu","sensor":"cpu/total","threshold":95,"duration_minutes":1}]}`, "123:abc", false, true},
		{"wrong sensor", `{"enabled":true,"chat_id":"123","rules":[{"id":"x","metric_type":"cpu","sensor":"cpu/Core 0","threshold":50,"duration_minutes":1}]}`, "123:abc", false, true},
		{"wrong metric", `{"enabled":true,"chat_id":"123","rules":[{"id":"x","metric_type":"temperature","sensor":"cpu/total","threshold":50,"duration_minutes":1}]}`, "123:abc", false, true},
		{"missing threshold", `{"enabled":true,"chat_id":"123","rules":[{"id":"x","metric_type":"cpu","sensor":"cpu/total","duration_minutes":1}]}`, "123:abc", false, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(dir, tt.name+".json")
			if tt.public {
				path = filepath.Join(public, "active.json")
			}
			if err := os.WriteFile(path, []byte(tt.body), 0600); err != nil {
				t.Fatal(err)
			}
			_, err := alerts.LoadConfig(path, public, tt.token)
			if (err != nil) != tt.wantErr {
				t.Fatalf("error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
	cfg, err := alerts.LoadConfig("", public, "")
	if err != nil || cfg.Enabled {
		t.Fatalf("unselected configuration: %+v, %v", cfg, err)
	}
	link := filepath.Join(dir, "link.json")
	if err := os.Symlink(filepath.Join(public, "active.json"), link); err != nil {
		t.Fatal(err)
	}
	if _, err := alerts.LoadConfig(link, public, "123:abc"); err == nil {
		t.Fatal("symlink into public directory accepted")
	}
	cfg, err = alerts.LoadConfig(filepath.Join("..", "..", "alerts.example.json"), public, "")
	if err != nil || cfg.Enabled {
		t.Fatalf("example should be disabled: %+v %v", cfg, err)
	}
}
