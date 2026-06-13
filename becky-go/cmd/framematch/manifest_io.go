// manifest_io.go — JSON serialization for the becky-framematch Manifest. Kept
// tiny and separate so manifest.go stays a pure schema file. The on-disk
// manifest.json and the --output file use the exact same bytes as stdout, so a
// re-run with adjusted params (threshold / interval / enhance) overwrites a
// directly-comparable record — the loop.
package main

import (
	"encoding/json"
	"fmt"
)

// marshalIndent renders the manifest as indented JSON with a trailing newline.
func marshalIndent(m Manifest) ([]byte, error) {
	b, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("marshal manifest: %w", err)
	}
	return append(b, '\n'), nil
}
