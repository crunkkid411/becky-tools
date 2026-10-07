package main

// moments.go - put back what becky-cut and the content edges cut out of a
// visual moment. Jordan, 2026-10-07: "the overall context of the cuts are still
// not understood... 'but not all of them' EXAGGERATED BODILY MOVEMENT to
// demonstrate defeat... 'I'm still allowed to livestream' GIVES THUMBS UP...
// 'Hair Jordan, hi' - during that silence I raised both my hands in an awkward
// hand wave, to match the 'hi' greeting. That could have been left in because it
// made sense." And: "that 'hi' followed by a hand wave should have been enough
// motion to trigger a bunch of small vision models to determine what is going
// on specifically, and then have gemma or qwen decide to leave it or cut it."
//
// So, for every stretch the edit cuts next to his words - a becky-cut pause
// inside a kept section, the 2.5 s after a section, the 2 s before one:
//  1. the small models measure every 0.1 s frame (picture_signals.py):
//     MediaPipe pose motion, gestures (thumbs up, open palm), face mesh lost
//     while the body is still there (head down / turned away), expression
//     scores; insightface + MediaPipe for the held-open mouth;
//  2. a stretch where they see something (2+ active frames, or any gesture) is
//     WATCHED by Gemma-4 12B - frames and audio - told the words around it, and
//     asked to label what he does (a reaction, a gesture, acting out the line - or
//     fixing his hair, reading chat, reaching for something);
//  3. Gemma names WHAT he does (one label, momentPrompt); a reaction, gesture
//     or acting-out label AND a small-model signal (two agreeing) puts back the
//     part Gemma names, on the frame grid. A marker says what and why.
// Calibrated on the 27-livestream (stream times): the wave 14:56.2-14:56.8
// (motion 0.46-0.53 shoulder widths a frame, Open_Palm 0.57) inside becky-cut's
// 14:56.27-14:56.9; the thumbs up 3:22.2-3:23.0 (Thumb_Up 0.51-0.72) after the
// section ended at 3:22.27; the head drop 3:17.7-3:18.5 (face mesh lost, body
// tracked) half cut at 3:18.03. It only ever puts picture back; it never cuts,
// and never pulls in a word the content decision left out.

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"becky-go/internal/avlm"
	"becky-go/internal/config"
)

const (
	// ponytail: calibration knobs (27-livestream, see the header).
	momentAfter     = 2.5  // seconds after a kept section that are watched
	momentBefore    = 2.0  // seconds before one
	momentMotion    = 0.30 // a nose/wrist/elbow moving this far in 0.1 s, in shoulder widths (talking: under 0.2)
	momentGesture   = 0.5  // MediaPipe gesture score that counts
	momentActive    = 2    // active frames that make a stretch worth watching (any gesture is enough)
	momentContext   = 1.5  // seconds of his words before the stretch that Gemma also sees
	momentFPS       = 4.0  // frames a second Gemma watches
	momentMaxFrames = 20   // at most (~256 tokens each), so a long pause lowers the rate
	momentWordGap   = 0.04 // never closer than this to a word that was cut
	momentMinFrame  = 4    // a put-back shorter than this many frames is not worth a cut point
)

// expression scores that count as a big face (MediaPipe; jawOpen is jawOpen).
var momentExpr = map[string]float64{"smile": 0.5, "brow": 0.5, "eyes": 0.4, "pucker": 0.6}

// moment is one watched stretch and what became of it.
type moment struct {
	A       float64  `json:"a"` // the stretch the edit cut
	B       float64  `json:"b"`
	Kind    string   `json:"kind"`
	Signals []string `json:"signals"`
	Said    string   `json:"said"`
	Label   string   `json:"label,omitempty"` // Gemma's labels, in order (momentPrompt)
	Gemma   string   `json:"gemma,omitempty"` // what Gemma says he does
	Keep    bool     `json:"keep"`
	Back    []span   `json:"back,omitempty"` // what was put back
}

