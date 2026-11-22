package app

import (
	"context"
	"encoding/json"

	"weloury/internal/eventbus"
	"weloury/internal/ipc"
	"weloury/internal/system"
	"weloury/pkg/fwp"
)

// registerServiceMethods wires the service.* FWP methods. Split from
// methods.go (which was starting to accumulate every domain's handlers
// in one file) now that services are their own substantial set - see
// CONTRIBUTING.md's "Where things go" on when a domain earns its own
// file/package.
func registerServiceMethods(r *ipc.Router, sys system.Provider, bus *eventbus.Bus) {
	r.Handle("service.list", serviceListHandler(sys))
	r.Handle("service.detail", serviceDetailHandler(sys))
	r.Handle("service.start", serviceStartHandler(sys, bus))
	r.Handle("service.stop", serviceStopHandler(sys, bus))
	r.Handle("service.restart", serviceRestartHandler(sys, bus))
	r.Handle("service.set_enabled", serviceSetEnabledHandler(sys, bus))
}

func serviceListHandler(sys system.Provider) ipc.HandlerFunc {
	return func(ctx context.Context, _ json.RawMessage) (any, error) {
		services, err := sys.ListServices(ctx)
		if err != nil {
			return nil, toFWPError(err)
		}
		return services, nil
	}
}

type serviceNameParams struct {
	Name string `json:"name"`
}

func decodeServiceNameParams(params json.RawMessage) (serviceNameParams, error) {
	var p serviceNameParams
	if err := json.Unmarshal(params, &p); err != nil {
		return p, fwp.NewError(fwp.ErrInvalidParams, err.Error())
	}
	if p.Name == "" {
		return p, fwp.NewError(fwp.ErrInvalidParams, "name must not be empty")
	}
	return p, nil
}

func serviceDetailHandler(sys system.Provider) ipc.HandlerFunc {
	return func(ctx context.Context, params json.RawMessage) (any, error) {
		p, err := decodeServiceNameParams(params)
		if err != nil {
			return nil, err
		}
		detail, err := sys.ServiceDetail(ctx, p.Name)
		if err != nil {
			return nil, toFWPError(err)
		}
		return detail, nil
	}
}

type serviceActionEventPayload struct {
	Name    string `json:"name"`
	Action  string `json:"action"`
	Enabled *bool  `json:"enabled,omitempty"`
	Success bool   `json:"success"`
	Error   string `json:"error,omitempty"`
}

// runServiceAction executes fn (a Start/Stop/Restart/SetEnabled call) and
// publishes a TypeServiceActionRequested event describing the outcome -
// success or failure, mirroring processTerminateHandler's philosophy that
// a state-changing action is always part of Weloury's observable history,
// not a silent side effect. See events_service.go.
func runServiceAction(bus *eventbus.Bus, name, action string, enabled *bool, fn func() error) error {
	err := fn()

	payload := serviceActionEventPayload{Name: name, Action: action, Enabled: enabled, Success: err == nil}
	if err != nil {
		payload.Error = err.Error()
	}
	if payloadJSON, marshalErr := json.Marshal(payload); marshalErr == nil {
		bus.Publish(eventbus.Event{
			Type:     TypeServiceActionRequested,
			Source:   "service." + action,
			Severity: eventbus.SeverityWarning,
			Payload:  payloadJSON,
		})
	}

	return err
}

func serviceStartHandler(sys system.Provider, bus *eventbus.Bus) ipc.HandlerFunc {
	return func(ctx context.Context, params json.RawMessage) (any, error) {
		p, err := decodeServiceNameParams(params)
		if err != nil {
			return nil, err
		}
		if actErr := runServiceAction(bus, p.Name, "start", nil, func() error {
			return sys.StartService(ctx, p.Name)
		}); actErr != nil {
			return nil, toFWPError(actErr)
		}
		return map[string]bool{"success": true}, nil
	}
}

func serviceStopHandler(sys system.Provider, bus *eventbus.Bus) ipc.HandlerFunc {
	return func(ctx context.Context, params json.RawMessage) (any, error) {
		p, err := decodeServiceNameParams(params)
		if err != nil {
			return nil, err
		}
		if actErr := runServiceAction(bus, p.Name, "stop", nil, func() error {
			return sys.StopService(ctx, p.Name)
		}); actErr != nil {
			return nil, toFWPError(actErr)
		}
		return map[string]bool{"success": true}, nil
	}
}

func serviceRestartHandler(sys system.Provider, bus *eventbus.Bus) ipc.HandlerFunc {
	return func(ctx context.Context, params json.RawMessage) (any, error) {
		p, err := decodeServiceNameParams(params)
		if err != nil {
			return nil, err
		}
		if actErr := runServiceAction(bus, p.Name, "restart", nil, func() error {
			return sys.RestartService(ctx, p.Name)
		}); actErr != nil {
			return nil, toFWPError(actErr)
		}
		return map[string]bool{"success": true}, nil
	}
}

type serviceSetEnabledParams struct {
	Name    string `json:"name"`
	Enabled bool   `json:"enabled"`
}

func serviceSetEnabledHandler(sys system.Provider, bus *eventbus.Bus) ipc.HandlerFunc {
	return func(ctx context.Context, params json.RawMessage) (any, error) {
		var p serviceSetEnabledParams
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, fwp.NewError(fwp.ErrInvalidParams, err.Error())
		}
		if p.Name == "" {
			return nil, fwp.NewError(fwp.ErrInvalidParams, "name must not be empty")
		}

		enabled := p.Enabled
		if actErr := runServiceAction(bus, p.Name, "set_enabled", &enabled, func() error {
			return sys.SetServiceEnabled(ctx, p.Name, p.Enabled)
		}); actErr != nil {
			return nil, toFWPError(actErr)
		}
		return map[string]bool{"success": true}, nil
	}
}
