package app

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"weloury/internal/automation"
	"weloury/internal/config"
	"weloury/internal/containers"
	"weloury/internal/diagnostics"
	"weloury/internal/eventbus"
	"weloury/internal/health"
	"weloury/internal/ipc"
	"weloury/internal/logs"
	"weloury/internal/metrics"
	"weloury/internal/scheduler"
	"weloury/internal/storage"
	"weloury/internal/system"
	"weloury/internal/version"
	"weloury/pkg/fwp"
)

// registration bundles every dependency registerMethods and its
// per-domain helpers need. See git history for the three-positional-
// parameter version this replaced once the dependency count grew past
// what read comfortably as positional parameters.
type registration struct {
	router      *ipc.Router
	sys         system.Provider
	bus         *eventbus.Bus
	events      *storage.EventRepository
	health      *health.Engine
	metrics     *metrics.Engine
	logs        *logs.Engine
	scheduler   *scheduler.Engine
	automation  *automation.Engine
	containers  *containers.Provider
	diagnostics *diagnostics.Generator

	// getSettings/setSettings expose the composition root's config load
	// and save to the settings.* FWP methods - see methods_settings.go.
	getSettings func() config.Config
	setSettings func(config.Config) error
}

// registerMethods wires every FWP method this build of Weloury core exposes
// onto reg.router. Method registration lives here, in the composition root,
// rather than inside package ipc: ipc is pure transport and must not know
// what "system.ping" means. Domain-specific handlers below (system.*,
// process.*) are thin adapters: decode params, call into internal/system,
// translate its errors (notably system.ErrUnavailable/ErrNotFound) into
// fwp.Envelope error codes, encode the result. The actual logic lives in
// internal/system - see its package doc comment.
func registerMethods(reg registration) {
	r, sys, bus := reg.router, reg.sys, reg.bus

	r.Handle("system.ping", handlePing)
	r.Handle("system.version", handleVersion)

	registerGPUMethods(r, sys)

	r.Handle("system.info", systemInfoHandler(sys))
	r.Handle("system.cpu_info", cpuInfoHandler(sys))
	r.Handle("system.cpu_usage", cpuUsageHandler(sys))
	r.Handle("system.memory", memoryHandler(sys))
	r.Handle("system.disks", disksHandler(sys))
	r.Handle("system.network_interfaces", networkInterfacesHandler(sys))
	r.Handle("system.network_connections", networkConnectionsHandler(sys))

	registerDiskHealthMethods(r, sys)
	registerStartupMethods(r, sys)

	r.Handle("process.list", processListHandler(sys))
	r.Handle("process.tree", processTreeHandler(sys))
	r.Handle("process.detail", processDetailHandler(sys))
	r.Handle("process.terminate", processTerminateHandler(sys, bus))

	registerServiceMethods(r, sys, bus)
	registerHealthMethods(r, reg.health)
	registerMetricsMethods(r, reg.metrics)
	registerLogsMethods(r, reg.logs)
	registerEventsMethods(r, reg.events)
	registerSchedulerMethods(r, reg.scheduler)
	registerAutomationMethods(r, reg.automation)
	registerContainersMethods(r, reg.containers)
	registerDiagnosticsMethods(r, reg.diagnostics)
	registerSettingsMethods(r, reg.getSettings, reg.setSettings)
}

type pingParams struct {
	Echo string `json:"echo"`
}

type pingResult struct {
	Echo string `json:"echo"`
	Pong bool   `json:"pong"`
}

func handlePing(_ context.Context, params json.RawMessage) (any, error) {
	var p pingParams
	if len(params) > 0 {
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, fwp.NewError(fwp.ErrInvalidParams, err.Error())
		}
	}
	return pingResult{Echo: p.Echo, Pong: true}, nil
}

func handleVersion(_ context.Context, _ json.RawMessage) (any, error) {
	return version.Info(), nil
}

// toFWPError translates a system.Provider error into a wire-level FWP
// error, mapping system.ErrUnavailable and system.ErrNotFound to their
// corresponding fwp error codes so the frontend can distinguish "this
// platform doesn't support this" and "that process is already gone" from
// a genuine failure - see spec section 4.
func toFWPError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, system.ErrUnavailable) {
		return fwp.NewError(fwp.ErrUnavailable, err.Error())
	}
	if errors.Is(err, system.ErrNotFound) {
		return fwp.NewError(fwp.ErrNotFound, err.Error())
	}
	return fwp.NewError(fwp.ErrInternal, err.Error())
}

func systemInfoHandler(sys system.Provider) ipc.HandlerFunc {
	return func(ctx context.Context, _ json.RawMessage) (any, error) {
		info, err := sys.SystemInfo(ctx)
		if err != nil {
			return nil, toFWPError(err)
		}
		return info, nil
	}
}

func cpuInfoHandler(sys system.Provider) ipc.HandlerFunc {
	return func(ctx context.Context, _ json.RawMessage) (any, error) {
		info, err := sys.CPUInfo(ctx)
		if err != nil {
			return nil, toFWPError(err)
		}
		return info, nil
	}
}

