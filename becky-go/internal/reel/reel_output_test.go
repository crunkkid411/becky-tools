package reel

import (
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"becky-go/internal/edl"
)

// fixtureRawDir is Jordan's raw-footage folder in each OS's native shape, so
// the drive-placement contract is exercised on Windows AND on the Linux CI
// runner (an `X:\` literal can never be filepath.IsAbs off-Windows).
func fixtureRawDir() string {
	if runtime.GOOS == "windows" {
		return `X:\Videos\2025\11_November\raw`
	}
	return "/videos/2025/11_November/raw"
}

// The render must land on the SAME DRIVE as the raw footage, in a Rendered/
// subfolder of it — never the process's cwd.
//
// This is not a style preference. The old cwd default wrote Jordan's own
// YouTube edits onto E:\, a removable forensic drive holding evidence for a
// criminal case, because a test run had left the cwd there. The rule existed
// for months but only in prose, so nothing stopped it drifting. It is a test
// now.
func TestRenderGoesBesideTheFootageNotTheCwd(t *testing.T) {
	r := edl.Reel{
		Name:  "post constantly",
		Clips: []edl.Clip{{Source: filepath.Join(fixtureRawDir(), "FLYV9992.mp4")}},
	}
	got := defaultReelOutput(r)

	wantDir := filepath.Join(fixtureRawDir(), RenderSubdir)
	if filepath.Dir(got) != wantDir {
		t.Errorf("output dir = %q, want %q — the render must sit with its own footage", filepath.Dir(got), wantDir)
	}
	if strings.HasPrefix(strings.ToUpper(got), "E:") {
		t.Fatalf("output = %q — NEVER the forensic drive", got)
	}
	if !filepath.IsAbs(got) {
		t.Errorf("output = %q — a bare relative name resolves against the cwd, which is the bug", got)
	}
}

func TestRenderDoesNotNestRenderedInsideRendered(t *testing.T) {
	// Jordan routinely edits from a previous render, so the source is often
	// already inside Rendered/. Appending again would make Rendered/Rendered/.
	renderedDir := filepath.Join(filepath.Dir(fixtureRawDir()), RenderSubdir)
	r := edl.Reel{
		Name:  "post constantly",
		Clips: []edl.Clip{{Source: filepath.Join(renderedDir, "FLYV9992.mp4")}},
	}
	got := filepath.Dir(defaultReelOutput(r))
	if !strings.EqualFold(got, renderedDir) {
		t.Errorf("output dir = %q, want the existing Rendered folder, not one nested inside it", got)
	}
}

func TestRenderFallsBackWhenThereIsNoSource(t *testing.T) {
	r := edl.Reel{Name: "orphan"}
	if got := defaultReelOutput(r); got == "" || filepath.IsAbs(got) {
		t.Errorf("output = %q, want a relative fallback name for a reel with no source", got)
	}
}
