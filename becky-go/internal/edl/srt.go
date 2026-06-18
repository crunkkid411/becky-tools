package edl

import (
	"bufio"
	"fmt"
	"io"
)

// WriteSRT emits a re-based SubRip (.srt) for the reel. Each clip with a
// non-empty Label becomes one cue, timed to that clip's position on the
// COMPILATION timeline (cumulative clip durations), NOT to its source position.
// This is what makes the exported .srt line up with the exported .mp4: as the
// compilation plays, the cue for clip N appears exactly while clip N is on
// screen. (The forensic lower-third, by contrast, shows the ORIGINAL source
// timecode — the two serve different purposes; see SPEC §5.)
//
// Clips without a Label are still advanced over on the timeline (their duration
// counts) but emit no cue, so cue indices may skip clips while the timing stays
// correct. A zero-duration clip is given a tiny visible window so the cue is not
// start==end.
func WriteSRT(w io.Writer, r Reel) error {
	bw := bufio.NewWriter(w)

	var cursor float64 // running compilation position (seconds)
	idx := 0
	for _, c := range r.Clips {
		start := cursor
		cursor += c.Dur()
		end := cursor
		if c.Label == "" {
			continue
		}
		if end <= start {
			// Zero-length clip: still show the label briefly so the cue is valid.
			end = start + minCueSeconds
		}
		idx++
		fmt.Fprintf(bw, "%d\r\n", idx)
		fmt.Fprintf(bw, "%s --> %s\r\n", secondsToSRTTime(start), secondsToSRTTime(end))
		fmt.Fprintf(bw, "%s\r\n\r\n", oneLine(c.Label))
	}

	return bw.Flush()
}

// minCueSeconds is the fallback display window for a zero-duration clip's cue.
const minCueSeconds = 0.5
