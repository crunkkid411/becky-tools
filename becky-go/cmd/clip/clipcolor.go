package main

import (
	"encoding/json"
	"fmt"
	"hash/fnv"
	"os"
	"path/filepath"
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
// The map is PERSISTED per project folder (2026-07-22, Jordan: "the colors are
// going wild... that color does not change for the rest of the project"). The
// in-memory-only version honoured his rule within one engine process, but every
// app launch starts a NEW engine with an empty map, so colours were re-assigned
// in whatever first-appearance order that session's reel happened to have -
// reorder or delete clips, restart, and video 3 wears video 1's green. The map
// now loads from %LOCALAPPDATA%\becky\colors\<hash-of-folder>.json when the
// project (case folder / reel folder) opens, and every NEW assignment writes it
// back - so an assignment is frozen for the life of the project, across edits,
// restarts and reel reloads. On disk beats in memory; the file always wins.
var (
	clipColorMu   sync.Mutex
	clipColorBy   = map[string]string{}
	clipColorNext int
	clipColorKey  string // normalised project dir the map was loaded for ("" = not loaded)
	clipColorPath string // the JSON file backing the map ("" = nothing to persist to yet)
)

// clipColorsFile is the on-disk shape. Colors keys are colorKey-normalised
// source paths; Next preserves first-appearance ordering across restarts.
type clipColorsFile struct {
	Next   int               `json:"next"`
	Colors map[string]string `json:"colors"`
}

func clipColorsPathFor(key string) string {
	base := os.Getenv("LOCALAPPDATA")
	if base == "" {
		if d, err := os.UserCacheDir(); err == nil {
			base = d
		} else {
			base = "."
		}
	}
	h := fnv.New64a()
	h.Write([]byte(key))
	dir := filepath.Join(base, "becky", "colors")
	_ = os.MkdirAll(dir, 0o755)
	return filepath.Join(dir, fmt.Sprintf("%016x.json", h.Sum64()))
}

// saveClipColorsLocked writes the current assignment. Caller holds clipColorMu.
// Tiny file, rare writes (only a brand-new source), so a plain write is enough.
func saveClipColorsLocked() {
	if clipColorPath == "" {
		return
	}
	b, err := json.MarshalIndent(clipColorsFile{Next: clipColorNext, Colors: clipColorBy}, "", " ")
	if err != nil {
		return
	}
	_ = os.WriteFile(clipColorPath, b, 0o644)
}

// LoadClipColors points the assignment at the project living in dir (the case
// folder, or a reel's own folder - same file when the reel sits in the case
// folder, which is the standard layout). Entries already on disk WIN over
// anything assigned in memory before the load (e.g. the forensic launcher's
// reel preload, which runs before open_folder); in-memory extras are kept and
// persisted. Opening a DIFFERENT project drops the old project's map first -
// "for the remainder of that project" ends when the project does.
func LoadClipColors(dir string) {
	k := colorKey(dir)
	if k == "" {
		return
	}
	clipColorMu.Lock()
	defer clipColorMu.Unlock()
	if clipColorKey == k {
		return // already loaded for this project
	}
	if clipColorKey != "" {
		clipColorBy = map[string]string{}
		clipColorNext = 0
	}
	clipColorKey = k
	clipColorPath = clipColorsPathFor(k)
	if b, err := os.ReadFile(clipColorPath); err == nil {
		var f clipColorsFile
		if json.Unmarshal(b, &f) == nil && f.Colors != nil {
			for s, c := range f.Colors {
				clipColorBy[s] = c // disk wins
			}
			if f.Next > clipColorNext {
				clipColorNext = f.Next
			}
		}
	}
	saveClipColorsLocked() // persist the union (disk + any pre-load assignments)
}

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
	saveClipColorsLocked() // a new assignment is frozen the moment it is made
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

// ResetClipColors clears the assignment AND detaches from the backing file
// (tests use this to simulate a fresh engine process; production project
// switching goes through LoadClipColors, which resets internally).
func ResetClipColors() {
	clipColorMu.Lock()
	defer clipColorMu.Unlock()
	clipColorBy = map[string]string{}
	clipColorNext = 0
	clipColorKey = ""
	clipColorPath = ""
}
