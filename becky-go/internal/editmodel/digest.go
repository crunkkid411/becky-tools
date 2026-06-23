package editmodel

import (
	"fmt"
	"strings"

	"becky-go/internal/edl"
	"becky-go/internal/pathx"
)

// Digest renders the COMPACT, model-facing view of the project: the state the
// embedded Gemma agent sees in its context every turn. It is deliberately terse —
// one line per clip plus the cursors — so a small local model is "not overloaded
// constantly, but can't be ignorant of what it's doing" (Jordan). No media bytes,
// no absolute-path bloat (basenames only), no JSON braces. Deterministic for a
// given Project so the agent loop + tests are reproducible.
//
// This is the minimal-but-sufficient discipline video-db/Director uses: a
// one-line-per-asset text digest, never a raw JSON state dump.
//
// Example:
//
//	project "TakingBack2007" rev=7 fps=30 dur=24.0s playhead=12.0s selection=[c2]
//	overlay: timecode date
//	track 0 V1 (video):
//	  c1  bounty.mp4 [60.0-68.0] pos=0.0 dur=8.0 "every penguin..." fx:[brightness]
//	  c2  bounty.mp4 [120.5-136.5] pos=8.0 dur=16.0 "i'll pay..." (selected)
//	track 1 A1 (audio): (empty)
func (p *Project) Digest() string {
	var b strings.Builder
	sel := "none"
	if len(p.Selection) > 0 {
		sel = "[" + strings.Join(p.Selection, " ") + "]"
	}
	fmt.Fprintf(&b, "project %q rev=%d fps=%g dur=%.1fs playhead=%.1fs selection=%s\n",
		p.Name, p.Rev, p.FPS, p.Duration(), p.Playhead, sel)
	if ov := overlaySummary(p.Overlay); ov != "" {
		fmt.Fprintf(&b, "overlay: %s\n", ov)
	}
	selSet := make(map[string]bool, len(p.Selection))
	for _, id := range p.Selection {
		selSet[id] = true
	}
	for _, t := range p.Tracks {
		mute := ""
		if t.Mute {
			mute = " MUTED"
		}
		if len(t.Clips) == 0 {
			fmt.Fprintf(&b, "track %d %s (%s)%s: (empty)\n", t.Index, t.Name, t.Kind, mute)
			continue
		}
		fmt.Fprintf(&b, "track %d %s (%s)%s:\n", t.Index, t.Name, t.Kind, mute)
		for _, c := range t.Clips {
			fmt.Fprintf(&b, "  %s  %s [%.1f-%.1f] pos=%.1f dur=%.1f",
				c.ID, pathx.Base(c.Source), c.In, c.Out, c.Pos, c.Dur())
			if c.Label != "" {
				fmt.Fprintf(&b, " %q", truncate(c.Label, 50))
			}
			if len(c.Effects) > 0 {
				names := make([]string, len(c.Effects))
				for i, e := range c.Effects {
					names[i] = e.Name
				}
				fmt.Fprintf(&b, " fx:[%s]", strings.Join(names, ","))
			}
			if selSet[c.ID] {
				b.WriteString(" (selected)")
			}
			b.WriteString("\n")
		}
	}
	if len(p.Markers) > 0 {
		b.WriteString("markers:")
		for _, m := range p.Markers {
			fmt.Fprintf(&b, " %s@%.1fs", labelOr(m.Label, m.ID), m.At)
		}
		b.WriteString("\n")
	}
	return b.String()
}

// overlaySummary lists the enabled forensic-overlay lines, or "" when off.
func overlaySummary(o edl.Overlay) string {
	if !o.Enabled {
		return ""
	}
	var on []string
	if o.ShowFilename {
		on = append(on, "filename")
	}
	if o.ShowTimecode {
		on = append(on, "timecode")
	}
	if o.ShowDate {
		on = append(on, "date")
	}
	if o.ShowPerson {
		on = append(on, "person")
	}
	if o.ShowLocation {
		on = append(on, "location")
	}
	if o.ShowLink {
		on = append(on, "link")
	}
	if len(on) == 0 {
		return "enabled"
	}
	return strings.Join(on, " ")
}

func truncate(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) <= n {
		return s
	}
	if n <= 1 {
		return s[:n]
	}
	return s[:n-1] + "…"
}

func labelOr(label, fallback string) string {
	if strings.TrimSpace(label) != "" {
		return label
	}
	return fallback
}