// momentCandidates are the stretches the edit cuts right next to his words. A
// stretch before or after a section stops short of the nearest word the
// content decision left out.
func momentCandidates(ranges []Range, pieces []span, words []Word) []moment {
	var out []moment
	add := func(kind string, a, b float64) {
		if b-a > 0.05 {
			out = append(out, moment{A: a, B: b, Kind: kind})
		}
	}
	for i, r := range ranges {
		lo := math.Max(r.In-momentBefore, 0)
		if i > 0 {
			lo = math.Max(lo, ranges[i-1].Out)
		}
		if r.W0 > 0 {
			lo = math.Max(lo, words[r.W0-1].End+momentWordGap)
		}
		add("before", lo, r.In)
		var in []span
		for _, p := range pieces {
			if p.A >= r.In-1e-6 && p.B <= r.Out+1e-6 {
				in = append(in, p)
			}
		}
		for j := 1; j < len(in); j++ {
			add("pause", in[j-1].B, in[j].A)
		}
		hi := r.Out + momentAfter
		if i+1 < len(ranges) {
			hi = math.Min(hi, ranges[i+1].In)
		}
		if r.W1+1 < len(words) {
			hi = math.Min(hi, words[r.W1+1].Start-momentWordGap)
		}
		add("after", r.Out, hi)
	}
	return out
}

// momentSignals lists what the small models see inside [a, b] (empty: nothing).
func momentSignals(p *pictures, a, b float64) []string {
	count := map[string]int{}
	var order []string
	hit := func(name string) {
		if count[name] == 0 {
			order = append(order, name)
		}
		count[name]++
	}
	for k := int(math.Ceil(a*picFPS - 1e-6)); float64(k) <= b*picFPS+1e-6; k++ {
		f, ok := p.frames[k]
		if !ok {
			continue
		}
		if prev, ok := p.frames[k-1]; ok && bodyMotion(f, prev) >= momentMotion {
			hit("fast movement (MediaPipe pose)")
		}
		for _, g := range f.Gest {
			if len(g) == 2 {
				name, _ := g[0].(string)
				if sc, _ := g[1].(float64); sc >= momentGesture {
					hit(strings.ReplaceAll(name, "_", " ") + " (MediaPipe gesture)")
				}
			}
		}
		if len(f.Head) == 0 && len(f.Body["n"]) == 3 && f.Body["n"][2] >= visible {
			hit("face turned away or down (MediaPipe face mesh lost him, pose still has him)")
		}
		big := f.Jaw >= jawOpen
		for name, min := range momentExpr {
			big = big || f.Expr[name] >= min
		}
		if big {
			hit("big facial expression (MediaPipe)")
		}
	}
	if in, mp := p.look(a, b).faceHeld(); in && mp {
		hit("mouth held open (insightface and MediaPipe)")
	}
	total, gesture := 0, false
	for _, n := range order {
		total += count[n]
		gesture = gesture || strings.Contains(n, "gesture")
	}
	if total < momentActive && !gesture {
		return nil
	}
	out := make([]string, len(order))
	for i, n := range order {
		out[i] = fmt.Sprintf("%s x%d", n, count[n])
	}
	return out
}

// bodyMotion: the farthest a nose, wrist or elbow moved since the last frame,
// in shoulder widths.
func bodyMotion(f, prev picFrame) float64 {
	ls, rs := f.Body["ls"], f.Body["rs"]
	if len(ls) != 3 || len(rs) != 3 {
		return 0
	}
	w := math.Abs(ls[0] - rs[0])
	if w <= 0 {
		return 0
	}
	m := 0.0
	for _, k := range []string{"n", "lw", "rw", "le", "re"} {
		a, b := f.Body[k], prev.Body[k]
		if len(a) == 3 && len(b) == 3 && a[2] >= visible && b[2] >= visible {
			m = math.Max(m, math.Hypot(a[0]-b[0], a[1]-b[1])/w)
		}
	}
	return m
}

// wordsAround: what he says just before t0 (up to 14 words, from the kept
// section) and just after t1 (up to 8 words), as Gemma is told it.
func wordsAround(words []Word, t0, t1 float64) (before, after string) {
	var b, a []string
	for i := len(words) - 1; i >= 0 && len(b) < 14; i-- {
		if words[i].End <= t0+0.3 && words[i].End > t0-12 {
			b = append([]string{words[i].Word}, b...)
		}
	}
	for _, w := range words {
		if w.Start >= t1-0.1 && w.Start < t1+8 && len(a) < 8 {
			a = append(a, w.Word)
		}
	}
	return strings.Join(b, " "), strings.Join(a, " ")
}

