package store

import (
	"database/sql"
	"fmt"
	"time"
)

// Reading is a *struct* -- like a Python dataclass or a NamedTuple.
// Fields with capital letters are "exported" (public) -- like Python's public attributes.
// Lowercase fields would be private (only accessible within this package).
// The backtick tags (`json:"sensor"`) tell the JSON encoder what key names to use.
// In Python you'd use @dataclass or just a dict.
// MetricType and Unit were added for multi-metric support (CPU, memory, disk, etc.).
type Reading struct {
	Sensor     string  `json:"sensor"`
	TempC      float64 `json:"temp_c"`      // Renamed to "value" later? Kept for backward compat.
	MetricType string  `json:"metric_type"` // "temperature", "cpu", "memory", "disk", "load"
	Unit       string  `json:"unit"`        // "C", "%", "bytes", ""
	CreatedAt  string  `json:"created_at"`
}

// AllMetrics is the metricType value meaning "every metric type".
// Query and Prune accept it as a filter that matches everything; QueryLatest
// does NOT (it needs one concrete type). Python analogy: a named constant
// instead of passing a bare "" around.
const AllMetrics = ""

// TimeWindow describes which slice of time a query covers.
// Python analogy: a small dataclass where unset fields keep their zero value.
// If Hours > 0 the window is relative ("last N hours"); otherwise From/To
// (both non-zero) describe an absolute range.
type TimeWindow struct {
	Hours int
	From  time.Time
	To    time.Time
}

// Validate reports whether the window is usable: either Hours > 0, or both
// From and To set with From not after To. Without it a zero window would
// silently query year 1. Python analogy: raising ValueError in __post_init__.
func (w TimeWindow) Validate() error {
	if w.Hours > 0 {
		return nil
	}
	if w.From.IsZero() || w.To.IsZero() {
		return fmt.Errorf("invalid time window: need Hours > 0 or both From and To")
	}
	if w.From.After(w.To) {
		return fmt.Errorf("invalid time window: From is after To")
	}
	return nil
}

// Store is the interface for database operations -- like a Python ABC/Protocol.
// Any type that has all these methods automatically satisfies Store (no "extends" keyword).
// This lets us swap out SQLite for another DB later without changing other code.
type Store interface {
	InitDB() *sql.DB
	Insert(db *sql.DB, sensor string, temp float64, metricType string, unit string)
	// Query returns readings inside a TimeWindow (relative Hours or absolute
	// From/To), optionally filtered by metric type (AllMetrics = every metric).
	// Returns an error for an invalid window (see TimeWindow.Validate).
	Query(db *sql.DB, window TimeWindow, metricType string) ([]Reading, error)
	// QueryLatest: newest reading per sensor for one metric type. metricType
	// is required: AllMetrics (empty) returns an error.
	QueryLatest(db *sql.DB, metricType string) ([]Reading, error)
	// Prune deletes readings older than retainHours; AllMetrics = all types.
	Prune(db *sql.DB, metricType string, retainHours int)
}
