package main

// watchpass.go — an LLM watches the assembled cut before it ships.
//
// Jordan, 2026-08-24 (the "insufficient contextual data" feedback): becky-clip's
// tools exist "so that becky-roughcut could ALSO use them... these are not the
// solution to everything, they are simply additional data points to help
// provide more corroborative context so that better video editing decisions
// can be made." SKILL.md's VIDEO CLIPPING canon states the law plainly: "AN LLM
// MUST WATCH THE OUTPUT BEFORE IT SHIPS" - becky-roughcut had never done this;
// every decision was word-timing/audio only. This is the first watch pass.
//
// Deliberately a SEPARATE, standalone mode (`--watch`, reads an existing
// vegas_cut.json) rather than baked into the main detection loop: Gemma-4 (via
// llama-server, ~5GB VRAM) cannot run alongside the LR-ASD speaking sweep on
// this machine's 8GB card ("ONE MODEL AT A TIME - a hardware fact", VIDEO
// CLIPPING rule #5) - run this once that sweep is done, or whenever GPU is
// free. It only ever produces a REPORT (watch_report.json): a detector/model is
// a signal, never a verdict - nothing here re-cuts or shortens anything.

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"becky-go/internal/avlm"
	"becky-go/internal/beckyio"
	"becky-go/internal/config"
)

// watchBlock is one contiguous run of kept content on the main track, in
// SOURCE time - the same shape the speaking sweep merges into, so a block
// already has real footage behind it worth watching.
type watchBlock struct {
	Source     string
	Start, End float64
}

// watchVerdict is one block's review result.
type watchVerdict struct {
	Source  string  `json:"source"`
	Start   float64 `json:"start"`
	End     float64 `json:"end"`
	Verdict string  `json:"verdict"` // "pass" | "flag" | "skipped"
	Reason  string  `json:"reason,omitempty"`
}

const (
	watchMergeGap    = 3.0  // seconds; same tolerance the speaking sweep merges on
	watchMinBlockSec = 1.0  // shorter than this has nothing worth watching
	watchWindowSec   = 25.0 // safely under Gemma-4's 30s audio / 60s video caps
)

// mergeWatchBlocks collapses per-source events (already in source time) into
// contiguous blocks, the same way the overnight speaking sweep does, so both
// passes watch the same real stretches of footage rather than disjoint slices.
func mergeWatchBlocks(events []tlEvent) []watchBlock {
	bySource := map[string][]tlEvent{}
	for _, e := range events {
		bySource[e.Source] = append(bySource[e.Source], e)
	}
	var out []watchBlock
	for src, evs := range bySource {
		sort.SliceStable(evs, func(i, j int) bool { return evs[i].In < evs[j].In })
		var cur *watchBlock
		for _, e := range evs {
			if cur != nil && e.In-cur.End <= watchMergeGap {
				if e.Out > cur.End {
					cur.End = e.Out
				}
				continue
			}
			if cur != nil {
				out = append(out, *cur)
			}
			cur = &watchBlock{Source: src, Start: e.In, End: e.Out}
		}
		if cur != nil {
			out = append(out, *cur)
		}
	}
	var kept []watchBlock
	for _, b := range out {
		if b.End-b.Start >= watchMinBlockSec {
			kept = append(kept, b)
		}
	}
	sort.SliceStable(kept, func(i, j int) bool {
		if kept[i].Source != kept[j].Source {
			return kept[i].Source < kept[j].Source
		}
		return kept[i].Start < kept[j].Start
	})
	return kept
}

const watchSystemPrompt = "You are a professional video editor's assistant reviewing a ROUGH CUT " +
	"assembled by an automated tool from a raw video recording. You are watching one short, " +
	"already-selected stretch of it, not the whole project."

const watchPromptTemplate = "This clip was KEPT by an automated silence/pause detector, meaning it " +
	"believes real spoken content is happening here. Watch and listen, then answer in this exact " +
	"format:\n\nVERDICT: PASS or FLAG\nREASON: <one short sentence, only if FLAG>\n\n" +
	"FLAG this clip only if something is clearly wrong for a rough cut - not perfect editing, " +
	"clearly wrong: the person is silently doing something unrelated with no meaningful speech, " +
	"the audio is just room noise or someone off-camera, the footage looks like an obviously " +
	"abandoned take that should have been cut, or nothing resembling content is happening. " +
	"PASS anything that is a real, on-topic moment of him talking or reacting, even if the " +
	"framing or pacing is not perfect - that is not this pass's job."

