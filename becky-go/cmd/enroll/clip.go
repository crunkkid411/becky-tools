// clip.go — single-clip enrollment: teach the KB ONE named person from ONE clip,
// without a wiki ("becky learn 'Shelby' clip.mp4" / "becky 'this is Shelby' clip.mp4").
//
// This is the natural-language enrollment path. It reuses the SAME enrollPerson
// machinery the wiki path uses (so voice clip + face frame selection are identical),
// but it APPENDS to an existing KB instead of rebuilding it: a single-clip teach must
// not clobber the people already enrolled. A single-person clip is the safe case; a
// multi-person clip falls back to the dominant speaker (most speech) for voice and the
// largest/clearest face for the picture — and reports which it used so the operator
// can confirm.
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"becky-go/internal/beckyio"
	"becky-go/internal/config"
)

// ClipEnrollResult is the JSON summary emitted for a single-clip teach.
type ClipEnrollResult struct {
	Tool       string            `json:"tool"`
	Generated  string            `json:"generated"`
	Mode       string            `json:"mode"`
	Name       string            `json:"name"`
	Clip       string            `json:"clip"`
	KB         string            `json:"kb"`
	VoiceClip  *string           `json:"voice_clip"`
	FaceImage  *string           `json:"face_image"`
	SpeakerID  string            `json:"speaker_id,omitempty"`
	Enrolled   EnrolledFlags     `json:"enrolled"`
	SkipReason map[string]string `json:"skip_reason,omitempty"`
	Notes      []string          `json:"notes,omitempty"`
}

// runLearnClip validates the single-clip teach inputs, builds the enroll options, and
// runs + emits the result. It is the entry point main() calls for --clip/--name mode.
func runLearnClip(cfg config.Config, dev, kbDir, clip, name, binDir string, noFace, noVoice, verbose bool) {
	name = strings.TrimSpace(name)
	if clip == "" || name == "" {
		beckyio.Fatalf("single-clip teach needs BOTH --clip <video> and --name \"<person>\"")
	}
	if _, err := os.Stat(clip); err != nil {
		beckyio.Fatalf("clip not found: %s", clip)
	}
	if err := os.MkdirAll(kbDir, 0o755); err != nil {
		beckyio.Fatalf("create KB dir: %v", err)
	}

	opts := enrollOptions{
		diarizeBin: resolveDiarizeBin(binDir),
		device:     dev,
		noFace:     noFace,
		noVoice:    noVoice,
		verbose:    verbose,
	}
	if !opts.noVoice && opts.diarizeBin == "" {
		beckyio.Logf(true, "warning: becky-diarize.exe not found (pass --bin); learning face only")
	}

	res := runClipEnroll(cfg, kbDir, name, clip, opts)
	emitClipResult(res)
}

