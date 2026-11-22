//go:build windows

package system

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"sort"
	"strings"
	"syscall"
	"time"
	"unsafe"

	"weloury/internal/hidewin"
)

// This implementation deliberately minimizes use of raw Win32 struct
// syscalls, and where one is unavoidable, sticks to Windows's oldest and
// most stable APIs. The reasoning is specific to how this codebase was
// authored (see DEVELOPMENT.md): a struct-layout mistake in a Win32
// syscall doesn't just return a wrong value the way a text-parsing
// mistake does - passed by pointer to a real OS API, it can write past
// the buffer Go allocated. Every syscall used here is chosen because it
// avoids that risk or because the struct involved is about as
// well-established as Win32 struct layouts get:
//
//   - GetTickCount64 (uptime): zero struct risk - no parameters, a single
//     scalar return.
//   - GetDiskFreeSpaceExW (disk space): three independent uint64 output
//     pointers, not a packed multi-field struct - a layout mistake here
//     just means the wrong pointer gets the wrong single value, not a
//     misaligned write into a larger structure.
//   - GlobalMemoryStatusEx (memory): does take a multi-field struct
//     (MEMORYSTATUSEX). This one is accepted with an explicit note: it is
//     one of the most widely reproduced Win32 structs in existence
//     (essentially unchanged since Windows 2000), which is the basis for
//     the confidence here, but it is the one call in this file most worth
//     double-checking against a real build before relying on it.
//
// Everything else - OS identity, CPU identity, process listing - goes
// through PowerShell's CIM cmdlets (`Get-CimInstance`) via os/exec with a
// fixed argument list, parsed as CSV the same way provider_darwin.go
// parses command-line tool output: text parsing that fails safely (a
// wrong value or a returned error) rather than a memory-safety risk.
// wmic, the traditional alternative, is deprecated and absent by default
// on newer Windows versions, so it was not used.
type windowsProvider struct{}

func newPlatformProvider() Provider { return &windowsProvider{} }

var (
	kernel32               = syscall.NewLazyDLL("kernel32.dll")
	procGetTickCount64     = kernel32.NewProc("GetTickCount64")
	procGlobalMemoryStatus = kernel32.NewProc("GlobalMemoryStatusEx")
	procGetDiskFreeSpaceEx = kernel32.NewProc("GetDiskFreeSpaceExW")
	procGetLogicalDrives   = kernel32.NewProc("GetLogicalDrives")
	procGetSystemTimes     = kernel32.NewProc("GetSystemTimes")
)

// systemTimes mirrors the three FILETIME out-parameters GetSystemTimes
// fills: time spent idle, in kernel mode, and in user mode, each as a
// 100-nanosecond count since boot. Three independent int64 fields - no
// nested structures, no unions - the same "three independent output
// pointers" safety shape GetDiskFreeSpaceExW's use above is accepted
// for. These are system-wide aggregates across every core, which is
// exactly what CPUUsage needs to diff between two samples; per-core
// breakdown comes from the perf-counter query in cpuUsagePerCore.
type systemTimes struct {
	idle   int64
	kernel int64
	user   int64
}

// readSystemTimes reads one GetSystemTimes snapshot, returning an error
// only if the call itself fails (which for a kernel32 export taking
// three out-pointers would mean a broken handle table, not a data
// problem).
func readSystemTimes() (systemTimes, error) {
	var t systemTimes
	ret, _, callErr := procGetSystemTimes.Call(
		uintptr(unsafe.Pointer(&t.idle)),
		uintptr(unsafe.Pointer(&t.kernel)),
		uintptr(unsafe.Pointer(&t.user)),
	)
	if ret == 0 {
		return systemTimes{}, fmt.Errorf("GetSystemTimes: %w", callErr)
	}
	return t, nil
}

// cpuBusyPercent diffs two systemTimes snapshots into an overall busy
// percentage. GetSystemTimes' kernel time INCLUDES idle time (per its
// Microsoft docs), so busy = (kernel - idle) + user, and total = the
// deltas' sum. A decrease across any counter (none should occur - these
// are monotonic since boot) is clamped to zero rather than negative.
func cpuBusyPercent(a, b systemTimes) float64 {
	deltaIdle := b.idle - a.idle
	deltaKernel := b.kernel - a.kernel
	deltaUser := b.user - a.user
	if deltaIdle < 0 {
		deltaIdle = 0
	}
	if deltaKernel < 0 {
		deltaKernel = 0
	}
	if deltaUser < 0 {
		deltaUser = 0
	}
	busy := (deltaKernel - deltaIdle) + deltaUser
	total := deltaKernel + deltaUser
	if total <= 0 {
		return 0
	}
	return float64(busy) / float64(total) * 100
}

// memoryStatusEx mirrors the Win32 MEMORYSTATUSEX struct field-for-field
// (see the package doc comment above on why this one struct is accepted
// despite the general preference against raw syscalls here). dwLength
// must be set to unsafe.Sizeof(memoryStatusEx{}) before the call - the
// API validates it and fails cleanly if it doesn't match, rather than
// writing out of bounds.
type memoryStatusEx struct {
	dwLength                uint32
	dwMemoryLoad            uint32
	ullTotalPhys            uint64
	ullAvailPhys            uint64
	ullTotalPageFile        uint64
	ullAvailPageFile        uint64
	ullTotalVirtual         uint64
	ullAvailVirtual         uint64
	ullAvailExtendedVirtual uint64
}

