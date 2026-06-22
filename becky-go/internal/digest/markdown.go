package digest

import (
	"fmt"
	"strings"

	"becky-go/internal/report"
)

// Markdown renders the Digest as the LINEAR DIGEST.md layout (SPEC §3). It is
// deliberately table-free, emoji-free, and box-drawing-free per ACCESSIBILITY.md:
// headings + short labelled lines a low-vision sighted reader scans and a model
// parses. Words carry the meaning (DOCUMENTED / CANDIDATE / UNTRUSTED / REVIEW),
// never an icon or a color.
func Markdown(d Digest) string {
	var sb strings.Builder

	// --- Case summary ---
	fmt.Fprintf(&sb, "# Case Digest - %s\n\n", displayFolder(d.Folder))
	fmt.Fprintf(&sb, "Generated: %s\n", d.GeneratedAt)
	fmt.Fprintf(&sb, "Clips: %d ingested, %d fully processed, %d partial, %d failed\n",
		d.Corpus.ClipsTotal, d.Corpus.ClipsOK, d.Corpus.ClipsPartial, d.Corpus.ClipsFailed)
	if d.KB != "" {
		fmt.Fprintf(&sb, "Knowledge base: %s\n", d.KB)
	} else {
		sb.WriteString("Knowledge base: none (identify step skipped)\n")
	}
	if len(d.Corpus.PeopleConcluded) > 0 {
		fmt.Fprintf(&sb, "People concluded across the corpus: %s\n", strings.Join(d.Corpus.PeopleConcluded, ", "))
	} else {
		sb.WriteString("People concluded across the corpus: none\n")
	}
	if d.Corpus.EarliestCapture != "" {
		fmt.Fprintf(&sb, "Earliest trusted capture: %s, Latest: %s\n",
			d.Corpus.EarliestCapture, d.Corpus.LatestCapture)
	}
	if d.Degraded {
		sb.WriteString("\nDEGRADED: no corroborated forensic data was found across this corpus. ")
		sb.WriteString("Check that the pipeline produced sidecars and that they are non-empty.\n")
	}
	sb.WriteString("\n---\n\n")

	// --- Per-clip sections ---
	for i, c := range d.Clips {
		writeClip(&sb, i+1, c)
		sb.WriteString("\n---\n\n")
	}

	// --- Corpus unknowns roll-up ---
	sb.WriteString("## Corpus unknowns (everything still needing a human)\n\n")
	wroteUnknown := false
	for _, c := range d.Clips {
		for _, u := range c.Unknowns {
			fmt.Fprintf(&sb, "- %s: %s\n", c.Stem, u)
			wroteUnknown = true
		}
		if !c.CaptureTrusted && c.CaptureTimeSource == sourceMTime {
			fmt.Fprintf(&sb, "- %s: capture-time fell back to file mtime (UNTRUSTED) - date unverified.\n", c.Stem)
			wroteUnknown = true
		}
	}
	if !wroteUnknown {
		sb.WriteString("- none flagged\n")
	}
	sb.WriteString("\n")

	// --- Notes ---
	sb.WriteString("## Notes\n\n")
	notes := corpusNotes(d)
	if len(notes) == 0 {
		sb.WriteString("- none\n")
	} else {
		for _, n := range notes {
			fmt.Fprintf(&sb, "- %s\n", n)
		}
	}

	return sb.String()
}

// writeClip renders one per-clip section in the linear contract (SPEC §3).
func writeClip(sb *strings.Builder, n int, c ClipDigest) {
	fmt.Fprintf(sb, "## %d. %s\n\n", n, clipTitle(c))

	if !c.HasReport {
		sb.WriteString("(no report.json or sidecars found for this clip - showing capture metadata only)\n\n")
	}

	// When (capture-time): always shown, with its source and trusted/UNTRUSTED.
	sb.WriteString(captureLine(c))

	if c.Duration > 0 {
		fmt.Fprintf(sb, "Duration: %s\n", formatDuration(c.Duration))
	}

	// Where.
	fmt.Fprintf(sb, "Where: %s\n", whereLine(c))

	// Device (omit if absent).
	if c.Device != "" {
		fmt.Fprintf(sb, "Device: %s\n", c.Device)
	}
	sb.WriteString("\n")

	// Who.
	sb.WriteString("Who:\n")
	if len(c.ConcludedPeople) == 0 && len(c.CandidatePeople) == 0 {
		sb.WriteString("- nobody identified\n")
	} else {
		for _, p := range c.ConcludedPeople {
			fmt.Fprintf(sb, "- %s - DOCUMENTED.\n", p)
		}
		for _, p := range c.CandidatePeople {
			fmt.Fprintf(sb, "- %s - CANDIDATE, single signal, not concluded.\n", p)
		}
	}
	sb.WriteString("\n")

	// What (key moments).
	sb.WriteString("What (key moments):\n")
	if len(c.KeyMoments) == 0 {
		sb.WriteString("- no notable moments\n")
	} else {
		for _, m := range c.KeyMoments {
			fmt.Fprintf(sb, "- %s\n", m)
		}
		if c.MoreMoments > 0 {
			fmt.Fprintf(sb, "- (+%d more in report.json)\n", c.MoreMoments)
		}
	}
	sb.WriteString("\n")

	// Unknowns / needs a human - NEVER empty-by-omission.
	sb.WriteString("Unknowns / needs a human:\n")
	if len(c.Unknowns) == 0 {
		sb.WriteString("- none flagged\n")
	} else {
		for _, u := range c.Unknowns {
			fmt.Fprintf(sb, "- %s\n", u)
		}
	}
	sb.WriteString("\n")

	// Sidecars: the audit trail, always present.
	if c.SidecarDir != "" {
		fmt.Fprintf(sb, "Sidecars: %s\n", c.SidecarDir)
	} else {
		sb.WriteString("Sidecars: (none recorded)\n")
	}
}

