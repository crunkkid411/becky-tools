package edl

import (
	"bufio"
	"fmt"
	"io"
	"strings"

	"becky-go/internal/pathx"
)

// WriteEDL emits a CMX3600 EDL for the reel. This is a cuts-only forensic
// compilation, so every event is a video cut ("V  C"). Per the CMX3600 format
// and R-REUSE.md's gotchas:
//
//   - Reel names are limited to 8 chars. Long/duplicate source basenames are
//     mapped to a stable reel table (BL000001, BL000002, …) and the full source
//     name is emitted as a "* FROM CLIP NAME:" comment above each event.
//   - Source in/out are timecodes at THAT clip's source fps (Clip.FPS) — the
//     position inside the original file.
//   - Record in/out are the running COMPILATION timecode (cumulative clip
//     durations), computed at a single record fps so the record track is
//     contiguous.
//   - A "FCM: NON-DROP FRAME" line precedes the events (we emit non-drop;
//     SecondsToTimecode uses integer-fps non-drop labelling).
//
// The record-track fps is the first clip's effective fps (fallback DefaultFPS).
// Mixed-fps sources still get correct PER-CLIP source timecodes; the record
// column is a monotonic timeline reference, which is all an EDL needs here.
//
// pathx.Base (not filepath.Base) derives the basename so a Windows source path
// is handled correctly even when the tool runs on Linux/CI.
func WriteEDL(w io.Writer, r Reel) error {
	bw := bufio.NewWriter(w)

	title := strings.TrimSpace(r.Name)
	if title == "" {
		title = "BECKY-REEL"
	}
	fmt.Fprintf(bw, "TITLE: %s\r\n", sanitizeTitle(title))
	fmt.Fprintf(bw, "FCM: NON-DROP FRAME\r\n")

	recFPS := DefaultFPS
	if len(r.Clips) > 0 {
		recFPS = r.Clips[0].FPS(DefaultFPS)
	}

	reels := newReelTable()
	var recCursor float64 // running position on the compilation timeline (seconds)

	for i, c := range r.Clips {
		srcFPS := c.FPS(DefaultFPS)
		reel := reels.nameFor(c.Source)

		srcIn := SecondsToTimecode(c.In, srcFPS)
		srcOut := SecondsToTimecode(c.Out, srcFPS)
		recIn := SecondsToTimecode(recCursor, recFPS)
		recCursor += c.Dur()
		recOut := SecondsToTimecode(recCursor, recFPS)

		// Event line: NNN<sp><sp>REEL<sp...>AA/V<sp...>C<sp...>srcIn srcOut recIn recOut
		// Channel "AA/V" = video + 2-channel audio (the standard CMX3600 designator
		// for a normal sync-sound cut). A bare "V" (video-only) is why an EDL import
		// into Vegas Pro carried no audio track — see internal/edl/edl_test.go.
		event := i + 1
		fmt.Fprintf(bw, "%03d  %-8s AA/V  C        %s %s %s %s\r\n",
			event, reel, srcIn, srcOut, recIn, recOut)

		// "* FROM CLIP NAME:" comment carries the full original basename so the
		// 8-char reel alias can be resolved back to the real file.
		name := pathx.Base(c.Source)
		if name == "" {
			name = c.Source
		}
		fmt.Fprintf(bw, "* FROM CLIP NAME: %s\r\n", name)
		if c.Label != "" {
			fmt.Fprintf(bw, "* COMMENT: %s\r\n", oneLine(c.Label))
		}
	}

	return bw.Flush()
}

// reelTable maps source paths to stable 8-char CMX3600 reel names. The first
// time a source is seen it gets the next BLnnnnnn alias; repeats reuse it.
type reelTable struct {
	bySource map[string]string
	next     int
}

func newReelTable() *reelTable {
	return &reelTable{bySource: make(map[string]string)}
}

// nameFor returns a stable <=8-char reel name for the given source path. We use
// "BLnnnnnn" (BL = a neutral tape/reel prefix) so the name is always exactly 8
// chars, unique, and free of the spacing/charset issues a raw filename causes in
// CMX3600's fixed-width reel column.
func (t *reelTable) nameFor(source string) string {
	if name, ok := t.bySource[source]; ok {
		return name
	}
	t.next++
	name := fmt.Sprintf("BL%06d", t.next)
	t.bySource[source] = name
	return name
}

// sanitizeTitle keeps the TITLE line single-line and within a sane width.
func sanitizeTitle(s string) string {
	s = oneLine(s)
	if len(s) > 70 {
		s = s[:70]
	}
	return s
}

// oneLine flattens CR/LF/tabs to single spaces so a label can't break the
// line-oriented EDL/SRT formats.
func oneLine(s string) string {
	s = strings.ReplaceAll(s, "\r\n", " ")
	s = strings.ReplaceAll(s, "\n", " ")
	s = strings.ReplaceAll(s, "\r", " ")
	s = strings.ReplaceAll(s, "\t", " ")
	return strings.TrimSpace(s)
}
