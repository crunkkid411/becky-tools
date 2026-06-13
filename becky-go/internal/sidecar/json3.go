// json3 parsing for the sidecar package. yt-dlp writes word-level timed subtitles
// in YouTube's "json3" format when invoked with --sub-format json3. The schema:
//
//	{"events":[
//	   {"tStartMs":2800,"dDurationMs":4479,
//	    "segs":[{"utf8":"Boom","tOffsetMs":0},{"utf8":" there","tOffsetMs":200}]},
//	   ...]}
//
// Each event is a caption window; its segs are the words (with per-word offsets).
// We collapse each event into one {start,end,text} caption segment (the
// becky-transcribe shape), concatenating the word utf8 fields. Pure-newline
// segs and paint-on events with no real text are skipped. json3's advantage over
// srt/vtt is accurate per-window timing without the rolling-caption duplication.
package sidecar

import (
	"encoding/json"
	"strings"
)

// json3Doc is the subset of the json3 schema we consume.
type json3Doc struct {
	Events []json3Event `json:"events"`
}

type json3Event struct {
	TStartMs   *int64     `json:"tStartMs"`
	DDurationM *int64     `json:"dDurationMs"`
	Segs       []json3Seg `json:"segs"`
}

type json3Seg struct {
	UTF8      string `json:"utf8"`
	TOffsetMs *int64 `json:"tOffsetMs"`
}

// parseJSON3 turns json3 bytes into ordered raw segments (one per real event).
// Returns nil on malformed input (callers degrade gracefully).
func parseJSON3(data []byte) []Segment {
	var doc json3Doc
	if err := json.Unmarshal(data, &doc); err != nil {
		return nil
	}
	var segs []Segment
	for _, ev := range doc.Events {
		if ev.TStartMs == nil {
			continue // style/format event without timing
		}
		var b strings.Builder
		for _, s := range ev.Segs {
			b.WriteString(s.UTF8)
		}
		text := cleanCaptionText(b.String())
		if text == "" {
			continue // newline-only or empty paint event
		}
		start := float64(*ev.TStartMs) / 1000.0
		dur := int64(0)
		if ev.DDurationM != nil {
			dur = *ev.DDurationM
		}
		end := start + float64(dur)/1000.0
		segs = append(segs, Segment{Start: start, End: end, Text: text})
	}
	return segs
}
