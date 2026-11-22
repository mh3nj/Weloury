//go:build darwin

package system

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"weloury/internal/hidewin"
)

// macOS GPU support: system_profiler carries the adapter inventory
// (Apple Silicon's integrated GPUs included, which no PCI listing
// shows); nvidia-smi leads for NVIDIA hardware where present (see
// gpu_nvidia.go). Live telemetry for Apple GPUs stays an honest
// ErrUnavailable - reading it means IOKit/IOAcceleration private
// selectors, not a documented surface.

func (p *darwinProvider) ListGPUs(ctx context.Context) ([]GPUInfo, error) {
	cmd := exec.Command("system_profiler", "SPDisplaysDataType", "-json")
	hidewin.Configure(cmd)
	out, err := cmd.Output()
	if err != nil {
		// system_profiler absent/failing: nvidia-smi is still worth a try.
		if nvidiaGPUs, _ := sampleNvidiaGPUs(ctx); len(nvidiaGPUs) > 0 {
			return nvidiaGPUs, nil
		}
		return []GPUInfo{}, nil
	}

	gpus := parseSystemProfilerGPUs(out)
	for i := range gpus {
		gpus[i].Index = i
	}
	return gpus, nil
}

func (p *darwinProvider) GPUUsage(ctx context.Context, index int) (GPUUsage, error) {
	gpus, err := p.ListGPUs(ctx)
	if err != nil {
		return GPUUsage{}, err
	}
	if index < 0 || index >= len(gpus) {
		return GPUUsage{}, fmt.Errorf("gpu %d: %w", index, ErrNotFound)
	}
	target := gpus[index]
	if strings.Contains(strings.ToLower(target.Vendor), "nvidia") {
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

// --- Disk health (macOS) ---

// Disk health on macOS: `smartctl` from smartmontools (if the user
// installed it; Homebrew ships it) reads both Intel/Apple T2 and NVMe
// drives; `diskutil info` supplies the model/inventory without root.
// Without smartctl the listing degrades to inventory-only rows with
// Status "unknown" rather than failing the whole call - a partial
// answer is still an answer (see DiskHealthProvider's contract).
func (p *darwinProvider) DiskHealth(ctx context.Context) ([]DiskHealth, error) {
	// -plist keeps the output machine-readable; each diskN gets its own
	// dict under "AllDisksAndPartitions" - we only need the top-level
	// device identity, so text parsing of `diskutil list` is avoided.
	out, err := exec.CommandContext(ctx, "diskutil", "list", "-plist").Output()
	if err != nil {
		return nil, fmt.Errorf("list disks: %w", err)
	}
	devices := parseDiskUtilList(out)

	smartctl := resolveCommand("smartctl")
	var healths []DiskHealth
	for _, dev := range devices {
		h := DiskHealth{
			Device: "/dev/" + dev,
			Status: "unknown",
		}
		h = p.enrichDiskUtil(ctx, h)
		if smartctl != nil {
			h = enrichSmartctlDarwin(ctx, *smartctl, h)
		}
		healths = append(healths, h)
	}
	return healths, nil
}

// enrichDiskUtil pulls the model name (and internal/external hint) from
// `diskutil info -plist /dev/diskN`.
func (p *darwinProvider) enrichDiskUtil(ctx context.Context, h DiskHealth) DiskHealth {
	out, err := exec.CommandContext(ctx, "diskutil", "info", "-plist", h.Device).Output()
	if err != nil {
		return h
	}
	if model, ok := plistStringForKey(out, "DeviceName"); ok {
		h.Model = model
	}
	if internal, ok := plistBoolForKey(out, "Internal"); ok {
		if internal {
			// BusType stays empty for Apple's proprietary internal
			// transports; claiming "NVMe" or "SATA" without reading it
			// would be a guess.
			h.BusType = "Internal"
		} else {
			h.BusType = "USB"
		}
	}
	return h
}

// enrichSmartctlDarwin runs the JSON health+attributes query and folds
// whatever smartctl reads into the row, the same shared logic Linux and
// Windows use (smartctl's JSON is identical across the three OSes).
func enrichSmartctlDarwin(ctx context.Context, smartctl string, h DiskHealth) DiskHealth {
	if o, ok := runSmartctlJSON(ctx, smartctl, h.Device); ok {
		applySmartctlJSON(&h, o)
	}
	return h
}

// parseDiskUtilList extracts whole-disk identifiers ("disk0", "disk1")
// from `diskutil list -plist`'s AllDisksAndPartitions array (the XML is
// flat and regular; a targeted scan of <string> nodes after the
// "DeviceIdentifier" keys is enough for this fixed shape).
func parseDiskUtilList(data []byte) []string {
	var disks []string
	text := string(data)
	key := "<string>"
	for {
		i := strings.Index(text, "DeviceIdentifier")
		if i < 0 {
			break
		}
		text = text[i+len("DeviceIdentifier"):]
		j := strings.Index(text, key)
		if j < 0 {
			break
		}
		text = text[j+len(key):]
		k := strings.Index(text, "</string>")
		if k < 0 {
			break
		}
		id := strings.TrimSpace(text[:k])
		// Whole disks only ("disk0"), not partitions/slices ("disk0s1").
		if strings.Contains(id, "s") {
			continue
		}
		disks = append(disks, id)
	}
	return disks
}

// plistStringForKey reads one <string> value following key from
// diskutil's plist output (same fixed-shape scan as parseDiskUtilList).
func plistStringForKey(data []byte, key string) (string, bool) {
	text := string(data)
	i := strings.Index(text, "<key>"+key+"</key>")
	if i < 0 {
		return "", false
	}
	rest := text[i+len("<key>"+key+"</key>"):]
	j := strings.Index(rest, "<string>")
	if j < 0 {
		return "", false
	}
	rest = rest[j+len("<string>"):]
	k := strings.Index(rest, "</string>")
	if k < 0 {
		return "", false
	}
	return strings.TrimSpace(rest[:k]), true
}

// plistBoolForKey reads one <true/>/<false/> following key.
func plistBoolForKey(data []byte, key string) (bool, bool) {
	text := string(data)
	i := strings.Index(text, "<key>"+key+"</key>")
	if i < 0 {
		return false, false
	}
	rest := text[i:]
	if strings.HasPrefix(rest, "<key>"+key+"</key><true/>") {
		return true, true
	}
	if strings.HasPrefix(rest, "<key>"+key+"</key><false/>") {
		return false, true
	}
	return false, false
}

// --- Startup entries (macOS) ---

// macOS startup locations: per-user LaunchAgents first (the only scope
// a non-elevated Weloury can act on), then the system one (reported,
// not editable - acting on it needs root, surfaced as an error per the
// no-self-elevation rule).
func (p *darwinProvider) ListStartupEntries(ctx context.Context) ([]StartupEntry, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("resolve home: %w", err)
	}
	var entries []StartupEntry
	for _, dir := range []string{
		home + "/Library/LaunchAgents",
		"/Library/LaunchAgents",
		"/Library/LaunchDaemons",
	} {
		scope := "system"
		if dir == home+"/Library/LaunchAgents" {
			scope = "user"
		}
		files, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, f := range files {
			if f.IsDir() || !strings.HasSuffix(f.Name(), ".plist") {
				continue
			}
			data, err := os.ReadFile(dir + "/" + f.Name())
			if err != nil {
				continue
			}
			label, _ := plistStringForKey(data, "Label")
			if label == "" {
				continue
			}
			program, _ := plistStringForKey(data, "Program")
			if program == "" {
				// ProgramArguments form: join the strings for display.
				program = plistProgramArguments(data)
			}
			runAtLoad, _ := plistBoolForKey(data, "RunAtLoad")
			keepAlive, _ := plistBoolForKey(data, "KeepAlive")
			entries = append(entries, StartupEntry{
				ID:       label,
				Name:     label,
				Command:  program,
				Location: "launch-" + strings.ToLower(strings.TrimSuffix(dir[strings.LastIndex(dir, "/")+1:], "s")),
				Scope:    scope,
				// RunAtLoad (or KeepAlive) is what makes it a startup
				// entry; launchd loads everything in the dir, so a plist
				// without either isn't an autostart item.
				Enabled: runAtLoad || keepAlive,
			})
		}
	}
	return entries, nil
}

// plistProgramArguments joins the <array> strings following the
// ProgramArguments key for display.
func plistProgramArguments(data []byte) string {
	text := string(data)
	i := strings.Index(text, "<key>ProgramArguments</key>")
	if i < 0 {
		return ""
	}
	rest := text[i:]
	j := strings.Index(rest, "<array>")
	if j < 0 {
		return ""
	}
	k := strings.Index(rest, "</array>")
	if k < 0 {
		return ""
	}
	block := rest[j:k]
	var parts []string
	for {
		s := strings.Index(block, "<string>")
		if s < 0 {
			break
		}
		block = block[s+len("<string>"):]
		e := strings.Index(block, "</string>")
		if e < 0 {
			break
		}
		parts = append(parts, block[:e])
		block = block[e:]
	}
	return strings.Join(parts, " ")
}

func (p *darwinProvider) SetStartupEntryEnabled(ctx context.Context, id string, enabled bool) error {
	label := id
	if label == "" {
		return fmt.Errorf("startup entry %q: %w", id, ErrNotFound)
	}
	action := "disable"
	if enabled {
		action = "enable"
	}
	// launchctl enable/disable marks the label in the override database
	// without touching the plist - the supported, non-destructive way.
	cmd := exec.CommandContext(ctx, "launchctl", action, "gui/$UID/"+label)
	hidewin.Configure(cmd)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("launchctl %s %s: %v: %s", action, label, err, strings.TrimSpace(string(out)))
	}
	return nil
}

