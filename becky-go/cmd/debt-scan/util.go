// util.go — tiny stdlib-only helpers used across the analyzers (int<->string,
// set construction). Kept separate so the analyzers read cleanly.
package main

import "strconv"

// itoa is strconv.Itoa under a short name (findings build many small messages).
func itoa(n int) string { return strconv.Itoa(n) }

// atoi parses an int, returning 0 on error (callers treat 0 as "unknown").
func atoi(s string) int {
	n, err := strconv.Atoi(s)
	if err != nil {
		return 0
	}
	return n
}

// stringSet turns a slice into a membership set, ignoring empty entries.
func stringSet(items []string) map[string]bool {
	set := map[string]bool{}
	for _, it := range items {
		if it != "" {
			set[it] = true
		}
	}
	return set
}

// contains reports whether s is in list.
func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}
