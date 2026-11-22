package system

import (
	"context"
	"time"
)

// SystemInfoProvider reports the machine's identity.
type SystemInfoProvider interface {
	SystemInfo(ctx context.Context) (SystemInfo, error)
}

// CPUProvider reports CPU identity and utilization.
type CPUProvider interface {
	CPUInfo(ctx context.Context) (CPUInfo, error)

	// CPUUsage samples utilization over sampleWindow. Instantaneous CPU
	// usage isn't a meaningful concept - it requires two time-separated
	// counter readings - so this call blocks for approximately
	// sampleWindow before returning. Callers on a request/response path
	// (as opposed to a background sampler) should pass a short window
	// (a few hundred milliseconds) and a context with its own timeout.
	CPUUsage(ctx context.Context, sampleWindow time.Duration) (CPUUsage, error)
}

// MemoryProvider reports physical memory and swap usage.
type MemoryProvider interface {
	MemoryInfo(ctx context.Context) (MemoryInfo, error)
}

// StorageProvider reports mounted filesystem usage.
type StorageProvider interface {
	Disks(ctx context.Context) ([]DiskInfo, error)
}

// NetworkProvider reports network interface identity and addresses.
type NetworkProvider interface {
	NetworkInterfaces(ctx context.Context) ([]NetworkInterface, error)

	// NetworkIOCounters reports each interface's cumulative bytes
	// sent/received since boot (or since the counter last wrapped -
	// callers computing a rate from two readings should treat a
	// decrease as a counter reset, not a negative rate). See
	// NetIOCounters's doc comment on why this returns a snapshot, not a
	// rate. Returns ErrUnavailable on platforms without a sufficiently
	// verifiable source - see provider_darwin.go/provider_windows.go.
	NetworkIOCounters(ctx context.Context) ([]NetIOCounters, error)

	// NetworkConnections reports current listening ports and active
	// connections (spec section 13). This is socket-table inspection,
	// not packet capture: Weloury reads the OS's existing record of what's
	// bound/connected (e.g. /proc/net/tcp on Linux), the same source
	// `netstat`/`ss` use, never opens a raw socket or inspects packet
	// contents - see spec section 13's explicit "do not turn this into a
	// packet-sniffing application." Returns ErrUnavailable on platforms
	// without a sufficiently verifiable source, matching
	// NetworkIOCounters's precedent.
	NetworkConnections(ctx context.Context) ([]NetworkConnection, error)
}

// ProcessProvider reports running processes and can act on them.
type ProcessProvider interface {
	ListProcesses(ctx context.Context) ([]ProcessInfo, error)

	// ProcessDetail fetches extended information for one process.
	// Returns ErrNotFound if pid does not correspond to a running
	// process. sampleWindow bounds how long this may block to compute
	// CPUPercent - see ProcessDetail's doc comment; pass 0 to accept each
	// provider's own short default.
	ProcessDetail(ctx context.Context, pid int32, sampleWindow time.Duration) (ProcessDetail, error)

	// TerminateProcess asks the OS to end the process identified by pid.
	// force selects the termination method: false requests a graceful
	// shutdown (SIGTERM on Unix); true requests immediate termination
	// (SIGKILL on Unix, TerminateProcess on Windows). Returns
	// ErrNotFound if the process is already gone, and ErrUnavailable if
	// this provider/platform doesn't support the requested mode (see
	// provider_windows.go: graceful termination isn't supported there).
	//
	// TerminateProcess never prompts for confirmation itself - that is a
	// UI-layer responsibility (see
	// frontend/src/components/ui/ConfirmDialog.tsx) - and never shells
	// out to a shell; every implementation uses a direct syscall.
	TerminateProcess(ctx context.Context, pid int32, force bool) error
}

// GPUProvider reports graphics adapters and their live telemetry. Every
// method returns ErrUnavailable on platforms without a verifiable source
// (or on machines with no discrete/integrated adapter exposed), matching
// the graceful-degradation pattern of NetworkProvider - see provider.go
// per-platform files for what each OS actually implements.
type GPUProvider interface {
	// ListGPUs reports every adapter the OS exposes. An empty (non-nil)
	// slice with no error means the machine genuinely has no reported
	// adapter - distinct from an error, which means the platform couldn't
	// determine it at all.
	ListGPUs(ctx context.Context) ([]GPUInfo, error)

	// GPUUsage samples one adapter's live telemetry. Returns
	// ErrNotFound if the index is out of range, ErrUnavailable if the
	// platform has no telemetry source for it.
	GPUUsage(ctx context.Context, index int) (GPUUsage, error)
}

// ServiceProvider reports OS-managed services and can act on them. See
// ServiceInfo's doc comment on why this is a projection of three
// genuinely different platform service models, not a claim they're
// equivalent.
type ServiceProvider interface {
	ListServices(ctx context.Context) ([]ServiceInfo, error)

	// ServiceDetail fetches extended information for one service, keyed
	// by ServiceInfo.Name. Returns ErrNotFound if no such service exists.
	ServiceDetail(ctx context.Context, name string) (ServiceDetail, error)

	// StartService, StopService, and RestartService change a service's
	// running state. All three commonly require elevated OS privileges
	// (root, or an administrator on Windows) that the Weloury core process
	// may not have; see SECURITY.md - Weloury never attempts to escalate
	// privileges itself, and a permission failure here is surfaced as a
	// normal error, not silently retried with elevated rights.
	StartService(ctx context.Context, name string) error
	StopService(ctx context.Context, name string) error
	RestartService(ctx context.Context, name string) error

	// SetServiceEnabled changes whether a service starts automatically at
	// boot (systemd: `enable`/`disable`; Windows: the service's start
	// type). It does not itself start or stop the service.
	SetServiceEnabled(ctx context.Context, name string, enabled bool) error
}

// DiskHealthProvider reports physical-drive SMART-class health.
type DiskHealthProvider interface {
	// DiskHealth lists one DiskHealth per physical drive the OS exposes.
	// Returns ErrUnavailable when the platform has no verifiable source
	// at all; individual drives the OS can't report (e.g. a USB bridge
	// that hides SMART) simply have Status "unknown" rather than failing
	// the listing - a partial answer is still an answer.
	DiskHealth(ctx context.Context) ([]DiskHealth, error)
}

// Provider is every capability this package exposes, combined. Platform
// implementations satisfy all of it; a platform that cannot support one
// piece still implements the method and returns ErrUnavailable rather
// than being excluded from the interface - see ARCHITECTURE.md "OS
// abstraction".
type Provider interface {
	SystemInfoProvider
	CPUProvider
	MemoryProvider
	StorageProvider
	DiskHealthProvider
	NetworkProvider
	ProcessProvider
	ServiceProvider
	GPUProvider
	StartupManager
}

// New returns the Provider implementation for the platform this binary
// was built for. newPlatformProvider is implemented once per platform in
// provider_linux.go / provider_darwin.go / provider_windows.go, each
// under its own Go build constraint, so exactly one is compiled into any
// given build.
func New() Provider {
	return newPlatformProvider()
}
