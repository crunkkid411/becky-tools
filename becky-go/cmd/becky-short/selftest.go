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

	// 10. Coverage is a MEASURE the caller can refuse on, not a boolean.
	p := crop.Path{Sampled: 100, Found: 55}
	check("coverage reports the fraction of samples with a real detection",
		abs(p.Coverage()-0.55) < 1e-9, fmt.Sprintf("%v", p.Coverage()))
	check("an unsampled window reports zero coverage, not a divide by zero",
		crop.Path{}.Coverage() == 0, "")

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
