//go:build windows

package system

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/windows/registry"
)

// Windows startup locations, exactly what Task Manager's Startup tab
// reads (Task Manager's own enable/disable state lives in the parallel
// StartupApproved subkey - a binary blob whose first byte is 2 for
// enabled, 3 for disabled; see startupApprovedValue below). Only per-user
// locations are writable without elevation; the machine-scope Run key is
// listed read-only and toggling it surfaces a permission error rather
// than silently succeeding or self-elevating (SECURITY.md).
func (p *windowsProvider) ListStartupEntries(ctx context.Context) ([]StartupEntry, error) {
	var entries []StartupEntry

	// HKCU Run - the user's own autostarts (writable).
	entries = append(entries, readRunKey(registry.CURRENT_USER, "registry-run", "user")...)

	// HKLM Run - machine-wide autostarts (read-only for us).
	entries = append(entries, readRunKey(registry.LOCAL_MACHINE, "registry-run", "system")...)

	// Startup folders - per-user and all-users. Files there always run
	// (no enable state of their own beyond StartupApproved, which
	// readStartupApproved overlays).
	entries = append(entries, readStartupFolder(startupFolderPath(false), "startup-folder", "user")...)
	entries = append(entries, readStartupFolder(startupFolderPath(true), "startup-folder", "system")...)

	// Overlay StartupApproved disabled state by entry ID.
	disabled := readStartupApprovedSet()
	for i := range entries {
		if _, off := disabled[entries[i].ID]; off {
			entries[i].Enabled = false
		}
	}

	return entries, nil
}

// readRunKey enumerates one Run key's values into StartupEntries. The
// value name is the entry ID (what Task Manager shows as the "name").
func readRunKey(root registry.Key, location, scope string) []StartupEntry {
	key := "Software\\Microsoft\\Windows\\CurrentVersion\\Run"
	k, err := registry.OpenKey(root, key, registry.QUERY_VALUE)
	if err != nil {
		return nil
	}
	defer k.Close()

	names, err := k.ReadValueNames(-1)
	if err != nil {
		return nil
	}
	entries := make([]StartupEntry, 0, len(names))
	for _, name := range names {
		val, _, err := k.GetStringValue(name)
		if err != nil {
			continue // non-string values (REG_BINARY etc.) aren't launch entries
		}
		entries = append(entries, StartupEntry{
			ID:       runKeyPrefix(root) + name,
			Name:     name,
			Command:  strings.TrimSpace(val),
			Location: location,
			Scope:    scope,
			Enabled:  true, // StartupApproved overlay adjusts below
		})
	}
	return entries
}

// runKeyPrefix distinguishes the two Run keys' value names, which can
// collide (an HKCU entry and an HKLM entry may share a name).
func runKeyPrefix(root registry.Key) string {
	if root == registry.LOCAL_MACHINE {
		return "hklm:"
	}
	return "hkcu:"
}

// startupFolderPath resolves the Startup shell folder without shelling
// out: the documented per-user location is %APPDATA%\Microsoft\Windows\
// Start Menu\Programs\Startup and the all-users one is under
// %ProgramData% (the CommonProgramFiles-free documented default; exotic
// redirected profiles would need SHGetKnownFolderPath - out of scope
// for a stdlib-only read).
func startupFolderPath(allUsers bool) string {
	if allUsers {
		if pd := os.Getenv("ProgramData"); pd != "" {
			return filepath.Join(pd, "Microsoft", "Windows", "Start Menu", "Programs", "StartUp")
		}
		return ""
	}
	if ad := os.Getenv("APPDATA"); ad != "" {
		return filepath.Join(ad, "Microsoft", "Windows", "Start Menu", "Programs", "Startup")
	}
	return ""
}

