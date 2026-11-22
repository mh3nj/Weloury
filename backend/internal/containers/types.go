// Package containers is Weloury's container discovery and control (spec
// section 15): detect a container runtime, list containers with status/
// image/ports/CPU/memory, start/stop/restart them, and fetch basic logs.
//
// Talks to Docker exclusively through the `docker` CLI (shelling out and
// parsing `--format '{{json .}}'` output), not the Docker Engine API over
// its Unix socket. This is a deliberate architectural choice, not a
// shortcut: it's the same pattern Weloury already uses for every other
// external system integration - `systemctl`/`launchctl`/PowerShell for
// service management (internal/system), `ps` for Darwin process
// listing - shell out to a well-documented, stable CLI and parse its
// output, rather than implement or depend on a client for that system's
// native protocol. The payoff is the same each time: zero new
// third-party dependencies (no Docker SDK), and the actual wire protocol
// (Docker's HTTP-over-Unix-socket API, versioned and non-trivial) is
// something the `docker` CLI already handles correctly on Weloury's
// behalf. Every `docker` invocation is argv-only via exec.CommandContext,
// never a shell - the identical discipline internal/health's `command`
// check type and internal/automation's `run_command` action use.
//
// "Container runtime" currently means Docker specifically, not container
// runtimes in general - see Provider's doc comment.
package containers

import "time"

// Container is one Docker container's inventory plus a best-effort
// resource snapshot (spec section 15: "containers... container status...
// image... ports... CPU usage... memory usage... start time... basic
// logs").
//
// Ports is kept as Docker's own pre-formatted display string (e.g.
// "0.0.0.0:18080->8080/tcp", or "8080/tcp" for an exposed-but-unpublished
// port) rather than parsed into structured host/container port fields.
// This mirrors NetworkConnection's approach in internal/system - display
// what the source already formats well rather than re-deriving a
// structure that's easy to get subtly wrong (Docker's own port-mapping
// JSON, from `docker inspect`, has real sharp edges: a mapping can be
// nil for an exposed-but-unpublished port, a container can have zero,
// one, or many mappings per internal port) for a UI that only needs to
// display the information, not act on its structure.
type Container struct {
	ID        string    `json:"id"`   // short ID (12 hex chars), as `docker ps` reports it
	Name      string    `json:"name"` // with any leading "/" Docker's API adds already stripped
	Image     string    `json:"image"`
	State     string    `json:"state"`  // Docker's own vocabulary verbatim: "running", "exited", "paused", "restarting", ...
	Status    string    `json:"status"` // Docker's human-readable status, e.g. "Up 3 seconds", "Exited (2) 4 minutes ago"
	Ports     string    `json:"ports,omitempty"`
	CreatedAt time.Time `json:"created_at"`

	// CPUPercent/MemoryUsageBytes/MemoryLimitBytes/PIDCount are non-nil
	// only for a currently-running container - `docker stats` (their
	// source) simply omits a stopped container from its output rather
	// than reporting zeros, and reporting a fabricated zero here would
	// misrepresent "not running" as "running and idle." See
	// Provider.List's doc comment.
	CPUPercent       *float64 `json:"cpu_percent,omitempty"`
	MemoryUsageBytes *uint64  `json:"memory_usage_bytes,omitempty"`
	MemoryLimitBytes *uint64  `json:"memory_limit_bytes,omitempty"`
	PIDCount         *int     `json:"pid_count,omitempty"`
}

// Availability describes whether the container runtime can be used right
// now - spec section 15: "show runtime availability." A false Available
// is normal, expected operation for most machines running Weloury, not an
// error condition - see spec section 15's "the application must remain
// useful on systems without containers" and Provider's doc comment.
type Availability struct {
	Available bool   `json:"available"`
	Reason    string `json:"reason,omitempty"` // human-readable, only set when Available is false
}
