//go:build darwin

package system

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// This file's fixtures (in darwin_parse_test.go) are written from
// documented, stable command output formats, not captured from a real
// macOS machine - this codebase was authored without access to one. See
// DEVELOPMENT.md and ARCHITECTURE.md "Darwin and Windows: os/exec over
// syscalls" for what that means for you as the first person actually
// running this on macOS.

// parseSysctlBootTime parses `sysctl -n kern.boottime`'s output, of the
// documented form: "{ sec = 1723400000, usec = 123456 } Mon Jan  1
// 00:00:00 2024" - extracting the sec value, which is a Unix timestamp.
func parseSysctlBootTime(output string) (time.Time, error) {
	const marker = "sec = "
	idx := strings.Index(output, marker)
	if idx < 0 {
		return time.Time{}, fmt.Errorf("no %q found in kern.boottime output %q", marker, output)
	}
	rest := output[idx+len(marker):]
	end := strings.IndexAny(rest, ", ")
	if end < 0 {
		return time.Time{}, fmt.Errorf("could not find end of seconds value in %q", rest)
	}
	sec, err := strconv.ParseInt(rest[:end], 10, 64)
	if err != nil {
		return time.Time{}, fmt.Errorf("parse boot seconds %q: %w", rest[:end], err)
	}
	return time.Unix(sec, 0), nil
}

// parseUint64Sysctl parses a bare-integer `sysctl -n <name>` output
// (used for hw.memsize, hw.physicalcpu, hw.logicalcpu, ...).
func parseUint64Sysctl(output string) (uint64, error) {
	trimmed := strings.TrimSpace(output)
	v, err := strconv.ParseUint(trimmed, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("parse sysctl output %q as uint64: %w", trimmed, err)
	}
	return v, nil
}

// parseVMStat extracts free/active/wired page counts from `vm_stat`'s
// documented output format:
//
//	Mach Virtual Memory Statistics: (page size of 16384 bytes)
//	Pages free:                                123456.
//	Pages active:                               234567.
//	...
//
// pageSize (from the header line) and the named counts are all needed to
// compute used-vs-available memory, since macOS doesn't expose a single
// "used bytes" sysctl the way Linux's /proc/meminfo does.
func parseVMStat(output string) (pageSize uint64, counts map[string]uint64, err error) {
	counts = make(map[string]uint64)
	lines := strings.Split(output, "\n")
	if len(lines) == 0 {
		return 0, nil, fmt.Errorf("empty vm_stat output")
	}

	const pageSizeMarker = "page size of "
	if idx := strings.Index(lines[0], pageSizeMarker); idx >= 0 {
		rest := lines[0][idx+len(pageSizeMarker):]
		end := strings.Index(rest, " ")
		if end > 0 {
			if v, perr := strconv.ParseUint(rest[:end], 10, 64); perr == nil {
				pageSize = v
			}
		}
	}
	if pageSize == 0 {
		return 0, nil, fmt.Errorf("could not find page size in vm_stat header %q", lines[0])
	}

	for _, line := range lines[1:] {
		colon := strings.IndexByte(line, ':')
		if colon < 0 {
			continue
		}
		key := strings.TrimSpace(line[:colon])
		valueStr := strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(line[colon+1:]), "."))
		v, err := strconv.ParseUint(valueStr, 10, 64)
		if err != nil {
			continue
		}
		counts[key] = v
	}

	return pageSize, counts, nil
}

