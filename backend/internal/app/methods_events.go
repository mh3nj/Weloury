package app

import (
	"context"
	"encoding/json"

	"weloury/internal/eventbus"
	"weloury/internal/ipc"
	"weloury/internal/storage"
	"weloury/pkg/fwp"
)

// registerEventsMethods wires up the Events screen's one read method:
// browsing persisted event history. Live events already reach the
// frontend via the /events NDJSON stream every subsystem publishes onto
// (see internal/ipc); events.recent exists for the complementary need -
// "what happened while I wasn't watching" / "what happened before I
// opened this screen" - served from storage.EventRepository, which
// internal/app's event-persistence bridge (persistence.go) already keeps
// durably in sync with the bus.
func registerEventsMethods(r *ipc.Router, events *storage.EventRepository) {
	r.Handle("events.recent", eventsRecentHandler(events))
}

type eventsRecentParams struct {
	// Limit caps how many events are returned, newest first. Defaults
	// to 100, clamped to maxEventsRecentLimit regardless of what's
	// requested - the same "bounded regardless of caller input"
	// discipline internal/logs.Query and metrics.history apply.
	Limit int `json:"limit,omitempty"`
}

const (
	defaultEventsRecentLimit = 100
	maxEventsRecentLimit     = 500
)

func eventsRecentHandler(events *storage.EventRepository) ipc.HandlerFunc {
	return func(ctx context.Context, params json.RawMessage) (any, error) {
		var p eventsRecentParams
		if len(params) > 0 {
			if err := json.Unmarshal(params, &p); err != nil {
				return nil, fwp.NewError(fwp.ErrInvalidParams, err.Error())
			}
		}
		limit := p.Limit
		if limit <= 0 {
			limit = defaultEventsRecentLimit
		}
		if limit > maxEventsRecentLimit {
			limit = maxEventsRecentLimit
		}

		recent, err := events.Recent(ctx, limit)
		if err != nil {
			return nil, fwp.NewError(fwp.ErrInternal, err.Error())
		}
		if recent == nil {
			recent = []eventbus.Event{}
		}
		return recent, nil
	}
}
