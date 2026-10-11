//go:build windows

package whois

import (
	"os/exec"
	"syscall"
)

// detach lets the popup outlive the tool that opened it (becky-identify returns at once).
// Not proc.NoWindow: its HideWindow would hide the popup itself.
func detach(cmd *exec.Cmd) {
	const detachedProcess, newGroup = 0x00000008, 0x00000200
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: detachedProcess | newGroup}
}
