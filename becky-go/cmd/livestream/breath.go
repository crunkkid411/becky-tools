package main

// breath.go - the breath check. REGIONS ONLY: it changes nothing in the edit,
// and cutting the breaths is NOT approved - Jordan judges the regions.
//
// Jordan, 2026-10-05: becky-cut keeps loud breaths because "they are loud enough
// and part of the same chunk"; the pass "must come AFTER the first VAD filter
// pass and does NOT affect the first VAD filter pass in any way". 2026-10-06:
// "fast movement in my chair does not = breath". His review of the first
// version the same day ("It's not there yet") set what v2 does:
//
//  1. The breath is found in the SOUND, frame by frame, over every kept piece:
//     pyhelpers/sound_labels.py --frames (PretrainedSED BEATs, Breathing/Pant
//     every 40 ms). v1 looked only in the transcript's gaps; the word times
//     were off and the piece edges, where half his breaths were, were skipped.
//  2. Margins like auto-editor's, after this second pass: a region starts 0.05 s
//     after his voice ends (at most 0.15 s before the breath) and ends 0.04 s
//     before his next word (at most 0.15 s after it), so a ripple delete never
//     clips a word. With no word between the breath and a piece edge, it runs
//     to the edge - through quiet only: a loud sound that is not the breath
//     stops it like a word (14:12 ran over 0.5 s of him handling the phone).
//  3. Every edge sits ON the timeline's frame grid (rounded inward); within 2
//     frames of a piece edge it takes the sliver too; under 4 frames it is
//     dropped. His script ripple-deletes inside every region: an edge between
//     frames leaves a one-frame black flash.
//  4. Not a breath: a laugh, cough or other mouth sound (vocalMax); a facial
//     expression (mouth held open - insightface or MediaPipe) or a big movement
//     (face lost, leaning in or out, shoulders) - picture.go; something held up
//     toward the camera, like the toast after "cheers" - MediaPipe on every
//     frame, then Falcon + Gemma on the middle one, picture.go. Any one of them
//     is enough to place no region. Hands alone do NOT rule a breath out: he
//     wants the breath with his arms spreading and with his hands to his head
//     cut; with a hand up (MediaPipe pose) only a clear breath gets a region.
//
// Calibrated on the 27-livestream against the ten breaths Jordan listed (all
// found, regions within a frame or two of his own spans) and checked by eye in
// frame strips: research/breath-vs-movement-sound-labels.md.

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
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
	breathPrefix   = "Breath check"   // region label; a re-run removes these...
	oldBreathLabel = "Breath example" // ...and the loudness-only markers of 2026-10-05

	sedHop = 0.04 // seconds per sound-labeler frame

	// ponytail: calibration knobs, set on the 27-livestream (see the header).
	// A missed breath only leaves a breath in; a wrong region would cut a word or
	// a moment, so every bound leans to "not a breath".
	breathOn     = 0.25 // Breathing/Pant score of a breath frame...
	voiceOff     = 0.50 // ...with no voice at or above this in the same frame
	breathGap    = 3    // a breath frame at most this many frames after the last one is the same breath
	breathMinLen = 0.12 // seconds
	vocalMax     = 0.30 // a laugh, cough, hum... at or above this: not a breath
	speechOverDB = 10.0 // his voice: at least this far above becky-cut's threshold...
	voiceNear    = 0.30 // ...with the labeler hearing a voice within 0.12 s
	afterVoice   = 0.05 // a region starts this long after his voice ends
	beforeWord   = 0.04 // and ends this long before his next word
	reach        = 0.15 // and reaches at most this far past the breath itself
	sliverFrames = 2
	minFrames    = 4
)

// Verdicts.
const (
	vBreath   = "breath"
	vMovement = "movement"
	vFace     = "facial expression"
	vHeld     = "holding something up"
	vSound    = "laugh or cough"
	vUnclear  = "not checked"
)

type breath struct {
	A          float64   `json:"a"` // the region, source seconds, on the frame grid
	B          float64   `json:"b"`
	EvA        float64   `json:"heard_a"` // the breath the labeler heard
	EvB        float64   `json:"heard_b"`
	Breath     float64   `json:"breath"`
	Vocal      float64   `json:"vocal"`
	VocalLabel string    `json:"vocal_label,omitempty"`
	Look       picLook   `json:"look"`
	Held       heldCheck `json:"held"`
	Verdict    string    `json:"verdict"`
	Why        string    `json:"why,omitempty"`
}