// watchAssembled runs each merged block past Gemma-4 and returns every
// verdict (including "skipped" when the model is not available, so the
// report is honest about what was and was not actually checked).
func watchAssembled(cfg config.Config, blocks []watchBlock, verbose bool) []watchVerdict {
	model, mmproj, _ := cfg.GemmaAVLM()
	logf := func(format string, a ...any) { beckyio.Logf(verbose, format, a...) }
	runner := avlm.New(model, mmproj, cfg.LlamaServer, "", cfg.FFmpeg, cfg.FFprobe, logf)

	if err := runner.Ready(); err != nil {
		out := make([]watchVerdict, len(blocks))
		for i, b := range blocks {
			out[i] = watchVerdict{Source: baseName(b.Source), Start: b.Start, End: b.End,
				Verdict: "skipped", Reason: "gemma4 not available: " + err.Error()}
		}
		return out
	}

	ctx := context.Background()
	out := make([]watchVerdict, 0, len(blocks))
	for _, b := range blocks {
		win := b.End - b.Start
		if win > watchWindowSec {
			win = watchWindowSec
		}
		res, err := runner.Analyze(ctx, avlm.Options{
			Clip:         b.Source,
			SystemPrompt: watchSystemPrompt,
			Prompt:       watchPromptTemplate,
			WindowStart:  b.Start,
			WindowSec:    win,
			FPS:          1.0,
			MaxTokens:    150,
			Temperature:  0.2,
			Seed:         42,
			Verbose:      verbose,
		})
		v := watchVerdict{Source: baseName(b.Source), Start: b.Start, End: b.End}
		if err != nil {
			v.Verdict, v.Reason = "skipped", err.Error()
		} else {
			v.Verdict, v.Reason = parseWatchVerdict(res.Text)
		}
		out = append(out, v)
		logf("watch %s [%.1f,%.1f]: %s %s", v.Source, v.Start, v.End, v.Verdict, v.Reason)
	}
	return out
}

// parseWatchVerdict reads the model's free-text answer. Tolerant, never an
// XML/strict parser (Reka's mismatched-tag lesson applies to every model
// here): anything that isn't clearly a FLAG is treated as PASS rather than
// silently dropped, because a parse failure is not evidence of a real
// problem, and this pass may only ever ADD a review marker, never remove one.
func parseWatchVerdict(raw string) (verdict, reason string) {
	up := strings.ToUpper(raw)
	if !strings.Contains(up, "FLAG") {
		return "pass", ""
	}
	return "flag", extractReasonField(raw, up)
}

// extractReasonField pulls the text after a "REASON:" label (case-
// insensitive, stops at the line break); falls back to the whole trimmed
// reply, capped, when the model didn't use the label. Shared by every pass
// that asks Gemma-4 for a one-line VERDICT/REASON answer (watchpass.go,
// triage.go).
func extractReasonField(raw, up string) string {
	if idx := strings.Index(up, "REASON:"); idx >= 0 {
		reason := strings.TrimSpace(raw[idx+len("REASON:"):])
		if nl := strings.IndexAny(reason, "\r\n"); nl >= 0 {
			reason = reason[:nl]
		}
		if reason != "" {
			return reason
		}
	}
	reason := strings.TrimSpace(raw)
	if len(reason) > 200 {
		reason = reason[:200]
	}
	return reason
}

// runWatchPass is becky-roughcut's --watch entry point: reads an existing
// vegas_cut.json from a prior detection run and writes watch_report.json
// beside it. Standalone by design - see the file header for why.
func runWatchPass(out string, verbose bool) error {
	b, err := os.ReadFile(filepath.Join(out, "vegas_cut.json"))
	if err != nil {
		return err
	}
	var rc struct {
		Events []tlEvent `json:"events"`
	}
	if err := json.Unmarshal(b, &rc); err != nil {
		return err
	}
	blocks := mergeWatchBlocks(rc.Events)
	beckyio.Logf(true, "%d merged blocks to watch", len(blocks))

	verdicts := watchAssembled(config.Load(), blocks, verbose)
	flagged := 0
	for _, v := range verdicts {
		if v.Verdict == "flag" {
			flagged++
		}
	}
	report, _ := json.MarshalIndent(map[string]any{
		"blocks":  len(verdicts),
		"flagged": flagged,
		"results": verdicts,
	}, "", "  ")
	return os.WriteFile(filepath.Join(out, "watch_report.json"), report, 0o644)
}
