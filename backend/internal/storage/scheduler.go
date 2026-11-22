package storage

import (
	"context"
	"database/sql"
	"time"

	"weloury/internal/scheduler"
)

// SchedulerRepository implements scheduler.Repository using SQLite.
// Kept in internal/storage - not internal/scheduler - so
// internal/scheduler has no database/sql dependency at all; see
// scheduler.Repository's doc comment.
type SchedulerRepository struct {
	db *sql.DB
}

// NewSchedulerRepository wraps db for scheduled task persistence.
func NewSchedulerRepository(db *sql.DB) *SchedulerRepository {
	return &SchedulerRepository{db: db}
}

func (r *SchedulerRepository) List(ctx context.Context) ([]scheduler.Task, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT id, name, type, run_at, interval_seconds, enabled, next_run_at, last_run_at, created_at, updated_at
		FROM scheduler_tasks;
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []scheduler.Task
	for rows.Next() {
		t, err := scanSchedulerTask(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

func (r *SchedulerRepository) Upsert(ctx context.Context, t scheduler.Task) error {
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO scheduler_tasks (
			id, name, type, run_at, interval_seconds, enabled, next_run_at, last_run_at, created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			name = excluded.name,
			type = excluded.type,
			run_at = excluded.run_at,
			interval_seconds = excluded.interval_seconds,
			enabled = excluded.enabled,
			next_run_at = excluded.next_run_at,
			last_run_at = excluded.last_run_at,
			updated_at = excluded.updated_at;
	`,
		t.ID, t.Name, string(t.Type), nullableTime(t.RunAt), t.IntervalSeconds, t.Enabled,
		nullableTime(t.NextRunAt), nullableTime(derefTime(t.LastRunAt)), sqliteTime(t.CreatedAt), sqliteTime(t.UpdatedAt),
	)
	return err
}

func (r *SchedulerRepository) Delete(ctx context.Context, id string) error {
	_, err := r.db.ExecContext(ctx, `DELETE FROM scheduler_tasks WHERE id = ?;`, id)
	return err
}

// nullableTime stores a zero time.Time as SQL NULL rather than SQLite's
// text-formatted zero value ("0001-01-01 ..."), and otherwise binds via
// sqliteTime - see timebind.go for why every time Weloury writes goes
// through that canonical text format (in short: binding a time.Time
// directly makes the driver store t.String(), whose local-zone suffix is
// not parseable back on every platform - notably Windows - and whose
// variable-width form does not sort correctly as text).
//
// Task.RunAt is legitimately unset (zero) for Interval tasks (see Task's
// doc comment), and a NULL round-trips back through scanSchedulerTask as
// a zero time.Time either way - but storing NULL keeps the column's
// actual content honest about "this task has no RunAt" rather than a
// magic date string a future query would need to know to special-case.
func nullableTime(t time.Time) any {
	if t.IsZero() {
		return nil
	}
	return sqliteTime(t)
}

// derefTime maps a nil *time.Time to the zero time.Time so nullableTime
// can treat "no timestamp" uniformly; a nil pointer and a zero time both
// store as SQL NULL.
func derefTime(t *time.Time) time.Time {
	if t == nil {
		return time.Time{}
	}
	return *t
}

type schedulerRow interface {
	Scan(dest ...any) error
}

func scanSchedulerTask(row schedulerRow) (scheduler.Task, error) {
	var (
		t                scheduler.Task
		typ              string
		runAt, nextRunAt sql.NullTime
		lastRunAt        sql.NullTime
	)
	if err := row.Scan(
		&t.ID, &t.Name, &typ, &runAt, &t.IntervalSeconds, &t.Enabled, &nextRunAt, &lastRunAt, &t.CreatedAt, &t.UpdatedAt,
	); err != nil {
		return scheduler.Task{}, err
	}
	t.Type = scheduler.Type(typ)
	if runAt.Valid {
		t.RunAt = runAt.Time
	}
	if nextRunAt.Valid {
		t.NextRunAt = nextRunAt.Time
	}
	if lastRunAt.Valid {
		lt := lastRunAt.Time
		t.LastRunAt = &lt
	}
	return t, nil
}
