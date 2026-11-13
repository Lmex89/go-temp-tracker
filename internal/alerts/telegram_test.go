package alerts

import (
	"errors"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"sensors-temp/internal/logging"
	"sensors-temp/internal/metrics"
	"sensors-temp/internal/poller"
	"sensors-temp/internal/store"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestTelegramRetriesAndOrder(t *testing.T) {
	var mu sync.Mutex
	var bodies []string
	var waits []time.Duration
	done := make(chan struct{})
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Error(err)
		}
		if r.URL.Host != "api.telegram.org" || !strings.Contains(r.URL.Path, "/bot123:secret/sendMessage") {
			t.Errorf("unexpected endpoint")
		}
		mu.Lock()
		bodies = append(bodies, string(body))
		n := len(bodies)
		mu.Unlock()
		if n == 4 {
			close(done)
		}
		if n < 3 {
			return &http.Response{StatusCode: 429, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"ok":false,"parameters":{"retry_after":7}}`))}, nil
		}
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"ok":true}`))}, nil
	})}
	s := newTelegram("123:secret", "private-chat", "host", client, 4, func(d time.Duration) { mu.Lock(); waits = append(waits, d); mu.Unlock() })
	s.Enqueue(Event{Kind: "activation", Rule: Rule{ID: "high", Sensor: "cpu/total", Threshold: 50, Duration: time.Minute}, At: time.Now()})
	s.Enqueue(Event{Kind: "recovery", Rule: Rule{ID: "high", Sensor: "cpu/total", Threshold: 50, Duration: time.Minute}, At: time.Now()})
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("delivery stalled")
	}
	mu.Lock()
	defer mu.Unlock()
	if len(bodies) != 4 || !strings.Contains(bodies[0], "activation") || !strings.Contains(bodies[3], "recovery") {
		t.Fatalf("wrong order or attempts: %v", bodies)
	}
	if len(waits) != 2 || waits[0] != 7*time.Second || waits[1] != 7*time.Second {
		t.Fatalf("waits = %v", waits)
	}
}

type exhaustedWriter struct{ done chan struct{} }

func (w exhaustedWriter) Write(p []byte) (int, error) {
	n, err := os.Stdout.Write(p)
	if strings.Contains(string(p), "delivery exhausted") {
		close(w.done)
	}
	return n, err
}

func TestTelegramExhaustionDoesNotLeakSecrets(t *testing.T) {
	if os.Getenv("ALERT_LOG_TEST") == "1" {
		done := make(chan struct{})
		logging.Logger = logging.New(exhaustedWriter{done}, "DEBUG")
		attempts := 0
		client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			attempts++
			return nil, errors.New("transport exposed " + r.URL.String() + " private-chat")
		})}
		s := newTelegram("123:secret-token", "private-chat", "host", client, 1, func(time.Duration) {})
		s.Enqueue(Event{Kind: "activation", Rule: Rule{ID: "high"}, At: time.Now()})
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Fatal("delivery stalled")
		}
		if attempts != 3 {
			t.Fatalf("attempts: %d", attempts)
		}
		return
	}
	cmd := exec.Command(os.Args[0], "-test.run=^TestTelegramExhaustionDoesNotLeakSecrets$")
	cmd.Env = append(os.Environ(), "ALERT_LOG_TEST=1")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("child: %v: %s", err, out)
	}
	if !strings.Contains(string(out), "delivery exhausted") {
		t.Fatalf("missing exhaustion log: %s", out)
	}
	for _, secret := range []string{"secret-token", "private-chat", "api.telegram.org"} {
		if strings.Contains(string(out), secret) {
			t.Fatalf("secret leaked: %s", out)
		}
	}
}

