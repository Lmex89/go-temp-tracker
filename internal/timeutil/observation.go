package timeutil

import "time"

const (
	ObservationUncertain time.Duration = -1
	ClockUnavailable     time.Duration = -2
)

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
