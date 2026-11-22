package app

import (
	"context"
	"encoding/json"
	"errors"

	"weloury/internal/automation"
	"weloury/internal/ipc"
	"weloury/pkg/fwp"
)

// registerAutomationMethods wires internal/automation onto r.
func registerAutomationMethods(r *ipc.Router, engine *automation.Engine) {
	r.Handle("automation.list", automationListHandler(engine))
	r.Handle("automation.get", automationGetHandler(engine))
	r.Handle("automation.create", automationCreateHandler(engine))
	r.Handle("automation.update", automationUpdateHandler(engine))
	r.Handle("automation.delete", automationDeleteHandler(engine))
}

// toAutomationFWPError translates an automation package error into a
// wire-level FWP error - mirrors toHealthFWPError in methods_health.go.
func toAutomationFWPError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, automation.ErrNotFound) {
		return fwp.NewError(fwp.ErrNotFound, err.Error())
	}
	if errors.Is(err, automation.ErrInvalid) {
		return fwp.NewError(fwp.ErrInvalidParams, err.Error())
	}
	return fwp.NewError(fwp.ErrInternal, err.Error())
}

func automationListHandler(engine *automation.Engine) ipc.HandlerFunc {
	return func(_ context.Context, _ json.RawMessage) (any, error) {
		return engine.List(), nil
	}
}

type automationIDParams struct {
	ID string `json:"id"`
}

func automationGetHandler(engine *automation.Engine) ipc.HandlerFunc {
	return func(_ context.Context, params json.RawMessage) (any, error) {
		var p automationIDParams
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, fwp.NewError(fwp.ErrInvalidParams, err.Error())
		}
		status, ok := engine.Get(p.ID)
		if !ok {
			return nil, fwp.NewError(fwp.ErrNotFound, "no automation rule with that id")
		}
		return status, nil
	}
}

// automationRuleParams mirrors automation.Rule's create/update wire
// shape - a separate type so ID/CreatedAt/UpdatedAt/LastRun* (all
// engine-assigned, never caller-set) simply aren't fields a client can
// set through this API, the same reasoning healthCheckParams and
// schedulerTaskParams use. Enabled is included (unlike those two)
// because, unlike a health check (no such concept) or a freshly-created
// scheduled task (always starts enabled - see scheduler.Engine.Create),
// an automation rule's enabled state is exactly the kind of thing an
// edit form's toggle needs to round-trip.
type automationRuleParams struct {
	Name            string                 `json:"name"`
	Enabled         bool                   `json:"enabled"`
	Trigger         automation.Trigger     `json:"trigger"`
	Conditions      []automation.Condition `json:"conditions,omitempty"`
	Actions         []automation.Action    `json:"actions"`
	CooldownSeconds int                    `json:"cooldown_seconds,omitempty"`
}

func (p automationRuleParams) toRule() automation.Rule {
	return automation.Rule{
		Name:            p.Name,
		Enabled:         p.Enabled,
		Trigger:         p.Trigger,
		Conditions:      p.Conditions,
		Actions:         p.Actions,
		CooldownSeconds: p.CooldownSeconds,
	}
}

func automationCreateHandler(engine *automation.Engine) ipc.HandlerFunc {
	return func(ctx context.Context, params json.RawMessage) (any, error) {
		var p automationRuleParams
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, fwp.NewError(fwp.ErrInvalidParams, err.Error())
		}
		status, err := engine.Create(ctx, p.toRule())
		if err != nil {
			return nil, toAutomationFWPError(err)
		}
		return status, nil
	}
}

type automationUpdateParams struct {
	ID string `json:"id"`
	automationRuleParams
}

func automationUpdateHandler(engine *automation.Engine) ipc.HandlerFunc {
	return func(ctx context.Context, params json.RawMessage) (any, error) {
		var p automationUpdateParams
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, fwp.NewError(fwp.ErrInvalidParams, err.Error())
		}
		if p.ID == "" {
			return nil, fwp.NewError(fwp.ErrInvalidParams, "id is required")
		}
		status, err := engine.Update(ctx, p.ID, p.automationRuleParams.toRule())
		if err != nil {
			return nil, toAutomationFWPError(err)
		}
		return status, nil
	}
}

func automationDeleteHandler(engine *automation.Engine) ipc.HandlerFunc {
	return func(ctx context.Context, params json.RawMessage) (any, error) {
		var p automationIDParams
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, fwp.NewError(fwp.ErrInvalidParams, err.Error())
		}
		if err := engine.Delete(ctx, p.ID); err != nil {
			return nil, toAutomationFWPError(err)
		}
		return struct{}{}, nil
	}
}
