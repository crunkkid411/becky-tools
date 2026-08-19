package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"becky-go/internal/moment"
	"becky-go/internal/sidecar"
)

// runSelftest is the one-command offline proof required by HANDOFF-TEMPLATE.md:
// it exercises the REAL code path (write a real .srt -> parse it through the real
// sidecar parser -> Find -> Rank -> the real becky-hits record shape) with no
// network, no model, and no media, and asserts VALUES rather than "it ran".
//
// It runs the same functions the CLI runs; if it passes, the deterministic half
// of becky-moment is sound and only the content pass needs a key.
func runSelftest() int {
	dir, err := os.MkdirTemp("", "becky-moment-selftest")
	if err != nil {
		fmt.Fprintf(os.Stderr, "selftest: %v\n", err)
		return 1
	}
	defer os.RemoveAll(dir)

	srtPath := filepath.Join(dir, "fixture.srt")
	if err := os.WriteFile(srtPath, []byte(fixtureSRT), 0o644); err != nil {
		fmt.Fprintf(os.Stderr, "selftest: %v\n", err)
		return 1
	}

	pass, fail := 0, 0
	check := func(name string, ok bool, detail string) {
		if ok {
			pass++
			fmt.Printf("  PASS  %s\n", name)
			return
		}
		fail++
		fmt.Printf("  FAIL  %s — %s\n", name, detail)
	}

	fmt.Println("becky-moment --selftest (offline; no model, no media, no network)")

	// 1. The real sidecar parser reads the real .srt.
	sub, err := sidecar.ParseSubtitle(srtPath)
	check("parses a real .srt through internal/sidecar",
		err == nil && len(sub.Segments) == 8,
		fmt.Sprintf("err=%v segments=%d want 8", err, len(sub.Segments)))

	segs := make([]moment.Segment, 0, len(sub.Segments))
	for _, s := range sub.Segments {
		segs = append(segs, moment.Segment{Start: s.Start, End: s.End, Text: s.Text})
	}

	// 2. The pause threshold is DERIVED, not constant.
	gap := moment.AutoThoughtGap(segs)
	check("thought gap is derived from the transcript's own pauses",
		gap > 0.35 && gap <= 2.5,
		fmt.Sprintf("gap=%.3f, want (0.35, 2.5]", gap))

	// 3. Candidates are found, and every window sits on real cue boundaries.
	cands := moment.Find(segs, moment.Options{MinDuration: 10, MaxDuration: 40, ExtendBudget: 8})
	check("finds at least one moment", len(cands) > 0, fmt.Sprintf("got %d", len(cands)))

	onBoundary := true
	starts, ends := map[float64]bool{}, map[float64]bool{}
	for _, s := range segs {
		starts[s.Start] = true
		ends[s.End] = true
	}
	for _, c := range cands {
		if !starts[c.Start] || !ends[c.End] {
			onBoundary = false
		}
	}
	check("every window starts and ends on a real cue boundary", onBoundary,
		"a window was cut at an interpolated time")

	// 4. Duration bounds are honoured.
	inBounds := true
	for _, c := range cands {
		if c.Dur() < 10-1e-9 || c.Dur() > 48+1e-9 {
			inBounds = false
		}
	}
	check("every window respects --min/--max(+extend)", inBounds, "a window fell outside the bounds")

	// 4b. --top returns DISTINCT moments, not re-cuts of the same one.
	//     Measured on test-for-clips.mp4: the structural top ten was ten windows
	//     over a single 68-second stretch of a five-minute video. Every one was a
	//     well-formed thought, so no score could reject them - they were
	//     individually right and collectively ten renders of one story.
	distinct := moment.Distinct(cands, moment.DefaultMaxOverlap)
	overlapping := 0
	for i := range distinct {
		for j := i + 1; j < len(distinct); j++ {
			if distinct[i].Source == distinct[j].Source &&
				distinct[i].Start < distinct[j].End && distinct[j].Start < distinct[i].End {
				shared := minf(distinct[i].End, distinct[j].End) - maxf(distinct[i].Start, distinct[j].Start)
				shorter := minf(distinct[i].Dur(), distinct[j].Dur())
				if shorter > 0 && shared/shorter > moment.DefaultMaxOverlap {
					overlapping++
				}
			}
		}
	}
	check("no surviving moment is a re-cut of a better one",
		overlapping == 0 && len(distinct) > 0 && len(distinct) <= len(cands),
		fmt.Sprintf("%d of %d kept windows still repeat another", overlapping, len(distinct)))

	// 5. The self-containment signal DISCRIMINATES: a window opening on the
	//    dangling "So he said..." must score below one opening on the clean
	//    declarative hook.
	//
	//    Note the shape of this assertion. It compares two STRUCTURAL openings —
	//    it does not ask which clip is more interesting. An earlier version of
	//    this check asserted that the fixture's "Some unrelated chatter" window
	//    lost to the tax story, and it failed: "Some unrelated chatter before we
	//    begin." is a complete, self-contained sentence at a clean recording
	//    start, so structurally it IS a valid opening. Knowing it is off-topic is
	//    a CONTENT call, and content is the judge's job (see the package doc).
	//    The failing assertion was the bug, not the scorer.
	var clean, dangling moment.Candidate
	for _, c := range cands {
		if strings.HasPrefix(c.Text, "Here is the single biggest mistake") && c.Score > clean.Score {
			clean = c
		}
		if strings.HasPrefix(c.Text, "So he said") && c.Score > dangling.Score {
			dangling = c
		}
	}
	check("a dangling 'So he said...' opening scores below a clean declarative hook",
		clean.Score > 0 && dangling.Score > 0 && clean.Score > dangling.Score,
		fmt.Sprintf("clean=%.3f dangling=%.3f", clean.Score, dangling.Score))
	check("the dangling opener's self-containment signal is penalised",
		dangling.Signals.SelfContained <= 0.5,
		fmt.Sprintf("self_contained=%.2f, want <= 0.50", dangling.Signals.SelfContained))

	// 6. Determinism.
	again := moment.Find(segs, moment.Options{MinDuration: 10, MaxDuration: 40, ExtendBudget: 8})
	same := len(again) == len(cands)
	for i := range again {
		if !same {
			break
		}
		if again[i].Start != cands[i].Start || again[i].End != cands[i].End || again[i].Score != cands[i].Score {
			same = false
		}
	}
	check("same input produces the same output (fixed, no seeds)", same, "a second Find differed")

	// 7. With NO judge, everything is a candidate — never a conclusion. This is
	//    the corroborate-then-conclude rule, proven rather than asserted.
	ranked := moment.Rank(cands, nil)
	allCandidateOnly := len(ranked) == len(cands)
	for _, r := range ranked {
		if r.Confidence != moment.CandidateOnly {
			allCandidateOnly = false
		}
	}
	check("with no content pass, every moment is CANDIDATE (never a conclusion)",
		allCandidateOnly, "a moment was concluded on one signal")

	// 8. A disagreeing judge is HELD at the lower signal, not averaged away.
	disputed := moment.Rank(
		[]moment.Candidate{{Start: 0, End: 20, Score: 0.90}},
		[]moment.Judgement{{Index: 0, Score: 5, Complete: true, Reason: "nothing happens"}})
	check("a disagreeing content verdict is held, not averaged",
		disputed[0].Confidence == moment.Disputed && disputed[0].Final <= 0.06,
		fmt.Sprintf("confidence=%s final=%.3f", disputed[0].Confidence, disputed[0].Final))

	// 9. The becky-hits seam: the emitted record must carry the exact keys
	//    cmd/becky-hits reads. This is the cross-tool boundary that
	//    HANDOFF-SHORTS-PIPELINE.md §3.4 says must be asserted, not assumed.
	h := hit{SRT: "fixture.srt", In: formatTC(5), Out: formatTC(28), Q: "x"}
	check("emits becky-hits' record shape with a parseable timecode",
		h.In == "00:00:05.000" && h.Out == "00:00:28.000",
		fmt.Sprintf("in=%q out=%q", h.In, h.Out))

	// 10. The spending guard: Zen's free models are accepted, everything else is
	//     refused before a request is ever built. One allowlist, no override.
	_, errFree := zenJudge(defaultZenModel, 4)
	_, errPaid := zenJudge("gpt-5.5", 4)
	_, errClaude := zenJudge("claude-opus-5", 4)
	_, errLookalike := zenJudge("deepseek-v4-flash", 4) // metered twin of the default
	check("accepts a free model (stops only at the missing key)",
		errFree != nil && strings.Contains(errFree.Error(), "BECKY_ZEN_API_KEY"),
		fmt.Sprintf("err=%v", errFree))
	check("refuses every model off the free list, Claude included",
		errPaid != nil && errClaude != nil && errLookalike != nil,
		fmt.Sprintf("paid=%v claude=%v lookalike=%v", errPaid != nil, errClaude != nil, errLookalike != nil))

	// 11. Face coverage (HANDOFF-SHORTS-PIPELINE.md §7 item 3): two candidates
	//     with IDENTICAL structural score must reorder on coverage alone — the
	//     off-screen one sinks below the on-screen one, with the exact numbers
	//     pinned so this cannot regress silently.
	faceRanked := moment.Rank([]moment.Candidate{
		{Start: 0, End: 20, Score: 0.95, Face: 0.0, FaceBasis: "face coverage 0.00 (0 sampled sighting(s))"},
		{Start: 30, End: 50, Score: 0.95, Face: 1.0, FaceBasis: "face coverage 1.00 (10 sampled sighting(s))"},
	}, nil)
	wantSecond := 0.95 * 0.35
	check("an off-screen window sinks below an identically-scored on-screen one",
		faceRanked[0].Start == 30 && closeEnough(faceRanked[0].Final, 0.95) && closeEnough(faceRanked[1].Final, wantSecond),
		fmt.Sprintf("top starts=%.0f final=%.4f; second final=%.4f (want top=30 @0.95, second @%.4f)",
			faceRanked[0].Start, faceRanked[0].Final, faceRanked[1].Final, wantSecond))

	fmt.Printf("\n%d/%d PASS\n", pass, pass+fail)
	if fail > 0 {
		return 1
	}
	return 0
}

