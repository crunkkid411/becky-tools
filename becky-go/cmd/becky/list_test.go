package main

import (
	"os"
	"path/filepath"
	"testing"

	"becky-go/internal/catalog"
)

// buildListDoc must surface every ToolCatalog entry (including the three
// added in P1 slice C: becky-vision, becky-perceive, search_library — the
// review's F7: these existed on disk but the catalog didn't know about them)
// with a non-empty one-line contract, plus the orchestrator's own ops.
func TestBuildListDoc_CoversCatalog(t *testing.T) {
	doc := buildListDoc()
	if doc.Tool != "becky" {
		t.Errorf("doc.Tool = %q, want becky", doc.Tool)
	}
	if len(doc.Tools) != len(catalog.ToolCatalog) {
		t.Fatalf("len(doc.Tools) = %d, want %d (one row per catalog.ToolCatalog entry)", len(doc.Tools), len(catalog.ToolCatalog))
	}
	if len(doc.Ops) != len(catalog.OrchestratorOps) {
		t.Fatalf("len(doc.Ops) = %d, want %d", len(doc.Ops), len(catalog.OrchestratorOps))
	}

	want := map[string]bool{"becky-vision": false, "becky-perceive": false, "search_library": false, "becky-ocr": false}
	for _, tool := range doc.Tools {
		if tool.Summary == "" {
			t.Errorf("%s: empty one-line contract (Summary)", tool.Name)
		}
		if _, tracked := want[tool.Name]; tracked {
			want[tool.Name] = true
		}
	}
	for name, found := range want {
		if !found {
			t.Errorf("expected %s in the inventory, not found", name)
		}
	}
}

// resolveTool must find a tool sitting next to the running test binary's
// os.Executable() dir (the same place becky.exe's siblings would live once
// installed) without touching a real PATH lookup.
func TestResolveTool_FindsSiblingBinary(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Skip("os.Executable unavailable in this test environment")
	}
	dir := filepath.Dir(exe)
	fake := filepath.Join(dir, "becky-test-fixture-tool.exe")
	if err := os.WriteFile(fake, []byte("fixture"), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	t.Cleanup(func() { os.Remove(fake) })

	path, ok := resolveTool("becky-test-fixture-tool")
	if !ok {
		t.Fatal("resolveTool did not find the fixture sitting next to the test binary")
	}
	if path != fake {
		t.Errorf("resolveTool path = %q, want %q", path, fake)
	}
}

// A tool that exists nowhere (not a sibling, not in the known PATH bin, not
// on PATH) must resolve as not-installed rather than erroring — the honest
// "what actually exists" signal the review's F7 asked for.
func TestResolveTool_MissingToolReportsFalse(t *testing.T) {
	if _, ok := resolveTool("becky-this-tool-does-not-exist-anywhere"); ok {
		t.Error("resolveTool should report false for a tool that isn't installed anywhere")
	}
}
