//go:build windows

package system

import (
	"encoding/csv"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// This file's fixtures (in windows_parse_test.go) are written from
// documented, stable command output shapes, not captured from a real
// Windows machine - this codebase was authored without access to one.
// See provider_windows.go's package-level comment and DEVELOPMENT.md for
// what that means for you as the first person actually running this on
// Windows.

// parseCIMCsv parses `ConvertTo-Csv -NoTypeInformation` output: a
// standard quoted CSV with a header row of the exact property names
// requested from the CIM query, e.g.:
//
//	"ProcessId","ParentProcessId","Name","WorkingSetSize"
//	"4","0","System","131072"
//
// Returns the header (lowercased, for case-insensitive lookup by
// callers) and each data row as a map from that lowercased header to
// value.
func parseCIMCsv(output string) ([]map[string]string, error) {
	r := csv.NewReader(strings.NewReader(output))
	records, err := r.ReadAll()
	if err != nil {
		return nil, fmt.Errorf("parse CSV: %w", err)
	}
	if len(records) == 0 {
		return nil, nil
	}

	header := records[0]
	for i, h := range header {
		header[i] = strings.ToLower(strings.TrimSpace(h))
	}

	rows := make([]map[string]string, 0, len(records)-1)
	for _, record := range records[1:] {
		row := make(map[string]string, len(header))
		for i, value := range record {
			if i < len(header) {
				row[header[i]] = value
			}
		}
		rows = append(rows, row)
	}
	return rows, nil
}

// parseUintField parses row[key] as a uint64, returning 0 (not an error)
// if the field is absent or empty - CIM properties are frequently null
// for a given object (e.g. a process with no meaningful working set at
// the instant it was queried), and a missing optional numeric field
// shouldn't fail the entire row.
func parseUintField(row map[string]string, key string) uint64 {
	v := strings.TrimSpace(row[key])
	if v == "" {
		return 0
	}
	n, err := strconv.ParseUint(v, 10, 64)
	if err != nil {
		return 0
	}
	return n
}

// parseWMIDateTime parses WMI's datetime string format, documented as
// `yyyyMMddHHmmss.ffffff±UUU` where UUU is the UTC offset in minutes
// (not hours) - e.g. "20240115103045.123456-300" is 2024-01-15
// 10:30:45.123456 local time, 300 minutes (5 hours) behind UTC. Used for
// Win32_Process's CreationDate property.
func parseWMIDateTime(s string) (time.Time, error) {
	if len(s) < 22 {
		return time.Time{}, fmt.Errorf("WMI datetime %q is shorter than expected", s)
	}
	datePart := s[:14]
	microPart := s[15:21]
	sign := s[21]
	offsetPart := s[22:]
	if offsetPart == "" {
		return time.Time{}, fmt.Errorf("WMI datetime %q has no UTC offset digits", s)
	}

	t, err := time.Parse("20060102150405", datePart)
	if err != nil {
		return time.Time{}, fmt.Errorf("parse date part %q: %w", datePart, err)
	}
	micros, err := strconv.Atoi(microPart)
	if err != nil {
		return time.Time{}, fmt.Errorf("parse microseconds %q: %w", microPart, err)
	}
	offsetMinutes, err := strconv.Atoi(offsetPart)
	if err != nil {
		return time.Time{}, fmt.Errorf("parse UTC offset %q: %w", offsetPart, err)
	}
	switch sign {
	case '-':
		offsetMinutes = -offsetMinutes
	case '+':
		// no-op
	default:
		return time.Time{}, fmt.Errorf("WMI datetime %q missing +/- offset sign", s)
	}

	loc := time.FixedZone("", offsetMinutes*60)
	return time.Date(
		t.Year(), t.Month(), t.Day(), t.Hour(), t.Minute(), t.Second(),
		int(time.Duration(micros)*time.Microsecond), loc,
	), nil
}

func parseIntField(row map[string]string, key string) int {
	v := strings.TrimSpace(row[key])
	if v == "" {
		return 0
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return 0
	}
	return n
}

// parseFloatField parses row[key] as a float64, returning 0 (not an
// error) if the field is absent, empty, or malformed - same
// "missing optional numeric field shouldn't fail the row" reasoning
// parseUintField documents. Used for pre-formatted perf-counter
// percentages (PercentProcessorTime and friends), which arrive as
// decimal strings like "12.50".
func parseFloatField(row map[string]string, key string) float64 {
	v := strings.TrimSpace(row[key])
	if v == "" {
		return 0
	}
	n, err := strconv.ParseFloat(v, 64)
	if err != nil {
		return 0
	}
	return n
}

// clampPercent bounds a percentage reading to [0, 100]. Pre-formatted
// counters occasionally report transient out-of-range values (a spike
// past 100 while a core boost-clocks, for instance); clamping keeps a
// single weird sample from stretching a chart's scale.
func clampPercent(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 100 {
		return 100
	}
	return v
}
