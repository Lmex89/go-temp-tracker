package alerts

import (
	"math"
	"sync"
	"time"

	"sensors-temp/internal/logging"
	"sensors-temp/internal/metrics"
)

type Event struct {
	Kind  string
	Rule  Rule
	Point metrics.MetricPoint
	At    time.Time
}

type ruleState struct {
	rule    Rule
	pending time.Time
	last    time.Time
	active  bool
	lost    bool
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
			s.pending, s.last = time.Time{}, time.Time{}
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
		if !s.last.IsZero() && (at.Sub(s.last) > 10*time.Second || at.Before(s.last)) {
			s.pending = time.Time{}
			if !s.lost {
				logging.Logger.Warn("Alert observation gap for rule %s", s.rule.ID)
			}
			s.lost = true
		} else {
			s.lost = false
		}
		s.last = at
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
		if at.Sub(s.pending) >= s.rule.Duration {
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
