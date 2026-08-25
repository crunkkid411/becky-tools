package main

// narrativetrim.go — a second, higher-stakes cut pass, run standalone AFTER
// triage/confident-cuts, when the finished rough cut is still too long to
// hand to Jordan for review. Jordan, 2026-08-25: "86 minutes is too long - i
// REFUSE to human review that until it's less than an hour. build whatever
// the fuck you need" and, minutes later, "you were supposed to implement
// gemma4 8 FUCKING HOURS AGO" — an explicit, urgent demand for an AUTOMATED
// pass, not another human-review tool, and for Gemma-4 to actually do the
// judging rather than a heuristic.
//
// This is NOT confidentcuts.go's job over again. confidentcuts only removes
// spans where NOTHING is there (no confident speaker AND no real words) — it
// cannot touch a rough cut that is too long because it is full of genuine,
// on-topic, but REDUNDANT talking. This pass judges narrative content itself:
// a point already made, a tangent, a false start redone later. Because that
// is a real editorial call on a real criminal-case video, every verdict is
// Gemma-4's, never a heuristic, and the prompt is written to fail closed
// (cut:false) on anything it is not confident about — see
// narrativeTrimSystemPrompt. Every cut is logged to narrative_trim.json with
// the exact text removed and the model's own reason, so nothing is silent
// and everything is reviewable/restorable.
//
// Runs on the ALREADY-SPLICED vegas_cut.json (events+quotes+markers+regions),
// same standalone shape as triage.go's --triage-markers and --vegas-only:
// read the artifact, judge, rewrite it in place. Quotes are NEVER a cut
// candidate — they are the verified, on-camera evidentiary clips Jordan
// deliberately placed; only the main narration track (events) is ever
// touched. See applyNarrativeCuts for how removing events reflows quotes,
// markers, and regions onto one consistent shorter timeline.

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"becky-go/internal/beckyio"
	"becky-go/internal/config"
	"becky-go/internal/llmlocal"
)

const (
	// narrativeChunkGapBreakSec: tlEvent.Dialogue is a ROLLING caption window
	// that grows word-by-word across several consecutive events (measured on
	// real output, 2026-08-25: the same sentence repeated with one more word
	// each time) rather than one clean phrase per event. A silence this long
	// between two events always starts a new chunk even if the text would
	// otherwise look like a continuation.
	narrativeChunkGapBreakSec = 0.75
	// narrativeBeatCapSec bounds how much story-time one LLM verdict
	// controls — never merge so much into one beat that a single cut:true
	// silently removes several unrelated points at once.
	narrativeBeatCapSec = 30.0
	// narrativeBeatGapBreakSec: a gap this long between chunks always starts
	// a new beat, even under the duration cap.
	narrativeBeatGapBreakSec = 2.0
	// narrativeBatchSize mirrors becky-moment/local.go's localBatchSize — a
	// 4B local model holds a per-line JSON format more reliably over fewer
	// items per call.
	narrativeBatchSize = 8
	// narrativeCtxLen: pure text, no image/audio tokens, so this is sized off
	// becky-moment's localCtxLen (8192, proven for a similar batched-verdict
	// task) rather than avlm's ~16384 image-token budget — plenty of room for
	// 8 beats' transcript text plus the rubric plus the reply.
	narrativeCtxLen = 8192
)

// capChunk is one deduped caption chunk — a clean piece of spoken text plus
// every original event index it covers, so a later cut decision still maps
// back to the exact events to remove.
type capChunk struct {
	Text     string
	TLStart  float64
	TLEnd    float64
	EventIdx []int
}

// dedupeCaptionChunks collapses tlEvent's rolling caption windows into clean
// chunks. events must already be TL-ascending (true of vegas_cut.json as
// written — splice.go's place() lays them out with a monotonic cursor).
func dedupeCaptionChunks(events []tlEvent) []capChunk {
	var chunks []capChunk
	var cur *capChunk
	for i, e := range events {
		dur := e.Out - e.In
		grows := cur != nil && e.Dialogue != "" && strings.HasPrefix(e.Dialogue, cur.Text)
		gap := 0.0
		if cur != nil {
			gap = e.TL - cur.TLEnd
		}
		if cur == nil || !grows || gap > narrativeChunkGapBreakSec {
			if cur != nil {
				chunks = append(chunks, *cur)
			}
			cur = &capChunk{TLStart: e.TL}
		}
		cur.TLEnd = e.TL + dur
		cur.EventIdx = append(cur.EventIdx, i)
		if e.Dialogue != "" {
			cur.Text = e.Dialogue
		}
	}
	if cur != nil {
		chunks = append(chunks, *cur)
	}
	return chunks
}

