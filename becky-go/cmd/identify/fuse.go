// fuse.go — corroborated-identity fusion (the core of the 2026-06-08 forensic
// philosophy: combine multiple data points, score confidence, and CONCLUDE).
//
// becky-identify runs three INDEPENDENT modalities (voice, face, location). Before
// this pass each produced its own hedged entry, so the same person showed up two or
// three times ("voice Shelby 0.80", "face Shelby 0.68") and a human had to fuse the
// signals by hand. That is exactly the failure the philosophy forbids: the tool must
// fuse the signals and state the name plainly.
//
// Rules (FORENSIC-OUTPUT-PHILOSOPHY.md TOP PRINCIPLE):
//   - MULTIPLE independent signals agreeing on one person -> ONE confident
//     "corroborated" identification, name stated plainly, with a combined
//     confidence and the list of corroborating signals attached.
//   - ONE strong signal from a RELIABLE modality (voice — the docs say voice beats
//     face) -> still a named identification (voice has always been the trusted path).
//   - ONE weak / single signal from a less-reliable modality (a lone face match, a
//     lone location hit, or a voice match below the strong floor) -> NOT an
//     identification. It becomes a CANDIDATE in unidentified[] with the basis, never
//     a confidently named entry. A lone 0.50 face match is not Shelby.
//
// The math is deterministic and lives here (testable). No LLM.
package main

import (
	"fmt"
	"sort"
)

// Fusion thresholds. These encode "don't name from one thin signal, but DO conclude
// when several agree". They are intentionally conservative on the single-signal path
// and generous on the corroborated path.
const (
	// voiceSoloFloor: a LONE voice match must clear this to stand as a named
	// identification on its own. Voice is the reliable modality, and same-person
	// CAM++ cosine runs ~0.74-0.84 on real clips while a cross-person hit sits far
	// lower, so 0.62 keeps genuine solo-voice naming while refusing a borderline one.
	voiceSoloFloor = 0.62

	// corroborateMinPerSignal: when two+ modalities agree, each contributing signal
	// only needs to clear this lower bar — corroboration IS the precision, so a face
	// at 0.60 + a voice at 0.74 together name the person even though neither alone
	// would. Set just below the per-modality naming thresholds so a real second
	// signal counts without admitting noise.
	corroborateMinPerSignal = 0.45

	// faceSoloFloor: a LONE face match must be this strong to stand as a named (single-
	// modality) identification. The philosophy says "don't name from one THIN match" —
	// but a 0.55 face (the bare naming threshold) is thin, while a 0.78+ face is a
	// confident visual recognition, not a guess. So a borderline lone face becomes a
	// candidate, but a strong lone face is named (clearly typed "face", single-signal).
	// Set above the cross-person false-match band (~0.50-0.68 observed on the contact
	// clip's strangers) so an unenrolled lookalike never clears it.
	faceSoloFloor = 0.78
)

// signal is one modality's contribution to a person's identity (the audit trail
// behind a fused conclusion).
type signal struct {
	Type       string  `json:"type"`       // voice | face | location
	Confidence float64 `json:"confidence"` // that modality's raw cosine/score
	SpeakerID  string  `json:"speaker_id,omitempty"`
	Hamming    *int    `json:"hamming,omitempty"`
}

// fuseIdentifications collapses the raw per-modality identifications into the final,
// corroboration-aware identifications[] + unidentified[]. The raw unidentified
// entries from each modality are preserved and appended (single weak signals demoted
// to candidates land here too).
func fuseIdentifications(rawIDs []Identification, rawUnids []Unidentified) ([]Identification, []Unidentified) {
	byPerson := groupSignalsByPerson(rawIDs)

	var fused []Identification
	var candidates []Unidentified

	names := make([]string, 0, len(byPerson))
	for name := range byPerson {
		names = append(names, name)
	}
	sort.Strings(names)

	for _, name := range names {
		sigs := byPerson[name]
		entry, demoted := fusePerson(name, sigs)
		if demoted != nil {
			candidates = append(candidates, *demoted)
			continue
		}
		fused = append(fused, entry)
	}

	// Highest combined confidence first so the strongest conclusions read at the top.
	sort.SliceStable(fused, func(i, j int) bool { return fused[i].Confidence > fused[j].Confidence })

	out := append([]Unidentified{}, rawUnids...)
	out = append(out, candidates...)
	if fused == nil {
		fused = []Identification{}
	}
	return fused, out
}

