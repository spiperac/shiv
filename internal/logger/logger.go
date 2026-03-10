// Package logger provides a minimal levelled logger for Shiv.
// Output is gated behind a verbose flag set at startup via Init.
// Only fatal startup messages use Always — everything else is verbose.
package logger

import (
	"fmt"
	"io"
	"os"
	"time"
)

var verbose bool

// Init configures the logger. Call once at startup.
func Init(v bool) {
	verbose = v
}

// Always logs a message that is always shown regardless of verbose flag.
// Use sparingly — only for critical startup/shutdown messages.
func Always(format string, args ...any) {
	log(os.Stdout, "INFO", format, args...)
}

// Info logs a message only when verbose mode is enabled.
func Info(format string, args ...any) {
	if verbose {
		log(os.Stdout, "INFO", format, args...)
	}
}

// Error logs an error that is always shown.
func Error(format string, args ...any) {
	log(os.Stderr, "ERR ", format, args...)
}

// Debug logs a message only when verbose mode is enabled.
func Debug(format string, args ...any) {
	if verbose {
		log(os.Stdout, "DBG ", format, args...)
	}
}

func log(w io.Writer, level, format string, args ...any) {
	ts := time.Now().Format("15:04:05.000")
	msg := fmt.Sprintf(format, args...)
	fmt.Fprintf(w, "[shiv] %s %s %s\n", ts, level, msg)
}
