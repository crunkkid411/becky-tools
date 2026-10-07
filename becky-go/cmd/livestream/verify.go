package main

// verify.go - report item #8, the edit check. It is what caught the missing
// sentences in the apology edit, so it runs on every edit, after VEGAS builds it:
//
//  1. the timeline VEGAS made is compared with the predicted pieces (same frames?
//     gaps? picture and sound still together?);
//  2. the edit's audio is rebuilt from the timeline exactly as it plays,
//     re-transcribed, and lined up with the planned words BY TIMELINE TIME.
//     A planned word that is not heard is "lost" when its whole span was cut,
//     else a possible clipped word; heard words that were not planned are
//     leftovers (or words the first transcript missed).
//
// Word times are never trusted alone: Parakeet puts the first word after a
// pause up to ~0.3 s early, inside the silence BeckyCut removes. On 2026-10-05
// "I have hair" (stream 13:10) was reported lost from three edits though every
// one of them plays it - the audio there is -60 dB until 791.0 s, exactly where
// BeckyCut's kept piece starts. So a word only counts as lost when two signals
// agree: its span is gone AND the re-transcription did not hear it.

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"unicode"
)

const matchTolerance = 0.75 // seconds between a planned word and the heard one

// Spot is a run of words that did not line up.
type Spot struct {
	Timeline float64 `json:"timeline"`
	Source   float64 `json:"source"`
	Words    int     `json:"words"`
	Text     string  `json:"text"`
	NearJoin bool    `json:"near_join"`
}

// Verification is the edit check's result.
type Verification struct {
	Timeline        string  `json:"timeline_health"`
	PredictedPieces int     `json:"predicted_pieces"`
	TimelinePieces  int     `json:"timeline_pieces"`
	IdenticalPieces int     `json:"identical_pieces"`
	EditSeconds     float64 `json:"edit_seconds"`
	PlannedWords    int     `json:"planned_words"`
	LostWords       []Spot  `json:"lost_words,omitempty"` // planned words no longer on the timeline
	HeardWords      int     `json:"heard_words"`
	MatchedWords    int     `json:"matched_words"`
	Missing         []Spot  `json:"not_heard,omitempty"` // planned, on the timeline, not heard
	Extra           []Spot  `json:"unplanned,omitempty"` // heard, not planned
	Note            string  `json:"note,omitempty"`
	OnePass         bool    `json:"parakeet_only,omitempty"` // another VEGAS was open: no WhisperX pass
}