// personSignals carries the raw signals plus the representative segments/frames so a
// fused entry can still point back to where the person was seen/heard.
type personSignals struct {
	signals  []signal
	segments []SpeakerSpan // from the voice signal (best-confidence one)
	frames   []FrameRef    // from the face/location signal (best-confidence one)
	bestVoiceConf,
	bestFaceConf,
	bestLocConf float64
}

// groupSignalsByPerson buckets every raw identification under its person name,
// keeping the best segments/frames and per-modality peak confidence.
func groupSignalsByPerson(rawIDs []Identification) map[string]*personSignals {
	byPerson := map[string]*personSignals{}
	for _, id := range rawIDs {
		ps := byPerson[id.Name]
		if ps == nil {
			ps = &personSignals{}
			byPerson[id.Name] = ps
		}
		ps.signals = append(ps.signals, signal{
			Type:       id.Type,
			Confidence: id.Confidence,
			SpeakerID:  id.SpeakerID,
			Hamming:    id.Hamming,
		})
		switch id.Type {
		case "voice":
			if id.Confidence >= ps.bestVoiceConf {
				ps.bestVoiceConf = id.Confidence
				ps.segments = id.Segments
			}
		case "face":
			if id.Confidence >= ps.bestFaceConf {
				ps.bestFaceConf = id.Confidence
				ps.frames = id.Frames
			}
		case "location":
			if id.Confidence >= ps.bestLocConf {
				ps.bestLocConf = id.Confidence
				if len(id.Frames) > 0 {
					ps.frames = id.Frames
				}
			}
		}
	}
	return byPerson
}

// fusePerson applies the corroboration rules to one person's collected signals and
// returns EITHER a fused identification OR a demoted candidate (never both).
func fusePerson(name string, ps *personSignals) (Identification, *Unidentified) {
	strong := strongSignals(ps.signals)
	modalities := distinctModalities(strong)

	// Multiple INDEPENDENT modalities agree -> CONCLUDE, plainly, with evidence.
	if len(modalities) >= 2 {
		return corroboratedEntry(name, ps, strong, modalities), nil
	}

	// Exactly one modality named this person. A strong solo from a RELIABLE-ENOUGH
	// modality still stands (voice is the most trusted; a very strong face is a
	// confident visual ID) — but a borderline lone signal is demoted to a candidate.
	if ps.bestVoiceConf >= voiceSoloFloor && hasModality(strong, "voice") {
		return soloVoiceEntry(name, ps), nil
	}
	if ps.bestFaceConf >= faceSoloFloor && hasModality(strong, "face") {
		return soloFaceEntry(name, ps), nil
	}

	// A lone face / location / weak-voice signal below its solo floor is NOT an
	// identification: demote it to a candidate with its basis so a human can see why
	// it didn't rise to a name. (A 0.55 lone face is not a conclusion.)
	return Identification{}, demoteToCandidate(name, ps)
}

// strongSignals keeps only signals clearing the per-signal corroboration floor, so a
// near-zero noise hit can never act as a corroborating second modality.
func strongSignals(sigs []signal) []signal {
	var out []signal
	for _, s := range sigs {
		if s.Confidence >= corroborateMinPerSignal {
			out = append(out, s)
		}
	}
	return out
}

// distinctModalities returns the set of modality types present (voice/face/location).
func distinctModalities(sigs []signal) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range sigs {
		if !seen[s.Type] {
			seen[s.Type] = true
			out = append(out, s.Type)
		}
	}
	sort.Strings(out)
	return out
}

func hasModality(sigs []signal, typ string) bool {
	for _, s := range sigs {
		if s.Type == typ {
			return true
		}
	}
	return false
}

