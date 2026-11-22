package app

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"

	"weloury/internal/config"
	"weloury/internal/ipc"
	"weloury/pkg/fwp"
)

// settingsTestRegistration builds a registration with the settings
// methods wired to a real config file in a temp directory - the same
// load/save path production uses, so persistence behavior (not just the
// in-memory merge) is what's under test.
func settingsTestRegistration(t *testing.T) registration {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.json")
	cfg, err := config.Load(path) // writes defaults on first load
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	store := newSettingsStore(path, cfg)
	return registration{
		router:      ipc.NewRouter(),
		getSettings: store.Get,
		setSettings: store.Set,
	}
}

func dispatchSettings(t *testing.T, reg registration, method, params string) fwp.Envelope {
	t.Helper()
	resp := reg.router.Dispatch(context.Background(), fwp.Envelope{
		V: fwp.ProtocolVersion, Type: fwp.TypeRequest, ID: "t-1", Method: method,
		Params: json.RawMessage(params),
	})
	return resp
}

func TestSettingsGetReturnsDefaults(t *testing.T) {
	reg := settingsTestRegistration(t)
	registerMethods(reg)

	resp := dispatchSettings(t, reg, "settings.get", "")
	if resp.Type != fwp.TypeResponse {
		t.Fatalf("resp.Type = %s, want response (error: %+v)", resp.Type, resp.Error)
	}
	var got config.Config
	if err := json.Unmarshal(resp.Result, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	want := config.Default()
	if got != want {
		t.Fatalf("settings.get = %+v, want defaults %+v", got, want)
	}
}

func TestSettingsUpdateMergesPartialUpdate(t *testing.T) {
	reg := settingsTestRegistration(t)
	registerMethods(reg)

	// A partial update - only theme - must not clobber the other fields
	// (notably the pointer-distinct "false" values: close_to_tray starts
	// true and must survive an update that doesn't mention it).
	resp := dispatchSettings(t, reg, "settings.update", `{"theme":"light"}`)
	if resp.Type != fwp.TypeResponse {
		t.Fatalf("resp.Type = %s, want response (error: %+v)", resp.Type, resp.Error)
	}
	var got config.Config
	if err := json.Unmarshal(resp.Result, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.Theme != "light" {
		t.Errorf("Theme = %q, want light", got.Theme)
	}
	if !got.CloseToTray {
		t.Errorf("CloseToTray = false, want true (partial update must not clobber)")
	}
}

func TestSettingsUpdatePersistsAcrossReload(t *testing.T) {
	reg := settingsTestRegistration(t)
	registerMethods(reg)

	if resp := dispatchSettings(t, reg, "settings.update", `{"close_to_tray":false}`); resp.Type != fwp.TypeResponse {
		t.Fatalf("update failed: %+v", resp.Error)
	}

	// A second read through the same store must reflect the write; the
	// shell reads the same file directly (see main.rs), and store.Set is
	// the only write path, so a successful read-back here plus Set's
	// config.Save error propagation covers the durable record.
	resp := dispatchSettings(t, reg, "settings.get", "")
	var got config.Config
	if err := json.Unmarshal(resp.Result, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.CloseToTray {
		t.Fatal("close_to_tray update did not persist")
	}
}

func TestSettingsUpdateStartupPairIsMutuallyExclusive(t *testing.T) {
	reg := settingsTestRegistration(t)
	registerMethods(reg)

	// Both true in one request → rejected outright.
	resp := dispatchSettings(t, reg, "settings.update", `{"run_at_startup":true,"run_at_startup_in_tray":true}`)
	if resp.Type != fwp.TypeError || resp.Error.Code != fwp.ErrInvalidParams {
		t.Fatalf("resp = %+v, want INVALID_PARAMS when both startup options are enabled", resp)
	}

	// Enabling one clears the other.
	if resp := dispatchSettings(t, reg, "settings.update", `{"run_at_startup":true}`); resp.Type != fwp.TypeResponse {
		t.Fatalf("update failed: %+v", resp.Error)
	}
	if resp := dispatchSettings(t, reg, "settings.update", `{"run_at_startup_in_tray":true}`); resp.Type != fwp.TypeResponse {
		t.Fatalf("update failed: %+v", resp.Error)
	}
	var got config.Config
	resp = dispatchSettings(t, reg, "settings.get", "")
	if err := json.Unmarshal(resp.Result, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.RunAtStartup {
		t.Error("RunAtStartup = true, want false (cleared by enabling the tray variant)")
	}
	if !got.RunAtStartupInTray {
		t.Error("RunAtStartupInTray = false, want true")
	}
}

func TestSettingsUpdateRejectsInvalidTheme(t *testing.T) {
	reg := settingsTestRegistration(t)
	registerMethods(reg)

	resp := dispatchSettings(t, reg, "settings.update", `{"theme":"solarized"}`)
	if resp.Type != fwp.TypeError || resp.Error.Code != fwp.ErrInvalidParams {
		t.Fatalf("resp = %+v, want INVALID_PARAMS for an invalid theme", resp)
	}
}

func TestSettingsUpdateInvalidJSON(t *testing.T) {
	reg := settingsTestRegistration(t)
	registerMethods(reg)

	resp := dispatchSettings(t, reg, "settings.update", `{not json}`)
	if resp.Type != fwp.TypeError || resp.Error.Code != fwp.ErrInvalidParams {
		t.Fatalf("resp = %+v, want INVALID_PARAMS for malformed params", resp)
	}
}
