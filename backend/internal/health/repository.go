package health

import (
	"context"
	"time"
)

// Repository persists Check configuration and live Status across
// restarts (spec section 16: "Store things such as: ... health
// checks..."). Defined here, as a consumer-side interface, so this
// package has no dependency on database/sql or any specific persistence
// technology - internal/storage.HealthRepository implements it using
// SQLite, but Engine itself only ever sees this interface, which is what
// lets it be unit-tested with an in-memory fake (see engine_test.go)
// despite this sandbox's inability to build against modernc.org/sqlite
// - see DEVELOPMENT.md.
//
// There is deliberately no result-history method (no "list every past
// result for this check"): only the single latest Status per check is
// persisted. Full history of state changes is already captured
// elsewhere - Engine publishes a bus event on every state transition
// (see engine.go), and internal/app's existing event-persistence bridge
// (persistence.go) durably records every bus event to the `events`
// table. A second, parallel history table here would duplicate that
// with no new information.
type Repository interface {
	// List returns every configured check with its last-known live
	// status, in no particular order.
	List(ctx context.Context) ([]Status, error)

	// Upsert persists c's configuration, creating it if c.ID is new.
	// It does not touch the live-status columns (State, Message,
	// LastSuccessAt, LastFailureAt, FailureCount) - UpdateResult owns
	// those exclusively, so a config edit (Update) never resets a
	// check's observed history.
	Upsert(ctx context.Context, c Check) error

	Delete(ctx context.Context, id string) error

	// UpdateResult persists the outcome of one evaluation. It must apply
	// the same three-way rule Engine's own in-memory bookkeeping does
	// (see trackedCheck.applyResult in engine.go), so that reloading
	// via List after a restart reproduces the same FailureCount/
	// LastSuccessAt/LastFailureAt an uninterrupted in-memory run would
	// have had:
	//   - result.State == StateHealthy: set LastSuccessAt, reset
	//     FailureCount to 0.
	//   - result.State == StateDegraded or StateUnhealthy: set
	//     LastFailureAt, increment FailureCount.
	//   - result.State == StateUnknown: touch neither counter - an
	//     unknown result (e.g. a capability unavailable on this
	//     platform) is not evidence the target is unhealthy, so it must
	//     not extend or reset either streak.
	// Always updates State and Message regardless of which branch above
	// applies.
	UpdateResult(ctx context.Context, id string, result Result) error
}

// clock exists purely so tests can control time deterministically
// without sleeping - see engine_test.go's fakeClock. Defaults to
// time.Now via realClock in production.
type clock interface {
	Now() time.Time
}

type realClock struct{}

func (realClock) Now() time.Time { return time.Now().UTC() }
