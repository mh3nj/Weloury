// Package system defines Weloury's OS abstraction layer: interfaces for
// every category of machine information Weloury reads (system identity,
// CPU, memory, storage, network, processes), plus one concrete
// implementation per supported platform selected at compile time via Go
// build constraints.
//
// No file in this package outside the platform-specific ones
// (provider_linux.go, provider_darwin.go, provider_windows.go) may
// contain OS-specific code - see ARCHITECTURE.md "OS abstraction" for why
// this boundary matters as much for the future remote-agent architecture
// as it does for cross-platform support today.
package system

import (
	"context"
	"time"
)

// SystemInfo describes the machine Weloury is running on.
type SystemInfo struct {
	Hostname string `json:"hostname"`
	// OS is the Go runtime.GOOS value ("linux", "darwin", "windows").
	OS string `json:"os"`
	// OSVersion is a best-effort human-readable string (e.g.
	// "Ubuntu 24.04.4 LTS"), not a value meant to be parsed.
	OSVersion     string    `json:"os_version"`
	KernelVersion string    `json:"kernel_version"`
	Architecture  string    `json:"architecture"`
	UptimeSeconds float64   `json:"uptime_seconds"`
	BootTime      time.Time `json:"boot_time"`
}

// CPUInfo describes the CPU's static identity - not its current load; see
// CPUUsage for that.
type CPUInfo struct {
	ModelName     string  `json:"model_name"`
	PhysicalCores int     `json:"physical_cores"`
	LogicalCores  int     `json:"logical_cores"`
	ClockMHz      float64 `json:"clock_mhz"`
}

// CPUUsage is a point-in-time (or short-window-averaged, depending on the
// provider - see each platform file's CPUUsage doc comment) utilization
// reading.
type CPUUsage struct {
	OverallPercent float64   `json:"overall_percent"`
	PerCorePercent []float64 `json:"per_core_percent"`
}

// MemoryInfo describes physical memory and swap usage.
type MemoryInfo struct {
	TotalBytes     uint64 `json:"total_bytes"`
	UsedBytes      uint64 `json:"used_bytes"`
	AvailableBytes uint64 `json:"available_bytes"`
	SwapTotalBytes uint64 `json:"swap_total_bytes"`
	SwapUsedBytes  uint64 `json:"swap_used_bytes"`
}

// DiskInfo describes one mounted filesystem.
type DiskInfo struct {
	Device     string `json:"device"`
	MountPoint string `json:"mount_point"`
	Filesystem string `json:"filesystem"`
	TotalBytes uint64 `json:"total_bytes"`
	FreeBytes  uint64 `json:"free_bytes"`
	UsedBytes  uint64 `json:"used_bytes"`
	// VolumeName is the OS-level label ("Windows", "Data", ...) where
	// the platform exposes one; empty otherwise.
	VolumeName string `json:"volume_name,omitempty"`
	// DriveType is a small documented vocabulary describing what the
	// volume physically is: "internal", "removable", "network",
	// "optical", "ramdisk", or "unknown". lets the UI group or icon
	// drives without re-deriving heuristics client-side.
	DriveType string `json:"drive_type,omitempty"`
	// BusType is the physical transport where reported ("NVMe", "SATA",
	// "USB", ...); empty when the platform doesn't expose it.
	BusType string `json:"bus_type,omitempty"`
	// IsSystemDrive marks the volume the OS boots from (C:\ on Windows,
	// / on Linux/macOS). Exactly one drive in a listing should carry it.
	IsSystemDrive bool `json:"is_system_drive,omitempty"`
}

// NetworkInterface describes one network interface's identity and
// addresses. Throughput (bytes sent/received) is intentionally not part
// of this snapshot type - it's a rate, which needs two time-separated
// samples to mean anything, and belongs to the metrics engine (Phase 4)
// rather than a one-shot inventory call.
type NetworkInterface struct {
	Name         string   `json:"name"`
	HardwareAddr string   `json:"hardware_addr"`
	Addresses    []string `json:"addresses"`
	IsUp         bool     `json:"is_up"`
	IsLoopback   bool     `json:"is_loopback"`
}

