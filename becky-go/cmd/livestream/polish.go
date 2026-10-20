package main

// polish.go - --video: the cuts Jordan makes by eye on the waveform, and the
// thinking he takes out. Jordan, 2026-10-14 (user-feedback.md, after the first
// full YouTube edit):
//
//   - "Becky's workflow made the cut 4 frames earlier than the actual
//     zero-crossing point ... the closest frame to 'zero' dB without cutting off
//     part of the word" (vegas\zero-crossing-errors.JPG). becky-cut's edge sat on
//     low crackle that is neither breath nor speech. tighten moves every edge
//     onto his voice.
//   - "from 00:22.11 to 24.26 contains 3 clips which all should have been
//     removed ... 'Whatever', 'here's', and the third clip I just quietly laugh
//     as I look off camera to read my notes ... Examples like the two I just
//     pointed out exist throughout the timeline." A breath cut splits a piece
//     and can leave a stretch with no words (the laugh): secondListen gives each
//     such stretch a second listen and drops it when no word is heard (the
//     transcript misses real speech too - 1:47 "I'm going to summarize it" had
//     no words in it, so a missing word alone never drops anything). The short
//     words he says on their own while he finds his next line go in thinking.

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"unicode"
)

// Levels are read against the voice's own loudest moment near the edge, so a
// different mic or room changes nothing (Jordan: "I was using a different mic
// than what becky-cut was dialed in to use ... we should be adjusting for that").
// Measured on the goodbye-update recording: voice peaks -8 dB, the crackle before
// it -36 to -48 dB, the first 5 ms of the voice -32 dB.
const (
	edgeWin    = 0.005 // the level is measured every 5 ms
	edgeHold   = 0.03  // the voice stays this loud for 30 ms (one click is not a start)
	onsetBelow = 25.0  // the voice starts within 25 dB of the peak...
	tailBelow  = 30.0  // ...and ends at 30 dB under it (a word fades out more slowly)
	edgeLook   = 1.0   // the peak is the loudest second at that end of the piece
	edgeMove   = 0.6   // an edge moves at most this far
	floorAbove = 6.0   // never a threshold within 6 dB of the room's own noise
)

// levelDB is the RMS level of [t, t+d), in dBFS.
func (a *audio) levelDB(t, d float64) float64 {
	lo, hi := int(t*float64(a.sr)), int((t+d)*float64(a.sr))
	lo, hi = max(lo, 0), min(hi, len(a.pcm))
	if hi <= lo {
		return -120
	}
	var sum float64
	for _, v := range a.pcm[lo:hi] {
		sum += float64(v) * float64(v)
	}
	return 20 * math.Log10(math.Sqrt(sum/float64(hi-lo))/32768+1e-9)
}

// noiseFloor is the room's own level: the quietest tenth of 50 ms windows.
func (a *audio) noiseFloor() float64 {
	var ls []float64
	for t := 0.0; t+0.05 <= float64(len(a.pcm))/float64(a.sr); t += 0.05 {
		ls = append(ls, a.levelDB(t, 0.05))
	}
	if len(ls) == 0 {
		return -90
	}
	sort.Float64s(ls)
	return ls[len(ls)/10]
}

// hissSpan is how long a hiss (an "f", "s", "h", "th": see censor.go's
// hissDB) runs from t, backwards (dir -1) or forwards (dir 1), above the room
// noise: 0 unless it is 30-200 ms, a consonant. Longer is a breath, which goes.
func (a *audio) hissSpan(t, dir, floor float64) float64 {
	n := 0
	for ; n <= 20; n++ {
		w := t + dir*float64(n)*envStep
		if dir < 0 {
			w -= envStep
		}
		if a.hissDB(w, envStep) < hissMin || a.levelDB(w, envStep) < floor+floorAbove {
			break
		}
	}
	if n < hissRun || n > 20 {
		return 0
	}
	return float64(n) * envStep
}

func (a *audio) peakDB(t0, t1 float64) float64 {
	p := -120.0
	for t := t0; t+edgeWin <= t1+1e-9; t += edgeWin {
		p = math.Max(p, a.levelDB(t, edgeWin))
	}
	return p
}

