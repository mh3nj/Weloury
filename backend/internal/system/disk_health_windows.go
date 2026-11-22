//go:build windows

package system

import (
	"context"
	"strconv"
	"strings"
)

// Windows physical-drive health, layered by what the OS exposes:
//
//  1. Get-PhysicalDisk + Get-StorageReliabilityCounter - the Storage
//     Spaces cmdlets give HealthStatus ("Healthy"/"Warning"/"Unhealthy")
//     plus temperature, wear, power-on hours. The reliability counters
//     are administrator-only (verified live: "Access to a CIM resource
//     was not available" unelevated), so unelevated runs get status only.
//  2. Win32_DiskDrive - model, size, interface - no admin needed,
//     always present; fills whatever the storage cmdlets couldn't.
//  3. smartctl (bundled with the app beside the core, or found on PATH) -
//     the only non-admin source of a real health percentage and
//     temperature on Windows. Its JSON reply is authoritative, so it
//     overrides whatever the WMI layers had to estimate.
//
// A partial answer is still an answer: Status is always filled
// ("unknown" at worst) - see DiskHealthProvider's contract.
func (p *windowsProvider) DiskHealth(ctx context.Context) ([]DiskHealth, error) {
	healths := p.diskHealthViaStorageCmdlets(ctx)
	inventory := p.diskInventory(ctx)

	// Join by Win32_DiskDrive Index (the storage cmdlets' DeviceId IS
	// that index, as a string). Rows missing from one side merge into
	// the other's.
	byIndex := make(map[int]*diskHealthRow, len(healths))
	for i := range healths {
		byIndex[healths[i].diskIndex] = &healths[i]
	}
	for idx, inv := range inventory {
		if h, ok := byIndex[idx]; ok {
			if h.Model == "" {
				h.Model = inv.model
			}
			if h.TotalBytes == 0 {
				h.TotalBytes = inv.sizeBytes
			}
			if h.BusType == "" {
				h.BusType = inv.busType
			}
			if h.MediaType == "" {
				h.MediaType = inv.mediaType
			}
			continue
		}
		row := diskHealthRow{diskIndex: idx}
		row.Device = physicalDriveName(idx)
		row.Model = inv.model
		row.MediaType = inv.mediaType
		row.BusType = inv.busType
		row.Status = "unknown"
		row.TotalBytes = inv.sizeBytes
		healths = append(healths, row)
	}

	// smartctl enrichment: run the JSON health+attributes query on every
	// drive. Where smartctl answers (temperature without admin, the
	// vendor's own wear figure) it overrides the WMI estimate; where it
	// can't reach the drive it leaves the WMI/inventory result in place.
	smartctl := resolveCommand("smartctl")
	out := make([]DiskHealth, 0, len(healths))
	for i := range healths {
		h := &healths[i]
		if smartctl != nil {
			if o, ok := runSmartctlJSON(ctx, *smartctl, h.Device); ok {
				applySmartctlJSON(&h.DiskHealth, o)
			}
		}
		if h.Model == "" {
			h.Model = h.Device
		}
		out = append(out, h.DiskHealth)
	}
	if len(out) == 0 {
		// No storage cmdlets AND no disk drive rows: report unavailable
		// rather than an empty listing that reads as "no drives".
		return nil, ErrUnavailable
	}
	return out, nil
}

// diskHealthRow is the internal accumulator: DiskHealth plus the join
// key (Win32_DiskDrive.Index) the exported type doesn't carry.
type diskHealthRow struct {
	DiskHealth
	diskIndex int
}

