package health

import "weloury/internal/eventbus"

// Event types this package publishes. Defined here, in the domain
// package itself, per eventbus.Type's doc comment ("domain packages
// (process, service, health, ...) define their own Type constants
// alongside their own code") - health is one of the packages that
// comment names explicitly. Unlike internal/system (which by design has
// no event bus dependency at all - see events_process.go in
// internal/app), Engine is not an OS-abstraction layer; it is exactly
// the kind of long-lived, event-driven subsystem the event bus exists
// for, so it owns a *eventbus.Bus dependency directly instead of routing
// through internal/app the way process/service actions do.
const (
	// TypeCheckPassed fires when a check transitions into HEALTHY from
	// any other state - a "recovery", including the very first
	// successful evaluation after a check is created (transition from
	// UNKNOWN counts). It does not fire on every successful evaluation
	// while a check remains HEALTHY - see runOnce's transition-only
	// publishing rule, which exists to keep the event stream meaningful
	// rather than flooding it with a repeat event every interval.
	TypeCheckPassed eventbus.Type = "health.check_passed"

	// TypeCheckFailed fires when a check transitions into UNHEALTHY from
	// any other state, for every check type.
	TypeCheckFailed eventbus.Type = "health.check_failed"

	// TypeDiskThresholdExceeded and TypeMemoryThresholdExceeded satisfy
	// spec section 10's explicit event vocabulary. They fire alongside
	// (not instead of) TypeCheckFailed, only for CheckDiskThreshold/
	// CheckMemoryThreshold checks, only on the same UNHEALTHY
	// transition - see runOnce. Spec section 10 does not separately name
	// a CPU-threshold event, so CheckCPUThreshold checks publish
	// TypeCheckFailed/TypeCheckPassed only, consistent with every other
	// non-threshold check type.
	TypeDiskThresholdExceeded   eventbus.Type = "health.disk_threshold_exceeded"
	TypeMemoryThresholdExceeded eventbus.Type = "health.memory_threshold_exceeded"
)

// eventPayload is the structured payload attached to every event this
// package publishes.
type eventPayload struct {
	CheckID   string  `json:"check_id"`
	Name      string  `json:"name"`
	Type      string  `json:"type"`
	State     State   `json:"state"`
	Message   string  `json:"message"`
	Target    string  `json:"target,omitempty"`
	Threshold float64 `json:"threshold,omitempty"`
}
