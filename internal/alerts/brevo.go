package alerts

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"time"
)

// Brevo sends events by e-mail through its HTTPS API. It embeds its own queue,
// independent of Telegram's, so a slow provider cannot block the other channel.
type Brevo struct {
	queue
	key, from, name, to, host string
	client                    *http.Client
}

// NewBrevo starts the Brevo worker with production defaults.
func NewBrevo(key, from, name, to, host string) *Brevo {
	return newBrevo(key, from, name, to, host, &http.Client{}, queueCapacity, time.Sleep)
}

// newBrevo mirrors newTelegram: injectable client, capacity and wait for tests.
func newBrevo(key, from, name, to, host string, client *http.Client, capacity int, wait func(time.Duration)) *Brevo {
	b := &Brevo{key: key, from: from, name: name, to: to, host: host, client: safeClient(client)}
	b.queue = startQueue("Brevo", capacity, b.send, wait)
	return b
}

// send makes ONE delivery attempt; see Telegram.send. The local "type address"
// is scoped to this function, like a class defined inside a Python function.
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
	}{address{b.from, b.name}, []address{{Email: b.to}}, "Alert " + string(e.Kind) + ": " + e.Rule.ID, eventText(e, b.host)})
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
