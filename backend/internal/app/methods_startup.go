package app

import (
	"context"
	"encoding/json"
	"strings"

	"weloury/internal/ipc"
	"weloury/internal/system"
	"weloury/pkg/fwp"
)

// registerDiskHealthMethods wires the physical-drive health capability.
// Data comes from the same system.Provider everything else uses; the
// wire surface mirrors system.disks (snapshot list, no params).
func registerDiskHealthMethods(r *ipc.Router, sys system.Provider) {
	r.Handle("disk.health", diskHealthHandler(sys))
}

func diskHealthHandler(sys system.Provider) ipc.HandlerFunc {
	return func(ctx context.Context, _ json.RawMessage) (any, error) {
		healths, err := sys.DiskHealth(ctx)
		if err != nil {
			return nil, toFWPError(err)
		}
		if healths == nil {
			healths = []system.DiskHealth{}
		}
		return healths, nil
	}
}

// registerStartupMethods wires the startup-manager capability. Mutating
// methods take an entry ID and validate non-emptiness the same way
// containers.*' id params do; the backend translates OS-level failures
// (registry denial, missing file) into errors - never silent skips.
func registerStartupMethods(r *ipc.Router, sys system.Provider) {
	r.Handle("startup.list", startupListHandler(sys))
	r.Handle("startup.set_enabled", startupSetEnabledHandler(sys))
	r.Handle("startup.add", startupAddHandler(sys))
	r.Handle("startup.remove", startupRemoveHandler(sys))
}

func startupListHandler(sys system.Provider) ipc.HandlerFunc {
	return func(ctx context.Context, _ json.RawMessage) (any, error) {
		entries, err := sys.ListStartupEntries(ctx)
		if err != nil {
			return nil, toFWPError(err)
		}
		if entries == nil {
			entries = []system.StartupEntry{}
		}
		return entries, nil
	}
}

type startupIDParams struct {
	ID      string `json:"id"`
	Enabled bool   `json:"enabled"`
}

func startupSetEnabledHandler(sys system.Provider) ipc.HandlerFunc {
	return func(ctx context.Context, params json.RawMessage) (any, error) {
		var p startupIDParams
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, fwp.NewError(fwp.ErrInvalidParams, err.Error())
		}
		if strings.TrimSpace(p.ID) == "" {
			return nil, fwp.NewError(fwp.ErrInvalidParams, "id is required")
		}
		if err := sys.SetStartupEntryEnabled(ctx, p.ID, p.Enabled); err != nil {
			return nil, toFWPError(err)
		}
		return struct{}{}, nil
	}
}

type startupAddParams struct {
	Name string   `json:"name"`
	Exe  string   `json:"exe"`
	Args []string `json:"args,omitempty"`
}

func startupAddHandler(sys system.Provider) ipc.HandlerFunc {
	return func(ctx context.Context, params json.RawMessage) (any, error) {
		var p startupAddParams
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, fwp.NewError(fwp.ErrInvalidParams, err.Error())
		}
		if strings.TrimSpace(p.Name) == "" {
			return nil, fwp.NewError(fwp.ErrInvalidParams, "name is required")
		}
		if strings.TrimSpace(p.Exe) == "" {
			return nil, fwp.NewError(fwp.ErrInvalidParams, "exe is required")
		}
		if err := sys.AddStartupEntry(ctx, p.Name, p.Exe, p.Args); err != nil {
			return nil, toFWPError(err)
		}
		return struct{}{}, nil
	}
}

func startupRemoveHandler(sys system.Provider) ipc.HandlerFunc {
	return func(ctx context.Context, params json.RawMessage) (any, error) {
		var p startupIDParams
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, fwp.NewError(fwp.ErrInvalidParams, err.Error())
		}
		if strings.TrimSpace(p.ID) == "" {
			return nil, fwp.NewError(fwp.ErrInvalidParams, "id is required")
		}
		if err := sys.RemoveStartupEntry(ctx, p.ID); err != nil {
			return nil, toFWPError(err)
		}
		return struct{}{}, nil
	}
}
