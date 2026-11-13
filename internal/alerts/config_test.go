package alerts_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"sensors-temp/internal/alerts"
)

func TestBrevoConfiguration(t *testing.T) {
	dir := t.TempDir()
	public := filepath.Join(dir, "static")
	if err := os.Mkdir(public, 0700); err != nil {
		t.Fatal(err)
	}
	valid := `{"enabled":true,"chat_id":"chat","brevo_sender_email":"from@example.test","brevo_sender_name":"Monitor","brevo_recipient_email":"to@example.test","rules":[{"id":"high","metric_type":"cpu","sensor":"cpu/total","threshold":50,"duration_minutes":1}]}`
	tests := []struct {
		name, body, token, key string
		wantErr                bool
	}{
		{"valid", valid, "123:abc", "test-key", false},
		{"disabled without secrets", `{"enabled":false}`, "", "", false},
		{"missing key", valid, "123:abc", "", true},
		{"invalid key", valid, "123:abc", "bad\nkey", true},
		{"missing telegram", valid, "", "test-key", true},
		{"invalid sender", strings.Replace(valid, "from@example.test", "bad address", 1), "123:abc", "test-key", true},
		{"invalid recipient", strings.Replace(valid, "to@example.test", "bad address", 1), "123:abc", "test-key", true},
		{"missing name", strings.Replace(valid, "Monitor", "", 1), "123:abc", "test-key", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(dir, "config.json")
			if err := os.WriteFile(path, []byte(tt.body), 0600); err != nil {
				t.Fatal(err)
			}
			_, err := alerts.LoadConfig(path, public, tt.token, tt.key)
			if (err != nil) != tt.wantErr {
				t.Fatalf("error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

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
			body := strings.Replace(tt.body, `{`, `{"brevo_sender_email":"from@example.test","brevo_sender_name":"Monitor","brevo_recipient_email":"to@example.test",`, 1)
			if err := os.WriteFile(path, []byte(body), 0600); err != nil {
				t.Fatal(err)
			}
			_, err := alerts.LoadConfig(path, public, tt.token, "test-key")
			if (err != nil) != tt.wantErr {
				t.Fatalf("error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
	cfg, err := alerts.LoadConfig("", public, "", "")
	if err != nil || cfg.Enabled {
		t.Fatalf("unselected configuration: %+v, %v", cfg, err)
	}
	link := filepath.Join(dir, "link.json")
	if err := os.Symlink(filepath.Join(public, "active.json"), link); err != nil {
		t.Fatal(err)
	}
	if _, err := alerts.LoadConfig(link, public, "123:abc", "test-key"); err == nil {
		t.Fatal("symlink into public directory accepted")
	}
	cfg, err = alerts.LoadConfig(filepath.Join("..", "..", "alerts.example.json"), public, "", "")
	if err != nil || cfg.Enabled {
		t.Fatalf("example should be disabled: %+v %v", cfg, err)
	}
}
