//go:build !windows

package sequence

import "os/exec"

// hideConsoleWindow is a no-op outside Windows, where a child process never
// gets a console window of its own.
func hideConsoleWindow(*exec.Cmd) {}
