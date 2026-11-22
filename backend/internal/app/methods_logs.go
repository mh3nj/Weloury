package app

import (
	"context"
	"encoding/json"

	"weloury/internal/ipc"
	"weloury/internal/logs"
	"weloury/pkg/fwp"
)

// registerLogsMethods wires the log engine (internal/logs, spec section
// 8) onto r. Live streaming needs no dedicated method: logs.Engine
// publishes logs.TypeEntry on the bus, so a client subscribes to it
// through the same /events NDJSON stream every other live feature uses.
func registerLogsMethods(r *ipc.Router, engine *logs.Engine) {
	r.Handle("logs.query", logsQueryHandler(engine))
}

type logsQueryParams struct {
	Level  string `json:"level,omitempty"`
	Search string `json:"search,omitempty"`
	Before uint64 `json:"before,omitempty"`
	Limit  int    `json:"limit,omitempty"`
}

func logsQueryHandler(engine *logs.Engine) ipc.HandlerFunc {
	return func(_ context.Context, params json.RawMessage) (any, error) {
		var p logsQueryParams
		if len(params) > 0 {
			if err := json.Unmarshal(params, &p); err != nil {
				return nil, fwp.NewError(fwp.ErrInvalidParams, err.Error())
			}
		}
		entries := engine.Query(logs.Query{
			Level:  p.Level,
			Search: p.Search,
			Before: p.Before,
			Limit:  p.Limit,
		})
		// Never nil over the wire - an empty result set should decode
		// to `[]` on the frontend, not `null`, so callers don't need a
		// null-check before ranging over it.
		if entries == nil {
			entries = []logs.Entry{}
		}
		return entries, nil
	}
}
