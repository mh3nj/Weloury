package containers

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// fakeRunner implements commandRunner entirely in memory - mirrors
// internal/automation's fakeLauncher in spirit, letting Provider's
// merging/error-handling logic be tested without a real `docker`
// binary. Responses are keyed by the joined command line
// ("docker ps -a --format ...") for simplicity; see newFakeRunner's
// callers for exact keys used.
type fakeRunner struct {
	responses map[string][]byte
	errors    map[string]error
	calls     []string
}

func newFakeRunner() *fakeRunner {
	return &fakeRunner{responses: make(map[string][]byte), errors: make(map[string]error)}
}

func (f *fakeRunner) key(name string, args ...string) string {
	return name + " " + strings.Join(args, " ")
}

func (f *fakeRunner) Run(_ context.Context, name string, args ...string) ([]byte, error) {
	k := f.key(name, args...)
	f.calls = append(f.calls, k)
	if err, ok := f.errors[k]; ok {
		return nil, err
	}
	return f.responses[k], nil
}

func TestProviderAvailabilityWhenReachable(t *testing.T) {
	runner := newFakeRunner()
	runner.responses[runner.key("docker", "info", "--format", "{{json .}}")] = []byte(`{"Containers":0}`)
	p := &Provider{runner: runner}

	got := p.Availability(context.Background())
	if !got.Available {
		t.Errorf("Available = false, want true; Reason = %q", got.Reason)
	}
}

func TestProviderAvailabilityWhenUnreachable(t *testing.T) {
	runner := newFakeRunner()
	runner.errors[runner.key("docker", "info", "--format", "{{json .}}")] = errors.New("Cannot connect to the Docker daemon")
	p := &Provider{runner: runner}

	got := p.Availability(context.Background())
	if got.Available {
		t.Error("Available = true, want false")
	}
	if got.Reason == "" {
		t.Error("expected a non-empty Reason when unavailable")
	}
}

func TestProviderListMergesStatsWithPS(t *testing.T) {
	runner := newFakeRunner()
	runner.responses[runner.key("docker", "ps", "-a", "--format", "{{json .}}")] =
		[]byte(fixturePSRunningWithPort + "\n" + fixturePSExitedNoPort + "\n")
	runner.responses[runner.key("docker", "stats", "--no-stream", "--format", "{{json .}}")] =
		[]byte(fixtureStatsRunning)
	p := &Provider{runner: runner}

	containers, err := p.List(context.Background())
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(containers) != 2 {
		t.Fatalf("len(containers) = %d, want 2", len(containers))
	}

	byID := map[string]Container{}
	for _, c := range containers {
		byID[c.ID] = c
	}

	// fixturePSRunningWithPort's ID has no matching stats row in this
	// fixture - confirms List doesn't fabricate resource data for it.
	if c := byID["69984edd6de7"]; c.CPUPercent != nil {
		t.Errorf("expected nil CPUPercent for a container with no stats row, got %v", c.CPUPercent)
	}
	// fixturePSExitedNoPort's ID does have a matching stats row.
	if c := byID["0e0e974a281f"]; c.CPUPercent == nil {
		t.Error("expected non-nil CPUPercent for a container with a matching stats row")
	}
}

func TestProviderListSurvivesStatsFailure(t *testing.T) {
	runner := newFakeRunner()
	runner.responses[runner.key("docker", "ps", "-a", "--format", "{{json .}}")] = []byte(fixturePSRunningWithPort)
	runner.errors[runner.key("docker", "stats", "--no-stream", "--format", "{{json .}}")] = errors.New("stats failed")
	p := &Provider{runner: runner}

	containers, err := p.List(context.Background())
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(containers) != 1 {
		t.Fatalf("len(containers) = %d, want 1 (list should still succeed without stats)", len(containers))
	}
}

func TestProviderListPropagatesPSFailure(t *testing.T) {
	runner := newFakeRunner()
	runner.errors[runner.key("docker", "ps", "-a", "--format", "{{json .}}")] = errors.New("daemon unreachable")
	p := &Provider{runner: runner}

	if _, err := p.List(context.Background()); err == nil {
		t.Error("expected List to fail when `docker ps` itself fails")
	}
}

