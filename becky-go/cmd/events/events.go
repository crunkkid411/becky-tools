// events.go — output schema, becky-diarize input parsing, and the deterministic
// speaker-derived event rules for becky-events.
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
)

// Event is one detected "nuance". Fields are omitempty so speaker events and
// location events share the struct without emitting irrelevant zero values.
type Event struct {
	Type        string  `json:"type"`
	Start       float64 `json:"start"`
	End         float64 `json:"end"`
	Duration    float64 `json:"duration,omitempty"`
	SpeakerID   string  `json:"speaker_id,omitempty"`
	Frame       int     `json:"frame,omitempty"`
	Timestamp   float64 `json:"timestamp,omitempty"`
	FaceCount   int     `json:"face_count,omitempty"`
	Hamming     int     `json:"hamming,omitempty"`
	Confidence  float64 `json:"confidence"`
	Description string  `json:"description"`
	OSINTExport string  `json:"osint_export,omitempty"`
	Provenance  string  `json:"provenance,omitempty"`
}

// Output is the becky-events JSON contract. Notes carries graceful-degradation
// markers (e.g. multi_face skipped) without polluting the events array.
type Output struct {
	File     string            `json:"file"`
	Duration float64           `json:"duration"`
	Events   []Event           `json:"events"`
	Notes    map[string]string `json:"notes,omitempty"`
}

// diarSegment / diarSpeaker / diarized mirror the becky-diarize JSON schema.
type diarSegment struct {
	Start      float64 `json:"start"`
	End        float64 `json:"end"`
	Confidence float64 `json:"confidence"`
}

type diarSpeaker struct {
	ID       string        `json:"id"`
	Segments []diarSegment `json:"segments"`
}

type diarized struct {
	File     string        `json:"file"`
	Duration float64       `json:"duration"`
	Speakers []diarSpeaker `json:"speakers"`
}

// loadDiarized reads and validates a becky-diarize JSON file.
func loadDiarized(path string) (diarized, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return diarized{}, fmt.Errorf("read diarized json: %w", err)
	}
	var d diarized
	if err := json.Unmarshal(data, &d); err != nil {
		return diarized{}, fmt.Errorf("parse diarized json: %w", err)
	}
	// An empty speakers array is tolerated: it just yields no speaker-derived
	// events (e.g. silent / speech-less footage), so location_change and
	// multi_face detection can still run. Populated inputs are unaffected.
	return d, nil
}

// speakerEvents finds the dominant speaker (most total speaking time) and turns
// every non-dominant turn into an event: <= phoneMax seconds -> phone_call,
// longer -> second_speaker. Returns the events and the dominant speaker id.
func speakerEvents(d diarized, phoneMax float64) ([]Event, string) {
	dominant := dominantSpeaker(d)
	var events []Event
	for _, sp := range d.Speakers {
		if sp.ID == dominant {
			continue
		}
		for _, seg := range sp.Segments {
			dur := seg.End - seg.Start
			if dur <= 0 {
				continue
			}
			events = append(events, speakerTurnEvent(sp.ID, seg, dur, phoneMax))
		}
	}
	sort.SliceStable(events, func(i, j int) bool { return events[i].Start < events[j].Start })
	return events, dominant
}

// speakerTurnEvent classifies one non-dominant turn as phone_call or second_speaker.
func speakerTurnEvent(speakerID string, seg diarSegment, dur, phoneMax float64) Event {
	ev := Event{
		Start:      round3(seg.Start),
		End:        round3(seg.End),
		Duration:   round3(dur),
		SpeakerID:  speakerID,
		Confidence: speakerConfidence(seg.Confidence),
	}
	if dur <= phoneMax {
		ev.Type = "phone_call"
		ev.Description = fmt.Sprintf("Short second-speaker segment (likely phone call): %s", speakerID)
	} else {
		ev.Type = "second_speaker"
		ev.Description = fmt.Sprintf("Second speaker detected (not dominant): %s", speakerID)
	}
	return ev
}

// dominantSpeaker returns the id with the most total speaking time. Ties break
// on speaker id for determinism.
func dominantSpeaker(d diarized) string {
	bestID := ""
	bestTotal := -1.0
	for _, sp := range d.Speakers {
		var total float64
		for _, seg := range sp.Segments {
			if dur := seg.End - seg.Start; dur > 0 {
				total += dur
			}
		}
		if total > bestTotal || (total == bestTotal && sp.ID < bestID) {
			bestTotal = total
			bestID = sp.ID
		}
	}
	return bestID
}

// speakerConfidence derives a 0-1 score for a flagged turn. Diarize emits hard
// 1.0 assignments, so we report a conservative fixed confidence that the turn is
// a genuine non-dominant speaker rather than a calibrated probability.
func speakerConfidence(segConf float64) float64 {
	const base = 0.75
	if segConf > 0 && segConf < 1 {
		return round3(base * segConf)
	}
	return base
}

// locationConfidence scales with how far the Hamming distance exceeds the
// threshold, capped at 0.99. A bigger jump is a more confident location change.
func locationConfidence(ham, threshold int) float64 {
	if threshold <= 0 {
		threshold = 1
	}
	over := float64(ham-threshold) / float64(64-threshold)
	conf := 0.6 + 0.39*over
	if conf > 0.99 {
		conf = 0.99
	}
	if conf < 0.6 {
		conf = 0.6
	}
	return round3(conf)
}

func marshalIndent(o Output) ([]byte, error) {
	b, err := json.MarshalIndent(o, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("marshal output: %w", err)
	}
	return append(b, '\n'), nil
}
