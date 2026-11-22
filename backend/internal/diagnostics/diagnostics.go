// Package diagnostics is Weloury's diagnostics area (spec section 23): an
// on-demand snapshot of "what's going on with this machine and with
// Weloury itself right now," composed entirely from subsystems that
// already exist - internal/system for machine facts, internal/health
// for check results, internal/storage for recent event history.
//
// Deliberately not an engine: Generator has no background goroutine, no
// persistence of its own, and Generate does no work beyond calling into
// each subsystem's existing read API and assembling the results. Every
// section degrades independently (spec section 4's "gracefully
// degrade... communicate unavailable capabilities rather than
// crashing," applied here exactly as it is throughout internal/system):
// one section failing (e.g. CPU usage sampling being unavailable on the
// current platform) never prevents the rest of the report from being
// generated, and is recorded as that section's own Error field rather
// than failing Generate outright.
package diagnostics

import (
	"context"
	"time"

	"weloury/internal/eventbus"
	"weloury/internal/health"
	"weloury/internal/system"
)

// Report is one point-in-time diagnostics snapshot. Every section field
// is a value type with its own optional Error string, not a pointer that
// might be nil - a report a client can render unconditionally, checking
// each section's own Error rather than nil-checking pointers throughout.
type Report struct {
	GeneratedAt time.Time `json:"generated_at"`

	System   SystemSection   `json:"system"`
	Storage  StorageSection  `json:"storage"`
	Network  NetworkSection  `json:"network"`
	Services ServicesSection `json:"services"`
	Health   HealthSection   `json:"health"`
	// RecentIssues is the tail of persisted event history filtered to
	// warning/critical severity (spec section 23: "important
	// application errors") - never secrets: it reads the same events
	// table internal/logs and the Events screen already expose, which
	// SECURITY.md's "Safe logging" section already covers.
	RecentIssues []eventbus.Event `json:"recent_issues"`
}

// SystemSection covers spec section 23's "system information."
type SystemSection struct {
	Info   *system.SystemInfo `json:"info,omitempty"`
	CPU    *system.CPUInfo    `json:"cpu,omitempty"`
	Memory *system.MemoryInfo `json:"memory,omitempty"`
	Error  string             `json:"error,omitempty"`
}

// StorageSection covers spec section 23's "disk health indicators where
// accessible" - scoped honestly to disk *usage*, not SMART/hardware
// health telemetry; see CHANGELOG.md's "Known limitations" for why real
// SMART data is out of scope (platform-specific, privilege-requiring,
// and risky to get subtly wrong across three operating systems for a
// diagnostics feature whose main job is being trustworthy).
type StorageSection struct {
	Disks []system.DiskInfo `json:"disks,omitempty"`
	Error string            `json:"error,omitempty"`
}

// NetworkSection covers spec section 23's "network configuration" and
// "listening ports." Listening is a TCP entry in LISTEN state, or any
// UDP entry (UDP has no connection state to check - see
// system.NetworkConnection.State's doc comment - so every UDP socket in
// the table counts as "listening" in the sense this section means: bound
// and able to receive).
type NetworkSection struct {
	Interfaces     []system.NetworkInterface  `json:"interfaces,omitempty"`
	ListeningPorts []system.NetworkConnection `json:"listening_ports,omitempty"`
	Error          string                     `json:"error,omitempty"`
}

// ServicesSection covers spec section 23's "service states" - every
// non-running service, not the full inventory (an operator running
// diagnostics wants to know what's wrong, not re-derive that from a
// list of everything).
type ServicesSection struct {
	NotRunning []system.ServiceInfo `json:"not_running,omitempty"`
	Error      string               `json:"error,omitempty"`
}

// HealthSection covers spec section 23's "failed health checks" -
// again, only the ones not currently HEALTHY, not the full list.
type HealthSection struct {
	Failing []health.Status `json:"failing,omitempty"`
}

// recentIssuesLimit bounds how many recent warning/critical events
// Generate includes - a diagnostics report is meant to be skimmed and
// exported, not a full event history dump (the Events screen already
// covers that).
const recentIssuesLimit = 50

// sysCapabilities is the minimal slice of system.Provider Generator
// needs - the same interface-segregation pattern
// internal/health/internal/metrics/internal/automation's sysCapabilities
// use, for the identical reason: an honest dependency, and a fake in
// tests only needs to implement 7 methods instead of system.Provider's
// full ~15-method surface. system.Provider satisfies this automatically.
type sysCapabilities interface {
	SystemInfo(ctx context.Context) (system.SystemInfo, error)
	CPUInfo(ctx context.Context) (system.CPUInfo, error)
	MemoryInfo(ctx context.Context) (system.MemoryInfo, error)
	Disks(ctx context.Context) ([]system.DiskInfo, error)
	NetworkInterfaces(ctx context.Context) ([]system.NetworkInterface, error)
	NetworkConnections(ctx context.Context) ([]system.NetworkConnection, error)
	ListServices(ctx context.Context) ([]system.ServiceInfo, error)
}

