package subs

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"
)

// reviewConcurrency is how many review batches are in flight at once. Batches
// are independent; running them serially left minutes of dead air.
const reviewConcurrency = 4

// DefaultReviewBatch is how many cuts go in one review call. Kept SMALL on
// purpose: at 24 the reply overran the response budget and came back truncated
// or empty ("unexpected end of JSON input"), silently dropping whole ranges of
// cuts back to pacing-only chunking.
const DefaultReviewBatch = 8

// Pass 2 — the LLM chunk review.
//
// Pass 1 (ChunkWords) breaks purely on pause length and line length, so it
// happily cuts mid-phrase: "can" / "you post" / "ten times a day? yeah". The
// timing is frame-correct but the GROUPING is wrong, and wrong grouping is what
// makes captions read as broken. cli-cut fixed this with a second pass
// (SKILL.md: "Merge syntactically incomplete fragments (a dangling 'that',
// 'and', 'the'). Split at strong clause boundaries where the pause was
// suppressed by the 22-char limit. Hard cap: 22 chars.") and it is load-bearing,
// not a nicety.
//
// The model only ever REGROUPS word indices. It never returns text, so it cannot
// alter a word, invent one, or corrupt a timing — the timings stay exactly what
// the ASR measured. Every response is validated to be a partition of the input
// indices, in order; anything else falls back to pass 1.

// ModelFunc runs one prompt and returns the model's raw text.
type ModelFunc func(ctx context.Context, prompt string) (string, error)

// segPlan is one segment's worth of work sent to the model.
type segPlan struct {
	Index int      `json:"segment"`
	Words []string `json:"words"`
	// Pauses[i] is the gap in ms BEFORE Words[i] (0 for the first word). The
	// model needs the pacing to decide where a break is natural.
	Pauses []int `json:"pause_ms_before"`
}

// segReply is the model's answer for one segment: groups of word indices.
type segReply struct {
	Index  int     `json:"segment"`
	Groups [][]int `json:"groups"`
}

// PlanChunks runs pass 1, then asks the model to regroup each segment. Segments
// are batched so a 90-cut edit is a handful of calls, not ninety.
//
// Degrade-never-crash: any model, transport, parsing or validation failure falls
// back to that batch's pass-1 chunks and is reported in the returned warnings.
// Captions are always produced.
func PlanChunks(ctx context.Context, model ModelFunc, segments []Segment, opt Options, batchSize int) ([][][]Word, []string) {
	if batchSize <= 0 {
		batchSize = DefaultReviewBatch
	}

	// Pass 1 first — it is both the input to the review and the fallback.
	pass1 := make([][][]Word, len(segments))
	inRange := WordsPerSegment(segments)
	for i := range segments {
		pass1[i] = Pass1Chunks(inRange[i], opt.MaxChars, opt.GapSeconds)
	}
	if model == nil {
		return pass1, nil
	}

	out := make([][][]Word, len(segments))
	copy(out, pass1)

	// Batches are INDEPENDENT, so they run concurrently. Sequentially this was
	// minutes of dead air; a small pool turns it into roughly one batch's worth
	// of waiting. The cap is modest because the far side rate-limits.
	var (
		mu       sync.Mutex
		warnings []string
		wg       sync.WaitGroup
	)
	sem := make(chan struct{}, reviewConcurrency)

	for start := 0; start < len(segments); start += batchSize {
		end := start + batchSize
		if end > len(segments) {
			end = len(segments)
		}

		var plans []segPlan
		for i := start; i < end; i++ {
			if len(inRange[i]) == 0 {
				continue
			}
			plans = append(plans, buildSegPlan(i, inRange[i]))
		}
		if len(plans) == 0 {
			continue
		}

		wg.Add(1)
		go func(start, end int, plans []segPlan) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			replies, err := reviewBatch(ctx, model, plans, opt.MaxChars)
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				warnings = append(warnings, fmt.Sprintf("caption review (cuts %d-%d) fell back to pacing-only: %v", start+1, end, err))
				return
			}
			for _, r := range replies {
				if r.Index < start || r.Index >= end {
					continue
				}
				groups, err := applyGroups(inRange[r.Index], r.Groups)
				if err != nil {
					warnings = append(warnings, fmt.Sprintf("cut %d fell back to pacing-only: %v", r.Index+1, err))
					continue
				}
				// The model is asked for phrase integrity but is not consistent
				// about it, so the rule is enforced deterministically on its
				// output too.
				out[r.Index] = RepairModelGroups(groups, opt.MaxChars, opt.GapSeconds)
			}
		}(start, end, plans)
	}
	wg.Wait()

	sort.Strings(warnings)
	return out, warnings
}

