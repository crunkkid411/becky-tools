//go:build !windows

package main

// enableANSI is a no-op off Windows (real terminals there already handle
// ANSI escapes natively).
func enableANSI() {}
