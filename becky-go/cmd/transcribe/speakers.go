package main

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"becky-go/internal/forensicrun"
)

// diarizeTimeout bounds the speaker pass; a multi-hour file diarizes in minutes, not hours.
const diarizeTimeout = 60 * time.Minute

// speakerSpan is one "who talks when" span from becky-diarize.
type speakerSpan struct {
	Start, End float64
	Speaker    string
}

// runDiarize is the seam to becky-diarize (swapped in tests). It returns the tool's JSON.
var runDiarize = func(media string, speakers int) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), diarizeTimeout)
	defer cancel()
	args := []string{media}
	if speakers > 0 {
		n := strconv.Itoa(speakers)
		args = append(args, "--min-speakers", n, "--max-speakers", n)
	}
	return forensicrun.RunTool(ctx, "becky-diarize", args...)
}

// parseDiarize flattens becky-diarize's {speakers:[{id,segments:[{start,end}]}]} into spans.
func parseDiarize(raw []byte) ([]speakerSpan, error) {
	var v struct {
		Speakers []struct {
			ID       string `json:"id"`
			Segments []struct {
				Start float64 `json:"start"`
				End   float64 `json:"end"`
			} `json:"segments"`
		} `json:"speakers"`
	}
	if err := json.Unmarshal(raw, &v); err != nil {
		return nil, fmt.Errorf("could not read becky-diarize output: %v", err)
	}
	var spans []speakerSpan
	for _, s := range v.Speakers {
		for _, seg := range s.Segments {
			spans = append(spans, speakerSpan{Start: seg.Start, End: seg.End, Speaker: s.ID})
		}
	}
	return spans, nil
}

// speakerAt returns the speaker whose spans overlap [start,end] the most; a word that falls in a
// gap between spans (diarize trims pauses) takes the nearest span's speaker.
func speakerAt(spans []speakerSpan, start, end float64) string {
	overlap := map[string]float64{}
	for _, s := range spans {
		if o := minF(end, s.End) - maxF(start, s.Start); o > 0 {
			overlap[s.Speaker] += o
		}
	}
	best, bestO := "", 0.0
	for id, o := range overlap {
		if o > bestO || (o == bestO && id < best) {
			best, bestO = id, o
		}
	}
	if best != "" {
		return best
	}
	mid, bestD := (start+end)/2, -1.0
	for _, s := range spans {
		d := 0.0
		if mid < s.Start {
			d = s.Start - mid
		} else if mid > s.End {
			d = mid - s.End
		}
		if bestD < 0 || d < bestD {
			best, bestD = s.Speaker, d
		}
	}
	return best
}

// labelSpeakers runs becky-diarize on the audio and stamps each word with its speaker. It returns
// the number of speakers heard, or a plain-words note when the speaker pass could not run.
func labelSpeakers(words []Word, media string, speakers int) (int, string) {
	raw, err := runDiarize(media, speakers)
	if err != nil {
		return 0, "could not tell the speakers apart, so lines are not labelled: " + err.Error()
	}
	spans, err := parseDiarize(raw)
	if err != nil {
		return 0, "could not tell the speakers apart, so lines are not labelled: " + err.Error()
	}
	if len(spans) == 0 {
		return 0, "no speech was found to split by speaker"
	}
	return labelWords(words, spans), ""
}

// labelWords stamps each word with its speaker and returns how many distinct speakers were used.
func labelWords(words []Word, spans []speakerSpan) int {
	seen := map[string]bool{}
	for i := range words {
		words[i].Speaker = speakerAt(spans, words[i].Start, words[i].End)
		if words[i].Speaker != "" {
			seen[words[i].Speaker] = true
		}
	}
	return len(seen)
}

// sidecarPath is where the one-call result is saved: <video>.transcript.json beside the video —
// the name becky-moment / becky-clip already look for.
func sidecarPath(input string) string {
	return strings.TrimSuffix(input, filepath.Ext(input)) + ".transcript.json"
}

func minF(a, b float64) float64 {
	if a < b {
		return a
	}
	return b
}

func maxF(a, b float64) float64 {
	if a > b {
		return a
	}
	return b
}