// diskHealthViaStorageCmdlets runs the Get-PhysicalDisk +
// Get-StorageReliabilityCounter pipeline in one PowerShell session -
// they must run in-process (Get-PhysicalDisk | Get-StorageReliability…)
// because the reliability counters are an association, not a queryable
// class. Every probe is soft: admin-denied reliability counters leave
// their fields at zero; a total cmdlet failure returns an empty slice
// (the Win32_DiskDrive layer then provides the listing).
func (p *windowsProvider) diskHealthViaStorageCmdlets(ctx context.Context) []diskHealthRow {
	const script = "(Get-PhysicalDisk | Select-Object DeviceId,FriendlyName,MediaType,BusType,HealthStatus,Size | ConvertTo-Csv -NoTypeInformation); '---RELIABILITY---'; (Get-PhysicalDisk | Get-StorageReliabilityCounter | Select-Object DeviceId,Temperature,Wear,PowerOnHours,PowerCycleCount | ConvertTo-Csv -NoTypeInformation)"
	out, err := runPowerShell(ctx, script)
	if err != nil {
		return nil
	}
	// The two CSV tables come back concatenated; split on the marker.
	mainPart, reliabilityPart := out, ""
	if i := strings.Index(out, "---RELIABILITY---"); i >= 0 {
		mainPart = out[:i]
		reliabilityPart = out[i+len("---RELIABILITY---"):]
	}

	mainRows, err := parseCIMCsv(mainPart)
	if err != nil {
		return nil
	}
	// Reliability CSV may be header-only (access denied): parseCIMCsv
	// then yields zero rows, which is exactly the "no reliability data"
	// state.
	reliabilityByID := make(map[string]map[string]string)
	if relRows, err := parseCIMCsv(reliabilityPart); err == nil {
		for _, row := range relRows {
			reliabilityByID[strings.TrimSpace(row["deviceid"])] = row
		}
	}

	healths := make([]diskHealthRow, 0, len(mainRows))
	for _, row := range mainRows {
		id := strings.TrimSpace(row["deviceid"])
		if id == "" {
			continue
		}
		idx := int(parseIntField(row, "deviceid"))
		h := diskHealthRow{diskIndex: idx}
		h.Device = physicalDriveName(idx)
		h.Model = strings.TrimSpace(row["friendlyname"])
		h.MediaType = normalizeMediaType(strings.TrimSpace(row["mediatype"]))
		h.BusType = normalizeBusType(strings.TrimSpace(row["bustype"]))
		h.Status = storageHealthLabel(strings.TrimSpace(row["healthstatus"]))
		h.TotalBytes = parseUintField(row, "size")

		if rel, ok := reliabilityByID[id]; ok {
			h.TemperatureCelsius = parseFloatField(rel, "temperature")
			h.PowerOnHours = parseUintField(rel, "poweronhours")
			h.PowerCycles = parseUintField(rel, "powercyclecount")
			// "Wear" is percent consumed; remaining health is its
			// complement. Wear > 0 is the only trustworthy signal -
			// HDDs (and permission-denied runs) report 0, which must
			// stay "not reported" rather than reading as "0% health".
			if wear := parseFloatField(rel, "wear"); wear > 0 && wear < 100 {
				h.HealthPercent = 100 - wear
				h.Estimated = true
				h.HealthSource = "wmi"
			} else if wear >= 100 {
				h.HealthPercent = 1
				h.Estimated = true
				h.HealthSource = "wmi"
			}
		}
		healths = append(healths, h)
	}
	return healths
}

// diskInventory is the always-available Win32_DiskDrive listing (no
// admin): model, size, interface - the identity half of DiskHealth.
type diskInventoryRow struct {
	model     string
	mediaType string
	busType   string
	sizeBytes uint64
}

func (p *windowsProvider) diskInventory(ctx context.Context) map[int]diskInventoryRow {
	inv := make(map[int]diskInventoryRow)
	out, err := runPowerShell(ctx,
		"(Get-CimInstance Win32_DiskDrive | Select-Object Index,Model,MediaType,Size,InterfaceType | ConvertTo-Csv -NoTypeInformation)")
	if err != nil {
		return inv
	}
	rows, err := parseCIMCsv(out)
	if err != nil {
		return inv
	}
	for _, row := range rows {
		idx := int(parseIntField(row, "index"))
		inv[idx] = diskInventoryRow{
			model:      strings.TrimSpace(row["model"]),
			mediaType:  normalizeMediaType(strings.TrimSpace(row["mediatype"])),
			busType:    normalizeBusType(strings.TrimSpace(row["interfacetype"])),
			sizeBytes:  parseUintField(row, "size"),
		}
	}
	return inv
}

// physicalDriveName renders Win32_DiskDrive's "\\\\.\\PHYSICALDRIVE0"
// device-name form from an index - DiskHealth.Device's documented shape,
// and the path form smartctl accepts on Windows too.
func physicalDriveName(index int) string {
	return `\\.\PhysicalDrive` + strconv.Itoa(index)
}

// storageHealthLabel maps Get-PhysicalDisk's HealthStatus vocabulary to
// DiskHealth.Status's.
func storageHealthLabel(s string) string {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "healthy":
		return "healthy"
	case "warning":
		return "warning"
	case "unhealthy":
		return "failing"
	default:
		return "unknown"
	}
}

// normalizeMediaType maps both sources' media vocabularies to
// DiskHealth.MediaType's SSD/HDD set. Win32_DiskDrive's
// "Fixed hard disk media" carries no SSD/HDD signal and maps to "".
func normalizeMediaType(s string) string {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "ssd":
		return "SSD"
	case "hdd":
		return "HDD"
	default:
		return ""
	}
}

// normalizeBusType maps Get-PhysicalDisk's BusType and
// Win32_DiskDrive's InterfaceType vocabularies to DiskHealth.BusType's.
func normalizeBusType(s string) string {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "nvme":
		return "NVMe"
	case "sata":
		return "SATA"
	case "usb":
		return "USB"
	case "sas":
		return "SAS"
	case "raid":
		return "RAID"
	case "ide":
		return "IDE"
	case "scsi":
		return "SCSI"
	case "sd":
		return "SD"
	default:
		return ""
	}
}
