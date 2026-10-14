package main

// instructions.go - --video: Jordan gives his editor instructions on camera and
// the edit follows them. Jordan, 2026-10-12: "At one point I verbally give
// instructions that I FORGOT to mention something towards the beginning, I then
// discuss what I forgot to mention, and I wrap it up by confirming AGAIN that
// it's supposed to go earlier... Qwen / Gemma MUST be utilized to catch
// instructions like this... INTELLIGENT decisions might need to be made on a
// whim dependent upon my on-camera instructions."
//
//  1. Find: Qwen3.5 and Gemma-4 read the transcript in parts and list every
//     sentence where he talks to his editor; any voter that labelled a sentence
//     "instruction" adds it too (union - a missed instruction ruins the edit).
//  2. Understand: each nearby group goes to Gemma-4 12B (the lead), Gemma-4 and
//     Qwen3.5, asked in parts (small models answer parts, code applies the
//     rule): what does he ask for (move / cut / other / none), which sentences
//     are the request, which are the part he means, and - for a move - which
//     sentence the part comes right after. The majority's action stands; the
//     lead's sentences are used when it is in the majority.
//  3. Apply, in code: his request sentences are cut, a moved part is kept and
//     its pieces are put after the anchor (source times never change, only the
//     order VEGAS lays them in), a cut part is cut, and anything else he asks
//     for gets a region with his own words. Every applied instruction gets a
//     region saying what was done and what each model said.

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
)

// Instruction is one request Jordan made to his editor, as understood.
type Instruction struct {
	Request   []int    `json:"request_ids"` // the sentences where he asks (cut)
	Action    string   `json:"action"`      // move, cut, other, none
	PartFirst int      `json:"part_first"`  // the part he means (-1: none)
	PartLast  int      `json:"part_last"`
	After     int      `json:"after"` // move: the sentence the part comes right after (-1: none)
	Said      string   `json:"said"`  // his request in a few words (the lead model's)
	Models    []string `json:"models"`
	Note      string   `json:"note,omitempty"`
	Applied   bool     `json:"applied"`
	Cleared   []int    `json:"cleared_ids,omitempty"` // flagged, but the lead reads them as said to the viewers
	Restored  []int    `json:"-"`                     // cleared ones the vote had cut, put back (applyInstructions)
}

// modelRead is one model's reading of one group.
type modelRead struct {
	Model     string `json:"model"`
	Request   []int  `json:"request_ids"`
	Action    string `json:"action"`
	PartFirst int    `json:"part_first_id"`
	PartLast  int    `json:"part_last_id"`
	After     int    `json:"after_id"`
	Words     string `json:"after_words,omitempty"`
	Said      string `json:"request"`
}

const instrRole = `You are the video editor for Hair Jordan, a YouTuber. You are editing the raw camera recording of one of his YouTube videos. While recording, Jordan sometimes stops talking to his viewers and talks to YOU, his editor, the way he would talk to a human editor: "cut that out", "I forgot to mention something at the beginning", "put this part earlier", "that goes before the part about...", "add a picture of it here".`

var findSchema = jsonSchema("instruction_ids", map[string]any{"type": "object", "properties": map[string]any{
	"ids": map[string]any{"type": "array", "items": map[string]any{"type": "integer"}}}, "required": []string{"ids"}})

var whatSchema = jsonSchema("instruction", map[string]any{"type": "object", "properties": map[string]any{
	"request_ids":   map[string]any{"type": "array", "items": map[string]any{"type": "integer"}},
	"action":        map[string]any{"type": "string", "enum": []string{"move", "cut", "other", "none"}},
	"part_first_id": map[string]any{"type": "integer"},
	"part_last_id":  map[string]any{"type": "integer"},
	"request":       map[string]any{"type": "string"},
}, "required": []string{"request_ids", "action", "part_first_id", "part_last_id", "request"}})

var whereSchema = jsonSchema("place", map[string]any{"type": "object", "properties": map[string]any{
	"after_id":    map[string]any{"type": "integer"},
	"after_words": map[string]any{"type": "string"},
}, "required": []string{"after_id", "after_words"}})

