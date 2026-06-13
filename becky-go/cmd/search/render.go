package main

import (
	"fmt"
	"path/filepath"
	"strings"
)

// renderTxt produces a human-readable rendering of the search output for
// --format txt. JSON stays the machine contract; this is for eyeballing in a
// terminal — "Defendant (92% match) — video.mp4 @ 1:23" style, the way Jordan
// asked search to "talk the way humans talk".
func renderTxt(out output) string {
	var b strings.Builder
	fmt.Fprintf(&b, "query: %q  [mode: %s]\n", out.Query, out.Mode)
	if out.Note != "" {
		fmt.Fprintf(&b, "note: %s\n", out.Note)
	}
	if len(out.Results) == 0 {
		b.WriteString("no results\n")
	}
	for _, r := range out.Results {
		if r.Kind == "ocr" {
			renderOCR(&b, r)
			continue
		}
		renderSeg(&b, r)
	}
	fmt.Fprintf(&b, "\nstats: %d result(s) · %d transcript · %d on-screen(ocr) · %d named · %d unidentified · avg similarity %.3f\n",
		out.Stats.TotalResults, out.Stats.TranscriptHits, out.Stats.OCRHits,
		out.Stats.NamedSpeakers, out.Stats.Unidentified, out.Stats.AvgConfidence)
	return b.String()
}

// renderSeg renders one spoken-transcript hit — "Defendant (92% match, hybrid) —
// video.mp4 @ 1:23" with its quote, duration, similarity, and review state.
func renderSeg(b *strings.Builder, r result) {
	fmt.Fprintf(b, "\n#%d  %s (%.0f%% match, %s)  —  %s @ %s\n",
		r.Rank, r.Speaker, r.Similarity*100, r.Matched, filepath.Base(r.SourceFile), clock(r.Timestamp))
	fmt.Fprintf(b, "    \"%s\"\n", strings.TrimSpace(r.Text))
	review := "verified"
	if r.NeedsReview {
		review = "needs review"
	}
	fmt.Fprintf(b, "    duration %.1fs · similarity %.3f · %s\n", r.Duration, r.Similarity, review)
	for _, c := range r.Context {
		fmt.Fprintf(b, "      …context @ %s: \"%s\"\n", clock(c.Timestamp), strings.TrimSpace(c.Text))
	}
}

// renderOCR renders one on-screen OCR hit — "on-screen text (97% read, ocr) —
// video.mp4 @ 0:13" with the recognized text, the exact frame image it came from,
// the candidate_* category (a triage hint, never a conclusion), and the recognition
// confidence. The reviewer gets everything needed to open the frame and verify.
func renderOCR(b *strings.Builder, r result) {
	fmt.Fprintf(b, "\n#%d  on-screen text (%.0f%% read, %s)  —  %s @ %s\n",
		r.Rank, r.OCRConfidence*100, r.Matched, filepath.Base(r.SourceFile), clock(r.Timestamp))
	fmt.Fprintf(b, "    \"%s\"\n", strings.TrimSpace(r.Text))
	cat := r.Category
	if cat == "" {
		cat = "text"
	}
	fmt.Fprintf(b, "    category %s · read confidence %.3f · frame %s\n",
		cat, r.OCRConfidence, filepath.Base(r.FramePath))
}

// clock formats seconds as M:SS for readable timestamps (e.g. 83.2 -> "1:23").
func clock(seconds float64) string {
	if seconds < 0 {
		seconds = 0
	}
	total := int(seconds + 0.5)
	return fmt.Sprintf("%d:%02d", total/60, total%60)
}
