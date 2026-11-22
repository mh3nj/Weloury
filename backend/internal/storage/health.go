package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"

	"weloury/internal/health"
)

// HealthRepository implements health.Repository using SQLite. Kept in
// internal/storage - not internal/health - so internal/health has no
// database/sql dependency at all; see health.Repository's doc comment.
type HealthRepository struct {
	db *sql.DB
}

// NewHealthRepository wraps db for health check persistence.
func NewHealthRepository(db *sql.DB) *HealthRepository {
	return &HealthRepository{db: db}
}

func (r *HealthRepository) List(ctx context.Context) ([]health.Status, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT id, name, type, target, command_args, threshold,
		       interval_seconds, timeout_seconds, max_retries, retry_delay_seconds,
		       created_at, updated_at,
		       state, message, last_success_at, last_failure_at, failure_count
		FROM health_checks;
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []health.Status
	for rows.Next() {
		st, err := scanHealthStatus(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, st)
	}
	return out, rows.Err()
}

func (r *HealthRepository) Upsert(ctx context.Context, c health.Check) error {
	args, err := json.Marshal(c.CommandArgs)
	if err != nil {
		return fmt.Errorf("marshal command_args: %w", err)
	}
	_, err = r.db.ExecContext(ctx, `
		INSERT INTO health_checks (
			id, name, type, target, command_args, threshold,
			interval_seconds, timeout_seconds, max_retries, retry_delay_seconds,
			created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			name = excluded.name,
			type = excluded.type,
			target = excluded.target,
			command_args = excluded.command_args,
			threshold = excluded.threshold,
			interval_seconds = excluded.interval_seconds,
			timeout_seconds = excluded.timeout_seconds,
			max_retries = excluded.max_retries,
			retry_delay_seconds = excluded.retry_delay_seconds,
			updated_at = excluded.updated_at;
	`,
		c.ID, c.Name, string(c.Type), c.Target, string(args), c.Threshold,
		c.IntervalSeconds, c.TimeoutSeconds, c.Retry.MaxRetries, c.Retry.RetryDelaySeconds,
		sqliteTime(c.CreatedAt), sqliteTime(c.UpdatedAt),
	)
	return err
}

func (r *HealthRepository) Delete(ctx context.Context, id string) error {
	_, err := r.db.ExecContext(ctx, `DELETE FROM health_checks WHERE id = ?;`, id)
	return err
}

// UpdateResult applies the exact three-way rule documented on
// health.Repository.UpdateResult, expressed here as a single SQL
// statement using CASE expressions so the read-modify-write happens
// atomically in SQLite rather than as a separate SELECT+UPDATE from Go
// (which would race a concurrent RunNow/scheduled evaluation of the
// same check).
func (r *HealthRepository) UpdateResult(ctx context.Context, id string, result health.Result) error {
	res, err := r.db.ExecContext(ctx, `
		UPDATE health_checks SET
			state = ?,
			message = ?,
			last_success_at = CASE WHEN ? = 'HEALTHY' THEN ? ELSE last_success_at END,
			last_failure_at = CASE WHEN ? IN ('DEGRADED', 'UNHEALTHY') THEN ? ELSE last_failure_at END,
			failure_count = CASE
				WHEN ? = 'HEALTHY' THEN 0
				WHEN ? IN ('DEGRADED', 'UNHEALTHY') THEN failure_count + 1
				ELSE failure_count
			END
		WHERE id = ?;
	`,
		string(result.State), result.Message,
		string(result.State), sqliteTime(result.CheckedAt),
		string(result.State), sqliteTime(result.CheckedAt),
		string(result.State), string(result.State),
		id,
	)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return health.ErrNotFound
	}
	return nil
}

// healthRow is satisfied by both *sql.Rows and *sql.Row, letting
// scanHealthStatus serve both List (multi-row) and any future
// single-row lookup without duplicating the scan.
type healthRow interface {
	Scan(dest ...any) error
}

func scanHealthStatus(row healthRow) (health.Status, error) {
	var (
		st                        health.Status
		typ, commandArgsJSON      string
		lastSuccessAt, lastFailAt sql.NullTime
	)
	if err := row.Scan(
		&st.ID, &st.Name, &typ, &st.Target, &commandArgsJSON, &st.Threshold,
		&st.IntervalSeconds, &st.TimeoutSeconds, &st.Retry.MaxRetries, &st.Retry.RetryDelaySeconds,
		&st.CreatedAt, &st.UpdatedAt,
		&st.State, &st.Message, &lastSuccessAt, &lastFailAt, &st.FailureCount,
	); err != nil {
		return health.Status{}, err
	}
	st.Type = health.CheckType(typ)
	if err := json.Unmarshal([]byte(commandArgsJSON), &st.CommandArgs); err != nil {
		return health.Status{}, fmt.Errorf("unmarshal command_args for check %s: %w", st.ID, err)
	}
	if lastSuccessAt.Valid {
		t := lastSuccessAt.Time
		st.LastSuccessAt = &t
	}
	if lastFailAt.Valid {
		t := lastFailAt.Time
		st.LastFailureAt = &t
	}
	return st, nil
}
