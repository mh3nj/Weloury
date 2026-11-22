package containers

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"weloury/internal/hidewin"
)

// commandRunner abstracts running the `docker` CLI - purely for
// testability (see fakeRunner in provider_test.go), the same pattern
// internal/automation's processLauncher interface uses for the same
// reason. execRunner (below) is the real implementation.
type commandRunner interface {
	Run(ctx context.Context, name string, args ...string) ([]byte, error)
}

// execRunner runs a command directly via exec.CommandContext - argv
// only, never a shell - the same discipline every other command
// execution in Weloury uses (internal/health's `command` check type,
// internal/automation's `run_command` action). See the package doc
// comment for why this shells out to `docker` at all rather than
// depending on a Docker SDK.
type execRunner struct{}

func (execRunner) Run(ctx context.Context, name string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	hidewin.Configure(cmd) // no console flash per docker call - see hidewin's doc comment
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return nil, fmt.Errorf("%s", msg)
	}
	return stdout.Bytes(), nil
}

// Provider is Weloury's container discovery/control surface. The zero
// value is not usable; construct with New.
//
// "Container runtime" currently means Docker specifically - spec section
// 15 says "where Docker or another supported container runtime is
// available," and Docker is the one implemented so far. Podman ships a
// Docker-CLI-compatible `docker` shim on many systems (in which case
// this already works unmodified), but genuine multi-runtime support
// (detecting and preferring a native `podman` binary, for instance)
// is future work - see CHANGELOG.md's "Known limitations."
type Provider struct {
	runner     commandRunner
	dockerPath string
}

// New constructs a Provider backed by the real `docker` CLI.
func New() *Provider {
	return &Provider{runner: execRunner{}, dockerPath: resolveDockerBin()}
}

// dockerBin returns the resolved docker executable, or "docker" to let
// exec's own PATH lookup make the final attempt.
func (p *Provider) dockerBin() string {
	if p.dockerPath != "" {
		return p.dockerPath
	}
	return "docker"
}

// Availability reports whether Docker can be used right now (spec
// section 15: "show runtime availability"). A single `docker info` call
// covers both "is the binary installed" and "is the daemon reachable" -
// exec.CommandContext fails the same way (a non-zero exit / a "file not
// found" error) whether the binary is missing or present-but-can't-reach
// a daemon, so Provider reports one combined Availability rather than
// trying to distinguish the two reasons, which would need a second call
// (and a second thing to keep in sync with commandRunner's fakeability)
// for a distinction of limited practical value: either way, the answer a
// user needs is "containers aren't usable right now."
// availabilityTimeout bounds the `docker info` probe. Observed live: a
// wedged Docker Desktop (engine pipe half-open) answers in 45+ seconds,
// which would hang the RPC - and an availability probe that takes that
// long IS an unavailable runtime as far as a UI is concerned. Anything
// slower than this reports unavailable with the timeout as the reason.
const availabilityTimeout = 8 * time.Second

func (p *Provider) Availability(ctx context.Context) Availability {
	ctx, cancel := context.WithTimeout(ctx, availabilityTimeout)
	defer cancel()
	if _, err := p.runner.Run(ctx, p.dockerBin(), "info", "--format", "{{json .}}"); err != nil {
		return Availability{Available: false, Reason: "Docker isn't available: " + err.Error()}
	}
	return Availability{Available: true}
}

// List returns every container, running or stopped (spec section 15:
// "containers... container status"), via `docker ps -a`. CPU/memory/PID
// fields are populated only for currently-running containers: `docker
// stats --no-stream` (their source) simply omits a stopped container
// from its output - there is no "stopped, using 0% CPU" row to parse,
// only an absence - so List treats a container missing from the stats
// output as "no resource data available," not as a parse failure.
func (p *Provider) List(ctx context.Context) ([]Container, error) {
	// Same wedged-daemon logic as Availability: a ps call that outlives
	// listTimeout isn't going to produce a usable listing.
	ctx, cancel := context.WithTimeout(ctx, listTimeout)
	defer cancel()
	psOut, err := p.runner.Run(ctx, p.dockerBin(), "ps", "-a", "--format", "{{json .}}")
	if err != nil {
		return nil, fmt.Errorf("list containers: %w", err)
	}
	rows, err := parsePSOutput(psOut)
	if err != nil {
		return nil, fmt.Errorf("parse container list: %w", err)
	}

	// Best-effort: if `docker stats` itself fails for some reason,
	// still return the container list from `docker ps` rather than
	// failing the whole call over resource-usage data being the one
	// thing unavailable.
	var statsByID map[string]statsRow
	if statsOut, err := p.runner.Run(ctx, p.dockerBin(), "stats", "--no-stream", "--format", "{{json .}}"); err == nil {
		statsByID, _ = parseStatsOutput(statsOut)
	}

	out := make([]Container, 0, len(rows))
	for _, row := range rows {
		var stats *statsRow
		if s, ok := statsByID[row.ID]; ok {
			stats = &s
		}
		out = append(out, mergeContainerRow(row, stats))
	}
	return out, nil
}

