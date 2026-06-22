// dates.go — gather the per-clip date signals from real sources and triangulate.
//
// Signal A (container capture tag) comes from internal/exifmeta, which already
// runs exiftool with an ffprobe fallback and labels its source. CRUCIALLY, an
// exifmeta CaptureTimeSource of "mtime(untrusted)" is NOT signal A — it is routed
// to signal B (the untrusted mtime bucket); reading that label correctly is the
// single most important correctness rule in the tool.
package main

import (
	"time"

	"becky-go/internal/datetri"
	"becky-go/internal/exifmeta"
)

// ClipResult is the per-clip JSON record (the verdict plus its provenance).
type ClipResult struct {
	SourceFile     string               `json:"source_file"`
	SourceBase     string               `json:"source_base"`
	VerdictDate    string               `json:"verdict_date"`
	VerdictTimeLoc string               `json:"verdict_time_local"`
	Status         string               `json:"status"`
	Confidence     float64              `json:"confidence"`
	Basis          string               `json:"basis"`
	SingleSignal   bool                 `json:"single_signal"`
	Signals        []datetri.SignalView `json:"signals"`
	Conflicts      []datetri.Conflict   `json:"conflicts"`
	Notes          []string             `json:"notes"`
}

// Output is the becky-dates stdout/--output JSON document.
type Output struct {
	Tool       string            `json:"tool"`
	Folder     string            `json:"folder,omitempty"`
	ClipsDated int               `json:"clips_dated"`
	Results    []ClipResult      `json:"results"`
	Skipped    []SkipRecord      `json:"skipped"`
	Notes      map[string]string `json:"notes"`
}

// SkipRecord notes a non-media or unreadable input that was skipped.
type SkipRecord struct {
	SourceFile string `json:"source_file"`
	Reason     string `json:"reason"`
}

// dateClip gathers every available signal for one file and triangulates them.
// It never panics: any failing source contributes no signal (or a labelled mtime
// fallback) so the worst case is UNKNOWN, never a crash.
func dateClip(ex exifmeta.Extractor, file string, ts datetri.TimestampSource, minOCRConf float64, tolerance int) ClipResult {
	base := pathBase(file)
	var signals []datetri.Signal
	var notes []string

	// --- Signal A + Signal B(mtime): exifmeta ---
	md, err := ex.Extract(file)
	if err != nil {
		notes = append(notes, "metadata extraction failed: "+err.Error())
	} else {
		notes = append(notes, mdNotes(md)...)
		// Container capture tag -> Signal A, but ONLY when it is a real tag.
		if md.CaptureTimeSource != exifmeta.SourceMTime && md.CaptureTimeSource != "" {
			if t, precise, ok := parseExifTime(md); ok {
				signals = append(signals, datetri.Signal{
					Source:      md.CaptureTimeSource, // exif | quicktime | ffprobe
					Trust:       datetri.TrustStrong,
					Time:        t,
					Raw:         bestRaw(md),
					TimePrecise: precise,
				})
			} else {
				notes = append(notes, "container capture tag present but unparseable; dropped")
			}
		}
		// mtime -> Signal B (weak, always emitted so the reviewer sees it).
		if mt, ok := parseRFC3339(md.FileMTime); ok {
			signals = append(signals, datetri.Signal{
				Source:      exifmeta.SourceMTime,
				Trust:       datetri.TrustWeak,
				Time:        mt,
				Raw:         md.FileMTime,
				TimePrecise: true,
			})
		}
	}

	// --- Signal B(filename token) ---
	if fd, ok := datetri.ParseFilenameDate(base); ok {
		signals = append(signals, datetri.Signal{
			Source:      datetri.SourceFilename,
			Trust:       datetri.TrustMedium,
			Time:        fd.Time,
			Raw:         fd.Raw,
			TimePrecise: fd.Precise,
		})
	}

	// --- Signal C(OCR burned-in) — optional ---
	if ts != nil {
		for _, c := range ts.BurnedInDates(file) {
			if s, ok := datetri.SignalFromOCR(c, minOCRConf); ok {
				signals = append(signals, s)
			}
		}
	}

	v := datetri.Triangulate(signals, tolerance)

	// Merge engine notes after the gather notes; keep both.
	allNotes := append(notes, v.Notes...)
	if allNotes == nil {
		allNotes = []string{}
	}

	return ClipResult{
		SourceFile:     file,
		SourceBase:     base,
		VerdictDate:    v.VerdictDate,
		VerdictTimeLoc: v.VerdictTimeLoc,
		Status:         string(v.Status),
		Confidence:     round2(v.Confidence),
		Basis:          v.Basis,
		SingleSignal:   v.SingleSignal,
		Signals:        nonNilSignals(v.Signals),
		Conflicts:      nonNilConflicts(v.Conflicts),
		Notes:          allNotes,
	}
}

// parseExifTime returns the best capture instant from exifmeta metadata: the
// device-local time when present (carries the real offset + wall-clock), else the
// UTC instant. precise is true because a container tag carries a real clock.
func parseExifTime(md exifmeta.Metadata) (t time.Time, precise bool, ok bool) {
	if t, ok := parseRFC3339(md.CaptureTimeLocal); ok {
		return t, true, true
	}
	if t, ok := parseRFC3339(md.CaptureTimeUTC); ok {
		return t, true, true
	}
	return time.Time{}, false, false
}

// bestRaw returns the most informative raw capture value for display.
func bestRaw(md exifmeta.Metadata) string {
	if md.CaptureTimeLocal != "" {
		return md.CaptureTimeLocal
	}
	return md.CaptureTimeUTC
}

// parseRFC3339 parses an RFC3339 timestamp, returning ok=false on empty/invalid.
func parseRFC3339(s string) (time.Time, bool) {
	if s == "" {
		return time.Time{}, false
	}
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t, true
	}
	return time.Time{}, false
}

// mdNotes surfaces a couple of provenance notes from the metadata pass.
func mdNotes(md exifmeta.Metadata) []string {
	var out []string
	if md.CaptureTimeSource == exifmeta.SourceFFprobe {
		out = append(out, "no exiftool capture tag; relied on ffprobe creation_time")
	}
	return out
}

func round2(f float64) float64 {
	return float64(int(f*100+0.5)) / 100
}

func nonNilSignals(s []datetri.SignalView) []datetri.SignalView {
	if s == nil {
		return []datetri.SignalView{}
	}
	return s
}

func nonNilConflicts(c []datetri.Conflict) []datetri.Conflict {
	if c == nil {
		return []datetri.Conflict{}
	}
	return c
}