// fixtureSRT is a synthetic transcript with a deliberate contrast: one clean,
// self-contained story (cues 2-6) and one that opens on a dangling back-reference
// ("So he said..."), so the self-containment signal is proven to discriminate
// rather than merely to compute.
const fixtureSRT = `1
00:00:00,000 --> 00:00:03,000
Some unrelated chatter before we begin.

2
00:00:05,000 --> 00:00:09,000
Here is the single biggest mistake people make with their taxes.

3
00:00:09,200 --> 00:00:14,000
They assume the standard deduction is always the better deal.

4
00:00:14,200 --> 00:00:19,000
They never actually run the numbers on itemising.

5
00:00:19,200 --> 00:00:24,000
I checked mine last year and found four thousand dollars.

6
00:00:24,200 --> 00:00:28,000
Run both every single time, it takes ten minutes.

7
00:00:30,000 --> 00:00:36,000
So he said the whole thing was a write-off anyway

8
00:00:36,200 --> 00:00:44,000
and nobody ever went back to check whether that was true.
`

// closeEnough compares floats within float64 rounding error — Go's constant
// arithmetic (used to compute an expected value in a test) and its runtime
// arithmetic (used inside the real code path) round at different points, so
// an exact == on two independently-derived float64s is the wrong tool.
func closeEnough(a, b float64) bool {
	d := a - b
	if d < 0 {
		d = -d
	}
	return d < 1e-9
}

func minf(a, b float64) float64 {
	if a < b {
		return a
	}
	return b
}

func maxf(a, b float64) float64 {
	if a > b {
		return a
	}
	return b
}
