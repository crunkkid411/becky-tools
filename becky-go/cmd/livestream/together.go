package main

// together.go - --model systemone: the content decision is never one model's.
// Jordan, 2026-10-08, on the first System One edit: "systemone is ONE DATA
// POINT - becky-tools uses multiple data points to corroborate decisions...
// gemma qwen and systemone have to work TOGETHER, not instead of one another."
// On that edit every line he flagged had been cut by Gemma-4 and Qwen3.5 in
// their own runs, but System One decided alone and its unsure calls were kept.
//
// So: System One, Gemma-4 and Qwen3.5 each decide every sentence on their own
// and the majority stands. A split vote while he is looking down at his screen
// (reading chat) is cut. Then contextPass reads the whole edit.

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"becky-go/internal/systemone"
)

// vote is one model's own call on one sentence.
type vote struct {
	Model string `json:"model"`
	Keep  bool   `json:"keep"`
	Why   string `json:"why,omitempty"`
}

var voters = []string{"systemone", "gemma4", "qwen3.5"}

// calls is each model's own call on every sentence: from its saved vote file,
// a saved full run of that model (its lead calls), or a new run.
func (r *run) calls(name string, spec localModelSpec, ss []Sentence, fresh bool) (Selection, error) {
	files := []string{"vote-" + name + ".json"}
	if name != "systemone" {
		files = append(files, "selection-"+name+".json") // a --model gemma/qwen run: its lead calls
	}
	for _, f := range files {
		var s Selection
		if b, err := os.ReadFile(filepath.Join(r.work, f)); err == nil && !fresh &&
			json.Unmarshal(b, &s) == nil && s.Guidance == r.guidance && len(s.Decisions) == len(ss) {
			r.logf("%s: using its earlier calls (%s)", name, f)
			return s, nil
		}
	}
	var s Selection
	var err error
	if name == "systemone" {
		s, err = runSystemOne(systemone.NewHosted("becky-livestream"), ss, r.guidance, r.logf)
	} else {
		s, err = runLead(spec, ss, r.guidance, "", r.logf)
	}
	if err != nil {
		return s, err
	}
	writeJSON(filepath.Join(r.work, "vote-"+name+".json"), s)
	return s, nil
}

// ownCall is a model's own keep and its reason. System One's own call is
// Said (a saved run before 2026-10-08 holds Keep after becky's old rules).
func ownCall(model string, d Decision) vote {
	if model == "systemone" {
		return vote{Model: model, Keep: d.Said, Why: d.Label}
	}
	return vote{Model: model, Keep: keeps(d.Topic, d.Label), Why: why(d.Topic, d.Label)}
}

// together runs the three models and the vote.
func (r *run) together(ss []Sentence, fresh bool) (Selection, error) {
	start := time.Now()
	gm, _, _ := r.cfg.GemmaAVLM()
	qm, _, _ := r.cfg.Qwen()
	specs := map[string]localModelSpec{
		"gemma4":  {name: "gemma4", model: gm, server: r.cfg.LlamaServer},
		"qwen3.5": {name: "qwen3.5", model: qm, server: r.cfg.LlamaServer},
	}
	sel := Selection{Model: "systemone", Reviewer: "gemma4 + qwen3.5", Guidance: r.guidance}
	all := map[string][]Decision{}
	for _, m := range voters {
		s, err := r.calls(m, specs[m], ss, fresh)
		if err != nil {
			return sel, fmt.Errorf("%s could not decide: %w", m, err)
		}
		all[m] = s.Decisions
		if m == "gemma4" { // its reading of the guidance and its outline brief the readers
			sel.Topics, sel.Outline = s.Topics, s.Outline
		}
	}
	post, err := loadPosture(r.cfg, r.media, r.work, r.stem, r.duration, r.logf)
	if err != nil {
		sel.Notes = append(sel.Notes, "where he looks could not be measured ("+err.Error()+"), so his posture was not used")
		r.logf("  posture: %v", err)
	}
	chat, asked, err := r.chatAnswers(ss)
	if err != nil {
		sel.Notes = append(sel.Notes, "the live chat could not be checked ("+err.Error()+"), so his answers to chat were not used")
		r.logf("  live chat: %v", err)
	}
	ds := vote3(ss, all, post, chat)
	for i := range ds {
		ds[i].Asked = asked[i]
	}
	var notes []string
	if len(sel.Topics) == 0 {
		sel.Topics = []string{r.guidance}
	}
	readers := []localModelSpec{specs["gemma4"], specs["qwen3.5"]}
	if _, err := os.Stat(r.cfg.GemmaModel12B); err == nil {
		// the 4B models alone kept "I'll just do it live because..." as channel update (2026-10-08)
		readers = append(readers, localModelSpec{name: "gemma4-12b", model: r.cfg.GemmaModel12B, server: r.cfg.LlamaServer})
	}
	sel.Decisions, notes = contextPass(readers, ss, cutFiller(ss, ds),
		readBrief(r.guidance, sel.Topics, sel.Outline), post, r.logf)
	sel.Notes = append(sel.Notes, notes...)
	sel.Rules = rulesVersion
	sel.Seconds = time.Since(start).Seconds()
	return sel, nil
}

