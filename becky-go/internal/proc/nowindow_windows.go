//go:build windows

// Package proc holds tiny cross-platform process-spawn helpers shared by the
// tools that shell out to ffmpeg/ffprobe/python/etc.
//
// NoWindow stops a child console process (ffmpeg, ffprobe, python, llama-server,
// becky-transcribe, ...) from popping its OWN console window when the PARENT has
// no console — which is exactly the becky-clip GUI case: the window exe is built
// with `-H windowsgui` (no console), so on Windows every console child would
// otherwise flash a black cmd box for a frame. CREATE_NO_WINDOW makes the child
// run with no console at all. Output is unaffected because every caller captures
// stdout/stderr via pipes/buffers, so nothing is lost by removing the console.
package proc

import (
	"os/exec"
	"syscall"
)

// createNoWindow is the Win32 CREATE_NO_WINDOW process-creation flag: the child
// runs without allocating a console window.
const createNoWindow = 0x08000000

// NoWindow marks cmd so its child process spawns without a console window. It is
// idempotent and preserves any other SysProcAttr fields a caller already set.
func NoWindow(cmd *exec.Cmd) {
	if cmd == nil {
		return
	}
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.HideWindow = true
	cmd.SysProcAttr.CreationFlags |= createNoWindow
}
