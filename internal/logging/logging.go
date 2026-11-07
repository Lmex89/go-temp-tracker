// Package logging provides the shared leveled logger used by the service
// and the cmd/ tools. In Python terms: a tiny shared module, like
// myproject/logging_utils.py, imported by every script instead of copy-pasted.
package logging

import (
	"fmt"
	"io"
	"log"
	"os"
	"strings"
	"sync"
	"time"
)

// Level is a custom type based on int -- like an IntEnum in Python.
// Go doesn't have classes, so we use typed constants with iota (similar to Enum auto()).
type Level int

// iota starts at 0 and increments for each const in the block.
// So: DEBUG=0, INFO=1, WARN=2, ERROR=3.
// This lets us filter logs by level (log only if level >= minimum).
const (
	DEBUG Level = iota
	INFO
	WARN
	ERROR
)

// levelNames maps Level values to their string names -- like a Python dict.
var levelNames = map[Level]string{
	DEBUG: "DEBUG",
	INFO:  "INFO",
	WARN:  "WARN",
	ERROR: "ERROR",
}

// LeveledLogger provides leveled logging with timestamps -- like Python's logging.Logger.
// It's thread-safe (uses sync.Mutex) so multiple goroutines can log without garbled output.
// In Python, logging.Logger is already thread-safe; here we have to manage it manually.
type LeveledLogger struct {
	mu     sync.Mutex  // Mutex = mutual exclusion lock -- like threading.Lock() in Python
	logger *log.Logger // Go's standard library logger
	level  Level       // Minimum level to actually print
}

// Logger is the package-level global logger (writes to stdout), initialized
// from the LOG_LEVEL env var. Similar to Python's logging.getLogger(__name__).
// Callers use it as logging.Logger.Info(...).
var Logger = New(os.Stdout, os.Getenv("LOG_LEVEL"))

// New creates a logger that writes to w. The level string comes from the
// LOG_LEVEL env var (case-insensitive); unknown or empty means INFO.
// Tools such as spike-report pass os.Stderr so stdout stays clean for reports.
func New(w io.Writer, level string) *LeveledLogger {
	l := &LeveledLogger{logger: log.New(w, "", 0)}
	// A switch statement (like Python's match/case or if/elif) sets the minimum level.
	switch strings.ToUpper(level) {
	case "DEBUG":
		l.level = DEBUG
	case "WARN":
		l.level = WARN
	case "ERROR":
		l.level = ERROR
	default:
		l.level = INFO // Default to INFO if env var is empty or unrecognized
	}
	return l
}

// log is the internal method that all public methods (Debug, Info, etc.) call.
// ...interface{} is like *args in Python -- variadic parameters of any type.
// In Python: def log(self, level, format, *args): ...
func (l *LeveledLogger) log(level Level, format string, args ...interface{}) {
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

// Debug logs at DEBUG level (lowest, most verbose)
func (l *LeveledLogger) Debug(format string, args ...interface{}) { l.log(DEBUG, format, args...) }
func (l *LeveledLogger) Info(format string, args ...interface{})  { l.log(INFO, format, args...) }
func (l *LeveledLogger) Warn(format string, args ...interface{})  { l.log(WARN, format, args...) }
func (l *LeveledLogger) Error(format string, args ...interface{}) { l.log(ERROR, format, args...) }

// Fatal logs an error and then exits the program -- like Python's logging.critical() + sys.exit(1).
func (l *LeveledLogger) Fatal(format string, args ...interface{}) {
	l.log(ERROR, format, args...)
	os.Exit(1)
}
