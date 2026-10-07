package main

// select.go - report item #1, the content decision. This is the ONLY place a
// model is asked anything about what stays; every other step is deterministic.
//
// Local models (Gemma-4 E4B, Qwen3.5-4B) decide ~40 sentences at a time, with a
// one-line-per-part outline of the whole stream for orientation, and are held
// to a JSON schema so a 4B model can only answer in the right shape. They are
// NOT asked keep/cut: they name each sentence's wanted topic (0 = none) and its
// label, and becky applies the keep rule itself (topic > 0 AND narrative). On
// 2026-10-05 Qwen3.5-4B labeled the whole baldness topic "narrative" and still
// answered keep=false for every line of it - but numbered the same lines topic
// 2 when asked which wanted topic they belong to. The other local model then
// re-decides, WITHOUT seeing the first answer, every call the lead was unsure
// about plus both sides of every keep/cut boundary (Jordan, 2026-10-05: "I'm
// open to gemma and qwen reviewing each other's work wherever it makes sense,
// especially when there is a low confidence score").
// Claude decides the whole transcript in one headless OAuth call (fleet-run).
//
// Corroborate, then conclude: two models agreeing is the answer. A disagreement,
// or a low-confidence call nobody could check, is UNSURE: it is kept (a wanted
// sentence that goes missing is invisible; an unwanted one is easy to delete)
// and gets a region on the timeline so Jordan looks at it.

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"time"

	"becky-go/internal/llmlocal"
)

var labels = []string{"narrative", "chat_reply", "super_chat", "break", "retake", "meta"}

const (
	windowSentences = 40  // sentences decided per local-model call
	windowWords     = 700 // ...or fewer, so one call stays a few thousand tokens
	sureConfidence  = 70  // below this the call is reviewed (or unsure)
	nearKeep        = 4   // cuts this close to a kept sentence are reviewed
	contextCarry    = 5   // already-decided sentences shown before each window
	isolatedReach   = 3   // an unsure sentence this far from kept material is cut
	localCtx        = 12288
)

// Decision is the verdict on one sentence. Topic is the local models' answer
// (1-based into Selection.Topics, 0 = none); Claude answers Keep directly.
type Decision struct {
	ID         int     `json:"id"`
	Label      string  `json:"label"`
	Topic      int     `json:"topic,omitempty"`
	Keep       bool    `json:"keep"`
	Confidence int     `json:"confidence"`
	Note       string  `json:"note,omitempty"`
	Review     *Review `json:"review,omitempty"`
	Unsure     bool    `json:"unsure,omitempty"`
	Said       bool    `json:"said"` // the lead model's own call (Keep is after becky's rules)
}

// Review is the second model's independent call on the same sentence.
type Review struct {
	Model      string `json:"model"`
	Label      string `json:"label"`
	Topic      int    `json:"topic,omitempty"`
	Keep       bool   `json:"keep"`
	Confidence int    `json:"confidence"`
}

// Selection is the content decision for one run, saved as selection-<model>.json.
type Selection struct {
	Model     string     `json:"model"`
	Reviewer  string     `json:"reviewer,omitempty"`
	Guidance  string     `json:"guidance"`
	Topics    []string   `json:"topics,omitempty"` // the wanted topics, as the lead model read the guidance
	Outline   []string   `json:"outline,omitempty"`
	Decisions []Decision `json:"decisions"`
	Seconds   float64    `json:"seconds"`
	Notes     []string   `json:"notes,omitempty"`
	Rules     int        `json:"rules,omitempty"` // which version of conclude() made Decisions
}

// rulesVersion is bumped whenever conclude() changes, so a saved decision is
// concluded again (2: chat replies the model cut stay cut; the stray-fragment
// rule only cuts what the model itself said to cut).
const rulesVersion = 2

