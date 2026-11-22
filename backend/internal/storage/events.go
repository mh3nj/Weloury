package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"time"

	"weloury/internal/eventbus"
)

// EventRepository persists eventbus.Event records and enforces retention.
type EventRepository struct {
	db *sql.DB
}

// NewEventRepository wraps db for event persistence.
func NewEventRepository(db *sql.DB) *EventRepository {
	return &EventRepository{db: db}
}

// Insert persists a single event.
func (r *EventRepository) Insert(ctx context.Context, e eventbus.Event) error {
	payload := e.Payload
	if payload == nil {
		payload = json.RawMessage("{}")
	}
	// Timestamp goes through sqliteTime (see timebind.go): binding a
	// time.Time directly makes the driver store t.String(), which is not
	// parseable back on every platform (Windows local zone names) and does
	// not sort lexicographically as stored text - `ORDER BY timestamp` and
	// PruneOlderThan's `timestamp < ?` comparison rely on both.
	_, err := r.db.ExecContext(ctx,
		`INSERT INTO events (id, timestamp, type, source, severity, payload) VALUES (?, ?, ?, ?, ?, ?);`,
		e.ID, sqliteTime(e.Timestamp), string(e.Type), e.Source, string(e.Severity), string(payload),
	)
	return err
}

// Recent returns up to limit most recent events, newest first.
func (r *EventRepository) Recent(ctx context.Context, limit int) ([]eventbus.Event, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT id, timestamp, type, source, severity, payload FROM events ORDER BY timestamp DESC LIMIT ?;`,
		limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []eventbus.Event
	for rows.Next() {
		var (
			e       eventbus.Event
			typ     string
			sev     string
			payload string
		)
		if err := rows.Scan(&e.ID, &e.Timestamp, &typ, &e.Source, &sev, &payload); err != nil {
			return nil, err
		}
		e.Type = eventbus.Type(typ)
		e.Severity = eventbus.Severity(sev)
		e.Payload = json.RawMessage(payload)
		out = append(out, e)
	}
	return out, rows.Err()
}

// PruneOlderThan deletes events with a timestamp before cutoff, returning
// the number of rows removed. Called periodically by the application layer
// using Config.EventsRetentionDays - see spec section 16/17, "do not store
// enormous amounts of high-frequency raw data forever."
func (r *EventRepository) PruneOlderThan(ctx context.Context, cutoff time.Time) (int64, error) {
	res, err := r.db.ExecContext(ctx, `DELETE FROM events WHERE timestamp < ?;`, sqliteTime(cutoff))
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}
