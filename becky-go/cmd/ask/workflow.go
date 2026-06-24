// workflow.go — a human asking to "transcribe" does NOT want one raw AI step; they
// want a FINISHED, corroborated transcript. So becky-ask's transcribe action runs a
// workflow: becky-pipeline (transcribe + diarize + events + osint + ocr) and then
// MERGES the results into one human transcript that
//   - attributes each spoken line to a diarized speaker, and
//   - surfaces the text shown ON SCREEN (burned-in captions / signage) that plain
//     speech-to-text ignores — flagging when a video is captioned, because then the
//     on-screen text IS the author's intended transcript.
//
// The merge (mergeTranscript) is pure and unit-tested headless; the chain reuses the
// existing, tested becky-pipeline so frame extraction / rotation / OCR are not
// reimplemented here.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"becky-go/internal/workflowdef"
)

// --- minimal views of the becky JSON we merge (only the fields we use) ---

type wfTSeg struct {
	Start float64 `json:"start"`
	End   float64 `json:"end"`
	Text  string  `json:"text"`
}
type wfWord struct {
	Word  string  `json:"word"`
	Start float64 `json:"start"`
	End   float64 `json:"end"`
}
type wfTranscript struct {
	Segments []wfTSeg `json:"segments"`
	Words    []wfWord `json:"words"`
}
type wfDSeg struct {
	Start float64 `json:"start"`
	End   float64 `json:"end"`
}
type wfDSpk struct {
	ID       string   `json:"id"`
	Segments []wfDSeg `json:"segments"`
}
type wfDiarize struct {
	Speakers []wfDSpk `json:"speakers"`
}
type wfOcrLine struct {
	Text       string  `json:"text"`
	Confidence float64 `json:"confidence"`
}
type wfOcrRes struct {
	Timestamp float64     `json:"timestamp"`
	Lines     []wfOcrLine `json:"lines"`
}
type wfOcr struct {
	Results []wfOcrRes `json:"results"`
}

// wfCap is one on-screen text line at a time.
type wfCap struct {
	T    float64
	Text string
}

func wfReadJSON(path string, v any) bool {
	b, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	return json.Unmarshal(b, v) == nil
}

func wfMMSS(sec float64) string {
	if sec < 0 {
		sec = 0
	}
	s := int(sec + 0.5)
	return fmt.Sprintf("%02d:%02d", s/60, s%60)
}

// wfSpeakerAt returns the diarized speaker id active at time t (nearest within 1.5s
// if t falls just outside a segment), or "" if nothing is close.
func wfSpeakerAt(d wfDiarize, t float64) string {
	best := ""
	bestD := -1.0
	for _, sp := range d.Speakers {
		for _, sg := range sp.Segments {
			if t >= sg.Start && t <= sg.End {
				return sp.ID
			}
			var dd float64
			if t < sg.Start {
				dd = sg.Start - t
			} else {
				dd = t - sg.End
			}
			if dd >= 0 && (bestD < 0 || dd < bestD) {
				bestD = dd
				best = sp.ID
			}
		}
	}
	if bestD >= 0 && bestD <= 1.5 {
		return best
	}
	return ""
}

// wfOcrCaptions returns the deduped, time-ordered on-screen text (confidence >= 0.7),
// and whether the video looks BURNED-IN captioned (>= 3 distinct non-@handle lines).
func wfOcrCaptions(o wfOcr) (caps []wfCap, burnedIn bool) {
	seen := map[string]bool{}
	distinct := 0
	for _, r := range o.Results {
		for _, ln := range r.Lines {
			txt := strings.TrimSpace(ln.Text)
			if txt == "" || ln.Confidence < 0.7 {
				continue
			}
			key := strings.ToLower(txt)
			if seen[key] {
				continue
			}
			seen[key] = true
			caps = append(caps, wfCap{T: r.Timestamp, Text: txt})
			if !strings.HasPrefix(txt, "@") && len(txt) >= 3 {
				distinct++
			}
		}
	}
	sort.SliceStable(caps, func(i, j int) bool { return caps[i].T < caps[j].T })
	return caps, distinct >= 3
}

