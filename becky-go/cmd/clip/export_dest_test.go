package main

import (
	"path/filepath"
	"strings"
	"testing"

	"becky-go/internal/edl"
	"becky-go/internal/reel"
)

// The review app's renders must land with THEIR OWN FOOTAGE, not in whatever
// folder the library happens to be browsing.
//
// This is the exact failure, reproduced: Jordan had E:\TakingBack2007 open in the
// library — a REMOVABLE FORENSIC DRIVE holding evidence for a live criminal case —
// while the timeline held footage from X:\Videos. renderDir() built the
// destination from the BROWSED FOLDER, so eight renders of his personal YouTube
// skits (one 214MB) were written onto the evidence volume.
//
// The browsed folder answers "what am I looking at". Only the clip sources answer
// "what is this render made of", and only that may choose the destination. The
// rule, in his words: "where the raw footage exists = where output files go".
//
// These tests assert the DECISION (renderDirPath), not the mkdir: the CI
// machine has no X: drive, so creating the decided path fails on Windows
// runners and litters literal `X:\...`-named directories on Linux ones. The
// mkdir path is still covered by TestRenderFallsBackToBrowsedFolderOnlyWithNoSource,
// which uses a real temp dir.
func TestRenderGoesWithTheFootageNotTheBrowsedFolder(t *testing.T) {
	a := &App{
		folder:  `E:\TakingBack2007`, // the forensic evidence drive, open in the library
		workDir: t.TempDir(),
	}
	sources := []string{`X:\Videos\2025\11_November\raw\FLYV9992.mp4`}

	got := a.renderDirPath(sources...)

	if strings.HasPrefix(strings.ToUpper(got), "E:") {
		t.Fatalf("render dir = %q — NEVER the forensic evidence drive", got)
	}
	want := filepath.Join(`X:\Videos\2025\11_November\raw`, reel.RenderSubdir)
	if !strings.EqualFold(got, want) {
		t.Errorf("render dir = %q, want %q — the render must sit with its own footage", got, want)
	}
}

// Thumbnails come from the same footage, so they follow it too — under a
// timeline_thumbnails subfolder so the many tiny jpegs don't litter the render
// folder beside the actual compilations (Jordan complained about both).
func TestThumbnailsGoWithTheFootageNotTheBrowsedFolder(t *testing.T) {
	a := &App{
		folder:  `E:\TakingBack2007`,
		workDir: t.TempDir(),
	}

	got := a.thumbDirPath(`X:\Videos\2025\11_November\raw\FLYV9992.mp4`)

	if strings.HasPrefix(strings.ToUpper(got), "E:") {
		t.Fatalf("thumb dir = %q — NEVER the forensic evidence drive", got)
	}
	want := filepath.Join(`X:\Videos\2025\11_November\raw`, reel.RenderSubdir, "timeline_thumbnails")
	if !strings.EqualFold(got, want) {
		t.Errorf("thumb dir = %q, want %q", got, want)
	}
}

// A reel whose clips are already inside Rendered/ (Jordan routinely edits from a
// previous render) stays put instead of nesting Rendered/Rendered.
func TestRenderDoesNotNestInsideRendered(t *testing.T) {
	a := &App{folder: `E:\TakingBack2007`, workDir: t.TempDir()}
	src := filepath.Join(`X:\Videos\2025\11_November`, reel.RenderSubdir, "post_constantly.mp4")

	got := a.renderDirPath(src)
	want := filepath.Join(`X:\Videos\2025\11_November`, reel.RenderSubdir)
	if !strings.EqualFold(got, want) {
		t.Errorf("render dir = %q, want %q — no Rendered inside Rendered", got, want)
	}
}

// Only with NO usable clip source does the browsed folder get to decide — a
// headless call or an empty timeline still needs somewhere to land.
func TestRenderFallsBackToBrowsedFolderOnlyWithNoSource(t *testing.T) {
	folder := t.TempDir()
	a := &App{folder: folder, workDir: t.TempDir()}

	got, err := a.renderDir()
	if err != nil {
		t.Fatalf("renderDir: %v", err)
	}
	if want := filepath.Join(folder, reel.RenderSubdir); got != want {
		t.Errorf("render dir = %q, want %q", got, want)
	}
}

// ClipSources feeds renderDir straight off the timeline, so the wiring the real
// export path uses is covered too, not just the helper underneath it.
func TestClipSourcesPickTheFirstRealSource(t *testing.T) {
	clips := []edl.Clip{
		{Source: ""}, // an empty slot must not win the vote
		{Source: `X:\Videos\raw\a.mp4`},
		{Source: `E:\TakingBack2007\evidence.mp4`},
	}
	got := reel.RenderDirFor(reel.ClipSources(clips)...)
	if want := filepath.Join(`X:\Videos\raw`, reel.RenderSubdir); !strings.EqualFold(got, want) {
		t.Errorf("render dir = %q, want %q", got, want)
	}
}
