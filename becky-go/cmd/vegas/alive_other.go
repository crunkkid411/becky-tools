//go:build !windows

package main

import (
	"os"
	"syscall"
)

// processAlive: VEGAS only runs on Windows; this keeps CI's Linux build honest.
func processAlive(pid int) bool {
	p, err := os.FindProcess(pid)
	return err == nil && p.Signal(syscall.Signal(0)) == nil
}
