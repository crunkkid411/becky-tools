// becky-ctx — report currently-open Windows File Explorer folders as JSON.
//
// Usage:
//
//	becky-ctx
//
// Output (JSON array on stdout, one object per open Explorer window):
//
//	[{"path":"C:\\Footage","title":"Footage"},...]
//
// Exits 0 in all cases: an empty array means no Explorer windows are open (or
// this is not a Windows machine). A non-zero exit only occurs on json.Encode
// failure — which cannot happen for this simple struct type.
//
// becky-canvas calls winctx.OpenExplorerFolders() directly; this binary exists
// so Jordan or a shell pipeline can query Explorer context without writing Go.
package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"

	"becky-go/internal/winctx"
)

func main() {
	windows, err := winctx.OpenExplorerFolders()
	if err != nil && !errors.Is(err, winctx.ErrUnsupportedOS) {
		// Real error (e.g. PowerShell failure). Log to stderr; still emit
		// valid JSON on stdout so callers that consume stdout don't break.
		fmt.Fprintf(os.Stderr, "becky-ctx: %v\n", err)
	}

	type jsonWindow struct {
		Path  string `json:"path"`
		Title string `json:"title"`
	}
	// Initialise to non-nil so empty result encodes as "[]" not "null".
	out := make([]jsonWindow, len(windows))
	for i, w := range windows {
		out[i] = jsonWindow{Path: w.Path, Title: w.Title}
	}

	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	if encErr := enc.Encode(out); encErr != nil {
		fmt.Fprintf(os.Stderr, "becky-ctx: json encode: %v\n", encErr)
		os.Exit(1)
	}
}
