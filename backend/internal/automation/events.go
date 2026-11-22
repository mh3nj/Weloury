package automation

import "weloury/internal/eventbus"

// Event types this package publishes. All share the "automation." prefix
// - this is not incidental naming, it is the mechanism the package doc
// comment and validate.go's Trigger.validate reference: no Rule may
// target an event type under this prefix as its trigger, which is what
// makes it structurally impossible for a rule's own output (a
// TypeRuleExecuted, or a TypeCustomEvent/TypeNotification from another
// rule's create_event/notify action) to trigger any rule, including
// itself, directly or through a chain.
const (
	// TypeRuleExecuted fires after every execution attempt - unlike
	// internal/health's transition-only events, every attempt is
	// published here, not just changes, because each individual
	// execution (what ran, what happened) is itself the information a
	// user wants from an audit trail of automation - there is no
	// steady-state "still executing successfully" to deduplicate away
	// the way there is with a health check's repeated confirmations of
	// the same state.
	TypeRuleExecuted eventbus.Type = "automation.rule_executed"

	// TypeCustomEvent is published by an ActionCreateEvent action.
	// Always this fixed type, never a caller-chosen one: letting a rule
	// fabricate an event under another subsystem's namespace (e.g.
	// impersonating "health.check_failed") would be confusing at best
	// and could feed misleading data to other rules or the Events
	// screen at worst.
	TypeCustomEvent eventbus.Type = "automation.custom_event"

	// TypeNotification is published by an ActionNotify action. Kept
	// distinct from TypeCustomEvent (rather than reusing it) so a future
	// frontend can treat the two differently - e.g. a notify surfaced as
	// a transient toast versus a create_event entry that's just part of
	// the event log. As of Phase 5, both render identically on the
	// Events screen; see CHANGELOG.md's "Known limitations" for why
	// there's no OS-level desktop notification yet.
	TypeNotification eventbus.Type = "automation.notification"

	// TypeRuleAutoDisabled fires exactly once per trip of the circuit
	// breaker (see MaxConsecutiveFailures) - the moment
	// trackedRule.recordOutcome flips a rule's Enabled from true to
	// false on its own, distinct from TypeRuleExecuted (which already
	// fired for the same failed run, same as every other run) because
	// "this rule just stopped running itself" is a materially different
	// fact from "this rule's action failed once more," worth its own
	// Warning-severity entry an operator scanning the Events screen
	// shouldn't have to infer from a run count.
	TypeRuleAutoDisabled eventbus.Type = "automation.rule_auto_disabled"
)

// ruleExecutedPayload is TypeRuleExecuted's JSON payload.
type ruleExecutedPayload struct {
	RuleID  string    `json:"rule_id"`
	Name    string    `json:"name"`
	Status  RunStatus `json:"status"`
	Message string    `json:"message"`
}

// customEventPayload is TypeCustomEvent's and TypeNotification's shared
// JSON payload shape.
type customEventPayload struct {
	RuleID  string `json:"rule_id"`
	Name    string `json:"name"`
	Message string `json:"message"`
}

// ruleAutoDisabledPayload is TypeRuleAutoDisabled's JSON payload.
type ruleAutoDisabledPayload struct {
	RuleID              string `json:"rule_id"`
	Name                string `json:"name"`
	ConsecutiveFailures int    `json:"consecutive_failures"`
}
