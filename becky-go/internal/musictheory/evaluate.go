package musictheory

import (
	"fmt"
	"strings"

	"becky-go/internal/dawmodel"
	"becky-go/internal/music"
)

// evaluate.go: becky checks its OWN generated arrangement against the deterministic
// half of the music-theory-engine evaluation checklist BEFORE shipping it — the
// "corroborate, then CONCLUDE" rule applied to becky's own music. The two subjective
// checks ("would a human recognize the genre?", "does it groove?") are NOT auto-
// passed here; they stay human/model-flagged.

// Issue is one problem found by Evaluate.
type Issue struct {
	Check string `json:"check"` // which rule ("key", "velocity", "bass_register", "space")
	Track string `json:"track"` // the offending track id ("" if arrangement-wide)
	Note  string `json:"note"`  // plain-English description
}

// Evaluate runs the machine-checkable music constraints over an arrangement and
// returns the issues (empty slice = clean). Deterministic; never panics. Checks:
//   - key consistency: every pitched note is in the arrangement's key (skipped if no
//     key is set),
//   - never-flat velocity: no pitched track has all-identical velocities,
//   - bass register: a "bass" track stays in MIDI 36–71,
//   - space: no pitched track fills every 16th step of every bar (rests are musical).
func Evaluate(a *dawmodel.Arrangement) []Issue {
	var issues []Issue
	if a == nil {
		return issues
	}
	rootPC, scaleIntervals, haveKey := keyOf(a)

	for _, t := range a.Tracks {
		notes := pitchedNotes(t)
		if len(notes) == 0 {
			continue
		}
		isBass := strings.EqualFold(t.ID, "bass")

		// key consistency
		if haveKey {
			for _, n := range notes {
				if !InScale(n.Pitch, rootPC, scaleIntervals) {
					issues = append(issues, Issue{
						Check: "key", Track: t.ID,
						Note: fmt.Sprintf("note %d (pitch class %d) is outside the key", n.Pitch, n.Pitch%12),
					})
					break // one report per track is enough
				}
			}
		}

		// never-flat velocity
		if flatVelocity(notes) {
			issues = append(issues, Issue{
				Check: "velocity", Track: t.ID,
				Note: "velocity is flat — vary it (ghost notes low, accents high)",
			})
		}

		// bass register
		if isBass {
			for _, n := range notes {
				if n.Pitch < 36 || n.Pitch > 71 {
					issues = append(issues, Issue{
						Check: "bass_register", Track: t.ID,
						Note: fmt.Sprintf("bass note %d is outside MIDI 36–71", n.Pitch),
					})
					break
				}
			}
		}

		// space (rests)
		if fillsEveryStep(notes) {
			issues = append(issues, Issue{
				Check: "space", Track: t.ID,
				Note: "every step is occupied — leave space (rests are musical)",
			})
		}
	}
	return issues
}

// keyOf resolves the arrangement key to (rootPC, scaleIntervals, ok). ok=false when
// no key is set (so the key check is skipped rather than guessed).
func keyOf(a *dawmodel.Arrangement) (int, []int, bool) {
	root := strings.TrimSpace(a.Root)
	if root == "" {
		return 0, nil, false
	}
	keyStr := root
	if sc := strings.TrimSpace(a.Scale); sc != "" {
		keyStr = root + " " + sc
	}
	pc, name := music.ParseKey(keyStr)
	return pc, music.ScaleIntervals(name), true
}

// pitchedNotes returns the non-drum (channel != 9) notes across a track's clips.
func pitchedNotes(t dawmodel.Track) []dawmodel.Note {
	var out []dawmodel.Note
	for _, c := range t.Clips {
		if c.Channel == 9 {
			continue
		}
		for _, n := range c.Notes {
			if n.Ch == 9 {
				continue
			}
			out = append(out, n)
		}
	}
	return out
}

func flatVelocity(notes []dawmodel.Note) bool {
	if len(notes) < 2 {
		return false
	}
	v0 := notes[0].Vel
	for _, n := range notes[1:] {
		if n.Vel != v0 {
			return false
		}
	}
	return true
}

// fillsEveryStep reports whether the notes start on every 16th step of every bar they
// span — i.e. wall-to-wall with no rest (clearly "no space"). Conservative: only
// fires when truly every step in the spanned range has an onset.
func fillsEveryStep(notes []dawmodel.Note) bool {
	if len(notes) == 0 {
		return false
	}
	step := music.StepTicks
	maxStart := 0
	onsets := map[int]bool{}
	for _, n := range notes {
		if n.Start%step != 0 {
			return false // off-grid onsets ⇒ not the wall-to-wall pattern we flag
		}
		s := n.Start / step
		onsets[s] = true
		if s > maxStart {
			maxStart = s
		}
	}
	for s := 0; s <= maxStart; s++ {
		if !onsets[s] {
			return false
		}
	}
	return maxStart >= 3 // need at least a beat's worth before calling it "no space"
}
