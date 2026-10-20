package main

// coarse.go - --video, rebuilt the openshorts way (2026-10-13). Jordan, on the
// vote-every-sentence edit: "literally it's WORSE than simply removing
// silences". openshorts (X:\AI-2\content_generators\openshorts) gets better
// edits out of weaker models because a model only makes a few coarse calls,
// with whole paragraphs in front of it, and code does every timing and every
// rule. So here:
//
//   - Everything he says stays; becky-cut takes the dead air out (the base edit
//     he trusts for pacing).
//   - Gemma-4 12B reads the transcript in parts and names only the exceptions:
//     sentences not said to his viewers (a mic check, getting ready, after the
//     goodbye) and retakes (an attempt he abandons and says again).
//   - Code checks every retake against the words: the redo must repeat it.
//   - A restart inside a sentence ("I'm gonna try to I'm gonna try not to") is
//     found in the words, with no model.
//   - Only pure hesitation lines ("Um") are filler - never "Yeah." (it was the
//     answer to "Am I grateful it's still here?").
//   - His instructions to the editor are followed as before (instructions.go).
//   - Last, the 12B reads the edit like a viewer, every cut shown in place, and
//     may only put cuts back.

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// coarseModel is the one model that reads the video (the 12B; E4B without it).
func (r *run) coarseModel() localModelSpec {
	if _, err := os.Stat(r.cfg.GemmaModel12B); err == nil {
		return localModelSpec{name: "gemma4-12b", model: r.cfg.GemmaModel12B, server: r.cfg.LlamaServer}
	}
	gm, _, _ := r.cfg.GemmaAVLM()
	return localModelSpec{name: "gemma4", model: gm, server: r.cfg.LlamaServer}
}

var videoRole = P(dirYouTube, "Who the model is", `You are the video editor for Hair Jordan, a YouTuber. You are editing the raw camera recording of one of his YouTube videos into the video he will post. Almost everything he says is to his viewers, and all of that stays in the video.`)

type retake struct {
	Abandoned int `json:"abandoned_id"`
	Redo      int `json:"redo_id"`
}

var exceptSchema = jsonSchema("exceptions", map[string]any{"type": "object", "properties": map[string]any{
	"not_for_viewers": map[string]any{"type": "array", "items": map[string]any{"type": "integer"}},
	"retakes": map[string]any{"type": "array", "items": map[string]any{"type": "object", "properties": map[string]any{
		"abandoned_id": map[string]any{"type": "integer"},
		"redo_id":      map[string]any{"type": "integer"},
	}, "required": []string{"abandoned_id", "redo_id"}}},
}, "required": []string{"not_for_viewers", "retakes"}})

var restoreSchema = jsonSchema("restore", map[string]any{"type": "object", "properties": map[string]any{
	"restore": map[string]any{"type": "array", "items": map[string]any{"type": "integer"}}}, "required": []string{"restore"}})

// coarseCache is what the model said, kept so a rerun asks nothing again.
type coarseCache struct {
	Model     string   `json:"model"`
	Sentences int      `json:"sentences"`
	Off       []int    `json:"not_for_viewers"`
	Retakes   []retake `json:"retakes"`
	Restore   []int    `json:"restore"`
	Restored  bool     `json:"restore_done"`
}

const partSize, partOverlap = 40, 8

// parts are the sentence ranges a model reads at once, overlapping.
func parts(n int) [][2]int {
	var out [][2]int
	for a := 0; a < n; a += partSize - partOverlap {
		b := min(a+partSize, n)
		out = append(out, [2]int{a, b})
		if b == n {
			break
		}
	}
	return out
}

// coarse is the whole content decision for --video.
func (r *run) coarse(ss []Sentence, fresh bool) (Selection, error) {
	spec := r.coarseModel()
	cachePath := filepath.Join(r.work, "coarse-"+r.tag+".json")
	stampPrompts(cachePath, videoRole, promptWhatToRemove, promptPutBackCutsTheViewerNeeds)
	var c coarseCache
	if b, err := os.ReadFile(cachePath); err != nil || fresh || json.Unmarshal(b, &c) != nil || c.Sentences != len(ss) || c.Model != spec.name {
		c = coarseCache{Model: spec.name, Sentences: len(ss)}
		var err error
		if c.Off, c.Retakes, err = exceptions(spec, ss, r.logf); err != nil {
			return Selection{}, err
		}
		writeJSON(cachePath, c)
	} else {
		r.logf("content: using %s's earlier reading (--fresh reads again)", spec.name)
	}

	ds := make([]Decision, len(ss))
	for i := range ss {
		ds[i] = Decision{ID: i, Label: "narrative", Keep: true}
	}
	protect := markExceptions(ds, ss, c, spec.name)
	for i := range ss {
		if ds[i].Keep && isHesitation(ss[i].Text) {
			ds[i].Keep, ds[i].Label, ds[i].Note = false, "break", "only a hesitation sound"
		}
	}

	r.instr = r.instructions(ss, fresh)
	applyInstructions(ds, ss, r.instr)
	for _, in := range r.instr {
		if in.Applied {
			for _, id := range in.Request {
				protect[id] = true
				ds[id].Label = "instruction"
			}
			for id := in.PartFirst; in.Action == "cut" && id <= in.PartLast; id++ {
				protect[id] = true
			}
		}
	}

	if !c.Restored {
		var err error
		if c.Restore, err = restoreCuts(spec, ss, ds, protect, r.logf); err != nil {
			return Selection{}, err
		}
		c.Restored = true
		writeJSON(cachePath, c)
	}
	restoreLines(ds, c.Restore, protect, spec.name)
	return Selection{Model: spec.name, Guidance: r.guidance, Decisions: ds}, nil
}