func TestProviderLogsSplitsLines(t *testing.T) {
	runner := newFakeRunner()
	runner.responses[runner.key("docker", "logs", "--tail", "200", "--", "abc123")] = []byte("line one\nline two\nline three\n")
	p := &Provider{runner: runner}

	lines, err := p.Logs(context.Background(), "abc123", 0)
	if err != nil {
		t.Fatalf("Logs: %v", err)
	}
	want := []string{"line one", "line two", "line three"}
	if len(lines) != len(want) {
		t.Fatalf("lines = %v, want %v", lines, want)
	}
	for i := range want {
		if lines[i] != want[i] {
			t.Errorf("lines[%d] = %q, want %q", i, lines[i], want[i])
		}
	}
}

func TestProviderLogsEmptyOutput(t *testing.T) {
	runner := newFakeRunner()
	runner.responses[runner.key("docker", "logs", "--tail", "200", "--", "abc123")] = []byte("")
	p := &Provider{runner: runner}

	lines, err := p.Logs(context.Background(), "abc123", 0)
	if err != nil {
		t.Fatalf("Logs: %v", err)
	}
	if len(lines) != 0 {
		t.Errorf("lines = %v, want empty slice, not nil or a single blank entry", lines)
	}
}

func TestProviderLogsCustomTail(t *testing.T) {
	runner := newFakeRunner()
	runner.responses[runner.key("docker", "logs", "--tail", "50", "--", "abc123")] = []byte("x\n")
	p := &Provider{runner: runner}

	if _, err := p.Logs(context.Background(), "abc123", 50); err != nil {
		t.Fatalf("Logs: %v", err)
	}
}

func TestProviderStartStopRestart(t *testing.T) {
	runner := newFakeRunner()
	p := &Provider{runner: runner}

	if err := p.Start(context.Background(), "abc123"); err != nil {
		t.Errorf("Start: %v", err)
	}
	if err := p.Stop(context.Background(), "abc123"); err != nil {
		t.Errorf("Stop: %v", err)
	}
	if err := p.Restart(context.Background(), "abc123"); err != nil {
		t.Errorf("Restart: %v", err)
	}

	// Also verifies the argument-injection fix from Phase 8's security
	// review: "--" appears before "abc123" in every call, so an id
	// beginning with "-" can never be interpreted as a flag by docker's
	// own CLI argument parser instead of a positional container
	// reference - see the "--" comment on Provider.Logs for the full
	// reasoning.
	want := []string{"docker start -- abc123", "docker stop -- abc123", "docker restart -- abc123"}
	if len(runner.calls) != len(want) {
		t.Fatalf("calls = %v, want %v", runner.calls, want)
	}
	for i := range want {
		if runner.calls[i] != want[i] {
			t.Errorf("calls[%d] = %q, want %q", i, runner.calls[i], want[i])
		}
	}
}

func TestProviderStartFailurePropagates(t *testing.T) {
	runner := newFakeRunner()
	runner.errors[runner.key("docker", "start", "--", "abc123")] = errors.New("no such container")
	p := &Provider{runner: runner}

	if err := p.Start(context.Background(), "abc123"); err == nil {
		t.Error("expected an error to propagate")
	}
}

// TestProviderIDsAlwaysFollowADoubleDash is Phase 8 hardening: proves
// Weloury's own side of the argument-injection fix - that "--" always
// appears immediately before an id in the constructed argv, for every
// operation that takes one, including an id an attacker might choose
// specifically to look like a flag. This only verifies what Weloury
// constructs; whether the real `docker` CLI actually honors "--" as an
// end-of-flags marker the way this fix assumes was verified separately,
// empirically, against a real `docker` binary and daemon (not assumed
// from general POSIX/Cobra convention) - see CHANGELOG.md's Phase 8
// entry for the actual commands run and their output.
func TestProviderIDsAlwaysFollowADoubleDash(t *testing.T) {
	const trickyID = "--rm" // an id crafted to look like a docker flag
	runner := newFakeRunner()
	p := &Provider{runner: runner}

	_, _ = p.Logs(context.Background(), trickyID, 0)
	_ = p.Start(context.Background(), trickyID)
	_ = p.Stop(context.Background(), trickyID)
	_ = p.Restart(context.Background(), trickyID)

	want := []string{
		"docker logs --tail 200 -- --rm",
		"docker start -- --rm",
		"docker stop -- --rm",
		"docker restart -- --rm",
	}
	if len(runner.calls) != len(want) {
		t.Fatalf("calls = %v, want %v", runner.calls, want)
	}
	for i := range want {
		if runner.calls[i] != want[i] {
			t.Errorf("calls[%d] = %q, want %q", i, runner.calls[i], want[i])
		}
	}
}