// narrativeBeat is one unit of judgment: a bounded span of sequential,
// already-clean narration text.
type narrativeBeat struct {
	Index    int
	Text     string
	TLStart  float64
	TLEnd    float64
	EventIdx []int
}

// groupChunksIntoBeats merges consecutive caption chunks into beats capped at
// narrativeBeatCapSec, breaking only at chunk boundaries — never mid-sentence.
func groupChunksIntoBeats(chunks []capChunk) []narrativeBeat {
	var beats []narrativeBeat
	var cur *narrativeBeat
	for _, ch := range chunks {
		gap, tooLong := 0.0, false
		if cur != nil {
			gap = ch.TLStart - cur.TLEnd
			tooLong = ch.TLEnd-cur.TLStart > narrativeBeatCapSec
		}
		if cur == nil || gap > narrativeBeatGapBreakSec || tooLong {
			if cur != nil {
				beats = append(beats, *cur)
			}
			cur = &narrativeBeat{TLStart: ch.TLStart}
		}
		cur.TLEnd = ch.TLEnd
		cur.EventIdx = append(cur.EventIdx, ch.EventIdx...)
		if ch.Text != "" {
			if cur.Text != "" {
				cur.Text += " " + ch.Text
			} else {
				cur.Text = ch.Text
			}
		}
	}
	if cur != nil {
		beats = append(beats, *cur)
	}
	for i := range beats {
		beats[i].Index = i
	}
	return beats
}

// narrativeVerdict is Gemma-4's read on one beat.
type narrativeVerdict struct {
	Index  int    `json:"index"`
	Cut    bool   `json:"cut"`
	Reason string `json:"reason"`
}

const narrativeTrimSystemPrompt = "You are a documentary editor's assistant working on a real criminal-case " +
	"recap video, most likely a stalking or harassment case. An earlier automated pass already removed " +
	"confirmed dead air. What remains is spoken narrative that must come down in length - by cutting " +
	"REDUNDANT, TANGENTIAL, OR LOW-VALUE content only. Never a unique fact, name, date, location, or " +
	"accusation. IMPORTANT: in a stalking/harassment case, the same claim or incident being mentioned more " +
	"than once is very often NOT filler - it is how a pattern of behavior or an escalation is established, " +
	"which is part of the evidence. Only call something redundant if it is a near word-for-word restatement " +
	"that adds literally no new detail, emphasis, or escalation versus the first time it was said. Be " +
	"conservative: on this kind of footage most beats should come back cut:false. When in doubt, cut:false."

// narrativeTrimPrompt builds one batch's user prompt. cutSoFar is reported so
// the model has honest running context, but the prompt is explicit that a
// beat is only ever cut on its own merits, never to help hit the number.
func narrativeTrimPrompt(batch []narrativeBeat, totalSec, targetSec, cutSoFar float64) string {
	var b strings.Builder
	fmt.Fprintf(&b, "The full rough cut is currently %.1f minutes and needs to come down toward %.1f "+
		"minutes; %.0fs of cuts have been made so far in this pass. Only mark a beat cut:true when YOU "+
		"are genuinely confident it is safe to remove - never just to hit the target.\n\n"+
		"Below are %d sequential beats from the timeline, IN STORY ORDER, each already-spoken narration. "+
		"For EACH, decide whether it can be cut without losing anything the audience or investigators need.\n\n"+
		"Return ONE JSON object per line, nothing else:\n"+
		"{\"index\":<n>,\"cut\":true|false,\"reason\":\"<one short line>\"}\n\n"+
		"cut:true ONLY for: a point already made elsewhere in the video, a rambling tangent unrelated to "+
		"the case, filler or hesitation with no real content, a false start redone later. cut:false for "+
		"anything introducing a new fact, name, date, location, or accusation, or that you are simply not "+
		"sure about.\n\n", totalSec/60, targetSec/60, cutSoFar, len(batch))
	for _, beat := range batch {
		fmt.Fprintf(&b, "[%d] (%.1fs): %q\n", beat.Index, beat.TLEnd-beat.TLStart, beat.Text)
	}
	return b.String()
}

// parseNarrativeVerdicts tolerantly extracts one JSON object per line,
// stripping anything before the first '{' or after the last '}' on that line
// so stray formatting (markdown fences, a leading dash) does not lose a
// verdict. An unparseable line is silently skipped — its beat stays cut:false
// by construction (fail closed: the caller only acts on verdicts it got).
func parseNarrativeVerdicts(raw string) []narrativeVerdict {
	var out []narrativeVerdict
	for _, line := range strings.Split(raw, "\n") {
		line = strings.TrimSpace(line)
		i := strings.Index(line, "{")
		j := strings.LastIndex(line, "}")
		if i < 0 || j < i {
			continue
		}
		var v narrativeVerdict
		if err := json.Unmarshal([]byte(line[i:j+1]), &v); err == nil {
			out = append(out, v)
		}
	}
	return out
}

