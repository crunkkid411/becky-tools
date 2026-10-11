// context.go - who the people in a video CALL each other, from the words.
//
// The voice alone cannot tell two men apart reliably: in hj-clone's line library
// (2026-09-24) other men's voices scored 0.80-0.88 against Jordan's, the same range as
// Jordan himself. What separated people there was the transcript: who gets called by
// name, who introduces whom, who tells his own stories. An LLM read the diarized
// transcript and picked the label; it was accepted only when the voice did not clearly
// point at someone else. Other people's lines called his: 0% (hj-clone TOOL-ISSUES #13).
// This file is that method, run locally (Gemma-4 E4B) inside becky-identify, so it works
// whether 3 people or 100 are enrolled: nobody else needs a voice print to be told apart.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"sort"
	"strings"
	"time"

	"becky-go/internal/beckyio"
	"becky-go/internal/config"
	"becky-go/internal/forensicrun"
	"becky-go/internal/llmlocal"
)

// tLine is one transcript line with its speaker label.
type tLine struct {
	Start, End float64
	Speaker    string
	Text       string
}

// contextPick is what the transcript says about one enrolled person.
type contextPick struct {
	Name       string   `json:"name"`
	Labels     []string `json:"labels"`
	Confidence string   `json:"confidence"` // high | medium | low
	Evidence   string   `json:"evidence"`
	Via        string   `json:"-"` // what decided it: "" = the words, "look", "jordan"
}

func (p contextPick) sure() bool { return p.Confidence == "high" || p.Confidence == "medium" }

// loadTranscriptLines reads a becky-transcribe --diarize JSON's labelled lines (nil when
// the file is not one).
func loadTranscriptLines(path string) []tLine {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	return parseTranscriptLines(b)
}

func parseTranscriptLines(b []byte) []tLine {
	var t struct {
		Segments []struct {
			Start   float64 `json:"start"`
			End     float64 `json:"end"`
			Text    string  `json:"text"`
			Speaker string  `json:"speaker"`
		} `json:"segments"`
	}
	if json.Unmarshal(b, &t) != nil {
		return nil
	}
	var out []tLine
	for _, s := range t.Segments {
		if s.Speaker != "" && strings.TrimSpace(s.Text) != "" {
			out = append(out, tLine{s.Start, s.End, s.Speaker, strings.TrimSpace(s.Text)})
		}
	}
	return out
}

// personHint is one enrolled person the transcript reader looks for.
type personHint struct {
	Name    string
	Aliases []string
	About   string
}

// maxContextChars keeps the prompt inside the local model's window (~8k tokens).
const maxContextChars = 24000

// contextTranscript merges lines into speaker turns and keeps the turns that can say who
// is who: the opening (introductions) and every turn that mentions a person's name, with
// the turn before and after it (pure).
func contextTranscript(lines []tLine, people []personHint) string {
	type turn struct {
		spk  string
		text []string
	}
	var turns []turn
	for _, l := range lines {
		if n := len(turns); n > 0 && turns[n-1].spk == l.Speaker {
			turns[n-1].text = append(turns[n-1].text, l.Text)
		} else {
			turns = append(turns, turn{l.Speaker, []string{l.Text}})
		}
	}
	var words []string
	for _, p := range people {
		for _, n := range append([]string{p.Name}, p.Aliases...) {
			if i := strings.IndexByte(n, '('); i > 0 {
				n = n[:i]
			}
			for _, w := range strings.Fields(n) {
				if len(w) >= 3 {
					words = append(words, regexp.QuoteMeta(strings.ToLower(w)))
				}
			}
		}
	}
	var mention *regexp.Regexp
	if len(words) > 0 {
		mention = regexp.MustCompile(`\b(` + strings.Join(words, "|") + `)\b`)
	}
	keep := map[int]bool{}
	for i, t := range turns {
		if i < 30 {
			keep[i] = true
		}
		if mention != nil && mention.MatchString(strings.ToLower(strings.Join(t.text, " "))) {
			keep[i-1], keep[i], keep[i+1] = true, true, true
		}
	}
	var b strings.Builder
	last := -1
	for i, t := range turns {
		if !keep[i] {
			continue
		}
		if last >= 0 && i > last+1 {
			b.WriteString("...\n")
		}
		line := t.spk + ": " + strings.Join(t.text, " ") + "\n"
		if b.Len()+len(line) > maxContextChars {
			break
		}
		b.WriteString(line)
		last = i
	}
	return b.String()
}

const contextSystem = `You read automatic transcripts and say which speaker label is which person.`

// contextPrompt asks which label(s) each person is (pure).
func contextPrompt(people []personHint, transcript string) string {
	var who strings.Builder
	for _, p := range people {
		fmt.Fprintf(&who, "- %s", p.Name)
		if len(p.Aliases) > 0 {
			fmt.Fprintf(&who, " (also called: %s)", strings.Join(p.Aliases, ", "))
		}
		if p.About != "" {
			fmt.Fprintf(&who, ". %s", p.About)
		}
		who.WriteString("\n")
	}
	return `Below are parts of an automatic transcript of a video ("..." marks skipped parts). Speaker labels
come from automatic speaker separation: one label is usually one person but can mix two, and
speech-to-text mishears words, names included.

People to find:
` + who.String() + `
For each person, which speaker label(s) are them? Use who gets called by name, who introduces whom,
who answers to the name, and who talks about that person's own life. For each name, work out from
the context whether it is said BY that person (introducing themselves) or TO them (talking to or
about them). A last name alone never
identifies anyone (families share it). If the transcript does not show it, give no labels and
confidence "low". Never guess from how someone talks.

Reply with JSON only:
{"people": [{"name": "<person>", "labels": ["SPEAKER_xx"], "confidence": "high|medium|low", "evidence": "<one short quote>"}]}

TRANSCRIPT:
` + transcript
}

