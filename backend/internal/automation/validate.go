package automation

import (
	"fmt"
	"strings"
)

// MaxCooldownSeconds bounds Rule.CooldownSeconds - a sanity ceiling (one
// week), not a meaningful operational limit; it exists only to catch an
// obviously-wrong value (e.g. a unit mix-up) rather than to constrain a
// legitimate use case.
const MaxCooldownSeconds = 7 * 24 * 60 * 60

// minRuleCooldownSeconds is a hard floor applied to every rule's
// effective cooldown regardless of its configured CooldownSeconds,
// including 0 - which CooldownSeconds's own doc comment defines as "no
// cooldown at all," a legitimate choice for a rule with no side effect
// worth rate-limiting (e.g. one that only logs an event on every
// occurrence). Added in Phase 8's security review after tracing spec
// section 11's own flagship automation example - "WHEN process X
// crashes THEN restart process X" - against the default configuration
// both this package and the frontend form ship: CooldownSeconds
// defaults to 0 in both places, so a rule built exactly as the spec
// suggests has, by default, nothing bounding how fast a restart loop
// could spin if the restarted process crashes again immediately. This
// floor by itself doesn't fully fix that scenario - it bounds the
// *rate* of a crash loop, not *whether* it runs forever - see
// MaxConsecutiveFailures below for the circuit breaker that closes the
// rest of it. Without this floor, the only limit on how fast the loop
// spins is process-spawn overhead; with it, the loop is rate-limited to
// once per this many seconds regardless of configuration. One second is
// negligible for any rule that legitimately wants near-immediate
// re-firing and is the difference between "bounded" and "limited only
// by how fast the OS can fork."
const minRuleCooldownSeconds = 1

// MaxConsecutiveFailures is the circuit breaker minRuleCooldownSeconds's
// doc comment names as the rest of the fix: a rule whose action keeps
// genuinely failing (RunFailed - not a skip, see RunResult's doc
// comment) this many times in a row, with no RunSucceeded in between,
// is auto-disabled by Engine (see trackedRule.recordOutcome) rather
// than left to retry forever at whatever rate the cooldown floor still
// permits. A skip (RunSkippedCooldown/RunSkippedConditions) neither
// extends nor resets this streak - it isn't evidence the action itself
// is failing, just that this particular attempt didn't run it.
//
// Five is deliberately generous rather than tight: this exists to stop
// an indefinite loop, not to second-guess a rule after one or two bad
// runs that might be transient (a service still starting up, a
// momentary network blip). A rule that's tripped the breaker is
// unambiguously not transient.
//
// Auto-disabling (not deleting, not un-scheduling in some
// harder-to-notice way) is the deliberate choice here: the rule and its
// full failure context stay visible in List/Get exactly where an
// operator would already look, Enabled: false is the same signal a
// manual SetEnabled(false) produces, and re-enabling it (via Update,
// which also resets ConsecutiveFailures to 0 - see engine.go's Update)
// is how it starts trying again, once whatever was wrong is fixed.
const MaxConsecutiveFailures = 5

// automationEventPrefix is the namespace every event this package
// publishes uses (see events.go) and the one no Trigger may target - see
// this function's check below and the package doc comment's "no Trigger
// may match an event type this package itself publishes."
const automationEventPrefix = "automation."

// applyDefaultsAndValidate validates r in place. Called by
// Engine.Create/Update before anything is persisted or scheduled.
func (r *Rule) applyDefaultsAndValidate() error {
	r.Name = strings.TrimSpace(r.Name)
	if r.Name == "" {
		return fmt.Errorf("%w: name is required", ErrInvalid)
	}

	if err := r.Trigger.validate(); err != nil {
		return err
	}

	if len(r.Actions) == 0 {
		return fmt.Errorf("%w: at least one action is required", ErrInvalid)
	}
	for i, a := range r.Actions {
		if err := a.validate(); err != nil {
			return fmt.Errorf("action %d: %w", i, err)
		}
	}

	if r.CooldownSeconds < 0 || r.CooldownSeconds > MaxCooldownSeconds {
		return fmt.Errorf("%w: cooldown_seconds must be between 0 and %d, got %d", ErrInvalid, MaxCooldownSeconds, r.CooldownSeconds)
	}

	return nil
}

func (t *Trigger) validate() error {
	switch t.Type {
	case TriggerEvent:
		t.EventType = strings.TrimSpace(t.EventType)
		if t.EventType == "" {
			return fmt.Errorf("%w: event_type is required for an event trigger", ErrInvalid)
		}
		// The core loop-prevention rule - see the package doc comment.
		// Checked here (validation, at Create/Update time) *and*
		// defensively again at dispatch time in Engine.handleEvent, so
		// a rule already persisted before this check existed (there
		// isn't one, but future-proofing costs nothing here) can't
		// reintroduce the hazard.
		if strings.HasPrefix(t.EventType, automationEventPrefix) {
			return fmt.Errorf("%w: event_type %q would let this rule trigger on automation's own output - not allowed, see SECURITY.md", ErrInvalid, t.EventType)
		}
	case TriggerSchedule:
		t.ScheduleID = strings.TrimSpace(t.ScheduleID)
		if t.ScheduleID == "" {
			return fmt.Errorf("%w: schedule_id is required for a schedule trigger", ErrInvalid)
		}
	default:
		return fmt.Errorf("%w: unrecognized trigger type %q", ErrInvalid, t.Type)
	}
	return nil
}

func (a *Action) validate() error {
	switch a.Type {
	case ActionRestartProcess:
		if strings.TrimSpace(a.ProcessName) == "" {
			return fmt.Errorf("%w: process_name is required for restart_process", ErrInvalid)
		}
		if len(a.LaunchCommand) == 0 {
			return fmt.Errorf("%w: launch_command is required for restart_process", ErrInvalid)
		}
	case ActionStopProcess:
		if strings.TrimSpace(a.ProcessName) == "" {
			return fmt.Errorf("%w: process_name is required for stop_process", ErrInvalid)
		}
	case ActionStartService, ActionRestartService:
		if strings.TrimSpace(a.ServiceName) == "" {
			return fmt.Errorf("%w: service_name is required for %s", ErrInvalid, a.Type)
		}
	case ActionRunCommand:
		if len(a.CommandArgs) == 0 {
			return fmt.Errorf("%w: command_args is required for run_command", ErrInvalid)
		}
	case ActionCreateEvent, ActionNotify:
		if strings.TrimSpace(a.Message) == "" {
			return fmt.Errorf("%w: message is required for %s", ErrInvalid, a.Type)
		}
	default:
		return fmt.Errorf("%w: unrecognized action type %q", ErrInvalid, a.Type)
	}
	return nil
}
