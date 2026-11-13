package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"sensors-temp/internal/httpapi"
	"sensors-temp/internal/store"
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
	handler := staticHandler(public, "", nil)
	for _, path := range []string{"/linked.json", "/linked-dir/private.json"} {
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
		if w.Code == http.StatusOK || strings.Contains(w.Body.String(), "secret-destination") || strings.Contains(w.Body.String(), private) {
			t.Fatalf("private file exposed: %d %q", w.Code, w.Body.String())
		}
	}
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/public.txt", nil))
	if w.Code != http.StatusOK || w.Body.String() != "public" {
		t.Fatalf("public file not served: %d %q", w.Code, w.Body.String())
	}
	changing := filepath.Join(public, "changing.json")
	if err := os.Symlink(filepath.Join(public, "public.txt"), changing); err != nil {
		t.Fatal(err)
	}
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/changing.json", nil))
	if w.Code != http.StatusOK || w.Body.String() != "public" {
		t.Fatalf("internal link not served: %d %q", w.Code, w.Body.String())
	}
	if err := os.Remove(changing); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(private, changing); err != nil {
		t.Fatal(err)
	}
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/changing.json", nil))
	if w.Code == http.StatusOK || strings.Contains(w.Body.String(), "secret-destination") {
		t.Fatalf("retargeted link exposed private file: %d %q", w.Code, w.Body.String())
	}
}

