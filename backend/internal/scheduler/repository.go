package scheduler

import "context"

// Repository persists Task configuration and scheduling state across
// restarts. A consumer-defined interface - see internal/health's
// Repository doc comment for why - implemented by
// internal/storage.SchedulerRepository using SQLite.
type Repository interface {
	// List returns every configured task, in no particular order.
	List(ctx context.Context) ([]Task, error)

	// Upsert persists t's full state (configuration and scheduling
	// fields alike - unlike internal/health.Repository, there is no
	// split between "config" and "live status" here: NextRunAt/
	// LastRunAt are set by Engine before every Upsert call, including
	// the one immediately after firing (see Engine.applyResult), so
	// there is no separate result-recording method to keep in sync with
	// this one.
	Upsert(ctx context.Context, t Task) error

	Delete(ctx context.Context, id string) error
}
