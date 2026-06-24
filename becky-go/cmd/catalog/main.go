// Package main is becky-catalog: it prints becky's tool catalog as JSON (or a plain
// list) so a GUI or any external program can show the available tools WITHOUT
// hardcoding them. JSON out, exit 0. The catalog is internal/catalog — the single
// shared source of truth also used by becky-ask and becky-harness — so the GUI's tool
// list can never drift from the real tools. This is Step 1 of HANDOFF-BECKY-WPF-GUI.md:
// the verified data source the native WPF window reads at startup.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"

	"becky-go/internal/catalog"
)

// entry is the STABLE JSON shape the GUI consumes (explicit lowercase field names so
// the C# side binds cleanly and the contract is obvious).
type entry struct {
	Verb     string   `json:"verb"`
	Summary  string   `json:"summary"`
	Example  string   `json:"example"`
	Keywords []string `json:"keywords"`
	Tier     string   `json:"tier"` // resolved green|yellow|red (never empty)
	Pack     string   `json:"pack"`
}

// doc is the whole catalog document.
type doc struct {
	Tools []entry `json:"tools"` // the becky-*.exe tools (what the GUI makes clickable)
	Ops   []entry `json:"ops"`   // the `becky <verb>` orchestrator operations
}

func toEntry(c catalog.Capability) entry {
	kw := c.Keywords
	if kw == nil {
		kw = []string{}
	}
	return entry{
		Verb:     c.Verb,
		Summary:  c.Summary,
		Example:  c.Example,
		Keywords: kw,
		Tier:     string(c.TierOf()), // TierOf resolves unset/unknown -> red, never empty
		Pack:     c.Pack,
	}
}

// buildDoc maps the shared catalog into the GUI-facing document. Pure + testable.
func buildDoc() doc {
	d := doc{Tools: []entry{}, Ops: []entry{}}
	for _, c := range catalog.ToolCatalog {
		d.Tools = append(d.Tools, toEntry(c))
	}
	for _, c := range catalog.OrchestratorOps {
		d.Ops = append(d.Ops, toEntry(c))
	}
	return d
}

func main() {
	asJSON := flag.Bool("json", false, "print the catalog as JSON (for a GUI / external program)")
	flag.Parse()

	d := buildDoc()

	if *asJSON {
		b, err := json.MarshalIndent(d, "", "  ")
		if err != nil {
			fmt.Fprintf(os.Stderr, "becky-catalog: %v\n", err)
			os.Exit(1)
		}
		fmt.Println(string(b))
		return
	}

	// Human-readable default: a plain high-contrast-friendly list.
	for _, e := range d.Tools {
		fmt.Printf("%-18s [%-6s] %s\n", e.Verb, e.Tier, e.Summary)
	}
}
