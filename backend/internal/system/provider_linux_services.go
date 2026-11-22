//go:build linux

package system

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
)

func runSystemctl(ctx context.Context, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "systemctl", args...)
	out, err := cmd.Output()
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			return "", fmt.Errorf("systemctl %v: %w (stderr: %s)", args, err, strings.TrimSpace(string(exitErr.Stderr)))
		}
		return "", fmt.Errorf("systemctl %v: %w", args, err)
	}
	return string(out), nil
}

func (p *linuxProvider) ListServices(ctx context.Context) ([]ServiceInfo, error) {
	unitsOut, err := runSystemctl(ctx, "list-units", "--type=service", "--all", "--no-legend", "--no-pager", "--plain")
	if err != nil {
		return nil, fmt.Errorf("list units: %w", err)
	}
	units := parseSystemctlListUnits(unitsOut)

	// Startup mode (enabled/disabled) comes from a second, separate call.
	// A failure here degrades every service's StartupMode to "unknown"
	// rather than failing the whole listing - the running/stopped/failed
	// state from list-units above is the more important signal and
	// shouldn't be lost over a secondary one.
	startupByUnit := make(map[string]string)
	if filesOut, err := runSystemctl(ctx, "list-unit-files", "--type=service", "--no-legend", "--no-pager"); err == nil {
		for _, f := range parseSystemctlListUnitFiles(filesOut) {
			startupByUnit[f.unitFile] = normalizeSystemdStartupMode(f.state)
		}
	}

	result := make([]ServiceInfo, 0, len(units))
	for _, u := range units {
		startup, ok := startupByUnit[u.unit]
		if !ok {
			startup = "unknown"
		}
		result = append(result, ServiceInfo{
			Name: u.unit,
			// systemd has no concept of a display name distinct from the
			// unit name; DisplayName mirrors Name rather than being left
			// empty, so frontend code doesn't need a per-platform
			// fallback of its own.
			DisplayName: u.unit,
			Description: u.description,
			State:       normalizeSystemdState(u.activeState),
			StartupMode: startup,
			// PID: left 0 in the bulk listing - see ServiceDetail, which
			// fetches it via a per-unit systemctl show call where the
			// extra property costs nothing additional.
		})
	}
	return result, nil
}

func (p *linuxProvider) ServiceDetail(ctx context.Context, name string) (ServiceDetail, error) {
	out, err := runSystemctl(ctx, "show", name, "--no-pager",
		"--property=Id,Description,LoadState,ActiveState,SubState,UnitFileState,MainPID,Requires")
	if err != nil {
		return ServiceDetail{}, fmt.Errorf("show %s: %w", name, err)
	}
	props := parseSystemctlShow(out)

	// systemd's documented behavior: `show` on a nonexistent unit exits
	// 0 and reports LoadState=not-found rather than the command failing
	// - see linux_service_parse_test.go's TestParseSystemctlShowNotFoundUnit.
	if props["LoadState"] == "" || props["LoadState"] == "not-found" {
		return ServiceDetail{}, fmt.Errorf("service %s: %w", name, ErrNotFound)
	}

	var deps []string
	if requires := strings.TrimSpace(props["Requires"]); requires != "" {
		deps = strings.Fields(requires)
	}

	return ServiceDetail{
		ServiceInfo: ServiceInfo{
			Name:        props["Id"],
			DisplayName: props["Id"],
			Description: props["Description"],
			State:       normalizeSystemdState(props["ActiveState"]),
			StartupMode: normalizeSystemdStartupMode(props["UnitFileState"]),
			PID:         parseMainPID(props["MainPID"]),
		},
		Dependencies: deps,
	}, nil
}

func (p *linuxProvider) StartService(ctx context.Context, name string) error {
	_, err := runSystemctl(ctx, "start", name)
	return wrapSystemctlActionError(name, err)
}

func (p *linuxProvider) StopService(ctx context.Context, name string) error {
	_, err := runSystemctl(ctx, "stop", name)
	return wrapSystemctlActionError(name, err)
}

func (p *linuxProvider) RestartService(ctx context.Context, name string) error {
	_, err := runSystemctl(ctx, "restart", name)
	return wrapSystemctlActionError(name, err)
}

func (p *linuxProvider) SetServiceEnabled(ctx context.Context, name string, enabled bool) error {
	verb := "disable"
	if enabled {
		verb = "enable"
	}
	_, err := runSystemctl(ctx, verb, name)
	return wrapSystemctlActionError(name, err)
}

// wrapSystemctlActionError adds the service name to a systemctl action
// failure for a clearer error message. It deliberately does not attempt
// to classify permission-denied failures as a distinct error type: the
// stderr text systemd/polkit produce for that case varies too much
// across distributions and configurations to pattern-match reliably, so
// the raw command error (which does include stderr - see runSystemctl)
// is surfaced as-is. See SECURITY.md: Weloury never attempts to escalate
// privileges to work around a failure like this.
func wrapSystemctlActionError(name string, err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("service %s: %w", name, err)
}