// tighten puts every piece's start on the last frame before his voice begins
// and its end on the first frame after it stops. An edge that already sits in
// his voice (a cut inside running speech) stays where it is.
func tighten(ps []span, au *audio, fps float64) ([]span, int) {
	floor := au.noiseFloor()
	out := make([]span, 0, len(ps))
	moved := 0
	for _, p := range ps {
		a, b := tightEdges(p, au, fps, floor)
		if a != p.A || b != p.B {
			moved++
		}
		out = append(out, span{a, b})
	}
	return out, moved
}

func tightEdges(p span, au *audio, fps, floor float64) (float64, float64) {
	const eps = 1e-6
	a, b := p.A, p.B
	thr := math.Max(au.peakDB(a, math.Min(b, a+edgeLook))-onsetBelow, floor+floorAbove)
	if au.levelDB(a, edgeHold) < thr {
		for t := a; t < math.Min(b, a+edgeMove); t += edgeWin {
			if au.levelDB(t, edgeWin) >= thr && au.levelDB(t, edgeHold) >= thr {
				t -= au.hissSpan(t, -1, floor) // his "f", "s", "h" before the vowel is the word too
				if f := math.Floor(t*fps+eps) / fps; f > a+eps {
					a = f
				}
				break
			}
		}
	}
	thr = math.Max(au.peakDB(math.Max(a, b-edgeLook), b)-tailBelow, floor+floorAbove)
	if au.levelDB(b-edgeHold, edgeHold) < thr {
		for t := b - edgeWin; t > math.Max(a, b-edgeMove); t -= edgeWin {
			if au.levelDB(t, edgeWin) >= thr && au.levelDB(t+edgeWin-edgeHold, edgeHold) >= thr {
				t += au.hissSpan(t+edgeWin, 1, floor) // ... and the "s" of "this" after it
				if f := math.Ceil((t+edgeWin)*fps-eps) / fps; f < b-eps {
					b = f
				}
				break
			}
		}
	}
	if b-a < 3/fps { // never shrink a piece to nothing
		return p.A, p.B
	}
	return a, b
}

// unheard is a stretch the second listen checked.
type unheard struct {
	A     float64 `json:"a"`
	B     float64 `json:"b"`
	Heard string  `json:"heard"`
}

// secondListen: every piece with no transcript word in it gets a second listen
// (becky-transcribe on just those stretches, one call); the ones it hears no
// word in are dropped (a laugh, a sound while he reads his notes).
func (r *run) secondListen(ps []span, words []Word, au *audio) ([]span, []unheard) {
	var idx []int
	for i, p := range ps {
		if !hasWord(words, p.A, p.B) {
			idx = append(idx, i)
		}
	}
	if len(idx) == 0 {
		return ps, nil
	}
	// one WAV: each stretch, then 1 s of silence, so a word belongs to one stretch
	gap := au.sr
	var pcm []int16
	starts := make([]float64, len(idx))
	for k, i := range idx {
		starts[k] = float64(len(pcm)) / float64(au.sr)
		lo, hi := int(ps[i].A*float64(au.sr)), min(int(ps[i].B*float64(au.sr)), len(au.pcm))
		pcm = append(pcm, au.pcm[max(lo, 0):max(hi, lo)]...)
		pcm = append(pcm, make([]int16, gap)...)
	}
	wav := filepath.Join(r.work, "second-listen.wav")
	out := filepath.Join(r.work, "second-listen.transcript.json")
	heard := make([]string, len(idx))
	bin, err := beckyBin("becky-transcribe")
	if err == nil {
		err = writeWAV16(wav, au.sr, pcm)
	}
	if err == nil {
		r.logf("second listen: %d stretch(es) with no words in the transcript...", len(idx))
		var res []byte
		if res, err = exec.Command(bin, wav, "--output", out, "--single-pass").CombinedOutput(); err != nil {
			err = fmt.Errorf("%v (%s)", err, lastLines(string(res), 2))
		}
	}
	var ws []Word
	if err == nil {
		ws, err = loadWords(out)
		if err != nil && strings.Contains(err.Error(), "has no words") {
			err = nil // nothing heard anywhere
		}
	}
	if err != nil { // without the second listen nothing is dropped
		r.logf("  the second listen could not run (%v) - every stretch stays", err)
		return ps, nil
	}
	for _, w := range ws {
		n := normWord(w.Word)
		if n == "" || hesitations[n] {
			continue
		}
		k := sort.SearchFloat64s(starts, w.Start+1e-6) - 1
		if k >= 0 {
			heard[k] = strings.TrimSpace(heard[k] + " " + w.Word)
		}
	}
	var keep []span
	var checked []unheard
	drop := map[int]bool{}
	for k, i := range idx {
		checked = append(checked, unheard{ps[i].A, ps[i].B, heard[k]})
		drop[i] = heard[k] == ""
	}
	for i, p := range ps {
		if !drop[i] {
			keep = append(keep, p)
		}
	}
	r.logf("  %d dropped (no word heard twice), %d kept (words heard)", len(ps)-len(keep), len(idx)-(len(ps)-len(keep)))
	return keep, checked
}

