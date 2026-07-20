package subs

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

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
		batchSize = 24
	}

	// Pass 1 first — it is both the input to the review and the fallback.
	pass1 := make([][][]Word, len(segments))
	inRange := make([][]Word, len(segments))
	for i, seg := range segments {
		inRange[i] = WordsInRange(seg.Words, seg.Start, seg.End)
		pass1[i] = ChunkWords(inRange[i], opt.MaxChars, opt.GapSeconds)
	}
	if model == nil {
		return pass1, nil
	}

	out := make([][][]Word, len(segments))
	copy(out, pass1)
	var warnings []string

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

		replies, err := reviewBatch(ctx, model, plans, opt.MaxChars)
		if err != nil {
			warnings = append(warnings, fmt.Sprintf("caption review (cuts %d-%d) fell back to pacing-only: %v", start+1, end, err))
			continue
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
			out[r.Index] = groups
		}
	}
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

Regroup each segment's words into caption lines:
- MERGE fragments that are syntactically incomplete on their own. A line must
  never end on a dangling "that", "and", "the", "of", "to", "a", "is", "it",
  "you", "but", "so", "in", "on", "my", "we", "i".
- SPLIT at strong clause boundaries (after a complete thought, before a
  conjunction starting a new clause, at a question mark) even when the pause
  there is short.
- Prefer breaking where the speaker actually paused (large pause_ms_before).
- HARD CAP: %d characters per line, counting the spaces between words.
- Keep every word, in the original order. Never edit, add, drop or reorder words.

Return ONLY a JSON array, no prose, no markdown fence:
[{"segment":0,"groups":[[0,1,2],[3,4]]},{"segment":1,"groups":[[0,1]]}]

"groups" holds the WORD INDICES for each caption line, in order. Concatenated,
a segment's groups must be exactly 0,1,2,...,n-1 with nothing missing or repeated.`

func reviewBatch(ctx context.Context, model ModelFunc, plans []segPlan, maxChars int) ([]segReply, error) {
	payload, err := json.Marshal(plans)
	if err != nil {
		return nil, err
	}
	prompt := fmt.Sprintf(reviewSystem, maxChars) + "\n\nSegments:\n" + string(payload)

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
