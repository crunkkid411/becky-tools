package main

// dossier.go — one comprehensive file per clip (our version of ButterCut's
// per-clip library entry, Jordan 2026-08-25: "insufficient contextual data is
// the key bottleneck"). It aggregates every signal the suite can produce -
// word-level timestamps, measured gain/room tone, the keep/cut decisions,
// retake chains, and LR-ASD lip-sync speaking tracks (audio+visual agreement
// on WHO talks WHEN) - so editing decisions are corroborated, not guessed.
// Every field degrades to absent when its producer hasn't run.

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"becky-go/internal/quotes"
)

type speakingWindow struct {
	Start    float64 `json:"start"`
	End      float64 `json:"end"`
	Speakers int     `json:"speakers"`
	BestFrac float64 `json:"best_speaking_frac"`
}

// loadSpeaking collects becky-speaking results for a clip from the overnight
// sweep dir (temp/keepspeaking/<file>.<k>.json) or beside the artifacts.
func loadSpeaking(out string, c clip) []speakingWindow {
	var wins []speakingWindow
	var paths []string
	for _, g := range []string{
		filepath.Join(out, c.Stem+".speaking.*.json"),
		filepath.Join(os.TempDir(), "keepspeaking", filepath.Base(c.Path)+".*.json"),
	} {
		m, _ := filepath.Glob(g)
		paths = append(paths, m...)
	}
	for _, p := range paths {
		b, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		var r struct {
			OK     bool `json:"ok"`
			Tracks []struct {
				Start        *float64 `json:"start"`
				End          *float64 `json:"end"`
				Speaking     bool     `json:"speaking"`
				SpeakingFrac *float64 `json:"speaking_frac"`
			} `json:"tracks"`
		}
		if json.Unmarshal(b, &r) != nil || !r.OK {
			continue
		}
		w := speakingWindow{}
		for _, t := range r.Tracks {
			if t.Start != nil && w.Start == 0 {
				w.Start = *t.Start
			}
			if t.End != nil {
				w.End = *t.End
			}
			if t.Speaking {
				w.Speakers++
			}
			if t.SpeakingFrac != nil && *t.SpeakingFrac > w.BestFrac {
				w.BestFrac = *t.SpeakingFrac
			}
		}
		wins = append(wins, w)
	}
	sort.SliceStable(wins, func(i, j int) bool { return wins[i].Start < wins[j].Start })
	return wins
}

// speakingCorroborationThreshold is how low a keep's best overlapping
// speaking_frac may go before it's flagged - Jordan, 2026-08-24: "no visual
// grounding, no audio analysis, no contextual understanding is highly
// unreasonable... these are additional data points to help provide more
// corroborative context so that better video editing decisions can be made."
const speakingCorroborationThreshold = 0.35

// speakingCorroboration cross-checks every kept span against LR-ASD: a keep
// that has real audio/transcript content but where nobody is visibly
// speaking on camera for most of it is exactly the kind of thing a
// word-timing-only detector cannot see (room noise mis-transcribed,
// off-camera crosstalk, a false-positive cue). This is a SIGNAL, never a
// VERDICT (SKILL.md's VIDEO CLIPPING rules, "a detector is a signal never a
// verdict on the footage") - it never cuts or shortens anything, it raises a
// review marker so a human decides. Only judges keeps where LR-ASD actually
// covers most of the span; a keep with no visual data at all has nothing to
// corroborate against and is left alone rather than guessed at.
func speakingCorroboration(cStem string, keeps []span, speaking []speakingWindow) []pendingMarker {
	var out []pendingMarker
	for _, k := range keeps {
		if k.End-k.Start < 1.0 {
			continue // too short for a meaningful visual read
		}
		var best *speakingWindow
		var bestOverlap float64
		for i := range speaking {
			w := &speaking[i]
			lo, hi := maxF(w.Start, k.Start), minF(w.End, k.End)
			if overlap := hi - lo; overlap > bestOverlap {
				bestOverlap, best = overlap, w
			}
		}
		if best == nil || bestOverlap < 0.5*(k.End-k.Start) {
			continue // LR-ASD doesn't cover enough of this keep to corroborate either way
		}
		if best.Speakers == 0 || best.BestFrac < speakingCorroborationThreshold {
			out = append(out, pendingMarker{
				Source: cStem,
				T:      k.Start,
				TEnd:   k.End,
				Title: fmt.Sprintf("CHECK: audio kept here but LR-ASD saw no one visibly speaking (%.0f%% of the window) - %s",
					best.BestFrac*100, cStem),
				Kind: "review",
			})
		}
	}
	return out
}

// writeDossier emits <stem>.dossier.json and returns the greppable summary
// line that also lands in library.yaml.
func writeDossier(out string, c clip, gain float64, cues []quotes.Cue, keeps []span, speaking []speakingWindow, retakesCut, retakeMarkers int) string {
	speech := 0.0
	for _, k := range keeps {
		speech += k.End - k.Start
	}
	summary := ""
	if len(cues) > 0 {
		first, last := cues[0].Text, cues[len(cues)-1].Text
		if len(first) > 80 {
			first = first[:80] + "..."
		}
		if len(last) > 80 {
			last = last[:80] + "..."
		}
		summary = strings.Join([]string{
			strings.TrimSpace(first),
			strings.TrimSpace(last),
		}, " | ")
	}
	d := map[string]any{
		"source":         c.Path,
		"duration":       c.Duration,
		"gain_db":        gain,
		"srt":            c.SRT,
		"words_json":     filepath.Join(out, c.Stem+".words.json"),
		"audio_profile":  filepath.Join(out, c.Stem+".audio_profile.json"),
		"keeps":          keeps,
		"keep_seconds":   speech,
		"cue_count":      len(cues),
		"retakes_cut":    retakesCut,
		"retake_markers": retakeMarkers,
		"speaking":       speaking,
		"summary":        summary,
	}
	b, _ := json.MarshalIndent(d, "", "  ")
	os.WriteFile(filepath.Join(out, c.Stem+".dossier.json"), b, 0o644)
	return summary
}
