//go:build !linux

package timeutil

import "time"

// BootTime fails closed where a suspend-aware clock is unavailable.
// The "//go:build !linux" line above picks this file on other systems, like
// an "if sys.platform != 'linux':" branch decided at compile time.
func BootTime() time.Duration { return -1 }
