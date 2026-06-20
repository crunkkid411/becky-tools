package reaperbrain

import (
	"os"
	"os/exec"
	"path/filepath"
)

// NewResolver returns the real, os-backed Resolver used by the CLI.
func NewResolver() Resolver {
	return Resolver{
		Getenv:   os.Getenv,
		Exists:   func(p string) bool { _, err := os.Stat(p); return err == nil },
		LookPath: exec.LookPath,
		Glob:     filepath.Glob,
	}
}
