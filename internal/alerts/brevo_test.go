package alerts

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"sensors-temp/internal/metrics"
	"sensors-temp/internal/poller"
	"sensors-temp/internal/store"
)

func TestBrevoResponses(t *testing.T) {
	tests := []struct {
		name      string
		status    int
		body      string
		transport bool
		want      bool
	}{
		{"accepted", 201, `{"messageId":"id"}`, false, true},
		{"missing message ID", 201, `{}`, false, false},
		{"invalid JSON", 201, `{"messageId":"id"}garbage`, false, false},
		{"empty ID", 201, `{"messageId":" "}`, false, false},
		{"HTTP error", 429, `{"messageId":"id"}`, false, false},
		{"redirect", 302, `{"messageId":"id"}`, false, false},
		{"transport timeout", 0, "", true, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			calls := 0
			client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
				calls++
				if deadline, ok := r.Context().Deadline(); !ok || time.Until(deadline) <= 0 || time.Until(deadline) > 10*time.Second {
					t.Error("missing 10 second deadline")
				}
				if tt.transport {
					return nil, context.DeadlineExceeded
				}
				return &http.Response{StatusCode: tt.status, Header: http.Header{"Location": []string{"https://evil.invalid/"}}, Body: io.NopCloser(strings.NewReader(tt.body)), Request: r}, nil
			})}
			b := newBrevo("test-key", "from@example.test", "Monitor", "to@example.test", "host", client, 1, func(time.Duration) {})
			ok, _ := b.send(Event{Kind: "activation", Rule: Rule{ID: "high"}, At: time.Now()})
			if ok != tt.want || calls != 1 {
				t.Fatalf("accepted %v, calls %d", ok, calls)
			}
		})
	}
}

