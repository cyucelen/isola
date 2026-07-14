package logging

import (
	"fmt"
	"io"
	"os"
	"sync"
)

// Level represents log verbosity.
type Level int

const (
	LevelQuiet   Level = -1
	LevelNormal  Level = 0
	LevelVerbose Level = 1
	LevelDebug   Level = 2
)

// Logger provides leveled logging output.
type Logger struct {
	mu    sync.RWMutex
	level Level
	out   io.Writer
}

var std = &Logger{level: LevelNormal, out: os.Stderr}

// SetLevel sets the global log level.
func SetLevel(l Level) {
	std.mu.Lock()
	std.level = l
	std.mu.Unlock()
}

// GetLevel returns the current log level.
func GetLevel() Level {
	std.mu.RLock()
	defer std.mu.RUnlock()
	return std.level
}

// emit writes prefix+format+args to the log output when the current level is at
// or above threshold. Error passes LevelQuiet (the floor) so it always prints.
func emit(threshold Level, prefix, format string, args ...any) {
	std.mu.Lock()
	defer std.mu.Unlock()
	if std.level >= threshold {
		_, _ = fmt.Fprintf(std.out, prefix+format+"\n", args...)
	}
}

// Info prints a message at normal level.
func Info(format string, args ...any) { emit(LevelNormal, "", format, args...) }

// Verbose prints a message at verbose level.
func Verbose(format string, args ...any) { emit(LevelVerbose, "", format, args...) }

// Debug prints a message at debug level.
func Debug(format string, args ...any) { emit(LevelDebug, "[debug] ", format, args...) }

// Warn always prints unless in quiet mode.
func Warn(format string, args ...any) { emit(LevelNormal, "warning: ", format, args...) }

// Error always prints.
func Error(format string, args ...any) { emit(LevelQuiet, "error: ", format, args...) }
