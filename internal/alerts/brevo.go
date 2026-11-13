package alerts

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"time"

	"sensors-temp/internal/logging"
)

// Brevo owns an independent bounded FIFO so a slow provider cannot block Telegram.
type Brevo struct {
	queue                     chan Event
	key, from, name, to, host string
	client                    *http.Client
	wait                      func(time.Duration)
}

func NewBrevo(key, from, name, to, host string) *Brevo {
	return newBrevo(key, from, name, to, host, &http.Client{}, queueCapacity, time.Sleep)
}

func newBrevo(key, from, name, to, host string, client *http.Client, capacity int, wait func(time.Duration)) *Brevo {
	c := *client
	c.Timeout = 10 * time.Second
	c.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	b := &Brevo{make(chan Event, capacity), key, from, name, to, host, &c, wait}
	go runDelivery("Brevo", b.queue, b.send, b.wait)
	return b
}

func (b *Brevo) Enqueue(e Event) {
	select {
	case b.queue <- e:
	default:
		logging.Logger.Error("Brevo delivery queue full; lost %s for rule %s", e.Kind, e.Rule.ID)
	}
}

func (b *Brevo) send(e Event) (bool, time.Duration) {
	type address struct {
		Email string `json:"email"`
		Name  string `json:"name,omitempty"`
	}
	body, err := json.Marshal(struct {
		Sender  address   `json:"sender"`
		To      []address `json:"to"`
		Subject string    `json:"subject"`
		Text    string    `json:"textContent"`
	}{address{b.from, b.name}, []address{{Email: b.to}}, "Alert " + e.Kind + ": " + e.Rule.ID, eventText(e, b.host)})
	if err != nil {
		return false, time.Second
	}
	req, err := http.NewRequest(http.MethodPost, "https://api.brevo.com/v3/smtp/email", bytes.NewReader(body))
	if err != nil {
		return false, time.Second
	}
	req.Header.Set("api-key", b.key)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	resp, err := b.client.Do(req)
	// Neither transport errors nor provider bodies are safe to log.
	if err != nil {
		return false, time.Second
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 65537))
	var result struct {
		MessageID string `json:"messageId"`
	}
	if err != nil || len(data) > 65536 || json.Unmarshal(data, &result) != nil {
		return false, retryDelay(resp.Header, 0)
	}
	return resp.StatusCode >= 200 && resp.StatusCode < 300 && strings.TrimSpace(result.MessageID) != "", retryDelay(resp.Header, 0)
}
