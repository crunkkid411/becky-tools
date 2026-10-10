// becky-diarfix — a second opinion on WHO said each word, read from the words themselves.
//
//	becky-diarfix <transcript.json> [--output f] [--radius 5] [--verbose]
//
// becky-diarize (Nemotron) decides who speaks from the SOUND only. It is weakest exactly where
// the sound is ambiguous: a word glued to the wrong side of a turn change ("how are you doing |
// today?"), a short "yeah" / "mm-hm" from the listener, a question answered in two words. Those
// are clear from the WORDING. This tool sends those unsure stretches to Google's
// DiarizationLM-Gemma-4-E4B-v1 (a Gemma 4 E4B fine-tuned to correct ASR + diarization output;
// Apache-2.0, arXiv:2401.03506) and keeps its corrections only there.
//
// Input: becky-transcribe JSON whose words carry "speaker" (becky-transcribe --diarize output).
// Output (stdout or --output): {"speakers": one label per input word, "fixes": every word that
// changed, "note": plain words when the check could not run}. becky-transcribe calls this itself
// after the speaker pass and re-cuts its caption lines from the corrected labels; callers never
// need to run it by hand.
//
// What counts as UNSURE (the only words a correction may touch): any word within --radius words
// of a speaker change. A long stretch by one person, far from any change, keeps Nemotron's label
// no matter what — the model card says the model is trained to fix turn boundaries and short
// backchannels (1-5 words) while holding identity across long monologues, so this guard matches
// what it is good at. It never invents a speaker Nemotron did not hear.
//
// Runtime: llama-server (config llama_server) + the Q4_K_M GGUF (config diarlm_model, ~5.3 GB,
// fetched by scripts/get-diarizationlm.ps1). Greedy decoding, fixed seed: same input, same output.
// Degrade, never crash: no model / no server / a bad reply leaves the labels as they were, with a
// note saying why, and exit 0.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"becky-go/internal/beckyio"
	"becky-go/internal/config"
	"becky-go/internal/llmlocal"
)

// modelID names the checker in the output.
const modelID = "DiarizationLM-Gemma-4-E4B-v1"

// promptChars is the model card's training segmentation (4,000 characters per prompt).
const promptChars = 4000

// word is the part of a becky-transcribe word this tool reads.
type word struct {
	Word    string  `json:"word"`
	Start   float64 `json:"start"`
	End     float64 `json:"end"`
	Speaker string  `json:"speaker"`
}

// Fix is one word whose speaker the check changed.
type Fix struct {
	Index int     `json:"index"`
	Start float64 `json:"start"`
	End   float64 `json:"end"`
	Word  string  `json:"word"`
	From  string  `json:"from"`
	To    string  `json:"to"`
}

// Output is the becky-diarfix JSON contract.
type Output struct {
	Model         string   `json:"model"`
	Speakers      []string `json:"speakers"`
	Fixes         []Fix    `json:"fixes"`
	UnsureWords   int      `json:"unsure_words"`
	ChunksSent    int      `json:"chunks_sent"`
	ChunksTrusted int      `json:"chunks_trusted"`
	Note          string   `json:"note,omitempty"`
}

func main() {
	out := flag.String("output", "", "output file (default: stdout)")
	radius := flag.Int("radius", 5, "words either side of a speaker change that the check may relabel")
	verbose := flag.Bool("verbose", false, "show progress on stderr")
	input := parsePositional()
	if input == "" {
		beckyio.Fatalf("usage: becky-diarfix <transcript.json> [--output f] [--radius 5] [--verbose]")
	}
	raw, err := os.ReadFile(input)
	if err != nil {
		beckyio.Fatalf("read %s: %v", input, err)
	}
	var tr struct {
		Words []word `json:"words"`
	}
	if err := json.Unmarshal(raw, &tr); err != nil {
		beckyio.Fatalf("%s is not becky-transcribe JSON: %v", input, err)
	}

	cfg := config.Load()
	logf := func(f string, a ...any) { beckyio.Logf(*verbose, f, a...) }
	client := llmlocal.NewWarmClientCtx(cfg.DiarLMModel, cfg.LlamaServer, 8192, logf)
	defer client.Close()
	complete := func(prompt string, maxTok int) (string, error) {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer cancel()
		return client.Complete(ctx, prompt, maxTok)
	}
	res := check(tr.Words, *radius, client.Available(), complete, logf)

	data, _ := json.MarshalIndent(res, "", "  ")
	if *out == "" {
		fmt.Println(string(data))
		return
	}
	if err := os.WriteFile(*out, data, 0o644); err != nil {
		beckyio.Fatalf("write output: %v", err)
	}
}

