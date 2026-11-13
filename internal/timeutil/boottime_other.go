//go:build !linux

package timeutil

import "time"

// BootTime fails closed where a suspend-aware clock is unavailable.
func BootTime() time.Duration { return -1 }
