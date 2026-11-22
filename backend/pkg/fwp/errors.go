package fwp

// ErrorCode is a stable, machine-readable error identifier. Clients should
// branch on Code, never on Message (Message is for humans and may change
// wording between versions).
type ErrorCode string

const (
	// ErrUnsupportedMethod means the requested Method has no registered
	// handler on this build/platform. Distinguished from ErrUnavailable:
	// this method simply doesn't exist; that one exists but the
	// capability it represents isn't available right now.
	ErrUnsupportedMethod ErrorCode = "UNSUPPORTED_METHOD"

	// ErrInvalidParams means Params failed to decode or failed validation.
	ErrInvalidParams ErrorCode = "INVALID_PARAMS"

	// ErrInternal means the handler failed for a reason not attributable
	// to the request itself. Internal error messages are safe to show
	// (Weloury core never puts secrets in them - see SECURITY.md) but are
	// not actionable by the caller beyond "try again" or "report a bug".
	ErrInternal ErrorCode = "INTERNAL"

	// ErrUnauthorized means the request's bearer token was missing or
	// incorrect.
	ErrUnauthorized ErrorCode = "UNAUTHORIZED"

	// ErrCanceled means the request's context was canceled before the
	// handler finished (client disconnected, or sent TypeCancel).
	ErrCanceled ErrorCode = "CANCELED"

	// ErrNotFound means a referenced entity (process, service, health
	// check, ...) does not exist.
	ErrNotFound ErrorCode = "NOT_FOUND"

	// ErrUnavailable means the method exists but the underlying
	// capability is not available in this environment - e.g. a
	// Windows-only method called on Linux, or Docker inspection when no
	// container runtime is installed. See spec section 4, "gracefully
	// degrade functionality... the UI must clearly communicate
	// unavailable capabilities rather than crashing."
	ErrUnavailable ErrorCode = "UNAVAILABLE"
)

// ErrorObject is FWP's structured error shape, carried in
// Envelope.Error. It also implements the standard error interface so
// handlers can return it directly.
type ErrorObject struct {
	Code    ErrorCode `json:"code"`
	Message string    `json:"message"`

	// Retryable hints whether the same request is likely to succeed if
	// retried unchanged (e.g. a transient timeout) versus needing the
	// caller to change something first (e.g. INVALID_PARAMS).
	Retryable bool `json:"retryable,omitempty"`
}

// Error implements the error interface.
func (e *ErrorObject) Error() string {
	return string(e.Code) + ": " + e.Message
}

// NewError constructs an ErrorObject.
func NewError(code ErrorCode, message string) *ErrorObject {
	return &ErrorObject{Code: code, Message: message}
}
