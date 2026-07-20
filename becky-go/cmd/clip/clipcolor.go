package main

import "strings"

// clipPalette is the review app's timeline palette, byte-for-byte the same eight
// colours in the same order as kPalette in native/becky-review/main.cpp. Jordan
// chose these; they are an ACCESSIBILITY AID, not decoration. Never mute,
// desaturate or "tone down" any of them.
var clipPalette = []string{
	"#14FF39", // green
	"#00AEEF", // blue
	"#DC143C", // crimson
	"#8A2BE2", // violet
	"#FF57D1", // pink
	"#FFD700", // gold
	"#16F0EA", // cyan
	"#FF8C00", // orange
}

// clipColor picks a clip's colour from its SOURCE FILE, deterministically.
//
// The requirement is not just "colour the clips" — it is that the assignment is
// PERMANENT. Jordan's rule: deleting every clip that came from video 2 must
// never recolour video 3. Any scheme that assigns colours by position, by
// first-appearance order, or by counting distinct sources present fails that,
// because removing one source renumbers the rest and the whole timeline changes
// colour under him mid-edit. For someone who identifies clips by colour at a
// glance, that is worse than having no colours at all.
//
// So the colour is a pure function of the source path: the same file is always
// the same colour, in every reel, forever, no matter what else is on the
// timeline and no matter what is deleted. Nothing to persist and nothing that
// can drift.
//
// FNV-1a because it is stable across runs and platforms — Go's built-in map
// hashing is deliberately randomised per process and would give the same file a
// different colour every launch.
func clipColor(source string) string {
	if strings.TrimSpace(source) == "" {
		return ""
	}
	// Case-insensitive: Windows hands us the same file as both C:\A.MP4 and
	// c:\a.mp4, and those must not be two different colours.
	key := strings.ToLower(strings.ReplaceAll(strings.TrimSpace(source), "/", "\\"))
	var h uint32 = 2166136261
	for i := 0; i < len(key); i++ {
		h ^= uint32(key[i])
		h *= 16777619
	}
	return clipPalette[h%uint32(len(clipPalette))]
}