// editorRole is what every model is told; the local models then get topicRule
// (systemPrompt), Claude gets keepRule (claudeOrder).
const editorRole = `You are the video editor for Hair Jordan, a YouTuber. You are cutting one of his livestream recordings down to an edited video. You get the transcript one sentence at a time and decide, for every sentence, whether it stays in the edit.

The editor's GUIDANCE says which topics stay. Everything the guidance does not ask for is cut.

Give every sentence exactly one label:
- narrative: Jordan talking about a topic (the real content)
- chat_reply: reading or answering live chat, greeting or thanking viewers
- super_chat: reading or thanking for a paid Super Chat, donation or membership
- break: stepping away, "be right back", waiting, drinking, filler while nothing happens
- retake: a false start or broken sentence that Jordan says again right after
- meta: about the stream itself: can you hear me, camera or audio problems, start or end housekeeping, asking viewers to like or subscribe`

const keepRule = `

keep is true only when the sentence belongs to a topic the guidance asks for AND it is narrative.
Inside a wanted topic, still cut chat replies, super chats, breaks, meta and retakes (for a retake, cut the broken attempt and keep the attempt he finishes).
One exception: if Jordan reads a viewer's question and then answers it inside a wanted topic, keep the question too, so the answer makes sense.

confidence is 0-100: how sure you are about keep. Use a number below 70 when you are unsure, for example where a topic starts or ends, or when a sentence could go either way.`

const topicRule = `

The topics the guidance asks for are listed as numbered WANTED TOPICS. topic is the number of the wanted topic the sentence is part of, or 0 when it is part of none of them. A sentence is part of a topic while Jordan is talking about that topic, even when the sentence itself does not name it.
A viewer's question that Jordan reads out and then answers inside a wanted topic is labeled narrative, so the answer makes sense. For a retake, label the broken attempt retake and the attempt he finishes narrative.

confidence is 0-100: how sure you are about the topic. Use a number below 70 when you are unsure, for example where a topic starts or ends, or when a sentence could go either way.`

const systemPrompt = editorRole + topicRule

// outlinePrompt is the outline's own instruction: under systemPrompt Gemma-4
// once answered a part's outline with a list of labels (2026-10-05).
const outlinePrompt = `You summarize one part of a livestream transcript by Hair Jordan, a YouTuber, in one short line that names the topics he talks about.`

// keeps is the keep rule the local models are not asked to apply themselves.
func keeps(topic int, label string) bool { return topic > 0 && label == "narrative" }

// why says in a few words what a local call rests on (for the report).
func why(topic int, label string) string {
	switch {
	case topic == 0:
		return "not a wanted topic"
	case label != "narrative":
		return fmt.Sprintf("topic %d, but %s", topic, label)
	}
	return fmt.Sprintf("topic %d", topic)
}

// windows splits the sentences into decision windows.
func windows(ss []Sentence) [][2]int {
	var out [][2]int
	for a := 0; a < len(ss); {
		b, n := a, 0
		for b < len(ss) && b-a < windowSentences && (n < windowWords || b == a) {
			n += ss[b].W1 - ss[b].W0 + 1
			b++
		}
		out = append(out, [2]int{a, b})
		a = b
	}
	return out
}

func sentenceLines(ss []Sentence, a, b int) string {
	var sb strings.Builder
	for i := a; i < b; i++ {
		fmt.Fprintf(&sb, "[id %d | %s] %s\n", ss[i].ID, clock(ss[i].Start), ss[i].Text)
	}
	return sb.String()
}

func outlineText(outline []string) string {
	if len(outline) == 0 {
		return ""
	}
	return "WHAT THE WHOLE STREAM COVERS (rough outline, for orientation):\n" + strings.Join(outline, "\n") + "\n\n"
}

func wantedText(topics []string) string {
	var sb strings.Builder
	sb.WriteString("WANTED TOPICS:\n")
	for i, t := range topics {
		fmt.Fprintf(&sb, "%d. %s\n", i+1, t)
	}
	return sb.String() + "\n"
}

func jsonSchema(name string, schema map[string]any) map[string]any {
	return map[string]any{"type": "json_schema", "json_schema": map[string]any{"name": name, "schema": schema}}
}

