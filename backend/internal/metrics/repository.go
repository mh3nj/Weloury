package metrics

import (
	"context"
	"time"
)

// Repository persists Aggregate buckets and serves historical queries.
// A consumer-defined interface - see the package doc comment on why -
// implemented by internal/storage.MetricsRepository using SQLite.
type Repository interface {
	// InsertBatch persists every aggregate produced by one flush cycle
	// in a single call, so a flush is one write operation regardless of
	// how many series were active - see engine.go's flush.
	InsertBatch(ctx context.Context, aggregates []Aggregate) error

	// Query returns aggregates for one series (metric+scope) at or
	// after since, oldest first, capped at limit.
	Query(ctx context.Context, metric, scope string, since time.Time, limit int) ([]Aggregate, error)

	// PruneOlderThan deletes aggregates older than cutoff, returning the
	// number of rows removed - the metrics-engine counterpart to
	// storage.EventRepository.PruneOlderThan, driven by
	// config.Config.MetricsRetentionDays via internal/app's retention
	// pruner (see retention.go).
	PruneOlderThan(ctx context.Context, cutoff time.Time) (int64, error)
}
