// kb.go — write the becky-identify KB layout, the enrollment registry JSON, and a
// human-readable report. The voice clips and face frames are already on disk under
// KB/voice-prints/<Name>/ and KB/face-prints/<Name>/ (written by enroll.go); this
// file adds the entities/<name>.json metadata records that becky-identify reads for
// display names + aliases, plus the registry + report the operator reviews.
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Registry is the enrollment-registry.json document (shape from the spec).
type Registry struct {
	Generated string          `json:"generated"` // ISO date YYYY-MM-DD
	Tool      string          `json:"tool"`
	WikiRoots []string        `json:"wiki_roots"`
	KB        string          `json:"kb"`
	Entities  []RegistryEnt   `json:"entities"`
	Summary   RegistrySummary `json:"summary"`
	Warnings  []string        `json:"warnings,omitempty"`
}

// RegistryEnt is one enrolled-or-skipped person record.
type RegistryEnt struct {
	Name       string            `json:"name"`
	Aliases    []string          `json:"aliases"`
	MDSource   string            `json:"md_source"`
	VideoRefs  []string          `json:"video_refs"`
	ImageRefs  []string          `json:"image_refs"`
	Enrolled   EnrolledFlags     `json:"enrolled"`
	VoiceClip  *string           `json:"voice_clip"`
	FaceImage  *string           `json:"face_image"`
	VoiceFrom  *string           `json:"voice_from,omitempty"`
	FaceFrom   *string           `json:"face_from,omitempty"`
	SpeakerID  string            `json:"speaker_id,omitempty"`
	SkipReason map[string]string `json:"skip_reason,omitempty"`
}

// EnrolledFlags records which modalities were successfully enrolled.
type EnrolledFlags struct {
	Voice bool `json:"voice"`
	Face  bool `json:"face"`
}

// RegistrySummary is the top-level rollup.
type RegistrySummary struct {
	PeopleDetected int `json:"people_detected"`
	VoiceEnrolled  int `json:"voice_enrolled"`
	FaceEnrolled   int `json:"face_enrolled"`
	FullyEnrolled  int `json:"fully_enrolled"`
	Skipped        int `json:"skipped"`
}

// entityRecord is the becky-identify entities/<name>.json shape.
type entityRecord struct {
	Name        string   `json:"name"`
	Aliases     []string `json:"aliases"`
	Description string   `json:"description"`
}

// writeKB writes entity metadata + registry + report and returns the registry.
func writeKB(kbDir string, roots []string, results []EnrollResult, warnings []string) (Registry, error) {
	reg := Registry{
		Generated: time.Now().UTC().Format("2006-01-02"),
		Tool:      "becky-enroll v1.0.0",
		WikiRoots: roots,
		KB:        absOr(kbDir),
		Entities:  make([]RegistryEnt, 0, len(results)),
		Warnings:  warnings,
	}

	for _, r := range results {
		if err := writeEntityRecord(kbDir, r); err != nil {
			return reg, fmt.Errorf("write entity %q: %w", r.person.Name, err)
		}
		reg.Entities = append(reg.Entities, buildRegistryEntry(kbDir, r))
	}

	reg.Summary = summarize(reg.Entities)
	if err := writeJSONFile(filepath.Join(kbDir, "enrollment-registry.json"), reg); err != nil {
		return reg, err
	}
	if err := os.WriteFile(filepath.Join(kbDir, "enrollment-report.txt"), []byte(renderReport(reg)), 0o644); err != nil {
		return reg, err
	}
	return reg, nil
}

// writeEntityRecord writes entities/<slug>.json (display name + aliases) so
// becky-identify shows the friendly name. becky-identify keys entities by the
// lowercased file stem and matches it to the voice-prints/<dir> key (also
// lowercased). The voice/face subdir is the person's display Name, so the entity
// stem must equal lower(Name).
func writeEntityRecord(kbDir string, r EnrollResult) error {
	dir := filepath.Join(kbDir, "entities")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	rec := entityRecord{
		Name:        r.person.Name,
		Aliases:     nonNil(r.person.Aliases),
		Description: "Enrolled from wiki: " + r.person.MDSource,
	}
	stem := strings.ToLower(r.person.Name)
	return writeJSONFile(filepath.Join(dir, sanitizeFileStem(stem)+".json"), rec)
}

// buildRegistryEntry assembles one registry record from an EnrollResult.
func buildRegistryEntry(kbDir string, r EnrollResult) RegistryEnt {
	ent := RegistryEnt{
		Name:       r.person.Name,
		Aliases:    nonNil(r.person.Aliases),
		MDSource:   r.person.MDSource,
		VideoRefs:  relList(r.person.VideoRefs),
		ImageRefs:  relList(r.person.ImageRefs),
		Enrolled:   EnrolledFlags{Voice: r.voiceClip != "", Face: r.faceImage != ""},
		VoiceClip:  relPtr(kbDir, r.voiceClip),
		FaceImage:  relPtr(kbDir, r.faceImage),
		VoiceFrom:  basePtr(r.voiceVideo),
		FaceFrom:   basePtr(r.faceVideo),
		SpeakerID:  r.speakerID,
		SkipReason: map[string]string{},
	}
	if r.skipVoice != "" {
		ent.SkipReason["voice"] = r.skipVoice
	}
	if r.skipFace != "" {
		ent.SkipReason["face"] = r.skipFace
	}
	if len(ent.SkipReason) == 0 {
		ent.SkipReason = nil
	}
	return ent
}

