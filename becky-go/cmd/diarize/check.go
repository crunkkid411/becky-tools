package main

// check.go - becky-diarize checks Nemotron's speaker split before handing it over
// (2026-10-11). Jordan: "simply running one single diarization model is unacceptable".
// Nemotron decides from the sound in one pass and makes two kinds of mistake that
// matter: one label holding two people (FaO9hHNxP0I: one label, two people) and one
// person split across two labels. Escalation, each step only when the one before is unsure:
//  1. voice prints (ReDimNet2+, ~3 s clips of each label): code settles clear cases;
//  2. LR-ASD (becky-speaking) finds the face whose lips move with each side's voice, and
//     the face model (ArcFace) compares the two faces: code settles clear cases;
//  3. Gemma-4 E4B LOOKS at those faces / frames: same person talking?
//  4. Gemma-4 12B looks when E4B is unsure;
//  5. still unsure: Nemotron's labels stay, and the output says so in plain words.
// (Who-said-which-word at turn changes is a separate check: becky-transcribe runs
// becky-diarfix / DiarizationLM on the words.)

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"becky-go/internal/avlm"
	"becky-go/internal/beckyio"
	"becky-go/internal/config"
	"becky-go/internal/talkingface"
	"becky-go/internal/voiceprint"
)

// Voice bars on ReDimNet2+'s scale for ~3 s clips of real footage, calibrated 2026-10-11 on 5
// of hj-clone's checked videos: clips of ONE person scored 0.72-0.91 against each other
// (even Jordan doing a Johnnie Guilbert impression next to the real one: 0.86); labels of
// DIFFERENT people 0.26-0.41. The sure bars sit outside both ranges, the look covers the gap.
const (
	chunkSec    = 3.0
	minChunkSec = 1.0
	maxChunks   = 30   // per label, evenly spread (bounds the run time)
	mergeSure   = 0.72 // two labels this alike are one person
	mergeUnsure = 0.50 // between the two: look
	splitSure   = 0.35 // two groups inside one label this unlike are two people
	splitUnsure = 0.60 // between the two: look
	minGroup    = 4    // clips each group needs before a label can be split
)

// Check is one thing becky-diarize checked, in the output.
type Check struct {
	Kind     string   `json:"kind"`     // "one-person-two-labels" | "two-people-one-label"
	Speakers []string `json:"speakers"` // labels as Nemotron gave them
	Voice    float64  `json:"voice"`    // cosine of the two sides' voice prints
	Looked   string   `json:"looked,omitempty"`
	Action   string   `json:"action"` // "merged" | "split" | "kept"
	Why      string   `json:"why"`
}

type chunk struct {
	span  voiceprint.Span
	print []float64
	group int // 0/1 inside a label being split
}

// chunksFor cuts one label's speech into up to maxChunks clips of ~3 s, skipping speech
// that overlaps another label (two voices at once pollute a print) (pure).
func chunksFor(spk string, segs []flatSegment) []chunk {
	var out []chunk
	for _, s := range segs {
		if s.Speaker != spk {
			continue
		}
		for t := s.Start; s.End-t >= minChunkSec; t += chunkSec {
			e := t + chunkSec
			if e > s.End || s.End-e < minChunkSec {
				e = s.End
			}
			if e-t > 2*chunkSec {
				e = t + chunkSec
			}
			if !overlapsOther(spk, t, e, segs) {
				out = append(out, chunk{span: voiceprint.Span{Start: t, End: e}})
			}
			if e == s.End {
				break
			}
		}
	}
	if len(out) <= maxChunks {
		return out
	}
	picked := make([]chunk, maxChunks)
	for i := range picked {
		picked[i] = out[i*len(out)/maxChunks]
	}
	return picked
}

func overlapsOther(spk string, a, b float64, segs []flatSegment) bool {
	for _, s := range segs {
		if s.Speaker != spk && s.Start < b && s.End > a {
			return true
		}
	}
	return false
}

func prints(cs []chunk) [][]float64 {
	var p [][]float64
	for _, c := range cs {
		if c.print != nil {
			p = append(p, c.print)
		}
	}
	return p
}

