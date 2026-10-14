package main

import (
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// publishRegions: the only things --video puts on Jordan's timeline - a short
// region where something may not be postable (another creator's video, an
// address on screen). Reasons go in the report, never on the timeline.
func publishRegions(findings []Finding, ps []piece) []mark {
	var ms []mark
	for _, f := range findings {
		if !f.Region {
			continue
		}
		if t0, t1, ok := toTimeline(ps, f.Src0, f.Src1); ok {
			ms = append(ms, mark{At: t0, Len: math.Max(t1-t0, 1), Label: short("Check before posting: "+f.Kind, 40)})
		}
	}
	return ms
}

// videoReport writes becky-edit\report-video.md: what was cut besides the
// silences, and why, in plain words.
func (r *run) videoReport(sel Selection, ss []Sentence, restarted []int, words []Word, bc breathResult, findings []Finding, ver *Verification, veg string) {
	var b strings.Builder
	fmt.Fprintf(&b, "# YouTube video edit\n\n**Result:** %.1f of %.1f minutes kept. Saved as `%s`.\n\n", ver.EditSeconds/60, r.duration/60, filepath.Base(veg))
	b.WriteString("Everything you say stays. Taken out: the silences (becky-cut), the breaths, and the lines below. Gemma-4 12B (on this PC) read the video in parts of 40 sentences; the words, not the model, confirm every retake.\n\n")
	writeInstructions(&b, r.instr, ss)

	section := func(title string, match func(Decision) bool) {
		var lines []string
		for _, d := range sel.Decisions {
			if match(d) {
				lines = append(lines, fmt.Sprintf("- %s \"%s\" - %s", clock(ss[d.ID].Start), short(ss[d.ID].Text, 90), d.Note))
			}
		}
		fmt.Fprintf(&b, "## %s (%d)\n\n%s\n\n", title, len(lines), strings.Join(lines, "\n"))
	}
	section("Not said to the viewers", func(d Decision) bool { return !d.Keep && d.Label == "meta" })
	section("Retakes - the attempt you said again", func(d Decision) bool { return !d.Keep && d.Label == "retake" })
	section("Only a hesitation sound", func(d Decision) bool { return !d.Keep && d.Label == "break" })
	section("Your words to the editor", func(d Decision) bool { return !d.Keep && d.Label == "instruction" })
	section("Cut by your instruction", func(d Decision) bool {
		return !d.Keep && d.Label != "meta" && d.Label != "retake" && d.Label != "break" && d.Label != "instruction"
	})
	section("Put back after reading the edit like a viewer", func(d Decision) bool { return d.Keep && strings.HasPrefix(d.Note, "put back") })

	var rs []string
	for i := 0; i < len(restarted); {
		j := i
		for j+1 < len(restarted) && restarted[j+1] == restarted[j]+1 {
			j++
		}
		var ws []string
		for k := restarted[i]; k <= restarted[j]; k++ {
			ws = append(ws, words[k].Word)
		}
		rs = append(rs, fmt.Sprintf("- %s \"%s\"", clock(words[restarted[i]].Start), strings.Join(ws, " ")))
		i = j + 1
	}
	fmt.Fprintf(&b, "## Words you started again inside a sentence (%d)\n\n%s\n\n", len(rs), strings.Join(rs, "\n"))
	r.writeVideoExtra(&b)

	b.WriteString("## Checks of the finished edit\n\n")
	fmt.Fprintf(&b, "- Timeline: %s\n", ver.Timeline)
	if ver.PlannedWords > 0 {
		fmt.Fprintf(&b, "- Re-transcribed: %d of the %d planned words heard again (%.1f%%)\n", ver.MatchedWords, ver.PlannedWords, 100*float64(ver.MatchedWords)/float64(ver.PlannedWords))
	}
	fmt.Fprintf(&b, "- Breaths cut: %d\n", countVerdict(bc.Spots, vBreath))
	n := 0
	for _, f := range findings {
		if f.Region {
			n++
			fmt.Fprintf(&b, "- Check before posting, %s-%s: %s (%s)\n", clock(f.Src0), clock(f.Src1), f.Kind, f.Evidence)
		}
	}
	if n == 0 {
		b.WriteString("- Publish check: nothing found\n")
	}
	r.times = append(r.times, fmt.Sprintf("total %s", time.Since(r.started).Round(time.Second)))
	fmt.Fprintf(&b, "\n## Time\n\n%s\n", strings.Join(r.times, ", "))

	path := filepath.Join(r.work, "report-video.md")
	_ = os.WriteFile(path, []byte(b.String()), 0o644)
	fmt.Println()
	fmt.Println("DONE - YouTube video edit")
	fmt.Printf("  Kept %.1f of %.1f minutes.\n", ver.EditSeconds/60, r.duration/60)
	fmt.Printf("  VEGAS project: %s (open in VEGAS now)\n", filepath.Base(veg))
	fmt.Printf("  Report: %s\n", path)
	fmt.Printf("  Titles, description and chapters: %s\n", filepath.Join(r.work, "youtube-upload.txt"))
	if r.vx.markers != "" {
		fmt.Printf("  Things to add (markers on the timeline): %s\n", r.vx.markers)
	}
}

// writeVideoExtra: the thinking taken out, the second listen, the cuts on his
// voice, the censor, the zooms, his track effects and the things to show.
func (r *run) writeVideoExtra(b *strings.Builder) {
	var cut []string
	kept := 0
	for _, t := range r.vx.thoughts {
		if t.Cut {
			cut = append(cut, fmt.Sprintf("- %s \"%s\" - %s", t.At, t.Text, t.By))
		} else {
			kept++
		}
	}
	fmt.Fprintf(b, "## Thinking out loud, taken out (%d)\n\nShort words you said on their own between pauses (times in the recording; they are not on the timeline). %d more were kept: they finish or start a sentence.\n\n%s\n\n", len(cut), kept, strings.Join(cut, "\n"))

	dropped, back := 0, []string{}
	for _, h := range r.vx.heard {
		if h.Heard == "" {
			dropped++
		} else {
			back = append(back, fmt.Sprintf("\"%s\" at %s", short(h.Heard, 60), clock(h.A)))
		}
	}
	if len(r.vx.heard) > 0 {
		fmt.Fprintf(b, "## Stretches with no words in the transcript (%d)\n\nListened to again: %d had no words (a laugh, a sound) and were taken out; %d had words the transcript missed and stayed%s.\n\n",
			len(r.vx.heard), dropped, len(back), listOrNone(back))
	}
	fmt.Fprintf(b, "## Cuts\n\n%d pieces were moved onto the first and last frame of your voice.\n\n", r.vx.tightened)

	d := r.vx.done
	var cs []string
	for _, x := range d.Bleeps {
		at := shiftAfterInserts(float64(x.F0)/r.fps, d.Placed, r.fps)
		cs = append(cs, fmt.Sprintf("- %s \"%s\" (%s in the recording)", frameClock(at, r.fps), x.Word, clock(x.At)))
	}
	fmt.Fprintf(b, "## Censored (%d)\n\nCensor 2.mp3 at %.1f dB, from one frame after the word starts to one frame before it ends. Times are on the VEGAS timeline.\n\n%s\n\n", len(cs), censorGain, strings.Join(cs, "\n"))
	fmt.Fprintf(b, "## Look\n\n- Slow zooms and crops: %d\n", d.Looks)
	if d.Starter != "" {
		b.WriteString("- Your hj-starter track effects are on the main video track (Beauty Box, AutoLooks, Color Corrector, Color Curves x2)\n")
	}
	b.WriteString("\n")
	var ls []string
	for _, p := range d.Placed {
		how := "over your picture"
		if p.Insert {
			how = "inserted"
		}
		ls = append(ls, fmt.Sprintf("- %s \"%s\" - %s %s", clock(p.Line.At), p.Line.Label, filepath.Base(p.Line.Asset), how))
	}
	for k, m := range d.Markers {
		ls = append(ls, fmt.Sprintf("- %s marker \"%s\" - %s", frameClock(m.At, r.fps), m.Label, d.MarkLines[k].What))
	}
	fmt.Fprintf(b, "## Things you mean to show (%d)\n\n%s\n", len(ls), strings.Join(ls, "\n"))
	if r.vx.markers != "" {
		fmt.Fprintf(b, "\nThe markers are listed in %s\n", r.vx.markers)
	}
	b.WriteString("\n")
	if len(d.Problems) > 0 {
		fmt.Fprintf(b, "## Not done\n\n- %s\n\n", strings.Join(d.Problems, "\n- "))
	}
}

func listOrNone(xs []string) string {
	if len(xs) == 0 {
		return ""
	}
	return ": " + strings.Join(xs, ", ")
}
