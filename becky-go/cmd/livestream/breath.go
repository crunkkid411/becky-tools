package main

// breath.go - the breath check (Jordan approved 2026-10-06). MARKERS ONLY: it
// changes nothing in the edit.
//
// Jordan, 2026-10-05: becky-cut keeps loud breaths because "they are loud enough
// and part of the same chunk"; he asked for a separate pass over the KEPT clips
// that "must come AFTER the first VAD filter pass and does NOT affect the first
// VAD filter pass in any way". It reads becky-cut's finished decisions, the
// audio and the picture; it never touches becky-cut or BeckyCut.cs.
//
// The first version called every loud gap with no words a "breath". On the test
// stream most of them were his voice (word times off), hand gestures or big
// movements - "fast movement in my chair does not = breath, and that does change
// the nature of the edit". So a gap is a breath only when two independent
// signals agree (research/breath-vs-movement-sound-labels.md):
//  1. the sound labeler (pyhelpers/sound_labels.py, PretrainedSED BEATs) hears a
//     breath, and no other sound and no voice;
//  2. the picture is still (motion.go, against his own movement while talking).
// Movement and voice are never treated as breaths; anything unsure stays. Up to
// five checked breaths per edit become "Breath check" regions; the report lists
// every gap with its verdict. --breaths-only redoes just this on a saved project.

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"becky-go/internal/config"
	"becky-go/internal/pyhelpers"
)

const (
	breathMinGap   = 0.35 // seconds without a word that counts as a pause
	breathExamples = 5
	breathPrefix   = "Breath check"   // marker label; a re-run removes these...
	oldBreathLabel = "Breath example" // ...and the loudness-only markers of 2026-10-05

	// ponytail: calibration knobs, set on the 27-livestream and checked by eye on
	// 20 gaps (research doc). A missed breath only leaves a breath in; a false one
	// would cut a movement or a word, so every bound leans to "not a breath".
	// Another mic, room or camera may need them moved.
	breathHeard   = 0.30 // the labeler's Breathing/Pant score
	otherSoundMax = 0.15 // the strongest other sound (movement, impact, laugh...)
	voiceMax      = 0.20 // share of the gap where a voice is heard
	stillMean     = 1.5  // picture movement, x his median while talking
	stillPeak     = 2.5
)

// Verdicts.
const (
	vBreath   = "breath"
	vMovement = "movement"
	vSound    = "other sound"
	vVoice    = "voice"
	vUnclear  = "unclear"
)

type breath struct {
	A, B       float64 // source seconds
	Breath     float64 // sound labeler: breathing, 0-1
	Other      float64 // sound labeler: the strongest other sound, 0-1
	OtherLabel string
	Voice      float64 // share of the gap with a voice in it
	Move, Peak float64 // picture movement, x his median while talking
	Verdict    string
}

// breathSpots finds every wordless gap inside one kept piece (pieces sorted by
// time). There is no loudness test: loudness says nothing about what a sound
// is, and on the 27-livestream two real breaths (13:18, 14:10) were quieter
// than becky-cut's threshold. The sound labeler hears a silent gap as no
// breath, so it ends "unclear" and is left alone.
func breathSpots(words []Word, pieces []span) []breath {
	var out []breath
	pi := 0
	for i := 0; i+1 < len(words); i++ {
		a, b := words[i].End, words[i+1].Start
		if b-a < breathMinGap {
			continue
		}
		for pi < len(pieces) && pieces[pi].B <= a {
			pi++
		}
		if pi >= len(pieces) || pieces[pi].A > a || pieces[pi].B < b {
			continue // the gap is not wholly inside one kept piece: becky-cut already cut there
		}
		out = append(out, breath{A: a, B: b})
	}
	return out
}

// judge is the verdict for one measured gap. Order matters: a voice or a
// movement rules a gap out whatever the breath score says.
func judge(b breath) string {
	switch {
	case b.Voice > voiceMax:
		return vVoice
	case b.Move >= stillMean || b.Peak >= stillPeak:
		return vMovement
	case b.Other >= otherSoundMax:
		return vSound
	case b.Breath >= breathHeard:
		return vBreath
	}
	return vUnclear
}

type soundLabel struct {
	Breath     float64 `json:"breath"`
	Other      float64 `json:"other"`
	OtherLabel string  `json:"other_label"`
	Voice      float64 `json:"voice"`
}

