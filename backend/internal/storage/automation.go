package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"

	"weloury/internal/automation"
)

// AutomationRepository implements automation.Repository using SQLite.
// Kept in internal/storage - not internal/automation - so
// internal/automation has no database/sql dependency at all; see
// automation.Repository's doc comment.
type AutomationRepository struct {
	db *sql.DB
}

// NewAutomationRepository wraps db for automation rule persistence.
func NewAutomationRepository(db *sql.DB) *AutomationRepository {
	return &AutomationRepository{db: db}
}

func (r *AutomationRepository) List(ctx context.Context) ([]automation.Status, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT id, name, enabled, trigger, conditions, actions, cooldown_seconds,
		       created_at, updated_at, last_run_at, last_run_status, last_run_message, run_count,
		       consecutive_failures
		FROM automation_rules;
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []automation.Status
	for rows.Next() {
		st, err := scanAutomationStatus(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, st)
	}
	return out, rows.Err()
}

func (r *AutomationRepository) Upsert(ctx context.Context, rule automation.Rule) error {
	triggerJSON, err := json.Marshal(rule.Trigger)
	if err != nil {
		return fmt.Errorf("marshal trigger: %w", err)
	}
	conditionsJSON, err := json.Marshal(rule.Conditions)
	if err != nil {
		return fmt.Errorf("marshal conditions: %w", err)
	}
	actionsJSON, err := json.Marshal(rule.Actions)
	if err != nil {
		return fmt.Errorf("marshal actions: %w", err)
	}

	_, err = r.db.ExecContext(ctx, `
		INSERT INTO automation_rules (
			id, name, enabled, trigger, conditions, actions, cooldown_seconds, created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			name = excluded.name,
			enabled = excluded.enabled,
			trigger = excluded.trigger,
			conditions = excluded.conditions,
			actions = excluded.actions,
			cooldown_seconds = excluded.cooldown_seconds,
			updated_at = excluded.updated_at;
	`,
		rule.ID, rule.Name, rule.Enabled, string(triggerJSON), string(conditionsJSON), string(actionsJSON),
		rule.CooldownSeconds, sqliteTime(rule.CreatedAt), sqliteTime(rule.UpdatedAt),
	)
	return err
}

func (r *AutomationRepository) Delete(ctx context.Context, id string) error {
	_, err := r.db.ExecContext(ctx, `DELETE FROM automation_rules WHERE id = ?;`, id)
	return err
}

// UpdateRunResult is a single UPDATE, run only for actual execution
// attempts (never skips) - see automation.Repository.UpdateRunResult's
// doc comment for why, and internal/health's analogous UpdateResult for
// the same atomic-single-statement approach (avoiding a separate
// SELECT+UPDATE from Go that could race a concurrent execution of the
// same rule). result.Enabled and result.ConsecutiveFailures are written
// verbatim, not derived here - Engine has already computed them (see
// trackedRule.recordOutcome) - in the same statement as the rest of the
// run outcome specifically so a crash between "run recorded" and "circuit
// breaker state persisted" can't happen; see RunResult's doc comment.
func (r *AutomationRepository) UpdateRunResult(ctx context.Context, id string, result automation.RunResult) error {
	res, err := r.db.ExecContext(ctx, `
		UPDATE automation_rules SET
			last_run_at = ?,
			last_run_status = ?,
			last_run_message = ?,
			run_count = run_count + 1,
			enabled = ?,
			consecutive_failures = ?
		WHERE id = ?;
	`, sqliteTime(result.RanAt), string(result.Status), result.Message, result.Enabled, result.ConsecutiveFailures, id)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return automation.ErrNotFound
	}
	return nil
}

type automationRow interface {
	Scan(dest ...any) error
}

func scanAutomationStatus(row automationRow) (automation.Status, error) {
	var (
		st                                       automation.Status
		triggerJSON, conditionsJSON, actionsJSON string
		lastRunAt                                sql.NullTime
		lastRunStatus                            string
	)
	if err := row.Scan(
		&st.ID, &st.Name, &st.Enabled, &triggerJSON, &conditionsJSON, &actionsJSON, &st.CooldownSeconds,
		&st.CreatedAt, &st.UpdatedAt, &lastRunAt, &lastRunStatus, &st.LastRunMessage, &st.RunCount,
		&st.ConsecutiveFailures,
	); err != nil {
		return automation.Status{}, err
	}

	if err := json.Unmarshal([]byte(triggerJSON), &st.Trigger); err != nil {
		return automation.Status{}, fmt.Errorf("unmarshal trigger for rule %s: %w", st.ID, err)
	}
	if err := json.Unmarshal([]byte(conditionsJSON), &st.Conditions); err != nil {
		return automation.Status{}, fmt.Errorf("unmarshal conditions for rule %s: %w", st.ID, err)
	}
	if err := json.Unmarshal([]byte(actionsJSON), &st.Actions); err != nil {
		return automation.Status{}, fmt.Errorf("unmarshal actions for rule %s: %w", st.ID, err)
	}
	st.LastRunStatus = automation.RunStatus(lastRunStatus)
	if lastRunAt.Valid {
		t := lastRunAt.Time
		st.LastRunAt = &t
	}
	return st, nil
}