// momentPrompt asks Gemma only WHAT he does (a label from a fixed list for each
// separate action) and when; the keep/cut rule is applied in Go (momentKeeps).
// One label per stretch lost the thumbs up after "still allowed to livestream":
// he reaches for his water right after it, and "object" won the stretch. A
// label alone put back his hair fixing before "yes, update" (12B called hands
// going to his hair "raising both hands near his head" = gesture), so each
// action also says whether it goes with his words, and both must hold. Asked "keep or cut?"
// directly, Gemma-4 E4B saw both of the 27-livestream's big faces ("he is not
// talking and has a big facial expression") and cut both: it treats silence as
// a reason to cut. The same split fixed the content calls (4B models: ask the
// parts, code applies the rule - 33% -> 96%).
const momentPrompt = `This is Jordan, a livestreamer, talking to his camera. Look at the moment between %.1f s and %.1f s (the timestamps on the frames). He does not speak in it.
Just before it he says: "%s"
%s
Small vision models noticed: %s.
First, for each frame write its timestamp, where his head points (up, level, down), his facial expression and what his hands do, a few words each.
Then split the moment into the separate things he does, in order (often just one; for example a thumbs up and THEN reaching for a drink are two), and give each ONE label:
- "reaction": a facial expression that reacts to or punctuates what he said (an excited, shocked, sad, smug or funny face)
- "gesture": a gesture to the camera (thumbs up, wave, pointing, hands raised, shrug)
- "acting": a body movement that acts out what he said (dropping his head in defeat, slumping, throwing his hands up)
- "grooming": fixing or touching his hair, face or clothes
- "looking away": reading chat, looking at a phone or screen, looking off to the side
- "object": reaching for, holding or drinking something
- "still": sitting still, nothing in particular
For each action also say whether it goes with what he says just before or right after it (true), or has nothing to do with his words (false).
End with one line of JSON: {"actions": [{"label": "one of the labels", "what": "what he does, at most 12 words", "fits_line": true or false, "from": first second of it as shown on the frames, "to": last second}, ...]}`

// momentKeeps: the labels that go with his line. Everything else stays cut.
var momentKeeps = map[string]bool{"reaction": true, "gesture": true, "acting": true}

// momentRunner is Gemma-4 12B, always on the processor (it does not fit the
// 8 GB card). Measured on the 27-livestream's moments: E4B called the head drop,
// the excited face and the wave "head level, neutral expression" frame after
// frame (it sees the picture - "bright green hair, black t-shirt" - but not
// these details); 12B called them "head down" 3:17.5-3:18.5, "wide-eyed
// surprise, mouth open", "hands raised and open" - in the same time on the
// processor (about 1.5 minutes for 8 frames). E4B is the fallback when the 12B
// files are missing; the note says so.
func momentRunner(cfg config.Config) (*avlm.Runner, string) {
	m, mp, note := cfg.GemmaModel12B, cfg.GemmaMMProj12B, ""
	if _, err := os.Stat(m); err != nil {
		m, mp, _ = cfg.GemmaAVLM()
		note = "Gemma 12B is missing, so the smaller Gemma watched (it misses head and face details)"
	} else if _, err := os.Stat(mp); err != nil {
		m, mp, _ = cfg.GemmaAVLM()
		note = "Gemma 12B's picture file is missing, so the smaller Gemma watched (it misses head and face details)"
	}
	r := avlm.New(m, mp, cfg.LlamaServer, "", cfg.FFmpeg, cfg.FFprobe, nil)
	r.NGL = 0
	// ponytail: half the default context - a window is at most ~6 s at 4 fps
	// (~24 frames, ~6k tokens); at 16384 the 12B's cache alone was several GB
	// and Claude Code stopped the run twice for low memory (2026-10-07)
	r.CtxSize = 8192
	return r, note
}

// action is one thing he does in a watched stretch, as Gemma labels it.
type action struct {
	Label string  `json:"label"`
	What  string  `json:"what"`
	Fits  bool    `json:"fits_line"` // goes with what he says
	From  seconds `json:"from"`
	To    seconds `json:"to"`
}

// parseActions reads the {"actions": [...]} object at the end of Gemma's answer
// (after its frame-by-frame description); times are moved onto the clip when
// Gemma counted from the window start [w0, w1].
func parseActions(s string, w0, w1 float64) ([]action, error) {
	i := strings.LastIndex(s, `"actions"`)
	if i >= 0 {
		i = strings.LastIndex(s[:i], "{")
	}
	if i < 0 {
		return nil, fmt.Errorf("Gemma's answer had no actions: %.120s", lastLines(s, 2))
	}
	var v struct {
		Actions []action `json:"actions"`
	}
	if err := json.NewDecoder(strings.NewReader(s[i:])).Decode(&v); err != nil {
		return nil, fmt.Errorf("Gemma's actions were not readable: %.120s", lastLines(s, 2))
	}
	for k := range v.Actions {
		a := &v.Actions[k]
		a.Label = strings.ToLower(strings.TrimSpace(a.Label))
		a.What = strings.TrimSpace(a.What)
		if float64(a.To) <= w1-w0+1e-6 && float64(a.From) < w0-1 {
			a.From, a.To = a.From+seconds(w0), a.To+seconds(w0)
		}
	}
	return v.Actions, nil
}