func TestStaticHandlerProtectsSelectedFileAgainstLaterHardLinks(t *testing.T) {
	dir := t.TempDir()
	public := filepath.Join(dir, "static")
	if err := os.Mkdir(public, 0700); err != nil {
		t.Fatal(err)
	}
	private := filepath.Join(dir, "private.json")
	if err := os.WriteFile(private, []byte("secret-destination"), 0600); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(private)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Link(private, filepath.Join(public, "initial.json")); err != nil {
		t.Fatal(err)
	}
	handler := staticHandler(public, private, info)
	link := filepath.Join(public, "copy.json")
	if err := os.Link(private, link); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(link, filepath.Join(public, "alias.json")); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/initial.json", "/copy.json", "/alias.json"} {
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
		if w.Code == http.StatusOK || strings.Contains(w.Body.String(), "secret-destination") {
			t.Fatalf("file exposed: %d %q", w.Code, w.Body.String())
		}
	}
	replacement := filepath.Join(dir, "replacement.json")
	if err := os.WriteFile(replacement, []byte("new-secret"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(replacement, private); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(private, filepath.Join(public, "new.json")); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/initial.json", "/new.json", "/alias.json"} {
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
		if w.Code == http.StatusOK || strings.Contains(w.Body.String(), "secret") {
			t.Fatalf("file exposed after replacement: %d %q", w.Code, w.Body.String())
		}
	}
	if err := os.WriteFile(filepath.Join(public, "safe.txt"), []byte("safe"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(private); err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/safe.txt", nil))
	if w.Code == http.StatusOK {
		t.Fatal("served file while selected configuration cannot be checked")
	}
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/", nil))
	if w.Code == http.StatusOK {
		t.Fatal("served directory while selected configuration cannot be checked")
	}
}

func TestStaticStopsAfterSuccessiveConfigReplacements(t *testing.T) {
	dir := t.TempDir()
	public := filepath.Join(dir, "static")
	if err := os.Mkdir(public, 0700); err != nil {
		t.Fatal(err)
	}
	private := filepath.Join(dir, "alerts.json")
	write := func(path, contents string) {
		t.Helper()
		if err := os.WriteFile(path, []byte(contents), 0600); err != nil {
			t.Fatal(err)
		}
	}
	write(private, "config A")
	info, err := os.Stat(private)
	if err != nil {
		t.Fatal(err)
	}
	backup := filepath.Join(dir, "original.json")
	if err := os.Link(private, backup); err != nil {
		t.Fatal(err)
	}
	write(filepath.Join(public, "safe.txt"), "safe")
	handler := staticHandler(public, private, info)
	second := filepath.Join(dir, "second.json")
	write(second, "private B")
	if err := os.Rename(second, private); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(private, filepath.Join(public, "copy.json")); err != nil {
		t.Fatal(err)
	}
	third := filepath.Join(dir, "third.json")
	write(third, "config C")
	if err := os.Rename(third, private); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/copy.json", "/safe.txt"} {
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
		if w.Code == http.StatusOK || strings.Contains(w.Body.String(), "private B") {
			t.Fatalf("file served after replacement: %d %q", w.Code, w.Body.String())
		}
	}
	if err := os.Rename(backup, private); err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/safe.txt", nil))
	if w.Code == http.StatusOK {
		t.Fatal("static resumed without restart after config replacement")
	}
}

func TestStaticRejectsNewAliasWhenOriginalConfigRestoredBeforeRequest(t *testing.T) {
	dir := t.TempDir()
	public := filepath.Join(dir, "static")
	if err := os.Mkdir(public, 0700); err != nil {
		t.Fatal(err)
	}
	private := filepath.Join(dir, "alerts.json")
	if err := os.WriteFile(private, []byte("config A"), 0600); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(private)
	if err != nil {
		t.Fatal(err)
	}
	backup := filepath.Join(dir, "original")
	if err := os.Link(private, backup); err != nil {
		t.Fatal(err)
	}
	handler := staticHandler(public, private, info)
	b := filepath.Join(dir, "config-b")
	if err := os.WriteFile(b, []byte("private B"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(b, private); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(private, filepath.Join(public, "copy.json")); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(backup, private); err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/copy.json", nil))
	if w.Code == http.StatusOK || strings.Contains(w.Body.String(), "private B") {
		t.Fatalf("unobserved transition exposed data: %d %q", w.Code, w.Body.String())
	}
}

func TestSelectedConfigFreezesStaticFileIdentityAndContents(t *testing.T) {
	dir := t.TempDir()
	public := filepath.Join(dir, "static")
	if err := os.Mkdir(public, 0700); err != nil {
		t.Fatal(err)
	}
	private := filepath.Join(dir, "alerts.json")
	asset := filepath.Join(public, "asset.txt")
	if err := os.WriteFile(private, []byte("config"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(asset, []byte("public"), 0600); err != nil {
		t.Fatal(err)
	}
	configInfo, err := os.Stat(private)
	if err != nil {
		t.Fatal(err)
	}
	assetInfo, err := os.Stat(asset)
	if err != nil {
		t.Fatal(err)
	}
	handler := staticHandler(public, private, configInfo)
	request := func() *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/asset.txt", nil))
		return w
	}
	if w := request(); w.Code != http.StatusOK || w.Body.String() != "public" {
		t.Fatalf("original asset: %d %q", w.Code, w.Body.String())
	}
	if err := os.WriteFile(asset, []byte("secret"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(asset, assetInfo.ModTime(), assetInfo.ModTime()); err != nil {
		t.Fatal(err)
	}
	if w := request(); strings.Contains(w.Body.String(), "secret") || (w.Code == http.StatusOK && w.Body.String() != "public") {
		t.Fatalf("modified asset exposed: %d %q", w.Code, w.Body.String())
	}
}

func TestAbsoluteSymlinksInsideStaticRemainAvailable(t *testing.T) {
	dir := t.TempDir()
	public := filepath.Join(dir, "static")
	if err := os.Mkdir(public, 0700); err != nil {
		t.Fatal(err)
	}
	index := filepath.Join(public, "dashboard.html")
	if err := os.WriteFile(index, []byte("dashboard-content"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(index, filepath.Join(public, "index.html")); err != nil {
		t.Fatal(err)
	}
	private := filepath.Join(dir, "alerts.json")
	if err := os.WriteFile(private, []byte(`{"enabled":false}`), 0600); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(private)
	if err != nil {
		t.Fatal(err)
	}
	handler := staticHandler(public, private, info)
	for _, path := range []string{"/", "/index.html"} {
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
		if path == "/index.html" && w.Code == http.StatusMovedPermanently {
			continue
		}
		if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "dashboard-content") {
			t.Fatalf("%s: %d %q", path, w.Code, w.Body.String())
		}
	}
}

func TestMissingStaticDoesNotPreventAPIRoutes(t *testing.T) {
	public := filepath.Join(t.TempDir(), "static")
	st := store.NewSQLiteStore(filepath.Join(t.TempDir(), "test.db"))
	db := st.InitDB()
	if db == nil {
		t.Fatal("database initialization failed")
	}
	t.Cleanup(func() { db.Close() })
	mux := http.NewServeMux()
	httpapi.Register(mux, st, db)
	mux.Handle("/", staticHandler(public, "", nil))
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/current", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("API status = %d", w.Code)
	}
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/", nil))
	if w.Code != http.StatusNotFound {
		t.Fatalf("missing static status = %d", w.Code)
	}
	if err := os.Mkdir(public, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(public, "index.html"), []byte("dashboard-content"), 0600); err != nil {
		t.Fatal(err)
	}
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/", nil))
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "dashboard-content") {
		t.Fatalf("static after creation: %d %q", w.Code, w.Body.String())
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
