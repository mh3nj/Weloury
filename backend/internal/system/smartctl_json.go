package system

import (
	"bytes"
	"context"
	"encoding/json"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"weloury/internal/hidewin"
)

// smartctlOutput is the slice of `smartctl --json=quiet` (schema v1) that
// the health reader needs, for ATA and NVMe drives alike. smartctl's JSON
// shape is byte-for-byte the same on Windows, Linux and macOS, so this one
// file turns a drive's raw SMART data into a real health number on every
// platform. Fields smartctl leaves out (a USB bridge that hides SMART, for
// instance) unmarshal as nil and read back as "not reported" rather than
// zero.
type smartctlOutput struct {
	ModelName   string `json:"model_name"`
	SmartStatus struct {
		Passed *bool `json:"passed"`
	} `json:"smart_status"`
	// RotationRate is the spindle speed in RPM: 0 means a solid-state
	// drive, a positive number a spinning disk, and a missing field (nil)
	// an unknown media type.
	RotationRate *int64 `json:"rotation_rate"`
	UserCapacity struct {
		Bytes *uint64 `json:"bytes"`
	} `json:"user_capacity"`
	Temperature struct {
		Current *float64 `json:"current"`
	} `json:"temperature"`
	NVMeHealth struct {
		Temperature    *float64 `json:"temperature"`
		PercentageUsed *float64 `json:"percentage_used"`
		PowerOnHours   *uint64  `json:"power_on_hours"`
		PowerCycles    *uint64  `json:"power_cycles"`
	} `json:"nvme_smart_health_information_log"`
	ATAAttributes struct {
		Table []smartctlAttribute `json:"table"`
	} `json:"ata_smart_attributes"`
}

type smartctlAttribute struct {
	ID    int              `json:"id"`
	Name  string           `json:"name"`
	Value float64          `json:"value"`
	Raw   smartctlRawValue `json:"raw"`
}

// smartctlRawValue reads an attribute's raw value whether smartctl emits
// the current {"value":N,"string":"..."} object or the older bare number,
// so a version skew between the bundled smartctl and the parser still
// yields the numeric reading.
type smartctlRawValue struct {
	number float64
}

func (r *smartctlRawValue) UnmarshalJSON(b []byte) error {
	s := strings.TrimSpace(string(b))
	if s == "null" || s == "" {
		return nil
	}
	if strings.HasPrefix(s, "{") {
		var obj struct {
			Value  json.RawMessage `json:"value"`
			String string          `json:"string"`
		}
		if err := json.Unmarshal(b, &obj); err != nil {
			return err
		}
		if n, ok := rawToFloat(obj.Value); ok {
			r.number = n
			return nil
		}
		if n, ok := rawToFloat([]byte(obj.String)); ok {
			r.number = n
		}
		return nil
	}
	if n, ok := rawToFloat(b); ok {
		r.number = n
	}
	return nil
}

// rawToFloat coerces a JSON number, or the leading number of a quoted or
// space-separated raw string like "12345" or "33 (243 111)", to a float64.
func rawToFloat(b []byte) (float64, bool) {
	s := strings.Trim(strings.TrimSpace(string(b)), `"`)
	if s == "" || s == "null" {
		return 0, false
	}
	if f := strings.Fields(s); len(f) > 0 {
		s = f[0]
	}
	f, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0, false
	}
	return f, true
}

// runSmartctlJSON runs `smartctl --json=quiet -H -A <device>` and decodes
// the reply. smartctl signals problems through a non-zero exit bitmap while
// still printing valid JSON, so the exit code is deliberately ignored in
// favour of the parsed output; a reply that isn't a JSON object (an unknown
// device, a permission wall) reports false and the caller keeps whatever
// the platform layer already knew.
func runSmartctlJSON(ctx context.Context, exe, device string) (*smartctlOutput, bool) {
	ctx, cancel := context.WithTimeout(ctx, 6*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, exe, "--json=quiet", "-H", "-A", device)
	hidewin.Configure(cmd)
	out, _ := cmd.Output()
	trimmed := bytes.TrimSpace(out)
	if len(trimmed) == 0 || trimmed[0] != '{' {
		return nil, false
	}
	var o smartctlOutput
	if err := json.Unmarshal(trimmed, &o); err != nil {
		return nil, false
	}
	return &o, true
}

