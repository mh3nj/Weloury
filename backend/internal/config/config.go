// Package config loads and persists Weloury core's on-disk configuration.
package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// Config holds every user- and machine-specific setting for Weloury core. It
// is stored as JSON at Path() and merged over Default() so that fields
// introduced by a later version have a safe value even when loading an
// older on-disk file.
type Config struct {
	// SchemaVersion allows the config loader to detect and migrate older
	// on-disk config shapes in the future. It is independent of both the
	// FWP protocol version (pkg/fwp.ProtocolVersion) and the SQLite schema
	// version (tracked in the schema_migrations table).
	SchemaVersion int `json:"schema_version"`

	// IPCHost is the loopback address Weloury core listens on. NewServer
	// refuses to bind anything that is not a loopback address - Weloury
	// never exposes local IPC on a non-loopback interface.
	IPCHost string `json:"ipc_host"`

	// IPCPort is the TCP port to listen on. 0 (the default) means "choose
	// an ephemeral port"; the desktop shell learns the chosen port from
	// Weloury core's startup handshake, so a fixed port is never required
	// for local operation. A non-zero value is mainly useful for manual
	// development against a fixed port (see DEVELOPMENT.md).
	IPCPort int `json:"ipc_port"`

	// LogLevel is one of "debug", "info", "warn", "error".
	LogLevel string `json:"log_level"`

	// LogFormat is one of "text" (human-readable) or "json" (structured).
	LogFormat string `json:"log_format"`

	// Theme is one of "dark", "light". Stored here (rather than only in
	// the frontend) so a future remote agent or headless mode has a
	// single source of truth for user preference.
	Theme string `json:"theme"`

	// CloseToTray controls the desktop shell's window-close behavior:
	// when true (the default - an operations tool is typically left
	// running alongside the systems it watches, and closing a monitoring
	// window by accident should not tear down its background engines),
	// clicking the window's close button hides it to the system tray
	// instead of exiting; Weloury keeps running and the tray menu's Quit
	// is the real exit. When false, closing the window exits the app
	// (stopping Weloury core with it - see apps/desktop/src/main.rs).
	//
	// The desktop shell reads this file directly at startup (it is the
	// same file this package writes - see main.rs's configPath) and the
	// settings UI additionally applies changes live via a Tauri command,
	// so the behavior updates immediately, not just next launch.
	CloseToTray bool `json:"close_to_tray"`

	// RunAtStartup and RunAtStartupInTray are a mutually exclusive pair:
	// at most one may be true, enforced by internal/app's settings.update
	// handler (the frontend presents them as one three-state choice:
	// off / at startup / at startup in tray). Both register the
	// desktop shell with the OS's per-user autostart mechanism (Windows:
	// the HKCU Run key; Linux: an XDG autostart .desktop entry; macOS: a
	// LaunchAgent) - the tray variant additionally passes a --tray flag
	// so the shell starts hidden to the tray. The OS registration itself
	// is applied by the shell (apps/desktop/src/autostart.rs) when the
	// settings UI saves a change; this config field is the durable
	// record of what the user asked for and what the settings UI shows.
	RunAtStartup bool `json:"run_at_startup"`
	RunAtStartupInTray bool `json:"run_at_startup_in_tray"`

	// MetricsRetentionDays controls how long aggregated metric samples are
	// kept before being pruned. Enforced starting with the metrics engine
	// (Phase 4); recorded from Phase 1 so the config schema is stable
	// before anything depends on it.
	MetricsRetentionDays int `json:"metrics_retention_days"`

	// EventsRetentionDays controls how long historical events are kept.
	// Enforced starting in Phase 1 by internal/app's retention pruner.
	EventsRetentionDays int `json:"events_retention_days"`
}

// CurrentSchemaVersion is the config schema version this build writes.
const CurrentSchemaVersion = 1

// Default returns the built-in default configuration, used both when no
// config file exists yet and as the base a partially-populated file is
// merged over.
func Default() Config {
	return Config{
		SchemaVersion:        CurrentSchemaVersion,
		IPCHost:              "127.0.0.1",
		IPCPort:              0,
		LogLevel:             "info",
		LogFormat:            "text",
		Theme:                "dark",
		CloseToTray:          true,
		MetricsRetentionDays: 14,
		EventsRetentionDays:  30,
	}
}

// Dir returns the directory Weloury stores its config, database, and
// logs in, creating it if necessary. It is platform-appropriate via Go's
// os.UserConfigDir:
//   - Linux:   $XDG_CONFIG_HOME/weloury or ~/.config/weloury
//   - macOS:   ~/Library/Application Support/weloury
//   - Windows: %AppData%\weloury
func Dir() (string, error) {
	base, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("resolve user config dir: %w", err)
	}
	dir := filepath.Join(base, "weloury")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("create config dir %s: %w", dir, err)
	}
	return dir, nil
}

// Path returns the path to the config file inside Dir().
func Path() (string, error) {
	dir, err := Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "config.json"), nil
}

// Load reads the config file at path, merging it over Default(). If the
// file does not exist, the defaults are written to disk (so subsequent
// runs, and a curious user, see an explicit file rather than implicit
// values) and returned.
func Load(path string) (Config, error) {
	cfg := Default()

	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		if saveErr := Save(path, cfg); saveErr != nil {
			return cfg, fmt.Errorf("write default config: %w", saveErr)
		}
		return cfg, nil
	}
	if err != nil {
		return cfg, fmt.Errorf("read config %s: %w", path, err)
	}

	if err := json.Unmarshal(data, &cfg); err != nil {
		return Default(), fmt.Errorf("parse config %s: %w", path, err)
	}
	return cfg, nil
}

// Save writes cfg to path as indented JSON.
func Save(path string, cfg Config) error {
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal config: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("create config dir: %w", err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return fmt.Errorf("write config %s: %w", path, err)
	}
	return nil
}
