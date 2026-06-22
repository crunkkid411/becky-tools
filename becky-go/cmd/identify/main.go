// becky-identify — match voices, faces, and locations in a video against an
// enrolled knowledge base, emitting NAMED identifications ("Defendant", not
// "Speaker 1") with confidence, plus an unidentified[] list for non-matches.
//
//	becky-identify <video> --kb <knowledge-base-dir> [options]
//
// Deterministic matching only — no LLM. Three modalities:
//
//  1. voice  — diarize the clip (reuse becky-diarize, or --diarized to skip),
//     concat each speaker's audio, extract a CAM++ 192-dim embedding (sherpa-onnx),
//     and cosine-match against enrolled <kb>/voice-prints/<name>/*.wav. Best match
//     >= --voice-threshold names the speaker; below threshold -> unidentified[].
//  2. location — sample frames at ~1 fps, aHash them (shared internal/osintexport),
//     and Hamming-match against enrolled <kb>/locations/<name>/ frames or hashes.
//  3. face — DEGRADES GRACEFULLY: no ArcFace model ships here, so face matching is
//     skipped and surfaced as a note. The code is structured so a backend can plug
//     in later, but the tool ships with voice + location working.
//
// JSON to stdout (or --output); diagnostics to stderr; exit 0 on success.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"

	"becky-go/internal/beckyio"
	"becky-go/internal/config"
	"becky-go/internal/mediainfo"
)

// Output is the becky-identify JSON contract (matches 05-becky-identify.md).
type Output struct {
	File            string            `json:"file"`
	Identifications []Identification  `json:"identifications"`
	Unidentified    []Unidentified    `json:"unidentified"`
	Notes           map[string]string `json:"notes,omitempty"`
}

// Identification is one named match. After the fusion pass (fuse.go) a single
// entry may be a CORROBORATED conclusion fusing several modalities: Type is then
// "corroborated", CorroboratedBy lists the agreeing modalities, and Signals carries
// each contributing modality's raw confidence as the audit trail. A solo voice match
// keeps Type "voice". The legacy fields (type/name/confidence/speaker_id/frames) stay
// populated for backward compatibility with downstream consumers (becky-consolidate).
type Identification struct {
	Type           string        `json:"type"`
	SpeakerID      string        `json:"speaker_id,omitempty"`
	Name           string        `json:"name"`
	Confidence     float64       `json:"confidence"`
	Match          string        `json:"match"`
	CorroboratedBy []string      `json:"corroborated_by,omitempty"` // modalities that agreed (voice/face/location)
	Signals        []signal      `json:"signals,omitempty"`         // each agreeing modality's raw confidence (audit)
	Segments       []SpeakerSpan `json:"segments,omitempty"`
	Frames         []FrameRef    `json:"frames,omitempty"`
	Hamming        *int          `json:"hamming,omitempty"`
	// Voice top-2 audit trail (always emitted on a voice name so the basis is visible per
	// FORENSIC-OUTPUT-PHILOSOPHY's "show the basis"). VoiceMargin = best - runner-up.
	VoiceMargin        float64 `json:"voice_margin,omitempty"`
	RunnerUp           string  `json:"runner_up,omitempty"`            // the #2 enrollee (omitted when no rival)
	RunnerUpConfidence float64 `json:"runner_up_confidence,omitempty"` // its cosine
}

// Unidentified is one detected-but-unmatched entry: a speaker below threshold, OR a
// person who matched a SINGLE weak signal and was DEMOTED (Candidate names them, with
// the basis in Description) rather than asserted from one thin match.
type Unidentified struct {
	Type        string  `json:"type"`
	SpeakerID   string  `json:"speaker_id,omitempty"`
	Description string  `json:"description"`
	Confidence  float64 `json:"confidence"`
	Candidate   string  `json:"candidate,omitempty"` // the near-miss name, when a single weak signal was demoted
	// Voice naming audit (set when a speaker was demoted rather than named). CandidateConfidence
	// is the near-miss top-1 cosine; WhyUnnamed is a closed-set machine-readable reason
	// (below-detection | below-name-threshold | ambiguous-margin | not-in-cast).
	CandidateConfidence float64 `json:"candidate_confidence,omitempty"`
	RunnerUp            string  `json:"runner_up,omitempty"`
	RunnerUpConfidence  float64 `json:"runner_up_confidence,omitempty"`
	VoiceMargin         float64 `json:"voice_margin,omitempty"`
	WhyUnnamed          string  `json:"why_unnamed,omitempty"`
	// Remedy is the inline "teach me" hint: the exact becky command a human runs to
	// enroll this unidentified person (see remedy.go). Filled in for every
	// unidentified entry so the fix is visible at the point of failure.
	Remedy string `json:"remedy,omitempty"`
}