// soundLabels runs the sound labeler over the spots (one call, ~10 s).
func soundLabels(cfg config.Config, wav, work string, spots []breath) ([]soundLabel, error) {
	if _, err := os.Stat(cfg.SoundLabelPython); err != nil {
		return nil, fmt.Errorf("the sound labeler's Python is missing (%s)", cfg.SoundLabelPython)
	}
	if _, err := os.Stat(filepath.Join(cfg.SoundLabelRepo, "resources", "BEATs_strong_1.pt")); err != nil {
		return nil, fmt.Errorf("the sound labeler is not installed (%s)", cfg.SoundLabelRepo)
	}
	script, err := pyhelpers.Materialize("sound_labels.py", pyhelpers.SoundLabels)
	if err != nil {
		return nil, err
	}
	spans := make([][2]float64, len(spots))
	for i, s := range spots {
		spans[i] = [2]float64{s.A, s.B}
	}
	spanFile := filepath.Join(work, "breath-spans.json")
	writeJSON(spanFile, spans)
	cmd := exec.Command(cfg.SoundLabelPython, script, "--wav", wav, "--repo", cfg.SoundLabelRepo, "--spans", spanFile)
	var stderr strings.Builder
	cmd.Stderr = &stderr
	out, runErr := cmd.Output()
	var res struct {
		OK     bool         `json:"ok"`
		Reason string       `json:"reason"`
		Spans  []soundLabel `json:"spans"`
	}
	if err := json.Unmarshal([]byte(lastJSONLine(string(out))), &res); err != nil {
		return nil, fmt.Errorf("the sound labeler gave no answer: %v (%s)", runErr, lastLines(stderr.String(), 2))
	}
	if !res.OK {
		return nil, fmt.Errorf("%s", res.Reason)
	}
	if len(res.Spans) != len(spots) {
		return nil, fmt.Errorf("the sound labeler answered %d of %d gaps", len(res.Spans), len(spots))
	}
	return res.Spans, nil
}

// lastJSONLine is the last stdout line that starts like a JSON object.
func lastJSONLine(s string) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if l := strings.TrimSpace(lines[i]); strings.HasPrefix(l, "{") {
			return l
		}
	}
	return strings.TrimSpace(s)
}

// checkBreaths measures both signals for every spot and gives each a verdict.
// When a signal cannot be measured nothing is called a breath (never back to
// loudness alone) and the note says why.
func checkBreaths(cfg config.Config, media, work, stem, wav string, words []Word, spots []breath) ([]breath, string) {
	out := make([]breath, len(spots))
	copy(out, spots)
	for i := range out {
		out[i].Verdict = vUnclear
	}
	if len(out) == 0 {
		return out, ""
	}
	labels, err := soundLabels(cfg, wav, work, out)
	if err != nil {
		return out, "the sound check did not run: " + err.Error()
	}
	mt, err := loadMotion(cfg.FFmpeg, media, filepath.Join(work, stem+".motion.json"))
	if err != nil {
		return out, "the picture check did not run: " + err.Error()
	}
	base := mt.baseline(words)
	if base <= 0 {
		return out, "the picture check found no talking to compare with"
	}
	for i := range out {
		l := labels[i]
		out[i].Breath, out[i].Other, out[i].OtherLabel, out[i].Voice = l.Breath, l.Other, l.OtherLabel, l.Voice
		out[i].Move, out[i].Peak = mt.movement(out[i].A, out[i].B, base)
		out[i].Verdict = judge(out[i])
	}
	return out, ""
}

// breathMarks picks up to five checked breaths spread across the edit (the
// longest in each fifth, in time order).
func breathMarks(spots []breath) []breath {
	var ok []breath
	for _, s := range spots {
		if s.Verdict == vBreath {
			ok = append(ok, s)
		}
	}
	var picks []breath
	for k := 0; k < breathExamples && len(ok) > 0; k++ {
		lo, hi := k*len(ok)/breathExamples, (k+1)*len(ok)/breathExamples
		if hi <= lo {
			continue
		}
		best := lo
		for i := lo; i < hi; i++ {
			if ok[i].B-ok[i].A > ok[best].B-ok[best].A {
				best = i
			}
		}
		picks = append(picks, ok[best])
	}
	sort.Slice(picks, func(i, j int) bool { return picks[i].A < picks[j].A })
	return picks
}