// instructions finds and understands every instruction (cached per guidance).
func (r *run) instructions(ss []Sentence, fresh bool) []Instruction {
	path := filepath.Join(r.work, "instructions-"+r.tag+".json")
	var saved struct {
		Sentences int           `json:"sentences"`
		List      []Instruction `json:"instructions"`
	}
	if b, err := os.ReadFile(path); err == nil && !fresh && json.Unmarshal(b, &saved) == nil && saved.Sentences == len(ss) {
		r.logf("instructions: using the ones found before (%d)", len(saved.List))
		return saved.List
	}
	gm, _, _ := r.cfg.GemmaAVLM()
	qm, _, _ := r.cfg.Qwen()
	finders := []localModelSpec{{name: "qwen3.5", model: qm, server: r.cfg.LlamaServer}, {name: "gemma4", model: gm, server: r.cfg.LlamaServer}}
	readers := finders
	if _, err := os.Stat(r.cfg.GemmaModel12B); err == nil {
		readers = append([]localModelSpec{{name: "gemma4-12b", model: r.cfg.GemmaModel12B, server: r.cfg.LlamaServer}}, finders...)
	}

	found := voterInstructions(r.work, len(ss))
	for _, spec := range finders {
		ids, err := findInstructions(spec, ss, r.logf)
		if err != nil {
			r.logf("  instructions: %s could not look: %v", spec.name, err)
		}
		for _, id := range ids {
			found[id] = append(found[id], spec.name)
		}
	}
	var list []Instruction
	for _, g := range groups(found, groupGap) {
		var reads []modelRead
		for _, spec := range readers {
			mr, err := readInstruction(spec, ss, g)
			if err != nil {
				r.logf("  instructions: %s could not read %s: %v", spec.name, clock(ss[g[0]].Start), err)
				continue
			}
			reads = append(reads, mr)
		}
		in := combine(reads, ss)
		if len(reads) > 0 {
			in.Cleared = slices.DeleteFunc(slices.Clone(g), func(id int) bool { return slices.Contains(reads[0].Request, id) })
		}
		r.logf("  instructions at %s: %s - %s", clock(ss[g[0]].Start), in.Action, in.Note)
		list = append(list, in) // "none" too: its cleared sentences may go back in
	}
	saved.Sentences, saved.List = len(ss), list
	writeJSON(path, saved)
	return list
}

// voterInstructions: the sentences a voter labelled "instruction".
func voterInstructions(work string, n int) map[int][]string {
	out := map[int][]string{}
	for _, m := range voters {
		var s Selection
		if b, err := os.ReadFile(filepath.Join(work, "vote-"+m+".json")); err == nil && json.Unmarshal(b, &s) == nil && len(s.Decisions) == n {
			for _, d := range s.Decisions {
				if d.Label == "instruction" {
					out[d.ID] = append(out[d.ID], m+" vote")
				}
			}
		}
	}
	return out
}

// findInstructions: one model lists the sentences where he talks to the editor.
func findInstructions(spec localModelSpec, ss []Sentence, logf func(string, ...any)) ([]int, error) {
	m, err := spec.open()
	if err != nil {
		return nil, err
	}
	defer m.client.Close()
	logf("instructions: %s is listing where Jordan talks to his editor...", spec.name)
	var out []int
	const size, overlap = 40, 8
	for a := 0; a < len(ss); a += size - overlap {
		b := min(a+size, len(ss))
		user := "THE TRANSCRIPT (one part):\n" + sentenceLines(ss, a, b) +
			"\nList the ids of EVERY sentence above where Jordan talks to his editor instead of his viewers: telling the editor to cut, move or add something, saying he forgot to mention something that belongs somewhere else, or confirming where a part should go. Sentences he says to his viewers are not listed. Answer {\"ids\": []} when there are none."
		raw, err := m.chat(instrRole, user, findSchema, 300)
		var ans struct {
			IDs []int `json:"ids"`
		}
		if err != nil || json.Unmarshal([]byte(raw), &ans) != nil {
			logf("  %s: part %s-%s unreadable (%v)", spec.name, clock(ss[a].Start), clock(ss[b-1].End), err)
			continue
		}
		for _, id := range ans.IDs {
			if id >= a && id < b && !slices.Contains(out, id) {
				out = append(out, id)
			}
		}
		if b == len(ss) {
			break
		}
	}
	sort.Ints(out)
	return out, nil
}

