package app

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"

	"weloury/internal/config"
	"weloury/internal/ipc"
	"weloury/pkg/fwp"
)

// settingsStore is the live, in-memory copy of the config behind the
// settings.* FWP methods, guarded by a mutex (an FWP handler can run on
// any of the IPC server's request goroutines). Set is the only write
// path: it persists to disk through config.Save and then publishes the
// new value into memory, so a reader never sees a state the file
// doesn't reflect.
type settingsStore struct {
	mu   sync.Mutex
	path string
	cur  config.Config
}

func newSettingsStore(path string, initial config.Config) *settingsStore {
	return &settingsStore{path: path, cur: initial}
}

func (s *settingsStore) Get() config.Config {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.cur
}

func (s *settingsStore) Set(c config.Config) error {
	if err := config.Save(s.path, c); err != nil {
		return err
	}
	s.mu.Lock()
	s.cur = c
	s.mu.Unlock()
	return nil
}

// registerSettingsMethods exposes Weloury core's persisted user settings
// (internal/config.Config) over FWP. The config file is the backend's -
// the desktop shell reads the same file directly for the behaviors it
// owns (close-to-tray, autostart registration, see
// apps/desktop/src/main.rs) and the settings UI additionally applies
// those behaviors live via Tauri commands on save; this API is the
// single durable record so both sides agree and the UI survives
// restarts.
func registerSettingsMethods(r *ipc.Router, get func() config.Config, set func(config.Config) error) {
	r.Handle("settings.get", settingsGetHandler(get))
	r.Handle("settings.update", settingsUpdateHandler(get, set))
}

func settingsGetHandler(get func() config.Config) ipc.HandlerFunc {
	return func(_ context.Context, _ json.RawMessage) (any, error) {
		return get(), nil
	}
}

// settingsUpdateParams mirrors the subset of config.Config a user sets
// from the settings UI. Retention/IPC fields are deliberately absent:
// they are power-user file edits (changing the IPC host/port mid-run
// would break the shell's connection), not settings-page toggles.
type settingsUpdateParams struct {
	Theme              *string `json:"theme,omitempty"`
	CloseToTray        *bool   `json:"close_to_tray,omitempty"`
	RunAtStartup       *bool   `json:"run_at_startup,omitempty"`
	RunAtStartupInTray *bool   `json:"run_at_startup_in_tray,omitempty"`
}

// settingsUpdateHandler merges the provided fields over the current
// config and persists it. Pointer fields distinguish "not provided"
// from "provided as false" so a partial update never clobbers fields
// the caller didn't send.
//
// RunAtStartup and RunAtStartupInTray are mutually exclusive (at most
// one true) - the UI models them as one three-state choice (off /
// at startup / at startup in tray). Enabling either clears the other,
// so a client can never persist an ambiguous "both" state.
func settingsUpdateHandler(get func() config.Config, set func(config.Config) error) ipc.HandlerFunc {
	return func(_ context.Context, params json.RawMessage) (any, error) {
		var p settingsUpdateParams
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, fwp.NewError(fwp.ErrInvalidParams, err.Error())
		}

		if p.RunAtStartup != nil && *p.RunAtStartup && p.RunAtStartupInTray != nil && *p.RunAtStartupInTray {
			return nil, fwp.NewError(fwp.ErrInvalidParams,
				"run_at_startup and run_at_startup_in_tray are mutually exclusive; enable at most one")
		}

		updated, err := updateSettings(get, set, func(cfg config.Config) (config.Config, error) {
			if p.Theme != nil {
				if *p.Theme != "dark" && *p.Theme != "light" {
					return cfg, fmt.Errorf(`theme must be "dark" or "light", got %q`, *p.Theme)
				}
				cfg.Theme = *p.Theme
			}
			if p.CloseToTray != nil {
				cfg.CloseToTray = *p.CloseToTray
			}
			if p.RunAtStartup != nil {
				cfg.RunAtStartup = *p.RunAtStartup
				if *p.RunAtStartup {
					cfg.RunAtStartupInTray = false
				}
			}
			if p.RunAtStartupInTray != nil {
				cfg.RunAtStartupInTray = *p.RunAtStartupInTray
				if *p.RunAtStartupInTray {
					cfg.RunAtStartup = false
				}
			}
			return cfg, nil
		})
		if err != nil {
			return nil, err
		}
		return updated, nil
	}
}

// updateSettings loads the current config through get, applies mutate,
// persists the result through set, and returns the saved value. An
// invalid mutation fails before set is called, so a rejected update
// never writes a half-applied config.
func updateSettings(
	get func() config.Config,
	set func(config.Config) error,
	mutate func(config.Config) (config.Config, error),
) (config.Config, error) {
	cfg, err := mutate(get())
	if err != nil {
		return config.Config{}, fwp.NewError(fwp.ErrInvalidParams, err.Error())
	}
	if err := set(cfg); err != nil {
		return config.Config{}, fwp.NewError(fwp.ErrInternal, "persist settings: "+err.Error())
	}
	return cfg, nil
}