// judgeNarrativeBeats runs beats past Gemma-4 in batches, tracking a running
// cutSoFar so later batches see honest progress. It STOPS calling the model
// the moment cutSoFar satisfies the actual deficit (totalSec-targetSec) —
// every remaining beat defaults to cut:false, untouched. This is a hard,
// code-level ceiling, not just a prompt instruction: measured on real
// footage 2026-08-25, a prompt-only version (told the running total, asked
// to stay conservative, never told to stop) cut 167 of 191 beats anyway -
// 86.1min down to 15.5min, over 3x the ~23min actually needed - because nothing
// in the loop stopped it once the target was already long since satisfied.
// The rubric alone cannot be trusted to self-limit; the loop must.
//
// A model that is unavailable returns no verdicts (nothing cut) rather than
// block the run — same degrade-never-crash shape as triageMarkers.
func judgeNarrativeBeats(cfg config.Config, beats []narrativeBeat, totalSec, targetSec float64, verbose bool) []narrativeVerdict {
	logf := func(f string, a ...any) { beckyio.Logf(verbose, f, a...) }
	model, _, _ := cfg.GemmaAVLM()
	probe := llmlocal.NewClientCtx(model, cfg.LlamaServer, narrativeCtxLen, logf)
	if err := probe.Available(); err != nil {
		logf("narrative trim: local model unavailable: %v - no cuts made", err)
		return nil
	}
	c := llmlocal.NewWarmClient(model, cfg.LlamaServer, logf)
	defer c.Close()

	needed := totalSec - targetSec
	if needed < 0 {
		needed = 0
	}

	var verdicts []narrativeVerdict
	cutSoFar := 0.0
	ctx := context.Background()
	for start := 0; start < len(beats); start += narrativeBatchSize {
		if cutSoFar >= needed {
			logf("narrative trim: %.0fs cut already meets the %.0fs needed - stopping, %d beat(s) left untouched",
				cutSoFar, needed, len(beats)-start)
			break
		}
		end := start + narrativeBatchSize
		if end > len(beats) {
			end = len(beats)
		}
		batch := beats[start:end]
		raw, err := c.Chat(ctx, narrativeTrimSystemPrompt,
			narrativeTrimPrompt(batch, totalSec, targetSec, cutSoFar),
			llmlocal.Options{MaxTokens: 100 * len(batch)})
		if err != nil {
			logf("narrative trim batch %d-%d failed: %v - leaving those beats uncut", start, end, err)
			continue
		}
		for _, v := range parseNarrativeVerdicts(raw) {
			if v.Index < start || v.Index >= end {
				continue // ignore an out-of-batch index rather than trust it blind
			}
			verdicts = append(verdicts, v)
			if v.Cut {
				dur := beats[v.Index].TLEnd - beats[v.Index].TLStart
				cutSoFar += dur
				logf("narrative trim [%d] CUT (%.1fs): %s", v.Index, dur, v.Reason)
			}
		}
	}
	return verdicts
}