func normWord(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || r == '\'' {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// similar: equal, or close enough that a re-transcription spelled it differently.
func similar(a, b string) bool {
	if a == b {
		return a != ""
	}
	if a == "" || b == "" {
		return false
	}
	return 1-float64(levenshtein(a, b))/float64(max(len(a), len(b))) >= 0.6
}

func levenshtein(a, b string) int {
	ra, rb := []rune(a), []rune(b)
	prev := make([]int, len(rb)+1)
	cur := make([]int, len(rb)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(ra); i++ {
		cur[0] = i
		for j := 1; j <= len(rb); j++ {
			c := 1
			if ra[i-1] == rb[j-1] {
				c = 0
			}
			cur[j] = min(prev[j]+1, cur[j-1]+1, prev[j-1]+c)
		}
		prev, cur = cur, prev
	}
	return prev[len(rb)]
}

// fromTimeline maps a timeline time back to the source; join is the distance to
// the nearest cut.
func fromTimeline(ps []piece, t float64) (src, join float64) {
	i := sort.Search(len(ps), func(i int) bool { return ps[i].TL > t }) - 1
	i = max(i, 0)
	p := ps[i]
	end := p.TL + (p.Out - p.In)
	return p.In + (t - p.TL), math.Min(math.Abs(t-p.TL), math.Abs(end-t))
}

// tlOf is where a planned word plays on the timeline: the start of the part of
// its span still there, or, when the whole span was cut, the cut itself (where
// a mis-timed word that still plays will be heard). on=false: span all cut.
func tlOf(ps []piece, s0, s1 float64) (tl float64, on bool) {
	if t0, _, ok := toTimeline(ps, s0, s1); ok {
		return t0, true
	}
	for _, p := range ps {
		if p.In >= s1 {
			return p.TL, false
		}
	}
	if len(ps) == 0 {
		return 0, false
	}
	last := ps[len(ps)-1]
	return last.TL + (last.Out - last.In), false
}

// rebuildAudio concatenates the timeline's pieces from the source PCM.
func rebuildAudio(au *audio, ps []piece) []int16 {
	var out []int16
	for _, p := range ps {
		a := min(max(int(math.Round(p.In*float64(au.sr))), 0), len(au.pcm))
		b := min(max(int(math.Round(p.Out*float64(au.sr))), a), len(au.pcm))
		out = append(out, au.pcm[a:b]...)
	}
	return out
}

type tword struct {
	norm, text string
	src, tl    float64
	join       float64
	used       bool
}

// matchByTime lines planned words up with heard ones. Both lists are in
// timeline order; a heard word may match one planned word within matchTolerance.
func matchByTime(planned, heard []tword) (missing, extra []int, matched int) {
	j0 := 0
	for i := range planned {
		p := &planned[i]
		for j0 < len(heard) && heard[j0].tl < p.tl-matchTolerance {
			j0++
		}
		best, bestScore := -1, math.Inf(1)
		for j := j0; j < len(heard) && heard[j].tl <= p.tl+matchTolerance; j++ {
			h := &heard[j]
			if h.used || !similar(p.norm, h.norm) {
				continue
			}
			score := math.Abs(h.tl - p.tl)
			if h.norm != p.norm {
				score += matchTolerance // prefer an exact spelling
			}
			if score < bestScore {
				best, bestScore = j, score
			}
		}
		if best < 0 {
			missing = append(missing, i)
			continue
		}
		heard[best].used = true
		p.used = true
		matched++
	}
	for j := range heard {
		if !heard[j].used {
			extra = append(extra, j)
		}
	}
	return missing, extra, matched
}

// spots groups consecutive indexes into runs of at least minWords.
func spots(ws []tword, idx []int, minWords int) []Spot {
	var out []Spot
	for k := 0; k < len(idx); {
		e := k
		for e+1 < len(idx) && idx[e+1] == idx[e]+1 {
			e++
		}
		if n := e - k + 1; n >= minWords {
			var txt []string
			near := false
			for _, i := range idx[k : e+1] {
				txt = append(txt, ws[i].text)
				near = near || ws[i].join < 0.3
			}
			out = append(out, Spot{Timeline: ws[idx[k]].tl, Source: ws[idx[k]].src, Words: n, Text: strings.Join(txt, " "), NearJoin: near})
		}
		k = e + 1
	}
	return out
}

// verifyEdit runs the whole check. transcribe is becky-transcribe's path.
func verifyEdit(ps []piece, predicted []span, fps float64, au *audio, words []Word, keep []bool,
	work, tag, health string, logf func(string, ...any)) Verification {
	v := Verification{Timeline: health, PredictedPieces: len(predicted), TimelinePieces: len(ps)}
	have := map[[2]int64]bool{}
	for _, p := range ps {
		have[[2]int64{int64(math.Round(p.In * fps)), int64(math.Round(p.Out * fps))}] = true
		v.EditSeconds += p.Out - p.In
	}
	for _, s := range predicted {
		if have[[2]int64{int64(math.Round(s.A * fps)), int64(math.Round(s.B * fps))}] {
			v.IdenticalPieces++
		}
	}

	var planned []tword
	var offIdx []int // whole span cut: lost unless the re-transcription hears it
	off := map[int]bool{}
	for i, w := range words {
		if !keep[i] {
			continue
		}
		tw := tword{norm: normWord(w.Word), text: w.Word, src: w.Start}
		if tw.norm == "" {
			continue
		}
		var on bool
		tw.tl, on = tlOf(ps, w.Start, math.Max(w.End, w.Start+0.05))
		planned = append(planned, tw)
		if !on {
			offIdx = append(offIdx, len(planned)-1)
			off[len(planned)-1] = true
		}
	}
	v.PlannedWords = len(planned)
	v.LostWords = spots(planned, offIdx, 1) // narrowed to the unheard ones below

	wav := filepath.Join(work, "edit-"+tag+".wav")
	if err := writeWAV16(wav, au.sr, rebuildAudio(au, ps)); err != nil {
		v.Note = "could not rebuild the edit's audio: " + err.Error()
		return v
	}
	bin, err := beckyBin("becky-transcribe")
	if err != nil {
		v.Note = err.Error()
		return v
	}
	out := filepath.Join(work, "edit-"+tag+".transcript.json")
	logf("checking the edit: re-transcribing its %.1f minutes of audio...", v.EditSeconds/60)
	args := []string{wav, "--output", out}
	if vegasCount() > 1 {
		// Another VEGAS - maybe with unsaved work - shares the 8 GB graphics card
		// with this run's: WhisperX's second opinion (~4.5 GB) is skipped.
		v.OnePass = true
		args = append(args, "--single-pass")
		logf("  (another VEGAS is open, so only Parakeet listens - the second opinion would crowd the graphics card)")
	}
	if res, err := exec.Command(bin, args...).CombinedOutput(); err != nil {
		v.Note = fmt.Sprintf("the edit could not be re-transcribed: %v (%s)", err, lastLines(string(res), 2))
		return v
	}
	ew, err := loadWords(out)
	if err != nil {
		v.Note = err.Error()
		return v
	}
	var heard []tword
	for _, w := range ew {
		n := normWord(w.Word)
		if n == "" {
			continue
		}
		src, join := fromTimeline(ps, w.Start)
		heard = append(heard, tword{norm: n, text: w.Word, src: src, tl: w.Start, join: join})
	}
	sort.SliceStable(heard, func(i, j int) bool { return heard[i].tl < heard[j].tl })
	v.HeardWords = len(heard)
	missing, extra, matched := matchByTime(planned, heard)
	v.MatchedWords = matched
	var lostIdx, notHeard []int
	for _, i := range missing {
		_, planned[i].join = fromTimeline(ps, planned[i].tl)
		if off[i] {
			lostIdx = append(lostIdx, i)
		} else {
			notHeard = append(notHeard, i)
		}
	}
	v.LostWords = spots(planned, lostIdx, 1)
	v.Missing = spots(planned, notHeard, 2)
	v.Extra = spots(heard, extra, 2)
	b, _ := json.MarshalIndent(v, "", " ")
	_ = os.WriteFile(filepath.Join(work, "verify-"+tag+".json"), b, 0o644)
	return v
}
