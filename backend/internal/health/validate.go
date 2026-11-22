package health

import (
	"fmt"
	"strings"
)

// applyDefaultsAndValidate fills in unset optional fields with defaults
// and validates c in place, returning ErrInvalid (wrapped with a
// specific reason) if it can't be made valid. Called by Engine.Create
// and Engine.Update before anything is persisted or scheduled - every
// Check the rest of this package ever runs has already passed this.
func (c *Check) applyDefaultsAndValidate() error {
	c.Name = strings.TrimSpace(c.Name)
	if c.Name == "" {
		return fmt.Errorf("%w: name is required", ErrInvalid)
	}
	if !c.Type.valid() {
		return fmt.Errorf("%w: unrecognized check type %q", ErrInvalid, c.Type)
	}

	if c.IntervalSeconds == 0 {
		c.IntervalSeconds = defaultIntervalSeconds
	}
	if c.IntervalSeconds < MinIntervalSeconds || c.IntervalSeconds > MaxIntervalSeconds {
		return fmt.Errorf("%w: interval_seconds must be between %d and %d, got %d",
			ErrInvalid, MinIntervalSeconds, MaxIntervalSeconds, c.IntervalSeconds)
	}

	if c.TimeoutSeconds == 0 {
		c.TimeoutSeconds = defaultTimeoutSeconds
	}
	if c.TimeoutSeconds < MinTimeoutSeconds || c.TimeoutSeconds > MaxTimeoutSeconds {
		return fmt.Errorf("%w: timeout_seconds must be between %d and %d, got %d",
			ErrInvalid, MinTimeoutSeconds, MaxTimeoutSeconds, c.TimeoutSeconds)
	}
	if c.TimeoutSeconds >= c.IntervalSeconds {
		return fmt.Errorf("%w: timeout_seconds (%d) must be less than interval_seconds (%d)",
			ErrInvalid, c.TimeoutSeconds, c.IntervalSeconds)
	}

	if c.Retry.MaxRetries < 0 || c.Retry.MaxRetries > MaxRetries {
		return fmt.Errorf("%w: retry.max_retries must be between 0 and %d, got %d",
			ErrInvalid, MaxRetries, c.Retry.MaxRetries)
	}
	if c.Retry.RetryDelaySeconds < 0 || c.Retry.RetryDelaySeconds > MaxRetryDelay {
		return fmt.Errorf("%w: retry.retry_delay_seconds must be between 0 and %d, got %d",
			ErrInvalid, MaxRetryDelay, c.Retry.RetryDelaySeconds)
	}

	switch c.Type {
	case CheckProcessExists, CheckServiceState, CheckTCPPort, CheckHTTPEndpoint, CheckDiskThreshold:
		c.Target = strings.TrimSpace(c.Target)
		if c.Target == "" {
			return fmt.Errorf("%w: target is required for %s checks", ErrInvalid, c.Type)
		}
	case CheckCommand:
		if len(c.CommandArgs) == 0 {
			return fmt.Errorf("%w: command_args is required for command checks", ErrInvalid)
		}
	}

	if c.Type.isThreshold() && c.Threshold <= 0 {
		return fmt.Errorf("%w: threshold must be greater than 0 for %s checks", ErrInvalid, c.Type)
	}

	return nil
}

const (
	defaultIntervalSeconds = 30
	defaultTimeoutSeconds  = 5
)