// gemmaMoment asks Gemma about one stretch.
func gemmaMoment(r *avlm.Runner, media string, m moment, before, after string) ([]action, error) {
	w0 := math.Max(m.A-momentContext, 0)
	w1 := m.B + 0.5
	afterLine := "After it the video goes on without words."
	if after != "" {
		afterLine = fmt.Sprintf(`Right after it he says: "%s"`, after)
	}
	prompt := fmt.Sprintf(momentPrompt, m.A, m.B, before, afterLine, strings.Join(m.Signals, "; "))
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()
	fps := math.Min(momentFPS, momentMaxFrames/(w1-w0)) // fits the 8192 context (momentRunner)
	res, err := r.Analyze(ctx, avlm.Options{Clip: media, Prompt: prompt, WindowStart: w0, WindowSec: w1 - w0,
		FPS: fps, MaxTokens: 2000, Temperature: 0.01})
	if err != nil {
		return nil, err
	}
	return parseActions(res.Text, w0, w1)
}

// seconds reads a time Gemma writes as 185.2, "185.2" or "185.2s".
type seconds float64

func (s *seconds) UnmarshalJSON(b []byte) error {
	t := strings.TrimSuffix(strings.Trim(strings.TrimSpace(string(b)), `"`), "s")
	f, err := strconv.ParseFloat(strings.TrimSpace(t), 64)
	if err != nil {
		*s = 0 // not placeable: putBack then keeps the whole stretch
		return nil
	}
	*s = seconds(f)
	return nil
}

// putBack is the part of stretch [a, b] to restore for a gesture Gemma placed
// at [from, to]: padded 0.1 s, inside the stretch, edges on the frame grid
// (outward, then clipped), and the whole stretch when what is left on either
// side would be a sliver of 2 frames or less. ok=false: too short to matter.
func putBack(a, b, from, to, fps float64) (float64, float64, bool) {
	if !(to > from) || to < a || from > b {
		from, to = a, b // Gemma kept it but could not place it: the stretch
	}
	lo := math.Max(a, math.Floor((from-0.1)*fps+1e-6)/fps)
	hi := math.Min(b, math.Ceil((to+0.1)*fps-1e-6)/fps)
	if lo-a <= 2/fps+1e-6 {
		lo = a
	}
	if b-hi <= 2/fps+1e-6 {
		hi = b
	}
	if (hi-lo)*fps < momentMinFrame-1e-6 {
		return 0, 0, false
	}
	return lo, hi, true
}

// addPieces merges put-back stretches into the pieces.
func addPieces(pieces []span, add []span) []span {
	all := append(append([]span{}, pieces...), add...)
	sort.Slice(all, func(i, j int) bool { return all[i].A < all[j].A })
	var out []span
	for _, p := range all {
		if n := len(out); n > 0 && p.A <= out[n-1].B+1e-6 {
			out[n-1].B = math.Max(out[n-1].B, p.B)
			continue
		}
		out = append(out, p)
	}
	return out
}

// judgeActions applies the rule to Gemma's actions: each reaction, gesture or
// acting-out that goes with his words is put back (padded, on the frame grid, inside the stretch); the
// rest stays cut.
func judgeActions(c *moment, acts []action, fps float64) {
	var labels, whats []string
	c.Keep, c.Back = false, nil
	for _, a := range acts {
		labels = append(labels, a.Label)
		whats = append(whats, a.What)
		if !momentKeeps[a.Label] || !a.Fits {
			continue
		}
		from, to := float64(a.From), float64(a.To)
		if !(to > from) && len(acts) > 1 {
			continue // not placed, and the stretch holds other things too: keep none of it
		}
		if lo, hi, ok := putBack(c.A, c.B, from, to, fps); ok {
			c.Back = addPieces(c.Back, []span{{lo, hi}})
			c.Keep = true
		}
	}
	c.Label, c.Gemma = strings.Join(labels, ", "), strings.Join(whats, "; then ")
}

