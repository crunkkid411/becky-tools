// osint.go — events JSON parsing, event→timestamp mapping, file-name prefixing,
// and the manifest schema for becky-osint. The export mechanics (frame extract,
// SHA-256, perceptual hash, sidecar) all live in internal/osintexport and are
// reused here; this file only does the becky-osint-specific orchestration glue.
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"becky-go/internal/exifmeta"
)

// Event mirrors the subset of the becky-events output schema this tool consumes.
// location_change events carry an explicit Timestamp; speaker events (phone_call
// / second_speaker) do not, so they fall back to Start. HasTimestamp records
// whether the field was present in the source JSON (vs. a real 0.0 value).
type Event struct {
	Type         string  `json:"type"`
	Start        float64 `json:"start"`
	End          float64 `json:"end"`
	Timestamp    float64 `json:"timestamp"`
	SpeakerID    string  `json:"speaker_id,omitempty"`
	Frame        int     `json:"frame,omitempty"`
	HasTimestamp bool    `json:"-"`
}

// UnmarshalJSON detects whether the optional "timestamp" key was present so we
// can distinguish "timestamp: 0.0" from "no timestamp field" (the latter means
// fall back to Start).
func (e *Event) UnmarshalJSON(data []byte) error {
	type alias Event // avoid recursion
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return fmt.Errorf("parse event: %w", err)
	}
	var a alias
	if err := json.Unmarshal(data, &a); err != nil {
		return fmt.Errorf("parse event fields: %w", err)
	}
	*e = Event(a)
	_, e.HasTimestamp = raw["timestamp"]
	return nil
}

// eventsFile mirrors the becky-events JSON contract (file/duration/events).
type eventsFile struct {
	File     string  `json:"file"`
	Duration float64 `json:"duration"`
	Events   []Event `json:"events"`
}

// loadEvents reads and validates a becky-events JSON file.
func loadEvents(path string) (eventsFile, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return eventsFile{}, fmt.Errorf("read events json: %w", err)
	}
	var ef eventsFile
	if err := json.Unmarshal(data, &ef); err != nil {
		return eventsFile{}, fmt.Errorf("parse events json: %w", err)
	}
	return ef, nil
}

// EventTime returns the timestamp to extract a frame at: the explicit timestamp
// field when present, otherwise the event start.
func (e Event) EventTime() float64 {
	if e.HasTimestamp {
		return e.Timestamp
	}
	return e.Start
}

// filePrefix maps an event type to the file-name prefix used in the output dir.
// location_change -> "location"; second_speaker/phone_call -> "speaker";
// multi_face -> "face"; anything else falls back to a sanitized type name.
func filePrefix(eventType string) string {
	switch eventType {
	case "location_change":
		return "location"
	case "second_speaker", "phone_call":
		return "speaker"
	case "multi_face":
		return "face"
	default:
		p := sanitize(eventType)
		if p == "" {
			return "event"
		}
		return p
	}
}

// sanitize reduces an arbitrary event type to a safe, lowercase file-name token.
func sanitize(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == ' ' || r == '-' || r == '_':
			b.WriteByte('_')
		}
	}
	return strings.Trim(b.String(), "_")
}

// ExportRecord is one entry in the stdout manifest: where the frame + sidecar
// landed and the fingerprints detectives correlate on.
type ExportRecord struct {
	EventType      string  `json:"event_type"`
	Timestamp      float64 `json:"timestamp"`
	FrameIndex     int     `json:"frame_index"`
	FramePath      string  `json:"frame_path"`
	SidecarPath    string  `json:"sidecar_path"`
	AudioPath      string  `json:"audio_path,omitempty"`
	PerceptualHash string  `json:"perceptual_hash"`
	SHA256         string  `json:"sha256"`
}

// Manifest is the machine-readable summary printed to stdout. Skipped collects
// per-event failures so a single bad frame never aborts the whole run.
//
// Metadata is an ADDITIVE forensic-provenance block: capture device, the TRUE
// capture datetime + timezone, GPS, rotation, duration, resolution, and codecs.
// It is surfaced first-class (next to the existing frame-export fields) and is
// omitted only if the metadata pass could not run at all — the frame-export
// behavior never depends on it.
type Manifest struct {
	Tool          string             `json:"tool"`
	SourceFile    string             `json:"source_file"`
	SourceSHA256  string             `json:"source_sha256"`
	OutputDir     string             `json:"output_dir"`
	Format        string             `json:"format"`
	FPS           float64            `json:"fps"`
	Resolution    string             `json:"resolution"`
	RecordingDate string             `json:"recording_date,omitempty"`
	Metadata      *exifmeta.Metadata `json:"metadata,omitempty"`
	Exported      int                `json:"exported"`
	Skipped       []SkipRecord       `json:"skipped,omitempty"`
	Exports       []ExportRecord     `json:"exports"`
}

// SkipRecord notes an event that could not be exported and why.
type SkipRecord struct {
	EventType string  `json:"event_type"`
	Timestamp float64 `json:"timestamp"`
	Reason    string  `json:"reason"`
}

// marshalIndent renders the manifest as indented JSON with a trailing newline,
// matching the on-stdout format for --output file writes.
func marshalIndent(m Manifest) ([]byte, error) {
	b, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("marshal manifest: %w", err)
	}
	return append(b, '\n'), nil
}

// metadataProvenanceNote states the forensic boundary for the metadata sidecar:
// the values are reported from the container/EXIF tags as found, and the file
// mtime is never to be treated as a capture time.
const metadataProvenanceNote = "Forensic metadata reported as found in the container/EXIF tags. " +
	"capture_time_source records the tag used; file mtime is UNTRUSTED (rewritten by copy/sync/cloud)."

// MetadataSidecar wraps the extracted metadata with source provenance for a
// standalone, self-describing sidecar written next to the exported frames.
type MetadataSidecar struct {
	SourceFile   string             `json:"source_file"`
	SourceSHA256 string             `json:"source_sha256"`
	Tool         string             `json:"tool"`
	ExtractedAt  string             `json:"extracted_at"` // RFC3339
	Notes        string             `json:"notes"`
	Metadata     *exifmeta.Metadata `json:"metadata"`
}

// writeMetadataSidecar serializes the forensic metadata + provenance to path.
func writeMetadataSidecar(path string, md *exifmeta.Metadata, sourceFile, srcSHA string) error {
	s := MetadataSidecar{
		SourceFile:   sourceFile,
		SourceSHA256: srcSHA,
		Tool:         toolVersion,
		ExtractedAt:  nowRFC3339(),
		Notes:        metadataProvenanceNote,
		Metadata:     md,
	}
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal metadata sidecar: %w", err)
	}
	if err := os.WriteFile(path, append(b, '\n'), 0o644); err != nil {
		return fmt.Errorf("write metadata sidecar %s: %w", path, err)
	}
	return nil
}
