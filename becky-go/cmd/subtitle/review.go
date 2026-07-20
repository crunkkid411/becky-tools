package main

import (
	"fmt"
	"os"
	"strings"

	"becky-go/internal/subs"
)

func firstLine(s string) string {
	if i := strings.IndexAny(s, "\r\n"); i >= 0 {
		return s[:i]
	}
	return s
}

// loadMarginV returns the vertical placement the review apps saved beside this
// .srt, or 0 when there is none. The sidecar lives in internal/subs so the reel
// render burns captions at the SAME height this tool does.
func loadMarginV(srt string) int { return subs.LoadMarginV(srt) }

// noteReviewSkipped writes the reason to stderr so a silent fallback is never
// mistaken for a successful review.
func noteReviewSkipped(reason string) {
	fmt.Fprintf(os.Stderr, "caption review skipped (%s) - captions will break on pacing only\n", reason)
}