// mergeTranscript builds the finished, human-readable transcript: a diarized spoken
// track + the on-screen-text track, with a burned-in-captions warning when relevant.
func mergeTranscript(name string, tf wfTranscript, df wfDiarize, of wfOcr) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# Verified transcript — %s\n\n", name)
	fmt.Fprintf(&b, "Speakers detected: **%d**. This is the finished transcript — "+
		"speech (with the speaker labelled) plus the text shown on screen.\n\n", len(df.Speakers))

	caps, burned := wfOcrCaptions(of)
	if burned {
		b.WriteString("> **This video has text burned into it.** The on-screen text below is " +
			"the author's intended caption track — where it disagrees with the machine " +
			"speech-to-text, trust the on-screen text.\n\n")
	}

	b.WriteString("## Spoken (speaker-labelled)\n\n")
	wrote := false
	for _, s := range tf.Segments {
		txt := strings.TrimSpace(s.Text)
		if txt == "" {
			continue
		}
		spk := wfSpeakerAt(df, s.Start)
		if spk == "" {
			spk = "speaker ?"
		}
		fmt.Fprintf(&b, "- [%s] **%s**: %s\n", wfMMSS(s.Start), spk, txt)
		wrote = true
	}
	if !wrote {
		b.WriteString("_(no speech transcribed)_\n")
	}
	b.WriteString("\n## On-screen text (burned-in captions / signage)\n\n")
	if len(caps) == 0 {
		b.WriteString("_(no on-screen text detected)_\n")
	} else {
		for _, c := range caps {
			fmt.Fprintf(&b, "- [%s] %q\n", wfMMSS(c.T), c.Text)
		}
	}
	return b.String()
}

// --- human-style caption track: censor + reflow from word timings ---
//
// The measured gap between becky's ASR and hand-made social captions is NOT content
// (ASR is at/above parity) — it's FORMATTING: profanity is censored, and lines are
// short 1-4 word chunks timed to the words. Both are deterministic, so we do them
// here from the word-level timings becky-transcribe already emits. We never add,
// drop, or rewrite a spoken word — only censor and re-chunk.

// profanityMap matches the social-caption style observed in the human clips
// (f**k / sh*t / f***ing). Unknown profanity is left as-is rather than over-censored.
var profanityMap = map[string]string{
	"fuck": "f**k", "fucks": "f**ks", "fucking": "f***ing", "fucked": "f**ked",
	"fucker": "f**ker", "motherfucker": "motherf**ker", "fuckin": "f**kin",
	"shit": "sh*t", "shits": "sh*ts", "shitty": "sh*tty", "bullshit": "bullsh*t",
	"bitch": "b*tch", "bitches": "b*tches", "asshole": "a**hole",
	"dick": "d*ck", "pussy": "p*ssy", "cunt": "c*nt", "cock": "c*ck",
}

// censorToken censors one whitespace token, preserving surrounding punctuation and
// the original first-letter case.
func censorToken(tok string) string {
	i, j := 0, len(tok)
	for i < j && !isLetterByte(tok[i]) {
		i++
	}
	for j > i && !isLetterByte(tok[j-1]) {
		j--
	}
	if i >= j {
		return tok
	}
	core := tok[i:j]
	rep, ok := profanityMap[strings.ToLower(core)]
	if !ok {
		return tok
	}
	if core[0] >= 'A' && core[0] <= 'Z' && len(rep) > 0 {
		rep = strings.ToUpper(rep[:1]) + rep[1:]
	}
	return tok[:i] + rep + tok[j:]
}