// groupGap: he gives an instruction, talks through the part, then confirms it
// ("put this at the beginning" ... "so that's the part, I want that to go at
// the beginning" - 17 sentences apart on 2026-10-12), so one group spans it.
// ponytail: two different instructions this close become one reading (one action).
const groupGap = 25

// groups joins found sentences that are at most gap apart.
func groups(found map[int][]string, gap int) [][]int {
	var ids []int
	for id := range found {
		ids = append(ids, id)
	}
	sort.Ints(ids)
	var out [][]int
	for _, id := range ids {
		if n := len(out); n > 0 && id-out[n-1][len(out[n-1])-1] <= gap {
			out[n-1] = append(out[n-1], id)
			continue
		}
		out = append(out, []int{id})
	}
	return out
}

// contextFor is the transcript a reader gets: from the start (a part can go
// back to the beginning) to a little after the group.
// ponytail: up to ~9000 tokens; a longer recording starts 250 sentences back.
func contextFor(ss []Sentence, g []int) (int, int) {
	b := min(g[len(g)-1]+30, len(ss))
	chars := 0
	for i := 0; i < b; i++ {
		chars += len(ss[i].Text) + 20
	}
	if chars > 36000 {
		return max(0, b-250), b
	}
	return 0, b
}

func readInstruction(spec localModelSpec, ss []Sentence, g []int) (modelRead, error) {
	mr := modelRead{Model: spec.name, PartFirst: -1, PartLast: -1, After: -1}
	m, err := spec.open()
	if err != nil {
		return mr, err
	}
	defer m.client.Close()
	a, b := contextFor(ss, g)
	var quoted []string
	for _, id := range g {
		quoted = append(quoted, fmt.Sprintf("[id %d] %s", id, ss[id].Text))
	}
	user := "THE TRANSCRIPT:\n" + sentenceLines(ss, a, b) +
		"\nThese sentences may be Jordan talking to his editor:\n" + strings.Join(quoted, "\n") +
		"\n\nAnswer in parts:\n" +
		"- action: what he asks the editor to do. \"move\" = put a part of the video somewhere else (e.g. he forgot to mention something and says it belongs earlier); \"cut\" = take a part out; \"other\" = anything else (add a picture, music, a title...); \"none\" = he is not talking to the editor here.\n" +
		"- request_ids: the ids of every sentence where he talks to the editor about this, including when he repeats or confirms it and when he points the part out (\"so that's the part\", \"from here\", \"up to here\"). These are cut from the video.\n" +
		"  He often gives the instruction BEFORE the part (\"put this at the beginning\"), talks through the part to his viewers, and confirms it AFTER (\"so that's the part, I want that to go at the beginning\"). Then the part is everything he says between the two.\n" +
		"  Not every listed sentence is for the editor: \"oh wait, I forgot to mention\" said to his viewers, followed by more of his story, is part of the video.\n" +
		"- part_first_id, part_last_id: the first and last id of the part he means (what to move or cut). Not the sentences where he talks to the editor. -1 when there is no such part.\n" +
		"- request: his request in at most 15 words."
	raw, err := m.chat(instrRole, user, whatSchema, 400)
	if err != nil {
		return mr, err
	}
	if err := json.Unmarshal([]byte(raw), &mr); err != nil {
		return mr, fmt.Errorf("unreadable answer %q", tail(raw, 120))
	}
	mr.Model = spec.name
	if mr.Action != "move" || mr.PartFirst < 0 || mr.PartFirst >= len(ss) {
		return mr, nil
	}
	user = "THE TRANSCRIPT:\n" + sentenceLines(ss, a, b) +
		fmt.Sprintf("\nJordan asked his editor: \"%s\". The part to move is id %d to id %d:\n\"%s\"\n\n", mr.Said, mr.PartFirst, mr.PartLast, short(spanText(ss, mr.PartFirst, mr.PartLast), 600)) +
		"Where does that part go? Give after_id: the id of the sentence the part should come right AFTER, so it plays where he meant it to be. Then copy the first 6 words of that sentence into after_words."
	raw, err = m.chat(instrRole, user, whereSchema, 120)
	if err != nil {
		return mr, err
	}
	var w struct {
		After int    `json:"after_id"`
		Words string `json:"after_words"`
	}
	if json.Unmarshal([]byte(raw), &w) != nil {
		return mr, fmt.Errorf("unreadable answer %q", tail(raw, 120))
	}
	mr.After, mr.Words = w.After, w.Words
	return mr, nil
}