func breathLabel(n, of int, b breath) string {
	return fmt.Sprintf("%s %d of %d: %.1f s - heard a breath and the picture is still; becky could treat it as a pause", breathPrefix, n, of, b.B-b.A)
}

// breathRegions turns the picks into regions over each gap on the timeline.
func breathRegions(picks []breath, ps []piece) []mark {
	var ms []mark
	for n, b := range picks {
		if t0, t1, ok := toTimeline(ps, b.A, b.B); ok {
			ms = append(ms, mark{At: t0, Len: t1 - t0, Label: breathLabel(n+1, len(picks), b)})
		}
	}
	return ms
}

// breathSummary is the report's one-line summary and its detail list.
func breathSummary(spots, picks []breath, note string) (line string, details []string) {
	if note != "" {
		return fmt.Sprintf("- **Breath check:** skipped - %s. No breath markers; nothing was cut.", note), nil
	}
	n := map[string]int{}
	secs := 0.0
	for _, s := range spots {
		n[s.Verdict]++
		if s.Verdict == vBreath {
			secs += s.B - s.A
		}
	}
	line = fmt.Sprintf("- **Breath check:** %d checked breath(s), %.1f s; %d marked; nothing was cut. Not breaths: %d movement, %d other sound, %d voice, %d unclear.",
		n[vBreath], secs, len(picks), n[vMovement], n[vSound], n[vVoice], n[vUnclear])
	for _, s := range spots {
		d := fmt.Sprintf("- stream %s: %.1f s - %s", clock(s.A), s.B-s.A, s.Verdict)
		switch s.Verdict {
		case vBreath:
			d += fmt.Sprintf(" (breathing %.2f, picture movement %.1fx his talking)", s.Breath, s.Move)
		case vMovement:
			d += fmt.Sprintf(" (picture movement %.1fx his talking, peak %.1fx)", s.Move, s.Peak)
		case vSound:
			d += fmt.Sprintf(" (%s %.2f)", s.OtherLabel, s.Other)
		case vVoice:
			d += fmt.Sprintf(" (a voice in %.0f%% of it)", 100*s.Voice)
		default:
			d += fmt.Sprintf(" (breathing %.2f)", s.Breath)
		}
		details = append(details, d)
	}
	return line, details
}

func breathSection(details []string) string {
	if len(details) == 0 {
		return "\n### Breath check\n\nNothing to list.\n"
	}
	return "\n### Breath check\n\n" + strings.Join(details, "\n") + "\n"
}

// breathResult is what the timeline and the report need from the check.
type breathResult struct {
	Spots, Picks []breath
	Note         string
}

// runBreathCheck finds the wordless gaps inside the kept pieces, checks each
// one and picks the examples to mark.
func runBreathCheck(cfg config.Config, media, work, stem, wav string, words []Word, pieces []span, logf func(string, ...any)) breathResult {
	logf("breath check: listening to the wordless gaps and watching the picture...")
	spots, note := checkBreaths(cfg, media, work, stem, wav, words, breathSpots(words, pieces))
	bc := breathResult{Spots: spots, Picks: breathMarks(spots), Note: note}
	if note != "" {
		logf("  breath check skipped: %s", note)
	} else {
		logf("  %d gaps checked, %d breaths, %d marked", len(spots), countVerdict(spots, vBreath), len(bc.Picks))
	}
	return bc
}

func countVerdict(spots []breath, v string) int {
	n := 0
	for _, s := range spots {
		if s.Verdict == v {
			n++
		}
	}
	return n
}