func (p *darwinProvider) AddStartupEntry(ctx context.Context, name, exe string, args []string) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("resolve home: %w", err)
	}
	label := startupEntryLabel(name)
	path := home + "/Library/LaunchAgents/" + label + ".plist"
	var sb strings.Builder
	sb.WriteString("<?xml version=\"1.0\" encoding=\"UTF-8\"?>\n")
	sb.WriteString("<!DOCTYPE plist PUBLIC \"-//Apple//DTD PLIST 1.0//EN\" \"http://www.apple.com/DTDs/PropertyList-1.0.dtd\">\n")
	sb.WriteString("<plist version=\"1.0\">\n<dict>\n")
	sb.WriteString("    <key>Label</key>\n    <string>" + label + "</string>\n")
	sb.WriteString("    <key>ProgramArguments</key>\n    <array>\n")
	sb.WriteString("        <string>" + xmlEscape(exe) + "</string>\n")
	for _, a := range args {
		sb.WriteString("        <string>" + xmlEscape(a) + "</string>\n")
	}
	sb.WriteString("    </array>\n")
	sb.WriteString("    <key>RunAtLoad</key>\n    <true/>\n")
	sb.WriteString("</dict>\n</plist>\n")
	if err := os.WriteFile(path, []byte(sb.String()), 0o644); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	// Bootstrapping (registering without re-login) needs `launchctl
	// bootstrap gui/$UID` - best-effort, errors here don't undo the file.
	cmd := exec.CommandContext(ctx, "launchctl", "bootstrap", "gui/$UID", path)
	hidewin.Configure(cmd)
	_, _ = cmd.CombinedOutput()
	return nil
}

