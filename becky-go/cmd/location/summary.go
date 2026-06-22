package main

import (
	"fmt"
	"strings"
)

// renderSummary produces the concise, eyes-friendly block (SPEC §3c;
// ACCESSIBILITY.md "lead with the answer, keep it tight"). Verdict FIRST, then
// the per-clip room map, then anything flagged. One fact per line, no alignment-
// dependent tables.
func renderSummary(r Report) string {
	var b strings.Builder

	// Verdict first.
	fmt.Fprintf(&b, "VERDICT: %s (confidence %.2f).\n", r.Verdict.Headline, r.Verdict.Confidence)
	for _, basis := range r.Verdict.Basis {
		fmt.Fprintf(&b, "  Basis: %s\n", basis)
	}

	// Per-clip room map.
	if len(r.Rooms) > 0 {
		b.WriteString("\nRooms:\n")
		for _, room := range r.Rooms {
			fmt.Fprintf(&b, "  %s — %s\n", room.Label, clipList(room.ClipIndices))
		}
	}

	// Multi-room clips.
	for _, c := range r.Clips {
		if c.MultiRoom {
			fmt.Fprintf(&b, "  clip %d — multi-room (%d segments)\n", c.Index, len(c.Segments))
		}
	}

	// High-confidence same-room conclusions.
	var sameRoom []PairVerdict
	for _, pv := range r.PairVerdicts {
		if pv.Level == "SAME_ROOM" {
			sameRoom = append(sameRoom, pv)
		}
	}
	if len(sameRoom) > 0 {
		b.WriteString("\nSame room (corroborated):\n")
		for _, pv := range sameRoom {
			fmt.Fprintf(&b, "  clip %d + clip %d -> same room (%s).\n", pv.A, pv.B, pv.Basis)
			if pv.ExhibitHint != "" {
				fmt.Fprintf(&b, "    Exhibit: %s\n", pv.ExhibitHint)
			}
		}
	}

	// Things that need a human.
	if len(r.ReviewRequired) > 0 {
		b.WriteString("\nNeeds your eyes:\n")
		for _, w := range r.ReviewRequired {
			fmt.Fprintf(&b, "  clip %d + clip %d: %s\n", w.A, w.B, w.Reason)
		}
	}

	// Degraded clips.
	if len(r.Degraded) > 0 {
		b.WriteString("\nCould not fingerprint:\n")
		for _, d := range r.Degraded {
			fmt.Fprintf(&b, "  clip %d (%s): %s\n", d.Index, d.Path, d.Reason)
		}
	}

	if r.FingerprintMethod == "phash" {
		b.WriteString("\nNote: phash-only fingerprint (feature matcher unavailable) — lower certainty.\n")
	}
	return b.String()
}

func clipList(idxs []int) string {
	if len(idxs) == 0 {
		return "(none)"
	}
	parts := make([]string, len(idxs))
	for i, v := range idxs {
		parts[i] = fmt.Sprintf("clip %d", v)
	}
	return strings.Join(parts, ", ")
}
