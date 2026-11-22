//go:build windows

package system

import (
	"context"
	"fmt"
	"strings"
)

// GPU support on Windows targets only documented, stable surfaces:
//
//   - Inventory: Win32_VideoController (present since Windows 2000; the
//     same class Device Manager reads) - name, driver, VRAM, PCI IDs.
//   - Utilization: Win32_PerfFormattedData_GPUEngine's
//     PercentEngineUtilization... except a GPU has many engine nodes
//     (3D, Copy, VideoDecode, ...), whose percentages are per-engine
//     and don't sum to a card total. Task Manager's "GPU" number is
//     actually the max across engines. This implementation follows that
//     semantics: the max engine utilization across a card's engines.
//   - Memory: Win32_PerfFormattedData_GPUPerformanceCounters
//     DedicatedUsage (current dedicated-memory bytes in flight).
//   - Temperature/fan: only via vendor WMI namespaces (NVIDIA's
//     root\CIMV2\NVGPU or AMD's ATiSMART), which exist on machines with
//     those vendors' drivers. Best-effort with per-vendor probes; when
//     absent the fields stay zero and the UI shows "not reported".

func (p *windowsProvider) ListGPUs(ctx context.Context) ([]GPUInfo, error) {
	out, err := runPowerShell(ctx,
		"(Get-CimInstance Win32_VideoController | Select-Object Name,AdapterCompatibility,DriverVersion,AdapterRAM,PNPDeviceID | ConvertTo-Csv -NoTypeInformation)")
	if err != nil {
		return nil, fmt.Errorf("query video controllers: %w", err)
	}
	rows, err := parseCIMCsv(out)
	if err != nil {
		return nil, fmt.Errorf("parse video controller output: %w", err)
	}

	gpus := make([]GPUInfo, 0, len(rows))
	for _, row := range rows {
		name := strings.TrimSpace(row["name"])
		if name == "" {
			continue
		}
		// Win32_VideoController.AdapterRAM is a uint32 (documented
		// quirk): it caps at 4 GiB and wraps for larger cards. Treat a
		// suspiciously exact wrap value as unknown rather than reporting
		// a wrong number; the perf-counter memory path reports real
		// usage against GPUUsage.MemoryTotalBytes where the OS does
		// expose an accurate total.
		totalRAM := parseUintField(row, "adapterram")
		const uint32Wrap = 4 * 1024 * 1024 * 1024
		if totalRAM == uint32Wrap {
			totalRAM = 0
		}
		gpus = append(gpus, GPUInfo{
			Name:             name,
			Vendor:           strings.TrimSpace(row["adaptercompatibility"]),
			DriverVersion:    strings.TrimSpace(row["driverversion"]),
			TotalMemoryBytes: totalRAM,
			PCIID:            parsePCIFromPNPID(strings.TrimSpace(row["pnpdeviceid"])),
			Index:            len(gpus),
		})
	}
	// Fallback inventory: Win32_VideoController is present on every
	// Windows machine in principle, but driver-wipe states and locked-down
	// SKUs can return zero rows; nvidia-smi still answers there.
	if len(gpus) == 0 {
		if nvidiaGPUs, _ := sampleNvidiaGPUs(ctx); len(nvidiaGPUs) > 0 {
			return nvidiaGPUs, nil
		}
	}
	return gpus, nil
}

// parsePCIFromPNPID extracts the "vendor:device" pair from a Win32
// PNPDeviceID like "PCI\\VEN_10DE&DEV_2484&..." → "10de:2484". Empty for
// non-PCI adapters (basic display adapters report a root-enumerated ID).
func parsePCIFromPNPID(pnpID string) string {
	upper := strings.ToUpper(pnpID)
	if !strings.HasPrefix(upper, "PCI\\") {
		return ""
	}
	const venKey, devKey = "VEN_", "DEV_"
	vi := strings.Index(upper, venKey)
	di := strings.Index(upper, devKey)
	if vi < 0 || di < 0 {
		return ""
	}
	vendor := vendorHex(upper[vi+len(venKey):])
	device := vendorHex(upper[di+len(devKey):])
	if vendor == "" || device == "" {
		return ""
	}
	return strings.ToLower(vendor + ":" + device)
}

