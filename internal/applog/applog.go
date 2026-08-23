// Package applog provides one shared structured logger for the whole
// application. Using log/slog (Go's standard library structured logger)
// keeps every log line machine-parseable (JSON) - useful once this is
// running unattended on a kiosk and something needs investigating after
// the fact, rather than scrolling raw fmt.Println text.
package applog

import (
	"io"
	"log/slog"
	"os"
)

// L is the process-wide logger. Call Init once at startup (main.go) before
// using it; it defaults to a plain JSON-to-stderr logger so packages that
// log at init time (rare, but safe) never hit a nil pointer.
var L = slog.New(slog.NewJSONHandler(os.Stderr, nil))

// Init configures the shared logger to write to stderr and, if logFile is
// non-empty, additionally to that file (created/appended). Call this once
// from main() before starting any other component.
func Init(logFile string, debug bool) error {
	level := slog.LevelInfo
	if debug {
		level = slog.LevelDebug
	}

	writer := io.Writer(os.Stderr)
	if logFile != "" {
		f, err := os.OpenFile(logFile, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
		if err != nil {
			return err
		}
		writer = io.MultiWriter(os.Stderr, f)
	}

	L = slog.New(slog.NewJSONHandler(writer, &slog.HandlerOptions{Level: level}))
	slog.SetDefault(L)
	return nil
}