// An island is a word or two he says on their own: a pause of islandGap before
// and after, kept speech on both sides. Half of them are content that finishes
// a sentence after a pause ("I do owe you guys a ... explanation.", "he
// blackmailed me into an apology ... saying this."), half are thinking
// ("Whatever.", "Here's", "And um") - measured on the goodbye update, 42
// islands. So the words alone only take out islands of filler; the rest go to
// the lead model with the words around them.
const (
	islandGap   = 0.5
	islandWords = 2
)

var fillers = map[string]bool{"and": true, "so": true, "like": true, "but": true, "or": true}

type island struct {
	W0, W1 int
	Text   string `json:"text"`
	At     string `json:"at"`
	Cut    bool   `json:"cut"`
	By     string `json:"by"`
}

// islands finds them among the kept words.
func islands(words []Word, keep []bool) []island {
	var kept []int
	for i := range words {
		if keep[i] && normWord(words[i].Word) != "" {
			kept = append(kept, i)
		}
	}
	var runs [][]int
	for k, i := range kept {
		if k == 0 || words[i].Start-words[kept[k-1]].End >= islandGap || i != kept[k-1]+1 {
			runs = append(runs, nil)
		}
		runs[len(runs)-1] = append(runs[len(runs)-1], i)
	}
	var out []island
	for j := 1; j+1 < len(runs); j++ {
		if len(runs[j]) > islandWords {
			continue
		}
		w0, w1 := runs[j][0], runs[j][len(runs[j])-1]
		prev, next := words[runs[j-1][len(runs[j-1])-1]], words[runs[j+1][0]]
		text := wordsText(words, w0, w1)
		if strings.HasSuffix(strings.TrimRight(prev.Word, `"')`), "?") {
			continue // an answer to his own question ("Yeah.") is never thinking
		}
		// A pause inside his sentence is emphasis, not thinking (goodbye update
		// 23:41: "on this platform... or to... violate multiple... protection
		// orders"; 9:22: "against him... for her."). Only a fragment between two
		// sentences is one: the sentence before it has ended, and it ends itself
		// or what follows starts a new one (or he stops for 2 s).
		if allFiller(text) {
			if len(runs[j]) == 1 && conjunctions[normWord(text)] && !sentenceDone(prev) && !startsSentence(next) {
				continue // "kids... and... wife": a real "and" between his words
			}
		} else if !sentenceDone(prev) || !(sentenceDone(words[w1]) || startsSentence(next) || next.Start-words[w1].End >= 2) {
			continue
		}
		out = append(out, island{W0: w0, W1: w1, Text: text, At: clock(words[w0].Start)})
	}
	return out
}

var conjunctions = map[string]bool{"and": true, "or": true, "but": true}

// sentenceDone: the word ends a sentence (. ? !) or is a hesitation ("um").
func sentenceDone(w Word) bool {
	return endsSentence(w.Word) || hesitations[normWord(w.Word)]
}