// captureLine renders the "When (capture-time):" line. The capture_time_source is
// ALWAYS shown, and the literal word UNTRUSTED is emitted when the source is the
// file mtime fallback (a copied/synced file's mtime must never read as a capture
// time).
func captureLine(c ClipDigest) string {
	if c.CaptureTimeLocal == "" && c.CaptureTimeSource == "" {
		return "When (capture-time): unknown - no capture tag\n"
	}
	when := c.CaptureTimeLocal
	if when == "" {
		when = "unknown"
	}
	src := c.CaptureTimeSource
	if src == "" {
		src = "unknown"
	}
	trust := "trusted"
	if !c.CaptureTrusted {
		trust = "UNTRUSTED"
	}
	return fmt.Sprintf("When (capture-time): %s  [source: %s - %s]\n", when, src, trust)
}

// whereLine renders the location summary from GPS (no on-screen OCR address is
// available to the digest today; that is noted plainly).
func whereLine(c ClipDigest) string {
	if c.GPS != nil {
		return fmt.Sprintf("GPS %.6f, %.6f (lat/long in file)", c.GPS.Latitude, c.GPS.Longitude)
	}
	return "no location signal"
}

// formatMoment renders one DOCUMENTED conclusion as a key-moment bullet:
// "0:13 - <what>. [DOCUMENTED, <sources>]".
func formatMoment(f report.Finding) string {
	when := f.When
	if when == "" {
		when = "unknown"
	}
	return fmt.Sprintf("%s - %s. [%s, %s]", when, f.What, f.Tag, joinSources(f.Sources))
}

// formatUnknown renders one review item as an unknown bullet, keeping its basis
// and the REVIEW marker so the reader sees what's pending and why.
func formatUnknown(f report.Finding) string {
	when := f.When
	if when == "" {
		when = "unknown"
	}
	return fmt.Sprintf("@ %s - %s [%s, %s] - REVIEW", when, f.What, f.Tag, joinSources(f.Sources))
}

func joinSources(s []string) string {
	if len(s) == 0 {
		return "unknown"
	}
	return strings.Join(s, "+")
}

// corpusNotes folds run-level notes plus partial/failed-clip summaries into the
// Notes block.
func corpusNotes(d Digest) []string {
	var notes []string
	notes = append(notes, d.Notes...)
	for _, c := range d.Clips {
		if c.Status == "partial" {
			notes = append(notes, fmt.Sprintf("%s is PARTIAL - one or more steps failed (see manifest.json).", c.Stem))
		}
		if c.Status == "failed" {
			notes = append(notes, fmt.Sprintf("%s FAILED to process (see manifest.json).", c.Stem))
		}
		for _, n := range c.Notes {
			notes = append(notes, fmt.Sprintf("%s: %s", c.Stem, n))
		}
	}
	return notes
}

// formatDuration renders seconds as H:MM:SS (or M:SS under an hour) for the
// Duration line.
func formatDuration(sec float64) string {
	total := int(sec + 0.5)
	h := total / 3600
	m := (total % 3600) / 60
	s := total % 60
	if h > 0 {
		return fmt.Sprintf("%d:%02d:%02d", h, m, s)
	}
	return fmt.Sprintf("%d:%02d", m, s)
}

// displayFolder returns a readable folder label, "(unnamed corpus)" when empty.
func displayFolder(folder string) string {
	folder = strings.TrimSpace(folder)
	if folder == "" {
		return "(unnamed corpus)"
	}
	return folder
}

// clipTitle returns the clip's display title: the source basename if known, else
// the stem.
func clipTitle(c ClipDigest) string {
	if c.Input != "" {
		return baseName(c.Input)
	}
	return c.Stem
}

// baseName returns the last path element of p, separator-agnostic (a value may be
// a Windows path even on Linux).
func baseName(p string) string {
	p = strings.TrimRight(p, `/\`)
	if i := strings.LastIndexAny(p, `/\`); i >= 0 {
		return p[i+1:]
	}
	return p
}
