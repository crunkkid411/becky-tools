package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// report writes becky-edit\report-<model>.md and prints the short version.
func (r *run) report(sel Selection, ss []Sentence, ranges []Range, predicted []span, loud []string,
	findings []Finding, pubNotes []string, bc breathResult, faces []moment, faceNote string,
	ver *Verification, marks []mark, veg string) {
	var b strings.Builder
	edit := 0.0
	for _, p := range predicted {
		edit += p.B - p.A
	}
	if ver != nil && ver.EditSeconds > 0 {
		edit = ver.EditSeconds
	}
	unsure, reviewed, agreed, chats := 0, 0, 0, 0
	labelCount := map[string]int{}
	for _, d := range sel.Decisions {
		labelCount[d.Label]++
		if d.Unsure && d.Keep {
			unsure++
		}
		if chatCut(d) {
			chats++
		}
		if d.Review != nil {
			reviewed++
			if d.Review.Keep == d.Keep && !d.Unsure {
				agreed++
			}
		}
	}
	regions := 0
	for _, f := range findings {
		if f.Region {
			regions++
		}
	}

	fmt.Fprintf(&b, "# Livestream edit - %s\n\n", r.label)
	fmt.Fprintf(&b, "**Result:** %.1f of %.1f minutes kept, in %d sections.", edit/60, r.duration/60, len(ranges))
	if veg != "" {
		fmt.Fprintf(&b, " Saved as `%s`.", filepath.Base(veg))
	}
	fmt.Fprintf(&b, "\n\n**What to keep (guidance):** %s\n\n", r.guidance)
	if len(sel.Topics) > 0 {
		b.WriteString("**The topics the model read in that:**\n\n")
		for i, t := range sel.Topics {
			fmt.Fprintf(&b, "%d. %s\n", i+1, t)
		}
		b.WriteString("\n")
	}

	b.WriteString("## What stays\n\n| # | In the stream | Length | Starts with | Ends with |\n|---|---|---|---|---|\n")
	for i, rg := range ranges {
		fmt.Fprintf(&b, "| %d | %s-%s | %.0f s | %s | %s |\n", i+1, clock(rg.In), clock(rg.Out), rg.Out-rg.In,
			mdCell(short(rg.First, 50)), mdCell(short(rg.Last, 50)))
	}

	fmt.Fprintf(&b, "\n## On the timeline for you to look at (%d)\n\n", len(marks))
	fmt.Fprintf(&b, "- **Unsure calls:** %d kept and marked with an \"Unsure\" region\n", unsure)
	fmt.Fprintf(&b, "- **Unsure chat replies:** %d cut, as the model said, with a marker at each cut\n", chats)
	b.WriteString(momentLine(faces, faceNote) + "\n")
	fmt.Fprintf(&b, "- **Publish check:** %d region(s)", regions)
	if n := len(findings) - regions; n > 0 {
		fmt.Fprintf(&b, ", plus %d single-frame maybe(s) listed below", n)
	}
	fmt.Fprintf(&b, "\n- **Loud cuts:** %d marker(s) where a cut sits inside speech\n", len(loud))
	breathLine, breathDetails := breathSummary(bc.Spots, bc.Note, bc.Notes)
	b.WriteString(breathLine + "\n")
	if ver != nil {
		n := 0
		for _, s := range ver.Missing {
			if s.Words >= 3 {
				n++
			}
		}
		fmt.Fprintf(&b, "- **Planned words not heard:** %d region(s)\n", n)
	}

	if ver != nil {
		b.WriteString("\n## Checks of the finished edit\n\n")
		fmt.Fprintf(&b, "- Timeline: %s\n", ver.Timeline)
		fmt.Fprintf(&b, "- %d of %d pieces are exactly as planned (frame for frame)\n", ver.IdenticalPieces, ver.PredictedPieces)
		lost := 0
		for _, s := range ver.LostWords {
			lost += s.Words
		}
		fmt.Fprintf(&b, "- Planned words: %d; still on the timeline: %d\n", ver.PlannedWords, ver.PlannedWords-lost)
		if ver.Note != "" {
			fmt.Fprintf(&b, "- Re-transcription: %s\n", ver.Note)
		} else if ver.PlannedWords > 0 {
			fmt.Fprintf(&b, "- Re-transcribed edit: %d words heard; %d of the %d planned words heard again (%.1f%%)%s\n",
				ver.HeardWords, ver.MatchedWords, ver.PlannedWords, 100*float64(ver.MatchedWords)/float64(ver.PlannedWords), onePassNote(ver.OnePass))
			fmt.Fprintf(&b, "- %d spot(s) of 2+ planned words not heard; %d spot(s) of 2+ unplanned words heard (listed below)\n", len(ver.Missing), len(ver.Extra))
		}
	}

	b.WriteString("\n## How the model decided\n\n")
	var ls []string
	for _, l := range labels {
		if labelCount[l] > 0 {
			ls = append(ls, fmt.Sprintf("%s %d", l, labelCount[l]))
		}
	}
	fmt.Fprintf(&b, "- %d sentences: %s\n", len(sel.Decisions), strings.Join(ls, ", "))
	if sel.Reviewer != "" {
		fmt.Fprintf(&b, "- %s re-decided %d of %s's calls on its own (both sides of every keep/cut edge, cuts near kept lines, unsure calls): agreed on %d, disagreed on %d\n",
			sel.Reviewer, reviewed, sel.Model, agreed, reviewed-agreed)
	}
	fmt.Fprintf(&b, "- Took %s\n", time.Duration(sel.Seconds*float64(time.Second)).Round(time.Second))
	for _, n := range append(sel.Notes, pubNotes...) {
		fmt.Fprintf(&b, "- Note: %s\n", n)
	}
	if len(sel.Outline) > 0 {
		b.WriteString("\nWhat the model thinks the stream covers:\n\n")
		for _, o := range sel.Outline {
			fmt.Fprintf(&b, "- %s\n", o)
		}
	}

	b.WriteString("\n## Details\n\n### Unsure calls\n\n")
	for _, d := range sel.Decisions {
		if d.Unsure {
			fmt.Fprintf(&b, "- %s %s - %s: \"%s\"\n", clock(ss[d.ID].Start), verdict(d.Keep), d.Note, short(ss[d.ID].Text, 90))
		}
	}
	b.WriteString("\n### Publish check\n\n")
	for _, f := range findings {
		tag := "region"
		if !f.Region {
			tag = "maybe (one frame, no region)"
		}
		fmt.Fprintf(&b, "- %s-%s %s [%s]: %s\n", clock(f.Src0), clock(f.Src1), f.Kind, tag, f.Evidence)
	}
	b.WriteString("\n### Loud cuts\n\n")
	for _, l := range loud {
		fmt.Fprintf(&b, "- %s\n", l)
	}
	if ver != nil {
		b.WriteString("\n### Planned words not heard in the edit\n\n")
		for _, s := range ver.Missing {
			fmt.Fprintf(&b, "- edit %s (stream %s)%s: \"%s\"\n", clock(s.Timeline), clock(s.Source), joinTag(s.NearJoin), short(s.Text, 90))
		}
		b.WriteString("\n### Words heard in the edit that were not planned\n\n")
		for _, s := range ver.Extra {
			fmt.Fprintf(&b, "- edit %s (stream %s)%s: \"%s\"\n", clock(s.Timeline), clock(s.Source), joinTag(s.NearJoin), short(s.Text, 90))
		}
		if len(ver.LostWords) > 0 {
			b.WriteString("\n### Planned words no longer on the timeline\n\n")
			for _, s := range ver.LostWords {
				fmt.Fprintf(&b, "- stream %s: \"%s\"\n", clock(s.Source), short(s.Text, 90))
			}
		}
	}
	b.WriteString(breathSection(breathDetails))
	r.times = append(r.times, fmt.Sprintf("total %s", time.Since(r.started).Round(time.Second)))
	fmt.Fprintf(&b, "\n## Time\n\n%s\n", strings.Join(r.times, ", "))

	path := reportPath(r.work, r.tag, r.media, veg)
	_ = os.WriteFile(path, []byte(b.String()), 0o644)

	fmt.Println()
	fmt.Println("DONE - " + r.label)
	fmt.Printf("  Kept %.1f of %.1f minutes in %d sections.\n", edit/60, r.duration/60, len(ranges))
	if veg != "" {
		fmt.Printf("  VEGAS project: %s (open in VEGAS now)\n", filepath.Base(veg))
	}
	fmt.Printf("  %d things to look at on the timeline (unsure %d, publish %d, loud cuts %d, checked breaths %d, cut).\n",
		len(marks), unsure, regions, len(loud), countVerdict(bc.Spots, vBreath))
	fmt.Printf("  Report: %s\n", path)
}

func mdCell(s string) string { return strings.ReplaceAll(s, "|", "/") }

// reportPath is becky-edit\report-<model>.md, or report-<model> (2).md for the
// project "<folder>-<model> (2).veg": a new version never overwrites the report
// of the project before it.
func reportPath(work, tag, media, veg string) string {
	def := filepath.Base(filepath.Dir(media)) + "-" + tag
	suffix := strings.TrimPrefix(strings.TrimSuffix(filepath.Base(veg), ".veg"), def)
	if veg == "" || suffix == filepath.Base(veg) {
		suffix = ""
	}
	return filepath.Join(work, "report-"+tag+suffix+".md")
}

func onePassNote(one bool) string {
	if one {
		return " - Parakeet only: another VEGAS was open, so the WhisperX second opinion was left out"
	}
	return ""
}

func joinTag(near bool) string {
	if near {
		return " at a cut"
	}
	return ""
}
