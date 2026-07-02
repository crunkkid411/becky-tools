package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"becky-go/internal/reel"
)

// TestTimelineEDLPrefersCachedProxy is the safety check for wiring the windowed scrub
// proxy into the timeline: with no proxy on disk the EDL references the RAW source at
// its in-point (today's behavior — the never-regress fallback); once a fresh windowed
// proxy exists at its deterministic path, the EDL references the PROXY at in=0 (the
// proxy IS the [in,out) window, so it starts at 0). Getting that offset wrong would
// play the wrong content, so it is asserted explicitly.
func TestTimelineEDLPrefersCachedProxy(t *testing.T) {
	app, dir := openFixture(t)
	ring := filepath.Join(dir, "ring.mp4")
	if _, err := app.AddClip(ring, 1, 3, ""); err != nil {
		t.Fatalf("AddClip: %v", err)
	}

	// No proxy yet -> raw source at its in-point (in=1, length=out-in=2).
	res, err := app.TimelineEDL()
	if err != nil {
		t.Fatalf("TimelineEDL: %v", err)
	}
	raw, err := os.ReadFile(res.Path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), ring+",1.000,2.000") {
		t.Fatalf("with no proxy the EDL must use the raw source at its in-point:\n%s", raw)
	}

	// Plant a fresh windowed proxy at the deterministic path -> EDL prefers it at in=0.
	pp := reel.SegmentProxyPath(ring, app.workDir, 1, 3)
	mustWrite(t, pp, "proxy-bytes")
	res, err = app.TimelineEDL()
	if err != nil {
		t.Fatalf("TimelineEDL (with proxy): %v", err)
	}
	got, err := os.ReadFile(res.Path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(got), pp+",0.000,2.000") {
		t.Fatalf("once a proxy exists the EDL must reference it at in=0:\n%s", got)
	}
	if strings.Contains(string(got), ring+",1.000") {
		t.Fatalf("the EDL must NOT still reference the raw source once a proxy exists:\n%s", got)
	}
}