// sedFrames is the sound labeler's answer, per 40 ms frame.
type sedFrames struct {
	breath, voice, vocal []float64
	vocalK               []int
	have                 []bool
	names                []string
}

func (s *sedFrames) ok(f int) bool { return f >= 0 && f < len(s.have) && s.have[f] }

// maxVoice is the strongest voice in frames [f0, f1).
func (s *sedFrames) maxVoice(f0, f1 int) float64 {
	m := 0.0
	for f := max(f0, 0); f < f1; f++ {
		if s.ok(f) {
			m = math.Max(m, s.voice[f])
		}
	}
	return m
}

// soundFrames runs the sound labeler over the spans (one call, ~10 s on the GPU).
func soundFrames(cfg config.Config, wav, work string, spans []span) (*sedFrames, error) {
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
	js := make([][2]float64, len(spans))
	for i, s := range spans {
		js[i] = [2]float64{s.A, s.B}
	}
	spanFile := filepath.Join(work, "breath-spans.json")
	writeJSON(spanFile, js)
	cmd := exec.Command(cfg.SoundLabelPython, script, "--wav", wav, "--repo", cfg.SoundLabelRepo, "--spans", spanFile, "--frames")
	var stderr strings.Builder
	cmd.Stderr = &stderr
	out, runErr := cmd.Output()
	var res struct {
		OK     bool     `json:"ok"`
		Reason string   `json:"reason"`
		Names  []string `json:"vocal_names"`
		Spans  []struct {
			F0     int       `json:"f0"`
			Breath []float64 `json:"breath"`
			Voice  []float64 `json:"voice"`
			Vocal  []float64 `json:"vocal"`
			VocalK []int     `json:"vocal_k"`
		} `json:"spans"`
	}
	if err := json.Unmarshal([]byte(lastJSONLine(string(out))), &res); err != nil {
		return nil, fmt.Errorf("the sound labeler gave no answer: %v (%s)", runErr, lastLines(stderr.String(), 2))
	}
	if !res.OK {
		return nil, fmt.Errorf("%s", res.Reason)
	}
	n := 0
	for _, sp := range res.Spans {
		n = max(n, sp.F0+len(sp.Breath))
	}
	s := &sedFrames{breath: make([]float64, n), voice: make([]float64, n), vocal: make([]float64, n),
		vocalK: make([]int, n), have: make([]bool, n), names: res.Names}
	for _, sp := range res.Spans {
		if len(sp.Voice) != len(sp.Breath) || len(sp.Vocal) != len(sp.Breath) || len(sp.VocalK) != len(sp.Breath) {
			return nil, errors.New("the sound labeler's answer is malformed")
		}
		for i := range sp.Breath {
			f := sp.F0 + i
			s.breath[f], s.voice[f], s.vocal[f], s.vocalK[f], s.have[f] = sp.Breath[i], sp.Voice[i], sp.Vocal[i], sp.VocalK[i], true
		}
	}
	return s, nil
}

// breathEvents are the breaths heard inside one kept piece [a, b].
func breathEvents(s *sedFrames, a, b float64) []span {
	f0, f1 := int(math.Ceil(a/sedHop-1e-9)), int(b/sedHop)
	var runs [][2]int
	for f := f0; f < f1; f++ {
		if !s.ok(f) || s.breath[f] < breathOn || s.voice[f] >= voiceOff {
			continue
		}
		if n := len(runs); n > 0 && f-runs[n-1][1] <= breathGap {
			runs[n-1][1] = f
			continue
		}
		runs = append(runs, [2]int{f, f})
	}
	var out []span
	for _, r := range runs {
		if float64(r[1]-r[0]+1)*sedHop >= breathMinLen-1e-9 {
			out = append(out, span{float64(r[0]) * sedHop, float64(r[1]+1) * sedHop})
		}
	}
	return out
}

// speechAt: his voice at t - loud enough to be speech, and the labeler hears a
// voice in the frames from f+lo to f+hi (exclusive).
func speechAt(au *audio, s *sedFrames, thr, t float64, lo, hi int) bool {
	f := int(t / sedHop)
	return au.dbAt(t) >= thr+speechOverDB && s.maxVoice(f+lo, f+hi) >= voiceNear
}

// onsetAfter is where his next word starts after t (searching up to lim), in
// 10 ms steps.
func onsetAfter(au *audio, s *sedFrames, thr, t, lim float64) (float64, bool) {
	for i := 0; ; i++ {
		x := t + float64(i)*0.01
		if x >= lim {
			return 0, false
		}
		if speechAt(au, s, thr, x, 0, 3) {
			return x - 0.01, true
		}
	}
}

