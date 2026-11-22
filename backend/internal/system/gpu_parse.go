//go:build !windows

package system

import (
	"encoding/json"
	"regexp"
	"strings"
)

// lspciSlotRE matches lspci -mm's output shape:
//
//	"00:02.0" "VGA compatible controller [0300]" "Intel Corporation" "UHD Graphics 630 [8086:3e9b]"
//
// The trailing [8086:3e9b] bracket is the PCI ID pair (present because
// of -nn). Fields are double-quoted and bracketed by the slot on the
// left; a straightforward field scan (not a full CSV grammar - lspci
// -mm quoting is regular) is enough for this fixed shape.
var lspciSlotRE = regexp.MustCompile(`^"([0-9a-fA-F:.]+)"\s+"(.+?)"\s+"(.+?)"\s+"(.+?)"`)

// parseLspciGPULine turns one lspci -mm -nn line into a GPUInfo, or nil
// for lines that don't carry a usable name/PCI pair.
func parseLspciGPULine(line string) *GPUInfo {
	m := lspciSlotRE.FindStringSubmatch(strings.TrimSpace(line))
	if m == nil {
		return nil
	}
	name := cleanLspciLabel(m[4])
	vendor := cleanLspciLabel(m[3])
	pciID := ""
	if ids := regexp.MustCompile(`\[([0-9a-fA-F]{4}:[0-9a-fA-F]{4})\]`).FindStringSubmatch(m[4]); ids != nil {
		pciID = strings.ToLower(ids[1])
	}
	if name == "" {
		return nil
	}
	return &GPUInfo{Name: name, Vendor: vendor, PCIID: pciID}
}

// cleanLspciLabel strips lspci's trailing "[nnnn:nnnn]" annotation and
// trims whitespace.
func cleanLspciLabel(s string) string {
	if i := strings.Index(s, " ["); i >= 0 && strings.HasSuffix(s, "]") {
		s = s[:i]
	}
	return strings.TrimSpace(s)
}

// systemProfilerGPUs mirrors the subset of `system_profiler
// SPDisplaysDataType -json`'s shape this parser reads: a top-level
// "SPDisplaysDataType" array of graphics devices, each with sppci_model
// / spdisplays_vendor / spdisplays_revision-id-style keys and
// spdisplays_vram_shared (a string like "8 GB") where present.
type systemProfilerGPUs struct {
	SPDisplaysDataType []struct {
		SppciModel  string `json:"sppci_model"`
		Spvendor    string `json:"spdisplays_vendor"`
		SpVram      string `json:"spdisplays_vram_shared"`
		SpVram2     string `json:"spdisplays_vram"`
		SpPciID     string `json:"spdisplays_pciid"`
		DriverVers  string `json:"spdisplays_driver_version"`
	} `json:"SPDisplaysDataType"`
}

// parseSystemProfilerGPUs best-effort parses the JSON; anything missing
// stays at its zero value consistent with GPUInfo's omitempty fields.
func parseSystemProfilerGPUs(data []byte) []GPUInfo {
	var parsed systemProfilerGPUs
	if err := json.Unmarshal(data, &parsed); err != nil {
		return []GPUInfo{}
	}
	gpus := make([]GPUInfo, 0, len(parsed.SPDisplaysDataType))
	for _, d := range parsed.SPDisplaysDataType {
		if d.SppciModel == "" {
			continue
		}
		gpus = append(gpus, GPUInfo{
			Name:             d.SppciModel,
			Vendor:           d.Spvendor,
			DriverVersion:    d.DriverVers,
			TotalMemoryBytes: parseMacVRAM(firstNonEmpty(d.SpVram, d.SpVram2)),
			PCIID:            normalizeMacPCIID(d.SpPciID),
		})
	}
	return gpus
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

// parseMacVRAM converts system_profiler's human VRAM strings ("8 GB",
// "1536 MB", "4 GB") to bytes; 0 for anything unrecognized.
func parseMacVRAM(s string) uint64 {
	s = strings.TrimSpace(s)
	parts := strings.Fields(s)
	if len(parts) != 2 {
		return 0
	}
	var n uint64
	for _, c := range parts[0] {
		if c < '0' || c > '9' {
			return 0
		}
		n = n*10 + uint64(c-'0')
	}
	switch strings.ToUpper(parts[1]) {
	case "GB":
		return n * 1024 * 1024 * 1024
	case "MB":
		return n * 1024 * 1024
	case "KB":
		return n * 1024
	default:
		return 0
	}
}

// normalizeMacPCIID converts system_profiler's "spdisplays_pciid"
// ("0x8086 0x3e9b") into the "vendor:device" lowercase pair GPUInfo
// documents.
func normalizeMacPCIID(s string) string {
	fields := strings.Fields(strings.TrimSpace(s))
	if len(fields) < 2 {
		return ""
	}
	hexOnly := func(x string) string {
		x = strings.TrimPrefix(strings.ToLower(x), "0x")
		if len(x) != 4 {
			return ""
		}
		return x
	}
	v, d := hexOnly(fields[0]), hexOnly(fields[1])
	if v == "" || d == "" {
		return ""
	}
	return v + ":" + d
}
