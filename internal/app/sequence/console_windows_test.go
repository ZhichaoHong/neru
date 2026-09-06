//go:build windows

package sequence

import (
	"os/exec"
	"testing"
)

// The daemon has no console of its own, so Windows allocates one for the shell
// behind an exec step and the user sees it flash. CREATE_NO_WINDOW is the only
// thing preventing that, hence this pin.
func TestHideConsoleWindowSetsCreateNoWindow(t *testing.T) {
	t.Parallel()

	command := exec.Command("cmd.exe")

	hideConsoleWindow(command)

	if command.SysProcAttr == nil {
		t.Fatal("hideConsoleWindow() left SysProcAttr nil")
	}

	if command.SysProcAttr.CreationFlags&createNoWindow == 0 {
		t.Errorf(
			"CreationFlags = %#x, want CREATE_NO_WINDOW (%#x) set",
			command.SysProcAttr.CreationFlags,
			createNoWindow,
		)
	}
}
