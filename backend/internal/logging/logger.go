// Package logging builds Weloury core's application-wide structured logger.
package logging

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
)

// Options configures New.
type Options struct {
	// Level is one of "debug", "info", "warn", "error".
	Level string
	// Format is "text" or "json".
	Format string
	// FilePath, if non-empty, additionally writes logs to a size-rotated
	// file at this path (see NewRotatingWriter).
	FilePath string

	// Tap, if non-nil, is called with every log record this logger
	// emits, in addition to (never instead of) writing it through the
	// normal handler chain above. It exists so a consumer - in
	// practice, internal/logs's in-memory ring buffer/event bridge for
	// the Logs screen (spec section 8) - can observe every record
	// without this package needing to know that consumer exists. Tap
	// must not block or panic: it runs synchronously on the same
	// goroutine as the log call, inline with slog's own handler
	// invocation, so a slow or panicking Tap would corrupt or stall
	// every log call in the process. tapHandler recovers from a Tap
	// panic defensively (see below) but still expects callers to keep
	// it fast and non-blocking by construction.
	Tap func(ctx context.Context, r slog.Record)
}

// New builds the application-wide structured logger. It always returns a
// usable logger: an unrecognized Level or Format falls back to a sane
// default (info/text) rather than failing startup over a log setting. The
// returned close function flushes and closes any open log file and should
// be deferred by the caller.
func New(opts Options) (*slog.Logger, func() error, error) {
	level := parseLevel(opts.Level)

	var closers []io.Closer
	writers := []io.Writer{os.Stdout}

	if opts.FilePath != "" {
		rw, err := NewRotatingWriter(opts.FilePath, DefaultMaxSizeBytes, DefaultMaxBackups)
		if err != nil {
			return nil, func() error { return nil }, fmt.Errorf("open log file: %w", err)
		}
		writers = append(writers, rw)
		closers = append(closers, rw)
	}

	out := io.MultiWriter(writers...)

	handlerOpts := &slog.HandlerOptions{Level: level}
	var handler slog.Handler
	if strings.EqualFold(opts.Format, "json") {
		handler = slog.NewJSONHandler(out, handlerOpts)
	} else {
		handler = slog.NewTextHandler(out, handlerOpts)
	}
	if opts.Tap != nil {
		handler = &tapHandler{next: handler, tap: opts.Tap}
	}

	logger := slog.New(handler)

	closeFn := func() error {
		var firstErr error
		for _, c := range closers {
			if err := c.Close(); err != nil && firstErr == nil {
				firstErr = err
			}
		}
		return firstErr
	}

	return logger, closeFn, nil
}

func parseLevel(s string) slog.Level {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "debug":
		return slog.LevelDebug
	case "warn", "warning":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}
