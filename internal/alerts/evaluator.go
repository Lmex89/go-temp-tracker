package alerts

import (
	"math"
	"sync"
	"time"

	"sensors-temp/internal/logging"
	"sensors-temp/internal/metrics"
	"sensors-temp/internal/timeutil"
)

// EventKind says whether an alert started or ended. A named string type is
// Go's lightweight enum (like class EventKind(str, Enum) in Python).
type EventKind string

const (
	Activation EventKind = "activation"
	Recovery   EventKind = "recovery"
)

// Event is what the evaluator hands to the delivery queues.
type Event struct {
	Kind  EventKind
	Rule  Rule
	Point metrics.MetricPoint
	At    time.Time
}

// ruleState is the in-memory progress of one rule. pending is the start of
// the current above-threshold streak (zero time = no streak, like None);
// last is the normalized acquisition window of the previous valid reading.
type ruleState struct {
	rule    Rule
	pending time.Time
	last    timeutil.Observation
	active  bool
	lost    bool
}

// reset forgets the streak and the previous reading after a gap or bad data.
func (s *ruleState) reset() {
	s.pending, s.last = time.Time{}, timeutil.Observation{}
}

// Evaluator protects independent rule states shared by the polling goroutines.
// emit is a callback (a plain function value, like passing a lambda in Python)
// that main wires to the Telegram and Brevo queues.
type Evaluator struct {
	mu     sync.Mutex
	states []ruleState
	emit   func(Event)
}

func NewEvaluator(rules []Rule, emit func(Event)) *Evaluator {
	e := &Evaluator{emit: emit}
	for _, r := range rules {
		e.states = append(e.states, ruleState{rule: r})
	}
	return e
}

// Observe requires a fresh reading; a missing point never means recovery.
func (e *Evaluator) Observe(metricType string, now time.Time, points []metrics.MetricPoint) {
	e.observe(metricType, now, points, timeutil.BootTime)
}

// ObserveAt is Observe with an explicit suspend-aware clock. It stays exported
// because the cross-package Poller -> rules tests need a controlled clock
// (in Python you would monkeypatch the clock instead).
func (e *Evaluator) ObserveAt(metricType string, now time.Time, bootNow time.Duration, points []metrics.MetricPoint) {
	e.observe(metricType, now, points, func() time.Duration { return bootNow })
}

func (e *Evaluator) observe(metricType string, now time.Time, points []metrics.MetricPoint, bootClock func() time.Duration) {
	// Both pollers call this from their own goroutines; the mutex is the Go
	// version of "with self._lock:".
	e.mu.Lock()
	defer e.mu.Unlock()
	for i := range e.states {
		// Take a pointer so changes stick; ranging by value would edit a copy.
		s := &e.states[i]
		if s.rule.MetricType != metricType {
			continue
		}
		var point metrics.MetricPoint
		found := false
		for _, p := range points {
			if p.Sensor == s.rule.Sensor {
				point, found = p, true
				break
			}
		}
		if !found || math.IsNaN(point.Value) || math.IsInf(point.Value, 0) {
			s.reset()
			if !s.lost {
				logging.Logger.Warn("Alert observation lost for rule %s", s.rule.ID)
				s.lost = true
			}
			continue
		}
		at := now
		if !point.At.IsZero() {
			at = point.At
		}
		startedAt := point.StartedAt
		if startedAt.IsZero() {
			startedAt = at
		}
		if point.BootAt < 0 {
			s.reset()
			if !s.lost {
				if point.BootAt == timeutil.ClockUnavailable {
					logging.Logger.Warn("Alert observation clock unavailable for rule %s", s.rule.ID)
				} else {
					logging.Logger.Warn("Alert observation timing uncertain for rule %s", s.rule.ID)
				}
				s.lost = true
			}
			continue
		}
		bootStart := point.BootStart
		if bootStart <= 0 {
			bootStart = point.BootAt
		}
		stale := startedAt.After(at) || now.Before(at) || now.Sub(startedAt) > 10*time.Second
		if point.BootAt > 0 {
			bootNow := bootClock()
			stale = stale || bootNow <= 0 || point.BootAt > bootNow || bootStart > point.BootAt || bootNow-bootStart > 10*time.Second
		}
		if stale {
			s.reset()
			if !s.lost {
				logging.Logger.Warn("Alert observation stale for rule %s", s.rule.ID)
				s.lost = true
			}
			continue
		}
		last := s.last
		bootGap := (point.BootAt == 0) != (last.BootAt == 0) ||
			(point.BootAt > 0 && last.BootAt > 0 && (bootStart-last.BootStart > 10*time.Second || point.BootAt-last.BootAt > 10*time.Second || bootStart < last.BootStart || point.BootAt < last.BootAt))
		if !last.StartedAt.IsZero() && (startedAt.Sub(last.StartedAt) > 10*time.Second || at.Sub(last.At) > 10*time.Second || startedAt.Before(last.StartedAt) || at.Before(last.At) || bootGap) {
			s.pending = time.Time{}
			if !s.lost {
				logging.Logger.Warn("Alert observation gap for rule %s", s.rule.ID)
			}
			s.lost = true
		} else {
			s.lost = false
		}
		s.last = timeutil.Observation{StartedAt: startedAt, At: at, BootStart: bootStart, BootAt: point.BootAt}
		if point.Value < s.rule.Threshold {
			s.pending = time.Time{}
			if s.active {
				s.active = false
				e.event(Recovery, s.rule, point, at)
			}
			continue
		}
		if s.active {
			continue
		}
		if s.pending.IsZero() {
			s.pending = at
			continue
		}
		// Conservative on purpose: the streak counts only up to the START of
		// the current reading, so a 1s CPU sample cannot round the duration up.
		if startedAt.Sub(s.pending) >= s.rule.Duration {
			s.active = true
			s.pending = time.Time{}
			e.event(Activation, s.rule, point, at)
		}
	}
}

func (e *Evaluator) event(kind EventKind, rule Rule, point metrics.MetricPoint, at time.Time) {
	logging.Logger.Info("Alert %s for rule %s", kind, rule.ID)
	e.emit(Event{kind, rule, point, at})
}
