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
