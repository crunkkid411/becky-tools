package reel

import (
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"becky-go/internal/edl"
	"becky-go/internal/pathx"
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
	// pathx.IsAbs, not filepath.IsAbs: the sources here are Windows paths and
	// this test also runs on Linux CI, where filepath.IsAbs calls `X:\...`
	// relative and fails the test on the wrong OS instead of the real bug.
	if !pathx.IsAbs(got) {
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

// A reel sourced FROM the evidence drive renders BESIDE ITS OWN FOOTAGE, on
// that same drive. This is the direct reversal of the deleted ProtectedDrive
// rule, and it is the behaviour Jordan asked for in the first place: output
// lives in a render/ subfolder of the raw footage, whatever volume that is.
//
// The rule it replaces refused every E: path, which sent forensic renders to
// %TEMP%\becky-clip; Windows cleared it and clips already delivered to law
// enforcement were lost. A render is a NEW file in a NEW subfolder — it never
// alters an original, so the forensic invariant is not what was at stake.
func TestRenderDirForFollowsTheFootageOntoTheEvidenceDrive(t *testing.T) {
	got := RenderDirFor(`E:\TakingBack2007\clips_01-02-reddit.mp4`)
	want := filepath.Join(`E:\TakingBack2007`, RenderSubdir)
	if !strings.EqualFold(got, want) {
		t.Errorf("RenderDirFor(E: source) = %q, want %q — renders sit with their own footage", got, want)
	}
}

// The destination is the FIRST usable source, with no drive playing favourites.
func TestRenderDirForUsesTheFirstUsableSource(t *testing.T) {
	got := RenderDirFor(
		"",
		`X:\Videos\2025\11_November\raw\FLYV9992.mp4`,
		`E:\TakingBack2007\clip_a.mp4`,
	)
	want := filepath.Join(`X:\Videos\2025\11_November\raw`, RenderSubdir)
	if !strings.EqualFold(got, want) {
		t.Errorf("RenderDirFor = %q, want %q", got, want)
	}
}

// The folder name is "render" — lowercase and singular, Jordan's explicit
// choice. Asserted by literal so a future rename to "Rendered"/"Renders"
// fails here instead of silently scattering his output across two folder names.
func TestRenderSubdirIsLowercaseRender(t *testing.T) {
	if RenderSubdir != "render" {
		t.Errorf("RenderSubdir = %q, want \"render\"", RenderSubdir)
	}
}

// A render must NEVER be decided into a temp directory. This is the regression
// test for the incident that destroyed delivered forensic clips: any source at
// all means the answer is beside that source, never %TEMP%.
func TestRenderDirForNeverAnswersWithATempPath(t *testing.T) {
	for _, src := range []string{
		`E:\TakingBack2007\clip.mp4`,
		`X:\Videos\raw\a.mp4`,
		`C:\Users\only1\Videos\b.mp4`,
	} {
		got := strings.ToLower(RenderDirFor(src))
		if got == "" || strings.Contains(got, `\temp\`) || strings.Contains(got, "becky-clip") {
			t.Errorf("RenderDirFor(%q) = %q — a render may never be routed to a temp dir", src, got)
		}
	}
}
