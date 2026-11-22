package scheduler

import "errors"

// ErrNotFound means a referenced task ID does not exist.
var ErrNotFound = errors.New("scheduled task not found")

// ErrInvalid means a Task failed validation (see validate.go).
var ErrInvalid = errors.New("invalid scheduled task")
