package alerts

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"

	"sensors-temp/internal/logging"
)

const queueCapacity = 64

// Telegram keeps a bounded FIFO queue; the poller never waits for the network.
type Telegram struct {
	queue             chan Event
	token, chat, host string
	client            *http.Client
	wait              func(time.Duration)
}

func NewTelegram(token, chat, host string) *Telegram {
	return newTelegram(token, chat, host, &http.Client{}, queueCapacity, time.Sleep)
}

func newTelegram(token, chat, host string, client *http.Client, capacity int, wait func(time.Duration)) *Telegram {
	// Copy the client so timeout and redirect policy cannot be weakened by callers.
	c := *client
	c.Timeout = 10 * time.Second
	c.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	t := &Telegram{make(chan Event, capacity), token, chat, host, &c, wait}
	go t.run()
	return t
}

func (t *Telegram) Enqueue(e Event) {
	select {
	case t.queue <- e:
	default:
		logging.Logger.Error("Alert delivery queue full; lost %s for rule %s", e.Kind, e.Rule.ID)
	}
}

func (t *Telegram) run() {
	for e := range t.queue {
		for attempt := 1; attempt <= 3; attempt++ {
			ok, delay := t.send(e)
			if ok {
				logging.Logger.Info("Telegram accepted %s for rule %s", e.Kind, e.Rule.ID)
				break
			}
			if attempt == 3 {
				logging.Logger.Error("Telegram delivery exhausted for %s rule %s", e.Kind, e.Rule.ID)
				break
			}
			t.wait(delay)
		}
	}
}

func (t *Telegram) send(e Event) (bool, time.Duration) {
	text := fmt.Sprintf("%s\nHost: %s\nRule: %s\nSensor: %s\nValue: %g%s\nThreshold: %g%%\nDuration: %g minutes\nTime: %s", e.Kind, t.host, e.Rule.ID, e.Point.Sensor, e.Point.Value, e.Point.Unit, e.Rule.Threshold, e.Rule.Duration.Minutes(), e.At.Format(time.RFC3339))
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
	delay := time.Second
	if result.Parameters.RetryAfter > 0 {
		delay = retrySeconds(result.Parameters.RetryAfter)
	}
	if h := resp.Header.Get("Retry-After"); h != "" {
		if n, err := strconv.ParseInt(h, 10, 64); (err == nil || errors.Is(err, strconv.ErrRange)) && n > 0 {
			if d := retrySeconds(n); d > delay {
				delay = d
			}
		} else if at, err := http.ParseTime(h); err == nil && time.Until(at) > delay {
			delay = time.Until(at)
		}
	}
	return err == nil && resp.StatusCode >= 200 && resp.StatusCode < 300 && result.OK, delay
}

func retrySeconds(n int64) time.Duration {
	if n > (1<<63-1)/int64(time.Second) {
		return time.Duration(1<<63 - 1)
	}
	return time.Duration(n) * time.Second
}
