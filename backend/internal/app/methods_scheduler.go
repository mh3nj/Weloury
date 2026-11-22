package app

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"weloury/internal/ipc"
	"weloury/internal/scheduler"
	"weloury/pkg/fwp"
)

// registerSchedulerMethods wires internal/scheduler onto r.
func registerSchedulerMethods(r *ipc.Router, engine *scheduler.Engine) {
	r.Handle("scheduler.list", schedulerListHandler(engine))
	r.Handle("scheduler.get", schedulerGetHandler(engine))
	r.Handle("scheduler.create", schedulerCreateHandler(engine))
	r.Handle("scheduler.update", schedulerUpdateHandler(engine))
	r.Handle("scheduler.delete", schedulerDeleteHandler(engine))
	r.Handle("scheduler.set_enabled", schedulerSetEnabledHandler(engine))
}

// toSchedulerFWPError translates a scheduler package error into a
// wire-level FWP error - mirrors toHealthFWPError in methods_health.go.
func toSchedulerFWPError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, scheduler.ErrNotFound) {
		return fwp.NewError(fwp.ErrNotFound, err.Error())
	}
	if errors.Is(err, scheduler.ErrInvalid) {
		return fwp.NewError(fwp.ErrInvalidParams, err.Error())
	}
	return fwp.NewError(fwp.ErrInternal, err.Error())
}

func schedulerListHandler(engine *scheduler.Engine) ipc.HandlerFunc {
	return func(_ context.Context, _ json.RawMessage) (any, error) {
		return engine.List(), nil
	}
}

type schedulerIDParams struct {
	ID string `json:"id"`
}

func schedulerGetHandler(engine *scheduler.Engine) ipc.HandlerFunc {
	return func(_ context.Context, params json.RawMessage) (any, error) {
		var p schedulerIDParams
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, fwp.NewError(fwp.ErrInvalidParams, err.Error())
		}
		task, ok := engine.Get(p.ID)
		if !ok {
			return nil, fwp.NewError(fwp.ErrNotFound, "no scheduled task with that id")
		}
		return task, nil
	}
}

// schedulerTaskParams mirrors scheduler.Task's create/update wire shape -
// a separate type so ID/Enabled/NextRunAt/LastRunAt/CreatedAt/UpdatedAt
// (all engine-assigned, never caller-set) simply aren't fields a client
// can set through this API, the same reasoning healthCheckParams uses.
type schedulerTaskParams struct {
	Name            string         `json:"name"`
	Type            scheduler.Type `json:"type"`
	RunAt           string         `json:"run_at,omitempty"` // RFC3339, e.g. "2026-08-25T17:00:00-07:00" - see scheduler.Task's doc comment on timezone handling
	IntervalSeconds int            `json:"interval_seconds,omitempty"`
}

func (p schedulerTaskParams) toTask() (scheduler.Task, error) {
	t := scheduler.Task{Name: p.Name, Type: p.Type, IntervalSeconds: p.IntervalSeconds}
	if p.RunAt != "" {
		parsed, err := parseRFC3339(p.RunAt)
		if err != nil {
			return scheduler.Task{}, fwp.NewError(fwp.ErrInvalidParams, "run_at must be RFC3339, e.g. 2026-08-25T17:00:00-07:00: "+err.Error())
		}
		t.RunAt = parsed
	}
	return t, nil
}

// parseRFC3339 parses an offset-carrying timestamp string - see
// scheduler.Task's doc comment on why RunAt is accepted this way rather
// than as a bare wall-clock string.
func parseRFC3339(s string) (time.Time, error) {
	return time.Parse(time.RFC3339, s)
}

func schedulerCreateHandler(engine *scheduler.Engine) ipc.HandlerFunc {
	return func(ctx context.Context, params json.RawMessage) (any, error) {
		var p schedulerTaskParams
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, fwp.NewError(fwp.ErrInvalidParams, err.Error())
		}
		task, err := p.toTask()
		if err != nil {
			return nil, err
		}
		created, err := engine.Create(ctx, task)
		if err != nil {
			return nil, toSchedulerFWPError(err)
		}
		return created, nil
	}
}

type schedulerUpdateParams struct {
	ID string `json:"id"`
	schedulerTaskParams
}

func schedulerUpdateHandler(engine *scheduler.Engine) ipc.HandlerFunc {
	return func(ctx context.Context, params json.RawMessage) (any, error) {
		var p schedulerUpdateParams
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, fwp.NewError(fwp.ErrInvalidParams, err.Error())
		}
		if p.ID == "" {
			return nil, fwp.NewError(fwp.ErrInvalidParams, "id is required")
		}
		task, err := p.schedulerTaskParams.toTask()
		if err != nil {
			return nil, err
		}
		updated, err := engine.Update(ctx, p.ID, task)
		if err != nil {
			return nil, toSchedulerFWPError(err)
		}
		return updated, nil
	}
}

func schedulerDeleteHandler(engine *scheduler.Engine) ipc.HandlerFunc {
	return func(ctx context.Context, params json.RawMessage) (any, error) {
		var p schedulerIDParams
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, fwp.NewError(fwp.ErrInvalidParams, err.Error())
		}
		if err := engine.Delete(ctx, p.ID); err != nil {
			return nil, toSchedulerFWPError(err)
		}
		return struct{}{}, nil
	}
}

type schedulerSetEnabledParams struct {
	ID      string `json:"id"`
	Enabled bool   `json:"enabled"`
}

func schedulerSetEnabledHandler(engine *scheduler.Engine) ipc.HandlerFunc {
	return func(ctx context.Context, params json.RawMessage) (any, error) {
		var p schedulerSetEnabledParams
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, fwp.NewError(fwp.ErrInvalidParams, err.Error())
		}
		updated, err := engine.SetEnabled(ctx, p.ID, p.Enabled)
		if err != nil {
			return nil, toSchedulerFWPError(err)
		}
		return updated, nil
	}
}
