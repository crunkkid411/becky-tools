// Package beckyio holds the output conventions shared by every becky tool:
// structured JSON to stdout, human diagnostics to stderr, non-zero exit on
// fatal errors. Keeping this in one place is what makes the tools chainable.
package beckyio

import (
	"encoding/json"
	"fmt"
	"os"
)

// PrintJSON writes v to stdout as indented JSON followed by a newline.
func PrintJSON(v any) {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(v); err != nil {
		Fatalf("failed to encode JSON: %v", err)
	}
}

// Fatalf prints an error to stderr and exits with status 1.
func Fatalf(format string, a ...any) {
	fmt.Fprintf(os.Stderr, "error: "+format+"\n", a...)
	os.Exit(1)
}

// Logf prints a progress line to stderr only when verbose is set.
func Logf(verbose bool, format string, a ...any) {
	if verbose {
		fmt.Fprintf(os.Stderr, format+"\n", a...)
	}
}