func vendorHex(s string) string {
	if len(s) < 4 {
		return ""
	}
	for _, c := range s[:4] {
		isHex := (c >= '0' && c <= '9') || (c >= 'A' && c <= 'F')
		if !isHex {
			return ""
		}
	}
	return s[:4]
}

// gpuEngineRow is one engine node from the GPU engine counter class:
// an instance name like "pid_1234_luid_0x00000000_0x0000C871_phys_0_eng_3D"
// plus a pre-formatted utilization percentage.
type gpuEngineRow struct {
	luid    string
	eng     string // "eng_3D", "eng_VideoDecode", ...
	phys    string
	percent float64
}

// parseEngineInstance splits a GPUEngine instance name into its parts.
// The luid_..._phys_N segment identifies the adapter (LUID = locally
// unique ID, the same value IDXGIFactory reports); eng_X identifies the
// engine node. Unparseable names (new Windows versions could add
// shapes) return ok=false and are skipped rather than mis-grouped.
func parseEngineInstance(name string) (gpuEngineRow, bool) {
	row := gpuEngineRow{}
	const (
		luidKey = "luid_"
		physKey = "_phys_"
		engKey  = "_eng_"
	)
	li := strings.Index(name, luidKey)
	if li < 0 {
		return row, false
	}
	rest := name[li+len(luidKey):]
	pi := strings.Index(rest, physKey)
	if pi < 0 {
		return row, false
	}
	row.luid = rest[:pi]
	rest = rest[pi+len(physKey):]
	ei := strings.Index(rest, engKey)
	if ei < 0 {
		return row, false
	}
	row.phys = rest[:ei]
	row.eng = rest[ei+len(engKey):]
	return row, true
}

// GPUUsage samples one adapter's live telemetry. nvidia-smi leads for
// NVIDIA adapters (see gpu_nvidia.go - it's what Task Manager itself
// reads, and it always carries utilization/memory/temperature/fan/power,
// while the WMI GPUEngine class the original implementation depended on
// is absent on many current driver builds - verified "Invalid class" on
// real hardware, which is exactly why the GPU page used to show nothing
// at all); the WMI path handles everything else (Intel/AMD/other) and
// serves as NVIDIA's fallback when nvidia-smi is absent.
func (p *windowsProvider) GPUUsage(ctx context.Context, index int) (GPUUsage, error) {
	gpus, err := p.ListGPUs(ctx)
	if err != nil {
		return GPUUsage{}, err
	}
	if index < 0 || index >= len(gpus) {
		return GPUUsage{}, fmt.Errorf("gpu %d: %w", index, ErrNotFound)
	}
	target := gpus[index]

	if strings.Contains(strings.ToLower(target.Vendor), "nvidia") {
		if usage, ok := p.nvidiaUsageFor(ctx, index, target, gpus); ok {
			return usage, nil
		}
	}
	return p.gpuUsageViaWMI(ctx, index, target)
}

// nvidiaUsageFor matches the target adapter to its nvidia-smi row - by
// exact name first (Win32_VideoController's Name and nvidia-smi's name
// agree on real hardware, e.g. "NVIDIA GeForce GTX 1650"), else by
// counting NVIDIA-vendored adapters up to index (the only ordering rule
// that holds when both lists cover the same adapters in the same
// order). ok=false means nvidia-smi is absent or returned no usable
// sample for this adapter - the caller falls through to the WMI path.
func (p *windowsProvider) nvidiaUsageFor(ctx context.Context, index int, target GPUInfo, gpus []GPUInfo) (GPUUsage, bool) {
	nvGpus, nvUsages := sampleNvidiaGPUs(ctx)
	pos := -1
	for i, g := range nvGpus {
		if strings.EqualFold(g.Name, target.Name) {
			pos = i
			break
		}
	}
	if pos < 0 {
		n := 0
		for i := 0; i < len(gpus) && i <= index; i++ {
			if !strings.Contains(strings.ToLower(gpus[i].Vendor), "nvidia") {
				continue
			}
			if i == index {
				pos = n
				break
			}
			n++
		}
	}
	if pos < 0 || pos >= len(nvUsages) {
		return GPUUsage{}, false
	}
	usage := nvUsages[pos]
	if usage.MemoryTotalBytes == 0 {
		usage.MemoryTotalBytes = target.TotalMemoryBytes
	}
	return usage, true
}

