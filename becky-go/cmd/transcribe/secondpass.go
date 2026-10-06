package main

// The second pass (2026-10-05; Jordan: the human must never hunt for missing
// words). Parakeet stays the primary transcript. WhisperX, run through
// Jordan's OWN install with his tested settings
// (X:\Videos\video_tools\whsiperx_basic.bat), is the second opinion. A
// passage only WhisperX heard is never trusted on WhisperX's word alone:
// Parakeet re-listens to just that stretch in a fresh window (the second look).
//   - Parakeet hears it too        -> two models agree: Parakeet's words go in.
//   - Parakeet still hears nothing -> WhisperX's words go in only when its
//     aligner matched them strongly to the audio (score >= minAlignScore);
//     otherwise the passage is listed as unconfirmed and stays OUT of the words.
// WhisperX word times run late (Jordan: "typically delayed by 2-5 frames"), so
// its words are shifted by the median offset measured against Parakeet on the
// words both heard. Neither model's word times decide a cut: auto-editor's cut
// times are the gold standard, and the edit tools snap to them.

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"unicode"

	"becky-go/internal/proc"
)

const (
	coverTolerance = 0.25 // s: a WhisperX word with a Parakeet word this close was heard by both
	runMaxGap      = 1.0  // s: a pause this long splits two missed passages
	runMinWords    = 2    // shorter misses are filler noise, not lost sentences
	minAlignScore  = 0.5  // WhisperX's own aligner must match the words this well to stand alone
)

// Passage is one stretch of speech the main Parakeet pass did not have.
type Passage struct {
	Start        float64  `json:"start"`
	End          float64  `json:"end"`
	Text         string   `json:"text"`
	Source       string   `json:"source,omitempty"` // "parakeet-recheck" | "whisperx"
	WhisperXText string   `json:"whisperx_text,omitempty"`
	Score        *float64 `json:"whisperx_align_score,omitempty"`
}

// SecondPass is the audit trail of the second opinion, so nothing it changed
// is invisible.
type SecondPass struct {
	Model        string    `json:"model"`
	Ran          bool      `json:"ran"`
	Note         string    `json:"note,omitempty"`
	Recovered    []Passage `json:"recovered_passages,omitempty"`
	Unconfirmed  []Passage `json:"unconfirmed_passages,omitempty"`
	OffsetMS     float64   `json:"whisperx_minus_parakeet_ms"`
	MatchedWords int       `json:"matched_words"`
	AgreePct     float64   `json:"start_agree_within_100ms_pct"`
}

const whisperXModel = "whisperx large-v2 + WAV2VEC2_ASR_LARGE_LV60K_960H"

// wxWord is one entry of WhisperX's JSON "word_segments". Words its aligner
// could not place (digits, symbols) come without start/end.
type wxWord struct {
	Word  string   `json:"word"`
	Start *float64 `json:"start"`
	End   *float64 `json:"end"`
	Score *float64 `json:"score"`
}

// timedWord is a WhisperX word after missing times are filled in.
type timedWord struct {
	Word       string
	Start, End float64
	Score      float64
	HasScore   bool
}

// whisperXArgs is Jordan's tested command line (whsiperx_basic.bat), with JSON
// output to dir. The language is pinned so a music intro can't be detected as
// another language.
func whisperXArgs(wav, lang, dir string) []string {
	return []string{wav,
		"--model", "large-v2",
		"--align_model", "WAV2VEC2_ASR_LARGE_LV60K_960H",
		"--compute_type", "float16",
		"--vad_method", "silero", "--vad_onset", "0.1", "--vad_offset", "0.1",
		"--interpolate_method", "linear",
		"--language", lang,
		"--output_format", "json", "--output_dir", dir,
		"--verbose", "False"}
}