// markExceptions cuts what the model says is not for the viewers and the
// retakes the words confirm. It returns the lines the restore pass may never
// put back: both of those (run 10 put the mic check "Talking talking." back).
func markExceptions(ds []Decision, ss []Sentence, c coarseCache, name string) map[int]bool {
	protect := map[int]bool{}
	for _, id := range c.Off {
		if id >= 0 && id < len(ds) {
			ds[id].Keep, ds[id].Label, ds[id].Note, protect[id] = false, "meta", "not said to the viewers ("+name+")", true
		}
	}
	for _, rt := range c.Retakes {
		if okRetake(ss, rt) { // the attempt and anything between ("No, I'm sorry.")
			for id := rt.Abandoned; id < rt.Redo; id++ {
				ds[id].Keep, ds[id].Label, protect[id] = false, "retake", true
				ds[id].Note = fmt.Sprintf("a retake: he says it again at %s", clock(ss[rt.Redo].Start))
			}
		}
	}
	return protect
}

func restoreLines(ds []Decision, restore []int, protect map[int]bool, name string) {
	for _, id := range restore {
		if id >= 0 && id < len(ds) && !ds[id].Keep && !protect[id] {
			ds[id].Keep, ds[id].Note = true, "put back: "+name+" read the edit and it needs this line"
		}
	}
}

// exceptions: the model names what is not for the viewers and the retakes.
func exceptions(spec localModelSpec, ss []Sentence, logf func(string, ...any)) ([]int, []retake, error) {
	m, err := spec.open()
	if err != nil {
		return nil, nil, err
	}
	logf("content: %s is reading the recording in parts...", spec.name)
	var off []int
	var rts []retake
	for _, p := range parts(len(ss)) {
		a, b := p[0], p[1]
		user := fill(promptWhatToRemove, "{TRANSCRIPT}", sentenceLines(ss, a, b))
		raw, err := m.chat(videoRole, user, exceptSchema, 400)
		var ans struct {
			Off     []int    `json:"not_for_viewers"`
			Retakes []retake `json:"retakes"`
		}
		if err != nil || json.Unmarshal([]byte(raw), &ans) != nil {
			return nil, nil, fmt.Errorf("%s could not read %s-%s: %v (%s)", spec.name, clock(ss[a].Start), clock(ss[b-1].End), err, tail(raw, 120))
		}
		for _, id := range ans.Off {
			if id >= a && id < b && !slices.Contains(off, id) {
				off = append(off, id)
			}
		}
		for _, rt := range ans.Retakes {
			if rt.Abandoned >= a && rt.Redo < b && !slices.Contains(rts, rt) {
				rts = append(rts, rt)
			}
		}
	}
	slices.Sort(off)
	logf("  %d sentence(s) not for the viewers, %d retake(s) named", len(off), len(rts))
	return off, rts, nil
}

// okRetake: the redo comes right after and repeats the abandoned attempt - it
// starts the same way, or most of the attempt's words come back. A model's
// "retake" that the words do not show is not cut.
func okRetake(ss []Sentence, rt retake) bool {
	if rt.Abandoned < 0 || rt.Redo >= len(ss) || rt.Redo <= rt.Abandoned || rt.Redo-rt.Abandoned > 3 {
		return false
	}
	a, b := normWords(ss[rt.Abandoned].Text), normWords(ss[rt.Redo].Text)
	if len(a) == 0 || len(b) == 0 {
		return false
	}
	if len(a) >= 2 && len(b) >= 2 && a[0] == b[0] && a[1] == b[1] {
		return true
	}
	shared := 0
	for _, w := range a {
		if slices.Contains(b, w) {
			shared++
		}
	}
	return len(a) >= 2 && float64(shared) >= 0.6*float64(len(a))
}

func normWords(s string) []string {
	var out []string
	for _, w := range strings.Fields(strings.ToLower(s)) {
		if w = strings.Trim(w, ".,!?;:-\"'"); w != "" {
			out = append(out, w)
		}
	}
	return out
}