func (p *darwinProvider) RemoveStartupEntry(ctx context.Context, id string) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("resolve home: %w", err)
	}
	// Only files under the user's LaunchAgents are removable - a system
	// LaunchDaemon plist would need root and must never be touched by a
	// non-elevated process (SECURITY.md).
	path := home + "/Library/LaunchAgents/" + id + ".plist"
	if _, err := os.Stat(path); err != nil {
		return fmt.Errorf("startup entry %q: %w", id, ErrNotFound)
	}
	cmd := exec.CommandContext(ctx, "launchctl", "bootout", "gui/$UID", path)
	hidewin.Configure(cmd)
	_, _ = cmd.CombinedOutput()
	if err := os.Remove(path); err != nil {
		return fmt.Errorf("remove %s: %w", path, err)
	}
	return nil
}

// startupEntryLabel derives a launchd label ("com.weloury.custom.foo")
// from a display name.
func startupEntryLabel(name string) string {
	var b strings.Builder
	b.WriteString("com.weloury.custom.")
	for _, r := range strings.ToLower(name) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == ' ', r == '-', r == '_', r == '.':
			b.WriteRune('-')
		}
	}
	return strings.Trim(b.String(), "-")
}

// xmlEscape escapes the five XML specials for plist string values.
func xmlEscape(s string) string {
	r := strings.NewReplacer(
		"&", "&amp;",
		"<", "&lt;",
		">", "&gt;",
		`"`, "&quot;",
		"'", "&apos;",
	)
	return r.Replace(s)
}
