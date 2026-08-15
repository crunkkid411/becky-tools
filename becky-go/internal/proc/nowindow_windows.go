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
//
// It also launches every child at BELOW_NORMAL_PRIORITY_CLASS (BUILD_1.md
// Functionality Contract I-8 / spec 3.4 P3: "external helper processes (ffmpeg
// etc.) are launched at BELOW_NORMAL_PRIORITY_CLASS or lower" — dropping CPU
// priority is Microsoft's documented fix for a background disk-I/O process
// stalling the OS mouse cursor). Every ffmpeg/ffprobe spawn site already routes
// through this one function, so the priority drop is applied once, here, rather
// than re-added at each of the ~20 call sites.
package proc

import (
	"os/exec"
	"syscall"
)

const (
	// createNoWindow is the Win32 CREATE_NO_WINDOW process-creation flag: the
	// child runs without allocating a console window.
	createNoWindow = 0x08000000
	// belowNormalPriorityClass is the Win32 BELOW_NORMAL_PRIORITY_CLASS
	// process-creation flag.
	belowNormalPriorityClass = 0x00004000
)

// NoWindow marks cmd so its child process spawns without a console window, at
// below-normal priority. It is idempotent and preserves any other SysProcAttr
// fields a caller already set.
func NoWindow(cmd *exec.Cmd) {
	if cmd == nil {
		return
	}
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.HideWindow = true
	cmd.SysProcAttr.CreationFlags |= createNoWindow | belowNormalPriorityClass
}