func decideSchema(n, nTopics int) map[string]any {
	item := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"id":         map[string]any{"type": "integer"},
			"label":      map[string]any{"type": "string", "enum": labels},
			"topic":      map[string]any{"type": "integer", "minimum": 0, "maximum": nTopics},
			"confidence": map[string]any{"type": "integer", "minimum": 0, "maximum": 100},
		},
		"required": []string{"id", "label", "topic", "confidence"},
	}
	return jsonSchema("decisions", map[string]any{
		"type": "object",
		"properties": map[string]any{
			"decisions": map[string]any{"type": "array", "items": item, "minItems": n, "maxItems": n},
		},
		"required": []string{"decisions"},
	})
}

// jsonPart drops anything a model wrote before its JSON object.
func jsonPart(raw string) string {
	raw = strings.TrimSpace(raw)
	if i := strings.Index(raw, "{"); i > 0 {
		raw = raw[i:]
	}
	return raw
}

// parseDecisions reads the model's JSON and lines it up with the asked ids by
// position (a small model sometimes miscounts ids, never the array length the
// schema forces), then applies the keep rule.
func parseDecisions(raw string, ids []int, nTopics int) ([]Decision, error) {
	var got struct {
		Decisions []Decision `json:"decisions"`
	}
	if err := json.Unmarshal([]byte(jsonPart(raw)), &got); err != nil {
		return nil, fmt.Errorf("unreadable answer: %w", err)
	}
	if len(got.Decisions) != len(ids) {
		return nil, fmt.Errorf("answered %d of %d sentences", len(got.Decisions), len(ids))
	}
	for i := range got.Decisions {
		d := &got.Decisions[i]
		d.ID = ids[i]
		if !validLabel(d.Label) {
			d.Label = "narrative"
		}
		if d.Topic < 0 || d.Topic > nTopics {
			d.Topic = 0
		}
		d.Keep = keeps(d.Topic, d.Label)
		d.Confidence = min(max(d.Confidence, 0), 100)
	}
	return got.Decisions, nil
}

// parseTopics reads the wanted-topics answer; blank items are dropped.
func parseTopics(raw string) ([]string, error) {
	var got struct {
		Topics []string `json:"topics"`
	}
	if err := json.Unmarshal([]byte(jsonPart(raw)), &got); err != nil {
		return nil, fmt.Errorf("unreadable answer: %w", err)
	}
	var out []string
	for _, t := range got.Topics {
		if t = strings.TrimSpace(t); t != "" {
			out = append(out, t)
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("no topics in the answer")
	}
	return out, nil
}

func validLabel(l string) bool { return slices.Contains(labels, l) }

// localModel is one llama-server session.
type localModel struct {
	name   string // gemma4 / qwen3.5
	client *llmlocal.Client
}

func (m localModel) ask(user string, schema any, maxTok int) (string, error) {
	return m.chat(systemPrompt, user, schema, maxTok)
}

func (m localModel) chat(system, user string, schema any, maxTok int) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	return m.client.Chat(ctx, system, user, llmlocal.Options{MaxTokens: maxTok, ResponseFormat: schema})
}

// decideIDs asks for one decision per id. Temperature 0 + a fixed seed make a
// plain retry give the same answer, so the second attempt gets double the
// allowance; a failed answer's ending goes to the log so the cause is visible.
func (m localModel) decideIDs(user string, ids []int, nTopics int, what string, logf func(string, ...any)) ([]Decision, error) {
	user += "\nWrite the JSON compactly, on one line."
	var err error
	for attempt := 1; attempt <= 2; attempt++ {
		var raw string
		if raw, err = m.ask(user, decideSchema(len(ids), nTopics), decideBudget(len(ids), attempt)); err == nil {
			var ds []Decision
			if ds, err = parseDecisions(raw, ids, nTopics); err == nil {
				return ds, nil
			}
		}
		logf("  %s attempt %d: %v (answer ends: %q)", what, attempt, err, tail(raw, 160))
	}
	return nil, err
}

