// Package scheduler is Weloury's task scheduler (spec section 12):
// one-time and recurring-interval tasks, persisted across restarts, with
// a documented, deterministic policy for missed executions (see
// Engine's doc comment) instead of leaving that to chance.
//
// Scope, deliberately: this package knows nothing about automation
// rules, health checks, or any other Weloury subsystem. It does exactly
// one thing - track Tasks and publish TypeTaskDue on the bus when one
// becomes due - and nothing consumes that event except
// internal/automation's ScheduleTrigger (see that package's doc
// comment). This is not a general-purpose cron replacement wired into
// every subsystem: internal/health and internal/metrics each already
// have their own purpose-built interval scheduling (per-check tickers,
// per-metric collection loops) that a shared generic scheduler would
// only complicate, not simplify - see ARCHITECTURE.md's "Automation and
// scheduling engine" for the full reasoning. The one real consumer,
// internal/automation, only needs "tell me when a task is due," which
// is exactly the shape of Task/TypeTaskDue below.
package scheduler

import "time"

// Type distinguishes a one-shot Task from a recurring one.
type Type string

const (
	// Once fires exactly once, at RunAt, then disables itself.
	Once Type = "once"
	// Interval fires every IntervalSeconds, indefinitely, until disabled
	// or deleted.
	Interval Type = "interval"
)

// Bounds enforced by Task.applyDefaultsAndValidate (validate.go) - the
// scheduler's equivalent of internal/health's MinIntervalSeconds: an
// interval task with too short a period could otherwise let a single
// misconfigured task fire in an unbounded loop.
const (
	MinIntervalSeconds = 10
	MaxIntervalSeconds = 30 * 24 * 60 * 60 // 30 days
)

// Task is a scheduled unit of "tell someone it's time" - spec section
// 12's "one-time tasks, recurring tasks, interval tasks."
//
// Proper timezone handling (spec section 12) means, concretely: RunAt is
// always an absolute instant. A caller supplies it as RFC3339 with
// whatever UTC offset is meaningful to them (e.g.
// "2026-08-25T17:00:00-07:00" for 5pm Pacific) and Go's time.Parse
// resolves that unambiguously - there is no wall-clock-only field
// anywhere in this type that could be silently misinterpreted in the
// wrong zone. What this package deliberately does *not* implement is
// wall-clock-recurring scheduling ("every day at 9am local time,
// correctly through DST transitions") - that needs real calendar-aware
// recurrence rules (RRULE-style), which is meaningfully more machinery
// than spec section 12 asks for ("one-time tasks, recurring tasks,
// interval tasks" - not cron expressions). Interval recurrence here is
// a fixed number of seconds from the last run, full stop; a task that
// truly needs "same local time every day" should be recreated as a
// series of Once tasks, or wait for a future cron-style addition.
type Task struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Type Type   `json:"type"`

	// RunAt is used for Type == Once; the absolute instant to fire at.
	RunAt time.Time `json:"run_at,omitempty"`
	// IntervalSeconds is used for Type == Interval.
	IntervalSeconds int `json:"interval_seconds,omitempty"`

	Enabled bool `json:"enabled"`

	// NextRunAt is maintained by Engine, not the caller - see
	// Engine.applyResult. Exposed (not unexported) because List/Get
	// callers (the frontend) need "when does this fire next" and
	// recomputing it externally would just be this same logic
	// duplicated badly.
	NextRunAt time.Time  `json:"next_run_at"`
	LastRunAt *time.Time `json:"last_run_at,omitempty"`

	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}
