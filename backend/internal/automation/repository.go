package automation

import "context"

// Repository persists Rule configuration and live Status across
// restarts. A consumer-defined interface - see internal/health's
// Repository doc comment for why - implemented by
// internal/storage.AutomationRepository using SQLite.
//
// Same "no per-execution history table" design as internal/health: only
// the latest Status per rule is persisted. Every execution still
// publishes a bus event (TypeRuleExecuted, see events.go) that
// internal/app's event-persistence bridge durably records, which is
// where a full history belongs - see health.Repository's doc comment for
// the identical reasoning, which applies here unchanged.
type Repository interface {
	List(ctx context.Context) ([]Status, error)
	Upsert(ctx context.Context, r Rule) error
	Delete(ctx context.Context, id string) error
	// UpdateRunResult persists the outcome of an actual execution
	// attempt (RunSucceeded or RunFailed) - setting LastRunAt/
	// LastRunStatus/LastRunMessage, incrementing RunCount, and applying
	// result.Enabled/result.ConsecutiveFailures verbatim (Engine has
	// already computed these - see trackedRule.recordOutcome and
	// RunResult's doc comment on why they're written atomically with
	// the run outcome itself, not as a separate call). Not called for a
	// skipped attempt (RunSkippedCooldown/RunSkippedConditions): a skip
	// still fires TypeRuleExecuted on the bus (see events.go), which
	// internal/app's event-persistence bridge durably records, so
	// skips are not lost - they simply don't perturb "when did this
	// rule last actually do something" or the circuit breaker's
	// streak, which is what a rule's own LastRunAt/RunCount/
	// ConsecutiveFailures are for. Mirrors trackedRule.recordOutcome's
	// identical in-memory rule in engine.go - the two must apply the
	// same distinction, the same way health.Repository.UpdateResult and
	// trackedCheck.applyResult must stay in lockstep.
	UpdateRunResult(ctx context.Context, id string, result RunResult) error
}
