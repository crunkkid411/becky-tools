//go:build windows

package main

import "syscall"

// processAlive reports whether pid is a running process. OpenProcess fails for a
// pid that no longer exists; STILL_ACTIVE (259) separates a live process from one
// that exited but still has a handle open somewhere.
func processAlive(pid int) bool {
	const queryLimited = 0x1000 // PROCESS_QUERY_LIMITED_INFORMATION
	h, err := syscall.OpenProcess(queryLimited, false, uint32(pid))
	if err != nil {
		return false
	}
	defer syscall.CloseHandle(h)
	var code uint32
	if err := syscall.GetExitCodeProcess(h, &code); err != nil {
		return false
	}
	return code == 259
}
