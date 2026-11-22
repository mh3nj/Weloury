package app

import (
	"context"
	"encoding/json"
	"errors"

	"weloury/internal/health"
	"weloury/internal/ipc"
	"weloury/pkg/fwp"
)

// registerHealthMethods wires the health check engine (internal/health,
// spec section 9) onto r. engine is a *health.Engine, following the same
// thin-adapter pattern as registerServiceMethods: decode params, call
// into the engine, translate its errors, encode the result.
func registerHealthMethods(r *ipc.Router, engine *health.Engine) {
	r.Handle("health.list", healthListHandler(engine))
	r.Handle("health.get", healthGetHandler(engine))
	r.Handle("health.create", healthCreateHandler(engine))
	r.Handle("health.update", healthUpdateHandler(engine))
	r.Handle("health.delete", healthDeleteHandler(engine))
	r.Handle("health.run_now", healthRunNowHandler(engine))
}

// toHealthFWPError translates a health package error into a wire-level
// FWP error - the health.Engine counterpart to toFWPError (methods.go).
// A separate function rather than extending toFWPError: that one is
// specifically scoped to system.Provider's two sentinel errors, and
// mixing sentinels from two unrelated packages into one translator would
// make each harder to audit for completeness.
func toHealthFWPError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, health.ErrNotFound) {
		return fwp.NewError(fwp.ErrNotFound, err.Error())
	}
	if errors.Is(err, health.ErrInvalid) {
		return fwp.NewError(fwp.ErrInvalidParams, err.Error())
	}
	return fwp.NewError(fwp.ErrInternal, err.Error())
}

func healthListHandler(engine *health.Engine) ipc.HandlerFunc {
	return func(_ context.Context, _ json.RawMessage) (any, error) {
		return engine.List(), nil
	}
}

type healthGetParams struct {
	ID string `json:"id"`
}

func healthGetHandler(engine *health.Engine) ipc.HandlerFunc {
	return func(_ context.Context, params json.RawMessage) (any, error) {
		var p healthGetParams
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, fwp.NewError(fwp.ErrInvalidParams, err.Error())
		}
		status, ok := engine.Get(p.ID)
		if !ok {
			return nil, fwp.NewError(fwp.ErrNotFound, "no health check with that id")
		}
		return status, nil
	}
}

// healthCheckParams mirrors health.Check's JSON shape for create/update
// requests. A separate wire type (rather than decoding straight into
// health.Check) so ID/CreatedAt/UpdatedAt - which the engine assigns
// itself (Create) or preserves itself (Update), never the caller - simply
// aren't fields a client can set through this API at all.
type healthCheckParams struct {
	Name            string             `json:"name"`
	Type            health.CheckType   `json:"type"`
	Target          string             `json:"target,omitempty"`
	CommandArgs     []string           `json:"command_args,omitempty"`
	Threshold       float64            `json:"threshold,omitempty"`
	IntervalSeconds int                `json:"interval_seconds"`
	TimeoutSeconds  int                `json:"timeout_seconds"`
	Retry           health.RetryPolicy `json:"retry"`
}

func (p healthCheckParams) toCheck() health.Check {
	return health.Check{
		Name:            p.Name,
		Type:            p.Type,
		Target:          p.Target,
		CommandArgs:     p.CommandArgs,
		Threshold:       p.Threshold,
		IntervalSeconds: p.IntervalSeconds,
		TimeoutSeconds:  p.TimeoutSeconds,
		Retry:           p.Retry,
	}
}

func healthCreateHandler(engine *health.Engine) ipc.HandlerFunc {
	return func(ctx context.Context, params json.RawMessage) (any, error) {
		var p healthCheckParams
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, fwp.NewError(fwp.ErrInvalidParams, err.Error())
		}
		status, err := engine.Create(ctx, p.toCheck())
		if err != nil {
			return nil, toHealthFWPError(err)
		}
		return status, nil
	}
}

type healthUpdateParams struct {
	ID string `json:"id"`
	healthCheckParams
}

func healthUpdateHandler(engine *health.Engine) ipc.HandlerFunc {
	return func(ctx context.Context, params json.RawMessage) (any, error) {
		var p healthUpdateParams
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, fwp.NewError(fwp.ErrInvalidParams, err.Error())
		}
		if p.ID == "" {
			return nil, fwp.NewError(fwp.ErrInvalidParams, "id is required")
		}
		status, err := engine.Update(ctx, p.ID, p.healthCheckParams.toCheck())
		if err != nil {
			return nil, toHealthFWPError(err)
		}
		return status, nil
	}
}

func healthDeleteHandler(engine *health.Engine) ipc.HandlerFunc {
	return func(ctx context.Context, params json.RawMessage) (any, error) {
		var p healthGetParams
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, fwp.NewError(fwp.ErrInvalidParams, err.Error())
		}
		if err := engine.Delete(ctx, p.ID); err != nil {
			return nil, toHealthFWPError(err)
		}
		return struct{}{}, nil
	}
}

func healthRunNowHandler(engine *health.Engine) ipc.HandlerFunc {
	return func(ctx context.Context, params json.RawMessage) (any, error) {
		var p healthGetParams
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, fwp.NewError(fwp.ErrInvalidParams, err.Error())
		}
		status, err := engine.RunNow(ctx, p.ID)
		if err != nil {
			return nil, toHealthFWPError(err)
		}
		return status, nil
	}
}