// eventReader is the minimal slice of storage.EventRepository Generator
// needs - the same consumer-defined-interface pattern
// internal/health.Repository/internal/metrics.Repository/
// internal/scheduler.Repository/internal/automation.Repository all use,
// applied here for the identical payoff: Generator has no
// database/sql dependency and is fully testable with an in-memory fake,
// despite this sandbox's continued inability to reach
// modernc.org/sqlite directly - see DEVELOPMENT.md.
type eventReader interface {
	Recent(ctx context.Context, limit int) ([]eventbus.Event, error)
}

// Generator assembles a Report from its constituent subsystems. The zero
// value is not usable; construct with New.
type Generator struct {
	sys    sysCapabilities
	health *health.Engine
	events eventReader
}

// New constructs a Generator. All three arguments must be non-nil. sys
// is typically a system.Provider (satisfying sysCapabilities
// structurally - see that interface's doc comment); events is typically
// a *storage.EventRepository (satisfying eventReader the same way).
func New(sys sysCapabilities, healthEngine *health.Engine, events eventReader) *Generator {
	return &Generator{sys: sys, health: healthEngine, events: events}
}

// Generate assembles a fresh Report. It makes a bounded number of calls
// into already-fast subsystems (in-memory reads, one or two syscalls
// each) and is expected to complete in well under a second, including
// CPUInfo's brief blocking usage sample (see
// internal/app/methods.go's identically-purposed cpuUsageSampleWindow
// constant for the precedent this follows).
func (g *Generator) Generate(ctx context.Context) Report {
	report := Report{GeneratedAt: time.Now().UTC()}

	report.System = g.generateSystemSection(ctx)
	report.Storage = g.generateStorageSection(ctx)
	report.Network = g.generateNetworkSection(ctx)
	report.Services = g.generateServicesSection(ctx)
	report.Health = g.generateHealthSection()
	report.RecentIssues = g.generateRecentIssues(ctx)

	return report
}

func (g *Generator) generateSystemSection(ctx context.Context) SystemSection {
	var s SystemSection
	if info, err := g.sys.SystemInfo(ctx); err == nil {
		s.Info = &info
	} else {
		s.Error = err.Error()
	}
	if cpu, err := g.sys.CPUInfo(ctx); err == nil {
		s.CPU = &cpu
	}
	if mem, err := g.sys.MemoryInfo(ctx); err == nil {
		s.Memory = &mem
	}
	return s
}

func (g *Generator) generateStorageSection(ctx context.Context) StorageSection {
	disks, err := g.sys.Disks(ctx)
	if err != nil {
		return StorageSection{Error: err.Error()}
	}
	return StorageSection{Disks: disks}
}

func (g *Generator) generateNetworkSection(ctx context.Context) NetworkSection {
	var s NetworkSection
	if ifaces, err := g.sys.NetworkInterfaces(ctx); err == nil {
		s.Interfaces = ifaces
	} else {
		s.Error = err.Error()
	}
	if conns, err := g.sys.NetworkConnections(ctx); err == nil {
		for _, c := range conns {
			if c.State == "LISTEN" || c.Protocol == "udp" || c.Protocol == "udp6" {
				s.ListeningPorts = append(s.ListeningPorts, c)
			}
		}
	} else if s.Error == "" {
		s.Error = err.Error()
	}
	return s
}

func (g *Generator) generateServicesSection(ctx context.Context) ServicesSection {
	services, err := g.sys.ListServices(ctx)
	if err != nil {
		return ServicesSection{Error: err.Error()}
	}
	var notRunning []system.ServiceInfo
	for _, svc := range services {
		if svc.State != "running" {
			notRunning = append(notRunning, svc)
		}
	}
	return ServicesSection{NotRunning: notRunning}
}

func (g *Generator) generateHealthSection() HealthSection {
	var failing []health.Status
	for _, st := range g.health.List() {
		if st.State != health.StateHealthy {
			failing = append(failing, st)
		}
	}
	return HealthSection{Failing: failing}
}

func (g *Generator) generateRecentIssues(ctx context.Context) []eventbus.Event {
	// Over-fetch since most persisted events are informational - see the
	// severity filter below - then trim to recentIssuesLimit.
	recent, err := g.events.Recent(ctx, recentIssuesLimit*4)
	if err != nil {
		return nil
	}
	issues := make([]eventbus.Event, 0, recentIssuesLimit)
	for _, e := range recent {
		if e.Severity != eventbus.SeverityWarning && e.Severity != eventbus.SeverityCritical {
			continue
		}
		issues = append(issues, e)
		if len(issues) >= recentIssuesLimit {
			break
		}
	}
	return issues
}