// twoGroups splits a label's clips into the two most different voices (2-means, seeded
// by the two least alike clips; deterministic). Returns the cosine of the two groups'
// voices and false when a group is too small to mean anything (pure).
func twoGroups(cs []chunk) (float64, bool) {
	var idx []int
	for i, c := range cs {
		if c.print != nil {
			idx = append(idx, i)
		}
	}
	if len(idx) < 2*minGroup {
		return 0, false
	}
	a, b, low := idx[0], idx[1], 2.0
	for i := range idx {
		for j := i + 1; j < len(idx); j++ {
			if c := voiceprint.Cosine(cs[idx[i]].print, cs[idx[j]].print); c < low {
				a, b, low = idx[i], idx[j], c
			}
		}
	}
	ca, cb := cs[a].print, cs[b].print
	for round := 0; round < 10; round++ {
		var ga, gb [][]float64
		for _, i := range idx {
			if voiceprint.Cosine(cs[i].print, ca) >= voiceprint.Cosine(cs[i].print, cb) {
				cs[i].group = 0
				ga = append(ga, cs[i].print)
			} else {
				cs[i].group = 1
				gb = append(gb, cs[i].print)
			}
		}
		if len(ga) == 0 || len(gb) == 0 {
			return 0, false
		}
		ca, cb = voiceprint.Mean(ga), voiceprint.Mean(gb)
	}
	n0, n1 := 0, 0
	for _, i := range idx {
		if cs[i].group == 0 {
			n0++
		} else {
			n1++
		}
	}
	small := n0
	if n1 < small {
		small = n1
	}
	if small < minGroup || float64(small) < 0.2*float64(len(idx)) {
		return 0, false
	}
	return voiceprint.Cosine(ca, cb), true
}

// looker asks a vision model whether the person talking in two sets of moments is the
// same person: "yes" | "no" | "unsure", plus which model answered (swapped in tests).
var looker = lookAtTalkers

// checkSpeakers runs the escalation over Nemotron's segments and returns the corrected
// segments and what was checked. Degrades: anything missing leaves segs as they were.
func checkSpeakers(cfg config.Config, input, pics string, hasVideo bool, segs []flatSegment, verbose bool) ([]flatSegment, []Check, string) {
	if why := voiceprint.Ready(cfg); why != "" {
		return segs, nil, "speaker check skipped: " + why
	}
	labels := speakerOrder(segs)
	chunks := map[string][]chunk{}
	var all []voiceprint.Span
	for _, l := range labels {
		chunks[l] = chunksFor(l, segs)
		for _, c := range chunks[l] {
			all = append(all, c.span)
		}
	}
	if len(all) == 0 {
		return segs, nil, ""
	}
	pcm, err := voiceprint.Decode(cfg.FFmpeg, input)
	if err != nil {
		return segs, nil, "speaker check skipped: " + err.Error()
	}
	beckyio.Logf(verbose, "speaker check: voice prints for %d clips...", len(all))
	ps, err := voiceprint.Prints(cfg, pcm, all, verbose)
	if err != nil {
		return segs, nil, "speaker check skipped: " + err.Error()
	}
	k := 0
	for _, l := range labels {
		for i := range chunks[l] {
			chunks[l][i].print = ps[k]
			k++
		}
	}
	return decide(segs, labels, chunks, func(a, b []voiceprint.Span) (string, string) {
		if !hasVideo {
			return "unsure", ""
		}
		return looker(cfg, pics, a, b, verbose)
	})
}

// decide applies the voice bars and, between them, the look (pure but for look).
func decide(segs []flatSegment, labels []string, chunks map[string][]chunk, look func(a, b []voiceprint.Span) (string, string)) ([]flatSegment, []Check, string) {
	var checks []Check
	talk := map[string]float64{}
	for _, s := range segs {
		talk[s.Speaker] += s.End - s.Start
	}

	// 1. two people in one label
	for _, l := range labels {
		cs := chunks[l]
		d, ok := twoGroups(cs)
		debugf("label %s: %d clips, two-group voice %.3f ok=%v", l, len(cs), d, ok)
		if !ok || d >= splitUnsure {
			continue
		}
		c := Check{Kind: "two-people-one-label", Speakers: []string{l}, Voice: round3(d), Action: "kept"}
		split := d < splitSure
		if split {
			c.Why = fmt.Sprintf("the label holds two clearly different voices (%.2f)", d)
		} else {
			ans, who := look(groupSpans(cs, 0), groupSpans(cs, 1))
			c.Looked = lookNote(ans, who)
			split = ans == "no"
			c.Why = fmt.Sprintf("the label may hold two voices (%.2f); the picture says: %s", d, ans)
		}
		if split {
			c.Action = "split"
			segs = splitLabel(segs, l, cs)
		}
		checks = append(checks, c)
	}

	// 2. one person in two labels (on the labels as they are now)
	labels = speakerOrder(segs)
	means := map[string][]float64{}
	spans := map[string][]voiceprint.Span{}
	for _, l := range labels {
		var ps [][]float64
		for _, cs := range chunks {
			for _, c := range cs {
				if c.print != nil && labelAt(segs, c.span) == l {
					ps = append(ps, c.print)
					spans[l] = append(spans[l], c.span)
				}
			}
		}
		if len(ps) >= 2 {
			means[l] = voiceprint.Mean(ps)
		}
	}
	merged := map[string]string{}
	for i, a := range labels {
		for _, b := range labels[i+1:] {
			if means[a] == nil || means[b] == nil || merged[a] != "" || merged[b] != "" {
				continue
			}
			s := voiceprint.Cosine(means[a], means[b])
			debugf("labels %s+%s: voice %.3f", a, b, s)
			if s < mergeUnsure {
				continue
			}
			c := Check{Kind: "one-person-two-labels", Speakers: []string{a, b}, Voice: round3(s), Action: "kept"}
			same := s >= mergeSure
			if same {
				c.Why = fmt.Sprintf("the two labels are clearly one voice (%.2f)", s)
			} else {
				ans, who := look(spans[a], spans[b])
				c.Looked = lookNote(ans, who)
				same = ans == "yes"
				c.Why = fmt.Sprintf("the two labels may be one voice (%.2f); the picture says: %s", s, ans)
			}
			if same {
				c.Action = "merged"
				keep, drop := a, b
				if talk[b] > talk[a] {
					keep, drop = b, a
				}
				merged[drop] = keep
			}
			checks = append(checks, c)
		}
	}
	for i := range segs {
		if to, ok := merged[segs[i].Speaker]; ok {
			segs[i].Speaker = to
		}
	}
	return segs, checks, summary(checks)
}

