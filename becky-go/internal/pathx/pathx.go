// Package pathx provides separator-agnostic path helpers.
//
// becky-tools runs in production on Windows, where paths use '\', but the unit
// tests and CI run on Linux, where the standard library's path/filepath treats
// only '/' as a separator. That mismatch silently breaks any helper that calls
// filepath.Base/Dir on a Windows path while running on Linux (filepath.Base of
// `C:\dir\file.jpg` returns the whole string, not `file.jpg`).
//
// These helpers treat BOTH '/' and '\' as separators regardless of host OS, so
// a display name or basename is derived correctly no matter where the tool runs
// or which platform produced the path. Use them whenever the input path may have
// originated on a different OS than the one currently executing.
package pathx

import "strings"

// Base returns the final element of p, treating both '/' and '\' as separators.
// It returns p unchanged when p contains no separator. Unlike filepath.Base it
// does not collapse "" to "." — an empty input yields "".
func Base(p string) string {
	if i := strings.LastIndexAny(p, `/\`); i >= 0 {
		return p[i+1:]
	}
	return p
}

// Dir returns everything before the final separator in p, treating both '/' and
// '\' as separators. It returns "" when p has no separator.
func Dir(p string) string {
	if i := strings.LastIndexAny(p, `/\`); i >= 0 {
		return p[:i]
	}
	return ""
}

// IsAbs reports whether p is absolute under EITHER OS convention: a rooted
// POSIX path ("/x"), a Windows drive path (`C:\x` or `C:/x`), or a UNC path
// (`\\server\share`). filepath.IsAbs answers only for the HOST OS, so on Linux
// it calls a `X:\...` path relative — which reads as "resolves against the
// cwd" in tests that guard against exactly that bug.
func IsAbs(p string) bool {
	if len(p) >= 1 && (p[0] == '/' || p[0] == '\\') {
		return true
	}
	if len(p) >= 3 && p[1] == ':' && (p[2] == '/' || p[2] == '\\') {
		c := p[0]
		return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
	}
	return false
}