func TestPollerToTelegramAndSQLite(t *testing.T) {
	st := store.NewSQLiteStore(filepath.Join(t.TempDir(), "readings.db"))
	db := st.InitDB()
	if db == nil {
		t.Fatal("database initialization failed")
	}
	t.Cleanup(func() { db.Close() })
	requests := make(chan string, 2)
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Error(err)
		}
		requests <- string(body)
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"ok":true}`))}, nil
	})}
	s := newTelegram("123:secret", "chat", "machine", client, 4, func(time.Duration) { t.Error("unexpected retry") })
	e := NewEvaluator([]Rule{{ID: "high", Sensor: "cpu/total", Threshold: 50, Duration: time.Minute}}, s.Enqueue)
	start := time.Now()
	for i := 0; i <= 13; i++ {
		value := 50.0
		if i == 13 {
			value = 49
		}
		now := start.Add(time.Duration(i) * 5 * time.Second)
		poller.PollMetric(st, db, "cpu", 24, func() []metrics.MetricPoint {
			return []metrics.MetricPoint{{Sensor: "cpu/total", Value: value, Unit: "%"}}
		}, func(p []metrics.MetricPoint) { e.Observe(now, p) })
	}
	for _, kind := range []string{"activation", "recovery"} {
		select {
		case body := <-requests:
			for _, part := range []string{kind, "machine", "high", "cpu/total", "Threshold: 50%", "Duration: 1 minutes", "Time:", `"chat_id":"chat"`} {
				if !strings.Contains(body, part) {
					t.Errorf("missing %q in %s", part, body)
				}
			}
		case <-time.After(5 * time.Second):
			t.Fatal("delivery stalled")
		}
	}
	rows, err := st.Query(db, store.TimeWindow{Hours: 1}, "cpu")
	if err != nil || len(rows) != 14 {
		t.Fatalf("stored %d rows: %v", len(rows), err)
	}
}

func TestTelegramResponseAndSecretSafety(t *testing.T) {
	tests := []struct {
		name         string
		status       int
		body         string
		transportErr bool
		want         bool
	}{
		{"accepted", 200, `{"ok":true}`, false, true},
		{"rejected", 200, `{"ok":false}`, false, false},
		{"invalid body", 200, `oops`, false, false},
		{"trailing invalid data", 200, `{"ok":true}junk`, false, false},
		{"server error", 500, `{"ok":true}`, false, false},
		{"transport", 0, "", true, false},
		{"redirect", 302, `{"ok":true}`, false, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			calls := 0
			client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
				calls++
				if tt.transportErr {
					return nil, errors.New("secret token and private chat")
				}
				return &http.Response{StatusCode: tt.status, Header: http.Header{"Location": []string{"https://evil.invalid/"}}, Body: io.NopCloser(strings.NewReader(tt.body)), Request: r}, nil
			})}
			s := newTelegram("123:secret", "private-chat", "host", client, 1, func(time.Duration) {})
			if s.client.Timeout != 10*time.Second {
				t.Fatalf("timeout %s", s.client.Timeout)
			}
			ok, _ := s.send(Event{Kind: "activation", Rule: Rule{ID: "x"}, At: time.Now()})
			if ok != tt.want || calls != 1 {
				t.Fatalf("accepted %v, calls %d", ok, calls)
			}
		})
	}
}

func TestTelegramQueueIsBounded(t *testing.T) {
	if os.Getenv("ALERT_QUEUE_TEST") != "1" {
		cmd := exec.Command(os.Args[0], "-test.run=^TestTelegramQueueIsBounded$")
		cmd.Env = append(os.Environ(), "ALERT_QUEUE_TEST=1")
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("child: %v: %s", err, out)
		}
		if !strings.Contains(string(out), "delivery queue full; lost") {
			t.Fatalf("missing saturation log: %s", out)
		}
		return
	}
	started, release, sent := make(chan struct{}), make(chan struct{}), make(chan struct{}, 2)
	var once sync.Once
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		once.Do(func() { close(started); <-release })
		sent <- struct{}{}
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"ok":true}`))}, nil
	})}
	s := newTelegram("123:secret", "chat", "host", client, 1, func(time.Duration) {})
	e := Event{Kind: "activation", Rule: Rule{ID: "x"}, At: time.Now()}
	s.Enqueue(e)
	<-started
	s.Enqueue(e)
	s.Enqueue(e)
	close(release)
	for i := 0; i < 2; i++ {
		select {
		case <-sent:
		case <-time.After(5 * time.Second):
			t.Fatal("delivery stalled")
		}
	}
}
