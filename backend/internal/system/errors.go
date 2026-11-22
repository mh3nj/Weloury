package system

import "errors"

// ErrUnavailable indicates a capability that exists conceptually but is
// not available on the current platform or in the current environment
// (e.g. a metric that requires a privilege this process doesn't have, or
// a Windows-only concept queried on Linux).
//
// internal/system never returns fwp.ErrUnavailable directly - this
// package has no dependency on the protocol layer (see the package doc
// comment). internal/app's method handlers are what translate this
// sentinel into the wire-level error; see spec section 4, "gracefully
// degrade functionality... the UI must clearly communicate unavailable
// capabilities rather than crashing."
var ErrUnavailable = errors.New("capability not available on this platform")

// ErrNotFound indicates the referenced process (or, in future phases,
// service/health check/...) does not exist - most commonly because it
// exited between being listed and being acted on, which for a process
// manager is routine rather than exceptional.
var ErrNotFound = errors.New("not found")