// voiceEndBefore is where his voice ends before t (searching back to lim).
func voiceEndBefore(au *audio, s *sedFrames, thr, t, lim float64) (float64, bool) {
	for i := 0; ; i++ {
		x := t - float64(i)*0.01
		if x <= lim {
			return 0, false
		}
		if speechAt(au, s, thr, x, -2, 1) {
			return x + 0.01, true
		}
	}
}

// loudAfter is where the first loud sound after t starts (searching up to lim),
// a voice or not.
func loudAfter(au *audio, thr, t, lim float64) (float64, bool) {
	for i := 0; t+float64(i)*0.01 < lim; i++ {
		if x := t + float64(i)*0.01; au.dbAt(x) >= thr+speechOverDB {
			return x - 0.01, true
		}
	}
	return 0, false
}

// loudBefore is where the last loud sound before t ends (searching back to lim).
func loudBefore(au *audio, thr, t, lim float64) (float64, bool) {
	for i := 0; t-float64(i)*0.01 > lim; i++ {
		if x := t - float64(i)*0.01; au.dbAt(x) >= thr+speechOverDB {
			return x + 0.01, true
		}
	}
	return 0, false
}

// breathRegion turns one heard breath inside piece [a, b] into a region on the
// frame grid, with word-safe margins (see the header). ok=false: too short.
func breathRegion(au *audio, s *sedFrames, thr, fps, a, b float64, ev span) (lo, hi float64, ok bool) {
	on, hasOn := onsetAfter(au, s, thr, ev.B-0.04, b+0.5)
	ve, hasVe := voiceEndBefore(au, s, thr, ev.A+0.04, a-0.5)
	lo, hi = a, b
	if hasVe && ve >= a {
		lo = math.Max(math.Max(ve+afterVoice, ev.A-reach), a)
	} else if x, loud := loudBefore(au, thr, ev.A, a); loud {
		lo = math.Max(x+afterVoice, ev.A-reach) // no word before it in this piece, but a loud sound
	}
	noOnset := !hasOn || on > b
	if !noOnset {
		hi = math.Min(math.Min(on-beforeWord, ev.B+reach), b)
	} else if x, loud := loudAfter(au, thr, ev.B, b); loud {
		hi, noOnset = math.Min(x-beforeWord, ev.B+reach), false // ...or after it
	}
	return gridRegion(lo, hi, a, b, fps, noOnset)
}

// gridRegion puts [lo, hi] on the frame grid INSIDE itself, gives a sliver at a
// piece edge to the region, and drops what is left too short to matter.
func gridRegion(lo, hi, a, b, fps float64, toEnd bool) (float64, float64, bool) {
	f0, f1 := int(math.Ceil(lo*fps-1e-6)), int(math.Floor(hi*fps+1e-6))
	pa, pb := int(math.Round(a*fps)), int(math.Round(b*fps))
	if f0-pa <= sliverFrames {
		f0 = pa
	}
	if pb-f1 <= sliverFrames && toEnd {
		f1 = pb
	}
	if f1-f0 < minFrames {
		return 0, 0, false
	}
	return float64(f0) / fps, float64(f1) / fps, true
}

// findBreaths lists every breath region in the kept pieces (sorted, never
// overlapping: two that touch or leave a sliver between them become one).
func findBreaths(au *audio, s *sedFrames, thr, fps float64, pieces []span) []breath {
	var out []breath
	for _, p := range pieces {
		var inPiece []breath
		for _, ev := range breathEvents(s, p.A, p.B) {
			lo, hi, ok := breathRegion(au, s, thr, fps, p.A, p.B, ev)
			if !ok {
				continue
			}
			b := breath{A: lo, B: hi, EvA: ev.A, EvB: ev.B}
			if n := len(inPiece); n > 0 && lo-inPiece[n-1].B <= float64(sliverFrames)/fps+1e-9 {
				last := &inPiece[n-1]
				last.B, last.EvB = math.Max(last.B, hi), ev.B
				continue
			}
			inPiece = append(inPiece, b)
		}
		out = append(out, inPiece...)
	}
	for i := range out {
		out[i].Breath, out[i].Vocal, out[i].VocalLabel = s.strongest(out[i].EvA, out[i].EvB)
	}
	return out
}

