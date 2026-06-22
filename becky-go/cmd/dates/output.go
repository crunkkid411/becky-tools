// output.go — writing the JSON document to a file (the --output path).
package main

import (
	"encoding/json"
	"os"

	"becky-go/internal/beckyio"
)

// writeJSONFile writes the output document to path as indented JSON.
func writeJSONFile(path string, v any) {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		beckyio.Fatalf("failed to encode JSON: %v", err)
	}
	if err := os.WriteFile(path, append(data, '\n'), 0o644); err != nil {
		beckyio.Fatalf("failed to write %s: %v", path, err)
	}
}
