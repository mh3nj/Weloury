package app

import (
	"context"
	"encoding/json"

	"weloury/internal/ipc"
	"weloury/internal/system"
	"weloury/pkg/fwp"
)

// registerGPUMethods wires the GPU capability onto r. GPU data comes
// from the same system.Provider everything else uses - no separate
// engine, no persistence: an inventory snapshot and a live telemetry
// sample per call, exactly like system.info/system.cpu_usage.
func registerGPUMethods(r *ipc.Router, sys system.Provider) {
	r.Handle("gpu.list", gpuListHandler(sys))
	r.Handle("gpu.usage", gpuUsageHandler(sys))
}

func gpuListHandler(sys system.Provider) ipc.HandlerFunc {
	return func(ctx context.Context, _ json.RawMessage) (any, error) {
		gpus, err := sys.ListGPUs(ctx)
		if err != nil {
			return nil, toFWPError(err)
		}
		return gpus, nil
	}
}

type gpuUsageParams struct {
	Index int `json:"index"`
}

func gpuUsageHandler(sys system.Provider) ipc.HandlerFunc {
	return func(ctx context.Context, params json.RawMessage) (any, error) {
		var p gpuUsageParams
		if len(params) > 0 {
			if err := json.Unmarshal(params, &p); err != nil {
				return nil, fwp.NewError(fwp.ErrInvalidParams, err.Error())
			}
		}
		usage, err := sys.GPUUsage(ctx, p.Index)
		if err != nil {
			return nil, toFWPError(err)
		}
		return usage, nil
	}
}