// strongest is the strongest breath and mouth sound inside [a, b].
func (s *sedFrames) strongest(a, b float64) (br, vocal float64, label string) {
	for f := int(a/sedHop + 1e-9); float64(f)*sedHop < b-1e-9; f++ {
		if !s.ok(f) {
			continue
		}
		br = math.Max(br, s.breath[f])
		if s.vocal[f] > vocal {
			vocal = s.vocal[f]
			if k := s.vocalK[f]; k >= 0 && k < len(s.names) {
				label = s.names[k]
			}
		}
	}
	return br, vocal, label
}

// breathResult is what the timeline and the report need from the check.
type breathResult struct {
	Spots []breath
	Note  string
	Notes []string
}

// runBreathCheck finds the breaths in the kept pieces and rules out everything
// that is part of the moment. A signal that cannot be measured places NO region
// (never back to a guess) and the note says why.
func runBreathCheck(cfg config.Config, media, work, stem, wav string, au *audio, thr, fps float64, pieces []span, logf func(string, ...any)) breathResult {
	logf("breath check: listening for breaths in the kept clips, then watching the picture...")
	spans := make([]span, len(pieces))
	for i, p := range pieces {
		spans[i] = span{math.Max(0, p.A-0.6), p.B + 0.6} // margins search 0.5 s past the piece
	}
	s, err := soundFrames(cfg, wav, work, spans)
	if err != nil {
		logf("  breath check skipped: %v", err)
		return breathResult{Note: "the sound check did not run: " + err.Error()}
	}
	spots := findBreaths(au, s, thr, fps, pieces)
	res := breathResult{Spots: spots}
	var look []span
	for i := range spots {
		if spots[i].Vocal >= vocalMax {
			spots[i].Verdict, spots[i].Why = vSound, fmt.Sprintf("%s %.2f", spots[i].VocalLabel, spots[i].Vocal)
			continue
		}
		look = append(look, span{spots[i].A, spots[i].B})
	}
	pics, err := loadPictures(cfg, media, picCache(work, stem), look)
	if err != nil {
		res.Notes = append(res.Notes, "the picture check did not run: "+err.Error())
	}
	var ask []float64
	for i := range spots {
		if spots[i].Verdict != "" {
			continue
		}
		if err != nil {
			spots[i].Verdict, spots[i].Why = vUnclear, "the picture could not be checked"
			continue
		}
		spots[i].Look = pics.look(spots[i].A, spots[i].B)
		if v, why := pictureVerdict(spots[i].Look, spots[i].Breath); v != "" {
			spots[i].Verdict, spots[i].Why = v, why
			continue
		}
		ask = append(ask, heldTime(spots[i]))
	}
	held, notes := checkHeld(cfg, media, work, ask, logf)
	res.Notes = append(res.Notes, notes...)
	for i := range spots {
		if spots[i].Verdict != "" {
			continue
		}
		h := held[fmt.Sprintf("%09.3f", heldTime(spots[i]))]
		spots[i].Held = h
		switch {
		case h.yes():
			spots[i].Verdict, spots[i].Why = vHeld, h.why()
		case !h.Done:
			spots[i].Verdict, spots[i].Why = vUnclear, "Falcon or Gemma could not look at it"
		default:
			spots[i].Verdict = vBreath
		}
	}
	logf("  %d breaths heard, %d regions placed", len(spots), countVerdict(spots, vBreath))
	return res
}

// heldTime is the frame Falcon and Gemma look at: the middle of the region, on
// the 0.1 s grid so a re-run finds it in the cache.
func heldTime(b breath) float64 { return math.Round((b.A+b.B)/2*10) / 10 }

func countVerdict(spots []breath, v string) int {
	n := 0
	for _, s := range spots {
		if s.Verdict == v {
			n++
		}
	}
	return n
}

func breathLabel(n, of int, b breath) string {
	return fmt.Sprintf("%s %d of %d (%.1f s)", breathPrefix, n, of, b.B-b.A)
}

// breathRegions puts a region on the timeline over every breath.
func breathRegions(spots []breath, ps []piece) []mark {
	var ok []breath
	for _, s := range spots {
		if s.Verdict == vBreath {
			ok = append(ok, s)
		}
	}
	var ms []mark
	for n, b := range ok {
		if t0, t1, in := toTimeline(ps, b.A, b.B); in {
			ms = append(ms, mark{At: t0, Len: t1 - t0, Label: breathLabel(n+1, len(ok), b)})
		}
	}
	return ms
}

