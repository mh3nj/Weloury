//go:build linux

package system

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"weloury/internal/hidewin"
)

// Linux GPU support: nvidia-smi leads for NVIDIA hardware (see
// gpu_nvidia.go - the same binary and field-query interface as Windows,
// so Linux gets the full utilization/memory/temperature/fan/power set);
// lspci fills in the adapter inventory for everything else. Live
// telemetry for non-NVIDIA adapters stays an honest ErrUnavailable -
// that would need DRM/ioctl work per-driver, not a guess.

func (p *linuxProvider) ListGPUs(ctx context.Context) ([]GPUInfo, error) {
	cmd := exec.Command("lspci", "-mm", "-nn", "-d", "0300:", "-d", "0302:")
	hidewin.Configure(cmd)
	out, err := cmd.Output()
	if err != nil {
		// No lspci (or no PCI bus): fall back to nvidia-smi's inventory
		// before reporting an empty machine - nvidia-smi answers even
		// where lspci is absent.
		if nvidiaGPUs, _ := sampleNvidiaGPUs(ctx); len(nvidiaGPUs) > 0 {
			return nvidiaGPUs, nil
		}
		return []GPUInfo{}, nil
	}

	var gpus []GPUInfo
	for _, line := range strings.Split(string(out), "\n") {
		gpu := parseLspciGPULine(line)
		if gpu != nil {
			gpu.Index = len(gpus)
			gpus = append(gpus, *gpu)
		}
	}
	// NVIDIA adapters from lspci get their identity enriched (and their
	// telemetry made possible) by merging nvidia-smi's row - lspci's
	// "[10de:2484]" bracket is the vendor:device pair nvidia-smi's PCI
	// query also yields, so exact PCIID match joins the two views.
	if nvGpus, _ := sampleNvidiaGPUs(ctx); len(nvGpus) > 0 {
		for i := range gpus {
			for _, ng := range nvGpus {
				if gpus[i].PCIID != "" && gpus[i].PCIID == ng.PCIID {
					if ng.TotalMemoryBytes > 0 {
						gpus[i].TotalMemoryBytes = ng.TotalMemoryBytes
					}
					if ng.DriverVersion != "" {
						gpus[i].DriverVersion = ng.DriverVersion
					}
					break
				}
			}
		}
	}
	if gpus == nil {
		gpus = []GPUInfo{}
	}
	return gpus, nil
}

func (p *linuxProvider) GPUUsage(ctx context.Context, index int) (GPUUsage, error) {
	gpus, err := p.ListGPUs(ctx)
	if err != nil {
		return GPUUsage{}, err
	}
	if index < 0 || index >= len(gpus) {
		return GPUUsage{}, fmt.Errorf("gpu %d: %w", index, ErrNotFound)
	}
	target := gpus[index]

	// nvidia-smi answers for NVIDIA hardware regardless of which source
	// produced the inventory row.
	if strings.Contains(strings.ToLower(target.Vendor), "nvidia") || strings.HasPrefix(target.PCIID, "10de:") {
		if _, nvUsages := sampleNvidiaGPUs(ctx); len(nvUsages) > 0 && index < len(nvUsages) {
			usage := nvUsages[index]
			if usage.MemoryTotalBytes == 0 {
				usage.MemoryTotalBytes = target.TotalMemoryBytes
			}
			return usage, nil
		}
	}
	return GPUUsage{}, fmt.Errorf("gpu usage sampling: %w", ErrUnavailable)
}

// --- Disk health (Linux) ---

// smartctlAvailable reports whether smartmontools' smartctl is installed
// - the one portable SMART reader on Linux (the /dev/ioctls it wraps are
// per-transport and far too sharp to hand-roll here).
func smartctlAvailable() bool {
	return resolveCommand("smartctl") != nil
}

func (p *linuxProvider) DiskHealth(ctx context.Context) ([]DiskHealth, error) {
	if !smartctlAvailable() {
		// Without smartctl there is no verifiable SMART source; listing
		// drives with fabricated "healthy" would be worse than saying so.
		return nil, fmt.Errorf("disk health: %w", ErrUnavailable)
	}

	// Physical-disk inventory via lsblk (util-linux, present everywhere):
	// -d skips partitions, -n skips headers, -o MACHINE for stable fields.
	out, err := exec.CommandContext(ctx, "lsblk", "-dn", "-o", "NAME,SIZE,MODEL").Output()
	if err != nil {
		return nil, fmt.Errorf("list block devices: %w", err)
	}

	var healths []DiskHealth
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		// lsblk aligns columns with runs of spaces; Fields + Join keeps
		// the multi-word MODEL field intact regardless.
		fields := strings.Fields(line)
		if len(fields) < 1 || fields[0] == "" {
			continue
		}
		dev := "/dev/" + fields[0]
		h := DiskHealth{
			Device: dev,
			Status: "unknown",
		}
		if len(fields) >= 3 {
			h.Model = strings.Join(fields[2:], " ")
		}
		// smartctl exit codes are a bitmap (bit 0 = command parse issue,
		// bit 3 = disk failing, bit 4 = prefailure attrs <= threshold);
		// 0/2/3/4-ish combos still carry usable text output, so parse the
		// text and map only the failing bits to status.
		h = p.enrichSmartctl(ctx, h)
		healths = append(healths, h)
	}
	return healths, nil
}