// applySmartctlJSON folds a smartctl reading into DiskHealth, overriding
// any estimate the platform layer guessed with the drive's own numbers.
// Fields smartctl didn't report are left untouched, so an incomplete SMART
// reply still keeps the WMI/inventory identity that already landed.
func applySmartctlJSON(h *DiskHealth, o *smartctlOutput) {
	if h.Model == "" && strings.TrimSpace(o.ModelName) != "" {
		h.Model = strings.TrimSpace(o.ModelName)
	}
	if o.UserCapacity.Bytes != nil && *o.UserCapacity.Bytes > 0 {
		h.TotalBytes = *o.UserCapacity.Bytes
	}
	if o.RotationRate != nil {
		if *o.RotationRate == 0 {
			h.MediaType = "SSD"
		} else {
			h.MediaType = "HDD"
		}
	}
	if o.SmartStatus.Passed != nil {
		if *o.SmartStatus.Passed {
			h.Status = "healthy"
		} else {
			h.Status = "failing"
		}
	}
	if h.TemperatureCelsius == 0 {
		if o.NVMeHealth.Temperature != nil && *o.NVMeHealth.Temperature > 0 {
			h.TemperatureCelsius = *o.NVMeHealth.Temperature
		} else if o.Temperature.Current != nil && *o.Temperature.Current > 0 {
			h.TemperatureCelsius = *o.Temperature.Current
		}
	}

	// Health percentage. NVMe drives report the vendor's own
	// "percentage_used", which is as close to a real health figure as a
	// SMART source gets, so that reads as a direct measurement (Estimated
	// false). ATA drives have no single health field: an SSD is scored from
	// its wear attribute and an HDD from its bad-sector counts, both of
	// which are honest calculations rather than guesses, hence Estimated
	// true.
	switch {
	case o.NVMeHealth.PercentageUsed != nil:
		h.HealthPercent = healthFromNVMeUsed(*o.NVMeHealth.PercentageUsed)
		h.Estimated = false
		h.HealthSource = "smart"
	case o.RotationRate != nil && *o.RotationRate == 0:
		if v, ok := ataWearPercent(o); ok {
			h.HealthPercent = v
			h.Estimated = true
			h.HealthSource = "smart"
		}
	default:
		if v, ok := hddHealthPercent(o); ok {
			h.HealthPercent = v
			h.Estimated = true
			h.HealthSource = "smart"
		}
	}

	if h.PowerOnHours == 0 {
		if o.NVMeHealth.PowerOnHours != nil && *o.NVMeHealth.PowerOnHours > 0 {
			h.PowerOnHours = *o.NVMeHealth.PowerOnHours
		} else if a, ok := ataAttr(o, 9); ok && a.Raw.number > 0 {
			h.PowerOnHours = uint64(a.Raw.number)
		}
	}
	if h.PowerCycles == 0 {
		if o.NVMeHealth.PowerCycles != nil && *o.NVMeHealth.PowerCycles > 0 {
			h.PowerCycles = *o.NVMeHealth.PowerCycles
		} else if a, ok := ataAttr(o, 12); ok && a.Raw.number > 0 {
			h.PowerCycles = uint64(a.Raw.number)
		}
	}
}

// healthFromNVMeUsed turns NVMe "percentage used" (wear consumed, which
// smartctl allows above 100) into remaining health, clamped to the 1-100
// display range so a fully worn but still passing drive shows 1 rather than
// a 0 that the wire drops as "not reported".
func healthFromNVMeUsed(used float64) float64 {
	h := 100 - clampFloatPercent(used)
	if h < 1 {
		return 1
	}
	return h
}

// ssdWearAttrIDs lists the ATA attribute ids vendors use for the SSD wear
// / remaining-life score, most-significant first. The normalized VALUE
// column counts down from 100 as the drive wears, so it is the remaining
// health directly.
var ssdWearAttrIDs = []int{231, 177, 202, 233, 166, 245, 203}

func ataWearPercent(o *smartctlOutput) (float64, bool) {
	for _, id := range ssdWearAttrIDs {
		if a, ok := ataAttr(o, id); ok && a.Value > 0 && a.Value <= 100 {
			return a.Value, true
		}
	}
	return 0, false
}

// hddHealthPercent scores a spinning disk from the attributes that predict
// failure: reallocated sectors (permanent damage), current pending sectors,
// and offline uncorrectable sectors. A drive with none reads a clean 100;
// each fault costs a weighted chunk, floored at 1.
func hddHealthPercent(o *smartctlOutput) (float64, bool) {
	realloc, okRealloc := ataAttr(o, 5)
	pending, okPending := ataAttr(o, 197)
	uncorr, okUncorr := ataAttr(o, 198)
	if !okRealloc && !okPending && !okUncorr {
		return 0, false
	}
	bad := 3*nonNeg(realloc.Raw.number) + nonNeg(pending.Raw.number) + 3*nonNeg(uncorr.Raw.number)
	h := 100 - bad
	if h < 1 {
		return 1, true
	}
	return h, true
}

func nonNeg(v float64) float64 {
	if v < 0 {
		return 0
	}
	return v
}

func ataAttr(o *smartctlOutput, id int) (smartctlAttribute, bool) {
	for _, a := range o.ATAAttributes.Table {
		if a.ID == id {
			return a, true
		}
	}
	return smartctlAttribute{}, false
}
