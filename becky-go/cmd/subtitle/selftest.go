package main

import (
	"fmt"
	"os"
	"strings"

	"becky-go/internal/edl"
	"becky-go/internal/subs"
)

// runSelftest exercises the real caption path end to end with no files, no
// media and no models, and asserts the timing VALUES that make captions stop
// flashing. This is the one-command proof for the cut-snapping contract:
//
//	becky-subtitle --selftest
func runSelftest() {
	fails := 0
	check := func(name string, ok bool, detail string) {
		if ok {
			fmt.Printf("  PASS  %s\n", name)
			return
		}
		fails++
		fmt.Printf("  FAIL  %s: %s\n", name, detail)
	}

	fmt.Println("becky-subtitle selftest")
	fmt.Println()

	// Two cuts taken from different parts of one source. Speech sits INSIDE each
	// cut with dead air at both edges — the exact shape that makes an unsnapped
	// caption blink on late and off early.
	words := []subs.Word{
		{Word: "Ninety", Start: 10.40, End: 10.75},
		{Word: "percent", Start: 10.78, End: 11.20},
		{Word: "of", Start: 11.23, End: 11.32},
		{Word: "what", Start: 11.35, End: 11.55},
		{Word: "it", Start: 11.58, End: 11.68},
		{Word: "does", Start: 11.71, End: 12.00},
		{Word: "is", Start: 12.90, End: 13.05},
		{Word: "wasted.", Start: 13.08, End: 13.60},
		{Word: "Every", Start: 40.30, End: 40.60},
		{Word: "single", Start: 40.63, End: 41.00},
		{Word: "time", Start: 41.03, End: 41.40},
	}
	segs := []subs.Segment{
		{Start: 10.0, End: 14.0, Words: words}, // 4.0s -> output [0,4)
		{Start: 40.0, End: 42.0, Words: words}, // 2.0s -> output [4,6)
	}

	cues := subs.Build(segs, subs.DefaultOptions())
	fmt.Printf("  built %d captions from %d cuts (%.1fs of edit)\n\n", len(cues), len(segs), 6.0)
	for i, c := range cues {
		fmt.Printf("        %d  %s --> %s  %q\n", i+1, subs.SRTTime(c.Start), subs.SRTTime(c.End), c.Text)
	}
	fmt.Println()

	check("captions were produced", len(cues) >= 3,
		fmt.Sprintf("got %d, want at least 3", len(cues)))
	if len(cues) == 0 {
		fmt.Println("\nFAILED")
		os.Exit(1)
	}

	// 1. The first caption starts exactly on the cut — no leading flash.
	check("first caption starts on the cut (0.000)", near(cues[0].Start, 0),
		fmt.Sprintf("starts at %.4f", cues[0].Start))

	// 2. The last caption ends exactly on the edit's end — no trailing flash.
	last := cues[len(cues)-1]
	check("last caption ends on the cut (6.000)", near(last.End, 6.0),
		fmt.Sprintf("ends at %.4f", last.End))

	// 3. No gaps anywhere: every caption runs until the next one starts.
	gapAt := -1
	for i := 0; i < len(cues)-1; i++ {
		if cues[i].End < cues[i+1].Start-1e-6 {
			gapAt = i
			break
		}
	}
	check("no gaps between captions", gapAt < 0,
		fmt.Sprintf("caption %d ends %.4f but %d starts %.4f", gapAt, cues[max(gapAt, 0)].End, gapAt+1, cues[max(gapAt, 0)+1].Start))

	// 4. The cut boundary itself is respected: some caption ends exactly at 4.0
	//    (where cut 1 ends and cut 2 begins) — captions never straddle a cut.
	onBoundary := false
	for _, c := range cues {
		if near(c.End, 4.0) {
			onBoundary = true
		}
	}
	check("a caption ends exactly on the 4.000 cut point", onBoundary,
		"no caption ends on the cut — captions are straddling cuts")

	// 5. Nothing is a flash.
	shortest := 1e9
	for _, c := range cues {
		if d := c.End - c.Start; d < shortest {
			shortest = d
		}
	}
	check("no caption shorter than 0.10s", shortest >= 0.10-1e-6,
		fmt.Sprintf("shortest is %.4fs", shortest))

	// 6. Pace-driven chunking really split on the 0.9s pause before "is wasted".
	check("chunked on the speaker's pause", len(cues) >= 4,
		fmt.Sprintf("only %d captions — the 0.90s pause before \"is\" should have forced a break", len(cues)))

	// 7. The SRT actually serialises.
	var b strings.Builder
	if err := subs.WriteSRT(&b, cues); err != nil {
		check("SRT serialises", false, err.Error())
	} else {
		out := b.String()
		check("SRT serialises with numbered cues", strings.HasPrefix(out, "1\r\n") && strings.Contains(out, " --> "),
			fmt.Sprintf("got %q", first(out, 40)))
	}

	// 8. The burn style is the shipped cli-cut look.
	want := "FontName=ProximaNova-Semibold,FontSize=12,Bold=0," +
		"PrimaryColour=&H00FFFFFF,OutlineColour=&H00000000,BackColour=&H00000000," +
		"BorderStyle=1,Outline=1,Shadow=0,Alignment=2,MarginV=90"
	got := subs.DefaultStyle().ForceStyle()
	check("default style is white text with a black outline", got == want,
		fmt.Sprintf("got %s", got))

	// 9. THE VEGAS PLACEMENT PROOF. The same two cuts, but as a live Vegas
	//    timeline where the second event sits at 10.0 on the ruler instead of
	//    butted at 4.0. Every caption of cut 2 must come back shifted by that
	//    6-second gap — this is the arithmetic that decides whether a caption
	//    lands under the right frame or six seconds early.
	tl := edl.VegasTimeline{
		Version: "1",
		FPS:     30,
		Events: []edl.VegasEvent{
			{Source: `C:\v\a.mp4`, In: 10, Out: 14, Timeline: 0},
			{Source: `C:\v\a.mp4`, In: 40, Out: 42, Timeline: 10},
		},
	}
	doc, _ := buildCues(cues, &tl, 30, `C:\v\a.srt`)
	fmt.Println()
	fmt.Println("  placed on a Vegas timeline whose second cut sits at 10.000:")
	for _, c := range doc.Cues {
		fmt.Printf("        %7.3f -> %7.3f  %q\n", c.Start, c.End, c.Text)
	}
	fmt.Println()

	check("every caption was placed", doc.Count == len(cues),
		fmt.Sprintf("placed %d of %d", doc.Count, len(cues)))
	check("placed cues are on the vegas clock", doc.Base == "vegas-timeline",
		fmt.Sprintf("base is %q", doc.Base))
	check("first caption still starts at 0.000", len(doc.Cues) > 0 && near(doc.Cues[0].Start, 0),
		fmt.Sprintf("starts at %.4f", doc.Cues[0].Start))

	// Cut 1 lives in [0,4) on the ruler; cut 2 in [10,12). NOTHING may land in
	// the 4..10 hole — that is the bug an EDL export would have produced.
	inHole := -1
	for i, c := range doc.Cues {
		if c.Start > 4.0+1e-6 && c.Start < 10.0-1e-6 {
			inHole = i
			break
		}
	}
	check("no caption lands in the timeline's empty 4.000-10.000 gap", inHole < 0,
		fmt.Sprintf("caption %d starts at %.4f, inside the hole", inHole, doc.Cues[max(inHole, 0)].Start))

	lastPlaced := doc.Cues[len(doc.Cues)-1]
	check("last caption ends where the second event ends (12.000)", near(lastPlaced.End, 12.0),
		fmt.Sprintf("ends at %.4f", lastPlaced.End))

	// The total spoken span is unchanged by the move: 6s of captions, just
	// positioned around the gap rather than compressed into it.
	var placedSpan float64
	for _, c := range doc.Cues {
		placedSpan += c.End - c.Start
	}
	check("placed captions still cover 6.000s of speech", near(round3(placedSpan), 6.0),
		fmt.Sprintf("cover %.4fs", placedSpan))

	fmt.Println()
	if fails > 0 {
		fmt.Printf("FAILED (%d)\n", fails)
		os.Exit(1)
	}
	fmt.Println("OK")
}

func near(a, b float64) bool { return a-b < 1e-6 && b-a < 1e-6 }

func first(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