// breathSummary is the report's one-line summary and its detail list.
func breathSummary(spots []breath, note string, notes []string) (line string, details []string) {
	if note != "" {
		return fmt.Sprintf("- **Breath check:** skipped - %s. No breath regions; nothing was cut.", note), nil
	}
	n := map[string]int{}
	secs := 0.0
	for _, s := range spots {
		n[s.Verdict]++
		if s.Verdict == vBreath {
			secs += s.B - s.A
		}
	}
	line = fmt.Sprintf("- **Breath check:** %d breath region(s), %.1f s, every edge on a frame; nothing was cut. Heard but left alone: %d movement, %d facial expression, %d holding something up, %d laugh or cough, %d not checked.",
		n[vBreath], secs, n[vMovement], n[vFace], n[vHeld], n[vSound], n[vUnclear])
	if len(notes) > 0 {
		line += " (" + strings.Join(notes, "; ") + ")"
	}
	for _, s := range spots {
		d := fmt.Sprintf("- stream %s: %.2f s - %s", clock(s.A), s.B-s.A, s.Verdict)
		switch {
		case s.Verdict == vBreath:
			d += fmt.Sprintf(" (breathing %.2f; picture still)", s.Breath)
		case s.Why != "":
			d += " (" + s.Why + ")"
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

// redoBreaths (--breaths-only) re-runs only the breath check on this model's
// saved project: open it, take the old breath regions off, put the new ones on,
// save, and rewrite the breath lines of its report. Nothing else on the
// timeline is touched, and becky-cut / BeckyCut.cs are never run.
func (r *run) redoBreaths() {
	veg := filepath.Join(filepath.Dir(r.media), filepath.Base(filepath.Dir(r.media))+"-"+r.tag+".veg")
	wav := filepath.Join(r.work, "source16k.wav")
	cutPath := filepath.Join(r.work, "becky-cut.json")
	for _, p := range []string{veg, wav, cutPath} {
		if _, err := os.Stat(p); err != nil {
			fatal("make the edit first - " + filepath.Base(p) + " is missing")
		}
	}
	if err := r.probe(); err != nil {
		fatal(err.Error())
	}
	cr, err := loadCutReport(cutPath)
	if err != nil {
		fatal(err.Error())
	}
	au, err := readWAV16(wav)
	if err != nil {
		fatal(err.Error())
	}

	r.logf("VEGAS: opening %s...", filepath.Base(veg))
	if err := launchVegas(); err != nil {
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

	bc := runBreathCheck(r.cfg, r.media, r.work, r.stem, wav, au, cr.ThresholdDB, r.fps, pieces, r.logf)
	// The old regions go even when the check could not run: a guess must not sit
	// on the timeline looking like a fact.
	rep, err := vegas(2*time.Minute, "delete_marks", "prefix="+oldBreathLabel+"|"+breathPrefix)
	if err != nil {
		fatal(err.Error())
	}
	var del struct {
		Markers int `json:"markers_removed"`
		Regions int `json:"regions_removed"`
	}
	_ = json.Unmarshal(rep.Result, &del)
	r.logf("VEGAS: %d old breath mark(s) removed", del.Markers+del.Regions)
	marks := breathRegions(bc.Spots, ps)
	if err := addMarks(marks, r.fps, r.work, r.logf); err != nil {
		fatal("the old breath regions are off, but the new ones could not be added: " + err.Error())
	}

	path := filepath.Join(r.work, "report-"+r.tag+".md")
	line, details := breathSummary(bc.Spots, bc.Note, bc.Notes)
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

// cutBreaths takes every checked breath (verdict "breath": the sound labeler
// heard it and the picture is still) out of the pieces. The regions are already
// on the frame grid and inside one piece each (breathRegion/gridRegion), so the
// pieces left keep frame-exact edges. Jordan, 2026-10-07, after judging the 22
// regions on the 27-livestream: "the breaths identified are spot on -
// definitely those should all be removed".
func cutBreaths(pieces []span, spots []breath) []span {
	var cuts []span
	for _, b := range spots {
		if b.Verdict == vBreath {
			cuts = append(cuts, span{b.A, b.B})
		}
	}
	out := append([]span{}, pieces...)
	for _, c := range cuts {
		var next []span
		for _, p := range out {
			if c.B <= p.A+1e-6 || c.A >= p.B-1e-6 {
				next = append(next, p)
				continue
			}
			if c.A > p.A+1e-6 {
				next = append(next, span{p.A, c.A})
			}
			if c.B < p.B-1e-6 {
				next = append(next, span{c.B, p.B})
			}
		}
		out = next
	}
	return out
}
