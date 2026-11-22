package app

import (
	"context"
	"encoding/json"

	"weloury/internal/containers"
	"weloury/internal/ipc"
	"weloury/pkg/fwp"
)

// registerContainersMethods wires internal/containers onto r.
func registerContainersMethods(r *ipc.Router, provider *containers.Provider) {
	r.Handle("containers.availability", containersAvailabilityHandler(provider))
	r.Handle("containers.list", containersListHandler(provider))
	r.Handle("containers.logs", containersLogsHandler(provider))
	r.Handle("containers.start", containersStartHandler(provider))
	r.Handle("containers.stop", containersStopHandler(provider))
	r.Handle("containers.restart", containersRestartHandler(provider))
}

func containersAvailabilityHandler(provider *containers.Provider) ipc.HandlerFunc {
	return func(ctx context.Context, _ json.RawMessage) (any, error) {
		return provider.Availability(ctx), nil
	}
}

func containersListHandler(provider *containers.Provider) ipc.HandlerFunc {
	return func(ctx context.Context, _ json.RawMessage) (any, error) {
		list, err := provider.List(ctx)
		if err != nil {
			return nil, fwp.NewError(fwp.ErrInternal, err.Error())
		}
		if list == nil {
			list = []containers.Container{}
		}
		return list, nil
	}
}

type containersIDParams struct {
	ID string `json:"id"`
}

type containersLogsParams struct {
	ID   string `json:"id"`
	Tail int    `json:"tail,omitempty"`
}

func containersLogsHandler(provider *containers.Provider) ipc.HandlerFunc {
	return func(ctx context.Context, params json.RawMessage) (any, error) {
		var p containersLogsParams
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, fwp.NewError(fwp.ErrInvalidParams, err.Error())
		}
		if p.ID == "" {
			return nil, fwp.NewError(fwp.ErrInvalidParams, "id is required")
		}
		lines, err := provider.Logs(ctx, p.ID, p.Tail)
		if err != nil {
			return nil, fwp.NewError(fwp.ErrInternal, err.Error())
		}
		return lines, nil
	}
}

func containersStartHandler(provider *containers.Provider) ipc.HandlerFunc {
	return func(ctx context.Context, params json.RawMessage) (any, error) {
		var p containersIDParams
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, fwp.NewError(fwp.ErrInvalidParams, err.Error())
		}
		if p.ID == "" {
			return nil, fwp.NewError(fwp.ErrInvalidParams, "id is required")
		}
		if err := provider.Start(ctx, p.ID); err != nil {
			return nil, fwp.NewError(fwp.ErrInternal, err.Error())
		}
		return struct{}{}, nil
	}
}

func containersStopHandler(provider *containers.Provider) ipc.HandlerFunc {
	return func(ctx context.Context, params json.RawMessage) (any, error) {
		var p containersIDParams
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, fwp.NewError(fwp.ErrInvalidParams, err.Error())
		}
		if p.ID == "" {
			return nil, fwp.NewError(fwp.ErrInvalidParams, "id is required")
		}
		if err := provider.Stop(ctx, p.ID); err != nil {
			return nil, fwp.NewError(fwp.ErrInternal, err.Error())
		}
		return struct{}{}, nil
	}
}

func containersRestartHandler(provider *containers.Provider) ipc.HandlerFunc {
	return func(ctx context.Context, params json.RawMessage) (any, error) {
		var p containersIDParams
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, fwp.NewError(fwp.ErrInvalidParams, err.Error())
		}
		if p.ID == "" {
			return nil, fwp.NewError(fwp.ErrInvalidParams, "id is required")
		}
		if err := provider.Restart(ctx, p.ID); err != nil {
			return nil, fwp.NewError(fwp.ErrInternal, err.Error())
		}
		return struct{}{}, nil
	}
}