// parseDfOutput parses `df -k`'s output (POSIX-mode, 1024-byte blocks):
//
//	Filesystem   1024-blocks      Used Available Capacity  Mounted on
//	/dev/disk3s1   488245288 412345678  70000000    86%    /
//
// Values are reported by df already in 1024-byte blocks (the "-k" flag),
// so this multiplies by 1024 directly rather than needing a separate
// block-size lookup the way Linux's statfs does.
func parseDfOutput(output string) ([]DiskInfo, error) {
	lines := strings.Split(strings.TrimRight(output, "\n"), "\n")
	if len(lines) < 2 {
		return nil, nil // header only, or empty: no disks, not an error
	}

	var out []DiskInfo
	for _, line := range lines[1:] { // skip header
		fields := strings.Fields(line)
		if len(fields) < 6 {
			continue
		}
		device := fields[0]
		totalKB, err1 := strconv.ParseUint(fields[1], 10, 64)
		usedKB, err2 := strconv.ParseUint(fields[2], 10, 64)
		availKB, err3 := strconv.ParseUint(fields[3], 10, 64)
		if err1 != nil || err2 != nil || err3 != nil {
			continue
		}
		// Mounted-on can itself contain spaces; it's every field from
		// index 5 onward rejoined, not just fields[5].
		mountPoint := strings.Join(fields[5:], " ")

		out = append(out, DiskInfo{
			Device:     device,
			MountPoint: mountPoint,
			Filesystem: "", // df -k alone doesn't report fs type; acceptable gap for Phase 2
			TotalBytes: totalKB * 1024,
			UsedBytes:  usedKB * 1024,
			FreeBytes:  availKB * 1024,
		})
	}
	return out, nil
}

// parseSwapUsage parses `sysctl -n vm.swapusage`'s documented output
// form: "total = 2048.00M  used = 512.00M  free = 1536.00M  (encrypted)" -
// values are megabytes (1024*1024 bytes).
func parseSwapUsage(output string) (totalBytes, usedBytes uint64, err error) {
	total, err := extractMegabyteField(output, "total = ")
	if err != nil {
		return 0, 0, err
	}
	used, err := extractMegabyteField(output, "used = ")
	if err != nil {
		return 0, 0, err
	}
	return uint64(total * 1024 * 1024), uint64(used * 1024 * 1024), nil
}

func extractMegabyteField(output, marker string) (float64, error) {
	idx := strings.Index(output, marker)
	if idx < 0 {
		return 0, fmt.Errorf("no %q found in vm.swapusage output %q", marker, output)
	}
	rest := output[idx+len(marker):]
	end := strings.IndexByte(rest, 'M')
	if end < 0 {
		return 0, fmt.Errorf("no trailing M found after %q in %q", marker, rest)
	}
	v, err := strconv.ParseFloat(strings.TrimSpace(rest[:end]), 64)
	if err != nil {
		return 0, fmt.Errorf("parse %q as float: %w", rest[:end], err)
	}
	return v, nil
}

// psDetailRow is one parsed row of
// `ps -axo pid,ppid,state,user,rss,pcpu,comm -p <pid>` - a single-process
// query with %CPU included, which macOS's ps computes for us directly
// (no two-sample technique needed here, unlike aggregate CPUUsage).
type psDetailRow struct {
	pid, ppid   int32
	state, user string
	rssKB       uint64
	cpuPercent  float64
	comm        string
}

// parsePSDetailOutput parses the single-process query described above.
func parsePSDetailOutput(output string) (psDetailRow, error) {
	lines := strings.Split(strings.TrimRight(output, "\n"), "\n")
	if len(lines) < 2 {
		return psDetailRow{}, fmt.Errorf("ps produced no data row (process may not exist)")
	}
	fields := strings.Fields(lines[1])
	if len(fields) < 7 {
		return psDetailRow{}, fmt.Errorf("ps data row has %d fields, want at least 7: %q", len(fields), lines[1])
	}

	pid, err1 := strconv.ParseInt(fields[0], 10, 32)
	ppid, err2 := strconv.ParseInt(fields[1], 10, 32)
	rss, err3 := strconv.ParseUint(fields[4], 10, 64)
	cpuPercent, err4 := strconv.ParseFloat(fields[5], 64)
	if err1 != nil || err2 != nil || err3 != nil || err4 != nil {
		return psDetailRow{}, fmt.Errorf("parse ps data row %q: pid=%v ppid=%v rss=%v cpu=%v", lines[1], err1, err2, err3, err4)
	}

	return psDetailRow{
		pid: int32(pid), ppid: int32(ppid),
		state: fields[2], user: fields[3],
		rssKB: rss, cpuPercent: cpuPercent,
		comm: strings.Join(fields[6:], " "),
	}, nil
}

