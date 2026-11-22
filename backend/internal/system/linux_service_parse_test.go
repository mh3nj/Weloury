//go:build linux

package system

import "testing"

// Fixtures below follow systemd's documented output formats (see `man
// systemctl`) rather than captured real output - this sandbox's
// container doesn't run systemd as PID 1, so unlike this package's other
// Linux fixtures, these could not be checked against a live system. See
// linux_service_parse.go's package comment.

const fixtureListUnits = `sshd.service    loaded active   running OpenSSH server daemon
cron.service    loaded active   running Periodic Command Scheduler
nginx.service   loaded failed   failed  A high performance web server
one-shot.service loaded active exited  Runs once at boot
`

func TestParseSystemctlListUnits(t *testing.T) {
	units := parseSystemctlListUnits(fixtureListUnits)
	if len(units) != 4 {
		t.Fatalf("got %d units, want 4", len(units))
	}
	if units[0].unit != "sshd.service" || units[0].activeState != "active" {
		t.Fatalf("units[0] = %+v", units[0])
	}
	if units[1].description != "Periodic Command Scheduler" {
		t.Fatalf("units[1].description = %q", units[1].description)
	}
	if units[2].activeState != "failed" {
		t.Fatalf("units[2].activeState = %q, want failed", units[2].activeState)
	}
}

const fixtureListUnitFiles = `sshd.service    enabled
cron.service    enabled
nginx.service   disabled
one-shot.service static
masked.service  masked
`

func TestParseSystemctlListUnitFiles(t *testing.T) {
	files := parseSystemctlListUnitFiles(fixtureListUnitFiles)
	if len(files) != 5 {
		t.Fatalf("got %d entries, want 5", len(files))
	}
	if files[0].unitFile != "sshd.service" || files[0].state != "enabled" {
		t.Fatalf("files[0] = %+v", files[0])
	}
}

func TestNormalizeSystemdState(t *testing.T) {
	cases := map[string]string{
		"active": "running", "activating": "running", "reloading": "running",
		"inactive": "stopped", "deactivating": "stopped",
		"failed": "failed", "": "unknown", "something-new": "unknown",
	}
	for input, want := range cases {
		if got := normalizeSystemdState(input); got != want {
			t.Errorf("normalizeSystemdState(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestNormalizeSystemdStartupMode(t *testing.T) {
	cases := map[string]string{
		"enabled": "enabled", "enabled-runtime": "enabled",
		"disabled": "disabled", "masked": "disabled",
		"static": "manual", "": "unknown", "generated": "unknown",
	}
	for input, want := range cases {
		if got := normalizeSystemdStartupMode(input); got != want {
			t.Errorf("normalizeSystemdStartupMode(%q) = %q, want %q", input, got, want)
		}
	}
}

const fixtureSystemctlShow = `Id=sshd.service
Description=OpenSSH server daemon
LoadState=loaded
ActiveState=active
SubState=running
UnitFileState=enabled
MainPID=1234
Requires=sysinit.target system.slice
`

func TestParseSystemctlShow(t *testing.T) {
	props := parseSystemctlShow(fixtureSystemctlShow)
	if props["Id"] != "sshd.service" {
		t.Fatalf(`props["Id"] = %q`, props["Id"])
	}
	if props["ActiveState"] != "active" {
		t.Fatalf(`props["ActiveState"] = %q`, props["ActiveState"])
	}
	if props["MainPID"] != "1234" {
		t.Fatalf(`props["MainPID"] = %q`, props["MainPID"])
	}
}

func TestParseSystemctlShowNotFoundUnit(t *testing.T) {
	// systemd's documented behavior: `systemctl show` on a nonexistent
	// unit exits 0 and reports LoadState=not-found rather than failing
	// the command - the caller must check this field, not the exit code.
	props := parseSystemctlShow("Id=nonexistent.service\nLoadState=not-found\nActiveState=inactive\n")
	if props["LoadState"] != "not-found" {
		t.Fatalf(`props["LoadState"] = %q, want "not-found"`, props["LoadState"])
	}
}

func TestParseMainPID(t *testing.T) {
	if got := parseMainPID("1234"); got != 1234 {
		t.Fatalf("got %d, want 1234", got)
	}
	if got := parseMainPID("0"); got != 0 {
		t.Fatalf("got %d, want 0 for a not-running service", got)
	}
	if got := parseMainPID(""); got != 0 {
		t.Fatalf("got %d, want 0 for empty input", got)
	}
}