// keepMoments returns the pieces with every visual moment Gemma and the small
// models agree on put back, every stretch watched, and a note when the check
// could not run (the pieces are then left as they were).
func keepMoments(cfg config.Config, media, work, stem string, ranges []Range, pieces []span, words []Word, fps float64, logf func(string, ...any)) ([]span, []moment, string) {
	cands := momentCandidates(ranges, pieces, words)
	if len(cands) == 0 {
		return pieces, nil, ""
	}
	spans := make([]span, len(cands))
	for i, c := range cands {
		spans[i] = span{c.A, c.B}
	}
	logf("visual moments: the small models measure %d stretches the edit cuts next to his words...", len(cands))
	pics, err := loadPictures(cfg, media, picCache(work, stem), spans)
	if err != nil {
		return pieces, nil, "the visual-moment check did not run: " + err.Error()
	}
	var watch []int
	for i := range cands {
		if cands[i].Signals = momentSignals(pics, cands[i].A, cands[i].B); cands[i].Signals != nil {
			watch = append(watch, i)
		}
	}
	logf("  %d of them show something; Gemma watches each one", len(watch))
	cachePath := filepath.Join(work, "moments-cache.json")
	cache := map[string]moment{}
	if b, err := os.ReadFile(cachePath); err == nil {
		_ = json.Unmarshal(b, &cache)
	}
	var runner *avlm.Runner
	var note string
	var add []span
	for _, i := range watch {
		c := &cands[i]
		before, after := wordsAround(words, c.A, c.B)
		c.Said = before
		if c.Kind == "before" {
			c.Said = after
		}
		key := fmt.Sprintf("v5|%.3f-%.3f|%s", c.A, c.B, strings.Join(c.Signals, ";"))
		if hit, ok := cache[key]; ok {
			c.Label, c.Gemma, c.Keep, c.Back = hit.Label, hit.Gemma, hit.Keep, hit.Back
		} else {
			if runner == nil {
				var rnote string
				runner, rnote = momentRunner(cfg)
				if rnote != "" {
					logf("  " + rnote)
				}
				if err := runner.Ready(); err != nil {
					note = "Gemma could not watch: " + err.Error()
					break
				}
				stop, err := runner.Start(context.Background())
				if err != nil {
					note = "Gemma could not watch: " + err.Error()
					break
				}
				defer stop()
			}
			acts, err := gemmaMoment(runner, media, *c, before, after)
			if err != nil {
				logf("  %.1f-%.1f: %v", c.A, c.B, err)
				continue
			}
			judgeActions(c, acts, fps)
			cache[key] = *c
			writeJSON(cachePath, cache)
		}
		verdict := "cut stays"
		if c.Keep {
			var parts []string
			for _, b := range c.Back {
				parts = append(parts, fmt.Sprintf("%.2f-%.2f", b.A, b.B))
			}
			verdict = "PUT BACK " + strings.Join(parts, ", ")
			add = append(add, c.Back...)
		}
		logf("  %s %.2f-%.2f: [%s] %s -> %s", c.Kind, c.A, c.B, c.Label, c.Gemma, verdict)
	}
	logf("  %d put back", len(add))
	return addPieces(pieces, add), cands, note
}

// momentMarks: a marker where each put-back moment starts on the timeline.
func momentMarks(ms []moment, ps []piece) []mark {
	var out []mark
	for _, m := range ms {
		if !m.Keep {
			continue
		}
		if t, _, ok := toTimeline(ps, m.Back[0].A, m.Back[0].A+0.04); ok {
			out = append(out, mark{At: t, Label: fmt.Sprintf("Kept for the picture - %s (Gemma, next to \"%s\"; small models: %s)",
				m.Gemma, lastWords(m.Said, 6), strings.Join(m.Signals, ", "))})
		}
	}
	return out
}

func lastWords(s string, n int) string {
	f := strings.Fields(s)
	if len(f) > n {
		f = f[len(f)-n:]
	}
	return strings.Join(f, " ")
}

// momentLine is the report's line about the visual-moment check.
func momentLine(ms []moment, note string) string {
	if note != "" {
		return "- **Kept for the picture:** " + note
	}
	watched, kept := 0, 0
	for _, m := range ms {
		if m.Signals != nil {
			watched++
		}
		if m.Keep {
			kept++
		}
	}
	return fmt.Sprintf("- **Kept for the picture:** the small vision models measured %d stretches the edit cut next to his words; %d showed something and Gemma watched them; %d put back (a marker on each)", len(ms), watched, kept)
}