func summarize(ents []RegistryEnt) RegistrySummary {
	s := RegistrySummary{PeopleDetected: len(ents)}
	for _, e := range ents {
		if e.Enrolled.Voice {
			s.VoiceEnrolled++
		}
		if e.Enrolled.Face {
			s.FaceEnrolled++
		}
		if e.Enrolled.Voice && e.Enrolled.Face {
			s.FullyEnrolled++
		}
		if !e.Enrolled.Voice && !e.Enrolled.Face {
			s.Skipped++
		}
	}
	return s
}

// renderReport produces the human-readable enrollment-report.txt body.
func renderReport(reg Registry) string {
	var b strings.Builder
	fmt.Fprintf(&b, "becky-enroll report — %s\n", reg.Generated)
	fmt.Fprintf(&b, "KB: %s\n", reg.KB)
	fmt.Fprintf(&b, "Wiki roots:\n")
	for _, r := range reg.WikiRoots {
		fmt.Fprintf(&b, "  - %s\n", r)
	}
	s := reg.Summary
	fmt.Fprintf(&b, "\nDetected %d people: voice=%d, face=%d, both=%d, none=%d\n\n",
		s.PeopleDetected, s.VoiceEnrolled, s.FaceEnrolled, s.FullyEnrolled, s.Skipped)

	for _, e := range reg.Entities {
		fmt.Fprintf(&b, "[%s] %s\n", statusLabel(e.Enrolled), e.Name)
		if len(e.Aliases) > 0 {
			fmt.Fprintf(&b, "    aliases: %s\n", strings.Join(e.Aliases, ", "))
		}
		fmt.Fprintf(&b, "    md: %s | videos: %d | images: %d\n", e.MDSource, len(e.VideoRefs), len(e.ImageRefs))
		if e.Enrolled.Voice {
			fmt.Fprintf(&b, "    voice: %s", deref(e.VoiceClip))
			if e.SpeakerID != "" {
				fmt.Fprintf(&b, " (%s)", e.SpeakerID)
			}
			fmt.Fprintf(&b, "\n")
		} else if e.SkipReason != nil {
			fmt.Fprintf(&b, "    voice SKIPPED: %s\n", e.SkipReason["voice"])
		}
		if e.Enrolled.Face {
			fmt.Fprintf(&b, "    face:  %s\n", deref(e.FaceImage))
		} else if e.SkipReason != nil {
			fmt.Fprintf(&b, "    face  SKIPPED: %s\n", e.SkipReason["face"])
		}
		fmt.Fprintf(&b, "\n")
	}
	if len(reg.Warnings) > 0 {
		fmt.Fprintf(&b, "Warnings (%d):\n", len(reg.Warnings))
		for _, w := range reg.Warnings {
			fmt.Fprintf(&b, "  - %s\n", w)
		}
	}
	return b.String()
}

func statusLabel(f EnrolledFlags) string {
	switch {
	case f.Voice && f.Face:
		return "VOICE+FACE"
	case f.Voice:
		return "VOICE    "
	case f.Face:
		return "FACE     "
	default:
		return "SKIPPED  "
	}
}

// --- small helpers ---

func writeJSONFile(path string, v any) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(b, '\n'), 0o644)
}

// relPtr returns a KB-relative path pointer, or nil for an empty path.
func relPtr(kbDir, abs string) *string {
	if abs == "" {
		return nil
	}
	if rel, err := filepath.Rel(kbDir, abs); err == nil {
		s := filepath.ToSlash(rel)
		return &s
	}
	s := filepath.ToSlash(abs)
	return &s
}

// basePtr returns a pointer to the base filename of a source, or nil if empty.
func basePtr(p string) *string {
	if p == "" {
		return nil
	}
	b := filepath.Base(p)
	return &b
}

// relList returns base filenames for a list of absolute source paths.
func relList(paths []string) []string {
	out := make([]string, 0, len(paths))
	for _, p := range paths {
		out = append(out, filepath.Base(p))
	}
	return out
}

func nonNil(in []string) []string {
	if in == nil {
		return []string{}
	}
	return in
}

func deref(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

func absOr(p string) string {
	if abs, err := filepath.Abs(p); err == nil {
		return abs
	}
	return p
}

// sanitizeFileStem makes an entity filename stem safe while preserving spaces (the
// voice-print dir name may contain spaces and becky-identify keys by lower(stem)).
func sanitizeFileStem(s string) string {
	repl := func(r rune) rune {
		switch r {
		case '/', '\\', ':', '*', '?', '"', '<', '>', '|':
			return '_'
		default:
			return r
		}
	}
	return strings.Map(repl, s)
}
