package timeutil

import (
	"time"

	"golang.org/x/sys/unix"
)

// BootTime is monotonic and includes time spent suspended on Linux.
func BootTime() time.Duration {
	var ts unix.Timespec
	if err := unix.ClockGettime(unix.CLOCK_BOOTTIME, &ts); err != nil || ts.Nano() <= 0 {
		return -1
	}
	return time.Duration(ts.Nano())
}
