package health

import "errors"

// ErrNotFound means a referenced check ID does not exist. Mirrors
// system.ErrNotFound's role - see internal/app's toFWPError-style
// translation in methods_health.go.
var ErrNotFound = errors.New("health check not found")

// ErrInvalid means a Check failed validation (see Check.Validate).
var ErrInvalid = errors.New("invalid health check")