// enrichSmartctl runs the JSON health+attributes query on the drive and
// folds whatever smartctl reads into the row: SMART pass/fail, temperature,
// power-on hours, and a real health percentage (NVMe percentage-used or the
// ATA wear/bad-sector scores). Full attributes usually need root, but the
// overall health line and temperature are readable unelevated on many
// setups; anything unreadable stays "unknown" rather than guessed.
func (p *linuxProvider) enrichSmartctl(ctx context.Context, h DiskHealth) DiskHealth {
	smartctl := resolveCommand("smartctl")
	if smartctl == nil {
		return h
	}
	if o, ok := runSmartctlJSON(ctx, *smartctl, h.Device); ok {
		applySmartctlJSON(&h, o)
	}
	return h
}

// --- Startup entries (Linux) ---

// Linux startup locations, per the XDG autostart spec plus systemd user
// units - the same two locations a desktop session actually honors.
func (p *linuxProvider) ListStartupEntries(ctx context.Context) ([]StartupEntry, error) {
	var entries []StartupEntry

	dirs := xdgAutostartDirs()
	for _, dir := range dirs {
		scope := "user"
		if dir == "/etc/xdg/autostart" {
			scope = "system"
		}
		files, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, f := range files {
			if f.IsDir() || !strings.HasSuffix(f.Name(), ".desktop") {
				continue
			}
			data, err := os.ReadFile(dir + "/" + f.Name())
			if err != nil {
				continue
			}
			entry := parseDesktopAutostart(string(data), f.Name())
			if entry == nil {
				continue
			}
			entry.Location = "xdg-autostart"
			entry.Scope = scope
			entries = append(entries, *entry)
		}
	}

	// systemd user units (the modern location for user services).
	if out, err := exec.CommandContext(ctx, "systemctl", "--user", "list-unit-files", "--type=service", "--no-legend").Output(); err == nil {
		for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
			f := strings.Fields(line)
			if len(f) < 2 || !strings.HasSuffix(f[0], ".service") {
				continue
			}
			entries = append(entries, StartupEntry{
				ID:       f[0],
				Name:     strings.TrimSuffix(f[0], ".service"),
				Location: "systemd-user",
				Scope:    "user",
				Enabled:  f[1] == "enabled",
			})
		}
	}

	return entries, nil
}

// xdgAutostartDirs lists the XDG autostart directories in lookup order:
// user first (entries there shadow the system ones in a real session).
func xdgAutostartDirs() []string {
	dirs := []string{}
	if home := os.Getenv("XDG_CONFIG_HOME"); home != "" {
		dirs = append(dirs, home+"/autostart")
	} else if home := os.Getenv("HOME"); home != "" {
		dirs = append(dirs, home+"/.config/autostart")
	}
	dirs = append(dirs, "/etc/xdg/autostart")
	return dirs
}