// gpuUsageViaWMI is the pre-nvidia-smi Windows telemetry path, kept for
// every non-NVIDIA adapter (and as NVIDIA's fallback): the pre-formatted
// GPUEngine perf-counter class for utilization (max across a card's
// engine nodes = Task Manager's headline "GPU" number), the
// process-memory counter class for dedicated bytes, and vendor WMI
// namespaces for temperature/fan. Every probe is soft: the GPUEngine
// class being absent ("Invalid class" on many current driver builds)
// leaves utilization nil - honest "not reported" - rather than failing
// the whole call the way the original implementation did.
func (p *windowsProvider) gpuUsageViaWMI(ctx context.Context, index int, target GPUInfo) (GPUUsage, error) {
	usage := GPUUsage{
		MemoryTotalBytes: target.TotalMemoryBytes,
		MemoryUsedBytes:  p.gpuDedicatedMemory(ctx, index),
	}

	var engineRows []gpuEngineRow
	if out, err := runPowerShell(ctx,
		"(Get-CimInstance Win32_PerfFormattedData_GPUEngine | Select-Object Name,PercentEngineUtilization | ConvertTo-Csv -NoTypeInformation)"); err == nil {
		if rows, perr := parseCIMCsv(out); perr == nil {
			// The engine instance name carries no PCI ID, so adapters are
			// matched by order: engines are grouped by LUID, and the Nth
			// distinct LUID corresponds to the Nth Win32_VideoController
			// entry on every machine shape this class documents. A LUID
			// that maps to no adapter (e.g. a paravirtualized one) is
			// skipped, keeping indices aligned with ListGPUs' output.
			for _, row := range rows {
				inst := strings.TrimSpace(row["name"])
				if inst == "" {
					continue
				}
				if er, ok := parseEngineInstance(inst); ok {
					er.percent = parseFloatField(row, "percentengineutilization")
					engineRows = append(engineRows, er)
				}
			}
		}
	}

	// Distinct LUIDs in first-seen order = adapter order.
	luidOrder := make([]string, 0, 2)
	luidSet := make(map[string]bool)
	for _, er := range engineRows {
		if !luidSet[er.luid] {
			luidSet[er.luid] = true
			luidOrder = append(luidOrder, er.luid)
		}
	}
	if index < len(luidOrder) {
		cardLUID := luidOrder[index]
		maxUtil := 0.0
		for _, er := range engineRows {
			if er.luid != cardLUID {
				continue
			}
			if er.percent > maxUtil {
				maxUtil = er.percent
			}
		}
		pct := clampPercent(maxUtil)
		usage.UtilizationPercent = &pct
	}

	// Vendor temperature probes, best-effort: NVIDIA first, then AMD.
	// Neither being present leaves TemperatureCelsius at zero, which
	// the UI renders as "not reported".
	if temp, err := p.nvidiaTemperature(ctx); err == nil && temp > 0 {
		usage.TemperatureCelsius = temp
	} else if temp, err := p.amdTemperature(ctx); err == nil && temp > 0 {
		usage.TemperatureCelsius = temp
	}
	if fan, err := p.nvidiaFanPercent(ctx); err == nil && fan > 0 {
		usage.FanPercent = fan
	} else if fan, err := p.amdFanPercent(ctx); err == nil && fan > 0 {
		usage.FanPercent = fan
	}

	return usage, nil
}