// redoBreaths (--breaths-only) re-runs only the breath check on this model's
// saved project: open it, take the old breath markers off, put the checked
// ones on, save, and rewrite the breath lines of its report. Nothing else on
// the timeline is touched, and becky-cut / BeckyCut.cs are never run.
func (r *run) redoBreaths() {
	veg := filepath.Join(filepath.Dir(r.media), filepath.Base(filepath.Dir(r.media))+"-"+r.tag+".veg")
	transcript := filepath.Join(r.work, r.stem+".transcript.json")
	wav := filepath.Join(r.work, "source16k.wav")
	for _, p := range []string{veg, transcript, wav} {
		if _, err := os.Stat(p); err != nil {
			fatal("make the edit first - " + filepath.Base(p) + " is missing")
		}
	}
	words, err := loadWords(transcript)
	if err != nil {
		fatal(err.Error())
	}

	r.logf("VEGAS: opening %s...", filepath.Base(veg))
	if _, err := vegas(6*time.Minute, "launch"); err != nil {
		fatal(err.Error())
	}
	if _, err := vegas(3*time.Minute, "open_project", "path="+veg); err != nil {
		fatal(err.Error())
	}
	ps, health, err := readTimeline()
	if err != nil {
		fatal(err.Error())
	}
	r.logf("  timeline: %s", health)
	pieces := make([]span, len(ps))
	for i, p := range ps {
		pieces[i] = span{p.In, p.Out}
	}
	sort.Slice(pieces, func(i, j int) bool { return pieces[i].A < pieces[j].A })

	bc := runBreathCheck(r.cfg, r.media, r.work, r.stem, wav, words, pieces, r.logf)
	// The old markers go even when the check could not run: they were loudness
	// guesses, and a guess must not sit on the timeline looking like a fact.
	rep, err := vegas(2*time.Minute, "delete_marks", "prefix="+oldBreathLabel+"|"+breathPrefix)
	if err != nil {
		fatal(err.Error())
	}
	var del struct {
		Markers int `json:"markers_removed"`
		Regions int `json:"regions_removed"`
	}
	_ = json.Unmarshal(rep.Result, &del)
	r.logf("VEGAS: %d old breath marker(s) removed", del.Markers+del.Regions)
	marks := breathRegions(bc.Picks, ps)
	if err := addMarks(marks, r.logf); err != nil {
		fatal("the old breath markers are off, but the new ones could not be added: " + err.Error())
	}

	path := filepath.Join(r.work, "report-"+r.tag+".md")
	line, details := breathSummary(bc.Spots, bc.Picks, bc.Note)
	old, err := os.ReadFile(path)
	if err == nil {
		var md string
		if md, err = rewriteBreathReport(string(old), line, details, del.Markers+del.Regions, len(marks)); err == nil {
			err = os.WriteFile(path, []byte(md), 0o644)
		}
	}
	fmt.Println()
	fmt.Println("DONE - breath check, " + r.label)
	fmt.Println("  " + strings.TrimPrefix(line, "- "))
	fmt.Printf("  VEGAS project: %s (saved)\n", filepath.Base(veg))
	if err != nil {
		fmt.Printf("  The report was not updated: %v\n", err)
	} else {
		fmt.Printf("  Report: %s\n", path)
	}
}

var timelineCount = regexp.MustCompile(`(?m)^## On the timeline for you to look at \((\d+)\)$`)

// rewriteBreathReport swaps the breath lines of a finished report for the new
// check and corrects its "on the timeline" count (removed old marks, added new).
func rewriteBreathReport(md, line string, details []string, removed, added int) (string, error) {
	lines := strings.Split(md, "\n")
	bullet, head := -1, -1
	for i, l := range lines {
		if bullet < 0 && (strings.HasPrefix(l, "- **Breath examples:**") || strings.HasPrefix(l, "- **Breath check:**")) {
			bullet = i
		}
		if head < 0 && (l == "### Breath examples" || l == "### Breath check") {
			head = i
		}
	}
	if bullet < 0 || head < 0 {
		return md, errors.New("the report has no breath lines to replace")
	}
	lines[bullet] = line
	end := len(lines)
	for i := head + 1; i < len(lines); i++ {
		if strings.HasPrefix(lines[i], "#") {
			end = i
			break
		}
	}
	sec := append([]string{"### Breath check", ""}, details...)
	if len(details) == 0 {
		sec = append(sec, "Nothing to list.")
	}
	sec = append(sec, "")
	out := strings.Join(append(append(append([]string{}, lines[:head]...), sec...), lines[end:]...), "\n")
	return timelineCount.ReplaceAllStringFunc(out, func(m string) string {
		n, _ := strconv.Atoi(timelineCount.FindStringSubmatch(m)[1])
		return fmt.Sprintf("## On the timeline for you to look at (%d)", max(0, n-removed+added))
	}), nil
}
