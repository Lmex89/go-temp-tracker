package main

import (
	"fmt"
	"log"
	"os"
	"strings"
	"sync"
	"time"
)

type LogLevel int

const (
	DEBUG LogLevel = iota
	INFO
	WARN
	ERROR
)

var levelNames = map[LogLevel]string{
	DEBUG: "DEBUG",
	INFO:  "INFO",
	WARN:  "WARN",
	ERROR: "ERROR",
}

type LeveledLogger struct {
	mu     sync.Mutex
	logger *log.Logger
	level  LogLevel
}

var Logger = NewLeveledLogger(strings.ToUpper(os.Getenv("LOG_LEVEL")))

func NewLeveledLogger(level string) *LeveledLogger {
	l := &LeveledLogger{logger: log.New(os.Stdout, "", 0)}
	switch level {
	case "DEBUG":
		l.level = DEBUG
	case "WARN":
		l.level = WARN
	case "ERROR":
		l.level = ERROR
	default:
		l.level = INFO
	}
	return l
}

func (l *LeveledLogger) log(level LogLevel, format string, args ...interface{}) {
	if level < l.level {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	ts := time.Now().Format("2006-01-02 15:04:05.000")
	msg := fmt.Sprintf(format, args...)
	l.logger.Printf("[%s] [%s] %s", ts, levelNames[level], msg)
}

func (l *LeveledLogger) Debug(format string, args ...interface{}) { l.log(DEBUG, format, args...) }
func (l *LeveledLogger) Info(format string, args ...interface{})  { l.log(INFO, format, args...) }
func (l *LeveledLogger) Warn(format string, args ...interface{})  { l.log(WARN, format, args...) }
func (l *LeveledLogger) Error(format string, args ...interface{}) { l.log(ERROR, format, args...) }
