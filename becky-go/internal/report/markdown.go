package report

import (
	"fmt"
	"strings"
)

// Markdown formats the Report as a human-readable markdown document following
// FORENSIC-OUTPUT-PHILOSOPHY.md: DOCUMENTED facts stated plainly, CANDIDATE /
// ANALYSIS items clearly flagged as needing human review.
func Markdown(r Report) string {
	var sb strings.Builder

	fmt.Fprintf(&sb, "# Forensic Case Report\n\n")
	fmt.Fprintf(&sb, "**Source:** %s  \n", r.Source)
	fmt.Fprintf(&sb, "**Generated:** %s  \n", r.GeneratedAt)
	if r.Duration > 0 {
		fmt.Fprintf(&sb, "**Clip duration:** %s  \n", formatTime(r.Duration))
	}
	sb.WriteString("\n")

	// Signal summary
	sb.WriteString("## Signals available\n\n")
	sb.WriteString(signalTable(r.Signals))
	sb.WriteString("\n")

	// Conclusions (DOCUMENTED)
	if len(r.Conclusions) > 0 {
		sb.WriteString("## [DOCUMENTED] Conclusions\n\n")
		sb.WriteString("These findings meet the corroboration threshold — state them plainly.\n\n")
		for _, f := range r.Conclusions {
			fmt.Fprintf(&sb, "- **%s** — %s *(confidence %.2f, from: %s)*\n",
				f.Tag, f.What, f.Confidence, strings.Join(f.Sources, ", "))
		}
		sb.WriteString("\n")
	}

	// Review items (CANDIDATE / ANALYSIS)
	if len(r.ReviewItems) > 0 {
		sb.WriteString("## [REVIEW REQUIRED] Candidates and Analysis\n\n")
		sb.WriteString("Single-signal or low-confidence findings — human review needed before concluding.\n\n")
		for _, f := range r.ReviewItems {
			fmt.Fprintf(&sb, "- **[%s]** %s at %s *(confidence %.2f, from: %s)*\n",
				f.Tag, f.What, f.When, f.Confidence, strings.Join(f.Sources, ", "))
		}
		sb.WriteString("\n")
	}

	// Entities section
	if len(r.Entities) > 0 {
		sb.WriteString("## Entities\n\n")
		for _, e := range r.Entities {
			icon := "⚫"
			if e.Concluded {
				icon = "✅"
			}
			fmt.Fprintf(&sb, "### %s %s [%s]\n\n", icon, e.Name, e.Tag)
			fmt.Fprintf(&sb, "- **Type:** %s  \n", e.Type)
			fmt.Fprintf(&sb, "- **Confidence:** %.2f  \n", e.Confidence)
			if len(e.CorroboratedBy) > 0 {
				fmt.Fprintf(&sb, "- **Corroborated by:** %s  \n", strings.Join(e.CorroboratedBy, ", "))
			}
			if len(e.Appearances) > 0 {
				fmt.Fprintf(&sb, "- **Appears at:** %s  \n", spansDesc(e.Appearances))
			}
			sb.WriteString("\n")
		}
	}

	// Timeline
	if len(r.Timeline) > 0 {
		sb.WriteString("## Timeline\n\n")
		sb.WriteString("| Time | Type | Source | Tag | Description | Speaker |\n")
		sb.WriteString("|------|------|--------|-----|-------------|--------|\n")
		for _, m := range r.Timeline {
			timeStr := formatTime(m.Time)
			if m.End > m.Time+0.1 {
				timeStr = formatTimeRange(m.Time, m.End)
			}
			speaker := m.Speaker
			if speaker == "" {
				speaker = "—"
			}
			desc := m.Description
			if len(desc) > 80 {
				desc = desc[:77] + "..."
			}
			fmt.Fprintf(&sb, "| %s | %s | %s | %s | %s | %s |\n",
				timeStr, m.Type, m.Source, m.Tag, desc, speaker)
		}
		sb.WriteString("\n")
	}

	// Notes
	if len(r.Notes) > 0 {
		sb.WriteString("## Notes\n\n")
		for _, n := range r.Notes {
			fmt.Fprintf(&sb, "- %s\n", n)
		}
		sb.WriteString("\n")
	}

	if r.Degraded {
		sb.WriteString("> **DEGRADED** — no forensic data was found. Check that the sidecar files exist and are non-empty.\n\n")
	}

	return sb.String()
}

func signalTable(sig SignalSummary) string {
	var sb strings.Builder
	sb.WriteString("| Tool | Available | Summary |\n")
	sb.WriteString("|------|-----------|--------|\n")

	if sig.Transcript != nil && sig.Transcript.Present {
		fmt.Fprintf(&sb, "| becky-transcribe | ✅ | %d segments, %.1fs, model: %s |\n",
			sig.Transcript.SegmentCount, sig.Transcript.Duration, sig.Transcript.Model)
	} else {
		sb.WriteString("| becky-transcribe | ❌ | not provided |\n")
	}

	if sig.Events != nil && sig.Events.Present {
		fmt.Fprintf(&sb, "| becky-events | ✅ | %d events |\n", sig.Events.EventCount)
	} else {
		sb.WriteString("| becky-events | ❌ | not provided |\n")
	}

	if sig.Identify != nil && sig.Identify.Present {
		fmt.Fprintf(&sb, "| becky-identify | ✅ | %d identified, %d unidentified |\n",
			sig.Identify.IdentifiedCount, sig.Identify.UnidentifiedCount)
	} else {
		sb.WriteString("| becky-identify | ❌ | not provided |\n")
	}

	if sig.Motion != nil && sig.Motion.Present {
		fmt.Fprintf(&sb, "| becky-motion | ✅ | %d bursts (%d sub-second) |\n",
			sig.Motion.BurstCount, sig.Motion.SubSecondCount)
	} else {
		sb.WriteString("| becky-motion | ❌ | not provided |\n")
	}

	return sb.String()
}

func spansDesc(spans []Span) string {
	parts := make([]string, 0, len(spans))
	for _, s := range spans {
		if s.End > s.Start+0.1 {
			parts = append(parts, formatTimeRange(s.Start, s.End))
		} else {
			parts = append(parts, formatTime(s.Start))
		}
	}
	if len(parts) > 6 {
		parts = parts[:6]
		parts = append(parts, fmt.Sprintf("... (+%d more)", len(spans)-6))
	}
	return strings.Join(parts, ", ")
}
