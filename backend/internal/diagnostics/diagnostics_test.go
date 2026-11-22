package diagnostics

import (
	"context"
	"errors"
	"testing"
	"time"

	"weloury/internal/eventbus"
	"weloury/internal/health"
	"weloury/internal/system"
)

// fakeSys implements both this package's sysCapabilities and (via
// structural typing - see health.New's doc comment on why an unexported
// interface parameter can still be satisfied from another package) the
// handful of methods internal/health's own unexported sysCapabilities
// needs, so one fake serves both this package's Generator and the real
// health.Engine used in tests below.
type fakeSys struct {
	sysInfo    system.SystemInfo
	cpuInfo    system.CPUInfo
	cpuUsage   system.CPUUsage
	memInfo    system.MemoryInfo
	disks      []system.DiskInfo
	interfaces []system.NetworkInterface
	conns      []system.NetworkConnection
	services   []system.ServiceInfo
	processes  []system.ProcessInfo

	sysInfoErr  error
	networkErr  error
	servicesErr error
	disksErr    error
}

func (f *fakeSys) SystemInfo(context.Context) (system.SystemInfo, error) {
	return f.sysInfo, f.sysInfoErr
}
func (f *fakeSys) CPUInfo(context.Context) (system.CPUInfo, error)       { return f.cpuInfo, nil }
func (f *fakeSys) MemoryInfo(context.Context) (system.MemoryInfo, error) { return f.memInfo, nil }
func (f *fakeSys) Disks(context.Context) ([]system.DiskInfo, error)      { return f.disks, f.disksErr }
func (f *fakeSys) NetworkInterfaces(context.Context) ([]system.NetworkInterface, error) {
	return f.interfaces, f.networkErr
}
func (f *fakeSys) NetworkConnections(context.Context) ([]system.NetworkConnection, error) {
	return f.conns, f.networkErr
}
func (f *fakeSys) ListServices(context.Context) ([]system.ServiceInfo, error) {
	return f.services, f.servicesErr
}
func (f *fakeSys) ListProcesses(context.Context) ([]system.ProcessInfo, error) {
	return f.processes, nil
}
func (f *fakeSys) ServiceDetail(context.Context, string) (system.ServiceDetail, error) {
	return system.ServiceDetail{}, system.ErrNotFound
}
func (f *fakeSys) CPUUsage(context.Context, time.Duration) (system.CPUUsage, error) {
	return f.cpuUsage, nil
}

// fakeHealthRepository is a minimal in-memory health.Repository, letting
// tests pre-populate a health.Engine with a check whose live status is
// already known, without waiting for a real evaluation cycle.
type fakeHealthRepository struct{ statuses []health.Status }

func (r *fakeHealthRepository) List(context.Context) ([]health.Status, error) { return r.statuses, nil }
func (r *fakeHealthRepository) Upsert(context.Context, health.Check) error    { return nil }
func (r *fakeHealthRepository) Delete(context.Context, string) error          { return nil }
func (r *fakeHealthRepository) UpdateResult(context.Context, string, health.Result) error {
	return nil
}

// fakeEvents implements eventReader in memory.
type fakeEvents struct {
	events []eventbus.Event
	err    error
}

func (f *fakeEvents) Recent(context.Context, int) ([]eventbus.Event, error) {
	return f.events, f.err
}

func newHealthEngineWithChecks(t *testing.T, sys *fakeSys, statuses []health.Status) *health.Engine {
	t.Helper()
	repo := &fakeHealthRepository{statuses: statuses}
	e := health.New(sys, nil, repo, nil)
	ctx, cancel := context.WithCancel(context.Background())
	if err := e.Start(ctx); err != nil {
		t.Fatalf("health.Engine.Start: %v", err)
	}
	t.Cleanup(func() { cancel(); e.Wait() })
	return e
}

