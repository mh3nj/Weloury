package automation

import "fmt"

// evaluateConditions checks every condition against payload (the
// triggering event's JSON payload, already unmarshaled into a flat map by
// the caller - see Engine.handleEvent), AND-ing them together: all must
// pass for a Rule to proceed. An empty conditions list always passes
// (vacuous truth) - a Trigger with no Conditions fires on every matching
// event, which is the expected default.
//
// Deliberately just a flat "does this top-level field equal this string"
// check, not a richer expression language (no nesting, no operators
// besides equality, no boolean combinators beyond implicit AND): spec
// section 11 doesn't ask for one, a Condition exists to narrow an
// already-specific Trigger (e.g. "which health check" out of "any health
// check failed"), and every event payload this codebase actually
// publishes (internal/health/events.go's eventPayload,
// internal/scheduler/events.go's taskDuePayload, and so on) is a flat
// struct with no nested objects - a dot-path or JSONPath implementation
// would be speculative complexity solving a problem this project doesn't
// have.
//
// A missing field fails closed (the condition does not match) rather
// than being treated as vacuously true - a condition referencing a field
// that doesn't exist on this event type is far more likely to be a
// configuration mistake than an intentional "match anything," and firing
// destructive actions on a silently-mismatched condition would be the
// wrong default.
func evaluateConditions(conditions []Condition, payload map[string]any) (ok bool, reason string) {
	for _, c := range conditions {
		val, present := payload[c.Field]
		if !present {
			return false, fmt.Sprintf("payload has no field %q", c.Field)
		}
		if fmt.Sprint(val) != c.Equals {
			return false, fmt.Sprintf("%s = %v, want %q", c.Field, val, c.Equals)
		}
	}
	return true, ""
}
