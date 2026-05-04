package main

import (
	"fmt"
	"strings"
	"time"
)

const (
	dbTimeLayout        = "2006-01-02 15:04:05"
	frontendTimeLayout  = "2006-01-02T15:04"
	frontendTimeLayout2 = "2006-01-02 15:04"
)

// TimeConverter is an interface for converting UTC timestamps to a local timezone.
// This decouples timezone logic from the rest of the code — similar to Python's
// abc or Protocol where you define a method signature.
type TimeConverter interface {
	ToLocal(utcTimestamp string) string
}

// MeridaTimeConverter implements TimeConverter for America/Merida timezone (UTC-6 / CST).
// In Python you'd use pytz or zoneinfo: pytz.timezone("America/Merida").
type MeridaTimeConverter struct {
	location *time.Location // Go's time.Location is like Python's pytz timezone object
}

// NewMeridaTimeConverter loads the America/Merida timezone.
// Falls back to UTC if the timezone isn't found on the system.
func NewMeridaTimeConverter() *MeridaTimeConverter {
	loc, err := time.LoadLocation("America/Merida")
	if err != nil {
		Logger.Error("Failed to load America/Merida timezone: %v", err)
		loc = time.UTC
	}
	return &MeridaTimeConverter{location: loc}
}

func meridaLocation() *time.Location {
	loc, err := time.LoadLocation("America/Merida")
	if err != nil {
		return time.UTC
	}
	return loc
}

// ParseTimestampInput accepts frontend date-time strings or database timestamps
// and normalizes them to UTC for SQLite queries.
func ParseTimestampInput(raw string) (time.Time, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return time.Time{}, fmt.Errorf("empty timestamp")
	}

	Logger.Debug("ParseTimestampInput raw=%q", raw)

	if t, err := time.Parse(time.RFC3339Nano, raw); err == nil {
		Logger.Debug("Parsed as RFC3339Nano -> UTC=%s", t.UTC().Format(dbTimeLayout))
		return t.UTC(), nil
	}
	if t, err := time.Parse(time.RFC3339, raw); err == nil {
		Logger.Debug("Parsed as RFC3339 -> UTC=%s", t.UTC().Format(dbTimeLayout))
		return t.UTC(), nil
	}
	if t, err := time.ParseInLocation(dbTimeLayout, raw, time.UTC); err == nil {
		Logger.Debug("Parsed as dbTimeLayout(UTC) -> UTC=%s", t.UTC().Format(dbTimeLayout))
		return t.UTC(), nil
	}

	loc := meridaLocation()
	if t, err := time.ParseInLocation(frontendTimeLayout, raw, loc); err == nil {
		Logger.Debug("Parsed as frontendTimeLayout(Merida) -> UTC=%s", t.UTC().Format(dbTimeLayout))
		return t.UTC(), nil
	}
	if t, err := time.ParseInLocation(frontendTimeLayout2, raw, loc); err == nil {
		Logger.Debug("Parsed as frontendTimeLayout2(Merida) -> UTC=%s", t.UTC().Format(dbTimeLayout))
		return t.UTC(), nil
	}

	return time.Time{}, fmt.Errorf("unsupported timestamp format: %s", raw)
}

// ToLocal parses a UTC timestamp string and converts it to America/Merida local time.
// In Python: from datetime import datetime; import pytz;
//
//	utc_dt = datetime.strptime(ts, "%Y-%m-%d %H:%M:%S").replace(tzinfo=pytz.UTC)
//	local_dt = utc_dt.astimezone(pytz.timezone("America/Merida"))
//
// Go uses a REFERENCE TIME: "2006-01-02 15:04:05" which is the time of Go's birth.
// This is Go's format string — NOT strftime-style like Python's "%Y-%m-%d %H:%M:%S".
// The reference time is: Mon Jan 2 15:04:05 MST 2006 (01/02 03:04:05PM '06 -0700).
func (c *MeridaTimeConverter) ToLocal(utcTimestamp string) string {
	utcTime, err := time.Parse("2006-01-02 15:04:05", utcTimestamp)
	if err != nil {
		return utcTimestamp
	}
	// .In(c.location) converts to the target timezone — like astimezone() in Python.
	// .Format() converts back to string — like strftime() in Python.
	return utcTime.In(c.location).Format("2006-01-02 15:04:05")
}
