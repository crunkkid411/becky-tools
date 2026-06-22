// Package digest rolls a whole pipeline run up into ONE skimmable, human- and
// LLM-readable corpus digest. It consumes the per-clip forensic reports that
// internal/report already corroborated (the "≥2 signals → DOCUMENTED" rule lives
// there and ONLY there — this package never re-decides corroboration) plus the
// capture-time / GPS / device block from each clip's osint-manifest.json.
//
// The output is deliberately LINEAR per ACCESSIBILITY.md: short labelled lines a
// low-vision sighted reader scans top-to-bottom and a model parses — NO markdown
// tables, NO box-drawing, NO emoji-as-meaning (a conscious departure from
// internal/report/markdown.go, which uses GitHub tables + ✅/⚫ icons and is the
// wrong shape for a corpus digest). It reuses report.Report DATA, not its
// rendering.
//
// Deterministic: the same sidecars always yield byte-identical DIGEST.md (the one
// non-deterministic field, GeneratedAt, is supplied by an injectable clock so
// tests can pin it). Offline + degrade-never-crash: missing fields omit a line or
// say so plainly (e.g. "no location signal"); the Unknowns section is never
// empty-by-omission (it says "none flagged" so absence is trustworthy).
package digest

import (
	"time"

	"becky-go/internal/report"
)

// CaptureMeta is the subset of an osint-manifest.json `metadata` block the digest
// needs: capture-time provenance, GPS, and device. It mirrors the relevant
// exifmeta.Metadata field tags so it decodes straight from the already-written
// JSON — it never calls exiftool/ffprobe.
type CaptureMeta struct {
	CaptureTimeLocal  string   `json:"capture_time_local,omitempty"`
	CaptureTimeUTC    string   `json:"capture_time_utc,omitempty"`
	UTCOffset         string   `json:"utc_offset,omitempty"`
	CaptureTimeSource string   `json:"capture_time_source,omitempty"` // exif|quicktime|ffprobe|mtime(untrusted)
	FileMTime         string   `json:"file_mtime_untrusted,omitempty"`
	DeviceMake        string   `json:"device_make,omitempty"`
	DeviceModel       string   `json:"device_model,omitempty"`
	DeviceName        string   `json:"device_name,omitempty"`
	GPS               *MetaGPS `json:"gps,omitempty"`
	DurationSeconds   float64  `json:"duration_seconds,omitempty"`
}

// MetaGPS holds decoded coordinates when the file carried them.
type MetaGPS struct {
	Latitude  float64 `json:"latitude"`
	Longitude float64 `json:"longitude"`
}

// sourceMTime is the literal capture_time_source value exifmeta emits when the
// only date is the untrusted filesystem mtime. Kept here (not imported) so the
// digest formatter has no dependency on the heavy exifmeta probe package.
const sourceMTime = "mtime(untrusted)"

// ClipInput is one clip handed to Build: its identity (from the pipeline
// manifest), its corroborated report, its capture metadata, and any per-clip
// notes (degraded steps, missing sidecars). Report/Capture may be zero values —
// Build degrades cleanly.
type ClipInput struct {
	Stem       string        // file stem, e.g. "reddit-livestream-2025-08-14"
	Input      string        // source path, e.g. "/cases/reddit/...mp4"
	Status     string        // pipeline VideoResult.Status: ok|partial|failed
	SidecarDir string        // <out>/<stem>/ — the audit trail
	Report     report.Report // the corroborated per-clip report (reuse, never re-corroborate)
	Capture    CaptureMeta   // capture-time / GPS / device from osint-manifest.json
	Notes      []string      // per-clip degrade notes (e.g. "validate produced 0 observations")
	HasReport  bool          // whether a real report was available (else a stub section)
}

// CorpusInfo carries run-level facts the manifest knows but the per-clip reports
// don't (the requested step set, kb, out root).
type CorpusInfo struct {
	Folder  string   // the ingested corpus folder
	OutRoot string   // pipeline output root
	KB      string   // knowledge base used for identify ("" = skipped)
	Steps   []string // the step set the pipeline ran
	Notes   []string // run-level notes (e.g. "becky-validate not run")
}

// maxKeyMoments caps the "What" list per clip so the digest stays skimmable; the
// rest are still in the clip's report.json (a "(+k more)" line points there).
const maxKeyMoments = 5