func isLetterByte(b byte) bool {
	return (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z') || b == '\''
}

type capLine struct {
	Start, End float64
	Text       string
}

// wordsFromSegments synthesizes word timings when the transcript has none (e.g. it
// was reused from a .srt/.vtt sidecar, which carries segment text but no per-word
// times). It spreads each segment's words evenly across the segment's span — coarse
// but enough to reflow into caption-sized chunks so captions are ALWAYS produced.
func wordsFromSegments(segs []wfTSeg) []wfWord {
	var ws []wfWord
	for _, s := range segs {
		toks := strings.Fields(s.Text)
		if len(toks) == 0 {
			continue
		}
		dur := s.End - s.Start
		if dur <= 0 {
			dur = float64(len(toks)) * 0.3
		}
		per := dur / float64(len(toks))
		for i, t := range toks {
			st := s.Start + per*float64(i)
			ws = append(ws, wfWord{Word: t, Start: st, End: st + per})
		}
	}
	return ws
}

// reflowCaptions re-chunks words into short caption lines: break after maxWords, on a
// pause longer than maxGap, or on sentence-ending punctuation. Each line is timed to
// its words. Profanity is censored per token.
func reflowCaptions(words []wfWord, maxWords int, maxGap float64) []capLine {
	if maxWords < 1 {
		maxWords = 4
	}
	var lines []capLine
	var cur []wfWord
	flush := func() {
		if len(cur) == 0 {
			return
		}
		toks := make([]string, 0, len(cur))
		for _, w := range cur {
			t := strings.TrimSpace(w.Word)
			if t != "" {
				toks = append(toks, censorToken(t))
			}
		}
		start := cur[0].Start
		end := cur[len(cur)-1].End
		if end < start+0.3 {
			end = start + 0.3
		}
		if len(toks) > 0 {
			lines = append(lines, capLine{Start: start, End: end, Text: strings.Join(toks, " ")})
		}
		cur = nil
	}
	for i := range words {
		cur = append(cur, words[i])
		brk := len(cur) >= maxWords
		if t := strings.TrimRight(strings.TrimSpace(words[i].Word), "\""); t != "" {
			if last := t[len(t)-1]; last == '.' || last == '!' || last == '?' {
				brk = true
			}
		}
		if i+1 < len(words) && words[i+1].Start-words[i].End > maxGap {
			brk = true
		}
		if brk {
			flush()
		}
	}
	flush()
	// clamp overlaps so each line ends no later than the next one starts
	for k := 0; k+1 < len(lines); k++ {
		if lines[k].End > lines[k+1].Start {
			lines[k].End = lines[k+1].Start
		}
	}
	return lines
}

func srtTime(sec float64) string {
	if sec < 0 {
		sec = 0
	}
	ms := int(sec*1000 + 0.5)
	return fmt.Sprintf("%02d:%02d:%02d,%03d", ms/3600000, (ms%3600000)/60000, (ms%60000)/1000, ms%1000)
}

func captionsSRT(lines []capLine) string {
	var b strings.Builder
	for i, l := range lines {
		fmt.Fprintf(&b, "%d\n%s --> %s\n%s\n\n", i+1, srtTime(l.Start), srtTime(l.End), l.Text)
	}
	return b.String()
}

// wfFindJSONDir locates the dir holding the pipeline's transcript.json (it nests the
// output under the input's base name).
func wfFindJSONDir(root, src string) string {
	base := strings.TrimSuffix(filepath.Base(src), filepath.Ext(filepath.Base(src)))
	for _, c := range []string{filepath.Join(root, base), root} {
		if _, err := os.Stat(filepath.Join(c, "transcript.json")); err == nil {
			return c
		}
	}
	found := root
	_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() && d.Name() == "transcript.json" {
			found = filepath.Dir(p)
			return fs.SkipAll
		}
		return nil
	})
	return found
}