func runPowerShell(ctx context.Context, script string) (string, error) {
	cmd := exec.CommandContext(ctx, "powershell.exe", "-NoProfile", "-NonInteractive", "-Command", script)
	hidewin.Configure(cmd) // no console flash per query - see hidewin's doc comment
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("run powershell: %w", err)
	}
	return string(out), nil
}

func (p *windowsProvider) SystemInfo(ctx context.Context) (SystemInfo, error) {
	hostname, err := os.Hostname()
	if err != nil {
		return SystemInfo{}, fmt.Errorf("read hostname: %w", err)
	}

	ret, _, callErr := procGetTickCount64.Call()
	if ret == 0 && callErr != syscall.Errno(0) {
		return SystemInfo{}, fmt.Errorf("GetTickCount64: %w", callErr)
	}
	uptime := time.Duration(ret) * time.Millisecond
	bootTime := time.Now().Add(-uptime)

	osInfo, err := runPowerShell(ctx, "(Get-CimInstance Win32_OperatingSystem | Select-Object Caption,Version | ConvertTo-Csv -NoTypeInformation)")
	osVersion, kernelVersion := "", ""
	if err == nil {
		if rows, perr := parseCIMCsv(osInfo); perr == nil && len(rows) > 0 {
			osVersion = strings.TrimSpace(rows[0]["caption"])
			kernelVersion = strings.TrimSpace(rows[0]["version"])
		}
	}

	return SystemInfo{
		Hostname:      hostname,
		OS:            runtime.GOOS,
		OSVersion:     osVersion,
		KernelVersion: kernelVersion,
		Architecture:  runtime.GOARCH,
		UptimeSeconds: uptime.Seconds(),
		BootTime:      bootTime,
	}, nil
}

func (p *windowsProvider) CPUInfo(ctx context.Context) (CPUInfo, error) {
	logical := runtime.NumCPU()
	physical := logical
	modelName := ""
	var clockMHz float64

	out, err := runPowerShell(ctx,
		"(Get-CimInstance Win32_Processor | Select-Object Name,NumberOfCores,MaxClockSpeed | ConvertTo-Csv -NoTypeInformation)")
	if err == nil {
		if rows, perr := parseCIMCsv(out); perr == nil && len(rows) > 0 {
			modelName = strings.TrimSpace(rows[0]["name"])
			if cores := parseIntField(rows[0], "numberofcores"); cores > 0 {
				physical = cores
			}
			clockMHz = float64(parseUintField(rows[0], "maxclockspeed"))
		}
	}

	return CPUInfo{
		ModelName:     modelName,
		PhysicalCores: physical,
		LogicalCores:  logical,
		ClockMHz:      clockMHz,
	}, nil
}

// cpuUsagePerCore returns one snapshot of per-core busy percentages from
// the pre-formatted perf-OS counter class, via the same
// PowerShell/CSV/no-interpolation pattern every other query in this file
// uses. PercentProcessorTime here is already a percentage of ONE core
// (values can reach 100 per core regardless of total core count - see
// https://learn.microsoft.com/windows/win32/cimwin32prov/
// win32-perfformatteddata-perfos-processor), matching Task Manager's
// per-core display and CPUUsage.PerCorePercent's contract.
func (p *windowsProvider) cpuUsagePerCore(ctx context.Context) ([]float64, error) {
	out, err := runPowerShell(ctx,
		"(Get-CimInstance Win32_PerfFormattedData_PerfOS_Processor | Select-Object Name,PercentProcessorTime | ConvertTo-Csv -NoTypeInformation)")
	if err != nil {
		return nil, err
	}
	rows, err := parseCIMCsv(out)
	if err != nil {
		return nil, err
	}

	// The counter set includes a "_Total" instance alongside "0","1",...;
	// numeric instances sort naturally before "_Total" (digits sort below
	// "_" in ASCII) so a plain string sort gives stable core ordering.
	type coreSample struct {
		name    string
		isTotal bool
		percent float64
	}
	var samples []coreSample
	for _, row := range rows {
		name := strings.TrimSpace(row["name"])
		if name == "" {
			continue
		}
		samples = append(samples, coreSample{
			name:    name,
			isTotal: name == "_Total",
			percent: parseFloatField(row, "percentprocessortime"),
		})
	}
	sort.Slice(samples, func(i, j int) bool {
		if samples[i].isTotal != samples[j].isTotal {
			return !samples[i].isTotal
		}
		return samples[i].name < samples[j].name
	})

	perCore := make([]float64, 0, len(samples))
	for _, s := range samples {
		if s.isTotal {
			continue
		}
		perCore = append(perCore, clampPercent(s.percent))
	}
	if len(perCore) == 0 {
		return nil, fmt.Errorf("no per-core samples in processor counter output")
	}
	return perCore, nil
}