// SpeakerSpan is one (start,end) attributed to a speaker.
type SpeakerSpan struct {
	Start float64 `json:"start"`
	End   float64 `json:"end"`
}

// FrameRef ties a face/location match back to a source frame (reserved for the
// future face backend; emitted by location matches via Hamming instead).
type FrameRef struct {
	Frame     int     `json:"frame"`
	Timestamp float64 `json:"timestamp"`
}

func main() {
	out := flag.String("output", "", "output file (default: stdout)")
	format := flag.String("format", "json", "output format: json")
	kb := flag.String("kb", "", "path to knowledge base directory (required)")
	kbAlt := flag.String("knowledge-base", "", "alias for --kb")
	diarized := flag.String("diarized", "", "path to becky-diarize JSON (optional; runs diarization if absent)")
	voiceThreshold := flag.Float64("voice-threshold", 0.45, "voice cosine DETECTION floor (calibrated: same-person ~0.84 vs different-person ~0.03). Below this a speaker is unknown.")
	voiceNameThreshold := flag.Float64("voice-name-threshold", 0.75, "voice cosine NAMING floor for a lone match: below this a speaker is a named candidate, not an identification (README same-person band is 0.76-0.91)")
	voiceNameMargin := flag.Float64("voice-name-margin", 0.06, "minimum top-1 minus top-2 cosine gap to assert a lone name; below this two near-tied enrollees are ambiguous -> candidate (different-person ~0.03, so 0.06 is ~2x the noise floor)")
	cast := flag.String("cast", "", "comma-separated expected enrollees present in this corpus; restricts NAMING to this cast (absent enrollees can never be named or act as runner-up). Empty = all eligible.")
	faceThreshold := flag.Float64("face-threshold", 0.55, "face cosine threshold for NAMING (recall is for DETECTION, not naming): below this a detected face is reported as an unknown person, never force-matched to the nearest enrolled name. Set above the observed cross-person false match (~0.50, an unenrolled person hitting a similar-looking enrollee) while keeping genuine same-person (~0.59+). Provisional pending eval-harness tuning; real fix is enroll-all + becky-cluster.")
	locThreshold := flag.Int("location-threshold", 14, "location Hamming distance threshold")
	device := flag.String("device", "", "device: cuda, cpu (default from config)")
	numThreads := flag.Int("num-threads", 4, "ONNX inference threads")
	keepTemp := flag.Bool("keep-temp", false, "keep extracted temp WAVs")
	verbose := flag.Bool("verbose", false, "show progress on stderr")

	input := parsePositional()
	if input == "" {
		beckyio.Fatalf("usage: becky-identify <video> --kb <knowledge-base-dir> [options]")
	}
	if _, err := os.Stat(input); err != nil {
		beckyio.Fatalf("input not found: %s", input)
	}
	kbDir := *kb
	if kbDir == "" {
		kbDir = *kbAlt
	}
	if kbDir == "" {
		beckyio.Fatalf("--kb <knowledge-base-dir> is required")
	}
	if fi, err := os.Stat(kbDir); err != nil || !fi.IsDir() {
		beckyio.Fatalf("knowledge base not found or not a directory: %s", kbDir)
	}
	if *format != "" && strings.ToLower(*format) != "json" {
		beckyio.Fatalf("unknown format: %s (only json is supported)", *format)
	}

	cfg := config.Load()
	dev := cfg.Device
	if *device != "" {
		dev = *device
	}

	info, err := mediainfo.Probe(cfg.FFprobe, input)
	if err != nil {
		beckyio.Fatalf("%v", err)
	}

	knowledge, err := loadKnowledgeBase(kbDir, *verbose)
	if err != nil {
		beckyio.Fatalf("%v", err)
	}
	beckyio.Logf(*verbose, "knowledge base: %d voice-print(s), %d location(s), %d entity record(s)",
		len(knowledge.Voices), len(knowledge.Locations), len(knowledge.Entities))

	report := Output{
		File:            input,
		Notes:           map[string]string{},
		Identifications: []Identification{},
		Unidentified:    []Unidentified{},
	}

	// Resolve --cast against the enrolled knowledge base BEFORE running voice ID, so an
	// unknown cast name is surfaced (notes.cast) and ignored — degrade, never crash.
	castList, castNote := resolveCast(*cast, knowledge)
	if castNote != "" {
		report.Notes["cast"] = castNote
	}

	// 1. Voice identification (the core path — requires audio).
	if info.HasAudio {
		opts := voiceOptions{
			diarizedPath:  *diarized,
			threshold:     *voiceThreshold,
			nameThreshold: *voiceNameThreshold,
			nameMargin:    *voiceNameMargin,
			cast:          castList,
			device:        dev,
			numThreads:    *numThreads,
			keepTemp:      *keepTemp,
			verbose:       *verbose,
		}
		ids, unids, verr := identifyVoices(cfg, info, input, knowledge, opts)
		if verr != nil {
			beckyio.Fatalf("voice identification: %v", verr)
		}
		report.Identifications = append(report.Identifications, ids...)
		report.Unidentified = append(report.Unidentified, unids...)
		beckyio.Logf(*verbose, "voice: %d identified, %d unidentified", len(ids), len(unids))
	} else {
		report.Notes["voice"] = "skipped: input has no audio stream"
		beckyio.Logf(*verbose, "no audio stream; skipping voice identification")
	}

	// 2. Location identification (requires video + enrolled locations).
	if info.HasVideo && len(knowledge.Locations) > 0 {
		ids, lerr := identifyLocations(cfg, info, input, knowledge, *locThreshold, dev, *verbose)
		if lerr != nil {
			// Sampling failure is not fatal: note it and keep other modalities.
			report.Notes["location"] = "skipped: " + lerr.Error()
			beckyio.Logf(true, "warning: location identification failed: %v", lerr)
		} else {
			report.Identifications = append(report.Identifications, ids...)
			beckyio.Logf(*verbose, "location: %d identified", len(ids))
		}
	} else if len(knowledge.Locations) == 0 {
		beckyio.Logf(*verbose, "no locations enrolled; skipping location identification")
	} else {
		report.Notes["location"] = "skipped: input has no video stream"
		beckyio.Logf(*verbose, "no video stream; skipping location identification")
	}

	// 3. Face identification (requires video + enrolled faces + insightface models).
	if info.HasVideo && len(knowledge.Faces) > 0 {
		ids, unids, ferr := identifyFaces(cfg, info, input, knowledge, *faceThreshold, dev, *verbose)
		if ferr != nil {
			// Missing models / no detections is not fatal: note it, keep other modalities.
			report.Notes["face"] = "skipped: " + ferr.Error()
			beckyio.Logf(true, "warning: face identification failed: %v", ferr)
		} else {
			report.Identifications = append(report.Identifications, ids...)
			report.Unidentified = append(report.Unidentified, unids...)
			beckyio.Logf(*verbose, "face: %d identified, %d unidentified", len(ids), len(unids))
		}
	} else if len(knowledge.Faces) == 0 {
		report.Notes["face"] = "skipped: no faces enrolled"
		beckyio.Logf(*verbose, "no faces enrolled; skipping face identification")
	} else {
		report.Notes["face"] = "skipped: input has no video stream"
		beckyio.Logf(*verbose, "no video stream; skipping face identification")
	}

	// FUSION PASS (the 2026-06-08 philosophy applied to identity): collapse the raw
	// per-modality entries into corroborated conclusions. When voice + face (and/or
	// location) agree on one person, emit ONE confident "corroborated" identification
	// fusing the signals — not separate hedged voice/face rows. A lone weak signal is
	// demoted to a named CANDIDATE in unidentified[] rather than asserted.
	beforeIDs, beforeUnids := len(report.Identifications), len(report.Unidentified)
	report.Identifications, report.Unidentified = fuseIdentifications(report.Identifications, report.Unidentified)
	beckyio.Logf(*verbose, "fusion: %d raw id(s)/%d unid(s) -> %d conclusion(s)/%d candidate-or-unid(s)",
		beforeIDs, beforeUnids, len(report.Identifications), len(report.Unidentified))

	// Attach the inline "teach me" remedy to every FINAL unidentified entry (after
	// fusion, so fusion-demoted candidates carry it too), telling the human how to
	// enroll the person at the point of failure (README's "remedy inline" gap). The
	// clip is the real input file; <name> stays a literal placeholder. Additive: the
	// why_unnamed / candidate / description audit trail is left intact.
	attachRemedies(&report)

	if err := emit(report, *out); err != nil {
		beckyio.Fatalf("%v", err)
	}
}