// runWhisperX runs Jordan's WhisperX on wav and returns its aligned words.
func runWhisperX(exe, wav, lang string, verbose bool) ([]timedWord, error) {
	dir, err := os.MkdirTemp("", "becky-whisperx-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(dir)
	cmd := exec.Command(exe, whisperXArgs(wav, lang, dir)...)
	proc.NoWindow(cmd)
	// Its own venv only: this PC sets PYTHONUSERBASE/PIP_TARGET globally, and a
	// user site-packages dir must never shadow WhisperX's pinned torch.
	cmd.Env = append(cleanEnv(os.Environ(), "PYTHONUSERBASE", "PIP_TARGET"), "PYTHONNOUSERSITE=1")
	var logs strings.Builder
	cmd.Stdout = &logs
	if verbose {
		cmd.Stderr = os.Stderr
	} else {
		cmd.Stderr = &logs
	}
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("whisperx failed: %v\n%s", err, tail(logs.String()))
	}
	base := strings.TrimSuffix(filepath.Base(wav), filepath.Ext(wav))
	data, err := os.ReadFile(filepath.Join(dir, base+".json"))
	if err != nil {
		return nil, fmt.Errorf("whisperx wrote no JSON: %v", err)
	}
	var res struct {
		WordSegments []wxWord `json:"word_segments"`
	}
	if err := json.Unmarshal(data, &res); err != nil {
		return nil, fmt.Errorf("whisperx JSON: %v", err)
	}
	return fillTimes(res.WordSegments), nil
}

// cleanEnv drops the named variables from an environment list.
func cleanEnv(env []string, names ...string) []string {
	out := make([]string, 0, len(env))
	for _, kv := range env {
		drop := false
		for _, n := range names {
			if strings.HasPrefix(strings.ToUpper(kv), strings.ToUpper(n)+"=") {
				drop = true
			}
		}
		if !drop {
			out = append(out, kv)
		}
	}
	return out
}

// fillTimes gives every word a start and end. A word WhisperX's aligner could
// not place gets times linearly interpolated between its timed neighbours
// (Jordan's --interpolate_method linear). Words with no timed word anywhere
// are dropped.
func fillTimes(ws []wxWord) []timedWord {
	type known struct {
		i          int
		start, end float64
	}
	var ks []known
	for i, w := range ws {
		if w.Start != nil && w.End != nil {
			ks = append(ks, known{i, *w.Start, *w.End})
		}
	}
	if len(ks) == 0 {
		return nil
	}
	out := make([]timedWord, 0, len(ws))
	k := 0
	for i, w := range ws {
		tw := timedWord{Word: strings.TrimSpace(w.Word)}
		if w.Score != nil {
			tw.Score, tw.HasScore = *w.Score, true
		}
		for k+1 < len(ks) && ks[k+1].i <= i {
			k++
		}
		switch {
		case w.Start != nil && w.End != nil:
			tw.Start, tw.End = *w.Start, *w.End
		case i < ks[0].i:
			tw.Start, tw.End = ks[0].start, ks[0].start
		case k+1 >= len(ks):
			tw.Start, tw.End = ks[k].end, ks[k].end
		default:
			a, b := ks[k], ks[k+1]
			f := float64(i-a.i) / float64(b.i-a.i)
			t := a.end + f*(b.start-a.end)
			tw.Start, tw.End = t, t
		}
		out = append(out, tw)
	}
	return out
}

