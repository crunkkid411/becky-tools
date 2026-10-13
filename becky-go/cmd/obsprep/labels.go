package main

// labels.go - "verify it sounds like a clap" (obs-workflow.md). The sound
// labeler (pyhelpers/sound_labels.py, BEATs AudioSet Strong) names the
// strongest non-background sound at each candidate spike. On Jordan's five
// claps it said Slap/smack, Cap gun or Generic impact sounds; on a keyboard
// click that was louder than his clap it said Clicking - so only impact-type
// names count as a clap.

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"

	"becky-go/internal/config"
	"becky-go/internal/pyhelpers"
)

var clapLabels = []string{"Clapping", "Hands", "Slap, smack", "Applause", "Cap gun", "Generic impact sounds",
	"Finger snapping", "Snap"}

type soundLabel struct {
	Label string
	Voice float64
}

// labelSpikes names the sound at each spike. A missing labeler is an error the
// caller reports; it never guesses.
func labelSpikes(cfg config.Config, ffmpeg, file string, micIdx int, sp []spike, work string) ([]soundLabel, error) {
	if _, err := os.Stat(cfg.SoundLabelPython); err != nil {
		return nil, fmt.Errorf("the sound labeler's Python is missing (%s)", cfg.SoundLabelPython)
	}
	if _, err := os.Stat(filepath.Join(cfg.SoundLabelRepo, "resources", "BEATs_strong_1.pt")); err != nil {
		return nil, fmt.Errorf("the sound labeler is not installed (%s)", cfg.SoundLabelRepo)
	}
	wav := filepath.Join(work, "mic16k.wav")
	if out, err := exec.Command(ffmpeg, "-v", "error", "-y", "-t", fmt.Sprint(searchSecs), "-i", file,
		"-map", fmt.Sprintf("0:a:%d", micIdx), "-ac", "1", "-ar", "16000", wav).CombinedOutput(); err != nil {
		return nil, fmt.Errorf("could not cut the mic audio: %v %s", err, out)
	}
	spans := make([][2]float64, len(sp))
	for i, s := range sp {
		spans[i] = [2]float64{s.Onset - 0.15, s.Onset + 0.25} // the labeler trims 0.1 s off each end
	}
	b, _ := json.Marshal(spans)
	spanFile := filepath.Join(work, "spans.json")
	if err := os.WriteFile(spanFile, b, 0o644); err != nil {
		return nil, err
	}
	script, err := pyhelpers.Materialize("sound_labels.py", pyhelpers.SoundLabels)
	if err != nil {
		return nil, err
	}
	out, runErr := exec.Command(cfg.SoundLabelPython, script, "--wav", wav, "--repo", cfg.SoundLabelRepo, "--spans", spanFile).Output()
	var res struct {
		OK     bool   `json:"ok"`
		Reason string `json:"reason"`
		Spans  []struct {
			Other string  `json:"other_label"`
			Voice float64 `json:"voice"`
		} `json:"spans"`
	}
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	if json.Unmarshal([]byte(lines[len(lines)-1]), &res) != nil || !res.OK || len(res.Spans) != len(sp) {
		return nil, fmt.Errorf("the sound labeler failed: %v %s", runErr, res.Reason)
	}
	ls := make([]soundLabel, len(sp))
	for i, s := range res.Spans {
		ls[i] = soundLabel{Label: s.Other, Voice: s.Voice}
	}
	return ls, nil
}

func soundsLikeClap(l soundLabel) bool {
	return l.Voice < 0.5 && slices.Contains(clapLabels, l.Label)
}
