// Package logs is Weloury's log engine (spec section 8): a bounded,
// in-memory, queryable, streamable record of Weloury core's own
// structured log output.
//
// Scope, deliberately: Phase 4 covers Weloury's own application log -
// exactly what internal/logging already writes via log/slog - not the
// OS's system log, arbitrary services' logs, or arbitrary processes'
// stdout/stderr. Those ("system logs where accessible", "service logs
// where accessible", "process logs where available" - spec section 8
// lists all three as "where accessible/available", not unconditionally)
// each need a genuinely different, platform-specific reader (journald on
// Linux, the Unified Logging System on macOS, the Event Log on Windows,
// a per-process pipe capture for arbitrary processes) and are left for a
// later pass, the same incremental-scope decision already made for
// container discovery (Phase 6) and remote agents (future). What's here
// is real and complete for what it claims: Weloury's own logging, which is
// also the most operationally relevant log source for a tool whose job
// is partly to explain its own behavior.
//
// Engine attaches to internal/logging via Options.Tap (see logging's doc
// comment) rather than parsing the rotated log file back out: a Tap
// callback gets the original structured slog.Record directly, which is
// strictly more information (real attrs, not text formatted and
// reparsed) for no extra cost.
package logs

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"weloury/internal/eventbus"
)

// TypeEntry is published on the event bus for every captured log entry,
// giving the frontend live streaming (spec section 8, "live streaming")
// via the same NDJSON /events mechanism every other live feature uses -
// no bespoke transport needed. Defined here rather than in internal/app,
// following the convention eventbus.Type's doc comment establishes for
// domain packages that own an event bus dependency directly (see also
// internal/health/events.go).
const TypeEntry eventbus.Type = "log.entry"

// DefaultCapacity bounds how many recent entries Engine keeps in memory.
// This is the concrete answer to spec section 8's "do not load millions
// of log entries into the frontend": the backend itself never holds more
// than this many, so there is nothing unbounded to load in the first
// place. At a few hundred bytes per entry, even the max here is a low
// single-digit number of megabytes.
const DefaultCapacity = 5000

// Entry is one captured log record, structured for both querying and
// wire transport (json tags match what the frontend's TypeScript mirror
// expects - see frontend/src/types/observability.ts).
type Entry struct {
	// Seq is a monotonically increasing, engine-lifetime-scoped sequence
	// number - the cursor Query's Before parameter paginates against.
	// Unlike a timestamp, it has no resolution/tie-breaking ambiguity
	// under bursty logging.
	Seq       uint64            `json:"seq"`
	Timestamp time.Time         `json:"timestamp"`
	Level     string            `json:"level"`
	Message   string            `json:"message"`
	Attrs     map[string]string `json:"attrs,omitempty"`
}

// Engine is a bounded ring buffer of Entry plus a live event bridge. The
// zero value is not usable; construct with New.
type Engine struct {
	bus *eventbus.Bus

	mu       sync.RWMutex
	entries  []Entry // ring buffer, oldest first; capped at capacity
	capacity int
	nextSeq  uint64
}

// New creates an Engine that publishes captured entries on bus. capacity
// <= 0 uses DefaultCapacity.
func New(bus *eventbus.Bus, capacity int) *Engine {
	if capacity <= 0 {
		capacity = DefaultCapacity
	}
	return &Engine{bus: bus, capacity: capacity, entries: make([]Entry, 0, capacity)}
}

// Capture converts an slog.Record into an Entry, stores it, and
// publishes it on the bus. Its signature matches
// logging.Options.Tap exactly so it can be passed as that field
// directly - see internal/app's composition root.
//
// Capture must be fast and must never block: it runs synchronously on
// every log call in the process (see tapHandler's doc comment in
// internal/logging). It takes only a short mutex around an append to a
// pre-sized slice, which is the same order of cost slog's own handlers
// already pay.
func (e *Engine) Capture(_ context.Context, r slog.Record) {
	attrs := make(map[string]string)
	r.Attrs(func(a slog.Attr) bool {
		attrs[a.Key] = a.Value.String()
		return true
	})
	if len(attrs) == 0 {
		attrs = nil
	}

	e.mu.Lock()
	e.nextSeq++
	entry := Entry{
		Seq:       e.nextSeq,
		Timestamp: r.Time,
		Level:     r.Level.String(),
		Message:   r.Message,
		Attrs:     attrs,
	}
	if len(e.entries) >= e.capacity {
		// Drop the oldest entry to make room - a ring buffer via slice
		// shift rather than a circular index, which keeps Query's
		// implementation (below) a plain linear scan over a
		// contiguous, oldest-first slice instead of needing to reason
		// about a wrap point.
		copy(e.entries, e.entries[1:])
		e.entries[len(e.entries)-1] = entry
	} else {
		e.entries = append(e.entries, entry)
	}
	e.mu.Unlock()

	if e.bus != nil {
		payload := marshalEntry(entry)
		e.bus.Publish(eventbus.Event{
			Type:     TypeEntry,
			Source:   "logs.engine",
			Severity: severityFor(entry.Level),
			Payload:  payload,
		})
	}
}

func severityFor(level string) eventbus.Severity {
	switch level {
	case "ERROR":
		return eventbus.SeverityCritical
	case "WARN":
		return eventbus.SeverityWarning
	default:
		return eventbus.SeverityInfo
	}
}

// Query is Engine's read/search surface (spec section 8: "search",
// "filtering", "severity levels", plus pagination so a large buffer is
// never returned in one response).
type Query struct {
	// Level, if non-empty, matches entries at or above this severity
	// ("WARN" also returns "ERROR" entries) using slog's own level
	// ordering. Empty matches every level.
	Level string
	// Search, if non-empty, is matched case-insensitively as a substring
	// of the entry's message. Empty matches every message.
	Search string
	// Before, if non-zero, returns only entries with Seq < Before -
	// pagination cursor for "load older entries".
	Before uint64
	// Limit bounds the number of entries returned, newest first. <= 0
	// defaults to 200; always clamped to at most DefaultCapacity
	// regardless of what's requested, so a misbehaving/malicious caller
	// can't force an unbounded response out of a bounded buffer.
	Limit int
}

const defaultQueryLimit = 200

// Query returns matching entries, newest first.
func (e *Engine) Query(q Query) []Entry {
	limit := q.Limit
	if limit <= 0 {
		limit = defaultQueryLimit
	}
	if limit > DefaultCapacity {
		limit = DefaultCapacity
	}

	minLevel, hasLevelFilter := parseLevel(q.Level)
	search := normalizeSearch(q.Search)

	e.mu.RLock()
	defer e.mu.RUnlock()

	out := make([]Entry, 0, limit)
	for i := len(e.entries) - 1; i >= 0; i-- {
		entry := e.entries[i]
		if q.Before != 0 && entry.Seq >= q.Before {
			continue
		}
		if hasLevelFilter {
			lvl, ok := parseLevel(entry.Level)
			if !ok || lvl < minLevel {
				continue
			}
		}
		if search != "" && !containsFold(entry.Message, search) {
			continue
		}
		out = append(out, entry)
		if len(out) >= limit {
			break
		}
	}
	return out
}