// NetIOCounters is one interface's cumulative byte counters at a point in
// time - the raw material the metrics engine (Phase 4) samples twice,
// time-separated, to compute a throughput rate. Deliberately not a rate
// itself: NetworkProvider is a snapshot API (see NetworkInterface's doc
// comment above), and computing rates from two snapshots is the metrics
// engine's job, not this layer's.
type NetIOCounters struct {
	Name    string `json:"name"`
	RxBytes uint64 `json:"rx_bytes"`
	TxBytes uint64 `json:"tx_bytes"`
}

// NetworkConnection is one entry from the OS's socket table - a
// listening socket or an active connection (spec section 13: "listening
// ports... active connections... protocol... local endpoint... remote
// endpoint... owning process where available"). Deliberately socket
// metadata only, the same information `netstat`/`ss`/`lsof -i` surface -
// see NetworkProvider.NetworkConnections' doc comment on why this is
// not, and will not become, packet-level inspection.
type NetworkConnection struct {
	// Protocol is "tcp", "tcp6", "udp", or "udp6".
	Protocol string `json:"protocol"`
	// LocalAddress is textual (IPv4 dotted-quad or IPv6), never a hex
	// encoding - the OS-specific source format is fully decoded before
	// this type is populated.
	LocalAddress string `json:"local_address"`
	LocalPort    int    `json:"local_port"`
	// RemoteAddress/RemotePort are the zero value for a listening
	// socket (nothing connected to it yet) or a connectionless UDP
	// socket that hasn't `connect()`-ed to a specific peer.
	RemoteAddress string `json:"remote_address,omitempty"`
	RemotePort    int    `json:"remote_port,omitempty"`
	// State is a TCP connection state ("LISTEN", "ESTABLISHED",
	// "TIME_WAIT", ...) - always empty for UDP, which has no connection
	// state to report.
	State string `json:"state,omitempty"`
	// PID/ProcessName identify the owning process where it could be
	// determined. Both are zero/empty - not an error - when the
	// platform can't attribute a socket to a process at all, or when the
	// socket belongs to a process Weloury core doesn't have permission to
	// inspect (a socket owned by another user, without elevated
	// privileges - see provider_linux.go's doc comment on
	// NetworkConnections for exactly what that means on Linux).
	PID         int32  `json:"pid,omitempty"`
	ProcessName string `json:"process_name,omitempty"`
}

// ProcessInfo describes one running process. This is Phase 2's discovery
// shape (list what's running); ProcessDetail (Phase 3) adds the
// single-process detail view - see spec section 6.
type ProcessInfo struct {
	PID  int32  `json:"pid"`
	PPID int32  `json:"ppid"`
	Name string `json:"name"`
	// User is the best-effort resolved owner username; empty if it
	// couldn't be resolved (e.g. the UID has no /etc/passwd entry).
	User string `json:"user"`
	// Status is provider-specific but drawn from a small, documented
	// vocabulary per platform (e.g. Linux: "running", "sleeping",
	// "stopped", "zombie") rather than passed through raw OS codes.
	Status      string    `json:"status"`
	StartedAt   time.Time `json:"started_at"`
	MemoryBytes uint64    `json:"memory_bytes"`
	Threads     int       `json:"threads"`

	// CPUPercent is the process's CPU share of the WHOLE machine (the
	// Task Manager Processes-tab convention: 100% = all cores), when
	// the platform can measure it in a bulk listing; nil = not measured
	// (a pointer so a genuinely-0% process stays distinguishable from
	// "this platform doesn't provide per-process CPU in listings").
	// Windows fills it from the pre-formatted process perf counters in
	// the same listing pass; Linux/macOS leave it nil rather than guess.
	// NOTE for ProcessDetail (below): its own shallower CPUPercent field
	// shadows this one on the wire for detail responses - documented
	// encoding/json depth behavior, relied on deliberately.
	CPUPercent *float64 `json:"cpu_percent,omitempty"`

	// DiskIOBytesPerSec is the process's current disk I/O rate in bytes
	// per second (the Task Manager "Disk" column), same pointer
	// semantics as CPUPercent. Network-per-process is deliberately
	// absent: it needs ETW/eBPF-class instrumentation, not a counter
	// read - see NetworkProvider's packet-inspection boundary.
	DiskIOBytesPerSec *float64 `json:"disk_io_bytes_per_sec,omitempty"`
}