func spanText(ss []Sentence, a, b int) string {
	var t []string
	for i := max(a, 0); i <= b && i < len(ss); i++ {
		t = append(t, ss[i].Text)
	}
	return strings.Join(t, " ")
}

// combine: the lead (Gemma-4 12B, the first read) decides the action - on
// 2026-10-12 the two 4B models out-voted it and would have moved a sentence he
// said to his viewers. The others corroborate; a model with the same action
// fills in when the lead's place does not check out (its anchor words must
// match its anchor id).
func combine(reads []modelRead, ss []Sentence) Instruction {
	in := Instruction{Action: "none", PartFirst: -1, PartLast: -1, After: -1}
	if len(reads) == 0 {
		in.Note = "no model could read it"
		return in
	}
	best := reads[0].Action
	var said []string
	for _, mr := range reads {
		d := mr.Action
		if mr.Action == "move" {
			d += fmt.Sprintf(" %d-%d after %d", mr.PartFirst, mr.PartLast, mr.After)
		}
		said = append(said, mr.Model+": "+d)
	}
	in.Note = strings.Join(said, "; ")
	in.Action = best
	if best == "none" {
		return in
	}
	for _, mr := range reads {
		if mr.Action != best {
			continue
		}
		if best == "move" && !anchorMatches(ss, mr.After, mr.Words) {
			in.Note += "; " + mr.Model + "'s place did not match its own quote"
			continue
		}
		in.Models = append(in.Models, mr.Model)
		if len(in.Models) > 1 {
			continue
		}
		in.Request, in.PartFirst, in.PartLast, in.After, in.Said = mr.Request, mr.PartFirst, mr.PartLast, mr.After, mr.Said
	}
	if len(in.Models) == 0 {
		in.Note += "; not applied: no model gave a place that checks out"
		return in
	}
	in.Request = slices.DeleteFunc(slices.Clone(in.Request), func(id int) bool {
		return id < 0 || id >= len(ss)
	})
	bracket(&in)
	switch {
	case (best == "move" || best == "cut") && (in.PartFirst < 0 || in.PartLast < in.PartFirst || in.PartLast >= len(ss)):
		in.Note += "; not applied: the part is not a valid range"
	case best == "move" && (in.After < 0 || in.After >= len(ss) || (in.After >= in.PartFirst-1 && in.After <= in.PartLast)):
		in.Note += "; not applied: the place is inside or right next to the part"
	default:
		in.Applied = true
	}
	return in
}

// bracket: when he asked both before and after the part, the part is
// everything between those two requests (the 12B ended the part early on
// 2026-10-12: at 6:28, where he went on to 6:57 "so that's the part").
func bracket(in *Instruction) {
	if in.PartFirst < 0 || in.PartLast < in.PartFirst {
		return
	}
	before, after := -1, -1
	for _, id := range in.Request {
		if id < in.PartFirst && id > before {
			before = id
		}
		if id > in.PartLast && (after < 0 || id < after) {
			after = id
		}
	}
	if before >= 0 && after >= 0 {
		in.PartFirst, in.PartLast = before+1, after-1
	}
}

