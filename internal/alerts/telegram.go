package alerts

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"
)

// Telegram sends events through the Bot API. Embedding queue (a field with no
// name) promotes its Enqueue method, so callers use t.Enqueue(e) directly --
// similar to inheriting a method from a base class in Python.
type Telegram struct {
	queue
	token, chat, host string
	client            *http.Client
}

// NewTelegram starts the Telegram worker with production defaults.
func NewTelegram(token, chat, host string) *Telegram {
	return newTelegram(token, chat, host, &http.Client{}, queueCapacity, time.Sleep)
}

// newTelegram takes the client, capacity and wait function as parameters so
// tests can inject a fake transport and a no-op sleep (dependency injection).
func newTelegram(token, chat, host string, client *http.Client, capacity int, wait func(time.Duration)) *Telegram {
	t := &Telegram{token: token, chat: chat, host: host, client: safeClient(client)}
	// t.send is a "bound method" value, exactly like passing self.send in Python.
	t.queue = startQueue("Telegram", capacity, t.send, wait)
	return t
}

// send makes ONE delivery attempt and returns (accepted, wait before retry),
// a tuple-style return. The worker in delivery.go decides whether to retry.
func (t *Telegram) send(e Event) (bool, time.Duration) {
	text := eventText(e, t.host)
	body, err := json.Marshal(struct {
		Chat string `json:"chat_id"`
		Text string `json:"text"`
	}{t.chat, text})
	if err != nil {
		return false, time.Second
	}
	req, err := http.NewRequest(http.MethodPost, "https://api.telegram.org/bot"+t.token+"/sendMessage", bytes.NewReader(body))
	if err != nil {
		return false, time.Second
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := t.client.Do(req)
	// Transport errors can contain the token-bearing URL: never log them.
	if err != nil {
		return false, time.Second
	}
	defer resp.Body.Close()
	var result struct {
		OK         bool `json:"ok"`
		Parameters struct {
			RetryAfter int64 `json:"retry_after"`
		} `json:"parameters"`
	}
	responseBody, err := io.ReadAll(io.LimitReader(resp.Body, 65537))
	if err == nil && len(responseBody) <= 65536 {
		err = json.Unmarshal(responseBody, &result)
	} else if err == nil {
		err = errors.New("response too large")
	}
	delay := retryDelay(resp.Header, result.Parameters.RetryAfter)
	return err == nil && resp.StatusCode >= 200 && resp.StatusCode < 300 && result.OK, delay
}
