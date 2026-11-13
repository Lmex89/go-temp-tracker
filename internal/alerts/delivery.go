package alerts

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"sensors-temp/internal/logging"
)

const (
	queueCapacity = 64
	maxAttempts   = 3
	// maxRetryDelay caps provider wait hints (Retry-After / retry_after) so one
	// huge value cannot park a channel while its queue fills and drops alerts.
	maxRetryDelay = time.Minute
)

// queue is a bounded FIFO with one background worker, shared by Telegram and
// Brevo. Python analogy: a queue.Queue(maxsize=64) drained by a daemon thread.
// Each channel owns its own queue, so a slow provider cannot block the other.
type queue struct {
	channel string
	events  chan Event
}

// startQueue launches the worker goroutine ("go f()" is like starting a
// threading.Thread) that sends each event with up to maxAttempts tries.
func startQueue(channel string, capacity int, send func(Event) (bool, time.Duration), wait func(time.Duration)) queue {
	q := queue{channel, make(chan Event, capacity)}
	go func() {
		// range over a channel blocks for the next item, like "while True: q.get()".
		for e := range q.events {
			for attempt := 1; attempt <= maxAttempts; attempt++ {
				ok, delay := send(e)
				if ok {
					logging.Logger.Info("%s accepted %s for rule %s", channel, e.Kind, e.Rule.ID)
					break
				}
				if attempt == maxAttempts {
					logging.Logger.Error("%s delivery exhausted for %s rule %s", channel, e.Kind, e.Rule.ID)
					break
				}
				wait(delay)
			}
		}
	}()
	return q
}

// Enqueue never blocks the poller: select with default is a non-blocking
// put, like q.put_nowait() raising queue.Full in Python.
func (q queue) Enqueue(e Event) {
	select {
	case q.events <- e:
	default:
		logging.Logger.Error("%s delivery queue full; lost %s for rule %s", q.channel, e.Kind, e.Rule.ID)
	}
}

// safeClient copies the client so timeout and redirect policy cannot be
// weakened by callers, and redirects never resend credentials.
func safeClient(client *http.Client) *http.Client {
	c := *client
	c.Timeout = 10 * time.Second
	c.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &c
}

func eventText(e Event, host string) string {
	return fmt.Sprintf("%s\nHost: %s\nRule: %s\nSensor: %s\nValue: %g%s\nThreshold: %g%s\nDuration: %g minutes\nTime: %s", e.Kind, host, e.Rule.ID, e.Point.Sensor, e.Point.Value, e.Point.Unit, e.Rule.Threshold, e.Point.Unit, e.Rule.Duration.Minutes(), e.At.Format(time.RFC3339))
}

// retryDelay honors the provider's wait hint (body field or Retry-After
// header, seconds or HTTP date), defaulting to 1s and capped at maxRetryDelay.
func retryDelay(header http.Header, retryAfter int64) time.Duration {
	delay := time.Second
	if retryAfter > 0 {
		delay = retrySeconds(retryAfter)
	}
	if h := header.Get("Retry-After"); h != "" {
		if n, err := strconv.ParseInt(h, 10, 64); (err == nil || errors.Is(err, strconv.ErrRange)) && n > 0 {
			delay = max(delay, retrySeconds(n))
		} else if at, err := http.ParseTime(h); err == nil {
			delay = max(delay, time.Until(at))
		}
	}
	return min(delay, maxRetryDelay)
}

// retrySeconds converts without overflowing time.Duration (int64 nanoseconds).
func retrySeconds(n int64) time.Duration {
	if n >= int64(maxRetryDelay/time.Second) {
		return maxRetryDelay
	}
	return time.Duration(n) * time.Second
}