func lookNote(ans, who string) string {
	if who == "" {
		return ans + " (no picture to look at)"
	}
	return ans + " (" + who + ")"
}

func summary(checks []Check) string {
	if len(checks) == 0 {
		return "speaker check: every label sounds like one person, and no two labels sound alike"
	}
	var parts []string
	for _, c := range checks {
		parts = append(parts, fmt.Sprintf("%s %s: %s", strings.Join(c.Speakers, "+"), c.Action, c.Why))
	}
	return "speaker check: " + strings.Join(parts, "; ")
}

func groupSpans(cs []chunk, g int) []voiceprint.Span {
	var out []voiceprint.Span
	for _, c := range cs {
		if c.print != nil && c.group == g {
			out = append(out, c.span)
		}
	}
	return out
}

// splitLabel gives the smaller voice group of label l its own new label. A segment goes
// with the group most of its clips belong to; a segment with no clip keeps l (pure).
func splitLabel(segs []flatSegment, l string, cs []chunk) []flatSegment {
	n := [2]int{}
	for _, c := range cs {
		if c.print != nil {
			n[c.group]++
		}
	}
	minor := 1
	if n[0] < n[1] {
		minor = 0
	}
	newID := fmt.Sprintf("SPEAKER_%02d", len(speakerOrder(segs)))
	for len(segOf(segs, newID)) > 0 {
		newID += "b"
	}
	out := append([]flatSegment(nil), segs...)
	for i, s := range out {
		if s.Speaker != l {
			continue
		}
		votes := [2]int{}
		for _, c := range cs {
			if c.print != nil && c.span.Start >= s.Start-0.01 && c.span.End <= s.End+0.01 {
				votes[c.group]++
			}
		}
		if votes[minor] > votes[1-minor] {
			out[i].Speaker = newID
		}
	}
	return out
}

func segOf(segs []flatSegment, id string) []flatSegment {
	var out []flatSegment
	for _, s := range segs {
		if s.Speaker == id {
			out = append(out, s)
		}
	}
	return out
}

func labelAt(segs []flatSegment, sp voiceprint.Span) string {
	for _, s := range segs {
		if sp.Start >= s.Start-0.01 && sp.End <= s.End+0.01 {
			return s.Speaker
		}
	}
	return ""
}

func speakerOrder(segs []flatSegment) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range segs {
		if !seen[s.Speaker] {
			seen[s.Speaker] = true
			out = append(out, s.Speaker)
		}
	}
	sort.Strings(out)
	return out
}

