// Package ipc implements Weloury core's local IPC listener: FWP (see
// pkg/fwp) carried over plain HTTP on a loopback port.
//
// # Why HTTP instead of a WebSocket
//
// A single persistent WebSocket is the more obvious transport for a
// protocol with commands, streaming events, and cancellation. We chose
// two plain HTTP endpoints instead for Phase 1:
//
//   - POST /rpc    one Envelope in, one Envelope back.
//   - GET  /events a long-lived NDJSON stream of event Envelopes.
//
// Go's standard library has no WebSocket implementation, so a WS transport
// would mean either a hand-rolled RFC 6455 frame parser (real, easy to get
// subtly wrong in ways that only show up under load - masking, control
// frames, fragmentation) or a third-party dependency chosen without the
// ability to compile against it in this environment. Plain net/http,
// which every line of this package is built on, is something we can
// reason about with confidence.
//
// This costs some elegance (two connections instead of one; cancellation
// happens because the client aborts an HTTP request rather than an
// explicit protocol message in the common case) but loses no
// functionality FWP is meant to provide, and every functional requirement
// - commands, responses, streaming events, subscriptions, errors,
// cancellation - is met. See handlers.go for exactly how each maps to
// net/http primitives. A future revision can add a WebSocket transport
// alongside this one without changing pkg/fwp.Envelope or any method
// contract.
package ipc

import (
	"context"
	"encoding/json"

	"weloury/pkg/fwp"
)

// HandlerFunc implements one FWP method. It receives the decoded params
// and returns a JSON-serializable result or an error (typically a
// *fwp.ErrorObject for a well-classified failure; any other error is
// reported to the caller as fwp.ErrUnsupportedMethod's sibling,
// fwp.ErrInternal). Handlers must respect ctx cancellation for any
// non-trivial work - see the package doc comment on how cancellation
// reaches ctx.
type HandlerFunc func(ctx context.Context, params json.RawMessage) (any, error)

// Router maps FWP method names to handlers.
type Router struct {
	handlers map[string]HandlerFunc
}

// NewRouter creates an empty Router.
func NewRouter() *Router {
	return &Router{handlers: make(map[string]HandlerFunc)}
}

// Handle registers handler for method. Handle panics on a duplicate
// registration: two handlers for the same method name is a programming
// error that should fail loudly at startup, not be silently resolved by
// "last registration wins" or discovered at runtime.
func (r *Router) Handle(method string, handler HandlerFunc) {
	if _, exists := r.handlers[method]; exists {
		panic("ipc: method already registered: " + method)
	}
	r.handlers[method] = handler
}

// Dispatch invokes the handler registered for env.Method and returns a
// well-formed response or error Envelope carrying the same correlation
// ID. Dispatch itself never panics on a missing method or a handler
// error.
func (r *Router) Dispatch(ctx context.Context, env fwp.Envelope) fwp.Envelope {
	handler, ok := r.handlers[env.Method]
	if !ok {
		return errorEnvelope(env.ID, fwp.ErrUnsupportedMethod, "no such method: "+env.Method)
	}

	result, err := handler(ctx, env.Params)
	if err != nil {
		if errObj, ok := err.(*fwp.ErrorObject); ok {
			return errorEnvelopeFrom(env.ID, errObj)
		}
		return errorEnvelope(env.ID, fwp.ErrInternal, err.Error())
	}

	resultJSON, err := json.Marshal(result)
	if err != nil {
		return errorEnvelope(env.ID, fwp.ErrInternal, "marshal result: "+err.Error())
	}

	return fwp.Envelope{
		V:      fwp.ProtocolVersion,
		Type:   fwp.TypeResponse,
		ID:     env.ID,
		Result: resultJSON,
	}
}

func errorEnvelope(id string, code fwp.ErrorCode, message string) fwp.Envelope {
	return errorEnvelopeFrom(id, fwp.NewError(code, message))
}

func errorEnvelopeFrom(id string, errObj *fwp.ErrorObject) fwp.Envelope {
	return fwp.Envelope{
		V:     fwp.ProtocolVersion,
		Type:  fwp.TypeError,
		ID:    id,
		Error: errObj,
	}
}
