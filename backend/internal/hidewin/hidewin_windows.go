//go:build windows

// Package hidewin configures subprocess spawns to never surface a console
// window. Weloury core is a windowless background process, but every
// child it spawns (powershell, docker, reg, tasklist, ...) would normally
// be allocated a brand-new visible console by Windows - one black box
// flashing per call, several per second while a page polls. This package
// is the one place that knows how to prevent that, and every command
// spawn in the codebase routes through it (the unix build is a no-op so
// call sites stay platform-agnostic).
package hidewin

import (
	"os/exec"
	"syscall"
)

// createNoWindow is Windows' CREATE_NO_WINDOW process-creation flag
// (0x08000000 per the Microsoft Process Creation Constants docs). The Go
// syscall package deliberately doesn't export it, so it's defined here
// once, next to its only use.
const createNoWindow = 0x0800_0000

// Configure marks cmd so Windows creates no console window for the child
// and hides any it would inherit. CREATE_NO_WINDOW plus HideWindow is
// the documented pairing for "run a console subprocess invisibly from a
// GUI process"; CREATE_NEW_PROCESS_GROUP is deliberately not set (it
// would change Ctrl+C semantics for children that care).
func Configure(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{
		CreationFlags: createNoWindow,
		HideWindow:    true,
	}
}