// anchorMatches: at least half of the quoted words are in that sentence.
func anchorMatches(ss []Sentence, id int, quote string) bool {
	if id < 0 || id >= len(ss) {
		return false
	}
	have := map[string]bool{}
	for _, w := range strings.Fields(ss[id].Text) {
		have[normWord(w)] = true
	}
	n, hit := 0, 0
	for _, w := range strings.Fields(quote) {
		if w = normWord(w); w != "" {
			n++
			if have[w] {
				hit++
			}
		}
	}
	return n > 0 && 2*hit >= n
}

// applyInstructions overrides the content decision. Request sentences are
// cut; a cut part is cut; inside a moved part the vote stands (it had cut the
// lone "Whatever." that opens it, 2026-10-13). A sentence the finders flagged
// that the lead does NOT read as talk to the editor goes back in when the vote
// cut it (and it is not filler): the voters cut "Oh wait, I forgot to mention."
// - said to his viewers - as an instruction. Restored lists those, for a region.
func applyInstructions(ds []Decision, ss []Sentence, list []Instruction) {
	at := func(id int) *Decision {
		for i := range ds {
			if ds[i].ID == id {
				return &ds[i]
			}
		}
		return nil
	}
	for k := range list {
		in := &list[k]
		in.Restored = nil
		for _, id := range in.Cleared {
			if d := at(id); d != nil && !d.Keep && !isFiller(ss[id].Text) {
				d.Keep, d.Note = true, "kept: the voters took it for talk to the editor; "+in.lead()+" reads it as said to the viewers"
				in.Restored = append(in.Restored, id)
			}
		}
		if !in.Applied {
			continue
		}
		for id := in.PartFirst; id <= in.PartLast && in.Action == "cut"; id++ {
			if d := at(id); d != nil {
				d.Keep, d.Note = false, "cut: per Jordan's instruction (\""+short(in.Said, 60)+"\")"
			}
		}
		for _, id := range in.Request { // last: a request said inside the part is still cut
			if d := at(id); d != nil {
				d.Keep, d.Note = false, "cut: Jordan talking to his editor (\""+short(in.Said, 60)+"\")"
			}
		}
	}
}

// lead is the model that read this group first (the 12B when it is there).
func (in Instruction) lead() string {
	if m, _, ok := strings.Cut(in.Note, ":"); ok {
		return m
	}
	return "the lead model"
}

// boundary is a cut point in the pause before sentence i (or after the last).
func boundary(ss []Sentence, i int) float64 {
	switch {
	case i <= 0:
		return math.Max(0, ss[0].Start-0.3)
	case i >= len(ss):
		return ss[len(ss)-1].End + 0.3
	}
	return (ss[i-1].End + ss[i].Start) / 2
}

// movePieces puts the pieces of source [cs, ce) right before the first piece
// at or after source time at. Pieces that straddle a point are split there.
func movePieces(ps []span, cs, ce, at, fps float64) []span {
	var cut []span
	for _, p := range ps {
		parts := []span{p}
		for _, t := range []float64{snap(cs, fps), snap(ce, fps), snap(at, fps)} {
			var next []span
			for _, q := range parts {
				if t > q.A && t < q.B {
					next = append(next, span{q.A, t}, span{t, q.B})
				} else {
					next = append(next, q)
				}
			}
			parts = next
		}
		cut = append(cut, parts...)
	}
	var moved, rest []span
	for _, p := range cut {
		if p.A >= snap(cs, fps)-1e-6 && p.B <= snap(ce, fps)+1e-6 {
			moved = append(moved, p)
		} else {
			rest = append(rest, p)
		}
	}
	if len(moved) == 0 {
		return ps
	}
	i := len(rest)
	for k, p := range rest {
		if p.A >= snap(at, fps)-1e-6 {
			i = k
			break
		}
	}
	return slices.Concat(rest[:i], moved, rest[i:])
}