// normWord lowercases and strips everything but letters, digits and apostrophes.
func normWord(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || r == '\'' {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// covered reports whether any Parakeet word overlaps [a-tol, b+tol]. p must be
// sorted by start.
func covered(p []Word, a, b, tol float64) bool {
	i := sort.Search(len(p), func(i int) bool { return p[i].Start >= a-tol-10 })
	for ; i < len(p) && p[i].Start <= b+tol; i++ {
		if p[i].End >= a-tol {
			return true
		}
	}
	return false
}

// uncoveredRuns groups the WhisperX words no Parakeet word overlaps into
// passages (split at pauses over runMaxGap), keeping those of runMinWords+.
func uncoveredRuns(p []Word, w []timedWord) [][]timedWord {
	var runs [][]timedWord
	var cur []timedWord
	flush := func() {
		if len(cur) >= runMinWords {
			runs = append(runs, cur)
		}
		cur = nil
	}
	for _, x := range w {
		if covered(p, x.Start, x.End, coverTolerance) {
			flush()
			continue
		}
		if len(cur) > 0 && x.Start-cur[len(cur)-1].End > runMaxGap {
			flush()
		}
		cur = append(cur, x)
	}
	flush()
	return runs
}

// startOffset measures WhisperX start minus Parakeet start on words both heard
// (same normalized text, 3+ letters, starts within 1 s): the median, how many
// words matched, and the share within 100 ms of the median.
func startOffset(p []Word, w []timedWord) (median float64, n int, agreePct float64) {
	var d []float64
	for _, x := range w {
		nx := normWord(x.Word)
		if len([]rune(nx)) < 3 {
			continue
		}
		i := sort.Search(len(p), func(i int) bool { return p[i].Start >= x.Start-1 })
		best, found := 0.0, false
		for ; i < len(p) && p[i].Start <= x.Start+1; i++ {
			if normWord(p[i].Word) == nx {
				dd := x.Start - p[i].Start
				if !found || math.Abs(dd) < math.Abs(best) {
					best, found = dd, true
				}
			}
		}
		if found {
			d = append(d, best)
		}
	}
	if len(d) == 0 {
		return 0, 0, 0
	}
	sort.Float64s(d)
	median = d[len(d)/2]
	if len(d)%2 == 0 {
		median = (d[len(d)/2-1] + d[len(d)/2]) / 2
	}
	agree := 0
	for _, x := range d {
		if math.Abs(x-median) <= 0.1 {
			agree++
		}
	}
	return median, len(d), 100 * float64(agree) / float64(len(d))
}

// newWords returns the candidate words that are not already in p (no Parakeet
// word overlapping them by time), so a second look never duplicates words.
func newWords(p []Word, cand []Word) []Word {
	var out []Word
	for _, c := range cand {
		if !covered(p, c.Start, c.End, 0.05) {
			out = append(out, c)
		}
	}
	return out
}

// insertWords merges add into p, keeping start order.
func insertWords(p, add []Word) []Word {
	out := append(append(make([]Word, 0, len(p)+len(add)), p...), add...)
	sort.SliceStable(out, func(i, j int) bool { return out[i].Start < out[j].Start })
	return out
}

func runText(r []timedWord) string {
	parts := make([]string, len(r))
	for i, x := range r {
		parts[i] = x.Word
	}
	return strings.Join(parts, " ")
}

func wordsText(ws []Word) string {
	parts := make([]string, len(ws))
	for i, x := range ws {
		parts[i] = x.Word
	}
	return strings.Join(parts, " ")
}

func meanScore(r []timedWord) (float64, bool) {
	sum, n := 0.0, 0
	for _, x := range r {
		if x.HasScore {
			sum += x.Score
			n++
		}
	}
	if n == 0 {
		return 0, false
	}
	return sum / float64(n), true
}

// recheckFunc re-listens to the given [start,end] spans with Parakeet and
// returns the words heard in each (nil = second look unavailable).
type recheckFunc func(spans [][2]float64) ([][]Word, error)

// mergeSecondPass applies the second opinion to the Parakeet words p and
// returns the merged words plus the audit trail.
func mergeSecondPass(p []Word, w []timedWord, recheck recheckFunc) ([]Word, SecondPass) {
	sp := SecondPass{Model: whisperXModel, Ran: true}
	p = append([]Word(nil), p...)
	sort.SliceStable(p, func(i, j int) bool { return p[i].Start < p[j].Start })
	off, n, agree := startOffset(p, w)
	sp.OffsetMS, sp.MatchedWords, sp.AgreePct = math.Round(off*1000), n, math.Round(agree*10)/10

	runs := uncoveredRuns(p, w)
	if len(runs) == 0 {
		return p, sp
	}
	var heard [][]Word
	if recheck != nil {
		spans := make([][2]float64, len(runs))
		for i, r := range runs {
			spans[i] = [2]float64{r[0].Start, r[len(r)-1].End}
		}
		h, err := recheck(spans)
		if err != nil {
			sp.Note = "second look unavailable: " + firstLine(err.Error())
		} else if len(h) == len(runs) {
			heard = h
		}
	}
	var add []Word
	for i, r := range runs {
		score, hasScore := meanScore(r)
		psg := Passage{Start: round3(r[0].Start - off), End: round3(r[len(r)-1].End - off), WhisperXText: runText(r)}
		if hasScore {
			s := math.Round(score*100) / 100
			psg.Score = &s
		}
		if heard != nil {
			if ws := newWords(p, heard[i]); len(ws) > 0 {
				for j := range ws {
					ws[j].Source = "parakeet-recheck"
				}
				add = append(add, ws...)
				psg.Start, psg.End, psg.Text, psg.Source = ws[0].Start, ws[len(ws)-1].End, wordsText(ws), "parakeet-recheck"
				sp.Recovered = append(sp.Recovered, psg)
				continue
			}
		}
		if hasScore && score >= minAlignScore {
			for _, x := range r {
				add = append(add, Word{Word: x.Word, Start: round3(x.Start - off), End: round3(math.Max(x.End, x.Start) - off), Source: "whisperx"})
			}
			psg.Text, psg.Source = psg.WhisperXText, "whisperx"
			sp.Recovered = append(sp.Recovered, psg)
			continue
		}
		psg.Text = psg.WhisperXText
		sp.Unconfirmed = append(sp.Unconfirmed, psg)
	}
	return insertWords(p, add), sp
}

func firstLine(s string) string {
	line, _, _ := strings.Cut(s, "\n")
	return line
}

// lastLine is the last non-empty line, where a Python traceback names the error.
func lastLine(s string) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	l := strings.TrimSpace(lines[len(lines)-1])
	if len(l) > 200 {
		l = l[:200]
	}
	return l
}

// secondPassFor runs WhisperX and the second look over the Parakeet words p.
// Degrades, never crashes: on any failure Parakeet's words are returned
// unchanged and the note says why in plain words.
func secondPassFor(exe, wav, lang string, recheck recheckFunc, p []Word, verbose bool) ([]Word, *SecondPass) {
	if exe == "" {
		return p, &SecondPass{Model: whisperXModel, Note: "WhisperX is not set up, so only one pass ran"}
	}
	if _, err := os.Stat(exe); err != nil {
		return p, &SecondPass{Model: whisperXModel, Note: "WhisperX was not found at " + exe + ", so only one pass ran"}
	}
	w, err := runWhisperX(exe, wav, lang, verbose)
	if err != nil {
		return p, &SecondPass{Model: whisperXModel, Note: "WhisperX could not run, so only one pass ran: " + lastLine(err.Error())}
	}
	merged, sp := mergeSecondPass(p, w, recheck)
	return merged, &sp
}

// runRecheck runs the DML helper's --spans mode: a fresh-window re-listen of
// each span.
func runRecheck(python, script, wav, device string, spans [][2]float64, verbose bool) ([][]Word, error) {
	f, err := os.CreateTemp("", "becky-spans-*.json")
	if err != nil {
		return nil, err
	}
	defer os.Remove(f.Name())
	if err := json.NewEncoder(f).Encode(spans); err != nil {
		f.Close()
		return nil, err
	}
	f.Close()
	cmd := exec.Command(python, script, wav, "--device", device, "--spans", f.Name(),
		"--chunk-overlap", strconv.FormatFloat(2, 'f', -1, 64))
	proc.NoWindow(cmd)
	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	if verbose {
		cmd.Stderr = os.Stderr
	} else {
		cmd.Stderr = &stderr
	}
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("second look failed: %v\n%s", err, tail(stderr.String()))
	}
	lines := strings.Split(strings.TrimSpace(stdout.String()), "\n")
	var res struct {
		Skipped bool   `json:"skipped"`
		Reason  string `json:"reason"`
		Spans   []struct {
			Words []Word `json:"words"`
		} `json:"spans"`
	}
	if err := json.Unmarshal([]byte(lines[len(lines)-1]), &res); err != nil {
		return nil, fmt.Errorf("second look output: %v", err)
	}
	if res.Skipped {
		return nil, fmt.Errorf("second look skipped: %s", res.Reason)
	}
	out := make([][]Word, len(res.Spans))
	for i, s := range res.Spans {
		out[i] = s.Words
	}
	return out, nil
}
