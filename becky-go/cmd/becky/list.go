// list.go — `becky list [--json]`: the machine-readable tool inventory
// (becky-AI-Agent-review-1.md P1 slice C, acceptance criterion 7 / F7:
// "There is no becky list --json that enumerates installed tools + one-line
// contracts. Agents cannot discover what exists; tonight's operator (me)
// claimed becky-vision didn't exist while it sat on disk.").
//
// Reuses internal/catalog (the same source of truth cmd/ask already reads)
// for the one-line contracts, and adds the ONE fact the catalog never
// carried: whether each tool ACTUALLY resolves on disk right now. That is
// the difference between this and cmd/catalog's becky-catalog.exe (which
// prints the same contracts but never checks reality) — discovery must be a
// tool call, not a hardcoded list assumed to be accurate.
package main

import (
	"os"
	"os/exec"
	"path/filepath"

	"becky-go/internal/catalog"
)

// toolEntry is one becky-*.exe tool's inventory row.
type toolEntry struct {
	Name      string `json:"name"`
	Summary   string `json:"summary"`
	Example   string `json:"example"`
	Tier      string `json:"tier"`
	Installed bool   `json:"installed"`
	Path      string `json:"path,omitempty"`
}

// opEntry is one `becky <verb>` orchestrator operation's inventory row.
type opEntry struct {
	Verb    string `json:"verb"`
	Summary string `json:"summary"`
	Example string `json:"example"`
}

// listDoc is becky list's stdout JSON document.
type listDoc struct {
	Tool  string      `json:"tool"`
	Tools []toolEntry `json:"tools"`
	Ops   []opEntry   `json:"ops"`
}

// runList implements `becky list [--json]`.
func runList(args []string) error {
	cf, _ := extractCommon(args)
	doc := buildListDoc()

	missing := 0
	for _, t := range doc.Tools {
		status := "installed"
		if !t.Installed {
			status = "MISSING"
			missing++
		}
		headline(cf, "  %-20s [%-9s] %s", t.Name, status, t.Summary)
	}
	headline(cf, "")
	headline(cf, "%d/%d tool(s) resolve on disk right now. %d becky orchestrator op(s) also available (becky help).",
		len(doc.Tools)-missing, len(doc.Tools), len(doc.Ops))
	emitJSON(doc)
	return nil
}

// buildListDoc assembles the inventory from internal/catalog, pure and
// testable (no flag parsing, no I/O beyond the disk-presence check).
func buildListDoc() listDoc {
	doc := listDoc{Tool: "becky", Tools: []toolEntry{}, Ops: []opEntry{}}
	for _, c := range catalog.ToolCatalog {
		path, ok := resolveTool(c.Verb)
		doc.Tools = append(doc.Tools, toolEntry{
			Name: c.Verb, Summary: c.Summary, Example: c.Example,
			Tier: string(c.TierOf()), Installed: ok, Path: path,
		})
	}
	for _, c := range catalog.AllOpsList() {
		doc.Ops = append(doc.Ops, opEntry{Verb: c.Verb, Summary: c.Summary, Example: c.Example})
	}
	return doc
}

// resolveTool checks, in order: next to the running becky.exe, the
// well-known PATH bin (C:\Users\only1\bin), then PATH itself
// (exec.LookPath) — the same places a real shell or runTool's binPath()
// would find a sibling tool. Returns the resolved path and whether the tool
// was found anywhere. A name that already carries no "becky-" prefix
// (search_library) resolves exactly the same way — it is just a name.
func resolveTool(name string) (string, bool) {
	exeName := name
	if filepath.Ext(exeName) == "" {
		exeName += ".exe"
	}
	var dirs []string
	if exe, err := os.Executable(); err == nil {
		dirs = append(dirs, filepath.Dir(exe))
	}
	dirs = append(dirs, knownPathBin)
	for _, d := range dirs {
		cand := filepath.Join(d, exeName)
		if fileExists(cand) {
			return cand, true
		}
	}
	if p, err := exec.LookPath(name); err == nil {
		return p, true
	}
	return "", false
}

// knownPathBin is Jordan's well-known tool PATH bin (AUTOPILOT.md law 9),
// checked directly in case becky.exe itself is run from somewhere else
// (e.g. becky-go\bin during development) and the OS PATH hasn't been
// refreshed in the current shell yet.
const knownPathBin = `C:\Users\only1\bin`
