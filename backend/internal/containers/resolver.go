package containers

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
)

// resolveDockerBin finds the `docker` CLI even when it is not on this
// process's PATH - the common real-world case on Windows, where Docker
// Desktop's per-user install (observed live:
// %LOCALAPPDATA%\Programs\DockerDesktop\resources\bin) frequently never
// reaches the PATH the Weloury core inherits, especially when the core
// is launched by the desktop shell rather than from a terminal (a Docker
// Desktop install after login appends the user PATH, which
// already-running parents never re-read). A daemon that IS reachable
// but whose CLI we can't find would leave the Containers tab
// inexplicably empty - the exact bug report that produced this file.
//
// Order: PATH first (respect the user's own setup), then Docker
// Desktop's per-user and per-machine install layouts, then the
// conventional Unix locations. Returns "" when nothing is found - the
// provider then still tries plain "docker" and surfaces the real error.
func resolveDockerBin() string {
	if path, err := exec.LookPath("docker"); err == nil && path != "" {
		return path
	}
	for _, candidate := range dockerCandidates() {
		if st, err := os.Stat(candidate); err == nil && !st.IsDir() {
			return candidate
		}
	}
	return ""
}

// dockerCandidates lists docker-CLI locations beyond PATH, most likely
// first. No build tags: every candidate is guarded by the environment
// it can only exist under (LOCALAPPDATA is unset on Unix, /Applications
// doesn't exist on Windows), so the same list works on all three
// platforms and os.Stat simply misses where inapplicable.
func dockerCandidates() []string {
	var out []string
	if runtime.GOOS == "windows" {
		if local := os.Getenv("LOCALAPPDATA"); local != "" {
			// Docker Desktop's per-user install (the default since the
			// 4.x installer gained the "install for me only" mode).
			out = append(out,
				filepath.Join(local, "Programs", "DockerDesktop", "resources", "bin", "docker.exe"),
				filepath.Join(local, "Docker", "resources", "bin", "docker.exe"),
			)
		}
		if pf := os.Getenv("ProgramFiles"); pf != "" {
			// The per-machine install (PATH entry present but only
			// picked up by processes started after the next login).
			out = append(out, filepath.Join(pf, "Docker", "Docker", "resources", "bin", "docker.exe"))
		}
		return out
	}
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		out = append(out,
			// Docker Desktop for Mac.
			filepath.Join(home, ".docker", "bin", "docker"),
			"/Applications/Docker.app/Contents/Resources/bin/docker",
			// Rootless installs and manual placements.
			filepath.Join(home, ".local", "bin", "docker"),
		)
	}
	return append(out, "/usr/local/bin/docker", "/usr/bin/docker")
}
