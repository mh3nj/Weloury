// Package fwp defines the Weloury Wire Protocol (FWP): the message envelope
// used for all communication between the Weloury desktop shell (Rust/React)
// and Weloury core (Go), and - in the future - between the desktop shell and
// a remote Weloury Agent.
//
// FWP is intentionally transport-agnostic at the type level. In Phase 1 it
// is carried over plain HTTP on a loopback port (see internal/ipc and
// ARCHITECTURE.md for why); the same Envelope shape is designed to later
// be carried over a TLS-secured connection to a remote agent without any
// change to method contracts.
//
// This package has no dependencies beyond the standard library and no
// knowledge of any specific method or domain - it is pure wire format.
package fwp

import "encoding/json"

// ProtocolVersion is the current FWP wire format version. Every Envelope
// carries it so a future incompatible change can be detected by either
// side rather than silently misinterpreted.
const ProtocolVersion = 1

// EnvelopeType identifies which of FWP's five message kinds an Envelope
// carries. Exactly which other fields are populated depends on Type.
type EnvelopeType string

const (
	// TypeRequest is sent client -> server to invoke a method.
	// Populates: ID, Method, Params.
	TypeRequest EnvelopeType = "request"

	// TypeResponse is sent server -> client with the successful result of
	// a Request. Populates: ID, Result.
	TypeResponse EnvelopeType = "response"

	// TypeError is sent server -> client when a Request fails, or by
	// either side to report a transport-level problem. Populates: ID
	// (when responding to a specific request), Error.
	TypeError EnvelopeType = "error"

	// TypeEvent is sent server -> client as a subscription push.
	// Populates: Channel, Seq, Payload.
	TypeEvent EnvelopeType = "event"

	// TypeCancel is sent client -> server to request cancellation of an
	// in-flight Request. Populates: ID (of the request being canceled).
	//
	// Phase 1's HTTP transport does not need this explicitly: aborting the
	// underlying HTTP request (e.g. via a JS AbortController) cancels the
	// request's context.Context on the server automatically. TypeCancel is
	// defined now so a future persistent-connection transport (WebSocket
	// or a remote agent's RPC stream) can express the same intent
	// explicitly without changing the Envelope shape.
	TypeCancel EnvelopeType = "cancel"
)

// Envelope is the single message shape exchanged over FWP.
type Envelope struct {
	V    int          `json:"v"`
	Type EnvelopeType `json:"type"`

	// ID correlates a request with its response/error/cancel. Required
	// for TypeRequest/TypeResponse/TypeError/TypeCancel; unused for
	// TypeEvent (events are correlated by Channel + Seq instead, since
	// they are not responses to a specific request).
	ID string `json:"id,omitempty"`

	// Method and Params are populated on TypeRequest only.
	Method string          `json:"method,omitempty"`
	Params json.RawMessage `json:"params,omitempty"`

	// Result is populated on TypeResponse only.
	Result json.RawMessage `json:"result,omitempty"`

	// Error is populated on TypeError only.
	Error *ErrorObject `json:"error,omitempty"`

	// Channel, Seq, and Payload are populated on TypeEvent only. Channel
	// is the event's type/topic (e.g. "system.heartbeat"); Seq is a
	// monotonic counter scoped to one event-stream connection (it resets
	// on reconnect) that a client can use to detect events dropped by the
	// bus's bounded per-subscriber buffer - see eventbus.Bus's doc
	// comment on why Publish drops rather than blocks.
	Channel string          `json:"channel,omitempty"`
	Seq     uint64          `json:"seq,omitempty"`
	Payload json.RawMessage `json:"payload,omitempty"`
}