func (p *windowsProvider) CPUUsage(ctx context.Context, sampleWindow time.Duration) (CPUUsage, error) {
	// Sample window: CPUUsage's contract lets the caller choose, but the
	// per-core perf counters here are PRE-FORMATTED (each query returns
	// an instantaneous rate, no diffing needed), so the honest window is
	// "whatever the counter's own display interval just measured" - a
	// short sleep only smooths the reading. The aggregate, by contrast,
	// genuinely needs two time-separated GetSystemTimes snapshots, so the
	// caller's window (defaulting to 300ms) is spent between them.
	if sampleWindow <= 0 {
		sampleWindow = 300 * time.Millisecond
	}

	// Per-core counters run concurrently with the aggregate's sampling
	// window: one PowerShell round trip (~100-300ms) hidden inside the
	// same wall-clock window the GetSystemTimes diff uses, so total call
	// latency stays ~sampleWindow rather than window + PowerShell cost.
	type perCoreResult struct {
		cores []float64
		err   error
	}
	perCoreCh := make(chan perCoreResult, 1)
	go func() {
		cores, err := p.cpuUsagePerCore(ctx)
		perCoreCh <- perCoreResult{cores: cores, err: err}
	}()

	before, err := readSystemTimes()
	if err != nil {
		return CPUUsage{}, err
	}
	time.Sleep(sampleWindow)
	after, err := readSystemTimes()
	if err != nil {
		return CPUUsage{}, err
	}

	overall := cpuBusyPercent(before, after)
	pr := <-perCoreCh
	// Per-core data is enrichment: if its query failed (PowerShell
	// hiccup), the aggregate is still real - return it rather than
	// failing the whole reading over the breakdown.
	perCore := pr.cores
	if pr.err != nil || len(perCore) == 0 {
		// Fabricate a single-bucket breakdown from the aggregate rather
		// than an empty one, so a chart never has to special-case null.
		perCore = []float64{overall}
	}

	return CPUUsage{
		OverallPercent: overall,
		PerCorePercent: perCore,
	}, nil
}

func (p *windowsProvider) MemoryInfo(_ context.Context) (MemoryInfo, error) {
	var stat memoryStatusEx
	stat.dwLength = uint32(unsafe.Sizeof(stat))

	ret, _, callErr := procGlobalMemoryStatus.Call(uintptr(unsafe.Pointer(&stat)))
	if ret == 0 {
		return MemoryInfo{}, fmt.Errorf("GlobalMemoryStatusEx: %w", callErr)
	}

	var swapUsed uint64
	if stat.ullTotalPageFile >= stat.ullAvailPageFile {
		swapUsed = stat.ullTotalPageFile - stat.ullAvailPageFile
	}

	return MemoryInfo{
		TotalBytes:     stat.ullTotalPhys,
		UsedBytes:      stat.ullTotalPhys - stat.ullAvailPhys,
		AvailableBytes: stat.ullAvailPhys,
		// Windows doesn't have a direct "swap" concept distinct from its
		// page file, which also backs non-swap commit charge; total/used
		// page file size is the closest equivalent and is what Task
		// Manager's older "Page File" figures were based on.
		SwapTotalBytes: stat.ullTotalPageFile,
		SwapUsedBytes:  swapUsed,
	}, nil
}

