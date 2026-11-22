package app

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
	"time"

	"weloury/pkg/fwp"
)

// TestAppEndToEnd is Weloury core's one full-stack test: it builds a real
// App via New() (real config loading, real SQLite via the sandbox's
// CGO-backed stub driver - see DEVELOPMENT.md - real migrations, every
// real engine) against an isolated temp directory, runs it, and talks to
// it over actual HTTP/FWP exactly as the Tauri shell would. Every other
// test in this codebase verifies one package in isolation with fakes;
// this one verifies the wiring between all of them actually holds
// together - notably the bus-before-logger construction order New()
// uses so internal/logs can Tap the logger (see New()'s doc comment),
// the health-engine-Start-before-server-Serve ordering Run() uses (see
// health.Engine.Start's doc comment), and - as of Phase 5 - the
// automation-engine-subscribes-before-anything-that-could-publish
// ordering Run() uses (see Run's doc comment), exercised for real by the
// "scheduled automation rule fires end to end" subtest below: a
// scheduler task due almost immediately, wired to an automation rule via
// TriggerSchedule, whose action publishes an event this test observes
// arrive through the real bus. None of this is exercisable by a
// single-package test with fakes.
func TestAppEndToEnd(t *testing.T) {
	// os.UserConfigDir() (internal/config.Dir) honors $XDG_CONFIG_HOME on
	// Linux and %APPDATA% on Windows, giving every test run its own
	// isolated config/database directory with no source changes needed to
	// make App testable. Both are set so the test is hermetic on every
	// platform - setting only XDG_CONFIG_HOME (the historical value)
	// leaves Windows runs pointing at the developer's real AppData
	// directory, where the run both pollutes real state and can fail on
	// whatever a previous binary wrote there.
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("APPDATA", t.TempDir())

	a, err := New()
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	runErrCh := make(chan error, 1)
	go func() { runErrCh <- a.Run(ctx) }()

	client := &rpcClient{
		base:  fmt.Sprintf("http://%s", a.Server().Addr().String()),
		token: a.Server().Token(),
		// Windows providers spawn several PowerShell processes per call
		// (cold PowerShell startup alone can exceed a second), so the
		// client timeout must sit well above any single handler's worst
		// case - 5s was enough for the Linux-only providers this test
		// originally exercised, but diagnostics.run on Windows does ~8
		// subprocess round trips and cold-started over 5s in practice.
		hc: &http.Client{Timeout: 30 * time.Second},
	}

	t.Run("ping", func(t *testing.T) {
		var result struct {
			Pong bool `json:"pong"`
		}
		if err := client.call(t, "system.ping", nil, &result); err != nil {
			t.Fatalf("system.ping: %v", err)
		}
		if !result.Pong {
			t.Error("expected pong=true")
		}
	})

	var checkID string
	t.Run("health check lifecycle", func(t *testing.T) {
		createParams := map[string]any{
			"name":             "loopback reachable",
			"type":             "tcp_port",
			"target":           "127.0.0.1:1", // almost certainly closed; we only care the round trip works
			"interval_seconds": 3600,          // long enough that only RunNow matters, not the ticker
			"timeout_seconds":  2,
		}
		var created struct {
			ID    string `json:"id"`
			State string `json:"state"`
		}
		if err := client.call(t, "health.create", createParams, &created); err != nil {
			t.Fatalf("health.create: %v", err)
		}
		if created.ID == "" {
			t.Fatal("expected a non-empty check ID")
		}
		checkID = created.ID

		var list []struct {
			ID string `json:"id"`
		}
		if err := client.call(t, "health.list", nil, &list); err != nil {
			t.Fatalf("health.list: %v", err)
		}
		found := false
		for _, item := range list {
			if item.ID == checkID {
				found = true
			}
		}
		if !found {
			t.Errorf("created check %s not present in health.list result", checkID)
		}

		var ranNow struct {
			State string `json:"state"`
		}
		if err := client.call(t, "health.run_now", map[string]any{"id": checkID}, &ranNow); err != nil {
			t.Fatalf("health.run_now: %v", err)
		}
		if ranNow.State != "UNHEALTHY" {
			t.Errorf("state after RunNow against a closed port = %q, want UNHEALTHY", ranNow.State)
		}

		if err := client.call(t, "health.delete", map[string]any{"id": checkID}, nil); err != nil {
			t.Fatalf("health.delete: %v", err)
		}
	})

	t.Run("metrics current", func(t *testing.T) {
		// The metrics engine samples immediately on start (see
		// metrics.Engine.runLoop) so this should already have data by
		// the time earlier subtests have run, but poll briefly for
		// determinism against slow CI machines rather than assuming it.
		deadline := time.Now().Add(3 * time.Second)
		var current []map[string]any
		for time.Now().Before(deadline) {
			if err := client.call(t, "metrics.current", nil, &current); err != nil {
				t.Fatalf("metrics.current: %v", err)
			}
			if len(current) > 0 {
				break
			}
			time.Sleep(50 * time.Millisecond)
		}
		if len(current) == 0 {
			t.Fatal("expected at least one current metric series (cpu/memory at minimum)")
		}
	})

	t.Run("logs query sees its own startup log line", func(t *testing.T) {
		// app.Run logs "weloury-core started" via a.logger, which - thanks
		// to logging.Options.Tap - internal/logs.Engine.Capture should
		// have already recorded by the time Run's goroutine above has
		// had a chance to run.
		var entries []struct {
			Message string `json:"message"`
		}
		deadline := time.Now().Add(2 * time.Second)
		found := false
		for time.Now().Before(deadline) && !found {
			if err := client.call(t, "logs.query", map[string]any{"search": "weloury-core started"}, &entries); err != nil {
				t.Fatalf("logs.query: %v", err)
			}
			if len(entries) > 0 {
				found = true
				break
			}
			time.Sleep(50 * time.Millisecond)
		}
		if !found {
			t.Error("expected logs.query to find the startup log line captured via logging.Options.Tap")
		}
	})

	t.Run("events recent sees backend_started", func(t *testing.T) {
		var events []struct {
			Type string `json:"type"`
		}
		deadline := time.Now().Add(2 * time.Second)
		found := false
		for time.Now().Before(deadline) && !found {
			if err := client.call(t, "events.recent", nil, &events); err != nil {
				t.Fatalf("events.recent: %v", err)
			}
			for _, e := range events {
				if e.Type == "system.backend_started" {
					found = true
				}
			}
			if found {
				break
			}
			time.Sleep(50 * time.Millisecond)
		}
		if !found {
			t.Error("expected events.recent to include the system.backend_started event persisted via internal/app's event-persistence bridge")
		}
	})

	t.Run("scheduled automation rule fires end to end", func(t *testing.T) {
		// This is the payoff test for Run's subsystem start ordering
		// (see Run's doc comment): a scheduler task due almost
		// immediately, feeding an automation rule via TriggerSchedule,
		// whose action publishes a custom event - end to end, over the
		// real wire, through the real bus, exercising exactly the
		// cross-engine subscription-ordering guarantee no
		// single-package test can.
		var task struct {
			ID string `json:"id"`
		}
		runAt := time.Now().Add(500 * time.Millisecond).Format(time.RFC3339)
		if err := client.call(t, "scheduler.create", map[string]any{
			"name": "fire automation soon", "type": "once", "run_at": runAt,
		}, &task); err != nil {
			t.Fatalf("scheduler.create: %v", err)
		}

		var rule struct {
			ID string `json:"id"`
		}
		if err := client.call(t, "automation.create", map[string]any{
			"name": "on schedule, notify",
			"trigger": map[string]any{
				"type": "schedule", "schedule_id": task.ID,
			},
			"actions": []map[string]any{
				{"type": "notify", "message": "scheduled automation fired"},
			},
		}, &rule); err != nil {
			t.Fatalf("automation.create: %v", err)
		}

		deadline := time.Now().Add(5 * time.Second)
		var status struct {
			LastRunStatus string `json:"last_run_status"`
		}
		for time.Now().Before(deadline) {
			if err := client.call(t, "automation.get", map[string]any{"id": rule.ID}, &status); err != nil {
				t.Fatalf("automation.get: %v", err)
			}
			if status.LastRunStatus == "succeeded" {
				break
			}
			time.Sleep(100 * time.Millisecond)
		}
		if status.LastRunStatus != "succeeded" {
			t.Fatalf("automation rule's last_run_status = %q after waiting, want %q", status.LastRunStatus, "succeeded")
		}

		var events []struct {
			Type string `json:"type"`
		}
		found := false
		deadline = time.Now().Add(2 * time.Second)
		for time.Now().Before(deadline) && !found {
			if err := client.call(t, "events.recent", nil, &events); err != nil {
				t.Fatalf("events.recent: %v", err)
			}
			for _, e := range events {
				if e.Type == "automation.notification" {
					found = true
				}
			}
			time.Sleep(50 * time.Millisecond)
		}
		if !found {
			t.Error("expected the notify action's automation.notification event to reach events.recent")
		}
	})

	t.Run("diagnostics run returns a complete report", func(t *testing.T) {
		var report struct {
			GeneratedAt string `json:"generated_at"`
			System      struct {
				Info *struct {
					Hostname string `json:"hostname"`
				} `json:"info"`
			} `json:"system"`
			Health struct {
				Failing []any `json:"failing"`
			} `json:"health"`
		}
		if err := client.call(t, "diagnostics.run", nil, &report); err != nil {
			t.Fatalf("diagnostics.run: %v", err)
		}
		if report.GeneratedAt == "" {
			t.Error("expected a non-empty generated_at")
		}
		if report.System.Info == nil || report.System.Info.Hostname == "" {
			t.Error("expected system.info.hostname to be populated on a real machine")
		}
	})

	t.Run("containers availability reflects this sandbox honestly", func(t *testing.T) {
		// This test intentionally does not assume Docker is or isn't
		// running - it only asserts the response shape is sane either
		// way, since whether Docker happens to be available in
		// whatever environment runs this suite is not something this
		// test should require or forbid.
		var avail struct {
			Available bool   `json:"available"`
			Reason    string `json:"reason,omitempty"`
		}
		if err := client.call(t, "containers.availability", nil, &avail); err != nil {
			t.Fatalf("containers.availability: %v", err)
		}
		if !avail.Available && avail.Reason == "" {
			t.Error("expected a non-empty reason when containers are unavailable")
		}

		// containers.list itself surfaces "Docker unavailable" as an
		// FWP error rather than silently returning an empty list - see
		// methods_containers.go's doc comment. The frontend is what's
		// responsible for checking containers.availability first and
		// showing a friendly empty state instead of calling list at
		// all; this subtest just confirms both codepaths behave
		// sensibly, whichever this sandbox happens to be in.
		var list []struct {
			ID string `json:"id"`
		}
		err := client.call(t, "containers.list", nil, &list)
		if avail.Available && err != nil {
			t.Errorf("containers.list failed despite containers.availability reporting available: %v", err)
		}
		if !avail.Available && err == nil {
			t.Error("expected containers.list to surface an error when Docker itself is unavailable")
		}
	})

	// Shut down cleanly and verify Run returns with no error, mirroring
	// what cmd/weloury-core does on SIGTERM.
	cancel()
	select {
	case err := <-runErrCh:
		if err != nil {
			t.Errorf("Run returned error after clean shutdown: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return within 5s of context cancellation - possible goroutine leak in a subsystem's Wait()")
	}

	if err := a.Close(); err != nil {
		t.Errorf("Close: %v", err)
	}
}

// rpcClient is a minimal FWP-over-HTTP client, just enough to drive the
// integration test above the way the real Tauri shell would.
type rpcClient struct {
	base  string
	token string
	hc    *http.Client
	seq   int
}

func (c *rpcClient) call(t *testing.T, method string, params any, out any) error {
	t.Helper()
	c.seq++

	var raw json.RawMessage
	if params != nil {
		data, err := json.Marshal(params)
		if err != nil {
			return err
		}
		raw = data
	}

	env := fwp.Envelope{
		V:      fwp.ProtocolVersion,
		Type:   fwp.TypeRequest,
		ID:     fmt.Sprintf("test-%d", c.seq),
		Method: method,
		Params: raw,
	}
	body, err := json.Marshal(env)
	if err != nil {
		return err
	}

	req, err := http.NewRequest(http.MethodPost, c.base+"/rpc", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.token)

	resp, err := c.hc.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	var respEnv fwp.Envelope
	if err := json.NewDecoder(resp.Body).Decode(&respEnv); err != nil {
		return err
	}
	if respEnv.Type == fwp.TypeError || respEnv.Error != nil {
		return fmt.Errorf("rpc error: %+v", respEnv.Error)
	}
	if out == nil || len(respEnv.Result) == 0 {
		return nil
	}
	return json.Unmarshal(respEnv.Result, out)
}
