package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestStaticHandlerDoesNotFollowSymlinksOutsideRoot(t *testing.T) {
	dir := t.TempDir()
	public := filepath.Join(dir, "static")
	if err := os.Mkdir(public, 0700); err != nil {
		t.Fatal(err)
	}
	private := filepath.Join(dir, "private.json")
	if err := os.WriteFile(private, []byte("secret-destination"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(private, filepath.Join(public, "linked.json")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(dir, filepath.Join(public, "linked-dir")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(public, "public.txt"), []byte("public"), 0600); err != nil {
		t.Fatal(err)
	}
	root, err := os.OpenRoot(public)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	for _, path := range []string{"/linked.json", "/linked-dir/private.json"} {
		w := httptest.NewRecorder()
		staticHandler(root).ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
		if w.Code == http.StatusOK || strings.Contains(w.Body.String(), "secret-destination") || strings.Contains(w.Body.String(), private) {
			t.Fatalf("private file exposed: %d %q", w.Code, w.Body.String())
		}
	}
	w := httptest.NewRecorder()
	staticHandler(root).ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/public.txt", nil))
	if w.Code != http.StatusOK || w.Body.String() != "public" {
		t.Fatalf("public file not served: %d %q", w.Code, w.Body.String())
	}
}

// TestGetEnvInt checks the env-var parsing contract: unset or invalid values
// fall back to the default (like int(os.getenv(k) or d) but without raising).
func TestGetEnvInt(t *testing.T) {
	tests := []struct {
		name string
		env  string
		want int
	}{
		{"unset uses default", "", 60},
		{"valid value", "15", 15},
		{"invalid falls back", "abc", 60},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// t.Setenv restores the variable after the test (like monkeypatch.setenv).
			t.Setenv("TEST_POLL_INTERVAL", tt.env)
			if got := getEnvInt("TEST_POLL_INTERVAL", 60); got != tt.want {
				t.Errorf("got %d, want %d", got, tt.want)
			}
		})
	}
}

func TestAlertPollingIntervals(t *testing.T) {
	for _, tt := range []struct {
		enabled   bool
		temp, cpu int
	}{
		{false, 37, 83},
		{true, 5, 5},
	} {
		temp, cpu := pollingIntervals(tt.enabled, 37, 83)
		if temp != tt.temp || cpu != tt.cpu {
			t.Fatalf("enabled %v: got %d, %d; want %d, %d", tt.enabled, temp, cpu, tt.temp, tt.cpu)
		}
	}
}

func TestInvalidAlertsFailBeforeDatabase(t *testing.T) {
	if os.Getenv("ALERT_STARTUP_TEST") == "1" {
		os.Args = []string{"temp-tracker", "-alerts-config", "alerts.json"}
		main()
		return
	}
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "static"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "alerts.json"), []byte(`{"enabled":true,"chat_id":"123","brevo_sender_email":"from@example.test","brevo_sender_name":"Monitor","brevo_recipient_email":"to@example.test","rules":[{"id":"high","metric_type":"cpu","sensor":"cpu/total","threshold":50,"duration_minutes":1}]}`), 0600); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(os.Args[0], "-test.run=^TestInvalidAlertsFailBeforeDatabase$")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "ALERT_STARTUP_TEST=1", "TELEGRAM_BOT_TOKEN=123:test", "BREVO_API_KEY=", "DB_DRIVER=sqlite")
	if out, err := cmd.CombinedOutput(); err == nil || !strings.Contains(string(out), "valid BREVO_API_KEY is required") {
		t.Fatalf("startup did not reject configuration: %v: %s", err, out)
	}
	if _, err := os.Stat(filepath.Join(dir, "temps.db")); !os.IsNotExist(err) {
		t.Fatalf("database created or stat failed: %v", err)
	}
}
