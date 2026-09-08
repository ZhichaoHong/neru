//go:build windows

package sequence

import (
	"os/exec"
	"syscall"
)

// createNoWindow is CREATE_NO_WINDOW: run a console application without giving
// it a console.
const createNoWindow = 0x08000000

// hideConsoleWindow keeps the shell behind an exec step off the screen. The
// daemon has no console of its own, so Windows would allocate a fresh one for
// cmd.exe and the user would see it flash on every step. The step's output is
// piped, so nothing needs a console to write to.
func hideConsoleWindow(command *exec.Cmd) {
	command.SysProcAttr = &syscall.SysProcAttr{CreationFlags: createNoWindow}
}