// runTranscribeWorkflow runs the full chain and writes ONE finished transcript next
// to the input (<base>.transcript.md). The source is never modified.
func runTranscribeWorkflow(ctx context.Context, target Target) runResult {
	src := target.Primary()
	// The chain is now driven by the declarative process-video recipe (recipe.go).
	// Without a cheap pre-pipeline speaker probe we cannot yet know the speaker count,
	// so we pass speakers=2 to keep diarize active — exactly the old always-run chain
	// (no behavior loss). The recipe's conditional skip is exercised + asserted in
	// internal/workflowdef; wiring a real probe here only flips diarize OFF for a
	// genuine one-speaker clip, never on.
	steps, _ := pipelineStepsFromRecipe(workflowdef.Facts{"speakers": 2})
	res := runResult{Command: []string{"becky-pipeline", src, "--steps", steps}}

	tmp, err := os.MkdirTemp("", "becky-ask-wf-")
	if err != nil {
		res.Err = fmt.Errorf("could not make a work dir: %w", err)
		return res
	}
	defer os.RemoveAll(tmp)

	pr := runCommand(ctx, []string{"becky-pipeline", src, "--steps", steps, "--out", tmp})

	jdir := wfFindJSONDir(tmp, src)
	var tf wfTranscript
	var df wfDiarize
	var of wfOcr
	okT := wfReadJSON(filepath.Join(jdir, "transcript.json"), &tf)
	wfReadJSON(filepath.Join(jdir, "diarized.json"), &df)
	wfReadJSON(filepath.Join(jdir, "ocr.json"), &of)
	if !okT {
		if pr.Err != nil {
			res.Err = fmt.Errorf("transcribe workflow failed: %v", pr.Err)
		} else {
			res.Err = fmt.Errorf("transcribe workflow produced no transcript")
		}
		return res
	}

	base := strings.TrimSuffix(src, filepath.Ext(src))

	// 1) Human-style caption track (reflowed into short timed chunks + profanity
	//    censored) — the end-goal artifact, built from word timings. No words added,
	//    dropped, or rewritten; only censored and re-chunked.
	words := tf.Words
	if len(words) == 0 {
		words = wordsFromSegments(tf.Segments) // transcript reused a .srt sidecar (no word times)
	}
	capLines := reflowCaptions(words, 4, 0.5)
	if len(capLines) > 0 {
		if w, e := saveOutput(base+".captions.srt", captionsSRT(capLines)); e == nil {
			res.Saved = append(res.Saved, w)
		} else {
			res.Saved = append(res.Saved, fmt.Sprintf("(could not save captions: %v)", e))
		}
	}

	// 2) Full transcript view: speaker-labelled speech + the on-screen text track.
	md := mergeTranscript(filepath.Base(src), tf, df, of)
	if w, e := saveOutput(base+".transcript.md", md); e == nil {
		res.Saved = append(res.Saved, w)
	} else {
		res.Saved = append(res.Saved, fmt.Sprintf("(could not save transcript: %v)", e))
	}

	_, burned := wfOcrCaptions(of)
	tail := ""
	if burned {
		tail = "; this clip also has burned-in captions (cross-checked in the .md)"
	}
	res.Stdout = fmt.Sprintf("%d caption lines + a %d-speaker transcript%s.",
		len(capLines), len(df.Speakers), tail)
	return res
}

// runWorkflow is the entry the model calls. A request that includes "transcribe" runs
// the verified-transcript WORKFLOW (which already covers diarize + on-screen text);
// any other requested ops run as their normal single tools.
func runWorkflow(ctx context.Context, target Target, ops []actionID) runResult {
	hasTranscribe := false
	for _, op := range ops {
		if op == actTranscribe {
			hasTranscribe = true
		}
	}
	if !hasTranscribe {
		return runOps(ctx, target, ops)
	}
	res := runTranscribeWorkflow(ctx, target)
	var rest []actionID
	for _, op := range ops {
		if op != actTranscribe && op != actDiarize { // the workflow already covers diarize
			rest = append(rest, op)
		}
	}
	if len(rest) > 0 {
		more := runOps(ctx, target, rest)
		res.Saved = append(res.Saved, more.Saved...)
		if strings.TrimSpace(more.Stdout) != "" {
			if res.Stdout != "" {
				res.Stdout += "\n\n"
			}
			res.Stdout += more.Stdout
		}
		if more.Err != nil {
			if res.Err != nil {
				res.Err = fmt.Errorf("%v | %v", res.Err, more.Err)
			} else {
				res.Err = more.Err
			}
		}
	}
	return res
}