// readStartupFolder lists .lnk/.bat/.cmd/.exe files in one Startup
// folder as startup entries (the file name is the entry ID; shortcuts'
// targets would need the shell link API, so Command stays the path).
func readStartupFolder(dir, location, scope string) []StartupEntry {
	if dir == "" {
		return nil
	}
	files, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var entries []StartupEntry
	for _, f := range files {
		if f.IsDir() {
			continue
		}
		ext := strings.ToLower(filepath.Ext(f.Name()))
		if ext != ".lnk" && ext != ".bat" && ext != ".cmd" && ext != ".exe" {
			continue
		}
		base := strings.TrimSuffix(f.Name(), filepath.Ext(f.Name()))
		entries = append(entries, StartupEntry{
			ID:       "folder:" + f.Name(),
			Name:     base,
			Command:  filepath.Join(dir, f.Name()),
			Location: location,
			Scope:    scope,
			Enabled:  true,
		})
	}
	return entries
}

// startupApprovedRunKey is where Windows records Task Manager's
// enable/disable choices: the same Run path with "StartupApproved\Run"
// appended, one binary value per entry.
const startupApprovedRunKey = "Software\\Microsoft\\Windows\\CurrentVersion\\Explorer\\StartupApproved\\Run"

// startupApprovedFolderKey mirrors it for Startup-folder entries.
const startupApprovedFolderKey = "Software\\Microsoft\\Windows\\CurrentVersion\\Explorer\\StartupApproved\\StartupFolder"

// readStartupApprovedSet returns the set of entry IDs the OS marks
// disabled (value exists and its first byte is 3). Values in
// StartupApproved are keyed by the Run value name, or the .lnk file
// name for Startup-folder entries - matching how we mint IDs above
// (minus the hkcu:/hklm: prefix, applied only on lookup).
func readStartupApprovedSet() map[string]struct{} {
	disabled := make(map[string]struct{})
	for _, keyPath := range []string{startupApprovedRunKey, startupApprovedFolderKey} {
		k, err := registry.OpenKey(registry.CURRENT_USER, keyPath, registry.QUERY_VALUE)
		if err != nil {
			continue
		}
		names, err := k.ReadValueNames(-1)
		if err != nil {
			k.Close()
			continue
		}
		for _, name := range names {
			val, _, err := k.GetBinaryValue(name)
			if err != nil || len(val) == 0 {
				continue
			}
			if val[0] == 0x03 {
				disabled[name] = struct{}{}
			}
		}
		k.Close()
	}
	return disabled
}

// startupApprovedValue builds the 12-byte binary blob StartupApproved
// uses for a disabled entry: byte 0 = 3 (disabled), then the FILETIME
// of the change (Windows fills 6-7 with timestamp high bits; zeros are
// accepted - reg.exe-written entries commonly carry them).
func startupApprovedValue() []byte {
	blob := make([]byte, 12)
	blob[0] = 0x03
	return blob
}

func (p *windowsProvider) SetStartupEntryEnabled(ctx context.Context, id string, enabled bool) error {
	// Strip the scope prefix to get the StartupApproved value name.
	name := strings.TrimPrefix(id, "hkcu:")
	isFolder := strings.HasPrefix(id, "folder:")
	if isFolder {
		name = strings.TrimPrefix(name, "folder:")
	}

	if strings.HasPrefix(id, "hklm:") {
		return fmt.Errorf("machine-scope startup entries require an elevated process; Weloury never self-elevates (see SECURITY.md)")
	}
	if name == "" {
		return fmt.Errorf("startup entry %q: %w", id, ErrNotFound)
	}

	keyPath := startupApprovedRunKey
	if isFolder {
		keyPath = startupApprovedFolderKey
	}
	k, err := registry.OpenKey(registry.CURRENT_USER, keyPath, registry.SET_VALUE)
	if err != nil {
		// The subkey doesn't exist until something was disabled once -
		// creating it is the documented, per-user, non-elevated action
		// Task Manager itself performs. (CreateKey wants the access mask
		// explicitly; OpenKey's deny here means the key is absent.)
		k, _, err = registry.CreateKey(registry.CURRENT_USER, keyPath, registry.SET_VALUE)
		if err != nil {
			return fmt.Errorf("open %s: %w", keyPath, err)
		}
	}
	defer k.Close()

	if enabled {
		// "Enabled" means the entry is NOT in StartupApproved - delete
		// the override value (Task Manager does exactly this).
		if err := k.DeleteValue(name); err != nil && err != registry.ErrNotExist {
			return fmt.Errorf("clear startup approval for %s: %w", name, err)
		}
		return nil
	}
	if err := k.SetBinaryValue(name, startupApprovedValue()); err != nil {
		return fmt.Errorf("disable startup entry %s: %w", name, err)
	}
	return nil
}

