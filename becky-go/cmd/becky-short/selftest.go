// selftest.go — the one-command offline proof for becky-short.
//
// It needs no video, no model, no GPU and no network: it exercises the
// deterministic half (aspect maths, the ffmpeg expression builder, the honest
// static-crop degrade) and asserts VALUES, not truthiness. That is the thing a
// non-dev can run to know the tool is alive, and the thing CI can run without
// media (STANDARDS-WORKFLOW.md §7's one-command proof).
package main

import (
	"fmt"
	"strings"

	"becky-go/internal/crop"
	"becky-go/internal/facesig"
	"becky-go/internal/facetrack"
	"becky-go/internal/moment"
	"becky-go/internal/subs"
)

func runSelftest() int {
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

	fmt.Println("becky-short selftest (offline: no video, no model, no network)")

	// 1. Aspect parsing, both forms.
	a, err := crop.ParseAspect("9:16")
	check("parses 9:16 as 0.5625", err == nil && abs(a-0.5625) < 1e-9, fmt.Sprintf("got %v err=%v", a, err))
	if _, err := crop.ParseAspect("banana"); err == nil {
		check("refuses a nonsense aspect", false, "no error")
	} else {
		check("refuses a nonsense aspect", true, "")
	}

	// 2. Output size: 1080 on the short edge, both orientations, always even.
	w, h := crop.OutputSize(0.5625)
	check("9:16 renders 1080x1920", w == 1080 && h == 1920, fmt.Sprintf("got %dx%d", w, h))
	w2, h2 := crop.OutputSize(16.0 / 9.0)
	check("16:9 renders 1920x1080", w2 == 1920 && h2 == 1080, fmt.Sprintf("got %dx%d", w2, h2))

	// 3. Static centre crop: centred, inside the frame, EVEN dimensions (odd ones
	//    break yuv420p, which is a silent encoder failure).
	r := crop.StaticCenter(1920, 1080, 0.5625)
	check("static centre crop fits inside the source",
		r.X >= 0 && r.Y >= 0 && r.X+r.W <= 1920 && r.Y+r.H <= 1080,
		fmt.Sprintf("%+v", r))
	check("static centre crop is even in both dimensions",
		r.W%2 == 0 && r.H%2 == 0 && r.X%2 == 0 && r.Y%2 == 0, fmt.Sprintf("%+v", r))
	check("static centre crop of a landscape source is a tall 9:16 slice",
		r.H == 1080 && r.W == 606, fmt.Sprintf("got %dx%d", r.W, r.H))

	// 4. The ffmpeg expression: a HELD value must not emit a ramp. The smoother
	//    deliberately produces long still holds, so if held segments generated
	//    interpolation terms the expression would balloon and the camera would
	//    drift by a pixel between identical samples.
	held := []crop.Rect{{T: 0, X: 100}, {T: 1, X: 100}, {T: 2, X: 100}}
	he := crop.FilterExpr(held, func(r crop.Rect) int { return r.X })
	check("a held path produces no interpolation terms",
		!strings.Contains(he, "(t-"), he)

	// 5. A moving path DOES interpolate, and lands on the right endpoints.
	moving := []crop.Rect{{T: 0, X: 0}, {T: 2, X: 100}}
	me := crop.FilterExpr(moving, func(r crop.Rect) int { return r.X })
	check("a moving path interpolates between samples",
		strings.Contains(me, "(t-0)") && strings.Contains(me, "100"), me)
	check("the expression is a single ffmpeg conditional",
		strings.HasPrefix(me, "if(lt(t,2)"), me)

	// 6. Degenerate inputs do not produce broken ffmpeg syntax.
	check("an empty path yields a literal, not empty text",
		crop.FilterExpr(nil, func(r crop.Rect) int { return r.X }) == "0", "")
	one := crop.FilterExpr([]crop.Rect{{T: 0, X: 42}}, func(r crop.Rect) int { return r.X })
	check("a single sample yields its own value", one == "42", one)

	// 7. The filter chain reads a per-frame command file, crops, THEN scales, and
	//    uses the smallest rect so the crop size is constant while x/y animate.
	path := []crop.Rect{
		{T: 0, X: 0, Y: 0, W: 800, H: 1422},
		{T: 1, X: 10, Y: 0, W: 766, H: 1362},
	}
	chain := crop.FilterChain(path, 1080, 1920, "crop.cmds")
	check("chain reads the per-frame command file before cropping",
		strings.Index(chain, "sendcmd=") < strings.Index(chain, "crop="), chain)
	check("chain crops before it scales",
		strings.Index(chain, "crop=") < strings.Index(chain, "scale="), chain)
	check("chain uses the smallest rect so the crop size is constant",
		strings.Contains(chain, "crop=766:1362:"), chain)
	check("chain scales to the requested output size",
		strings.Contains(chain, "scale=1080:1920"), chain)
	check("the command file is a BARE name (a Windows path breaks sendcmd's parser)",
		strings.Contains(chain, "sendcmd=f=crop.cmds") && !strings.Contains(chain, ":\\"), chain)

	// 7b. REGRESSION: the crop path must NOT be decimated. The previous version
	//     squeezed the whole clip into 48 ffmpeg-expression keyframes, let the
	//     error grow to 64px, and then TRUNCATED - freezing the crop for the rest
	//     of the shot, which looks exactly like the tracker giving up.
	var longMove []crop.Rect
	for i := 0; i < 900; i++ { // 30 seconds at 30 fps
		longMove = append(longMove, crop.Rect{T: float64(i) / 30.0, X: 100 + i, Y: 0, W: 606, H: 1080})
	}
	script := crop.SendcmdFile(longMove)
	check("every distinct position survives to the render (no keyframe cap)",
		strings.Count(script, "crop x ") == 900,
		fmt.Sprintf("%d commands for 900 distinct positions", strings.Count(script, "crop x ")))
	check("the last tracked position is still in the script (no truncation)",
		strings.Contains(script, "crop x 999"), "final position missing")

	// 7c. A held path costs one command, not one per frame.
	var stillCam []crop.Rect
	for i := 0; i < 300; i++ {
		stillCam = append(stillCam, crop.Rect{T: float64(i) / 30.0, X: 500, Y: 0, W: 606, H: 1080})
	}
	check("a still camera emits a single command, not 300",
		strings.Count(crop.SendcmdFile(stillCam), "crop x ") == 1,
		fmt.Sprintf("%d commands", strings.Count(crop.SendcmdFile(stillCam), "crop x ")))

	// 8. Render args: seek BEFORE -i (fast seek), duration as -t, even pixel fmt.
	args := crop.RenderArgs("in.mp4", 12906, 31.2, chain, "out.mp4")
	joined := strings.Join(args, " ")
	ssIdx, iIdx := indexOf(args, "-ss"), indexOf(args, "-i")
	check("seeks before -i (fast seek, as internal/reel does)",
		ssIdx >= 0 && iIdx >= 0 && ssIdx < iIdx, joined)
	check("passes the duration as -t, not an end timestamp",
		indexOf(args, "-t") >= 0 && args[indexOf(args, "-t")+1] == "31.2", joined)
	check("forces yuv420p so the file plays everywhere",
		strings.Contains(joined, "yuv420p"), joined)
	check("writes the output path last", args[len(args)-1] == "out.mp4", joined)

	// 8b. REGRESSION: a real 31s clip sampled at 8fps is 250 points, and one
	//     nested if() per point made ffmpeg reject the filter graph outright —
	//     two of the first four real shorts failed exactly here. The path must be
	//     reduced to significant points before it becomes an expression.
	var long []crop.Rect
	for i := 0; i < 250; i++ {
		x := 100
		if i > 200 { // a long hold, then one deliberate move
			x = 140
		}
		long = append(long, crop.Rect{T: float64(i) * 0.125, X: x, W: 766, H: 1362})
	}
	le := crop.FilterExpr(long, func(r crop.Rect) int { return r.X })
	check("a 250-sample path reduces to a graph ffmpeg will parse",
		strings.Count(le, "if(") <= 48,
		fmt.Sprintf("%d nested ifs", strings.Count(le, "if(")))
	check("reduction keeps the move (start and end values both survive)",
		strings.Contains(le, "100") && strings.Contains(le, "140"), le)

	// 9. Jumpcuts: becky-cut's whole-file keep decisions get intersected with
	//    the short's own window, and the removed time is real (a pacing
	//    decision Jordan can see, not just a shorter file).
	jcCache := newCutCache()
	jcCache.spans["src.mp4"] = []keepSpan{{In: 0, Out: 5}, {In: 8, Out: 20}, {In: 25, Out: 30}}
	plan, jcErr := planJumpcuts(jcCache, job{Src: "src.mp4", In: 4, Out: 27})
	check("jumpcuts intersects becky-cut's decisions with the short's window",
		jcErr == nil && len(plan.Spans) == 3, fmt.Sprintf("plan=%+v err=%v", plan, jcErr))
	check("jumpcuts reports the removed seconds (the pacing decision itself)",
		abs(plan.RemovedSeconds-8.0) < 1e-9, fmt.Sprintf("got %.3fs, want 8s", plan.RemovedSeconds))

	// 9b. REGRESSION: captions must land on the CUT (concatenated) timeline,
	//     not the original continuous window — the exact silent-and-plausible
	//     failure this repo keeps shipping. Two kept spans, 10s of dead air
	//     removed between them: span 2's words must open at output ~5s (right
	//     after span 1's 5s duration), never at ~15s (its position in the
	//     uncut window).
	cues := captionCuesJumpcut([]subs.Word{{Word: "two", Start: 26.0, End: 26.4}}, 10, 28,
		[]keepSpan{{In: 10, Out: 15}, {In: 25, Out: 28}}, 30)
	check("jumpcut captions land on the CUT timeline, not the original window",
		len(cues) == 1 && cues[0].Start > 4.5 && cues[0].Start < 5.5,
		fmt.Sprintf("%+v", cues))

	// 9c. Shot preservation (Part B, research/jordan-edit-reverse-engineered.md
	//     Finding 1+2): an already-edited window's existing cuts become span
	//     boundaries PRESERVED AS-IS, tightened by a small amount at each one —
	//     never re-cut with a raw-footage silence threshold.
	shotPlan := planShotSpans([]float64{4, 7}, nil, job{Src: "src.mp4", In: 0, Out: 10}, 0.2, nil)
	check("existing cuts are preserved as span boundaries, not re-invented",
		len(shotPlan.Spans) == 3 && abs(shotPlan.Spans[0].Out-3.9) < 1e-9 && abs(shotPlan.Spans[1].In-4.1) < 1e-9,
		fmt.Sprintf("%+v", shotPlan.Spans))
	check("Jordan can see the decision: how many cuts were found and preserved",
		shotPlan.ExistingCuts == 2 && shotPlan.PreservedCuts == 2, fmt.Sprintf("%+v", shotPlan))
	check("tightening totals 0.1s per side per cut, not a silence-style re-cut",
		abs(shotPlan.RemovedSeconds-0.4) < 1e-9, fmt.Sprintf("got %.3fs, want 0.4s", shotPlan.RemovedSeconds))

	// 9d. boundaryTighten prefers REAL becky-cut dead air over the flag default
	//     when it finds any right at the cut — "used to tighten", not a magic
	//     number applied blindly everywhere.
	real := boundaryTighten([]keepSpan{{In: 9.9, Out: 10.06}}, 10.0, defaultTighten)
	check("a real becky-cut REMOVE span near the cut overrides the flag default",
		abs(real-0.16) < 1e-9, fmt.Sprintf("got %.4f, want 0.16", real))
	fallback := boundaryTighten([]keepSpan{{In: 100, Out: 101}}, 10.0, defaultTighten)
	check("no dead air nearby falls back to the flag default exactly",
		fallback == defaultTighten, fmt.Sprintf("got %.4f, want %.4f", fallback, defaultTighten))

	// 10. Coverage is a MEASURE the caller can refuse on, not a boolean.
	p := crop.Path{Sampled: 100, Found: 55}
	check("coverage reports the fraction of samples with a real detection",
		abs(p.Coverage()-0.55) < 1e-9, fmt.Sprintf("%v", p.Coverage()))
	check("an unsampled window reports zero coverage, not a divide by zero",
		crop.Path{}.Coverage() == 0, "")

	// 11. --review's pure logic — the self-review pass (CLAUDE.md rule 3): every
	//     one of these is exercised offline, with no video/model, because the
	//     matching/gap/median math is exactly the part a re-watch of the actual
	//     RENDERED file depends on being right.

	// 11a. matchCue relocates a burned cue's words in a fresh transcript even
	//     when its own claimed Start has drifted several seconds away — the
	//     exact shape of "captions timed to the source, not the clip".
	fresh := tokenizeWords([]subs.Word{
		{Word: "so", Start: 10.0}, {Word: "here's", Start: 10.2}, {Word: "the", Start: 10.4},
		{Word: "thing", Start: 10.6}, {Word: "nobody", Start: 10.9},
	})
	shiftedStart, score, found := matchCue(tokenizeText("so here's the thing"), fresh, 4.0)
	check("matchCue relocates a shifted cue by its words, not its claimed time",
		found && score == 4 && abs(shiftedStart-10.0) < 1e-9,
		fmt.Sprintf("start=%v score=%d found=%v", shiftedStart, score, found))
	check("matchCue reports the real offset between claimed and actual time",
		abs((shiftedStart-4.0)-6.0) < 1e-9, fmt.Sprintf("offset=%v, want 6.0", shiftedStart-4.0))

	// 11b. A cue whose words appear NOWHERE in the fresh transcript — the
	//     "captions from elsewhere in the video" failure — is honestly
	//     unmatched, not silently paired with the nearest unrelated word.
	_, _, wrongFound := matchCue(tokenizeText("completely unrelated words"), fresh, 10.0)
	check("matchCue does not force a match when the cue's words are not there",
		!wrongFound, "")

	// 11c. median: both parities, and offsets keep their sign so a report can
	//     say WHICH WAY the captions are wrong.
	check("median of an odd count is the middle value",
		median([]float64{0.1, -0.4, 0.3}) == 0.1, fmt.Sprintf("%v", median([]float64{0.1, -0.4, 0.3})))
	check("median of an even count averages the middle two",
		abs(median([]float64{1.0, 3.0})-2.0) < 1e-9, fmt.Sprintf("%v", median([]float64{1.0, 3.0})))

	// 11d. longestFaceGap: the largest UNCOVERED stretch of the rendered
	//     file, including a gap that runs to the very end — the shape a
	//     tracker "confidently" claims to have followed while the subject
	//     actually left frame for the last several seconds.
	sig := facesig.Signals{OK: true, SamplePeriod: 0.5, Tracks: []facetrack.Track{
		{ID: 1, Detections: []facetrack.Detection{{Time: 0.0}, {Time: 0.5}, {Time: 1.0}}},
		{ID: 2, Detections: []facetrack.Detection{{Time: 5.0}, {Time: 5.5}}},
	}}
	gap, gapStart, gapEnd := longestFaceGap(sig, 10.0)
	check("longestFaceGap finds the largest uncovered stretch, tail included",
		abs(gap-4.5) < 1e-9 && abs(gapStart-5.5) < 1e-9 && abs(gapEnd-10.0) < 1e-9,
		fmt.Sprintf("gap=%.2f [%.2f,%.2f]", gap, gapStart, gapEnd))
	check("an empty face signal reports the WHOLE duration as the gap, not zero",
		func() bool { g, s, e := longestFaceGap(facesig.Signals{}, 8.0); return g == 8 && s == 0 && e == 8 }(),
		"")

	// 11e. internal/moment's ending checks, reused (not re-implemented) via
	//     their exported wrappers — a clip that stops mid-clause must not
	//     read as a clean ending just because it is the last cue on file.
	check("EndsSentence recognises real terminal punctuation",
		moment.EndsSentence("and that was the whole point."), "")
	check("EndsSentence refuses a trail-off",
		!moment.EndsSentence("and then he just kind of..."), "")
	unfinished := []moment.Segment{{Start: 0, End: 3, Text: "so we were driving down the road and"}}
	check("PayoffScore floors a punctuation-less last cue below a real completion",
		moment.PayoffScore(unfinished, 0, 0.6) < 0.85,
		fmt.Sprintf("%.2f", moment.PayoffScore(unfinished, 0, 0.6)))

	// 12. --caption-style=jordan (research/jordan-edit-reverse-engineered.md,
	//     CAPTIONS section): audio-driven emphasis, and proof the DEFAULT
	//     (cli-cut) path is byte-identical to before it existed.

	// 12a. REGRESSION: the subs.BuildWithWords refactor that made the jordan
	//     style possible must not have changed cli-cut's own output at all —
	//     exact values, the same two-cue split this input always produced.
	ccWords := []subs.Word{{Word: "hello", Start: 0.0, End: 0.3}, {Word: "there", Start: 0.32, End: 0.6},
		{Word: "world", Start: 2.0, End: 2.3}}
	ccCues := captionCues(ccWords, 0, 3, 30)
	check("--caption-style=cli-cut output is unchanged: exact cue text and timing",
		len(ccCues) == 2 &&
			ccCues[0].Start == 0 && abs(ccCues[0].End-2.0) < 1e-9 && ccCues[0].Text == "hello there" &&
			abs(ccCues[1].Start-2.0) < 1e-9 && ccCues[1].End == 3 && ccCues[1].Text == "world",
		fmt.Sprintf("%+v", ccCues))

	// 12b. captionFilterASS needs no force_style — an .ass file already carries
	//      its own style and inline colour overrides, unlike captionFilter's
	//      plain .srt (which has nowhere else to put a style).
	assFilter := captionFilterASS("clip.ass")
	check("the jordan style's filter burns the .ass with no force_style",
		strings.HasPrefix(assFilter, "subtitles=") && !strings.Contains(assFilter, "force_style"), assFilter)

	// 12c. THE HIGHLIGHT IS PER WORD, not one audio-chosen word per block.
	//      Jordan, 2026-08-20: "it highlights or animates each word as it is
	//      spoken ... what you provided animates nothing, but chooses random
	//      words to highlight". So a three-word block must produce THREE
	//      Dialogue events, each lighting the word being said at that moment.
	jCues := []subs.CueWords{{
		Cue: subs.Cue{Start: 0, End: 1.5},
		Words: []subs.Word{{Word: "oh", Start: 0.0, End: 0.3}, {Word: "you", Start: 0.4, End: 0.7},
			{Word: "guys", Start: 0.8, End: 1.2}},
	}}
	var assBuf strings.Builder
	assErr := subs.WriteASS(&assBuf, jCues, subs.DefaultJordanStyle(1920, 1080), 1080, 1920)
	assLines := 0
	for _, ln := range strings.Split(assBuf.String(), "\n") {
		if strings.HasPrefix(ln, "Dialogue:") {
			assLines++
		}
	}
	check("a 3-word block emits one highlight event per word",
		assErr == nil && assLines == 3, fmt.Sprintf("err=%v events=%d", assErr, assLines))

	// 12d. The highlight follows the VOICE: word 2 lights at its own start.
	check("the second word lights at its own start time (0:00:00.40)",
		strings.Contains(assBuf.String(), "Dialogue: 0,0:00:00.40,"), assBuf.String())

	// 12e. NEVER THREE LINES. Measured off his own render, the longest line is
	//      14 characters; libass's balanced wrapping put three lines on screen
	//      ("THIS MAKES / ME WANT / TO THROW"), so the break is placed here and
	//      WrapStyle 2 forbids libass from re-breaking it.
	wideBlock := []subs.Word{{Word: "This"}, {Word: "makes"}, {Word: "me"}, {Word: "want"}, {Word: "to"}, {Word: "throw"}}
	line := subs.JordanLinesForTest(wideBlock)
	check("a long block breaks into at most TWO lines, never three",
		strings.Count(line, `\N`) == 1, line)
	check("the file forbids libass from re-wrapping (WrapStyle 2)",
		strings.Contains(assBuf.String(), "WrapStyle: 2"), "")

	// 12g. segmentAt maps an output-timeline moment back to the right SOURCE
	//      span across a multi-span jumpcut short, not just a single window.
	jcSegs := []subs.Segment{{Source: "clip", Start: 0, End: 5}, {Source: "clip", Start: 20, End: 22}}
	seg2, off2, ok2 := segmentAt(jcSegs, 6.0) // 1s into the SECOND span's output range [5,7)
	check("segmentAt finds the second span and its own output offset",
		ok2 && seg2.Start == 20 && off2 == 5, fmt.Sprintf("seg=%+v off=%v ok=%v", seg2, off2, ok2))

	fmt.Printf("\n%d/%d PASS\n", pass, pass+fail)
	if fail > 0 {
		return 1
	}
	return 0
}

func indexOf(ss []string, want string) int {
	for i, s := range ss {
		if s == want {
			return i
		}
	}
	return -1
}

func abs(f float64) float64 {
	if f < 0 {
		return -f
	}
	return f
}
