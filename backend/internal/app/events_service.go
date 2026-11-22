package app

import "weloury/internal/eventbus"

// TypeServiceActionRequested is published every time
// service.start/stop/restart/set_enabled is called, whether it succeeded
// or failed - see runServiceAction in methods_service.go. Defined here
// rather than internal/eventbus or internal/system for the same reasons
// TypeProcessTerminationRequested is - see events_process.go's doc
// comment, which this mirrors.
//
// One event type covers all four actions (distinguished by the
// payload's Action field) rather than four separate Types: they're the
// same shape of event - "a state-changing service action was attempted"
// - differing only in which action, which a payload field expresses more
// naturally than four near-identical Type constants a subscriber would
// need to filter on anyway to treat generically.
const TypeServiceActionRequested eventbus.Type = "service.action_requested"
