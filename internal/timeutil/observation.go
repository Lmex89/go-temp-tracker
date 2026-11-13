package timeutil

import "time"

// Sentinel BootAt values. Go has no exceptions for "soft" failures, so a
// negative duration plays the role a Python None-with-reason would.
const (
	ObservationUncertain time.Duration = -1
	ClockUnavailable     time.Duration = -2
)

// Observation records when one reading was acquired, on both the wall clock
// (StartedAt/At) and the suspend-aware boot clock (BootStart/BootAt).
// It is embedded in metrics.MetricPoint and sensor.Reading, so its fields are
// "promoted": p.At works directly, a bit like inheriting attributes in Python.
type Observation struct {
	StartedAt time.Time
	At        time.Time
	// BootAt is zero for legacy readers and negative when the clock failed.
	BootStart time.Duration
	BootAt    time.Duration
}

// StartObservation is called right before a read; pass the wall clock "now".
// Usage (like a manual context manager in Python):
//
//	obs := timeutil.StartObservation(time.Now())
//	raw, err := os.ReadFile(path)
//	obs = obs.End(time.Now())
func StartObservation(now time.Time) Observation {
	return Observation{StartedAt: now, BootStart: BootTime()}
}

// End returns a copy with the acquisition end filled in. The receiver is a
// value (not a pointer), so the caller's original is untouched, like
// dataclasses.replace() in Python.
func (o Observation) End(now time.Time) Observation {
	o.At = now
	o.BootAt = ObservationBootTime(o.BootStart)
	return o
}

// ObservationBootTime rejects readings whose acquisition spans an unknown gap.
func ObservationBootTime(start time.Duration) time.Duration {
	end := BootTime()
	if start < 0 || end < 0 {
		return ClockUnavailable
	}
	if end < start || end-start > 10*time.Second {
		return ObservationUncertain
	}
	return end
}
