package alerts_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"sensors-temp/internal/alerts"
)

func TestBrevoConfiguration(t *testing.T) {
	dir := t.TempDir()
	public := filepath.Join(dir, "static")
	if err := os.Mkdir(public, 0700); err != nil {
		t.Fatal(err)
	}
	valid := `{"enabled":true,"chat_id":"123","brevo_sender_email":"from@example.test","brevo_sender_name":"Monitor","brevo_recipient_email":"to@example.test","rules":[{"id":"high","metric_type":"cpu","sensor":"cpu/total","threshold":50,"duration_minutes":1}]}`
	tests := []struct {
		name, body, token, key string
		wantErr                bool
	}{
		{"valid", valid, "123:abc", "test-key", false},
		{"disabled without secrets", `{"enabled":false}`, "", "", false},
		{"missing key", valid, "123:abc", "", true},
		{"invalid key", valid, "123:abc", "bad\nkey", true},
		{"missing telegram", valid, "", "test-key", true},
		{"malformed telegram token", valid, "garbage", "test-key", true},
		{"telegram token without secret", valid, "123:", "test-key", true},
		{"telegram token without bot id", valid, ":abc", "test-key", true},
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
		{"temperature", `{"enabled":true,"chat_id":"123","rules":[{"id":"x","metric_type":"temperature","sensor":"coretemp/Package id 0","threshold":65,"duration_minutes":2}]}`, "123:abc", false, false},
		{"multiple temperature rules", `{"enabled":true,"chat_id":"123","rules":[{"id":"x","metric_type":"temperature","sensor":"coretemp/Package id 0","threshold":65,"duration_minutes":2},{"id":"y","metric_type":"temperature","sensor":"acpitz","threshold":-273.15,"duration_minutes":0.5}]}`, "123:abc", false, false},
		{"below absolute zero", `{"enabled":true,"chat_id":"123","rules":[{"id":"x","metric_type":"temperature","sensor":"acpitz","threshold":-273.16,"duration_minutes":1}]}`, "123:abc", false, true},
		{"temperature missing sensor", `{"enabled":true,"chat_id":"123","rules":[{"id":"x","metric_type":"temperature","sensor":" ","threshold":65,"duration_minutes":1}]}`, "123:abc", false, true},
		{"temperature missing threshold", `{"enabled":true,"chat_id":"123","rules":[{"id":"x","metric_type":"temperature","sensor":"acpitz","duration_minutes":1}]}`, "123:abc", false, true},
		{"temperature zero duration", `{"enabled":true,"chat_id":"123","rules":[{"id":"x","metric_type":"temperature","sensor":"acpitz","threshold":65,"duration_minutes":0}]}`, "123:abc", false, true},
		{"temperature overflow duration", `{"enabled":true,"chat_id":"123","rules":[{"id":"x","metric_type":"temperature","sensor":"acpitz","threshold":65,"duration_minutes":1e100}]}`, "123:abc", false, true},
		{"temperature nonfinite threshold", `{"enabled":true,"chat_id":"123","rules":[{"id":"x","metric_type":"temperature","sensor":"acpitz","threshold":1e999,"duration_minutes":1}]}`, "123:abc", false, true},
		{"wrong metric", `{"enabled":true,"chat_id":"123","rules":[{"id":"x","metric_type":"memory","sensor":"memory","threshold":50,"duration_minutes":1}]}`, "123:abc", false, true},
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
	private := filepath.Join(dir, "private.json")
	if err := os.WriteFile(private, []byte(strings.Replace(valid, `{`, `{"brevo_sender_email":"from@example.test","brevo_sender_name":"Monitor","brevo_recipient_email":"to@example.test",`, 1)), 0600); err != nil {
		t.Fatal(err)
	}
	publicLink := filepath.Join(public, "linked.json")
	if err := os.Symlink(private, publicLink); err != nil {
		t.Fatal(err)
	}
	if _, err := alerts.LoadConfig(publicLink, public, "123:abc", "test-key"); err == nil || strings.Contains(err.Error(), private) {
		t.Fatalf("public symlink accepted or private path disclosed: %v", err)
	}
	publicDirLink := filepath.Join(public, "linked-dir")
	if err := os.Symlink(dir, publicDirLink); err != nil {
		t.Fatal(err)
	}
	if _, err := alerts.LoadConfig(filepath.Join(publicDirLink, "private.json"), public, "123:abc", "test-key"); err == nil {
		t.Fatal("public symlink directory accepted")
	}
	cfg, err = alerts.LoadConfig(filepath.Join("..", "..", "alerts.example.json"), public, "", "")
	if err != nil || cfg.Enabled {
		t.Fatalf("example should be disabled: %+v %v", cfg, err)
	}
}

func TestDisabledConfigMustAlsoBePrivate(t *testing.T) {
	dir := t.TempDir()
	public := filepath.Join(dir, "static")
	if err := os.Mkdir(public, 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(public, "disabled.json")
	if err := os.WriteFile(path, []byte(`{"enabled":false,"chat_id":"private-destination"}`), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := alerts.LoadConfig(path, public, "", ""); err == nil || strings.Contains(err.Error(), "private-destination") {
		t.Fatalf("public configuration accepted or disclosed: %v", err)
	}
	link := filepath.Join(dir, "link.json")
	if err := os.Symlink(path, link); err != nil {
		t.Fatal(err)
	}
	if _, err := alerts.LoadConfig(link, public, "", ""); err == nil {
		t.Fatal("disabled configuration linked into public directory accepted")
	}
	private := filepath.Join(dir, "private.json")
	if err := os.WriteFile(private, []byte(`{"enabled":false}`), 0600); err != nil {
		t.Fatal(err)
	}
	publicLink := filepath.Join(public, "linked.json")
	if err := os.Symlink(private, publicLink); err != nil {
		t.Fatal(err)
	}
	if _, err := alerts.LoadConfig(publicLink, public, "", ""); err == nil {
		t.Fatal("disabled public link to private configuration accepted")
	}
}

func TestDisabledPrivateConfigWithMissingStatic(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "alerts.json")
	if err := os.WriteFile(path, []byte(`{"enabled":false,"chat_id":"private-destination"}`), 0600); err != nil {
		t.Fatal(err)
	}
	cfg, err := alerts.LoadConfig(path, filepath.Join(dir, "static"), "", "")
	if err != nil || cfg.Enabled {
		t.Fatalf("disabled private config: enabled=%v error=%v", cfg.Enabled, err)
	}
}

func TestExampleRules(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "alerts.example.json"))
	if err != nil {
		t.Fatal(err)
	}
	var example map[string]json.RawMessage
	if err := json.Unmarshal(data, &example); err != nil {
		t.Fatal(err)
	}
	if string(example["enabled"]) != "false" {
		t.Fatal("example must be disabled")
	}
	example["enabled"] = json.RawMessage(`true`)
	example["chat_id"] = json.RawMessage(`"123"`)
	example["brevo_sender_email"] = json.RawMessage(`"from@example.test"`)
	example["brevo_sender_name"] = json.RawMessage(`"Monitor"`)
	example["brevo_recipient_email"] = json.RawMessage(`"to@example.test"`)
	data, err = json.Marshal(example)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	public := filepath.Join(dir, "static")
	if err := os.Mkdir(public, 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "alerts.json")
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	cfg, err := alerts.LoadConfig(path, public, "123:fake", "test-key")
	if err != nil {
		t.Fatal(err)
	}
	want := []alerts.Rule{
		{ID: "cpu-high", MetricType: "cpu", Sensor: "cpu/total", Threshold: 50, Duration: 3 * time.Minute},
		{ID: "cpu-saturated", MetricType: "cpu", Sensor: "cpu/total", Threshold: 95, Duration: time.Minute},
		{ID: "temperature-high", MetricType: "temperature", Sensor: "coretemp/Package id 0", Threshold: 65, Duration: 2 * time.Minute},
	}
	if !reflect.DeepEqual(cfg.Rules, want) {
		t.Fatalf("rules = %+v, want %+v", cfg.Rules, want)
	}
}