// check runs the whole pass. avail is the model/server readiness error (nil = ready); complete
// is the model call (swapped in tests).
func check(words []word, radius int, avail error, complete func(string, int) (string, error), logf func(string, ...any)) Output {
	res := Output{Model: modelID, Speakers: make([]string, len(words)), Fixes: []Fix{}}
	labels := make([]string, len(words))
	for i, w := range words {
		labels[i] = w.Speaker
		res.Speakers[i] = w.Speaker
	}
	if distinct(labels) < 2 {
		res.Note = "only one voice was labelled, so there is nothing to double-check"
		return res
	}
	unsure := unsureWords(labels, radius)
	for _, u := range unsure {
		if u {
			res.UnsureWords++
		}
	}
	if avail != nil {
		res.Note = "speaker double-check skipped (" + avail.Error() + "); labels are from the sound only"
		return res
	}
	text := make([]string, len(words))
	for i, w := range words {
		text[i] = strings.TrimSpace(w.Word)
	}
	for _, ch := range chunk(text, labels, promptChars) {
		if !anyIn(unsure, ch.from, ch.to) {
			continue
		}
		res.ChunksSent++
		hyp := ch.prompt(text)
		logf("becky-diarfix: checking words %d-%d (%d chars)", ch.from, ch.to-1, len(hyp))
		comp, err := complete("<|turn>user\n"+hyp+" --> <turn|>\n<|turn>model\n", len(hyp)/2+64)
		if err != nil {
			res.Note = "speaker double-check stopped early (" + err.Error() + "); the rest keeps the sound-only labels"
			break
		}
		fixed, ok := transfer(comp, text[ch.from:ch.to], ch.local)
		if !ok {
			logf("becky-diarfix: words %d-%d: reply did not match the transcript, ignored", ch.from, ch.to-1)
			continue
		}
		res.ChunksTrusted++
		for k, loc := range fixed {
			i := ch.from + k
			to := ch.global(loc)
			if to == "" || to == labels[i] || !unsure[i] {
				continue
			}
			res.Speakers[i] = to
			res.Fixes = append(res.Fixes, Fix{Index: i, Start: words[i].Start, End: words[i].End, Word: text[i], From: labels[i], To: to})
		}
	}
	return res
}

// unsureWords marks every word within radius words of a speaker change.
func unsureWords(labels []string, radius int) []bool {
	n := len(labels)
	out := make([]bool, n)
	for b := 1; b < n; b++ { // a change sits between b-1 and b
		if labels[b] == labels[b-1] {
			continue
		}
		for i := max(0, b-radius); i < min(n, b+radius); i++ {
			out[i] = true
		}
	}
	return out
}

// span is one prompt-sized run of words [from, to) with its own 1..N speaker numbering
// (the numbering the model was trained on: by order of first appearance in the prompt).
type span struct {
	from, to int
	local    []string          // per word in the span: "1", "2", ...
	back     map[string]string // "1" -> "SPEAKER_03"
}

func (s span) global(local string) string { return s.back[local] }

// prompt renders the span as DiarizationLM input: "<speaker:1> hello there <speaker:2> hi".
func (s span) prompt(text []string) string {
	var b strings.Builder
	prev := ""
	for k, w := range text[s.from:s.to] {
		if s.local[k] != prev {
			if b.Len() > 0 {
				b.WriteByte(' ')
			}
			b.WriteString("<speaker:" + s.local[k] + ">")
			prev = s.local[k]
		}
		b.WriteByte(' ')
		b.WriteString(w)
	}
	return b.String()
}

// chunk cuts the words into prompts of at most maxChars, preferring to cut at a speaker
// change in the back half of a full chunk so a turn is not split mid-sentence.
func chunk(text, labels []string, maxChars int) []span {
	var spans []span
	for from := 0; from < len(text); {
		size, to, lastChange := 0, from, -1
		for to < len(text) {
			add := len(text[to]) + 1
			if to == from || labels[to] != labels[to-1] {
				add += len("<speaker:N> ")
				if to > from {
					lastChange = to
				}
			}
			if size+add > maxChars && to > from {
				break
			}
			size += add
			to++
		}
		if to < len(text) && lastChange > from+(to-from)/2 {
			to = lastChange
		}
		spans = append(spans, newSpan(from, to, labels))
		from = to
	}
	return spans
}

func newSpan(from, to int, labels []string) span {
	s := span{from: from, to: to, back: map[string]string{}}
	num := map[string]string{}
	for _, l := range labels[from:to] {
		if _, ok := num[l]; !ok {
			num[l] = fmt.Sprint(len(num) + 1)
			s.back[num[l]] = l
		}
		s.local = append(s.local, num[l])
	}
	return s
}

func distinct(labels []string) int {
	seen := map[string]bool{}
	for _, l := range labels {
		if l != "" {
			seen[l] = true
		}
	}
	return len(seen)
}

func anyIn(b []bool, from, to int) bool {
	for _, v := range b[from:to] {
		if v {
			return true
		}
	}
	return false
}

// parsePositional allows flags before or after the input path (same as becky-diarize).
func parsePositional() string {
	flag.Parse()
	rest := flag.Args()
	if len(rest) == 0 {
		return ""
	}
	if len(rest) > 1 {
		_ = flag.CommandLine.Parse(rest[1:])
	}
	return rest[0]
}