func TestGenerateAllSectionsHappyPath(t *testing.T) {
	sys := &fakeSys{
		sysInfo:    system.SystemInfo{Hostname: "test-host"},
		disks:      []system.DiskInfo{{MountPoint: "/", TotalBytes: 100, UsedBytes: 50}},
		interfaces: []system.NetworkInterface{{Name: "eth0"}},
		conns: []system.NetworkConnection{
			{Protocol: "tcp", LocalPort: 8080, State: "LISTEN"},
			{Protocol: "tcp", LocalPort: 9090, State: "ESTABLISHED"},
			{Protocol: "udp", LocalPort: 53},
		},
		services: []system.ServiceInfo{
			{Name: "running-svc", State: "running"},
			{Name: "stopped-svc", State: "stopped"},
		},
	}
	// No checks configured -- an empty, always-satisfiable case, since
	// making one genuinely UNHEALTHY without waiting for a real
	// evaluation cycle needs its own dedicated test (see
	// TestGenerateHealthSectionOnlyIncludesFailing below).
	healthEngine := newHealthEngineWithChecks(t, sys, nil)
	events := &fakeEvents{}

	g := New(sys, healthEngine, events)
	report := g.Generate(context.Background())

	if report.GeneratedAt.IsZero() {
		t.Error("expected a non-zero GeneratedAt")
	}
	if report.System.Info == nil || report.System.Info.Hostname != "test-host" {
		t.Errorf("System.Info = %+v", report.System.Info)
	}
	if len(report.Storage.Disks) != 1 {
		t.Errorf("Storage.Disks = %+v, want 1 entry", report.Storage.Disks)
	}
	if len(report.Network.Interfaces) != 1 {
		t.Errorf("Network.Interfaces = %+v, want 1 entry", report.Network.Interfaces)
	}
	if len(report.Network.ListeningPorts) != 2 {
		t.Errorf("Network.ListeningPorts = %+v, want 2 (the LISTEN tcp entry and the udp entry, not the ESTABLISHED one)", report.Network.ListeningPorts)
	}
	if len(report.Services.NotRunning) != 1 || report.Services.NotRunning[0].Name != "stopped-svc" {
		t.Errorf("Services.NotRunning = %+v, want just stopped-svc", report.Services.NotRunning)
	}
	if len(report.Health.Failing) != 0 {
		t.Errorf("Health.Failing = %+v, want none (no checks configured)", report.Health.Failing)
	}
}

func TestGenerateDegradesIndividualSectionsIndependently(t *testing.T) {
	sys := &fakeSys{
		sysInfoErr:  errors.New("system info unavailable"),
		disksErr:    errors.New("disks unavailable"),
		networkErr:  errors.New("network unavailable"),
		servicesErr: errors.New("services unavailable"),
	}
	healthEngine := newHealthEngineWithChecks(t, sys, nil)
	events := &fakeEvents{err: errors.New("events unavailable")}

	g := New(sys, healthEngine, events)
	report := g.Generate(context.Background())

	if report.System.Error == "" {
		t.Error("expected System.Error to be set")
	}
	if report.Storage.Error == "" {
		t.Error("expected Storage.Error to be set")
	}
	if report.Network.Error == "" {
		t.Error("expected Network.Error to be set")
	}
	if report.Services.Error == "" {
		t.Error("expected Services.Error to be set")
	}
	// RecentIssues degrades to nil, not a panic, when events.Recent errors.
	if report.RecentIssues != nil {
		t.Errorf("RecentIssues = %v, want nil on events error", report.RecentIssues)
	}
	// Generate must still return a complete, non-panicking Report even
	// with every section failing -- this is the entire point of the
	// independent-degradation design.
	if report.GeneratedAt.IsZero() {
		t.Error("expected GeneratedAt to still be set despite every section failing")
	}
}

func TestGenerateHealthSectionOnlyIncludesFailing(t *testing.T) {
	sys := &fakeSys{}
	statuses := []health.Status{
		{Check: health.Check{ID: "chk_ok", Name: "ok check", Type: health.CheckProcessExists, Target: "nonexistent-so-it-fails-on-first-eval"}, State: health.StateUnknown},
	}
	healthEngine := newHealthEngineWithChecks(t, sys, statuses)

	// The check above will actually evaluate (process_exists against an
	// empty process list) almost immediately after Start, landing on
	// UNHEALTHY -- poll briefly rather than assuming exact timing.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if st, ok := healthEngine.Get("chk_ok"); ok && st.State != health.StateUnknown {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	events := &fakeEvents{}
	g := New(sys, healthEngine, events)
	report := g.Generate(context.Background())

	if len(report.Health.Failing) != 1 {
		t.Fatalf("Health.Failing = %+v, want exactly 1 (the check evaluates UNHEALTHY against an empty process list)", report.Health.Failing)
	}
	if report.Health.Failing[0].ID != "chk_ok" {
		t.Errorf("Health.Failing[0].ID = %q, want chk_ok", report.Health.Failing[0].ID)
	}
}

func TestGenerateRecentIssuesFiltersSeverity(t *testing.T) {
	sys := &fakeSys{}
	healthEngine := newHealthEngineWithChecks(t, sys, nil)
	events := &fakeEvents{events: []eventbus.Event{
		{ID: "1", Severity: eventbus.SeverityInfo, Type: "test.info"},
		{ID: "2", Severity: eventbus.SeverityWarning, Type: "test.warning"},
		{ID: "3", Severity: eventbus.SeverityCritical, Type: "test.critical"},
	}}

	g := New(sys, healthEngine, events)
	report := g.Generate(context.Background())

	if len(report.RecentIssues) != 2 {
		t.Fatalf("RecentIssues = %+v, want 2 (warning and critical, not info)", report.RecentIssues)
	}
	for _, e := range report.RecentIssues {
		if e.Severity == eventbus.SeverityInfo {
			t.Errorf("unexpected info-severity event in RecentIssues: %+v", e)
		}
	}
}
