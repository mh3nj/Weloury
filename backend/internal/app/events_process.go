package app

import "weloury/internal/eventbus"

// TypeProcessTerminationRequested is published every time
// process.terminate is called, whether the termination succeeded or
// failed - see processTerminateHandler. It is defined here rather than
// in internal/eventbus (which stays domain-agnostic - see its package
// doc comment) and rather than in internal/system (which has no
// dependency on the event bus at all, by design: an OS abstraction layer
// shouldn't need to know Weloury has an event bus). internal/app is where
// the FWP method handler that has both the outcome and access to the bus
// lives, making it the natural owner of this event's vocabulary.
//
// Named "requested" rather than "terminated": this fires the instant the
// termination syscall returns, which confirms the OS accepted the
// request, not that the process has actually exited yet. A future
// process-watching subsystem (Phase 4/5) that can observe real exits
// would be the right place to add a genuine TypeProcessStopped/
// TypeProcessCrashed event - see spec section 10's suggested event
// vocabulary.
const TypeProcessTerminationRequested eventbus.Type = "process.termination_requested"
