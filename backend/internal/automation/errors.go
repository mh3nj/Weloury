package automation

import "errors"

// ErrNotFound means a referenced rule ID does not exist.
var ErrNotFound = errors.New("automation rule not found")

// ErrInvalid means a Rule failed validation (see validate.go).
var ErrInvalid = errors.New("invalid automation rule")
