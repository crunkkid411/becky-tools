package main

// effects.go - the AI effect list (openshorts rule 10: "the model picks WHAT
// and WHEN, from a closed list; code builds it with hard limits"). Gemma-4 12B
// reads the short line by line and names the lines that earn an effect; code
// times it, sizes it and spaces it. When the model cannot run, the loudness
// beats decide the punch-ins instead (beats in frame.go).

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"slices"
	"strings"
	"time"

	"becky-go/internal/config"
	"becky-go/internal/llmlocal"
)

// The closed list. Sizes are Jordan's: his median push-in is 1.27x
// (habits.md); a face zoom is the livestream edit's 2.0x in 4 frames ("amazing!
// That's a breakthrough!", 2026-10-08).
const (
	fxPunch    = "punch_in"
	fxFaceZoom = "face_zoom"

	fxZoomScale = 2.0
	fxMinGap    = 8.0  // seconds between two effects
	fxPerSec    = 12.0 // at most one effect per this many seconds of short
	fxZoomRamp  = 4    // frames
	fxZoomMax   = 3.0  // a face zoom holds at most this long
)

type effect struct {
	T    float64 `json:"t"` // seconds into the short
	Len  float64 `json:"len"`
	Kind string  `json:"kind"`
	Line string  `json:"line"`
	Why  string  `json:"why,omitempty"`
}

const effectSystem = `You are the editor of a vertical short (TikTok / Reels / Shorts). You choose where on-screen effects go.
The ONLY effects:
- punch_in: a quick, small push-in on the speaker, for a line said with emphasis - a strong claim, a reveal, the point of the story, a callout.
- face_zoom: a big, fast zoom onto the speaker's face, for a reaction, a punchline, disbelief, or something shocking or absurd.
Most lines get NO effect. An effect on an ordinary line looks like a twitch. Choose only lines that clearly earn one.
Answer with the line numbers and the effect for each, and a few words why.`

// pickEffects asks the model which lines earn an effect. ok is false when the
// model could not run (the caller falls back to the loudness beats).
func pickEffects(c *llmlocal.Client, lines []segment, shortStart, dur float64) ([]effect, bool) {
	if c == nil || len(lines) == 0 {
		return nil, false
	}
	limit := max(1, int(dur/fxPerSec))
	var b strings.Builder
	fmt.Fprintf(&b, "The short is %.0f seconds long. Choose at most %d effects.\n\n", dur, limit)
	for i, l := range lines {
		fmt.Fprintf(&b, "%d. [%.1fs] %s\n", i+1, l.Start-shortStart, l.Text)
	}
	schema := map[string]any{"type": "json_schema", "json_schema": map[string]any{"name": "effects", "schema": map[string]any{
		"type": "object", "required": []string{"effects"},
		"properties": map[string]any{"effects": map[string]any{"type": "array", "items": map[string]any{
			"type": "object", "required": []string{"line", "effect", "why"},
			"properties": map[string]any{
				"line":   map[string]any{"type": "integer"},
				"effect": map[string]any{"type": "string", "enum": []string{fxPunch, fxFaceZoom}},
				"why":    map[string]any{"type": "string"},
			}}}}}}}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	out, err := c.Chat(ctx, effectSystem, b.String(), llmlocal.Options{MaxTokens: 600, ResponseFormat: schema})
	if err != nil {
		return nil, false
	}
	var ans struct {
		Effects []struct {
			Line   int    `json:"line"`
			Effect string `json:"effect"`
			Why    string `json:"why"`
		} `json:"effects"`
	}
	if json.Unmarshal([]byte(out), &ans) != nil {
		return nil, false
	}
	var picks []effect
	for _, e := range ans.Effects {
		if e.Line < 1 || e.Line > len(lines) || (e.Effect != fxPunch && e.Effect != fxFaceZoom) {
			continue
		}
		l := lines[e.Line-1]
		picks = append(picks, effect{T: l.Start - shortStart, Len: l.End - l.Start, Kind: e.Effect, Line: l.Text, Why: e.Why})
	}
	return limitEffects(picks, dur, limit), true
}

// limitEffects is the code side of the contract: none in the first second (the
// hook is reading) or the last two, at least fxMinGap apart (the model's order
// decides who wins a clash), at most limit, then back in time order. Lengths:
// a punch-in is fast and held (openshorts rise 0.25 s, hold 1.3 s); a face zoom
// holds for its line, at most fxZoomMax.
func limitEffects(picks []effect, dur float64, limit int) []effect {
	var kept []effect
	for _, p := range picks {
		switch p.Kind {
		case fxPunch:
			p.Len = punchRise + punchHold
		case fxFaceZoom:
			p.Len = math.Min(math.Max(p.Len, 1), fxZoomMax)
		}
		if p.T < 1 || p.T+p.Len > dur-2 || len(kept) >= limit {
			continue
		}
		if slices.ContainsFunc(kept, func(k effect) bool { return math.Abs(k.T-p.T) < fxMinGap }) {
			continue
		}
		kept = append(kept, p)
	}
	slices.SortFunc(kept, func(a, b effect) int { return int(math.Copysign(1, a.T-b.T)) })
	return kept
}

// effectModel starts the warm Gemma-4 12B (else the E4B) once for all shorts;
// nil when no model can start (the beats decide then).
func effectModel(cfg config.Config) *llmlocal.Client {
	model := cfg.GemmaModel12B
	if _, err := os.Stat(model); err != nil {
		model = cfg.GemmaModel
	}
	if model == "" {
		return nil
	}
	c := llmlocal.NewWarmClientCtx(model, cfg.LlamaServer, 8192, nil)
	if c.Available() != nil {
		return nil
	}
	return c
}