// lookAtTalkers compares the faces talking on each side (LR-ASD + ArcFace) and, when that
// is unclear or no face talks, asks Gemma-4 whether the same person is talking. E4B
// first; the 12B only when E4B is unsure.
func lookAtTalkers(cfg config.Config, input string, a, b []voiceprint.Span, verbose bool) (string, string) {
	dir, err := os.MkdirTemp("", "becky_diarlook_")
	if err != nil {
		return "unsure", ""
	}
	defer os.RemoveAll(dir)
	fa := frames(cfg.FFmpeg, input, dir, "a", a)
	fb := frames(cfg.FFmpeg, input, dir, "b", b)
	if len(fa) == 0 || len(fb) == 0 {
		return "unsure", ""
	}
	// both sides have a face whose lips moved with the sound (LR-ASD): the face model
	// compares those two faces first; Gemma is only asked when that is unclear
	if isFace(fa[0]) && isFace(fb[0]) {
		ans, sim := talkingface.Compare(cfg, fa[:1], fb[:1])
		beckyio.Logf(verbose, "speaker check: the two talking faces match %.2f -> %s", sim, ans)
		if ans != "unsure" {
			return ans, fmt.Sprintf("LR-ASD + face match %.2f", sim)
		}
	}
	prompt := fmt.Sprintf(`The first %d picture(s) were taken while VOICE A was talking (a picture of just one face is a close-up of the face whose lips moved with the sound). The last %d picture(s) were taken while VOICE B was talking. In each picture, find the person who is talking (mouth open mid-word, facing the camera or the listener, or the only person shown).
Is the person talking in the VOICE A pictures the SAME person as the one talking in the VOICE B pictures? Compare face, hair, clothes and build. If you cannot tell who is talking, answer "unsure".
Reply with JSON only: {"same": "yes" | "no" | "unsure", "why": "<short>"}`, len(fa), len(fb))
	imgs := append(fa, fb...)
	type m struct{ model, mmproj, name string }
	for _, g := range []m{{cfg.GemmaModel, cfg.GemmaMMProj, "Gemma-4 E4B"}, {cfg.GemmaModel12B, cfg.GemmaMMProj12B, "Gemma-4 12B"}} {
		if g.model == "" || !fileExists(g.model) {
			continue
		}
		beckyio.Logf(verbose, "speaker check: asking %s to look at %d pictures...", g.name, len(imgs))
		r := avlm.New(g.model, g.mmproj, cfg.LlamaServer, "", cfg.FFmpeg, cfg.FFprobe, nil)
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
		res, err := r.AnalyzeImages(ctx, imgs, avlm.ImageOptions{Prompt: prompt, MaxTokens: 200, Temperature: 0.01})
		cancel()
		if err != nil {
			beckyio.Logf(verbose, "speaker check: %s could not look: %v", g.name, err)
			continue
		}
		ans := parseSame(res.Text)
		beckyio.Logf(verbose, "speaker check: %s says %s (%s)", g.name, ans, oneLine(res.Text))
		if ans != "unsure" {
			return ans, g.name
		}
	}
	return "unsure", "Gemma-4 E4B and 12B"
}

func isFace(p string) bool { return strings.HasSuffix(filepath.Base(p), "face.jpg") }

// frames returns, for one side, a close-up of the face that is TALKING during its longest
// stretch (LR-ASD via becky-speaking) plus the whole frame; or, when no face clearly talks,
// up to 2 whole frames from the middle of the longest stretches.
func frames(ffmpeg, input, dir, tag string, spans []voiceprint.Span) []string {
	s := append([]voiceprint.Span(nil), spans...)
	sort.SliceStable(s, func(i, j int) bool { return s[i].End-s[i].Start > s[j].End-s[j].Start })
	var out []string
	if len(s) > 0 {
		if crop, _ := talkingface.Crop(ffmpeg, input, s[0].Start, s[0].End); crop != "" {
			dst := filepath.Join(dir, tag+"face.jpg")
			if os.Rename(crop, dst) == nil {
				out = append(out, dst)
			} else {
				os.Remove(crop)
			}
		}
	}
	for i := 0; i < len(s) && len(out) < 2; i++ {
		p := filepath.Join(dir, fmt.Sprintf("%s%d.jpg", tag, i))
		t := (s[i].Start + s[i].End) / 2
		cmd := exec.Command(ffmpeg, "-ss", fmt.Sprintf("%.2f", t), "-i", input, "-frames:v", "1",
			"-vf", "scale=640:-2", "-q:v", "3", "-y", "-loglevel", "error", p)
		if cmd.Run() == nil && fileExists(p) {
			out = append(out, p)
		}
	}
	return out
}

func parseSame(raw string) string {
	i, j := strings.Index(raw, "{"), strings.LastIndex(raw, "}")
	if i < 0 || j <= i {
		return "unsure"
	}
	var v struct {
		Same string `json:"same"`
	}
	if json.Unmarshal([]byte(raw[i:j+1]), &v) != nil {
		return "unsure"
	}
	switch s := strings.ToLower(strings.TrimSpace(v.Same)); s {
	case "yes", "no":
		return s
	}
	return "unsure"
}

func oneLine(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) > 160 {
		s = s[:160]
	}
	return s
}

// debugf prints calibration numbers when BECKY_DIAR_DEBUG is set.
func debugf(f string, a ...any) {
	if os.Getenv("BECKY_DIAR_DEBUG") != "" {
		fmt.Fprintln(os.Stderr, "debug: "+fmt.Sprintf(f, a...))
	}
}
