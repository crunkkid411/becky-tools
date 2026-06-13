package main

import (
	"fmt"
	"strings"
)

// renderTxt produces the human-readable consolidation report for --format txt,
// matching the layout in 12-becky-consolidate.md. JSON stays the machine
// contract; this is the at-a-glance view Jordan reads to spot coverage gaps.
func renderTxt(r Report) string {
	var b strings.Builder
	b.WriteString("=== Consolidation Report ===\n\n")

	b.WriteString("Entities:\n")
	if len(r.Entities) == 0 {
		b.WriteString("  (none)\n")
	}
	for _, e := range r.Entities {
		fmt.Fprintf(&b, "  %s: recognized in %d/%d videos (%.1f%%)\n",
			e.Name, e.Recognized, e.TotalVideos, e.Percent)
		// Show each modality with any coverage (skip flat-zero rows to stay terse).
		for _, m := range modalities {
			mc := e.Modalities[m]
			if mc.Videos == 0 {
				continue
			}
			fmt.Fprintf(&b, "    %s: %d/%d (%.1f%%)\n",
				titleCase(m), mc.Videos, mc.TotalVideos, mc.Percent)
		}
	}

	b.WriteString("\nCoverage Gaps:\n")
	if len(r.Gaps) == 0 {
		b.WriteString("  (none)\n")
	}
	for _, g := range r.Gaps {
		fmt.Fprintf(&b, "  %s not recognized in %d videos\n", g.Entity, g.NotRecognized)
		for _, s := range g.Suggestions {
			fmt.Fprintf(&b, "    -> %s\n", s)
		}
	}

	b.WriteString("\nPropagation:\n")
	mode := ""
	if r.DryRun {
		mode = " (dry-run, no changes written)"
	}
	fmt.Fprintf(&b, "  %d names propagated (confidence >= %.2f)%s\n", r.Propagation.Propagated, r.Threshold, mode)
	fmt.Fprintf(&b, "  %d names skipped (confidence < %.2f)\n", r.Propagation.Skipped, r.Threshold)
	for _, d := range r.Propagation.Details {
		if d.Action == "propagated" {
			fmt.Fprintf(&b, "    + %s [%s] %s conf %.4f%s\n",
				d.Entity, d.Modality, speakerTag(d.SpeakerID), d.Confidence, mode)
		} else {
			fmt.Fprintf(&b, "    - %s [%s] %s skipped: %s\n",
				d.Entity, d.Modality, speakerTag(d.SpeakerID), d.Reason)
		}
	}
	return b.String()
}

// titleCase upper-cases the first rune of s (voice -> Voice) for the report.
func titleCase(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

// speakerTag renders an optional speaker id for the propagation detail lines.
func speakerTag(speakerID string) string {
	if strings.TrimSpace(speakerID) == "" {
		return ""
	}
	return "(" + speakerID + ")"
}