// runClipEnroll enrolls one named person from one clip and APPENDS to the KB. It
// builds a synthetic Person (the clip as its only video) and runs the shared
// enrollPerson, then writes the entity record + a clip-enrollment marker without
// rewriting the wiki registry.
func runClipEnroll(cfg config.Config, kbDir, name, clip string, opts enrollOptions) ClipEnrollResult {
	res := ClipEnrollResult{
		Tool:       "becky-enroll (learn-clip)",
		Generated:  time.Now().UTC().Format("2006-01-02"),
		Mode:       "learn-clip",
		Name:       name,
		Clip:       clip,
		KB:         absOr(kbDir),
		SkipReason: map[string]string{},
	}

	// A single-clip teach is an explicit operator choice — never skip it as a
	// "non-subject" the way the wiki sweep does.
	person := Person{
		Name:      name,
		Slug:      strings.ToLower(sanitizeStem(name)),
		MDSource:  "learned from clip: " + filepath.Base(clip),
		VideoRefs: []string{clip},
	}
	opts.includeNonSubject = true

	er := enrollPerson(cfg, kbDir, person, opts)

	res.VoiceClip = absPtrIf(er.voiceClip)
	res.FaceImage = absPtrIf(er.faceImage)
	res.SpeakerID = er.speakerID
	res.Enrolled = EnrolledFlags{Voice: er.voiceClip != "", Face: er.faceImage != ""}
	if er.skipVoice != "" {
		res.SkipReason["voice"] = er.skipVoice
	}
	if er.skipFace != "" {
		res.SkipReason["face"] = er.skipFace
	}
	if len(res.SkipReason) == 0 {
		res.SkipReason = nil
	}
	// Multi-person clip: REPORT which speaker we used (the philosophy: when the input
	// is ambiguous, say plainly what was chosen so the operator can confirm).
	if er.numSpeakers > 1 && er.voiceClip != "" {
		res.Notes = append(res.Notes,
			fmt.Sprintf("multi-person clip (%d speakers): used the dominant speaker (%s) for voice — confirm this is %s",
				er.numSpeakers, er.speakerID, res.Name))
	}

	// Write/refresh the entity metadata record so becky-identify shows the friendly
	// name (additive — does not touch other people's records).
	if res.Enrolled.Voice || res.Enrolled.Face {
		if err := writeEntityRecord(kbDir, er); err != nil {
			res.Notes = append(res.Notes, "warning: could not write entity record: "+err.Error())
		}
		if err := appendClipRegistry(kbDir, res); err != nil {
			res.Notes = append(res.Notes, "warning: could not update learn log: "+err.Error())
		}
	}

	return res
}

// clipRegistry is the append-only log of single-clip teaches (kept separate from the
// wiki enrollment-registry.json so the two enrollment paths never clobber each other).
type clipRegistry struct {
	Tool    string             `json:"tool"`
	Updated string             `json:"updated"`
	Learned []ClipEnrollResult `json:"learned"`
}

// appendClipRegistry appends this teach to learn-registry.json (created on first use).
func appendClipRegistry(kbDir string, res ClipEnrollResult) error {
	path := filepath.Join(kbDir, "learn-registry.json")
	reg := clipRegistry{Tool: "becky-enroll (learn-clip)"}
	if data, err := os.ReadFile(path); err == nil {
		_ = json.Unmarshal(data, &reg)
	}
	reg.Updated = time.Now().UTC().Format("2006-01-02")
	reg.Learned = append(reg.Learned, res)
	return writeJSONFile(path, reg)
}

// absPtrIf returns a pointer to the absolute path, or nil for an empty path.
func absPtrIf(p string) *string {
	if p == "" {
		return nil
	}
	a := absOr(p)
	return &a
}

// emitClipResult prints the single-clip teach summary as the tool's JSON stdout, and
// a one-line plain-English headline to stderr.
func emitClipResult(res ClipEnrollResult) {
	switch {
	case res.Enrolled.Voice && res.Enrolled.Face:
		beckyio.Logf(true, "Learned %q from %s: voice + face enrolled into %s",
			res.Name, filepath.Base(res.Clip), res.KB)
	case res.Enrolled.Voice:
		beckyio.Logf(true, "Learned %q from %s: voice enrolled (face skipped: %s)",
			res.Name, filepath.Base(res.Clip), skipReasonOr(res, "face"))
	case res.Enrolled.Face:
		beckyio.Logf(true, "Learned %q from %s: face enrolled (voice skipped: %s)",
			res.Name, filepath.Base(res.Clip), skipReasonOr(res, "voice"))
	default:
		beckyio.Logf(true, "Could NOT learn %q from %s — nothing clean to enroll (voice: %s; face: %s)",
			res.Name, filepath.Base(res.Clip), skipReasonOr(res, "voice"), skipReasonOr(res, "face"))
	}
	beckyio.PrintJSON(res)
}

// skipReasonOr returns the recorded skip reason for a modality, or "" when none.
func skipReasonOr(res ClipEnrollResult, modality string) string {
	if res.SkipReason == nil {
		return ""
	}
	return res.SkipReason[modality]
}
