// gate.go — post-synthesis honesty gates for becky-validate observations.
//
// Two gates run after the model produces observations, before they are emitted:
//
//  1. gateContactFrames: every physical_contact / possible_contact observation
//     MUST link to a real frame image a human can open. The synthesis is asked to
//     cite the frame file(s) for any contact; here we resolve those citations to
//     actual extracted-frame paths (by file name, then by timestamp fallback). A
//     contact observation we cannot link to a frame is DOWNGRADED to a plain
//     "visual" observation with a note — never emitted as an unverifiable contact
//     claim. (The directive: never assert contact without a linked frame.)
//
//  2. suppressToneOnSilence: when VAD shows ~no speech, any asserted audio_tone
//     ("subdued / deliberate") is a hallucination on silence; we blank it to a
//     clear "no speech detected" marker so no tone is claimed.
//
// Both gates are conservative: they never invent contact and never strip tone
// from a clip that actually has speech.
package main

import (
	"fmt"
	"path/filepath"
	"strings"

	"becky-go/internal/avlm"
)

// contactTypes are the observation types that require a linked frame.
var contactTypes = map[string]bool{
	"physical_contact": true,
	"possible_contact": true,
}

// gateContactFrames resolves each contact observation's cited frames to real
// frame-image paths and downgrades any contact claim that cannot be linked to a
// frame. captions carries the timestamp+frame-path for every captioned frame.
func gateContactFrames(obs []Observation, captions []avlm.FrameCaption) []Observation {
	byName := make(map[string]string, len(captions))
	for _, c := range captions {
		if c.Frame == "" {
			continue
		}
		byName[strings.ToLower(filepath.Base(c.Frame))] = c.Frame
	}

	out := make([]Observation, 0, len(obs))
	for _, o := range obs {
		if !contactTypes[o.Type] {
			// Non-contact observations pass through; still resolve any cited
			// frames to full paths for convenience (best-effort).
			o.Frames = nonNil(resolveFrameRefs(o.Frames, byName))
			out = append(out, o)
			continue
		}

		resolved := resolveFrameRefs(o.Frames, byName)
		if len(resolved) == 0 {
			// No cited frame resolved — try the timestamp fallback so a genuine
			// contact the model simply forgot to cite is still linkable.
			resolved = framesInWindow(captions, o.SegmentStart, o.SegmentEnd)
		}
		if len(resolved) == 0 {
			// Still nothing to link: do NOT emit an unverifiable contact claim.
			// Downgrade to a plain visual observation, preserving the text for
			// recall but removing the contact typing and significance.
			out = append(out, downgradeUnlinkedContact(o))
			continue
		}
		o.Frames = resolved
		out = append(out, o)
	}
	return out
}

// nonNil guarantees a non-nil slice so the JSON "frames" field marshals as []
// rather than null.
func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

// resolveFrameRefs maps cited frame names (possibly bare base names or noisy
// strings the model emitted) to the real extracted-frame paths.
func resolveFrameRefs(refs []string, byName map[string]string) []string {
	if len(refs) == 0 {
		return nil
	}
	seen := make(map[string]bool)
	var out []string
	for _, ref := range refs {
		base := strings.ToLower(strings.TrimSpace(filepath.Base(ref)))
		if base == "" {
			continue
		}
		if full, ok := byName[base]; ok && !seen[full] {
			seen[full] = true
			out = append(out, full)
		}
	}
	return out
}

// framesInWindow returns the real frame paths whose timestamps fall in
// [start, end] (inclusive, with a small tolerance). Used as a fallback when the
// model cited a contact but no resolvable frame name.
func framesInWindow(captions []avlm.FrameCaption, start, end float64) []string {
	if end < start {
		start, end = end, start
	}
	const tol = 0.51 // half a 1fps step plus epsilon, so an integer second lands
	var out []string
	for _, c := range captions {
		if c.Frame == "" {
			continue
		}
		if c.Timestamp >= start-tol && c.Timestamp <= end+tol {
			out = append(out, c.Frame)
		}
	}
	return out
}

// downgradeUnlinkedContact turns an unverifiable contact observation into a
// plain visual observation, noting in the rationale why it was downgraded.
func downgradeUnlinkedContact(o Observation) Observation {
	note := "Downgraded from " + o.Type + " to visual: no frame image could be linked to verify the contact."
	o.Type = "visual"
	o.Significance = "low"
	o.Frames = []string{}
	if o.Rationale == "" {
		o.Rationale = note
	} else {
		o.Rationale = o.Rationale + " [" + note + "]"
	}
	return o
}

// suppressToneOnSilence blanks any asserted audio_tone when the clip is
// effectively silent — too little of it is speech (pct below the floor) OR too
// few absolute speech-seconds to judge tone. A verdict with Known=false (VAD
// could not run) leaves observations untouched. It also clears a tone-vs-content
// mismatch flag that was based on a hallucinated tone.
func suppressToneOnSilence(obs []Observation, sp speechStat) []Observation {
	if !sp.Known {
		return obs
	}
	if sp.Pct >= minSpeechPctForTone && sp.Seconds >= minSpeechSecForTone {
		return obs // enough speech to assess tone — leave it alone
	}
	marker := fmt.Sprintf("n/a — too little speech to assess tone (VAD: %.1f%%, %.2fs)", sp.Pct, sp.Seconds)
	out := make([]Observation, 0, len(obs))
	for _, o := range obs {
		o.AudioTone = marker
		// A tone-vs-content judgment on silence is meaningless: treat as
		// not-applicable (true = no conflict) rather than a flagged mismatch.
		t := true
		o.ToneContentMatch = &t
		out = append(out, o)
	}
	return out
}
