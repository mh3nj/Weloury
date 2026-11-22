package system

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"weloury/internal/hidewin"
)

// GPU telemetry via nvidia-smi - the ONE telemetry source that is
// identical across Windows, Linux, and macOS (the driver ships the same
// binary everywhere), which is why this file carries no build tag.
// nvidia-smi's field-query interface (`--query-gpu=...` with
// `--format=csv,noheader,nounits`) is a stable, documented,
// machine-parsable surface, unlike the ad-hoc human table - every field
// Weloury reads here is one the driver documents. It is also what Task
// Manager itself reads on Windows for NVIDIA adapters, so agreeing with
// it is the goal (see user-visible principle: match Task Manager).
//
// Platform files call sampleNvidiaGPUs: Windows leads with the WMI/CSV
// inventory and falls back to this for telemetry (its
// Win32_PerfFormattedData_GPUEngine class is absent on many current
// driver builds - verified "Invalid class" on real hardware); Linux and
// macOS lead with this outright for NVIDIA hardware and keep their
// lspci/system_profiler paths for everything else.

// resolveCommand finds name's full path, tolerating a missing binary as a
// nil path (not an error): callers treat nil as "this source isn't
// installed" and fall through to their next source. It checks the running
// executable's own directory first, because the desktop build ships helper
// binaries (smartctl) bundled right beside weloury-core, then falls back to
// the PATH lookup for a system-installed copy.
func resolveCommand(name string) *string {
	if dir, err := os.Executable(); err == nil {
		for _, candidate := range []string{name, name + ".exe"} {
			p := filepath.Join(filepath.Dir(dir), candidate)
			if st, err := os.Stat(p); err == nil && !st.IsDir() {
				return &p
			}
		}
	}
	path, err := exec.LookPath(name)
	if err != nil {
		return nil
	}
	return &path
}

// runNvidiaSMI queries the field-query interface for one field list.
// cmd.Output() (never CombinedOutput) so driver warnings on stderr can't
// corrupt the CSV; hidewin.Configure keeps the console window invisible
// on Windows. A short timeout bounds a wedged driver call - this runs on
// UI poll cadences.
func runNvidiaSMI(ctx context.Context, exe string, fields string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, exe,
		fmt.Sprintf("--query-gpu=%s", fields),
		"--format=csv,noheader,nounits",
	)
	hidewin.Configure(cmd)
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("nvidia-smi: %w", err)
	}
	return string(out), nil
}

// parseFloatString / parseUintString are strconv-only variants of the
// windows_parse.go field parsers, for sources (like nvidia-smi CSV)
// that hand back bare strings rather than map rows. Zero - not an
// error - for absent/malformed values, matching the same
// "missing optional field shouldn't fail the row" convention.
func parseFloatString(s string) float64 {
	v, err := strconv.ParseFloat(strings.TrimSpace(s), 64)
	if err != nil {
		return 0
	}
	return v
}

func parseUintString(s string) uint64 {
	v, err := strconv.ParseUint(strings.TrimSpace(s), 10, 64)
	if err != nil {
		return 0
	}
	return v
}

// parseNvidiaSMIInventory parses the name,driver_version,pci.bus_id
// field set (index is implicit in row order and added by the caller).
func parseNvidiaSMIInventory(out string) []GPUInfo {
	var gpus []GPUInfo
	for _, line := range strings.Split(out, "\n") {
		f := strings.Split(strings.TrimSpace(line), ", ")
		if len(f) < 3 || f[0] == "" {
			continue
		}
		gpus = append(gpus, GPUInfo{
			Name:          f[0],
			Vendor:        "NVIDIA",
			DriverVersion: f[1],
			// PCIID is filled by the pci.vendor_id/pci.device_id query in
			// sampleNvidiaGPUs - the bus id alone doesn't carry the pair.
			PCIID: "",
		})
	}
	return gpus
}