// corroboratedEntry builds the fused identification when 2+ modalities agree. The
// combined confidence uses a noisy-OR over the per-modality peaks: independent
// signals reinforce each other, so two 0.7s read higher than either alone, but it
// never exceeds 1.0. This is the "corroboration IS the precision" rule made numeric.
func corroboratedEntry(name string, ps *personSignals, strong []signal, modalities []string) Identification {
	combined := combinedConfidence(ps)
	return Identification{
		Type:           "corroborated",
		Name:           name,
		Confidence:     round4(combined),
		Match:          "corroborated",
		CorroboratedBy: modalities,
		Signals:        sortSignals(strong),
		Segments:       ps.segments,
		Frames:         ps.frames,
		SpeakerID:      representativeSpeaker(strong),
	}
}

// soloVoiceEntry keeps a strong lone-voice match as a named voice identification
// (voice is the trusted modality). It still carries the single signal for audit.
func soloVoiceEntry(name string, ps *personSignals) Identification {
	return Identification{
		Type:           "voice",
		Name:           name,
		Confidence:     round4(ps.bestVoiceConf),
		Match:          "cosine",
		CorroboratedBy: []string{"voice"},
		Signals:        sortSignals(strongSignals(ps.signals)),
		Segments:       ps.segments,
		SpeakerID:      representativeSpeaker(ps.signals),
	}
}

// soloFaceEntry keeps a STRONG lone-face match as a named face identification. It is
// clearly typed "face" (single modality) so the reader knows it rests on the picture
// alone — but a 0.78+ face is a confident visual recognition, not a thin guess.
func soloFaceEntry(name string, ps *personSignals) Identification {
	return Identification{
		Type:           "face",
		Name:           name,
		Confidence:     round4(ps.bestFaceConf),
		Match:          "cosine",
		CorroboratedBy: []string{"face"},
		Signals:        sortSignals(strongSignals(ps.signals)),
		Frames:         ps.frames,
	}
}

// demoteToCandidate turns a single weak signal into an unidentified CANDIDATE with a
// plain-English basis (so the human sees the near-miss without it being asserted).
func demoteToCandidate(name string, ps *personSignals) *Unidentified {
	best, modality := bestSignal(ps.signals)
	desc := fmt.Sprintf("possible %s (%s match %.2f) — single signal below the naming threshold, not confirmed",
		name, modality, best)
	return &Unidentified{
		Type:        modality,
		SpeakerID:   representativeSpeaker(ps.signals),
		Description: desc,
		Confidence:  round4(best),
		Candidate:   name,
	}
}

// combinedConfidence is a noisy-OR fusion of the per-modality peak confidences:
// 1 - prod(1 - c_i). Independent agreeing signals raise certainty; a single signal
// returns itself. Bounded to [0,1].
func combinedConfidence(ps *personSignals) float64 {
	confs := []float64{}
	for _, c := range []float64{ps.bestVoiceConf, ps.bestFaceConf, ps.bestLocConf} {
		if c > 0 {
			confs = append(confs, c)
		}
	}
	if len(confs) == 0 {
		return 0
	}
	prod := 1.0
	for _, c := range confs {
		prod *= (1.0 - clamp01(c))
	}
	return clamp01(1.0 - prod)
}

// bestSignal returns the highest-confidence signal's value and modality.
func bestSignal(sigs []signal) (float64, string) {
	best, modality := 0.0, "unknown"
	for _, s := range sigs {
		if s.Confidence > best {
			best, modality = s.Confidence, s.Type
		}
	}
	return best, modality
}

// representativeSpeaker returns a speaker_id from the signals (voice carries it),
// preferring the highest-confidence voice signal so the fused entry stays linkable to
// a diarized speaker for downstream consumers.
func representativeSpeaker(sigs []signal) string {
	best, id := -1.0, ""
	for _, s := range sigs {
		if s.Type == "voice" && s.SpeakerID != "" && s.Confidence > best {
			best, id = s.Confidence, s.SpeakerID
		}
	}
	return id
}

// sortSignals returns the signals ordered by confidence (desc) for a stable, readable
// audit list.
func sortSignals(sigs []signal) []signal {
	out := append([]signal{}, sigs...)
	sort.SliceStable(out, func(i, j int) bool { return out[i].Confidence > out[j].Confidence })
	return out
}

func clamp01(f float64) float64 {
	if f < 0 {
		return 0
	}
	if f > 1 {
		return 1
	}
	return f
}
