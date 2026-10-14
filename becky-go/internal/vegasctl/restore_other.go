//go:build !windows

package vegasctl

// Restore is a no-op off Windows (VEGAS is Windows-only).
func Restore() {}
