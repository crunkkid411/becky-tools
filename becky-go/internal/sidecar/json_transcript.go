package sidecar

// json_transcript.go parses becky-transcribe's own JSON output (cmd/transcribe's
// Output struct) into the same {start,end,text} Segment shape the .srt/.vtt path
// yields, so a plain "<stem>.json" (or "<stem>.transcript.json") transcribe result
// is a first-class transcript: the left panel reads "transcribed", cue scrubbing
// works, and the caption lane derives from it — exactly like an .srt.
//
// becky-transcribe writes (cmd/transcribe/main.go Output):
//
//	{ "file":..., "duration":..., "text":..., "words":[{word,start,end,confidence}],
//	  "segments":[{start,end,text}], ... }
//
// Segments map 1:1 onto Segment. When a file carries word-level timing but no
// segments (a words-only export), the words are grouped into caption-sized
// segments with the same pause/length rule cmd/transcribe uses, so nothing is lost.
//
// A .json that is NOT a transcript (a reel, meta, questions, or capstyle sidecar)
// carries neither "segments" nor "words" in this shape, so it fails validation and
// ParseSubtitle returns an error — the caller degrades to "no cues", never a
// false "transcribed" with an empty scrub list. isBeckyDataJSON below is the first,
// name-based guard so those files are never even offered as a transcript.

import (
	"encoding/json"
	"fmt"
	"strings"
)

// beckyTranscriptWord mirrors one cmd/transcribe Word: the ASR token + its span.
type beckyTranscriptWord struct {
	Word  string  `json:"word"`
	Start float64 `json:"start"`
	End   float64 `json:"end"`
}

// beckyTranscriptJSON is the subset of cmd/transcribe's Output this parser needs.
// Segments is the caption-sized grouping becky-transcribe already computed; Words
// is the word-level fallback for a words-only export.
type beckyTranscriptJSON struct {
	Segments []Segment             `json:"segments"`
	Words    []beckyTranscriptWord `json:"words"`
}

// wordGapNewSegment / wordSegmentMaxChars mirror cmd/transcribe's segmentize rule
// (a 0.6s pause OR ~80 chars starts a new caption) so a words-only transcript
// groups the same way the normal segmented output would have.
const (
	wordGapNewSegment   = 0.6
	wordSegmentMaxChars = 80
)

// parseBeckyTranscriptJSON parses a becky-transcribe JSON file into ordered
// segments. It returns an error when the payload is not a transcript (no segments
// and no words), so a reel/meta/questions .json degrades cleanly rather than
// masquerading as an empty transcript.
func parseBeckyTranscriptJSON(data []byte) ([]Segment, error) {
	var t beckyTranscriptJSON
	if err := json.Unmarshal(data, &t); err != nil {
		return nil, fmt.Errorf("not a becky transcript json: %w", err)
	}

	// Prefer the segments becky-transcribe already grouped.
	if len(t.Segments) > 0 {
		segs := make([]Segment, 0, len(t.Segments))
		for _, s := range t.Segments {
			text := cleanCaptionText(s.Text)
			if text == "" || s.End < s.Start {
				continue
			}
			segs = append(segs, Segment{Start: s.Start, End: s.End, Text: text})
		}
		if len(segs) > 0 {
			return segs, nil
		}
	}

	// Words-only fallback: group into caption-sized segments.
	if len(t.Words) > 0 {
		if segs := groupWordsIntoSegments(t.Words); len(segs) > 0 {
			return segs, nil
		}
	}

	return nil, fmt.Errorf("not a becky transcript json: no segments or words")
}

// groupWordsIntoSegments turns a flat word list into caption-sized segments,
// breaking on a >0.6s pause or ~80 characters — the same rule cmd/transcribe's
// segmentize uses, so a words-only export reads like normal segmented output.
func groupWordsIntoSegments(words []beckyTranscriptWord) []Segment {
	var out []Segment
	var cur []string
	var curStart, prevEnd float64
	flush := func(end float64) {
		if len(cur) == 0 {
			return
		}
		text := cleanCaptionText(strings.Join(cur, " "))
		if text != "" && end >= curStart {
			out = append(out, Segment{Start: curStart, End: end, Text: text})
		}
		cur = nil
	}
	for _, w := range words {
		tok := strings.TrimSpace(w.Word)
		if tok == "" {
			continue
		}
		if len(cur) == 0 {
			curStart = w.Start
		} else if w.Start-prevEnd > wordGapNewSegment || segTextLen(cur) >= wordSegmentMaxChars {
			flush(prevEnd)
			curStart = w.Start
		}
		cur = append(cur, tok)
		prevEnd = w.End
	}
	flush(prevEnd)
	return out
}

// segTextLen is the space-joined length of the words accumulated so far.
func segTextLen(words []string) int {
	n := 0
	for _, w := range words {
		n += len(w) + 1
	}
	if n > 0 {
		n--
	}
	return n
}

// IsBeckyDataJSON reports whether a .json file name is one of becky's OWN data
// sidecars (never a transcript) — a metadata/reel/questions/capstyle/yt-dlp file
// that happens to end in ".json". This is the name-based guard that keeps the
// widened discovery (which now accepts ".json") from mis-pairing e.g.
// "ring.mp4.beckymeta.json" as ring.mp4's transcript. Add any new becky .json
// sidecar suffix here. Compared case-insensitively.
//
// NOT excluded: ".json3" (a real word-level subtitle format, handled separately).
func IsBeckyDataJSON(name string) bool {
	ln := strings.ToLower(name)
	for _, suf := range []string{
		".beckymeta.json",
		".info.json",
		".live_chat.json",
		".reel.json",
		".questions.json",
		".capstyle.json",
		".forensic.json",
		"_forensic_answers.json",
	} {
		if strings.HasSuffix(ln, suf) {
			return true
		}
	}
	return false
}