func (p *windowsProvider) Disks(ctx context.Context) ([]DiskInfo, error) {
	ret, _, _ := procGetLogicalDrives.Call()
	if ret == 0 {
		return nil, fmt.Errorf("GetLogicalDrives returned no drives")
	}
	bitmask := uint32(ret)

	// Per-volume metadata (label, filesystem, drive type) via one CIM
	// query, keyed by drive letter - much cheaper than one
	// GetVolumeInformationW + GetDriveTypeW pair per letter, and it also
	// supplies the bus type via the underlying physical disk
	// (Win32_DiskDrive.MediaType is "Fixed hard disk media" /
	// "Removable Media" etc.; interface detail like NVMe vs SATA comes
	// from Win32_PnPEntity's bus enumerations, which this query reaches
	// via Win32_LogicalDiskToPartition associations in one shot).
	volumeMeta := p.volumeMetadata(ctx)

	var out []DiskInfo
	for i := 0; i < 26; i++ {
		if bitmask&(1<<uint(i)) == 0 {
			continue
		}
		letter := string(rune('A' + i))
		root := letter + `:\`

		rootPtr, err := syscall.UTF16PtrFromString(root)
		if err != nil {
			continue
		}

		var freeAvail, total, totalFree uint64
		ret, _, _ := procGetDiskFreeSpaceEx.Call(
			uintptr(unsafe.Pointer(rootPtr)),
			uintptr(unsafe.Pointer(&freeAvail)),
			uintptr(unsafe.Pointer(&total)),
			uintptr(unsafe.Pointer(&totalFree)),
		)
		if ret == 0 {
			// Common and expected for an empty optical/card reader drive
			// letter that's present in the bitmask but has no media
			// inserted - skip it rather than failing the whole listing.
			continue
		}

		meta := volumeMeta[letter+":"]
		out = append(out, DiskInfo{
			Device:        root,
			MountPoint:    root,
			Filesystem:    meta.filesystem,
			TotalBytes:    total,
			FreeBytes:     freeAvail,
			UsedBytes:     total - totalFree,
			VolumeName:    meta.label,
			DriveType:     meta.driveType,
			BusType:       meta.busType,
			IsSystemDrive: letter == "C",
		})
	}
	return out, nil
}

// volumeMeta carries the per-volume fields Disks enriches its listing
// with, all from one CIM round trip (see volumeMetadata).
type volumeMeta struct {
	label      string
	filesystem string
	driveType  string
	busType    string
}

// volumeMetadata queries Win32_LogicalDisk (label, filesystem, drive
// type) and enriches fixed disks with the interface type of the
// physical disk they live on (NVMe / SATA / USB via the Win32 disk
// drive PnP bus enumerations), returning a map keyed by "C:"-style
// device IDs. Every field is best-effort: a query failure leaves the
// corresponding field empty rather than failing Disks, which already
// has the size data it exists for.
func (p *windowsProvider) volumeMetadata(ctx context.Context) map[string]volumeMeta {
	meta := make(map[string]volumeMeta)

	out, err := runPowerShell(ctx,
		"(Get-CimInstance Win32_LogicalDisk | Select-Object DeviceID,VolumeName,FileSystem,DriveType | ConvertTo-Csv -NoTypeInformation)")
	if err != nil {
		return meta
	}
	rows, err := parseCIMCsv(out)
	if err != nil {
		return meta
	}
	for _, row := range rows {
		id := strings.TrimSpace(row["deviceid"])
		if id == "" {
			continue
		}
		m := volumeMeta{
			label:      strings.TrimSpace(row["volumename"]),
			filesystem: strings.TrimSpace(row["filesystem"]),
			driveType:  driveTypeLabel(parseIntField(row, "drivetype")),
		}
		meta[id] = m
	}

	// Bus type for fixed disks: associate each logical disk to its
	// physical disk's PnP ID and read the interface from the device ID
	// ("SCSI" disk[n] under an NVMe/SATA controller - the interface
	// itself comes from the hardware IDs; this second query greps them).
	busOut, err := runPowerShell(ctx,
		"(Get-PhysicalDisk | Select-Object DeviceId,BusType | ConvertTo-Csv -NoTypeInformation)")
	if err == nil {
		if rows, err := parseCIMCsv(busOut); err == nil {
			// Get-PhysicalDisk's DeviceId is the physical disk number,
			// which Win32_DiskDrive.Index matches; building the logical→
			// physical mapping needs the association query, so this pass
			// is keyed by disk index for that join below.
			busByIndex := make(map[string]string)
			for _, row := range rows {
				busByIndex[strings.TrimSpace(row["deviceid"])] = strings.TrimSpace(row["bustype"])
			}
			if len(busByIndex) > 0 {
				p.diskBusTypes(ctx, busByIndex, meta)
			}
		}
	}
	return meta
}

// diskBusTypes joins logical disks to physical disks (via
// Win32_LogicalDiskToPartition's Antecedent/Dependent references) and
// fills in m.busType from busByIndex (keyed by Win32_DiskDrive.Index).
// Best-effort: associations can be denied or absent; the field simply
// stays empty.
func (p *windowsProvider) diskBusTypes(ctx context.Context, busByIndex map[string]string, meta map[string]volumeMeta) {
	out, err := runPowerShell(ctx,
		"(Get-CimInstance Win32_LogicalDiskToPartition | Select-Object Antecedent,Dependent | ConvertTo-Csv -NoTypeInformation)")
	if err != nil {
		return
	}
	rows, err := parseCIMCsv(out)
	if err != nil {
		return
	}
	driveIndex := p.physicalDiskIndexes(ctx)
	for _, row := range rows {
		logical := extractCIMRefKey(row["dependent"], `DeviceID=`)
		if logical == "" {
			continue
		}
		diskRef := extractCIMRefKey(row["antecedent"], `DiskIndex=`)
		if diskRef == "" {
			// Partition row references Win32_DiskDrive via its device id
			// (\\.\PHYSICALDRIVE0); map that to the index busByIndex uses.
			driveID := extractCIMRefKey(row["antecedent"], `DeviceID=`)
			idx, ok := driveIndex[strings.ToLower(driveID)]
			if !ok {
				continue
			}
			diskRef = idx
		}
		if bus, ok := busByIndex[diskRef]; ok {
			if m, ok := meta[logical]; ok {
				m.busType = bus
				meta[logical] = m
			}
		}
	}
}

// physicalDiskIndexes maps "\\\\.\\physicaldrive0"-style Win32_DiskDrive
// DeviceIDs to their Index ("0"), the key Get-PhysicalDisk's rows use.
func (p *windowsProvider) physicalDiskIndexes(ctx context.Context) map[string]string {
	idx := make(map[string]string)
	out, err := runPowerShell(ctx,
		"(Get-CimInstance Win32_DiskDrive | Select-Object DeviceID,Index | ConvertTo-Csv -NoTypeInformation)")
	if err != nil {
		return idx
	}
	rows, err := parseCIMCsv(out)
	if err != nil {
		return idx
	}
	for _, row := range rows {
		id := strings.TrimSpace(row["deviceid"])
		if id != "" {
			idx[strings.ToLower(id)] = strings.TrimSpace(row["index"])
		}
	}
	return idx
}

// extractCIMRefKey pulls the value of `key` from a CIM association
// reference string like
// 'Win32_LogicalDisk.DeviceID="C:"' → 'C:"' → trimmed to "C:".
func extractCIMRefKey(ref, key string) string {
	i := strings.Index(ref, key)
	if i < 0 {
		return ""
	}
	rest := ref[i+len(key):]
	rest = strings.Trim(rest, `"`) // trailing quote or semicolon
	if j := strings.IndexAny(rest, `",;`); j >= 0 {
		rest = rest[:j]
	}
	return rest
}

// driveTypeLabel maps Win32_LogicalDisk.DriveType's documented integer
// vocabulary to DiskInfo.DriveType's string vocabulary. Unknown values
// map to "unknown" rather than guessed.
func driveTypeLabel(n int) string {
	switch n {
	case 2:
		return "removable"
	case 3:
		return "internal"
	case 4:
		return "network"
	case 5:
		return "optical"
	case 6:
		return "ramdisk"
	default:
		return "unknown"
	}
}

func (p *windowsProvider) NetworkInterfaces(_ context.Context) ([]NetworkInterface, error) {
	return listNetworkInterfaces()
}

// NetworkIOCounters reads each interface's cumulative byte counters via
// Win32_PerfFormattedData_Tcpip_NetworkInterface (the perfmon "Network
// Interface" object, pre-formatted so no two-sample diffing happens
// here - this returns the raw monotonic counters NetIOCounters's doc
// comment describes; rate computation stays the metrics engine's job).
// Instance names here are the adapter's connection name exactly as the
// Network Connections control panel shows it ("Ethernet", "Wi-Fi"),
// which is the same surface Get-NetAdapter's Name reports - no
// perfmon-specific name mangling to reconcile.
// NetworkIOCounters reads each interface's cumulative byte counters.
// The pre-formatted perfmon class (Win32_PerfFormattedData_Tcpip_
// NetworkInterface) was considered first and rejected: it exposes
// per-second RATES, while NetIOCounters's contract is cumulative
// counters - integrating rates over an assumed cadence would compound
// error into every chart built on them. Get-NetAdapterStatistics
// returns the true monotonic totals instead - see
// networkIOCountersViaAdapterStats below.
func (p *windowsProvider) NetworkIOCounters(ctx context.Context) ([]NetIOCounters, error) {
	return p.networkIOCountersViaAdapterStats(ctx)
}

// networkIOCountersViaAdapterStats is NetworkIOCounters's real
// implementation: Get-NetAdapterStatistics returns cumulative
// SentByteCount/ReceivedByteCount per adapter, exactly the monotonic
// counters NetIOCounters describes (the perf-counter class above was
// tried first and rejected - see its doc comment trail).
func (p *windowsProvider) networkIOCountersViaAdapterStats(ctx context.Context) ([]NetIOCounters, error) {
	out, err := runPowerShell(ctx,
		"(Get-NetAdapterStatistics | Select-Object Name,ReceivedBytes,SentBytes | ConvertTo-Csv -NoTypeInformation)")
	if err != nil {
		return nil, fmt.Errorf("query adapter statistics: %w", err)
	}
	rows, err := parseCIMCsv(out)
	if err != nil {
		return nil, fmt.Errorf("parse adapter statistics output: %w", err)
	}

	out2 := make([]NetIOCounters, 0, len(rows))
	for _, row := range rows {
		name := strings.TrimSpace(row["name"])
		if name == "" {
			continue
		}
		out2 = append(out2, NetIOCounters{
			Name:    name,
			RxBytes: parseUintField(row, "receivedbytes"),
			TxBytes: parseUintField(row, "sentbytes"),
		})
	}
	if len(out2) == 0 {
		return nil, fmt.Errorf("no adapter statistics returned")
	}
	return out2, nil
}

// NetworkConnections reads the TCP socket table via Get-NetTCPConnection
// and UDP endpoints via Get-NetUDPEndpoint - the modern documented
// equivalents of `netstat -ano`, both returning owning OwningProcess PIDs
// directly. Output is requested as CSV and parsed with the same
// fail-safe field parsing as every other CIM query in this file.
func (p *windowsProvider) NetworkConnections(ctx context.Context) ([]NetworkConnection, error) {
	pids := p.processNameIndex(ctx)

	var out []NetworkConnection

	tcpOut, tcpErr := runPowerShell(ctx,
		"(Get-NetTCPConnection | Select-Object LocalAddress,LocalPort,RemoteAddress,RemotePort,State,OwningProcess | ConvertTo-Csv -NoTypeInformation)")
	if tcpErr == nil {
		if rows, perr := parseCIMCsv(tcpOut); perr == nil {
			for _, row := range rows {
				pid := int32(parseUintField(row, "owningprocess"))
				state := strings.TrimSpace(row["state"])
				out = append(out, NetworkConnection{
					Protocol:      "tcp",
					LocalAddress:  strings.TrimSpace(row["localaddress"]),
					LocalPort:     int(parseUintField(row, "localport")),
					RemoteAddress: strings.TrimSpace(row["remoteaddress"]),
					RemotePort:    int(parseUintField(row, "remoteport")),
					State:         tcpStateLabel(state),
					PID:           pid,
					ProcessName:   pids[pid],
				})
			}
		}
	}

	udpOut, udpErr := runPowerShell(ctx,
		"(Get-NetUDPEndpoint | Select-Object LocalAddress,LocalPort,OwningProcess | ConvertTo-Csv -NoTypeInformation)")
	if udpErr == nil {
		if rows, perr := parseCIMCsv(udpOut); perr == nil {
			for _, row := range rows {
				pid := int32(parseUintField(row, "owningprocess"))
				out = append(out, NetworkConnection{
					Protocol:     "udp",
					LocalAddress: strings.TrimSpace(row["localaddress"]),
					LocalPort:    int(parseUintField(row, "localport")),
					PID:          pid,
					ProcessName:  pids[pid],
				})
			}
		}
	}

	if tcpErr != nil && udpErr != nil {
		return nil, fmt.Errorf("query tcp connections: %v; query udp endpoints: %v", tcpErr, udpErr)
	}
	return out, nil
}

// processNameIndex builds one PID→name map shared by every connection
// row - one Win32_Process query instead of a GetOwner-style lookup per
// row (a full socket table is hundreds of rows; per-row queries would
// make NetworkConnections take minutes).
func (p *windowsProvider) processNameIndex(ctx context.Context) map[int32]string {
	idx := make(map[int32]string)
	procs, err := p.ListProcesses(ctx)
	if err != nil {
		return idx // attribution is best-effort; rows just lack names
	}
	for _, proc := range procs {
		idx[proc.PID] = proc.Name
	}
	return idx
}

// tcpStateLabel maps PowerShell's TcpState enum names ("Established",
// "Listen", ...) to the uppercase netstat-style vocabulary
// NetworkConnection.State's doc comment promises ("ESTABLISHED",
// "LISTEN", ...). Unknown values pass through uppercased rather than
// being dropped - a new Windows state name should surface, not vanish.
func tcpStateLabel(state string) string {
	if state == "" {
		return ""
	}
	return strings.ToUpper(state)
}

func (p *windowsProvider) ListProcesses(ctx context.Context) ([]ProcessInfo, error) {
	out, err := runPowerShell(ctx,
		"(Get-CimInstance Win32_Process | Select-Object ProcessId,ParentProcessId,Name,WorkingSetSize | ConvertTo-Csv -NoTypeInformation)")
	if err != nil {
		return nil, fmt.Errorf("query processes: %w", err)
	}

	rows, err := parseCIMCsv(out)
	if err != nil {
		return nil, fmt.Errorf("parse process query output: %w", err)
	}

	result := make([]ProcessInfo, 0, len(rows))
	for _, row := range rows {
		result = append(result, ProcessInfo{
			PID:         int32(parseUintField(row, "processid")),
			PPID:        int32(parseUintField(row, "parentprocessid")),
			Name:        strings.TrimSpace(row["name"]),
			MemoryBytes: parseUintField(row, "workingsetsize"),
			// Status: Win32_Process doesn't expose a simple running/
			// sleeping/stopped vocabulary the way Unix process states do
			// (a Windows process is either running or gone); left as the
			// zero value rather than a fabricated label.
			//
			// User, Threads, StartedAt: each needs its own targeted CIM
			// property/query (GetOwner, ThreadCount, CreationDate)
			// omitted here to keep this one query fast across a full
			// process list - ProcessDetail below fetches them for one
			// process at a time instead, where the cost is negligible.
		})
	}
	// Task-manager-grade enrichment: one extra bulk perf-counter query
	// fills CPU% (whole-machine share) and disk I/O rate for every row.
	// Failure is soft - a nil-field row beats a failed listing.
	p.enrichProcessList(ctx, result)
	return result, nil
}

// numLogicalCores is the whole-machine denominator the per-process CPU
// counters need (see process_counters_windows.go). runtime.NumCPU is
// the logical processor count Windows reports to this process, which is
// exactly the counter convention's divisor.
func numLogicalCores() int { return runtime.NumCPU() }

func (p *windowsProvider) ProcessDetail(ctx context.Context, pid int32, _ time.Duration) (ProcessDetail, error) {
	script := fmt.Sprintf(
		`(Get-CimInstance Win32_Process -Filter "ProcessId=%d" | Select-Object ProcessId,ParentProcessId,Name,ExecutablePath,CommandLine,WorkingSetSize,ThreadCount,CreationDate | ConvertTo-Csv -NoTypeInformation)`,
		pid,
	)
	out, err := runPowerShell(ctx, script)
	if err != nil {
		return ProcessDetail{}, fmt.Errorf("query process %d: %w", pid, err)
	}
	rows, err := parseCIMCsv(out)
	if err != nil {
		return ProcessDetail{}, fmt.Errorf("parse process query output: %w", err)
	}
	if len(rows) == 0 {
		return ProcessDetail{}, fmt.Errorf("process %d: %w", pid, ErrNotFound)
	}
	row := rows[0]

	var startedAt time.Time
	if raw := strings.TrimSpace(row["creationdate"]); raw != "" {
		if t, err := parseWMIDateTime(raw); err == nil {
			startedAt = t
		}
	}

	// Windows doesn't have a Unix-style argv array - CommandLine is
	// already the one literal string the OS itself uses, and splitting
	// it on whitespace would break the extremely common case of a quoted
	// path containing spaces (e.g. "C:\Program Files\..."). Keeping it as
	// a single element preserves that fidelity rather than guessing at a
	// tokenization.
	var cmdLine []string
	if raw := strings.TrimSpace(row["commandline"]); raw != "" {
		cmdLine = []string{raw}
	}

	// ThreadCount comes from the same Win32_Process object the query
	// above already fetched - add ThreadCount to the property list so a
	// detail view shows it (the list view deliberately omits it: see
	// ListProcesses' comment on keeping that query fast).

	return ProcessDetail{
		ProcessInfo: ProcessInfo{
			PID:         int32(parseUintField(row, "processid")),
			PPID:        int32(parseUintField(row, "parentprocessid")),
			Name:        strings.TrimSpace(row["name"]),
			StartedAt:   startedAt,
			MemoryBytes: parseUintField(row, "workingsetsize"),
			Threads:     int(parseUintField(row, "threadcount")),
		},
		Executable:  strings.TrimSpace(row["executablepath"]),
		CommandLine: cmdLine,
		// CPUPercent: Win32_Process has no %CPU property, but the
		// pre-formatted process perf-counter class does - one extra
		// targeted query for this one process (cheap here where the cost
		// is per-detail-view, unlike the full listing where it would be
		// per-row; see ListProcesses' comment). PercentProcessorTime is
		// "percent of ONE processor" in Windows' counter convention, so
		// it's converted to the whole-machine share the UI shows.
		CPUPercent: wholeMachineCPUShare(p.processCPUPercent(ctx, pid)),
		// OwnerName: a GetOwner call is per-process too expensive for a
		// full listing but right at detail time (Task Manager's
		// "User name" column); best-effort - system/protected processes
		// deny it and the field stays empty.
		OwnerName: p.processOwner(ctx, pid),
	}, nil
}

// processOwner resolves the owning user of one process via
// Win32_Process GetOwner (Task Manager's "User name" column is the
// same attribution). One PowerShell call returns "domain\\user";
// empty on any denial - system/protected processes deny the owner
// query - never an error, since the detail payload stands without it.
func (p *windowsProvider) processOwner(ctx context.Context, pid int32) string {
	script := fmt.Sprintf(`$p = Get-CimInstance Win32_Process -Filter 'ProcessId=%d'; if ($p) { $o = Invoke-CimMethod -InputObject $p -MethodName GetOwner; if ($o.User) { if ($o.Domain) { "$($o.Domain)\$($o.User)" } else { $o.User } } }`, pid)
	out, err := runPowerShell(ctx, script)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(out)
}

// processCPUPercent reads one process's pre-formatted CPU percentage
// from Win32_PerfFormattedData_PerfProc_Process (IDProcess is the
// documented PID property). Best-effort: 0 when the counter instance
// is missing (the process just exited - a legitimate race) rather than
// an error, since the rest of the detail payload is already valid.
func (p *windowsProvider) processCPUPercent(ctx context.Context, pid int32) float64 {
	// The filter is an integer interpolated into WQL - no injection
	// surface (only digits can come from pid, which the handler above
	// has already validated).
	out, err := runPowerShell(ctx,
		fmt.Sprintf("(Get-CimInstance Win32_PerfFormattedData_PerfProc_Process -Filter 'IDProcess=%d' | Select-Object PercentProcessorTime | ConvertTo-Csv -NoTypeInformation)", pid))
	if err != nil {
		return 0
	}
	rows, err := parseCIMCsv(out)
	if err != nil || len(rows) == 0 {
		return 0
	}
	return clampPercent(parseFloatField(rows[0], "percentprocessortime"))
}

func (p *windowsProvider) TerminateProcess(_ context.Context, pid int32, force bool) error {
	if !force {
		// Windows has no general-purpose equivalent to SIGTERM. The
		// closest analog, Signal(os.Interrupt), sends a console control
		// event that only reaches processes sharing the caller's console
		// and configured to handle it - not the reliable "ask nicely,
		// wait, then escalate" primitive SIGTERM is on Unix. Rather than
		// silently behave like force=true, this is surfaced honestly as
		// unavailable - see spec section 4 and provider.go's
		// TerminateProcess doc comment.
		return fmt.Errorf("graceful termination on Windows: %w", ErrUnavailable)
	}

	// os.FindProcess + Process.Kill are standard-library Go, which
	// internally wraps TerminateProcess safely - no raw struct-layout
	// syscall needed here, unlike MemoryInfo above.
	proc, err := os.FindProcess(int(pid))
	if err != nil {
		return fmt.Errorf("find process %d: %w", pid, err)
	}
	if err := proc.Kill(); err != nil {
		if errors.Is(err, os.ErrProcessDone) {
			return fmt.Errorf("process %d: %w", pid, ErrNotFound)
		}
		return fmt.Errorf("terminate process %d: %w", pid, err)
	}
	return nil
}

// escapePowerShellSingleQuoted escapes a value for embedding inside a
// PowerShell single-quoted string literal, where the only special
// character is the quote itself, escaped by doubling it. Used for every
// service name interpolated into a script below - service names are
// data, never trusted to be shell-safe on their own.
func escapePowerShellSingleQuoted(s string) string {
	return strings.ReplaceAll(s, "'", "''")
}

func normalizeWin32ServiceState(state string) string {
	switch state {
	case "Running":
		return "running"
	case "Stopped":
		return "stopped"
	default:
		return "unknown"
	}
}

func normalizeWin32ServiceStartMode(startMode string) string {
	switch startMode {
	case "Auto":
		return "enabled"
	case "Manual":
		return "manual"
	case "Disabled":
		return "disabled"
	default:
		return "unknown"
	}
}

func serviceInfoFromCIMRow(row map[string]string) ServiceInfo {
	return ServiceInfo{
		Name:        strings.TrimSpace(row["name"]),
		DisplayName: strings.TrimSpace(row["displayname"]),
		Description: strings.TrimSpace(row["description"]),
		State:       normalizeWin32ServiceState(strings.TrimSpace(row["state"])),
		StartupMode: normalizeWin32ServiceStartMode(strings.TrimSpace(row["startmode"])),
		PID:         int32(parseUintField(row, "processid")),
	}
}

func (p *windowsProvider) ListServices(ctx context.Context) ([]ServiceInfo, error) {
	out, err := runPowerShell(ctx,
		"(Get-CimInstance Win32_Service | Select-Object Name,DisplayName,Description,State,StartMode,ProcessId | ConvertTo-Csv -NoTypeInformation)")
	if err != nil {
		return nil, fmt.Errorf("query services: %w", err)
	}
	rows, err := parseCIMCsv(out)
	if err != nil {
		return nil, fmt.Errorf("parse service query output: %w", err)
	}

	result := make([]ServiceInfo, 0, len(rows))
	for _, row := range rows {
		result = append(result, serviceInfoFromCIMRow(row))
	}
	return result, nil
}

// ServiceDetail deliberately does not build a WQL `-Filter` clause with
// the service name interpolated into it (an earlier version did, and
// it was a real command-injection bug, not a hypothetical one - see
// SECURITY.md and CHANGELOG.md's Phase 8 entry for the full account).
// The problem wasn't that the name went unescaped: it was escaped with
// escapePowerShellSingleQuoted, which is exactly correct for a
// PowerShell *single-quoted* string - but the filter was embedded in a
// *double-quoted* one (`-Filter "Name='%s'"`), and PowerShell
// interpolates `$(...)` subexpressions inside double-quoted strings
// regardless of what's nested inside them. A service name containing
// `$(...)` would have PowerShell evaluate that subexpression while
// parsing the script, before -Filter ever saw the value - full command
// execution as whatever Weloury core is running as, reachable by any
// authenticated IPC caller via service.detail's `name` parameter,
// which is validated only for non-emptiness (see
// internal/app/methods_service.go).
//
// Fixing the escaping in place was considered and rejected: it would
// need a second, double-quote-aware escape function layered under the
// existing WQL single-quote escaping, and getting nested-quoting-context
// escaping right by hand is exactly the kind of thing that's easy to
// get subtly wrong again later. Removing the injection surface
// entirely is more robust than patching it: this reuses the identical,
// already-argument-free CIM query ListServices runs (no name, no
// -Filter, no interpolation of any kind) and matches the requested
// name in Go instead of in WQL. Every field ServiceDetail exposes
// comes from that same query's columns; Dependencies is left empty
// either way (see the comment below), so nothing is lost.
func (p *windowsProvider) ServiceDetail(ctx context.Context, name string) (ServiceDetail, error) {
	services, err := p.ListServices(ctx)
	if err != nil {
		return ServiceDetail{}, fmt.Errorf("query service %s: %w", name, err)
	}
	for _, svc := range services {
		if svc.Name == name {
			return ServiceDetail{
				ServiceInfo: svc,
				// Dependencies: Win32_Service doesn't expose these via a
				// simple property select (it requires a separate WMI
				// associator query, e.g. against Win32_DependentService) -
				// left empty, a documented gap matching Darwin's for the
				// same "not worth the added risk without a real machine to
				// verify against" reasoning.
			}, nil
		}
	}
	return ServiceDetail{}, fmt.Errorf("service %s: %w", name, ErrNotFound)
}

func (p *windowsProvider) StartService(ctx context.Context, name string) error {
	script := fmt.Sprintf("Start-Service -Name '%s'", escapePowerShellSingleQuoted(name))
	if _, err := runPowerShell(ctx, script); err != nil {
		return fmt.Errorf("service %s: %w", name, err)
	}
	return nil
}

func (p *windowsProvider) StopService(ctx context.Context, name string) error {
	script := fmt.Sprintf("Stop-Service -Name '%s'", escapePowerShellSingleQuoted(name))
	if _, err := runPowerShell(ctx, script); err != nil {
		return fmt.Errorf("service %s: %w", name, err)
	}
	return nil
}

func (p *windowsProvider) RestartService(ctx context.Context, name string) error {
	script := fmt.Sprintf("Restart-Service -Name '%s'", escapePowerShellSingleQuoted(name))
	if _, err := runPowerShell(ctx, script); err != nil {
		return fmt.Errorf("service %s: %w", name, err)
	}
	return nil
}

func (p *windowsProvider) SetServiceEnabled(ctx context.Context, name string, enabled bool) error {
	startupType := "Disabled"
	if enabled {
		startupType = "Automatic"
	}
	script := fmt.Sprintf("Set-Service -Name '%s' -StartupType %s", escapePowerShellSingleQuoted(name), startupType)
	if _, err := runPowerShell(ctx, script); err != nil {
		return fmt.Errorf("service %s: %w", name, err)
	}
	return nil
}
