//go:build linux

package system

import (
	"bufio"
	"strconv"
	"strings"
)

// Linux service management: systemctl, not D-Bus directly
//
// systemd's real API is D-Bus (org.freedesktop.systemd1), which is what
// a "proper" implementation would use - but that means either a binary
// protocol client written by hand (high risk, no way to verify it here)
// or a third-party D-Bus library taken as a dependency sight-unseen (see
// DEVELOPMENT.md on why this codebase is cautious about exactly that).
// systemctl's machine-readable output formats (`list-units --plain`,
// `list-unit-files`, and especially `show`, which is explicitly designed
// for scripting - see `man systemctl`) are stable and well documented,
// so this package uses them the same way provider_darwin.go and
// provider_windows.go use their platforms' command-line tools, for the
// same reasons. Unlike this codebase's /proc-based system info, this
// specific subsystem could not be checked against a real running
// systemd in the sandbox it was authored in (a container's PID 1 isn't
// systemd) - see DEVELOPMENT.md for what that means for you.

// systemdUnitLine is one row of
// `systemctl list-units --type=service --all --no-legend --plain`.
type systemdUnitLine struct {
	unit                             string
	loadState, activeState, subState string
	description                      string
}

// parseSystemctlListUnits parses list-units' columnar output: UNIT LOAD
// ACTIVE SUB DESCRIPTION, where DESCRIPTION is free text that may itself
// contain spaces and is therefore the last column, joined from whatever
// remains.
func parseSystemctlListUnits(output string) []systemdUnitLine {
	var out []systemdUnitLine
	scanner := bufio.NewScanner(strings.NewReader(output))
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) < 4 {
			continue
		}
		out = append(out, systemdUnitLine{
			unit: fields[0], loadState: fields[1], activeState: fields[2], subState: fields[3],
			description: strings.Join(fields[4:], " "),
		})
	}
	return out
}

// systemdUnitFileLine is one row of
// `systemctl list-unit-files --type=service --no-legend`.
type systemdUnitFileLine struct {
	unitFile string
	state    string
}

// parseSystemctlListUnitFiles parses list-unit-files' output: UNIT_FILE
// STATE [VENDOR_PRESET] - only the first two columns are used; a
// possible third ("vendor preset") column, present in some systemd
// versions/flag combinations, is ignored.
func parseSystemctlListUnitFiles(output string) []systemdUnitFileLine {
	var out []systemdUnitFileLine
	scanner := bufio.NewScanner(strings.NewReader(output))
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) < 2 {
			continue
		}
		out = append(out, systemdUnitFileLine{unitFile: fields[0], state: fields[1]})
	}
	return out
}

// parseSystemctlShow parses `systemctl show`'s KEY=VALUE-per-line output
// - the format explicitly documented as intended for scripting - into a
// lookup map.
func parseSystemctlShow(output string) map[string]string {
	result := make(map[string]string)
	for _, line := range strings.Split(output, "\n") {
		eq := strings.IndexByte(line, '=')
		if eq < 0 {
			continue
		}
		result[line[:eq]] = line[eq+1:]
	}
	return result
}

// normalizeSystemdState maps systemd's ActiveState onto Weloury's shared,
// small state vocabulary. SubState (e.g. "running" vs "exited" for a
// one-shot unit that's ActiveState=active) is intentionally not
// distinguished further here - a deliberate simplification, not an
// oversight, for a first pass at service state reporting.
func normalizeSystemdState(activeState string) string {
	switch activeState {
	case "active", "activating", "reloading":
		return "running"
	case "inactive", "deactivating":
		return "stopped"
	case "failed":
		return "failed"
	default:
		return "unknown"
	}
}

// normalizeSystemdStartupMode maps a list-unit-files STATE column value
// onto Weloury's shared startup-mode vocabulary.
func normalizeSystemdStartupMode(fileState string) string {
	switch {
	case strings.HasPrefix(fileState, "enabled"):
		return "enabled"
	case fileState == "disabled" || fileState == "masked":
		return "disabled"
	case fileState == "static":
		// A static unit has no [Install] section and can't be
		// individually enabled/disabled - it's pulled in only as another
		// unit's dependency. "manual" is the closest fit in Weloury's
		// vocabulary to "this isn't something you toggle directly."
		return "manual"
	default:
		return "unknown"
	}
}

// parseMainPID parses systemctl show's MainPID property, which is "0"
// (not an error, not an empty string) for a service that isn't running.
func parseMainPID(value string) int32 {
	v, err := strconv.ParseInt(strings.TrimSpace(value), 10, 32)
	if err != nil {
		return 0
	}
	return int32(v)
}
