package logging

import (
	"context"
	"log/slog"
)

// tapHandler wraps another slog.Handler, invoking a Tap callback with
// every record in addition to delegating to the wrapped handler
// unchanged. See Options.Tap's doc comment for why this exists and the
// constraints it places on the callback.
type tapHandler struct {
	next slog.Handler
	tap  func(ctx context.Context, r slog.Record)
}

func (h *tapHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.next.Enabled(ctx, level)
}

func (h *tapHandler) Handle(ctx context.Context, r slog.Record) error {
	// The tap runs before the underlying handler so a consumer sees a
	// record at (approximately) the moment it was logged rather than
	// after however long the file/stdout write takes; deliberately not
	// dependent on that write succeeding.
	h.safeTap(ctx, r)
	return h.next.Handle(ctx, r)
}

// safeTap isolates the rest of the logging pipeline from a Tap that
// panics. Options.Tap's contract says a Tap "must not block or panic",
// but log/slog.Handler.Handle has no return path for "the handler itself
// is misbehaving" other than an error, and a caller who couldn't act on
// that error anyway - better to lose one Tap invocation than crash the
// process from inside a log call, which is disproportionate for a
// diagnostics-observation hook to be capable of.
func (h *tapHandler) safeTap(ctx context.Context, r slog.Record) {
	defer func() { _ = recover() }()
	h.tap(ctx, r)
}

func (h *tapHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &tapHandler{next: h.next.WithAttrs(attrs), tap: h.tap}
}

func (h *tapHandler) WithGroup(name string) slog.Handler {
	return &tapHandler{next: h.next.WithGroup(name), tap: h.tap}
}