// vote3: the majority of the voters decides; a split keep while he is reading
// his screen or answering a chat message is cut.
func vote3(ss []Sentence, all map[string][]Decision, post *postureFrames, chat map[int]string) []Decision {
	out := make([]Decision, len(ss))
	for i, s := range ss {
		d := Decision{ID: s.ID}
		yes := 0
		for _, m := range voters {
			v := ownCall(m, all[m][i])
			d.Votes = append(d.Votes, v)
			if v.Keep {
				yes++
			}
		}
		d.Keep = 2*yes > len(voters)
		d.Confidence = 100 * max(yes, len(voters)-yes) / len(voters)
		d.Label = all["gemma4"][i].Label
		d.Chat = chat[i]
		reading := false
		if post != nil {
			q := post.during(s.Start, s.End)
			d.Posture, reading = q.words(), q.reading()
		}
		if d.Keep && yes < len(voters) && (reading || d.Chat != "") {
			d.Keep = false
			d.Note = "cut: the vote was split and he is reading or answering chat"
		}
		if d.Keep {
			d.Topic, d.Label = 1, "narrative"
		}
		d.Said = d.Keep
		out[i] = d
	}
	return out
}

// writeTogether lists, for the report, how every line that was not a plain
// three-way agreement was decided, and every kept line.
func writeTogether(b *strings.Builder, sel Selection, ss []Sentence) {
	var kept, readCuts, split []string
	for _, d := range sel.Decisions {
		yes := 0
		for _, v := range d.Votes {
			if v.Keep {
				yes++
			}
		}
		line := fmt.Sprintf("- %s **%s** \"%s\" - %s", clock(ss[d.ID].Start), verdict(d.Keep), short(ss[d.ID].Text, 90), voteText(d))
		switch {
		case d.Keep:
			kept = append(kept, line)
		case len(d.Read) > 0:
			readCuts = append(readCuts, line)
		case yes > 0:
			split = append(split, line)
		}
	}
	fmt.Fprintf(b, "### Cut when the models read the edit (%d)\n\n%s\n\n", len(readCuts), strings.Join(readCuts, "\n"))
	fmt.Fprintf(b, "### Cut, but at least one model wanted it (%d)\n\n%s\n\n", len(split), strings.Join(split, "\n"))
	fmt.Fprintf(b, "### Every kept line (%d)\n\n%s\n\n", len(kept), strings.Join(kept, "\n"))
}

// fillerWords: a kept line made only of these is cut without asking a model
// ("Like", "Yeah.", "Um", "Okay." - Jordan: "that's nonsense", 2026-10-08).
// Not "no" or "what": those can be his answer or reaction.
var fillerWords = map[string]bool{"like": true, "um": true, "uh": true, "yeah": true, "okay": true, "ok": true,
	"so": true, "right": true, "oh": true, "well": true, "anyway": true, "anyways": true, "hmm": true,
	"you": true, "know": true, "i": true, "mean": true, "dude": true, "man": true, "just": true, "and": true, "but": true}

func cutFiller(ss []Sentence, ds []Decision) []Decision {
	out := append([]Decision(nil), ds...)
	for i := range out {
		if out[i].Keep && isFiller(ss[i].Text) {
			out[i].Keep, out[i].Topic = false, 0
			out[i].Note = "cut: only filler words"
		}
	}
	return out
}

func isFiller(text string) bool {
	n := 0
	for _, w := range strings.Fields(strings.ToLower(text)) {
		w = strings.Trim(w, ".,!?;:-\"'")
		if w == "" {
			continue
		}
		if !fillerWords[w] {
			return false
		}
		n++
	}
	return n > 0
}

// voteText is how a line was decided, in plain words, for the report.
func voteText(d Decision) string {
	var parts []string
	for _, v := range append(append([]vote{}, d.Votes...), d.Read...) {
		p := v.Model + " " + verdict(v.Keep)
		if v.Why != "" {
			p += " (" + v.Why + ")"
		}
		parts = append(parts, p)
	}
	if d.Chat != "" {
		parts = append(parts, "answering chat: "+d.Chat)
	}
	if d.Posture != "" {
		parts = append(parts, "posture: "+d.Posture)
	}
	return strings.Join(parts, "; ")
}