// psRow is one parsed row of `ps -axo pid,ppid,state,user,rss,comm`
// output (a fixed, script-friendly column set - deliberately not the
// default `ps` columns, which vary by invocation and terminal width).
type psRow struct {
	pid, ppid   int32
	state, user string
	rssKB       uint64
	comm        string
}

// parsePSOutput parses `ps -axo pid,ppid,state,user,rss,comm` output. The
// header line's exact column labels vary slightly across BSD/macOS ps
// versions, so this skips line 1 unconditionally rather than matching on
// header text, and relies entirely on the "-o" column order requested.
func parsePSOutput(output string) ([]psRow, error) {
	lines := strings.Split(strings.TrimRight(output, "\n"), "\n")
	if len(lines) < 2 {
		return nil, nil
	}

	var out []psRow
	for _, line := range lines[1:] {
		fields := strings.Fields(line)
		if len(fields) < 6 {
			continue
		}
		pid, err1 := strconv.ParseInt(fields[0], 10, 32)
		ppid, err2 := strconv.ParseInt(fields[1], 10, 32)
		rss, err3 := strconv.ParseUint(fields[4], 10, 64)
		if err1 != nil || err2 != nil || err3 != nil {
			continue
		}
		// comm (the command name) is everything from field 6 onward
		// rejoined, since a full path can't contain internal spaces but
		// we're conservative here in case ps ever reports arguments too.
		comm := strings.Join(fields[5:], " ")

		out = append(out, psRow{
			pid: int32(pid), ppid: int32(ppid),
			state: fields[2], user: fields[3],
			rssKB: rss, comm: comm,
		})
	}
	return out, nil
}

// psStateLabel maps BSD ps's single-letter state codes (see `man ps` on// macOS, the STAT section) to Weloury's shared status vocabulary.
func psStateLabel(code string) string {
	if code == "" {
		return "unknown"
	}
	switch code[0] {
	case 'R':
		return "running"
	case 'S':
		return "sleeping"
	case 'I':
		return "idle"
	case 'T':
		return "stopped"
	case 'U', 'D':
		return "disk_sleep"
	case 'Z':
		return "zombie"
	default:
		return "unknown"
	}
}

// Darwin service management: launchctl, deliberately conservative
//
// launchd's model is genuinely different from systemd's: jobs (not
// "services") are addressed by label, loaded from plist files whose
// paths `launchctl list` does not report, and modern launchctl
// (10.11+) addresses them via a "domain-target" scheme
// (system/<label>, gui/<uid>/<label>, ...) that requires knowing which
// domain a job runs in - something a bare `launchctl list` also doesn't
// disambiguate. That combination is a meaningfully higher-uncertainty
// area than anything else in this codebase's macOS support, which is
// why ListServices/ServiceDetail below are solid but thin (no
// description, no reliable startup-mode), and why SetServiceEnabled
// returns ErrUnavailable outright rather than guess at a plist path this
// author had no real Mac to verify against.

// launchctlListRow is one row of `launchctl list`'s tab-separated
// output: PID, last exit Status, and Label. PID is "-" for a job that
// isn't currently running.
type launchctlListRow struct {
	pidRaw string
	status string
	label  string
}

// parseLaunchctlList parses `launchctl list`'s output.
func parseLaunchctlList(output string) []launchctlListRow {
	var out []launchctlListRow
	lines := strings.Split(strings.TrimRight(output, "\n"), "\n")
	if len(lines) < 2 {
		return out
	}
	for _, line := range lines[1:] { // skip the "PID Status Label" header
		fields := strings.Fields(line)
		if len(fields) < 3 {
			continue
		}
		out = append(out, launchctlListRow{pidRaw: fields[0], status: fields[1], label: strings.Join(fields[2:], " ")})
	}
	return out
}

// launchctlPID parses the PID column: a decimal number, or "-" meaning
// not currently running (which is not an error - it's the common case).
func launchctlPID(raw string) int32 {
	if raw == "-" || raw == "" {
		return 0
	}
	v, err := strconv.ParseInt(raw, 10, 32)
	if err != nil {
		return 0
	}
	return int32(v)
}
