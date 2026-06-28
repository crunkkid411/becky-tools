package imagegen

import (
	"os/exec"

	"becky-go/internal/proc"
)

// newCmd builds the sd-cli command with the Windows console-window flash
// suppressed (proc.NoWindow is a no-op off Windows), matching every other becky
// tool that shells out so a GUI launcher never pops a black cmd box.
func newCmd(bin string, args []string) *exec.Cmd {
	cmd := exec.Command(bin, args...)
	proc.NoWindow(cmd)
	return cmd
}
