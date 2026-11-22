package scheduler

import (
	"fmt"
	"strings"
)

// applyDefaultsAndValidate fills in unset optional fields and validates
// t in place. Called by Engine.Create/Update before anything is
// persisted or scheduled.
func (t *Task) applyDefaultsAndValidate() error {
	t.Name = strings.TrimSpace(t.Name)
	if t.Name == "" {
		return fmt.Errorf("%w: name is required", ErrInvalid)
	}

	switch t.Type {
	case Once:
		if t.RunAt.IsZero() {
			return fmt.Errorf("%w: run_at is required for a one-time task", ErrInvalid)
		}
	case Interval:
		if t.IntervalSeconds < MinIntervalSeconds || t.IntervalSeconds > MaxIntervalSeconds {
			return fmt.Errorf("%w: interval_seconds must be between %d and %d, got %d",
				ErrInvalid, MinIntervalSeconds, MaxIntervalSeconds, t.IntervalSeconds)
		}
	default:
		return fmt.Errorf("%w: unrecognized type %q", ErrInvalid, t.Type)
	}

	return nil
}
