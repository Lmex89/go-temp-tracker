package timeutil

import (
	"time"

	"golang.org/x/sys/unix"
)

// BootTime is monotonic and includes time spent suspended on Linux
// (Python: time.clock_gettime_ns(time.CLOCK_BOOTTIME)). It returns -1 on
// failure because Go prefers sentinel values over exceptions here.
func BootTime() time.Duration {
	var ts unix.Timespec
	if err := unix.ClockGettime(unix.CLOCK_BOOTTIME, &ts); err != nil || ts.Nano() <= 0 {
		return -1
	}
	return time.Duration(ts.Nano())
}
