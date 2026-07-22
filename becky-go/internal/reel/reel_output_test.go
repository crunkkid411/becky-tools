package reel

import (
	"path/filepath"
	"strings"
	"testing"

	"becky-go/internal/edl"
	"becky-go/internal/pathx"
)

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
		Clips: []edl.Clip{{Source: `X:\Videos\2025\11_November\raw\FLYV9992.mp4`}},
	}
	got := defaultReelOutput(r)

	wantDir := filepath.Join(`X:\Videos\2025\11_November\raw`, RenderSubdir)
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
	r := edl.Reel{
		Name:  "post constantly",
		Clips: []edl.Clip{{Source: `X:\Videos\2025\11_November\Rendered\FLYV9992.mp4`}},
	}
	got := filepath.Dir(defaultReelOutput(r))
	if !strings.EqualFold(got, `X:\Videos\2025\11_November\Rendered`) {
		t.Errorf("output dir = %q, want the existing Rendered folder, not one nested inside it", got)
	}
}

func TestRenderFallsBackWhenThereIsNoSource(t *testing.T) {
	r := edl.Reel{Name: "orphan"}
	if got := defaultReelOutput(r); got == "" || filepath.IsAbs(got) {
		t.Errorf("output = %q, want a relative fallback name for a reel with no source", got)
	}
}

// The exact 2026-07-21/22 incident: a "Render Selection" whose clips are
// sourced FROM the evidence drive. "Output goes with the footage" alone put
// the render right back on E:\TakingBack2007\Rendered\ — 24MB of it, plus its
// .edl/.srt sidecars, sitting on live case evidence. RenderDirFor must refuse
// an evidence-drive source entirely, not just prefer non-evidence ones.
func TestRenderDirForRefusesTheEvidenceDriveEvenAsTheOnlySource(t *testing.T) {
	got := RenderDirFor(`E:\TakingBack2007\clips_01-02-reddit.mp4`)
	if got != "" {
		t.Errorf("RenderDirFor(all-evidence) = %q, want \"\" so the caller falls back — never a path on E:", got)
	}
}

// A mixed-source list must skip the evidence-drive entries and land on the
// first REAL (non-evidence) source, not just the first non-empty one.
func TestRenderDirForSkipsEvidenceDriveSourcesInFavorOfRealFootage(t *testing.T) {
	got := RenderDirFor(
		`E:\TakingBack2007\clip_a.mp4`,
		`X:\Videos\2025\11_November\raw\FLYV9992.mp4`,
	)
	want := filepath.Join(`X:\Videos\2025\11_November\raw`, RenderSubdir)
	if !strings.EqualFold(got, want) {
		t.Errorf("RenderDirFor(evidence, real) = %q, want %q", got, want)
	}
}

func TestOnProtectedDrive(t *testing.T) {
	cases := []struct {
		path string
		want bool
	}{
		{`E:\TakingBack2007\clip.mp4`, true},
		{`e:\TakingBack2007\clip.mp4`, true}, // lowercase drive letter still counts
		{`X:\Videos\raw\a.mp4`, false},
		{`C:\Users\only1\AppData\Local\Temp\becky-clip`, false},
		{"", false},
	}
	for _, c := range cases {
		if got := OnProtectedDrive(c.path); got != c.want {
			t.Errorf("OnProtectedDrive(%q) = %v, want %v", c.path, got, c.want)
		}
	}
}