func TestTelegramDestinations(t *testing.T) {
	dir := t.TempDir()
	public := filepath.Join(dir, "static")
	if err := os.Mkdir(public, 0700); err != nil {
		t.Fatal(err)
	}
	for _, tt := range []struct {
		name, chat string
		valid      bool
	}{
		{"user", "123456", true}, {"group", "-100123456", true}, {"channel", "@channel_name", true},
		{"garbage", "private destination", false}, {"unprefixed", "channel_name", false}, {"zero", "0", false},
		{"overflow", "99999999999999999999999", false}, {"whitespace", " 123", false}, {"control", "123\n", false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			body, err := json.Marshal(map[string]any{"enabled": true, "chat_id": tt.chat, "brevo_sender_email": "from@example.test", "brevo_sender_name": "Monitor", "brevo_recipient_email": "to@example.test", "rules": []map[string]any{{"id": "high", "metric_type": "cpu", "sensor": "cpu/total", "threshold": 50, "duration_minutes": 1}}})
			if err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(dir, "private.json")
			if err := os.WriteFile(path, body, 0600); err != nil {
				t.Fatal(err)
			}
			_, err = alerts.LoadConfig(path, public, "123:abc", "test-key")
			if (err == nil) != tt.valid {
				t.Fatalf("valid=%v, error=%v", tt.valid, err)
			}
			if err != nil && strings.Contains(err.Error(), tt.chat) {
				t.Fatal("destination disclosed")
			}
		})
	}
}