// parseNvidiaPCIPair resolves nvidia-smi's "00000000:01:00.0" PCI bus id
// to the "vendor:device" hex pair GPUInfo.PCIID documents. nvidia-smi's
// bus id does NOT carry the vendor/device ids, so the caller passes the
// pci.vendor_id / pci.device_id field values instead; this only joins
// and validates them. Empty result for anything malformed - PCIID is
// optional metadata, never worth failing an inventory over.
func parseNvidiaPCIPair(vendorID, deviceID string) string {
	clean := func(s string) string {
		s = strings.ToLower(strings.TrimSpace(s))
		s = strings.TrimPrefix(s, "0x")
		if len(s) != 4 {
			return ""
		}
		for _, c := range s {
			if !(c >= '0' && c <= '9') && !(c >= 'a' && c <= 'f') {
				return ""
			}
		}
		return s
	}
	v, d := clean(vendorID), clean(deviceID)
	if v == "" || d == "" {
		return ""
	}
	return v + ":" + d
}

// parseNvidiaSMIUsage parses one CSV row from gpuSmiFieldsUsage's field
// order (keep the two in sync).
func parseNvidiaSMIUsage(row string) GPUUsage {
	var u GPUUsage
	f := strings.Split(strings.TrimSpace(row), ", ")
	if len(f) < 7 {
		return u
	}
	u.UtilizationPercent = new(float64)
	*u.UtilizationPercent = clampFloatPercent(parseFloatString(f[1]))
	u.MemoryUsedBytes = parseUintString(f[2]) * 1024 * 1024 // MiB → bytes
	u.MemoryTotalBytes = parseUintString(f[3]) * 1024 * 1024
	u.TemperatureCelsius = parseFloatString(f[4])
	u.FanPercent = clampFloatPercent(parseFloatString(f[5]))
	u.PowerWatts = parseFloatString(f[6])
	if len(f) >= 9 {
		u.PowerLimitWatts = parseFloatString(f[7])
		u.ClockMHz = parseUintString(f[8])
	}
	return u
}

// gpuSmiFieldsUsage is the exact --query-gpu field list
// parseNvidiaSMIUsage reads, in order.
const gpuSmiFieldsUsage = "name,utilization.gpu,memory.used,memory.total,temperature.gpu,fan.speed,power.draw,power.limit,clocks.current.graphics"

// sampleNvidiaGPUs is the cross-platform telemetry path: one inventory
// query plus one usage query for ALL NVIDIA adapters at once. Returns
// zero GPUs - not an error - when the binary isn't installed or errors,
// so callers can distinguish "no NVIDIA hardware" (fall through to the
// next source) from a per-call failure they'd report themselves.
func sampleNvidiaGPUs(ctx context.Context) ([]GPUInfo, []GPUUsage) {
	exe := resolveCommand("nvidia-smi")
	if exe == nil {
		return nil, nil
	}
	invOut, err := runNvidiaSMI(ctx, *exe, "name,driver_version,pci.bus_id")
	if err != nil {
		return nil, nil
	}
	gpus := parseNvidiaSMIInventory(invOut)
	if len(gpus) == 0 {
		return nil, nil
	}
	// PCI vendor/device ids come as separate fields; a second tiny query
	// joins them onto the inventory (best-effort - an empty PCIID is fine).
	if pciOut, err := runNvidiaSMI(ctx, *exe, "pci.vendor_id,pci.device_id"); err == nil {
		lines := strings.Split(strings.TrimSpace(pciOut), "\n")
		for i := range gpus {
			if i >= len(lines) {
				break
			}
			if pair := strings.Split(strings.TrimSpace(lines[i]), ", "); len(pair) >= 2 {
				gpus[i].PCIID = parseNvidiaPCIPair(pair[0], pair[1])
			}
		}
	}
	usageOut, err := runNvidiaSMI(ctx, *exe, gpuSmiFieldsUsage)
	if err != nil {
		return gpus, nil
	}
	var usages []GPUUsage
	for _, line := range strings.Split(usageOut, "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		usages = append(usages, parseNvidiaSMIUsage(line))
	}
	return gpus, usages
}
