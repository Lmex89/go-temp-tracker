package timeutil

import (
	"math"
	"testing"
	"time"
)

func TestObservationBootTimeDistinguishesClockFailureAndUncertainAcquisition(t *testing.T) {
	if got := ObservationBootTime(-1); got != ClockUnavailable {
		t.Fatalf("unavailable clock = %v", got)
	}
	if got := ObservationBootTime(time.Duration(math.MaxInt64)); got != ObservationUncertain {
		t.Fatalf("uncertain acquisition = %v", got)
	}
}