// ProcessDetail extends ProcessInfo with information that's more
// expensive to gather for every process in a full listing but reasonable
// to fetch for one process at a time - see spec section 6's process
// detail fields.
type ProcessDetail struct {
	ProcessInfo

	// Executable is the resolved path to the running binary, where the
	// platform and process permissions allow it; empty otherwise (never
	// an error by itself - see each provider's ProcessDetail doc comment).
	Executable string `json:"executable"`

	// CommandLine is the process's argv, where available.
	CommandLine []string `json:"command_line"`

	// CPUPercent is sampled the same way CPUProvider.CPUUsage is -
	// blocking briefly to compare two time-separated readings - except
	// on platforms/providers where a single already-computed value is
	// available from the OS itself (see provider_darwin.go), in which
	// case ProcessDetail does not block at all. It shadows the embedded
	// ProcessInfo.CPUPercent pointer in JSON (Go's encoding/json prefers
	// the shallower field) - same wire name, this is the detail view's
	// authoritative value.
	CPUPercent float64 `json:"cpu_percent"`

	// User resolves the process owner at detail time (the listing
	// deliberately skips GetOwner per row - see ListProcesses).
	// OwnerName is set when the platform could attribute the process;
	// empty otherwise, never an error.
	OwnerName string `json:"owner_name,omitempty"`
}

// ProcessTreeNode is one node of the parent/child hierarchy built by
// BuildProcessTree. It embeds ProcessInfo rather than the heavier
// ProcessDetail - a tree view is for navigating the process list's
// structure, not displaying every process's full detail at once.
type ProcessTreeNode struct {
	ProcessInfo
	Children []*ProcessTreeNode `json:"children,omitempty"`
}

// ServiceInfo describes one OS-managed service/unit/job - a systemd
// unit on Linux, a launchd job on macOS, a Service Control Manager
// service on Windows. The three platforms' service models are genuinely
// different (see internal/system's Darwin/Windows provider files for
// specifics); ServiceInfo is Weloury's common projection of them, not a
// claim that they're the same thing. See spec section 7.
type ServiceInfo struct {
	// Name is the platform's own stable identifier: a systemd unit name
	// ("nginx.service"), a launchd label ("com.apple.something"), or a
	// Windows service name ("Spooler" - not its display name).
	Name string `json:"name"`

	// DisplayName is a human-friendly name where the platform provides
	// one distinct from Name; falls back to Name where it doesn't
	// (systemd and launchd have no separate concept of this).
	DisplayName string `json:"display_name"`

	Description string `json:"description"`

	// State is drawn from a small, documented vocabulary - "running",
	// "stopped", "failed", "unknown" - not passed through raw
	// platform-specific state strings, the same normalization principle
	// ProcessInfo.Status already follows.
	State string `json:"state"`

	// StartupMode is one of "enabled", "disabled", "manual", "unknown".
	// Platforms that don't cleanly map to this (notably launchd - see
	// provider_darwin.go) report "unknown" rather than guessing.
	StartupMode string `json:"startup_mode"`

	// PID is the service's main process ID if it's currently running,
	// 0 otherwise.
	PID int32 `json:"pid"`
}

// ServiceDetail extends ServiceInfo with information gathered per-service
// rather than for every service in a full listing.
type ServiceDetail struct {
	ServiceInfo
	// Dependencies lists other services this one depends on, where the
	// platform exposes that cleanly (Linux via systemd's Requires/After;
	// left empty on platforms where it doesn't - see each provider).
	Dependencies []string `json:"dependencies"`
}