// parseDesktopAutostart reads the [Desktop Entry] keys Weloury surfaces.
// Returns nil for files without a [Desktop Entry] section (theme dirs
// etc. also live in autostart dirs in theory) or entries marked Hidden.
func parseDesktopAutostart(content, filename string) *StartupEntry {
	inSection := false
	name, exec_, hidden := "", "", false
	for _, line := range strings.Split(content, "\n") {
		line = strings.TrimSpace(line)
		if line == "[Desktop Entry]" {
			inSection = true
			continue
		}
		if strings.HasPrefix(line, "[") {
			inSection = false
			continue
		}
		if !inSection {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key, value = strings.TrimSpace(key), strings.TrimSpace(value)
		switch key {
		case "Name":
			if name == "" {
				name = value
			}
		case "Exec":
			exec_ = value
		case "Hidden":
			hidden = value == "true"
		}
	}
	if exec_ == "" {
		return nil
	}
	if name == "" {
		name = strings.TrimSuffix(filename, ".desktop")
	}
	return &StartupEntry{
		ID:      filename,
		Name:    name,
		Command: exec_,
		Enabled: !hidden,
	}
}

func (p *linuxProvider) SetStartupEntryEnabled(ctx context.Context, id string, enabled bool) error {
	if strings.HasSuffix(id, ".service") {
		action := "disable"
		if enabled {
			action = "enable"
		}
		if out, err := exec.CommandContext(ctx, "systemctl", "--user", action, "--", id).CombinedOutput(); err != nil {
			return fmt.Errorf("systemctl --user %s %s: %v: %s", action, id, err, strings.TrimSpace(string(out)))
		}
		return nil
	}
	path := xdgEntryPath(id)
	if path == "" {
		return fmt.Errorf("startup entry %q: %w", id, ErrNotFound)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read %s: %w", path, err)
	}
	text := setDesktopEntryKey(string(data), "Hidden", fmt.Sprintf("%t", !enabled))
	if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}

func (p *linuxProvider) AddStartupEntry(ctx context.Context, name, exe string, args []string) error {
	dir := ""
	if home := os.Getenv("XDG_CONFIG_HOME"); home != "" {
		dir = home + "/autostart"
	} else if home := os.Getenv("HOME"); home != "" {
		dir = home + "/.config/autostart"
	} else {
		return fmt.Errorf("add startup entry: %w", ErrUnavailable)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create autostart dir: %w", err)
	}
	quoted := make([]string, 0, len(args))
	for _, a := range args {
		quoted = append(quoted, shellQuote(a))
	}
	command := exe
	if len(quoted) > 0 {
		command = exe + " " + strings.Join(quoted, " ")
	}
	slug := sanitizeEntryID(name)
	content := fmt.Sprintf("[Desktop Entry]\nType=Application\nVersion=1.0\nName=%s\nExec=%s\nTerminal=false\nX-GNOME-Autostart-enabled=true\n", name, command)
	if err := os.WriteFile(dir+"/"+slug+".desktop", []byte(content), 0o600); err != nil {
		return fmt.Errorf("write autostart entry: %w", err)
	}
	return nil
}

func (p *linuxProvider) RemoveStartupEntry(ctx context.Context, id string) error {
	if strings.HasSuffix(id, ".service") {
		// Removing a systemd user unit entirely would need the unit file
		// deleted from ~/.config/systemd/user - but unit files frequently
		// belong to installed packages; disabling is the supported way.
		return fmt.Errorf("removing a systemd user unit requires uninstalling it; use disable instead")
	}
	path := xdgEntryPath(id)
	if path == "" {
		return fmt.Errorf("startup entry %q: %w", id, ErrNotFound)
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove %s: %w", path, err)
	}
	return nil
}

// xdgEntryPath finds the user-dir .desktop file for id, preferring the
// user dir (entries there shadow /etc/xdg ones, so acting there is right).
func xdgEntryPath(id string) string {
	for _, dir := range xdgAutostartDirs() {
		path := dir + "/" + id
		if st, err := os.Stat(path); err == nil && !st.IsDir() {
			return path
		}
	}
	return ""
}

// setDesktopEntryKey rewrites one key's value inside the [Desktop Entry]
// section, appending it when absent - the inverse of parseDesktopAutostart.
func setDesktopEntryKey(content, key, value string) string {
	lines := strings.Split(content, "\n")
	inSection := false
	found := false
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "[Desktop Entry]" {
			inSection = true
			continue
		}
		if strings.HasPrefix(trimmed, "[") {
			inSection = false
			continue
		}
		if !inSection {
			continue
		}
		if k, _, ok := strings.Cut(trimmed, "="); ok && strings.TrimSpace(k) == key {
			lines[i] = key + "=" + value
			found = true
		}
	}
	if !found {
		// Append inside the section: after the last non-empty line.
		lines = append(lines, key+"="+value)
	}
	return strings.Join(lines, "\n")
}

// sanitizeEntryID reduces a display name to a filesystem-safe slug for
// the .desktop filename (the entry's ID).
func sanitizeEntryID(name string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(name) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == ' ', r == '-', r == '_', r == '.':
			b.WriteRune('-')
		}
	}
	slug := strings.Trim(b.String(), "-")
	if slug == "" {
		slug = "custom-entry"
	}
	return slug
}

// shellQuote single-quotes a value for a .desktop Exec line, per the
// Desktop Entry spec's quoting rules (only ' needs escaping inside a
// single-quoted string, written as '\'').
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\'`) + "'"
}
