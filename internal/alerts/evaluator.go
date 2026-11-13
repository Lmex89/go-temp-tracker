package alerts

import (
	"math"
	"sync"
	"time"

	"sensors-temp/internal/logging"
	"sensors-temp/internal/metrics"
	"sensors-temp/internal/timeutil"
)

type Event struct {
	Kind  string
	Rule  Rule
	Point metrics.MetricPoint
	At    time.Time
}

type ruleState struct {
	rule     Rule
	pending  time.Time
	last     time.Time
	lastBoot time.Duration
	active   bool
	lost     bool
}

// Evaluator protects independent rule states shared by the polling goroutines.
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

// ObserveAt allows callers with a controlled suspend-aware clock to evaluate readings.
func (e *Evaluator) ObserveAt(metricType string, now time.Time, bootNow time.Duration, points []metrics.MetricPoint) {
	e.observe(metricType, now, points, func() time.Duration { return bootNow })
}

func (e *Evaluator) observe(metricType string, now time.Time, points []metrics.MetricPoint, bootClock func() time.Duration) {
	e.mu.Lock()
	defer e.mu.Unlock()
	for i := range e.states {
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
			s.pending, s.last, s.lastBoot = time.Time{}, time.Time{}, 0
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
			s.pending, s.last, s.lastBoot = time.Time{}, time.Time{}, 0
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
			s.pending, s.last, s.lastBoot = time.Time{}, time.Time{}, 0
			if !s.lost {
				logging.Logger.Warn("Alert observation stale for rule %s", s.rule.ID)
				s.lost = true
			}
			continue
		}
		bootGap := (point.BootAt == 0) != (s.lastBoot == 0) ||
			(point.BootAt > 0 && s.lastBoot > 0 && (point.BootAt-s.lastBoot > 10*time.Second || point.BootAt < s.lastBoot))
		if !s.last.IsZero() && (at.Sub(s.last) > 10*time.Second || startedAt.Before(s.last) || bootGap) {
			s.pending = time.Time{}
			if !s.lost {
				logging.Logger.Warn("Alert observation gap for rule %s", s.rule.ID)
			}
			s.lost = true
		} else {
			s.lost = false
		}
		s.last = startedAt
		s.lastBoot = point.BootAt
		if point.BootStart > 0 {
			s.lastBoot = point.BootStart
		}
		if point.Value < s.rule.Threshold {
			s.pending = time.Time{}
			if s.active {
				s.active = false
				e.event("recovery", s.rule, point, at)
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
		if startedAt.Sub(s.pending) >= s.rule.Duration {
			s.active = true
			s.pending = time.Time{}
			e.event("activation", s.rule, point, at)
		}
	}
}

func (e *Evaluator) event(kind string, rule Rule, point metrics.MetricPoint, at time.Time) {
	logging.Logger.Info("Alert %s for rule %s", kind, rule.ID)
	e.emit(Event{kind, rule, point, at})
}