// reorder applies every moved part to the final pieces.
func reorder(ps []span, ss []Sentence, list []Instruction, fps float64) []span {
	for _, in := range list {
		if in.Applied && in.Action == "move" {
			ps = movePieces(ps, boundary(ss, in.PartFirst), boundary(ss, in.PartLast+1), boundary(ss, in.After+1), fps)
		}
	}
	return ps
}

// instructionMarks: what was done for each instruction, on the timeline.
func instructionMarks(list []Instruction, ss []Sentence, ps []piece) []mark {
	var ms []mark
	for _, in := range list {
		for _, id := range in.Restored {
			if t, _, ok := toTimeline(ps, ss[id].Start, ss[id].End); ok {
				ms = append(ms, mark{At: t, Label: short(fmt.Sprintf("Kept \"%s\" - the voters took it for talk to the editor; %s reads it as said to the viewers", ss[id].Text, in.lead()), 230)})
			}
		}
		if in.Action == "none" {
			continue
		}
		at := ss[in.Request0()].Start
		said := fmt.Sprintf("you said at %s: \"%s\"", clock(at), short(in.Said, 80))
		switch {
		case !in.Applied:
			if t, ok := cutPoint(ps, at); ok {
				ms = append(ms, mark{At: t, Label: short("Instruction NOT done - "+said+" ("+in.Note+")", 230)})
			}
		case in.Action == "move":
			if t0, t1, ok := toTimeline(ps, ss[in.PartFirst].Start, ss[in.PartLast].End); ok {
				ms = append(ms, mark{At: t0, Len: math.Max(t1-t0, 1), Label: short("Moved here - "+said+" ("+strings.Join(in.Models, " + ")+")", 230)})
			}
		case in.Action == "cut":
			if t, ok := cutPoint(ps, ss[in.PartFirst].Start); ok {
				ms = append(ms, mark{At: t, Label: short("Cut - "+said+" ("+strings.Join(in.Models, " + ")+")", 230)})
			}
		default: // other: becky cannot do it, so it is where he asked
			if t, ok := cutPoint(ps, at); ok {
				ms = append(ms, mark{At: t, Len: 2, Label: short("For you to do - "+said, 230)})
			}
		}
	}
	return ms
}

// Request0 is the first sentence of the request (the part's when none).
func (in Instruction) Request0() int {
	if len(in.Request) > 0 {
		return slices.Min(in.Request)
	}
	return max(in.PartFirst, 0)
}

// writeInstructions lists his on-camera instructions and what was done, for the report.
func writeInstructions(b *strings.Builder, list []Instruction, ss []Sentence) {
	var real []Instruction
	var kept []string
	for _, in := range list {
		if in.Action != "none" {
			real = append(real, in)
		}
		for _, id := range in.Restored {
			kept = append(kept, fmt.Sprintf("- %s \"%s\" - kept: the voters took it for talk to the editor; %s reads it as said to the viewers\n", clock(ss[id].Start), ss[id].Text, in.lead()))
		}
	}
	fmt.Fprintf(b, "## What you told the editor on camera (%d)\n\n", len(real))
	if len(real) == 0 {
		b.WriteString("Nothing - no model heard you talk to the editor.\n")
	}
	b.WriteString(strings.Join(kept, ""))
	for _, in := range real {
		done := "**not done**"
		switch {
		case in.Applied && in.Action == "move":
			done = fmt.Sprintf("moved %s-%s (\"%s\") to right after %s (\"%s\")", clock(ss[in.PartFirst].Start), clock(ss[in.PartLast].End),
				short(ss[in.PartFirst].Text, 50), clock(ss[in.After].End), short(ss[in.After].Text, 50))
		case in.Applied && in.Action == "cut":
			done = fmt.Sprintf("cut %s-%s", clock(ss[in.PartFirst].Start), clock(ss[in.PartLast].End))
		case in.Applied:
			done = "for you to do - becky cannot"
		}
		fmt.Fprintf(b, "- %s \"%s\" - %s. Your words to the editor are cut (%d sentences). Models: %s\n",
			clock(ss[in.Request0()].Start), in.Said, done, len(in.Request), in.Note)
	}
	b.WriteString("\n")
}
