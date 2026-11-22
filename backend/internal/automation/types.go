// Package automation is Weloury's rule engine (spec section 11): Trigger
// -> Conditions -> Actions, evaluated against events on the bus (which
// covers "system events," "health-check results," and "thresholds" from
// spec section 11's trigger list in one mechanism - a health check
// threshold failure is just another bus event by the time it reaches
// here, see internal/health/events.go) and against scheduler.TypeTaskDue
// (covering "schedules").
//
// Engine deliberately does not implement "an unrestricted
// arbitrary-command automation engine" (spec section 11's explicit
// warning): run_command actions exec argv directly, never through a
// shell, the same discipline internal/health's command check type uses
// - see SECURITY.md's "Automation actions" section. The other structural
// safety property worth knowing up front: no Trigger may match an event
// type this package itself publishes (see Trigger.Validate and
// Engine's subscription filter) - without that, a rule whose action
// creates an event could trigger itself, or another rule, into an
// unbounded cascade. See ARCHITECTURE.md's "Automation and scheduling
// engine" for the full design writeup.
package automation

import "time"

// TriggerType selects what starts a Rule's evaluation.
type TriggerType string

const (
	// TriggerEvent fires whenever a bus event of type EventType is
	// published - covers spec section 11's "system events," "health-check
	// results," and "thresholds" (all three arrive as bus events by the
	// time they reach here).
	TriggerEvent TriggerType = "event"
	// TriggerSchedule fires whenever scheduler.TypeTaskDue is published
	// for the referenced ScheduleID - covers spec section 11's
	// "schedules," delegating all timing concerns to internal/scheduler
	// (see that package's doc comment on the division of
	// responsibility).
	TriggerSchedule TriggerType = "schedule"
)

// Trigger is the "when" half of a Rule.
type Trigger struct {
	Type TriggerType `json:"type"`
	// EventType is required for TriggerEvent - the bus event Type to
	// match, e.g. "health.check_failed" or "process.terminated".
	EventType string `json:"event_type,omitempty"`
	// ScheduleID is required for TriggerSchedule - a scheduler.Task ID.
	ScheduleID string `json:"schedule_id,omitempty"`
}

// Condition narrows a Trigger match down further, evaluated against the
// triggering event's JSON payload (spec section 11: Trigger -> Conditions
// -> Actions) - the same payload map for either Trigger type: an event
// trigger's payload is whatever that bus event carried; a schedule
// trigger's payload is scheduler.TypeTaskDue's {task_id, name}, which
// Conditions can reference just as well (e.g. matching on "name" as an
// extra sanity check beyond the schedule_id match itself). Deliberately
// just one flat equality check per Condition - see conditions.go's doc
// comment for why a richer expression language isn't warranted here.
type Condition struct {
	// Field is a top-level key in the triggering event's JSON payload -
	// no nested dot-paths. E.g. "check_id", "name", "pid".
	Field  string `json:"field"`
	Equals string `json:"equals"`
}

// ActionType selects what a Rule does once triggered and its conditions
// pass - spec section 11's action list.
type ActionType string

const (
	ActionRestartProcess ActionType = "restart_process"
	ActionStopProcess    ActionType = "stop_process"
	ActionStartService   ActionType = "start_service"
	ActionRestartService ActionType = "restart_service"
	ActionRunCommand     ActionType = "run_command"
	ActionCreateEvent    ActionType = "create_event"
	ActionNotify         ActionType = "notify"
)

// Action is one step of a Rule's ordered action list - see actions.go
// for what each ActionType actually does and Engine.executeActions for
// why they run sequentially, not concurrently.
type Action struct {
	Type ActionType `json:"type"`

	// ProcessName selects which running process(es) to act on for
	// ActionRestartProcess/ActionStopProcess - every process whose name
	// case-insensitively matches, the same matching rule
	// internal/health's process_exists checker uses. Required for both.
	ProcessName string `json:"process_name,omitempty"`
	// LaunchCommand is argv (never a shell string - see the package doc
	// comment) used to start a replacement process after termination.
	// Required for ActionRestartProcess only.
	LaunchCommand []string `json:"launch_command,omitempty"`

	// ServiceName selects the target for ActionStartService/
	// ActionRestartService. Required for both.
	ServiceName string `json:"service_name,omitempty"`

	// CommandArgs is argv (never a shell string) for ActionRunCommand.
	// Required.
	CommandArgs []string `json:"command_args,omitempty"`

	// Message is the text carried by ActionCreateEvent/ActionNotify.
	// Required for both.
	Message string `json:"message,omitempty"`
}

// RunStatus is the outcome of one Rule execution attempt.
type RunStatus string

const (
	RunSucceeded         RunStatus = "succeeded"
	RunFailed            RunStatus = "failed"
	RunSkippedCooldown   RunStatus = "skipped_cooldown"
	RunSkippedConditions RunStatus = "skipped_conditions"
)

// Rule is a complete Trigger -> Conditions -> Actions definition (spec
// section 11).
type Rule struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Enabled bool   `json:"enabled"`

	Trigger    Trigger     `json:"trigger"`
	Conditions []Condition `json:"conditions,omitempty"`
	Actions    []Action    `json:"actions"`

	// CooldownSeconds, if > 0, is the minimum time between the end of
	// one execution and the start of the next, even if the trigger
	// fires again sooner - see Engine.shouldRun's doc comment. Defends
	// against a rapidly repeating trigger (e.g. a flapping health check)
	// turning a single misconfigured rule into an action storm.
	CooldownSeconds int `json:"cooldown_seconds,omitempty"`

	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// Status is Rule plus its live-tracked last execution outcome - the
// automation equivalent of health.Status.
type Status struct {
	Rule
	LastRunAt      *time.Time `json:"last_run_at,omitempty"`
	LastRunStatus  RunStatus  `json:"last_run_status,omitempty"`
	LastRunMessage string     `json:"last_run_message,omitempty"`
	RunCount       int        `json:"run_count"`

	// ConsecutiveFailures counts uninterrupted RunFailed executions
	// since the last RunSucceeded (or since creation) - reset to 0 by
	// any success, incremented by any failure, untouched by a skip
	// (RunSkippedCooldown/RunSkippedConditions). Once it reaches
	// MaxConsecutiveFailures, Engine auto-disables the rule (see
	// trackedRule.recordOutcome) - the circuit breaker named as a
	// follow-up in minRuleCooldownSeconds's doc comment (validate.go).
	// Exposed here, not just used internally, so the frontend can show
	// a rule approaching the breaker, not just one that already
	// tripped it.
	ConsecutiveFailures int `json:"consecutive_failures"`
}

// RunResult is the outcome of one Rule execution attempt, passed to
// Repository.UpdateRunResult in a single call so the run outcome and
// any resulting circuit-breaker state change (Enabled flipping to
// false, see MaxConsecutiveFailures) persist atomically - never as two
// separate writes that could leave a crash-window inconsistency between
// "recorded as failed" and the auto-disable it triggered.
type RunResult struct {
	Status  RunStatus
	Message string
	RanAt   time.Time
	// Enabled and ConsecutiveFailures are Engine's authoritative
	// post-outcome values (trackedRule.recordOutcome already applied
	// this run's effect on both before RunResult is built) - the
	// repository persists them verbatim, it does not derive them.
	Enabled             bool
	ConsecutiveFailures int
}