// GPUInfo describes one graphics adapter's static identity.
type GPUInfo struct {
	Name string `json:"name"`
	// Vendor is the adapter's vendor string as the OS reports it
	// ("NVIDIA", "AMD", "Intel", ...), not a normalized enum.
	Vendor string `json:"vendor,omitempty"`
	// DriverVersion is the installed driver version string.
	DriverVersion string `json:"driver_version,omitempty"`
	// TotalMemoryBytes is the adapter's dedicated video memory (VRAM);
	// 0 when the platform doesn't report it (shared-memory integrated
	// graphics often has none to report).
	TotalMemoryBytes uint64 `json:"total_memory_bytes,omitempty"`
	// PCIID, when known, is the adapter's PCI identifier "vendor:device"
	// hex pair (e.g. "10de:2484") - the stable key for matching against
	// vendor databases.
	PCIID string `json:"pci_id,omitempty"`
	// Index is the adapter's position in this listing - the same value a
	// GPUUsage call accepts, echoed here so a UI can key cards to usage
	// calls without recomputing array positions.
	Index int `json:"index,omitempty"`
}

// GPUUsage is one adapter's live telemetry at a sampling instant - the
// GPU counterpart to CPUUsage. Every field is best-effort: consumer
// GPUs vary in which counters they expose (temperature and fan speed
// need vendor tooling or elevated WMI namespaces on some hardware), so
// any field the platform can't read stays at its zero value and the UI
// treats that as "not reported" rather than an error.
type GPUUsage struct {
	// UtilizationPercent is the GPU's overall compute/graphics busy
	// percentage (the number Task Manager's GPU tab shows). A pointer so
	// "adapter present but no telemetry source" (driver counters absent,
	// a common Windows state) stays distinguishable from a genuine 0% -
	// nil means not reported.
	UtilizationPercent *float64 `json:"utilization_percent,omitempty"`
	// MemoryUsedBytes is current dedicated-memory usage; MemoryTotalBytes
	// repeats GPUInfo.TotalMemoryBytes so a usage ratio needs one call.
	MemoryUsedBytes   uint64 `json:"memory_used_bytes,omitempty"`
	MemoryTotalBytes  uint64 `json:"memory_total_bytes,omitempty"`
	// TemperatureCelsius is the GPU core temperature in °C when the
	// platform exposes it (often needs the vendor's WMI namespace, e.g.
	// NVIDIA's NVAPI or Win32_PerfFormattedData_GPUEngine on newer
	// builds); 0 means not reported.
	TemperatureCelsius float64 `json:"temperature_celsius,omitempty"`
	// FanPercent is fan speed as a percentage of maximum, when reported.
	FanPercent float64 `json:"fan_percent,omitempty"`
	// PowerWatts is the adapter's current board power draw in watts (the
	// number nvidia-smi's power.draw and afterburner-style overlays
	// show); PowerLimitWatts is the card's enforced power limit at that
	// moment. 0 means not reported.
	PowerWatts      float64 `json:"power_watts,omitempty"`
	PowerLimitWatts float64 `json:"power_limit_watts,omitempty"`
	// ClockMHz is the current graphics/core clock in MHz when reported.
	ClockMHz uint64 `json:"clock_mhz,omitempty"`
}