// decideBudget is the answer allowance (tokens) for n decisions. Gemma-4 ran
// out at 60 per sentence on its first live run (2026-10-05).
func decideBudget(n, attempt int) int {
	return min((120*n+400)*attempt, 8000) // window prompt + 8000 fits localCtx
}

func tail(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return "..." + s[len(s)-n:]
}

// wantedTopics lists the topics the guidance asks for, in its own words, so
// every sentence can be asked which one it is part of.
func (m localModel) wantedTopics(guidance string) ([]string, error) {
	user := "GUIDANCE: " + guidance + "\n\nList every topic this guidance asks to keep, one item per topic, in the guidance's own words. Do not add topics it does not ask for.\nWrite the JSON compactly, on one line."
	schema := jsonSchema("topics", map[string]any{
		"type": "object",
		"properties": map[string]any{
			"topics": map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "minItems": 1, "maxItems": 8},
		},
		"required": []string{"topics"},
	})
	raw, err := m.ask(user, schema, 400)
	if err != nil {
		return nil, err
	}
	return parseTopics(raw)
}

// outline asks for one line per window: what Jordan talks about there.
func (m localModel) outline(ss []Sentence, ws [][2]int, logf func(string, ...any)) []string {
	var out []string
	for i, w := range ws {
		user := "Here is one part of the livestream transcript:\n\n" + sentenceLines(ss, w[0], w[1]) +
			"\nIn ONE short line (at most 15 words), what does Jordan talk about in this part? Name the topics. Answer with the line only."
		ans, err := m.chat(outlinePrompt, user, nil, 60)
		if err != nil {
			logf("  outline part %d/%d: %v", i+1, len(ws), err)
			ans = "(unknown)"
		}
		ans = strings.TrimSpace(strings.SplitN(strings.TrimSpace(ans), "\n", 2)[0])
		out = append(out, fmt.Sprintf("[%s-%s] %s", clock(ss[w[0]].Start), clock(ss[w[1]-1].End), ans))
	}
	return out
}

// decideWindows runs the lead model over every window. brief is the guidance,
// the numbered wanted topics and the outline.
func (m localModel) decideWindows(ss []Sentence, ws [][2]int, brief string, nTopics int, logf func(string, ...any)) ([]Decision, error) {
	all := make([]Decision, 0, len(ss))
	for i, w := range ws {
		var ctxLines strings.Builder
		for j := max(0, w[0]-contextCarry); j < w[0]; j++ {
			fmt.Fprintf(&ctxLines, "[id %d] topic %d, %s: %s\n", ss[j].ID, all[j].Topic, all[j].Label, ss[j].Text)
		}
		user := brief
		if ctxLines.Len() > 0 {
			user += "ALREADY DECIDED JUST BEFORE THIS PART (context only, do not repeat):\n" + ctxLines.String() + "\n"
		}
		user += "DECIDE THESE SENTENCES:\n" + sentenceLines(ss, w[0], w[1]) +
			fmt.Sprintf("\nAnswer with one decision for every id from %d to %d, in order.", ss[w[0]].ID, ss[w[1]-1].ID)
		ids := make([]int, 0, w[1]-w[0])
		for j := w[0]; j < w[1]; j++ {
			ids = append(ids, ss[j].ID)
		}
		ds, err := m.decideIDs(user, ids, nTopics, fmt.Sprintf("part %d/%d", i+1, len(ws)), logf)
		if err != nil {
			return nil, fmt.Errorf("%s could not decide part %d (%s-%s): %w", m.name, i+1, clock(ss[w[0]].Start), clock(ss[w[1]-1].End), err)
		}
		all = append(all, ds...)
		logf("  %s decided part %d/%d (%s-%s)", m.name, i+1, len(ws), clock(ss[w[0]].Start), clock(ss[w[1]-1].End))
	}
	return all, nil
}

