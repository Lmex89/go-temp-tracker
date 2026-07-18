package main

import (
	"fmt"
	"log"
	"os"
	"strings"
	"sync"
	"time"
)

// logLevel is a custom type based on int — like an IntEnum in Python.
// Go doesn't have classes, so we use typed constants with iota (similar to Enum auto()).
type logLevel int

// iota starts at 0 and increments for each const in the block.
// So: logDEBUG=0, logINFO=1, logWARN=2, logERROR=3.
// This lets us filter logs by level (only log if level >= minimum).
const (
	logDEBUG logLevel = iota
	logINFO
	logWARN
	logERROR
)

// levelNames maps logLevel values to their string names — like a Python dict.
var levelNames = map[logLevel]string{
	logDEBUG: "DEBUG",
	logINFO:  "INFO",
	logWARN:  "WARN",
	logERROR: "ERROR",
}

// spikeLogger provides leveled logging with timestamps — like Python's logging.Logger.
// It's thread-safe (uses sync.Mutex) so multiple goroutines can log without garbled output.
// In Python, logging.Logger is already thread-safe; here we have to manage it manually.
type spikeLogger struct {
	mu     sync.Mutex  // Mutex = mutual exclusion lock — like threading.Lock() in Python
	logger *log.Logger // Go's standard library logger
	level  logLevel    // Minimum level to actually print
}

// newSpikeLogger creates a new logger. The level parameter comes from LOG_LEVEL env var.
// A switch statement (like Python's match/case or if/elif) sets the minimum log level.
func newSpikeLogger(level string) *spikeLogger {
	l := &spikeLogger{logger: log.New(os.Stderr, "", 0)}
	switch strings.ToUpper(level) {
	case "DEBUG":
		l.level = logDEBUG
	case "WARN":
		l.level = logWARN
	case "ERROR":
		l.level = logERROR
	default:
		l.level = logINFO // Default to INFO if env var is empty or unrecognized
	}
	return l
}

// logf is the internal method that all public methods (debug, info, etc.) call.
// ...interface{} is like *args in Python — variadic parameters of any type.
// In Python: def log(self, level, format, *args): ...
func (l *spikeLogger) logf(level logLevel, format string, args ...interface{}) {
	// Skip if this log level is below the configured minimum.
	if level < l.level {
		return
	}
	// Lock the mutex to ensure only one goroutine writes at a time.
	// In Python: with self.lock: ...
	l.mu.Lock()
	defer l.mu.Unlock()
	ts := time.Now().Format("2006-01-02 15:04:05.000")
	msg := fmt.Sprintf(format, args...) // Like Python's format % args or f-string
	l.logger.Printf("[%s] [%s] %s", ts, levelNames[level], msg)
}

// debug logs at DEBUG level (lowest, most verbose)
func (l *spikeLogger) debug(format string, args ...interface{}) { l.logf(logDEBUG, format, args...) }
func (l *spikeLogger) info(format string, args ...interface{})  { l.logf(logINFO, format, args...) }
func (l *spikeLogger) warn(format string, args ...interface{})  { l.logf(logWARN, format, args...) }
func (l *spikeLogger) error(format string, args ...interface{}) { l.logf(logERROR, format, args...) }
