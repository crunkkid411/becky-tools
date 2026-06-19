//go:build !windows

package proc

import "os/exec"

// NoWindow is a no-op on non-Windows platforms (there is no console-window flash
// to suppress). It exists so callers can call proc.NoWindow(cmd) unconditionally.
func NoWindow(cmd *exec.Cmd) {}