// resolveCast parses the --cast CSV and validates each name against the enrolled voice
// knowledge base (by dir key, display name, or entity alias, case-insensitively). It
// returns the cleaned cast list (passed to the voice path as-is — filtering uses the same
// matching) and a plain-language notes.cast string. Unknown names (matching no enrollee)
// are reported as ignored-with-reason; the run continues. Empty input -> no cast, no note.
func resolveCast(raw string, kb Knowledge) ([]string, string) {
	if strings.TrimSpace(raw) == "" {
		return nil, ""
	}
	var names []string
	for _, p := range strings.Split(raw, ",") {
		if s := strings.TrimSpace(p); s != "" {
			names = append(names, s)
		}
	}
	if len(names) == 0 {
		return nil, ""
	}

	// Build the set of every recognizable enrolled token for validation.
	known := map[string]bool{}
	for _, v := range kb.Voices {
		known[strings.ToLower(v.Key)] = true
		known[strings.ToLower(v.Name)] = true
		if e, ok := kb.Entities[v.Key]; ok {
			for _, a := range e.Aliases {
				known[strings.ToLower(strings.TrimSpace(a))] = true
			}
		}
	}

	var unknown []string
	matched := 0
	for _, n := range names {
		if known[strings.ToLower(n)] {
			matched++
		} else {
			unknown = append(unknown, n)
		}
	}

	if matched == 0 {
		// Nothing in --cast matched any enrollee: ignore the filter entirely so the run
		// does not silently name nobody for a typo. (Spec §4: degrade, never crash.)
		return nil, fmt.Sprintf("ignored: none of %v match an enrolled voice; naming proceeds as if --cast were unset", names)
	}
	note := fmt.Sprintf("naming restricted to expected cast %v", names)
	if len(unknown) > 0 {
		note += fmt.Sprintf("; ignored unknown name(s) %v (no matching enrollee)", unknown)
	}
	return names, note
}

// parsePositional parses leading flags, takes the first positional argument, then
// re-parses any flags that followed it (Go's flag stops at the first non-flag).
func parsePositional() string {
	flag.Parse()
	rest := flag.Args()
	if len(rest) == 0 {
		return ""
	}
	input := rest[0]
	if len(rest) > 1 {
		_ = flag.CommandLine.Parse(rest[1:])
	}
	return input
}

func emit(o Output, outPath string) error {
	if outPath == "" {
		beckyio.PrintJSON(o)
		return nil
	}
	b, err := marshalIndent(o)
	if err != nil {
		return err
	}
	if err := os.WriteFile(outPath, b, 0o644); err != nil {
		return err
	}
	return nil
}

// marshalIndent renders the report as indented JSON with a trailing newline,
// matching beckyio.PrintJSON's stdout shape for the --output file path.
func marshalIndent(o Output) ([]byte, error) {
	b, err := json.MarshalIndent(o, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(b, '\n'), nil
}