var hesitations = map[string]bool{"um": true, "uh": true, "erm": true, "hmm": true, "mm": true, "ah": true, "uhh": true, "umm": true}

// isHesitation: a line of nothing but hesitation sounds. "Yeah", "okay" and
// "alright" are words he means; they are never filler here.
func isHesitation(text string) bool {
	ws := normWords(text)
	for _, w := range ws {
		if !hesitations[w] {
			return false
		}
	}
	return len(ws) > 0
}

// restarts: words he says and then starts again inside one sentence ("I'm gonna
// try to I'm gonna try not to drag this out" - the first "I'm gonna try to" goes).
// Two or more words come back within 4 words, and the attempt has no stop or
// comma ("it's like a very long stream, it's like two hour" is not one).
func restarts(words []Word, ss []Sentence) []int {
	var cut []int
	for _, s := range ss {
		w := make([]string, 0, s.W1-s.W0+1)
		for i := s.W0; i <= s.W1; i++ {
			w = append(w, strings.Trim(strings.ToLower(words[i].Word), ".,!?;:-\"'"))
		}
		for i := 0; i < len(w); i++ {
			found := false
			for j := i + 2; j <= i+4 && j+1 < len(w) && !found; j++ {
				if w[i] == "" || w[i] != w[j] || w[i+1] != w[j+1] {
					continue
				}
				if strings.ContainsAny(words[s.W0+j-1].Word, ".!?,") {
					continue
				}
				for k := i; k < j; k++ {
					cut = append(cut, s.W0+k)
				}
				i, found = j-1, true
			}
		}
	}
	return cut
}

// restoreCuts: the model reads each part of the edit as the viewer will hear it,
// with every cut line shown in place, and names the cuts that must go back.
func restoreCuts(spec localModelSpec, ss []Sentence, ds []Decision, protect map[int]bool, logf func(string, ...any)) ([]int, error) {
	m, err := spec.open()
	if err != nil {
		return nil, err
	}
	logf("content: %s is reading the edit like a viewer...", spec.name)
	var back []int
	for _, p := range parts(len(ss)) {
		a, b := p[0], p[1]
		var sb strings.Builder
		cuts := 0
		for i := a; i < b; i++ {
			tag := ""
			if !ds[i].Keep {
				tag = " CUT"
				if !protect[i] {
					cuts++
				}
			}
			fmt.Fprintf(&sb, "[id %d%s] %s\n", i, tag, ss[i].Text)
		}
		if cuts == 0 {
			continue
		}
		user := fill(promptPutBackCutsTheViewerNeeds, "{EDIT}", sb.String())
		raw, err := m.chat(videoRole, user, restoreSchema, 200)
		var ans struct {
			Restore []int `json:"restore"`
		}
		if err != nil || json.Unmarshal([]byte(raw), &ans) != nil {
			return nil, fmt.Errorf("%s could not read the edit at %s: %v (%s)", spec.name, clock(ss[a].Start), err, tail(raw, 120))
		}
		for _, id := range ans.Restore {
			if id >= a && id < b && !ds[id].Keep && !protect[id] && !slices.Contains(back, id) {
				back = append(back, id)
			}
		}
	}
	slices.Sort(back)
	logf("  %d cut line(s) put back", len(back))
	return back, nil
}

// Prompts Jordan can edit (prompts.go).
var promptWhatToRemove = P(dirYouTube, "What to remove", "THE TRANSCRIPT (one part):\n{TRANSCRIPT}"+
	"\nName only the exceptions:\n"+
	"- not_for_viewers: ids of sentences that are not part of the video he is making: checking the sound or camera (words said only to test it: \"testing\", \"check\", \"talking talking\"), talking to himself while he gets ready, talking to someone off camera, anything after his goodbye. (Talk to his video editor is handled separately - do not list it.)\n"+
	"- retakes: a sentence he breaks off or flubs and then says again right after. abandoned_id is the attempt he gives up on, redo_id the sentence where he says it again. Only a real second attempt at the same sentence - never two different sentences about the same thing.\n"+
	"Most parts have no exceptions: then answer {\"not_for_viewers\": [], \"retakes\": []}.")

var promptPutBackCutsTheViewerNeeds = P(dirYouTube, "Put back cuts the viewer needs", "ONE PART OF THE EDIT, in order. Lines marked CUT are taken out; the viewer hears the others, one after another:\n{EDIT}"+
	"\nRead it the way a viewer will hear it. List the ids of CUT lines that must go back because the lines around them do not make sense without them: the answer to a question he just asked, the end of a sentence or a thought, words the next line depends on.\n"+
	"Never list a line where he talks to his video editor, an attempt he says again right after, or getting ready. Most cuts are right: answer {\"restore\": []} when none must go back.")