// cpuUsageSampleWindow bounds how long the system.cpu_usage request
// blocks (see system.CPUProvider.CPUUsage's doc comment on why a
// utilization reading inherently needs two time-separated samples). Kept
// short enough to feel instantaneous to a UI caller.
const cpuUsageSampleWindow = 300 * time.Millisecond

func cpuUsageHandler(sys system.Provider) ipc.HandlerFunc {
	return func(ctx context.Context, _ json.RawMessage) (any, error) {
		usage, err := sys.CPUUsage(ctx, cpuUsageSampleWindow)
		if err != nil {
			return nil, toFWPError(err)
		}
		return usage, nil
	}
}

func memoryHandler(sys system.Provider) ipc.HandlerFunc {
	return func(ctx context.Context, _ json.RawMessage) (any, error) {
		mem, err := sys.MemoryInfo(ctx)
		if err != nil {
			return nil, toFWPError(err)
		}
		return mem, nil
	}
}

func disksHandler(sys system.Provider) ipc.HandlerFunc {
	return func(ctx context.Context, _ json.RawMessage) (any, error) {
		disks, err := sys.Disks(ctx)
		if err != nil {
			return nil, toFWPError(err)
		}
		return disks, nil
	}
}

func networkInterfacesHandler(sys system.Provider) ipc.HandlerFunc {
	return func(ctx context.Context, _ json.RawMessage) (any, error) {
		ifaces, err := sys.NetworkInterfaces(ctx)
		if err != nil {
			return nil, toFWPError(err)
		}
		return ifaces, nil
	}
}

func networkConnectionsHandler(sys system.Provider) ipc.HandlerFunc {
	return func(ctx context.Context, _ json.RawMessage) (any, error) {
		conns, err := sys.NetworkConnections(ctx)
		if err != nil {
			return nil, toFWPError(err)
		}
		return conns, nil
	}
}

func processListHandler(sys system.Provider) ipc.HandlerFunc {
	return func(ctx context.Context, _ json.RawMessage) (any, error) {
		procs, err := sys.ListProcesses(ctx)
		if err != nil {
			return nil, toFWPError(err)
		}
		return procs, nil
	}
}

func processTreeHandler(sys system.Provider) ipc.HandlerFunc {
	return func(ctx context.Context, _ json.RawMessage) (any, error) {
		procs, err := sys.ListProcesses(ctx)
		if err != nil {
			return nil, toFWPError(err)
		}
		// BuildProcessTree is pure data transformation over what
		// ListProcesses already returned - see internal/system/tree.go.
		// No second OS query, no extra latency beyond the listing itself.
		return system.BuildProcessTree(procs), nil
	}
}

type processDetailParams struct {
	PID int32 `json:"pid"`
}

func processDetailHandler(sys system.Provider) ipc.HandlerFunc {
	return func(ctx context.Context, params json.RawMessage) (any, error) {
		var p processDetailParams
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, fwp.NewError(fwp.ErrInvalidParams, err.Error())
		}
		if p.PID <= 0 {
			return nil, fwp.NewError(fwp.ErrInvalidParams, "pid must be a positive integer")
		}

		detail, err := sys.ProcessDetail(ctx, p.PID, 0)
		if err != nil {
			return nil, toFWPError(err)
		}
		return detail, nil
	}
}

type processTerminateParams struct {
	PID   int32 `json:"pid"`
	Force bool  `json:"force"`
}

type processTerminateEventPayload struct {
	PID     int32  `json:"pid"`
	Force   bool   `json:"force"`
	Success bool   `json:"success"`
	Error   string `json:"error,omitempty"`
}

// processTerminateHandler is the one handler in this file with a side
// effect beyond reading system state: it can end a running process. It
// never prompts for confirmation itself (see
// system.ProcessProvider.TerminateProcess's doc comment - that is a
// UI-layer responsibility) but it does publish a
// TypeProcessTerminationRequested event on every attempt, success or
// failure, so a termination is always part of Weloury's observable history
// - see events_process.go.
func processTerminateHandler(sys system.Provider, bus *eventbus.Bus) ipc.HandlerFunc {
	return func(ctx context.Context, params json.RawMessage) (any, error) {
		var p processTerminateParams
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, fwp.NewError(fwp.ErrInvalidParams, err.Error())
		}
		if p.PID <= 0 {
			return nil, fwp.NewError(fwp.ErrInvalidParams, "pid must be a positive integer")
		}

		termErr := sys.TerminateProcess(ctx, p.PID, p.Force)

		eventPayload := processTerminateEventPayload{PID: p.PID, Force: p.Force, Success: termErr == nil}
		if termErr != nil {
			eventPayload.Error = termErr.Error()
		}
		payloadJSON, marshalErr := json.Marshal(eventPayload)
		if marshalErr == nil {
			bus.Publish(eventbus.Event{
				Type:     TypeProcessTerminationRequested,
				Source:   "process.terminate",
				Severity: eventbus.SeverityWarning,
				Payload:  payloadJSON,
			})
		}

		if termErr != nil {
			return nil, toFWPError(termErr)
		}
		return map[string]bool{"success": true}, nil
	}
}