// askContext runs the local model over the transcript. Degrades: any failure returns nil
// and a note, and identify falls back to voice + face.
func askContext(cfg config.Config, people []personHint, lines []tLine, verbose bool) ([]contextPick, string) {
	return askContextWith(cfg, cfg.GemmaModel, people, lines, verbose)
}

// askContextWith is askContext on a given model (the 12B rereads when E4B was unsure).
func askContextWith(cfg config.Config, model string, people []personHint, lines []tLine, verbose bool) ([]contextPick, string) {
	if len(people) == 0 || len(lines) == 0 {
		return nil, ""
	}
	if model == "" || !fileExists(model) {
		return nil, "transcript check skipped: the local model is not installed"
	}
	logf := func(f string, a ...any) { beckyio.Logf(verbose, f, a...) }
	c := llmlocal.NewClientCtx(model, cfg.LlamaServer, 16384, logf)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	raw, err := c.Chat(ctx, contextSystem, contextPrompt(people, contextTranscript(lines, people)), llmlocal.Options{MaxTokens: 600})
	if err != nil {
		return nil, "transcript check skipped: " + err.Error()
	}
	beckyio.Logf(verbose, "transcript check answer: %s", raw)
	picks, ok := parseContext(raw)
	if !ok {
		return nil, "transcript check skipped: the model's answer could not be read"
	}
	return picks, ""
}

// parseContext reads the model's JSON, tolerating text around it (pure).
func parseContext(raw string) ([]contextPick, bool) {
	i, j := strings.Index(raw, "{"), strings.LastIndex(raw, "}")
	if i < 0 || j <= i {
		return nil, false
	}
	var v struct {
		People []contextPick `json:"people"`
	}
	if json.Unmarshal([]byte(raw[i:j+1]), &v) != nil {
		return nil, false
	}
	for k := range v.People {
		v.People[k].Confidence = strings.ToLower(strings.TrimSpace(v.People[k].Confidence))
		sort.Strings(v.People[k].Labels)
	}
	return v.People, true
}

// wordsCheck asks the transcript who is who when a video has several speakers and an
// enrolled person might be one of them. The words come from --diarized / the video's
// .transcript.json, or becky-transcribe runs here (no transcript yet). Returns the
// picks and a plain-words note for notes.words.
func wordsCheck(cfg config.Config, input string, kb Knowledge, speakers []speakerAudio, enrolled []enrolledVoice, opts voiceOptions) ([]contextPick, string, []tLine, []personHint) {
	if !opts.context {
		return nil, "", nil, nil
	}
	talkers := 0
	for _, sp := range speakers {
		if sp.embedding != nil && talkSeconds(sp.segments) >= minTalk {
			talkers++
		}
	}
	if talkers < 2 {
		return nil, "", nil, nil
	}
	var people []personHint
	for _, e := range filterCast(enrolled, opts.cast) {
		near := false
		for _, sp := range speakers {
			if sp.embedding != nil && cosine(sp.embedding, e.embedding) >= opts.threshold {
				near = true
			}
		}
		if !near {
			continue
		}
		h := personHint{Name: e.name, Aliases: e.aliases}
		if ent, ok := kb.Entities[e.key]; ok && !strings.HasPrefix(ent.Description, "Enrolled from") {
			h.About = ent.Description
		}
		people = append(people, h)
	}
	if len(people) == 0 {
		return nil, "", nil, nil
	}
	var lines []tLine
	if opts.diarizedPath != "" {
		lines = loadTranscriptLines(opts.diarizedPath)
	}
	if lines == nil {
		if t := siblingTranscript(input); t != "" {
			lines = loadTranscriptLines(t)
		}
	}
	if lines == nil {
		beckyio.Logf(opts.verbose, "several speakers and no transcript: running becky-transcribe --diarize...")
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Hour)
		defer cancel()
		// --forensic=false: becky-transcribe's own name check runs becky-identify, which would
		// land back here and loop forever (it did, 2026-10-11).
		raw, err := forensicrun.RunTool(ctx, "becky-transcribe", input, "--diarize", "--forensic=false")
		if err != nil {
			return nil, "transcript check skipped: becky-transcribe failed: " + err.Error(), nil, people
		}
		if lines = parseTranscriptLines(raw); lines == nil {
			return nil, "transcript check skipped: becky-transcribe gave no speaker-labelled lines", nil, people
		}
	}
	picks, note := askContext(cfg, people, lines, opts.verbose)
	if note != "" {
		return nil, note, lines, people
	}
	var said []string
	for _, p := range picks {
		if len(p.Labels) > 0 {
			said = append(said, fmt.Sprintf("%s = %v (%s: %q)", p.Name, p.Labels, p.Confidence, p.Evidence))
		}
	}
	if len(said) == 0 {
		return picks, "transcript check: the words do not say who is who", lines, people
	}
	return picks, "transcript check: " + strings.Join(said, "; "), lines, people
}
