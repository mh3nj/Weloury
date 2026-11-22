package storage

import (
	"context"
	"database/sql"
	"time"

	"weloury/internal/metrics"
)

// MetricsRepository implements metrics.Repository using SQLite. Kept in
// internal/storage - not internal/metrics - for the same reason as
// HealthRepository: internal/metrics has no database/sql dependency at
// all, only the consumer-defined metrics.Repository interface.
type MetricsRepository struct {
	db *sql.DB
}

// NewMetricsRepository wraps db for metric aggregate persistence.
func NewMetricsRepository(db *sql.DB) *MetricsRepository {
	return &MetricsRepository{db: db}
}

// InsertBatch writes every aggregate in one transaction - the SQL-level
// counterpart to spec section 17's "use batching," matching
// internal/metrics.Engine's flush cycle producing all of a bucket's
// series at once.
func (r *MetricsRepository) InsertBatch(ctx context.Context, aggregates []metrics.Aggregate) error {
	if len(aggregates) == 0 {
		return nil
	}

	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }() // no-op if Commit succeeded below

	stmt, err := tx.PrepareContext(ctx, `
		INSERT INTO metric_aggregates (bucket_time, metric, scope, min_value, max_value, avg_value, sample_count)
		VALUES (?, ?, ?, ?, ?, ?, ?);
	`)
	if err != nil {
		return err
	}
	defer stmt.Close()

	for _, a := range aggregates {
		// BucketTime goes through sqliteTime (see timebind.go) so stored
		// text is fixed-width UTC - `ORDER BY bucket_time` and Prune's
		// `bucket_time < ?` compare correctly as text, and the value
		// round-trips through the driver's parse layouts on every platform.
		if _, err := stmt.ExecContext(ctx, sqliteTime(a.BucketTime), a.Metric, a.Scope, a.Min, a.Max, a.Avg, a.SampleCount); err != nil {
			return err
		}
	}

	return tx.Commit()
}

// Query returns aggregates for one series at or after since, oldest
// first, capped at limit.
func (r *MetricsRepository) Query(ctx context.Context, metric, scope string, since time.Time, limit int) ([]metrics.Aggregate, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT bucket_time, metric, scope, min_value, max_value, avg_value, sample_count
		FROM metric_aggregates
		WHERE metric = ? AND scope = ? AND bucket_time >= ?
		ORDER BY bucket_time ASC
		LIMIT ?;
	`, metric, scope, sqliteTime(since), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []metrics.Aggregate
	for rows.Next() {
		var a metrics.Aggregate
		if err := rows.Scan(&a.BucketTime, &a.Metric, &a.Scope, &a.Min, &a.Max, &a.Avg, &a.SampleCount); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// PruneOlderThan deletes aggregates older than cutoff - the metrics
// counterpart to EventRepository.PruneOlderThan, driven by
// Config.MetricsRetentionDays via internal/app's retention pruner.
func (r *MetricsRepository) PruneOlderThan(ctx context.Context, cutoff time.Time) (int64, error) {
	res, err := r.db.ExecContext(ctx, `DELETE FROM metric_aggregates WHERE bucket_time < ?;`, sqliteTime(cutoff))
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}
