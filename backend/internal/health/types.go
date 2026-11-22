// Package health is Weloury's health check engine (spec section 9):
// user-defined, concurrently but boundedly executed checks against
// processes, services, network endpoints, commands, and resource
// thresholds, each tracked through a small state machine (UNKNOWN ->
// HEALTHY/DEGRADED/UNHEALTHY) with retry policy and full history-free
// "current status" persistence (see Repository's doc comment on why no
// per-result history table exists yet).
//
// Engine owns scheduling and concurrency (engine.go); checkers.go holds
// the actual per-CheckType logic as small, independently testable
// functions dispatched by type.
package health

import "time"

// State is a health check's current assessed condition. See spec
// section 9's exact vocabulary - reused verbatim rather than inventing
// Weloury-specific names.
type State string

const (
	StateUnknown   State = "UNKNOWN"
	StateHealthy   State = "HEALTHY"
	StateDegraded  State = "DEGRADED"
	StateUnhealthy State = "UNHEALTHY"
)

// CheckType selects which checker (checkers.go) evaluates a Check.
type CheckType string

const (
	CheckProcessExists   CheckType = "process_exists"
	CheckServiceState    CheckType = "service_state"
	CheckTCPPort         CheckType = "tcp_port"
	CheckHTTPEndpoint    CheckType = "http_endpoint"
	CheckCommand         CheckType = "command"
	CheckDiskThreshold   CheckType = "disk_threshold"
	CheckMemoryThreshold CheckType = "memory_threshold"
	CheckCPUThreshold    CheckType = "cpu_threshold"
)

func (t CheckType) valid() bool {
	switch t {
	case CheckProcessExists, CheckServiceState, CheckTCPPort, CheckHTTPEndpoint,
		CheckCommand, CheckDiskThreshold, CheckMemoryThreshold, CheckCPUThreshold:
		return true
	}
	return false
}

// isThreshold reports whether t is one of the three resource-threshold
// check types, which share validation rules (Threshold required) and
// additionally publish a more specific bus event than
// TypeHealthCheckFailed on transition into UNHEALTHY - see engine.go.
func (t CheckType) isThreshold() bool {
	return t == CheckDiskThreshold || t == CheckMemoryThreshold || t == CheckCPUThreshold
}

// RetryPolicy controls how many times, and how far apart, a failing
// check is retried before its failure is considered final for this
// evaluation cycle. Retries happen within a single scheduled evaluation
// (bounded by MaxRetries*RetryDelaySeconds), not across cycles - a check
// due again at its normal interval starts a fresh retry budget rather
// than accumulating state across runs.
type RetryPolicy struct {
	MaxRetries        int `json:"max_retries"`
	RetryDelaySeconds int `json:"retry_delay_seconds"`
}

// Bounds enforced by Check.Validate. These exist for the same reason
// spec section 9 requires bounded concurrency: an interval or timeout of
// zero (or absurdly small) would make a single misconfigured check
// capable of saturating Engine's execution semaphore on its own.
const (
	MinIntervalSeconds = 5
	MaxIntervalSeconds = 24 * 60 * 60 // one day
	MinTimeoutSeconds  = 1
	MaxTimeoutSeconds  = 60
	MaxRetries         = 5
	MaxRetryDelay      = 60
)

// Check is a user-configured health check definition - spec section 9's
// "ID, name, target, interval, timeout, retry policy" fields.
type Check struct {
	ID   string    `json:"id"`
	Name string    `json:"name"`
	Type CheckType `json:"type"`

	// Target's meaning depends on Type: a process name
	// (CheckProcessExists), a service name (CheckServiceState), a
	// "host:port" pair (CheckTCPPort), a URL (CheckHTTPEndpoint), or a
	// mount point (CheckDiskThreshold). Unused (empty) for
	// CheckMemoryThreshold/CheckCPUThreshold, which are machine-wide.
	Target string `json:"target"`

	// CommandArgs is argv for CheckCommand - index 0 is the executable,
	// the rest are its arguments. Never a shell string: see checkers.go
	// and SECURITY.md's "Health checks" section on why this is
	// exec'd directly, with no shell interpolation whatsoever.
	CommandArgs []string `json:"command_args,omitempty"`

	// Threshold is the percent-used (0-100) value at or above which a
	// disk/memory/cpu threshold check reports UNHEALTHY; see
	// engine.go's evaluateThreshold for the DEGRADED band this also
	// implies. Required (must be > 0) for the three threshold check
	// types; unused otherwise.
	Threshold float64 `json:"threshold,omitempty"`

	IntervalSeconds int         `json:"interval_seconds"`
	TimeoutSeconds  int         `json:"timeout_seconds"`
	Retry           RetryPolicy `json:"retry"`

	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// Result is the outcome of one evaluation (all retries within it
// resolved), passed to Repository.UpdateResult and used by Engine to
// decide whether an event fires - see engine.go.
//
// Deliberately just State + Message + CheckedAt, not a separate
// success/failure bool: both Engine's in-memory bookkeeping
// (trackedCheck.applyResult) and Repository.UpdateResult derive
// LastSuccessAt/LastFailureAt/FailureCount from State using the same
// three-way rule (HEALTHY = success, UNHEALTHY/DEGRADED = failure,
// UNKNOWN = neither) - see Repository.UpdateResult's doc comment for why
// UNKNOWN is excluded from both counters.
type Result struct {
	State     State
	Message   string
	CheckedAt time.Time
}

// Status is Check plus its live-tracked outcome - spec section 9's
// "current state, last successful check, last failure, failure count,
// metadata" - returned by every read API (List, Get, Create, Update).
type Status struct {
	Check
	State         State      `json:"state"`
	Message       string     `json:"message"`
	LastSuccessAt *time.Time `json:"last_success_at,omitempty"`
	LastFailureAt *time.Time `json:"last_failure_at,omitempty"`
	FailureCount  int        `json:"failure_count"`
}