// reviewTargets: the lead's unsure calls, both sides of every keep/cut
// boundary, and every cut within nearKeep sentences of a kept one (where a
// missed line of the topic hides). Boundaries and near-keeps do not depend on
// confidence: Gemma-4 rated 276 of 300 calls exactly 100 on its first live
// run (2026-10-05), so a confidence trigger alone almost never fires.
func reviewTargets(ds []Decision) []int {
	var out []int
	for i, d := range ds {
		boundary := (i > 0 && ds[i-1].Keep != d.Keep) || (i+1 < len(ds) && ds[i+1].Keep != d.Keep)
		near := false
		for j := max(0, i-nearKeep); !d.Keep && j <= min(len(ds)-1, i+nearKeep); j++ {
			near = near || ds[j].Keep
		}
		if d.Confidence < sureConfidence || boundary || near {
			out = append(out, i)
		}
	}
	return out
}

// review asks the second model to decide the targets independently, window by
// window, with the whole window shown for context.
func (m localModel) review(ss []Sentence, ws [][2]int, brief string, nTopics int, targets []int, logf func(string, ...any)) (map[int]Review, error) {
	want := map[int]bool{}
	for _, t := range targets {
		want[t] = true
	}
	out := map[int]Review{}
	for i, w := range ws {
		var ids []int
		for j := w[0]; j < w[1]; j++ {
			if want[j] {
				ids = append(ids, ss[j].ID)
			}
		}
		if len(ids) == 0 {
			continue
		}
		idText := make([]string, len(ids))
		for k, id := range ids {
			idText[k] = fmt.Sprint(id)
		}
		user := brief + "THIS PART OF THE STREAM (context):\n" + sentenceLines(ss, w[0], w[1]) +
			"\nDECIDE ONLY THESE SENTENCE IDS, in this order: " + strings.Join(idText, ", ")
		ds, err := m.decideIDs(user, ids, nTopics, fmt.Sprintf("review part %d/%d", i+1, len(ws)), logf)
		if err != nil {
			return out, fmt.Errorf("%s could not review part %d: %w", m.name, i+1, err)
		}
		for _, d := range ds {
			out[d.ID] = Review{Model: m.name, Label: d.Label, Topic: d.Topic, Keep: d.Keep, Confidence: d.Confidence}
		}
		logf("  %s reviewed %d call(s) in part %d/%d", m.name, len(ids), i+1, len(ws))
	}
	return out, nil
}

// conclude applies the reviews and the unsure rule. Every sentence ends up
// keep/cut; Unsure marks the ones that need Jordan's eyes.
//
// Rules version 2 (2026-10-06, the 27-livestream review): becky's own rules had
// overruled Claude twice. "Oh my yeah, I exist" (Claude: chat reply, cut, 60%)
// was kept by the unsure rule - Jordan: "'Yeah I exist' is just me responding to
// chat". "Yes, so there we go" (Claude: keep, 65%) was cut by the stray-fragment
// rule, and with it the end of his toast after "cheers, water cheers". So an
// unsure chat reply the model itself said to cut stays cut (with a marker at the
// cut), and the stray-fragment rule only cuts what the model said to cut.
func conclude(ds []Decision, reviews map[int]Review, lead string) []Decision {
	out := make([]Decision, len(ds))
	copy(out, ds)
	for i := range out {
		d := &out[i]
		d.Said = d.Keep
		if r, ok := reviews[d.ID]; ok {
			rv := r
			d.Review = &rv
			if r.Keep == d.Keep {
				d.Confidence = max(d.Confidence, r.Confidence)
				continue
			}
			d.Unsure = true
			d.Note = fmt.Sprintf("%s said %s (%s), %s said %s (%s)",
				lead, verdict(d.Keep), why(d.Topic, d.Label), r.Model, verdict(r.Keep), why(r.Topic, r.Label))
		} else if d.Confidence < sureConfidence {
			d.Unsure = true
			if d.Note == "" {
				d.Note = fmt.Sprintf("%s was unsure (%d%%) whether to %s it", lead, d.Confidence, verdict(d.Keep))
			}
		} else {
			continue
		}
		if chatCut(*d) {
			d.Note += " - cut: a chat reply, as " + lead + " said"
			continue
		}
		d.Keep = true
	}
	// An unsure sentence far from anything confidently kept would be a stray
	// fragment on the timeline: cut it, but say so in the report - unless the
	// model itself said to keep it.
	sure := make([]bool, len(out))
	for i, d := range out {
		sure[i] = d.Keep && !d.Unsure
	}
	for i := range out {
		if !out[i].Unsure || !out[i].Keep || out[i].Said {
			continue
		}
		near := false
		for j := max(0, i-isolatedReach); j <= min(len(out)-1, i+isolatedReach); j++ {
			if sure[j] {
				near = true
				break
			}
		}
		if !near {
			out[i].Keep = false
			out[i].Note += " - cut: nothing confidently kept nearby"
		}
	}
	return out
}