// Build assembles the deterministic Digest from the per-clip inputs and run-level
// info. Clip order is preserved exactly as given (the pipeline manifest's sorted
// order). clock supplies GeneratedAt (inject a fixed clock in tests).
func Build(clips []ClipInput, info CorpusInfo, clock func() time.Time) Digest {
	if clock == nil {
		clock = func() time.Time { return time.Now().UTC() }
	}
	d := Digest{
		Tool:        "becky-ingest v1.0.0",
		Folder:      info.Folder,
		GeneratedAt: clock().UTC().Format(time.RFC3339),
		OutRoot:     info.OutRoot,
		KB:          info.KB,
		Steps:       nonNilStrings(info.Steps),
		Clips:       []ClipDigest{},
		Notes:       nonNilStrings(info.Notes),
	}

	peopleSet := newOrderedSet()
	var unverified []string
	var earliest, latest string // RFC3339 strings, trusted captures only
	usefulClips := 0

	for _, c := range clips {
		cd := buildClipDigest(c)
		d.Clips = append(d.Clips, cd)

		for _, p := range cd.ConcludedPeople {
			peopleSet.add(p)
		}
		if cd.CaptureTrusted && cd.CaptureTimeLocal != "" {
			if earliest == "" || cd.CaptureTimeLocal < earliest {
				earliest = cd.CaptureTimeLocal
			}
			if latest == "" || cd.CaptureTimeLocal > latest {
				latest = cd.CaptureTimeLocal
			}
		} else if cd.CaptureTimeSource == sourceMTime || (cd.CaptureTimeSource == "" && cd.CaptureTimeLocal == "") {
			unverified = append(unverified, c.Stem)
		}
		if cd.Status == "ok" || cd.DocumentedCount > 0 || len(cd.ConcludedPeople) > 0 {
			usefulClips++
		}
	}

	d.Corpus = CorpusSummary{
		ClipsTotal:      len(clips),
		ClipsOK:         countStatus(clips, "ok"),
		ClipsPartial:    countStatus(clips, "partial"),
		ClipsFailed:     countStatus(clips, "failed"),
		PeopleConcluded: peopleSet.slice(),
		EarliestCapture: earliest,
		LatestCapture:   latest,
		UnverifiedDates: nonNilStrings(unverified),
	}

	// A digest is degraded when nothing useful surfaced anywhere in the corpus.
	if len(clips) == 0 || usefulClips == 0 {
		d.Degraded = true
		if len(clips) == 0 {
			d.Notes = append(d.Notes, "no clips ingested — corpus empty or pipeline produced no manifest")
		} else {
			d.Notes = append(d.Notes, "no corroborated forensic data found across the corpus")
		}
	}
	return d
}

// buildClipDigest turns one ClipInput into its ClipDigest row. It reads ONLY
// already-corroborated report fields; it never re-runs the ≥2-signal rule.
func buildClipDigest(c ClipInput) ClipDigest {
	cd := ClipDigest{
		Stem:            c.Stem,
		Input:           c.Input,
		Status:          orDefault(c.Status, "unknown"),
		SidecarDir:      c.SidecarDir,
		ConcludedPeople: []string{},
		CandidatePeople: []string{},
		KeyMoments:      []string{},
		Unknowns:        []string{},
		Notes:           nonNilStrings(c.Notes),
		HasReport:       c.HasReport,
	}

	// Capture-time / location / device (from osint-manifest.json).
	cd.CaptureTimeLocal = c.Capture.CaptureTimeLocal
	cd.CaptureTimeSource = c.Capture.CaptureTimeSource
	cd.CaptureTrusted = captureTrusted(c.Capture.CaptureTimeSource)
	cd.UTCOffset = c.Capture.UTCOffset
	cd.Device = deviceName(c.Capture)
	if g := c.Capture.GPS; g != nil {
		cd.GPS = &MetaGPS{Latitude: g.Latitude, Longitude: g.Longitude}
	}

	// Duration: prefer the report's, fall back to capture metadata's.
	cd.Duration = c.Report.Duration
	if cd.Duration == 0 {
		cd.Duration = c.Capture.DurationSeconds
	}

	// Who: concluded (DOCUMENTED) people by name; candidates separately.
	for _, e := range c.Report.Entities {
		if e.Concluded {
			appendUnique(&cd.ConcludedPeople, e.Name)
		} else {
			appendUnique(&cd.CandidatePeople, e.Name)
		}
	}

	// What (key moments): DOCUMENTED conclusions first, capped.
	for _, f := range c.Report.Conclusions {
		cd.KeyMoments = append(cd.KeyMoments, formatMoment(f))
	}
	cd.MoreMoments = 0
	if len(cd.KeyMoments) > maxKeyMoments {
		cd.MoreMoments = len(cd.KeyMoments) - maxKeyMoments
		cd.KeyMoments = cd.KeyMoments[:maxKeyMoments]
	}

	// Unknowns: every review item, plus candidate-people not concluded.
	for _, f := range c.Report.ReviewItems {
		cd.Unknowns = append(cd.Unknowns, formatUnknown(f))
	}

	// Counts (read straight off the report, never recomputed beyond length).
	cd.DocumentedCount = len(c.Report.Conclusions)
	cd.ReviewCount = len(c.Report.ReviewItems)

	return cd
}

// captureTrusted reports whether a capture_time_source is a real capture tag (not
// the untrusted mtime fallback, and not absent).
func captureTrusted(src string) bool {
	return src != "" && src != sourceMTime
}

// deviceName builds a friendly device label from make/model/name, "" if none.
func deviceName(m CaptureMeta) string {
	if m.DeviceName != "" {
		return m.DeviceName
	}
	switch {
	case m.DeviceMake != "" && m.DeviceModel != "":
		return m.DeviceMake + " " + m.DeviceModel
	case m.DeviceModel != "":
		return m.DeviceModel
	case m.DeviceMake != "":
		return m.DeviceMake
	}
	return ""
}

func countStatus(clips []ClipInput, status string) int {
	n := 0
	for _, c := range clips {
		if c.Status == status {
			n++
		}
	}
	return n
}

func orDefault(s, def string) string {
	if s == "" {
		return def
	}
	return s
}

func nonNilStrings(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

func appendUnique(dst *[]string, v string) {
	if v == "" {
		return
	}
	for _, x := range *dst {
		if x == v {
			return
		}
	}
	*dst = append(*dst, v)
}

// orderedSet preserves first-seen order while de-duplicating (so corpus people
// list is deterministic given a deterministic clip order).
type orderedSet struct {
	seen  map[string]bool
	items []string
}

func newOrderedSet() *orderedSet { return &orderedSet{seen: map[string]bool{}} }

func (o *orderedSet) add(v string) {
	if v == "" || o.seen[v] {
		return
	}
	o.seen[v] = true
	o.items = append(o.items, v)
}

func (o *orderedSet) slice() []string {
	if o.items == nil {
		return []string{}
	}
	return o.items
}