// startsSentence: the word starts a new sentence - a capital letter that is
// not "I" (always capital), or a hesitation.
func startsSentence(w Word) bool {
	n := normWord(w.Word)
	switch n {
	case "i", "i'm", "i've", "i'll", "i'd":
		return false
	}
	r := []rune(strings.TrimLeft(w.Word, `"'(`))
	return hesitations[n] || len(r) > 0 && unicode.IsUpper(r[0])
}

func allFiller(text string) bool {
	ws := normWords(text)
	for _, w := range ws {
		if !fillers[w] && !hesitations[w] {
			return false
		}
	}
	return len(ws) > 0
}

var thinkSchema = jsonSchema("thinking", map[string]any{"type": "object", "properties": map[string]any{
	"thinking": map[string]any{"type": "array", "items": map[string]any{"type": "integer"}}}, "required": []string{"thinking"}})

const thinkBatch = 12

// thinking returns the islands, each marked Cut or not, cached per edit.
func (r *run) thinking(words []Word, keep []bool, fresh bool) []island {
	is := islands(words, keep)
	path := filepath.Join(r.work, "thinking-"+r.tag+".json")
	stampPrompts(path, videoRole, promptThinkingOutLoud)
	var saved []island
	if b, err := os.ReadFile(path); err == nil && !fresh && json.Unmarshal(b, &saved) == nil && sameIslands(saved, is) {
		r.logf("thinking: using the earlier reading (%d short fragments)", len(saved))
		return saved
	}
	var ask []int
	missed := false
	for k := range is {
		if allFiller(is[k].Text) {
			is[k].Cut, is[k].By = true, "only filler words"
		} else {
			ask = append(ask, k)
		}
	}
	if len(ask) > 0 {
		spec := r.coarseModel()
		m, err := spec.open()
		if err != nil {
			r.logf("thinking: %s could not start (%v) - only the filler fragments are cut", spec.name, err)
			missed = true
		} else {
			r.logf("thinking: %s is reading %d short fragments he said on their own...", spec.name, len(ask))
			for a := 0; a < len(ask); a += thinkBatch {
				part := ask[a:min(a+thinkBatch, len(ask))]
				var sb strings.Builder
				for _, k := range part {
					fmt.Fprintf(&sb, "[%d] ...%s >>%s<< %s...\n", k, wordsText(words, max(0, is[k].W0-15), is[k].W0-1), is[k].Text, wordsText(words, is[k].W1+1, min(len(words)-1, is[k].W1+12)))
				}
				user := fill(promptThinkingOutLoud, "{FRAGMENTS}", sb.String())
				raw, err := m.chat(videoRole, user, thinkSchema, 200)
				var ans struct {
					Thinking []int `json:"thinking"`
				}
				if err == nil {
					err = json.Unmarshal([]byte(raw), &ans)
				}
				if err != nil {
					missed = true
					r.logf("  thinking: %s could not answer: %v (%s) - those fragments stay this time", spec.name, err, tail(raw, 80))
					continue
				}
				for _, k := range ans.Thinking {
					if slices.Contains(part, k) {
						is[k].Cut, is[k].By = true, spec.name+" read it as thinking out loud"
					}
				}
			}
		}
	}
	if !missed { // a part the model could not answer is asked again next run
		writeJSON(path, is)
	}
	return is
}

func sameIslands(a, b []island) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].W0 != b[i].W0 || a[i].W1 != b[i].W1 {
			return false
		}
	}
	return true
}

// Prompts Jordan can edit (prompts.go).
var promptThinkingOutLoud = P(dirYouTube, "Thinking out loud", "Jordan pauses while he thinks about what to say next. Each line below is a short fragment (between >> <<) that he said on its own, with a pause before and after it, shown with the words around it.\n\n{FRAGMENTS}"+
	"\nFor each fragment decide: is it part of what he tells his viewers - it finishes the sentence before it, starts the sentence after it, answers a question, or is a word he means? Or is it him thinking out loud while he finds his next line - a word or two he drops, a start he abandons, \"whatever\", a filler before he starts over?\n"+
	"List the ids of the thinking ones only. When unsure, leave it out: it stays in the video. Answer {\"thinking\": []} when none are.")
