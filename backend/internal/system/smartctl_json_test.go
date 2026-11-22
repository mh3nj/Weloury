package system

import (
	"encoding/json"
	"testing"
)

func decodeSmartctl(t *testing.T, payload string) *smartctlOutput {
	t.Helper()
	var o smartctlOutput
	if err := json.Unmarshal([]byte(payload), &o); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	return &o
}

func TestApplySmartctlJSONNVMePercentage(t *testing.T) {
	// NVMe drive reporting the vendor's own percentage_used of 7 -> 93
	// health, a direct reading (not estimated) sourced from SMART.
	o := decodeSmartctl(t, `{
		"model_name": "Samsung SSD 980",
		"smart_status": {"passed": true},
		"rotation_rate": 0,
		"user_capacity": {"bytes": 1000204886016},
		"nvme_smart_health_information_log": {
			"temperature": 41, "percentage_used": 7,
			"power_on_hours": 1234, "power_cycles": 56
		}
	}`)
	h := DiskHealth{Device: `/dev/nvme0`, Status: "unknown"}
	applySmartctlJSON(&h, o)

	if h.Status != "healthy" {
		t.Errorf("status = %q, want healthy", h.Status)
	}
	if h.HealthPercent != 93 || h.Estimated || h.HealthSource != "smart" {
		t.Errorf("health = %v (est=%v src=%q), want 93 direct smart", h.HealthPercent, h.Estimated, h.HealthSource)
	}
	if h.MediaType != "SSD" || h.TemperatureCelsius != 41 || h.PowerOnHours != 1234 || h.PowerCycles != 56 {
		t.Errorf("identity mismatch: media=%q temp=%v poh=%v cyc=%v", h.MediaType, h.TemperatureCelsius, h.PowerOnHours, h.PowerCycles)
	}
}

func TestApplySmartctlJSONSSDWearAttribute(t *testing.T) {
	// SATA SSD: no NVMe log, health comes from the wear attribute VALUE
	// (attribute 177 at 88), flagged as an estimate.
	o := decodeSmartctl(t, `{
		"smart_status": {"passed": true},
		"rotation_rate": 0,
		"temperature": {"current": 33},
		"ata_smart_attributes": {"table": [
			{"id": 9, "name": "Power_On_Hours", "value": 100, "raw": {"value": 4321, "string": "4321"}},
			{"id": 177, "name": "Wear_Leveling_Count", "value": 88, "raw": {"value": 12, "string": "12"}}
		]}
	}`)
	h := DiskHealth{Device: `/dev/sda`, Status: "unknown"}
	applySmartctlJSON(&h, o)

	if h.HealthPercent != 88 || !h.Estimated || h.HealthSource != "smart" {
		t.Errorf("health = %v (est=%v src=%q), want 88 estimated smart", h.HealthPercent, h.Estimated, h.HealthSource)
	}
	if h.PowerOnHours != 4321 || h.TemperatureCelsius != 33 {
		t.Errorf("poh=%v temp=%v, want 4321 / 33", h.PowerOnHours, h.TemperatureCelsius)
	}
}

func TestApplySmartctlJSONHDDBadSectors(t *testing.T) {
	// Spinning disk: three reallocated sectors score 100 - 3*3 = 91. Raw
	// values here use the bare-number form some smartctl builds emit.
	o := decodeSmartctl(t, `{
		"smart_status": {"passed": true},
		"rotation_rate": 7200,
		"ata_smart_attributes": {"table": [
			{"id": 5, "name": "Reallocated_Sector_Ct", "value": 96, "raw": 3},
			{"id": 197, "name": "Current_Pending_Sector", "value": 100, "raw": 0}
		]}
	}`)
	h := DiskHealth{Device: `/dev/sdb`, Status: "unknown"}
	applySmartctlJSON(&h, o)

	if h.HealthPercent != 91 || !h.Estimated {
		t.Errorf("health = %v (est=%v), want 91 estimated", h.HealthPercent, h.Estimated)
	}
	if h.MediaType != "HDD" {
		t.Errorf("media = %q, want HDD", h.MediaType)
	}
}

func TestApplySmartctlJSONFailingDrive(t *testing.T) {
	o := decodeSmartctl(t, `{"smart_status": {"passed": false}, "rotation_rate": 7200}`)
	h := DiskHealth{Device: `/dev/sdc`, Status: "unknown"}
	applySmartctlJSON(&h, o)
	if h.Status != "failing" {
		t.Errorf("status = %q, want failing", h.Status)
	}
}

func TestApplySmartctlJSONLeavesUnreadableFields(t *testing.T) {
	// A USB bridge that answers only the model: WMI/inventory values that
	// smartctl didn't overwrite must survive.
	o := decodeSmartctl(t, `{"model_name": "External USB"}`)
	h := DiskHealth{Device: `/dev/sdd`, Status: "healthy", BusType: "USB", TotalBytes: 500}
	applySmartctlJSON(&h, o)
	if h.Model != "External USB" {
		t.Errorf("model = %q, want External USB", h.Model)
	}
	if h.Status != "healthy" || h.BusType != "USB" || h.TotalBytes != 500 {
		t.Errorf("preexisting fields clobbered: status=%q bus=%q bytes=%v", h.Status, h.BusType, h.TotalBytes)
	}
	if h.HealthPercent != 0 || h.HealthSource != "" {
		t.Errorf("fabricated health: pct=%v src=%q, want none", h.HealthPercent, h.HealthSource)
	}
}
