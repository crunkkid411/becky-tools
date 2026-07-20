package main

import (
	"strings"
	"sync"
)

// clipPalette is the review app's timeline palette, byte-for-byte the same eight
// colours in the same order as kPalette in native/becky-review/main.cpp, and the
// same order Jordan names them in: "#14FF39 (video 1), #00AEEF (video 2),
// #DC143C (video 3)". They are an ACCESSIBILITY AID, not decoration — he is
// vision-impaired and identifies which video a clip came from by its colour at a
// glance. Never mute, desaturate or "tone down" any of them.
var clipPalette = []string{
	"#14FF39", // video 1
	"#00AEEF", // video 2
	"#DC143C", // video 3
	"#8A2BE2",
	"#FF57D1",
	"#FFD700",
	"#16F0EA",
	"#FF8C00",
}

// Source -> colour, ASSIGNED IN ORDER OF FIRST APPEARANCE AND NEVER REASSIGNED.
//
// Both halves of that are Jordan's spec, verbatim (becky-review-gui-user-feedback7.md):
//
//	"Once a color is assigned to a video, question, etc. that color must be
//	 persistent for the remainder of that project."
//	"if user deletes all clips from video 2, then clips from video 3 change color
//	 to #00AEEF. This should not happen."
//
// So the map only ever GROWS. Deleting every clip of a source does not release
// its colour, which is exactly what stops the rest of the timeline recolouring
// under him mid-edit.
//
// An earlier version hashed the path instead. That satisfied persistence but not
// his ORDERING — his first video came out violet instead of #14FF39 — and the
// ordering is the half he actually wrote down, because "video 1 is green" is
// what he has learned to read.
var (
	clipColorMu   sync.Mutex
	clipColorBy   = map[string]string{}
	clipColorNext int
)

// colorKey normalises a path so Windows handing back C:\A.MP4 and c:/a.mp4 can
// never become two different colours for one file.
func colorKey(source string) string {
	return strings.ToLower(strings.ReplaceAll(strings.TrimSpace(source), "/", `\`))
}

// clipColor returns this source's "#RRGGBB", assigning the next palette colour
// the first time a source is seen.
func clipColor(source string) string {
	k := colorKey(source)
	if k == "" {
		return ""
	}
	clipColorMu.Lock()
	defer clipColorMu.Unlock()
	if c, ok := clipColorBy[k]; ok {
		return c
	}
	c := clipPalette[clipColorNext%len(clipPalette)]
	clipColorBy[k] = c
	clipColorNext++
	return c
}

// SeedClipColors assigns colours in the order sources appear in a freshly loaded
// reel, so a reopened project colours video 1 green again rather than in
// whatever order the UI happened to ask about clips.
func SeedClipColors(sources []string) {
	for _, s := range sources {
		clipColor(s)
	}
}

// ResetClipColors clears the assignment. Called when a DIFFERENT project is
// loaded — "for the remainder of that project" ends when the project does, and
// without this a long session would exhaust the palette on stale sources.
func ResetClipColors() {
	clipColorMu.Lock()
	defer clipColorMu.Unlock()
	clipColorBy = map[string]string{}
	clipColorNext = 0
}