// DiskHealth is one physical drive's SMART-class health projection -
// distinct from DiskInfo (mounted volumes) the same way Task Manager
// separates volumes from physical drives. Every field is best-effort:
// a platform that can't read a field leaves it at its zero value and
// Available stays true for what it could read.
type DiskHealth struct {
	// Device matches DiskInfo.Device where the two listings share a
	// name (Windows: the PHYSICALDRIVE%d name; Linux: /dev/sda;
	// macOS: /dev/disk0). Keyed by this in the UI's join with volumes.
	Device string `json:"device"`
	// Model is the drive's make/model string ("WDC WD10EZEX-00WN4A0",
	// "Samsung SSD 970", "APPLE SSD SM0256F").
	Model string `json:"model,omitempty"`
	// MediaType is "SSD", "HDD", or empty when unknown.
	MediaType string `json:"media_type,omitempty"`
	// BusType is the transport ("NVMe", "SATA", "USB", "RAID", ...).
	BusType string `json:"bus_type,omitempty"`
	// Status is a small vocabulary: "healthy", "warning", "failing",
	// or "unknown" (probes unavailable), never raw platform enums.
	Status string `json:"status"`
	// HealthPercent is the remaining life / health 0-100 where a source
	// reports one. A direct SMART reading (NVMe percentage-used, SSD wear
	// attribute) sets Estimated false; a complement, WMI wear value, or
	// HDD heuristic sets Estimated true. 0 with Estimated false means the
	// drive's health was not reported at all.
	HealthPercent float64 `json:"health_percent,omitempty"`
	// Estimated marks HealthPercent as derived (a complement, a WMI wear
	// value, or a bad-sector heuristic) rather than a vendor's own figure.
	Estimated bool `json:"estimated,omitempty"`
	// HealthSource names where HealthPercent came from so the UI can be
	// honest about it: "smart" (smartctl, direct reading), "wmi" (Windows
	// storage reliability wear), or "" when there is no percentage.
	HealthSource string `json:"health_source,omitempty"`
	// TemperatureCelsius is the drive's reported temperature; 0 means
	// not reported (many USB bridges and older SATAs don't expose it).
	TemperatureCelsius float64 `json:"temperature_celsius,omitempty"`
	// PowerOnHours / PowerCycles are the SMART 9 / 12 attributes where
	// the platform reports them.
	PowerOnHours uint64 `json:"power_on_hours,omitempty"`
	PowerCycles  uint64 `json:"power_cycles,omitempty"`
	// TotalBytes is the drive's advertised capacity (from the physical
	// disk, not a partitioned volume).
	TotalBytes uint64 `json:"total_bytes,omitempty"`
}

// StartupEntry is one OS startup-location item (the Startup tab's
// domain): an autostart registered in a per-user/per-system launch
// location. Location is a stable slug from a documented vocabulary (see
// startup_windows.go / startup_unix.go for each platform's list) so a
// UI can group by it without parsing command lines.
type StartupEntry struct {
	// ID is stable per-location identifier the toggle/delete round-trip
	// against (Windows: the registry value name or file name; Linux:
	// the .desktop file's basename; macOS: the launchd label).
	ID string `json:"id"`
	// Name is the human label (usually the program name from the entry).
	Name string `json:"name"`
	// Command is the registered command line / Exec value.
	Command string `json:"command,omitempty"`
	// Location is the documented location slug ("registry-run",
	// "startup-folder", "launch-agent", "systemd-user", ...).
	Location string `json:"location"`
	// Scope is "user" or "system" - entries a non-elevated Weloury can
	// act on vs ones it can only report (toggling a system-scope entry
	// needs elevation and is surfaced as an error, not silently skipped -
	// see SECURITY.md's no-self-elevation rule).
	Scope string `json:"scope"`
	// Enabled reports the entry's own enable/disable state where the
	// location has one (startup-folder entries are always true; some
	// .desktop files carry a Hidden flag; Windows Run-key entries are
	// enabled unless the OS reports them disabled via StartupApproved).
	Enabled bool `json:"enabled"`
}

// StartupManager is the optional Provider capability for reading and
// acting on OS startup entries. Enabled toggles an existing entry in
// place; Add registers a new custom entry launching exe with args;
// Remove deletes one entirely (custom entries added through Add are
// removable by ID; pre-existing entries are removed through the same
// OS location they came from). Implementations return ErrUnavailable
// on platforms where a location isn't supported, and errors - never
// silent skips - when a specific entry can't be acted on.
type StartupManager interface {
	ListStartupEntries(ctx context.Context) ([]StartupEntry, error)
	SetStartupEntryEnabled(ctx context.Context, id string, enabled bool) error
	AddStartupEntry(ctx context.Context, name, exe string, args []string) error
	RemoveStartupEntry(ctx context.Context, id string) error
}