// AddStartupEntry registers a custom autostart in HKCU's Run key - the
// same location autostart.rs uses for Weloury's own entry, so the
// entry shows up both in Task Manager and in this listing.
func (p *windowsProvider) AddStartupEntry(ctx context.Context, name, exe string, args []string) error {
	if strings.TrimSpace(name) == "" {
		return fmt.Errorf("startup entry name is required")
	}
	command := `"` + exe + `"`
	if len(args) > 0 {
		command += " " + strings.Join(quoteWindowsArgs(args), " ")
	}
	k, err := registry.OpenKey(registry.CURRENT_USER, "Software\\Microsoft\\Windows\\CurrentVersion\\Run", registry.SET_VALUE)
	if err != nil {
		return fmt.Errorf("open Run key: %w", err)
	}
	defer k.Close()
	if err := k.SetStringValue(name, command); err != nil {
		return fmt.Errorf("register startup entry %q: %w", name, err)
	}
	return nil
}

// RemoveStartupEntry deletes a Run-key value (user scope only) or a
// Startup-folder file. Entries under HKLM need elevation and error out.
func (p *windowsProvider) RemoveStartupEntry(ctx context.Context, id string) error {
	switch {
	case strings.HasPrefix(id, "folder:"):
		fileName := strings.TrimPrefix(id, "folder:")
		dir := startupFolderPath(false)
		if dir == "" {
			return fmt.Errorf("startup folder: %w", ErrUnavailable)
		}
		if err := os.Remove(filepath.Join(dir, fileName)); err != nil {
			return fmt.Errorf("remove startup entry %s: %w", fileName, err)
		}
		return nil
	case strings.HasPrefix(id, "hklm:"):
		return fmt.Errorf("machine-scope startup entries require an elevated process; Weloury never self-elevates (see SECURITY.md)")
	case strings.HasPrefix(id, "hkcu:"):
		k, err := registry.OpenKey(registry.CURRENT_USER, "Software\\Microsoft\\Windows\\CurrentVersion\\Run", registry.SET_VALUE)
		if err != nil {
			return fmt.Errorf("open Run key: %w", err)
		}
		defer k.Close()
		name := strings.TrimPrefix(id, "hkcu:")
		if err := k.DeleteValue(name); err != nil {
			return fmt.Errorf("remove startup entry %s: %w", name, err)
		}
		// Clean up any StartupApproved override with it.
		if ak, err := registry.OpenKey(registry.CURRENT_USER, startupApprovedRunKey, registry.SET_VALUE); err == nil {
			_ = ak.DeleteValue(name)
			ak.Close()
		}
		return nil
	default:
		return fmt.Errorf("startup entry %q: %w", id, ErrNotFound)
	}
}

// quoteWindowsArgs applies the CRT argv quoting rules for a command
// line (what Registry Run entries are): backslashes before a quote are
// doubled, quotes are escaped, and the whole argument is wrapped when
// it contains a space.
func quoteWindowsArgs(args []string) []string {
	quoted := make([]string, 0, len(args))
	for _, a := range args {
		if !strings.ContainsAny(a, " \t\"") {
			quoted = append(quoted, a)
			continue
		}
		var b strings.Builder
		b.WriteByte('"')
		backslashes := 0
		for _, c := range a {
			switch c {
			case '\\':
				backslashes++
				b.WriteByte('\\')
			case '"':
				for i := 0; i < backslashes; i++ {
					b.WriteByte('\\')
				}
				backslashes = 0
				b.WriteString(`\"`)
			default:
				backslashes = 0
				b.WriteRune(c)
			}
		}
		for i := 0; i < backslashes; i++ {
			b.WriteByte('\\')
		}
		b.WriteByte('"')
		quoted = append(quoted, b.String())
	}
	return quoted
}
