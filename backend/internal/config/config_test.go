package config

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestLoadWritesDefaultsWhenMissing(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.LogLevel != "info" {
		t.Fatalf("LogLevel = %q, want %q", cfg.LogLevel, "info")
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("expected default config file to be written: %v", err)
	}
}

func TestLoadMergesPartialFileOverDefaults(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	if err := os.WriteFile(path, []byte(`{"log_level":"debug"}`), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.LogLevel != "debug" {
		t.Fatalf("LogLevel = %q, want %q", cfg.LogLevel, "debug")
	}
	if cfg.Theme != "dark" {
		t.Fatalf("Theme = %q, want default %q", cfg.Theme, "dark")
	}
}

func TestSaveThenLoadRoundTrips(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")

	want := Default()
	want.IPCPort = 4242
	want.Theme = "light"
	if err := Save(path, want); err != nil {
		t.Fatalf("Save: %v", err)
	}

	got, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got.IPCPort != want.IPCPort || got.Theme != want.Theme {
		t.Fatalf("round trip mismatch: got %+v, want %+v", got, want)
	}
}

func TestLoadRejectsMalformedJSON(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	if err := os.WriteFile(path, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err == nil {
		t.Fatal("expected error loading malformed config, got nil")
	}
}

func TestDirCreatesDirectory(t *testing.T) {
	// os.UserConfigDir only honors $XDG_CONFIG_HOME on Unix (see its
	// documentation); on Windows/macOS it consults a fixed OS API instead,
	// so this test can only safely redirect it on Unix without touching
	// the real machine's config directory.
	if runtime.GOOS == "windows" || runtime.GOOS == "darwin" {
		t.Skipf("os.UserConfigDir does not honor XDG_CONFIG_HOME on %s", runtime.GOOS)
	}
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	dir, err := Dir()
	if err != nil {
		t.Fatalf("Dir: %v", err)
	}
	info, err := os.Stat(dir)
	if err != nil {
		t.Fatalf("expected config dir to exist: %v", err)
	}
	if !info.IsDir() {
		t.Fatalf("%s is not a directory", dir)
	}
}