// Logs returns the last tailLines lines of a container's combined stdout/
// stderr (spec section 15: "basic logs") via `docker logs --tail N`. Not
// a live stream - a bounded, on-demand snapshot, consistent with "basic"
// in the spec's own phrasing; internal/logs' live-streaming machinery
// (Phase 4) is scoped to Weloury core's own log, not arbitrary containers'
// - see that package's doc comment on why extending live streaming to
// every possible log source is explicitly out of scope for now.
func (p *Provider) Logs(ctx context.Context, id string, tailLines int) ([]string, error) {
	if tailLines <= 0 {
		tailLines = defaultLogTailLines
	}
	// "--" ends flag parsing for the docker CLI's own argument parser
	// before id - an untrusted value - is reached. Structural, not
	// escaping: it changes how the argument is interpreted (always
	// positional, never a flag) rather than trying to sanitize the
	// value itself, the same "eliminate the ambiguity rather than
	// filter around it" approach this file's package doc comment
	// already establishes for shell injection specifically (argv, not
	// a shell string) - this is the same idea one layer up, for the
	// docker CLI's *own* flag parsing rather than a shell's. Added in
	// Phase 8's security review after noticing id reaches here with no
	// format validation at all. Verified empirically against a real
	// docker binary and daemon, not assumed from POSIX/Cobra
	// convention: `docker start --rm` (no "--") fails with `unknown
	// flag: --rm`, proving the CLI really does attempt to parse a
	// leading-dash id as a flag; `docker start -- --rm` (with "--")
	// instead fails with `No such container: --rm`, proving "--"
	// correctly forces positional interpretation. See CHANGELOG.md's
	// Phase 8 entry for the full set of commands run and their output,
	// including the case where the crafted id matches a genuinely
	// valid flag (`-f` for `docker logs`) rather than an invalid one.
	out, err := p.runner.Run(ctx, p.dockerBin(), "logs", "--tail", fmt.Sprintf("%d", tailLines), "--", id)
	if err != nil {
		return nil, fmt.Errorf("fetch logs for container %s: %w", id, err)
	}
	text := strings.TrimRight(string(out), "\n")
	if text == "" {
		return []string{}, nil
	}
	return strings.Split(text, "\n"), nil
}

const defaultLogTailLines = 200

// listTimeout bounds the ps+stats pair inside List (see Availability's
// comment on the wedged-daemon case).
const listTimeout = 20 * time.Second

// Start, Stop, and Restart wrap `docker start/stop/restart -- <id>` -
// direct, unconfirmed action, the same "the confirmation point is the
// user's own click in the UI, not a second server-side gate" model
// process.terminate and service.start/stop/restart already use (see
// SECURITY.md). The "--" before id is explained on Logs above - same
// reasoning, same fix, applied consistently across every docker
// invocation that takes an id.
func (p *Provider) Start(ctx context.Context, id string) error {
	if _, err := p.runner.Run(ctx, p.dockerBin(), "start", "--", id); err != nil {
		return fmt.Errorf("start container %s: %w", id, err)
	}
	return nil
}

func (p *Provider) Stop(ctx context.Context, id string) error {
	if _, err := p.runner.Run(ctx, p.dockerBin(), "stop", "--", id); err != nil {
		return fmt.Errorf("stop container %s: %w", id, err)
	}
	return nil
}

func (p *Provider) Restart(ctx context.Context, id string) error {
	if _, err := p.runner.Run(ctx, p.dockerBin(), "restart", "--", id); err != nil {
		return fmt.Errorf("restart container %s: %w", id, err)
	}
	return nil
}