// gpuDedicatedMemory reads the GPU process-memory counter class for the
// index'th adapter's dedicated-memory bytes in use. Best-effort: zero
// when the class is absent (older Windows) or the adapter index is out
// of the counter set's range.
func (p *windowsProvider) gpuDedicatedMemory(ctx context.Context, index int) uint64 {
	out, err := runPowerShell(ctx,
		"(Get-CimInstance Win32_PerfFormattedData_GPUPerformanceCounters_GPUProcessMemory | Select-Object Name,DedicatedUsage | ConvertTo-Csv -NoTypeInformation)")
	if err != nil {
		return 0
	}
	rows, err := parseCIMCsv(out)
	if err != nil {
		return 0
	}

	// Instance names are "pid_PID_luid_0x..._phys_N" - group by the same
	// LUID-order rule GPUUsage uses, then sum across all processes on
	// the card (many processes share it; the counter is per-process).
	luidOrder := make([]string, 0, 2)
	luidSet := make(map[string]bool)
	sums := make(map[string]uint64)
	for _, row := range rows {
		inst := strings.TrimSpace(row["name"])
		if inst == "" {
			continue
		}
		er, ok := parseEngineInstance(inst)
		if !ok {
			continue
		}
		if !luidSet[er.luid] {
			luidSet[er.luid] = true
			luidOrder = append(luidOrder, er.luid)
		}
		sums[er.luid] += parseUintField(row, "dedicatedusage")
	}
	if index >= len(luidOrder) {
		return 0
	}
	return sums[luidOrder[index]]
}

// nvidiaTemperature probes NVIDIA's driver WMI namespace
// (root\CIMV2\NVGPU, present on machines with NVIDIA drivers installed)
// for the first GPU's current temperature and fan state. Both probes are
// independent: a driver exposing one but not the other still reports
// what it has.
func (p *windowsProvider) nvidiaTemperature(ctx context.Context) (float64, error) {
	return p.querySingleFloat(ctx,
		"(Get-CimInstance -Namespace root/CIMV2/NVGPU -ClassName NVIDIATemperatureNVA0 2>$null | Select-Object -First 1 CurrentTemp | ConvertTo-Csv -NoTypeInformation)",
		"currenttemp")
}

func (p *windowsProvider) nvidiaFanPercent(ctx context.Context) (float64, error) {
	return p.querySingleFloat(ctx,
		"(Get-CimInstance -Namespace root/CIMV2/NVGPU -ClassName NVIDIAPerfFanSpeedsNVA0 2>$null | Select-Object -First 1 DesiredFanSpeedInPercent | ConvertTo-Csv -NoTypeInformation)",
		"desiredfanspeedinpercent")
}

// amdTemperature probes AMD's ADLX/ADL driver WMI namespace
// (root\CIMV2\ATiSMART) - present on machines with AMD drivers -
// first GPU's temperature and fan duty cycle.
func (p *windowsProvider) amdTemperature(ctx context.Context) (float64, error) {
	return p.querySingleFloat(ctx,
		"(Get-CimInstance -Namespace root/CIMV2/ATiSMART -ClassName ATIGPU_Temperature 2>$null | Select-Object -First 1 Temperature | ConvertTo-Csv -NoTypeInformation)",
		"temperature")
}

func (p *windowsProvider) amdFanPercent(ctx context.Context) (float64, error) {
	return p.querySingleFloat(ctx,
		"(Get-CimInstance -Namespace root/CIMV2/ATiSMART -ClassName ATIGPU_FanSpeed 2>$null | Select-Object -First 1 Percent | ConvertTo-Csv -NoTypeInformation)",
		"percent")
}

// querySingleFloat runs one PowerShell query expected to yield a CSV
// with exactly one data row and one numeric column of the given name;
// any absence/failure is an error the caller treats as "not reported".
func (p *windowsProvider) querySingleFloat(ctx context.Context, script, field string) (float64, error) {
	out, err := runPowerShell(ctx, script)
	if err != nil {
		return 0, err
	}
	rows, err := parseCIMCsv(out)
	if err != nil || len(rows) == 0 {
		return 0, fmt.Errorf("no %s row returned", field)
	}
	v := parseFloatField(rows[0], field)
	if v == 0 {
		return 0, fmt.Errorf("%s not reported", field)
	}
	return v, nil
}