// chatCut: an unsure chat reply or super chat that the lead model itself said
// to cut - it stays cut.
func chatCut(d Decision) bool {
	return d.Unsure && !d.Said && (d.Label == "chat_reply" || d.Label == "super_chat")
}

func verdict(keep bool) string {
	if keep {
		return "keep"
	}
	return "cut"
}

// runLocal is the Gemma/Qwen path: lead decides, the other model reviews.
func runLocal(lead, reviewer localModelSpec, ss []Sentence, guidance string, logf func(string, ...any)) (Selection, error) {
	start := time.Now()
	sel := Selection{Model: lead.name, Reviewer: reviewer.name, Guidance: guidance}
	ws := windows(ss)

	lm, err := lead.open()
	if err != nil {
		return sel, err
	}
	if sel.Topics, err = lm.wantedTopics(guidance); err != nil {
		sel.Topics = []string{guidance}
		sel.Notes = append(sel.Notes, fmt.Sprintf("%s could not list the wanted topics (%v), so the whole guidance was one topic", lead.name, err))
	}
	for i, t := range sel.Topics {
		logf("%s: wanted topic %d: %s", lead.name, i+1, t)
	}
	logf("%s: outlining the stream (%d parts)...", lead.name, len(ws))
	sel.Outline = lm.outline(ss, ws, logf)
	brief := "GUIDANCE: " + guidance + "\n\n" + wantedText(sel.Topics) + outlineText(sel.Outline)
	logf("%s: deciding %d sentences...", lead.name, len(ss))
	ds, err := lm.decideWindows(ss, ws, brief, len(sel.Topics), logf)
	lm.client.Close()
	if err != nil {
		return sel, err
	}

	targets := reviewTargets(ds)
	reviews := map[int]Review{}
	if len(targets) > 0 {
		logf("%s: reviewing %d of %s's calls...", reviewer.name, len(targets), lead.name)
		rm, rerr := reviewer.open()
		if rerr == nil {
			reviews, rerr = rm.review(ss, ws, brief, len(sel.Topics), targets, logf)
			rm.client.Close()
		}
		if rerr != nil {
			sel.Notes = append(sel.Notes, fmt.Sprintf("the review by %s did not finish (%v); %s's unsure calls are marked unsure instead", reviewer.name, rerr, lead.name))
			logf("  review incomplete: %v", rerr)
		}
	}
	sel.Decisions, sel.Rules = conclude(ds, reviews, lead.name), rulesVersion
	sel.Seconds = time.Since(start).Seconds()
	return sel, nil
}

// localModelSpec is how to start a local model.
type localModelSpec struct {
	name, model, server string
}

func (s localModelSpec) open() (localModel, error) {
	c := llmlocal.NewWarmClientCtx(s.model, s.server, localCtx, func(f string, a ...any) {})
	if err := c.Available(); err != nil {
		return localModel{}, fmt.Errorf("%s is not available: %w", s.name, err)
	}
	return localModel{name: s.name, client: c}, nil
}
