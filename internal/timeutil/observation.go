package timeutil

import "time"

// ObservationBootTime rejects readings whose acquisition spans an unknown gap.
func ObservationBootTime(start time.Duration) time.Duration {
	end := BootTime()
	if start < 0 || end < start || end-start > 10*time.Second {
		return -1
	}
	return end
}