func buildSegPlan(index int, words []Word) segPlan {
	p := segPlan{Index: index, Words: make([]string, len(words)), Pauses: make([]int, len(words))}
	for i, w := range words {
		p.Words[i] = strings.TrimSpace(w.Word)
		if i > 0 {
			gap := w.Start - words[i-1].End
			if gap < 0 {
				gap = 0
			}
			p.Pauses[i] = int(gap*1000 + 0.5)
		}
	}
	return p
}

// applyGroups turns the model's index groups back into word chunks, after
// proving the groups are a strict in-order partition of the words. A model that
// drops, duplicates or reorders a word is rejected outright rather than silently
// producing captions that do not match the audio.
func applyGroups(words []Word, groups [][]int) ([][]Word, error) {
	if len(groups) == 0 {
		return nil, fmt.Errorf("no groups returned")
	}
	next := 0
	out := make([][]Word, 0, len(groups))
	for _, g := range groups {
		if len(g) == 0 {
			return nil, fmt.Errorf("empty group")
		}
		chunk := make([]Word, 0, len(g))
		for _, idx := range g {
			if idx != next {
				return nil, fmt.Errorf("groups are not an in-order partition (expected index %d, got %d)", next, idx)
			}
			if idx < 0 || idx >= len(words) {
				return nil, fmt.Errorf("index %d out of range", idx)
			}
			chunk = append(chunk, words[idx])
			next++
		}
		out = append(out, chunk)
	}
	if next != len(words) {
		return nil, fmt.Errorf("groups cover %d of %d words", next, len(words))
	}
	return out, nil
}

const reviewSystem = `You group words into short burned-in video captions (TikTok style).

You are given segments. Each segment is one CUT of an edit, with its words in
order and the pause in milliseconds before each word.

THE ONE RULE THAT MATTERS: never break a phrase in the middle. A line that ends
on a word belonging to the next line's phrase is WRONG, even if it fits.

  RIGHT:  "can you post" | "ten times a day?"
  RIGHT:  "can you post" | "ten times" | "a day?"     (when it must be shorter)
  WRONG:  "can you post ten" | "times a day?"         ("ten" belongs with "times")

A number stays with its unit: "ten times", "27 times", "twenty-seven times" -
NEVER "...27" | "times...". The same holds for an article, adjective,
preposition or auxiliary and the word it governs: "a day", "the platforms",
"more successful", "to grow".

A SHORTER line that is phrase-complete always beats a longer line that splits a
phrase. Do NOT pad a line toward the character cap.

Also:
- A line must never END on: a number, "that", "and", "the", "of", "to", "a",
  "an", "is", "it", "you", "but", "so", "in", "on", "my", "we", "i", "for",
  "with", "at", "your", "this", "very", "really", "just", "more", "most".
- SPLIT at clause boundaries (after a complete thought, before a conjunction
  starting a new clause, after a question mark) even when the pause is short.
- Prefer breaking where the speaker actually paused (large pause_ms_before) -
  the lines should follow their cadence.
- HARD CAP: %d characters per line, counting the spaces between words.
- Keep every word EXACTLY as given, in the original order. Never edit, add, drop,
  reorder or re-punctuate. Question marks and exclamation marks are part of the
  word and stay.

Return ONLY a JSON array, no prose, no markdown fence:
[{"segment":0,"groups":[[0,1,2],[3,4]]},{"segment":1,"groups":[[0,1]]}]

"groups" holds the WORD INDICES for each caption line, in order. Concatenated,
a segment's groups must be exactly 0,1,2,...,n-1 with nothing missing or repeated.`

func reviewBatch(ctx context.Context, model ModelFunc, plans []segPlan, maxChars int) ([]segReply, error) {
	payload, err := json.Marshal(plans)
	if err != nil {
		return nil, err
	}
	// The prompt is Jordan-editable — see prompt.go. A plain text file beats a Go
	// constant when the person who needs to tune it is not a developer.
	tmpl, _ := ReviewPrompt()
	prompt := fmt.Sprintf(tmpl, maxChars) + "\n\nSegments:\n" + string(payload)

	raw, err := model(ctx, prompt)
	if err != nil {
		return nil, err
	}
	replies, err := parseReplies(raw)
	if err != nil {
		return nil, err
	}
	return replies, nil
}

// parseReplies pulls the JSON array out of the model's answer, tolerating a
// markdown fence or a sentence of preamble.
func parseReplies(raw string) ([]segReply, error) {
	s := strings.TrimSpace(raw)
	if i := strings.Index(s, "["); i >= 0 {
		if j := strings.LastIndex(s, "]"); j > i {
			s = s[i : j+1]
		}
	}
	var replies []segReply
	if err := json.Unmarshal([]byte(s), &replies); err != nil {
		return nil, fmt.Errorf("could not read the model's answer as JSON: %w", err)
	}
	if len(replies) == 0 {
		return nil, fmt.Errorf("model returned no segments")
	}
	return replies, nil
}
