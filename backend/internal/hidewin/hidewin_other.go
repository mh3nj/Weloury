//go:build !windows

package hidewin

import "os/exec"

// Configure is a no-op on platforms without console windows - Unix
// children of a daemon have no window surface to appear in. It exists so
// call sites can configure every spawn unconditionally without build
// tags of their own.
func Configure(cmd *exec.Cmd) {}