func TestBrevoRetriesAndOrder(t *testing.T) {
	requests := make(chan string, 4)
	waits := make(chan time.Duration, 2)
	calls := 0
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		calls++
		data, _ := io.ReadAll(r.Body)
		requests <- string(data)
		if calls <= 2 {
			return &http.Response{StatusCode: 429, Header: http.Header{"Retry-After": []string{"7"}}, Body: io.NopCloser(strings.NewReader(`{"error":"private"}`))}, nil
		}
		return &http.Response{StatusCode: 201, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"messageId":"id"}`))}, nil
	})}
	b := newBrevo("test-key", "from@example.test", "Monitor", "to@example.test", "host", client, 4, func(d time.Duration) { waits <- d })
	b.Enqueue(Event{Kind: "activation", Rule: Rule{ID: "high"}, At: time.Now()})
	b.Enqueue(Event{Kind: "recovery", Rule: Rule{ID: "high"}, At: time.Now()})
	for i := 0; i < 2; i++ {
		select {
		case d := <-waits:
			if d != 7*time.Second {
				t.Fatalf("delay = %s", d)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("retry stalled")
		}
	}
	for i := 0; i < 4; i++ {
		select {
		case body := <-requests:
			kind := "activation"
			if i == 3 {
				kind = "recovery"
			}
			if !strings.Contains(body, kind) {
				t.Fatalf("request %d not %s", i, kind)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("delivery stalled")
		}
	}
}

func TestProviderIsolationAndSaturation(t *testing.T) {
	if os.Getenv("BREVO_QUEUE_TEST") != "1" {
		cmd := exec.Command(os.Args[0], "-test.run=^TestProviderIsolationAndSaturation$")
		cmd.Env = append(os.Environ(), "BREVO_QUEUE_TEST=1")
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("child: %v: %s", err, out)
		}
		if !strings.Contains(string(out), "Brevo delivery queue full; lost") {
			t.Fatalf("missing saturation log: %s", out)
		}
		for _, secret := range []string{"test-key", "from@example.test", "to@example.test", "private-chat"} {
			if strings.Contains(string(out), secret) {
				t.Fatalf("secret leaked: %s", out)
			}
		}
		return
	}
	st := store.NewSQLiteStore(filepath.Join(t.TempDir(), "readings.db"))
	db := st.InitDB()
	if db == nil {
		t.Fatal("database initialization failed")
	}
	t.Cleanup(func() { db.Close() })
	started, release := make(chan struct{}), make(chan struct{})
	tgRequests, brevoRequests := make(chan string, 4), make(chan string, 2)
	brevoCalls := 0
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		body, _ := io.ReadAll(r.Body)
		if r.URL.Host == "api.telegram.org" {
			tgRequests <- string(body)
			return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"ok":true}`))}, nil
		}
		brevoCalls++
		if brevoCalls == 1 {
			close(started)
			<-release
		}
		brevoRequests <- string(body)
		return &http.Response{StatusCode: 201, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"messageId":"id"}`))}, nil
	})}
	tg := newTelegram("123:secret", "private-chat", "host", client, 8, func(time.Duration) {})
	b := newBrevo("test-key", "from@example.test", "Monitor", "to@example.test", "host", client, 1, func(time.Duration) {})
	e := NewEvaluator([]Rule{{ID: "high", MetricType: "cpu", Sensor: "cpu/total", Threshold: 50, Duration: 5 * time.Second}}, func(e Event) { tg.Enqueue(e); b.Enqueue(e) })
	start := time.Now()
	for i, v := range []float64{50, 50, 49, 50, 50, 49} {
		now := start.Add(time.Duration(i) * 5 * time.Second)
		poller.PollMetric(st, db, "cpu", 24, func() []metrics.MetricPoint { return []metrics.MetricPoint{{Sensor: "cpu/total", Value: v, Unit: "%"}} }, func(p []metrics.MetricPoint) { e.Observe("cpu", now, p) })
		if i == 1 {
			select {
			case <-started:
			case <-time.After(5 * time.Second):
				t.Fatal("Brevo did not start")
			}
		}
	}
	for _, kind := range []string{"activation", "recovery", "activation", "recovery"} {
		select {
		case body := <-tgRequests:
			if !strings.Contains(body, kind) {
				t.Fatalf("wrong Telegram order")
			}
		case <-time.After(5 * time.Second):
			t.Fatal("Telegram blocked by Brevo")
		}
	}
	rows, err := st.Query(db, store.TimeWindow{Hours: 1}, "cpu")
	if err != nil || len(rows) != 6 {
		t.Fatalf("stored %d rows: %v", len(rows), err)
	}
	close(release)
	for _, kind := range []string{"activation", "recovery"} {
		select {
		case body := <-brevoRequests:
			if !strings.Contains(body, kind) {
				t.Fatal("wrong Brevo order")
			}
		case <-time.After(5 * time.Second):
			t.Fatal("Brevo stalled")
		}
	}
}

func TestTelegramBlockedBrevoExhaustsIndependently(t *testing.T) {
	if os.Getenv("BREVO_EXHAUST_TEST") != "1" {
		cmd := exec.Command(os.Args[0], "-test.run=^TestTelegramBlockedBrevoExhaustsIndependently$")
		cmd.Env = append(os.Environ(), "BREVO_EXHAUST_TEST=1")
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("child: %v: %s", err, out)
		}
		if !strings.Contains(string(out), "Brevo delivery exhausted") {
			t.Fatalf("missing exhaustion log: %s", out)
		}
		for _, secret := range []string{"test-key", "from@example.test", "to@example.test", "private-chat", "private body"} {
			if strings.Contains(string(out), secret) {
				t.Fatalf("secret leaked: %s", out)
			}
		}
		return
	}
	st := store.NewSQLiteStore(filepath.Join(t.TempDir(), "readings.db"))
	db := st.InitDB()
	if db == nil {
		t.Fatal("database initialization failed")
	}
	t.Cleanup(func() { db.Close() })
	started, release := make(chan struct{}), make(chan struct{})
	tgRequests, brevoRequests := make(chan string, 2), make(chan string, 4)
	waits := make(chan time.Duration, 2)
	brevoCalls, tgCalls := 0, 0
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		body, _ := io.ReadAll(r.Body)
		if r.URL.Host == "api.telegram.org" {
			tgCalls++
			if tgCalls == 1 {
				close(started)
				<-release
			}
			tgRequests <- string(body)
			return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"ok":true}`))}, nil
		}
		brevoCalls++
		brevoRequests <- string(body)
		if brevoCalls == 1 {
			return nil, errors.New("test-key from@example.test to@example.test private body")
		}
		if brevoCalls < 4 {
			return &http.Response{StatusCode: 500, Header: make(http.Header), Body: io.NopCloser(strings.NewReader("private body test-key"))}, nil
		}
		return &http.Response{StatusCode: 201, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"messageId":"id"}`))}, nil
	})}
	tg := newTelegram("123:secret", "private-chat", "host", client, 4, func(time.Duration) {})
	b := newBrevo("test-key", "from@example.test", "Monitor", "to@example.test", "host", client, 4, func(d time.Duration) { waits <- d })
	e := NewEvaluator([]Rule{{ID: "high", MetricType: "cpu", Sensor: "cpu/total", Threshold: 50, Duration: 5 * time.Second}}, func(e Event) { tg.Enqueue(e); b.Enqueue(e) })
	start := time.Now()
	for i, v := range []float64{50, 50, 49} {
		now := start.Add(time.Duration(i) * 5 * time.Second)
		poller.PollMetric(st, db, "cpu", 24, func() []metrics.MetricPoint { return []metrics.MetricPoint{{Sensor: "cpu/total", Value: v, Unit: "%"}} }, func(p []metrics.MetricPoint) { e.Observe("cpu", now, p) })
		if i == 1 {
			select {
			case <-started:
			case <-time.After(5 * time.Second):
				t.Fatal("Telegram did not start")
			}
		}
	}
	for i := 0; i < 4; i++ {
		select {
		case body := <-brevoRequests:
			kind := "activation"
			if i == 3 {
				kind = "recovery"
			}
			if !strings.Contains(body, kind) {
				t.Fatal("wrong Brevo order")
			}
		case <-time.After(5 * time.Second):
			t.Fatal("Brevo blocked by Telegram")
		}
	}
	for i := 0; i < 2; i++ {
		select {
		case d := <-waits:
			if d != time.Second {
				t.Fatalf("retry delay %s", d)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("missing retry")
		}
	}
	close(release)
	for _, kind := range []string{"activation", "recovery"} {
		select {
		case body := <-tgRequests:
			if !strings.Contains(body, kind) {
				t.Fatal("wrong Telegram order")
			}
		case <-time.After(5 * time.Second):
			t.Fatal("Telegram stalled")
		}
	}
	rows, err := st.Query(db, store.TimeWindow{Hours: 1}, "cpu")
	if err != nil || len(rows) != 3 {
		t.Fatalf("stored %d rows: %v", len(rows), err)
	}
}

func TestPollerToTelegramAndBrevo(t *testing.T) {
	st := store.NewSQLiteStore(filepath.Join(t.TempDir(), "readings.db"))
	db := st.InitDB()
	if db == nil {
		t.Fatal("database initialization failed")
	}
	t.Cleanup(func() { db.Close() })
	telegram, email := make(chan string, 2), make(chan string, 2)
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Error(err)
		}
		switch r.URL.Host {
		case "api.telegram.org":
			telegram <- string(body)
			return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"ok":true}`))}, nil
		case "api.brevo.com":
			if r.URL.Path != "/v3/smtp/email" || r.Header.Get("api-key") != "test-key" {
				t.Error("incorrect Brevo endpoint or authentication")
			}
			var payload struct {
				Sender struct {
					Email string `json:"email"`
					Name  string `json:"name"`
				} `json:"sender"`
				To []struct {
					Email string `json:"email"`
				} `json:"to"`
				Subject string `json:"subject"`
				Text    string `json:"textContent"`
			}
			if err := json.Unmarshal(body, &payload); err != nil {
				t.Error(err)
			}
			if payload.Sender.Email != "from@example.test" || payload.Sender.Name != "Monitor" || len(payload.To) != 1 || payload.To[0].Email != "to@example.test" {
				t.Error("incorrect email addresses")
			}
			email <- payload.Subject + " " + payload.Text
			return &http.Response{StatusCode: 201, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"messageId":"accepted"}`))}, nil
		default:
			t.Error("unexpected provider")
			return nil, io.ErrUnexpectedEOF
		}
	})}
	tg := newTelegram("123:secret", "chat", "machine", client, 4, func(time.Duration) { t.Error("unexpected retry") })
	b := newBrevo("test-key", "from@example.test", "Monitor", "to@example.test", "machine", client, 4, func(time.Duration) { t.Error("unexpected retry") })
	e := NewEvaluator([]Rule{{ID: "high", MetricType: "cpu", Sensor: "cpu/total", Threshold: 50, Duration: time.Minute}}, func(e Event) { tg.Enqueue(e); b.Enqueue(e) })
	start := time.Now()
	for i := 0; i <= 13; i++ {
		value := 50.0
		if i == 13 {
			value = 49
		}
		now := start.Add(time.Duration(i) * 5 * time.Second)
		poller.PollMetric(st, db, "cpu", 24, func() []metrics.MetricPoint {
			return []metrics.MetricPoint{{Sensor: "cpu/total", Value: value, Unit: "%"}}
		}, func(p []metrics.MetricPoint) { e.Observe("cpu", now, p) })
	}
	for _, kind := range []string{"activation", "recovery"} {
		for _, ch := range []chan string{telegram, email} {
			select {
			case body := <-ch:
				for _, part := range []string{kind, "machine", "high", "cpu/total", "Threshold: 50%", "Duration: 1 minutes", "Time:"} {
					if !strings.Contains(body, part) {
						t.Errorf("missing %q", part)
					}
				}
			case <-time.After(5 * time.Second):
				t.Fatal("delivery stalled")
			}
		}
	}
	rows, err := st.Query(db, store.TimeWindow{Hours: 1}, "cpu")
	if err != nil || len(rows) != 14 {
		t.Fatalf("stored %d rows: %v", len(rows), err)
	}
}
