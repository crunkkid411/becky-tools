package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
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
// gap between spans (diarize trims pauses) takes the nearest span's speaker. When two voices cover
// the word equally (Nemotron marks overlapping speech, both people talking at once), prev — the
// previous word's speaker — keeps it, so a line doesn't flip back and forth on a tie. Same for the
// nearest-span pick, which zero-length ASR words (start == end) always go through.
func speakerAt(spans []speakerSpan, start, end float64, prev string) string {
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
	if bestO > 0 && overlap[prev] == bestO {
		return prev
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
		if bestD < 0 || d < bestD || (d == bestD && s.Speaker == prev) {
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

// SpeakerFix is one word the wording-based double-check (becky-diarfix) moved to another speaker.
type SpeakerFix struct {
	Start float64 `json:"start"`
	End   float64 `json:"end"`
	Word  string  `json:"word"`
	From  string  `json:"from"`
	To    string  `json:"to"`
}

// runDiarFix is the seam to becky-diarfix (swapped in tests): it gets a JSON file of the labelled
// words and returns the tool's JSON.
var runDiarFix = func(wordsJSON string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), diarizeTimeout)
	defer cancel()
	return forensicrun.RunTool(ctx, "becky-diarfix", wordsJSON)
}

// checkSpeakers asks becky-diarfix (DiarizationLM-Gemma-4-E4B) to re-read the words around every
// speaker change, where the sound-only labels are least sure, and applies its corrections in place.
// It returns what changed plus one plain-words line. Degrade, never crash: on any failure the
// sound-only labels stay and the line says so.
func checkSpeakers(words []Word) ([]SpeakerFix, string) {
	tmp, err := os.CreateTemp("", "becky_diarfix_*.json")
	if err != nil {
		return nil, "speaker double-check skipped: " + err.Error()
	}
	defer os.Remove(tmp.Name())
	err = json.NewEncoder(tmp).Encode(map[string]any{"words": words})
	tmp.Close()
	if err != nil {
		return nil, "speaker double-check skipped: " + err.Error()
	}
	raw, err := runDiarFix(tmp.Name())
	if err != nil {
		return nil, "speaker double-check skipped (labels are from the sound only): " + err.Error()
	}
	var res struct {
		Speakers    []string     `json:"speakers"`
		Fixes       []SpeakerFix `json:"fixes"`
		UnsureWords int          `json:"unsure_words"`
		Note        string       `json:"note"`
	}
	if err := json.Unmarshal(raw, &res); err != nil || len(res.Speakers) != len(words) {
		return nil, "speaker double-check skipped: could not read becky-diarfix output"
	}
	for i := range words {
		words[i].Speaker = res.Speakers[i] // never a new speaker: becky-diarfix only moves words between existing ones
	}
	if res.Note != "" { // skipped, or stopped part-way (any fixes made before that are kept)
		return res.Fixes, res.Note
	}
	return res.Fixes, fmt.Sprintf("DiarizationLM re-read the %d words near speaker changes and moved %d of them to the other speaker",
		res.UnsureWords, len(res.Fixes))
}

// labelWords stamps each word with its speaker and returns how many distinct speakers were used.
func labelWords(words []Word, spans []speakerSpan) int {
	seen := map[string]bool{}
	prev := ""
	for i := range words {
		words[i].Speaker = speakerAt(spans, words[i].Start, words[i].End, prev)
		prev = words[i].Speaker
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