// applyNarrativeCuts removes every beat verdicts marked cut:true from events,
// then re-lays events+quotes on one shared cursor (quotes are NEVER cut) and
// reflows markers by containment against the removed ranges — the same
// shift-or-drop shape reshiftPendingTL already proved correct for splice's
// own insertions (triage.go); here the shift comes from a removal, not an
// insertion. Regions are rebuilt fresh from the new events, the same way
// spliceLayout itself derives them, rather than shifted — correct by
// construction instead of carrying edge cases across the cut.
func applyNarrativeCuts(events []tlEvent, quotes []quoteOut, markers []markerOut,
	verdicts []narrativeVerdict, beats []narrativeBeat) (newEvents []tlEvent, newQuotes []quoteOut,
	newMarkers []markerOut, newRegions []regionOut, cutLog []map[string]any) {

	cutEvent := map[int]bool{}
	for _, v := range verdicts {
		if !v.Cut || v.Index < 0 || v.Index >= len(beats) {
			continue
		}
		b := beats[v.Index]
		for _, ei := range b.EventIdx {
			cutEvent[ei] = true
		}
		cutLog = append(cutLog, map[string]any{
			"beat_index": v.Index, "tl_start": b.TLStart, "tl_end": b.TLEnd,
			"text": b.Text, "reason": v.Reason,
		})
	}

	type block struct {
		tl, dur float64
		cut     bool
		isEvent bool
		idx     int
	}
	blocks := make([]block, 0, len(events)+len(quotes))
	for i, e := range events {
		blocks = append(blocks, block{e.TL, e.Out - e.In, cutEvent[i], true, i})
	}
	for i, q := range quotes {
		blocks = append(blocks, block{q.TL, q.Out - q.In, false, false, i})
	}
	sort.SliceStable(blocks, func(i, j int) bool { return blocks[i].tl < blocks[j].tl })

	var cutRanges []span
	cursor := 0.0
	for _, bl := range blocks {
		if bl.cut {
			cutRanges = append(cutRanges, span{bl.tl, bl.tl + bl.dur})
			continue
		}
		if bl.isEvent {
			e := events[bl.idx]
			e.TL = cursor
			newEvents = append(newEvents, e)
		} else {
			q := quotes[bl.idx]
			q.TL = cursor
			newQuotes = append(newQuotes, q)
		}
		cursor += bl.dur
	}

	shiftFor := func(t float64) (shift float64, dropped bool) {
		for _, r := range cutRanges {
			if t >= r.Start && t < r.End {
				return 0, true
			}
			if r.Start < t {
				shift += r.End - r.Start
			}
		}
		return shift, false
	}
	for _, m := range markers {
		shift, dropped := shiftFor(m.T)
		if dropped {
			continue
		}
		newMarkers = append(newMarkers, markerOut{m.T - shift, m.Title})
	}

	first := map[string]int{}
	for i, e := range newEvents {
		if _, ok := first[e.Source]; !ok {
			first[e.Source] = i
		}
	}
	for src, i := range first {
		last := i
		for j := i; j < len(newEvents) && newEvents[j].Source == src; j++ {
			last = j
		}
		end := newEvents[last].TL + (newEvents[last].Out - newEvents[last].In)
		newRegions = append(newRegions, regionOut{newEvents[i].TL, end - newEvents[i].TL, baseName(src)})
	}
	sort.SliceStable(newRegions, func(i, j int) bool { return newRegions[i].T < newRegions[j].T })

	return newEvents, newQuotes, newMarkers, newRegions, cutLog
}

// runNarrativeTrimPass is becky-roughcut's --narrative-trim entry point:
// reads the current vegas_cut.json (a finished cut, post triage/confident-
// cuts), judges every remaining beat of narration against targetMinutes, and
// rewrites vegas_cut.json with only the beats Gemma-4 was confident are
// redundant/tangential removed. Writes narrative_trim.json as a full audit
// log so every cut is reviewable and nothing is silent.
func runNarrativeTrimPass(out string, targetMinutes float64, verbose bool) error {
	vb, err := os.ReadFile(filepath.Join(out, "vegas_cut.json"))
	if err != nil {
		return err
	}
	var doc map[string]any
	if err := json.Unmarshal(vb, &doc); err != nil {
		return err
	}
	var typed struct {
		Events  []tlEvent   `json:"events"`
		Quotes  []quoteOut  `json:"quotes"`
		Markers []markerOut `json:"markers"`
	}
	if err := json.Unmarshal(vb, &typed); err != nil {
		return err
	}

	totalSec := 0.0
	for _, e := range typed.Events {
		if end := e.TL + (e.Out - e.In); end > totalSec {
			totalSec = end
		}
	}
	for _, q := range typed.Quotes {
		if end := q.TL + (q.Out - q.In); end > totalSec {
			totalSec = end
		}
	}
	targetSec := targetMinutes * 60

	chunks := dedupeCaptionChunks(typed.Events)
	beats := groupChunksIntoBeats(chunks)

	verdicts := judgeNarrativeBeats(config.Load(), beats, totalSec, targetSec, verbose)
	newEvents, newQuotes, newMarkers, newRegions, cutLog :=
		applyNarrativeCuts(typed.Events, typed.Quotes, typed.Markers, verdicts, beats)

	newTotal := 0.0
	for _, e := range newEvents {
		if end := e.TL + (e.Out - e.In); end > newTotal {
			newTotal = end
		}
	}
	for _, q := range newQuotes {
		if end := q.TL + (q.Out - q.In); end > newTotal {
			newTotal = end
		}
	}

	doc["events"] = newEvents
	doc["quotes"] = newQuotes
	doc["markers"] = newMarkers
	doc["regions"] = newRegions

	nb, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(out, "vegas_cut.json"), nb, 0o644); err != nil {
		return err
	}

	beckyio.Logf(true, "narrative trim: %.1fmin -> %.1fmin (%d beats judged, %d cut, %.1fs removed)",
		totalSec/60, newTotal/60, len(beats), len(cutLog), totalSec-newTotal)

	report, _ := json.MarshalIndent(map[string]any{
		"before_sec":  totalSec,
		"after_sec":   newTotal,
		"target_min":  targetMinutes,
		"beats_total": len(beats),
		"beats_cut":   len(cutLog),
		"cuts":        cutLog,
	}, "", "  ")
	return os.WriteFile(filepath.Join(out, "narrative_trim.json"), report, 0o644)
}
