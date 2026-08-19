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

	// 7. The filter chain crops THEN scales, and uses the smallest rect so the
	//    crop size stays constant while x/y animate.
	chain := crop.FilterChain([]crop.Rect{
		{T: 0, X: 0, Y: 0, W: 800, H: 1422},
		{T: 1, X: 10, Y: 0, W: 766, H: 1362},
	}, 1080, 1920)
	check("chain crops before it scales",
		strings.Index(chain, "crop=") < strings.Index(chain, "scale="), chain)
	check("chain uses the smallest rect so the crop size is constant",
		strings.HasPrefix(chain, "crop=766:1362:"), chain)
	check("chain scales to the requested output size",
		strings.Contains(chain, "scale=1080:1920"), chain)

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

	// 9. Coverage is a MEASURE the caller can refuse on, not a boolean.
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
