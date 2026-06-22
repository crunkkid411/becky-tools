package digest

import "encoding/json"

// Digest is the corpus-level roll-up: one document for a whole pipeline run. It
// is both the source of DIGEST.md and the shape of digest.json (the machine
// manifest for chaining). All slices are initialised to [] (never null) and all
// timestamps are RFC3339, matching the house conventions in
// cmd/pipeline/manifest.go and internal/report/types.go.
type Digest struct {
	Tool        string        `json:"tool"`
	Folder      string        `json:"folder"`
	GeneratedAt string        `json:"generated_at"` // RFC3339 (the one non-deterministic field)
	OutRoot     string        `json:"out_root"`
	KB          string        `json:"kb,omitempty"`
	Steps       []string      `json:"steps"`
	Clips       []ClipDigest  `json:"clips"`
	Corpus      CorpusSummary `json:"corpus"`
	Degraded    bool          `json:"degraded"`
	Notes       []string      `json:"notes"`
}

// ClipDigest is one clip's row: capture-time provenance, who/what/where, and the
// unknowns that still need a human. The fields are read off an already-built
// report.Report — no corroboration is decided here.
type ClipDigest struct {
	Stem       string `json:"stem"`
	Input      string `json:"input"`
	Status     string `json:"status"` // ok|partial|failed|unknown
	SidecarDir string `json:"sidecar_dir"`

	// Capture-time provenance. CaptureTrusted is false when the only date was the
	// untrusted file mtime (CaptureTimeSource == "mtime(untrusted)").
	CaptureTimeLocal  string `json:"capture_time_local,omitempty"`
	CaptureTimeSource string `json:"capture_time_source,omitempty"`
	CaptureTrusted    bool   `json:"capture_trusted"`
	UTCOffset         string `json:"utc_offset,omitempty"`

	Duration float64  `json:"duration,omitempty"`
	GPS      *MetaGPS `json:"gps,omitempty"`
	Device   string   `json:"device,omitempty"`

	ConcludedPeople []string `json:"concluded_people"`
	CandidatePeople []string `json:"candidate_people"`
	DocumentedCount int      `json:"documented_count"`
	ReviewCount     int      `json:"review_count"`

	KeyMoments  []string `json:"key_moments"`
	MoreMoments int      `json:"more_moments,omitempty"` // count truncated past maxKeyMoments
	Unknowns    []string `json:"unknowns"`

	HasReport bool     `json:"has_report"`
	Notes     []string `json:"notes"`
}

// CorpusSummary is the run-level roll-up shown at the top of DIGEST.md.
type CorpusSummary struct {
	ClipsTotal      int      `json:"clips_total"`
	ClipsOK         int      `json:"clips_ok"`
	ClipsPartial    int      `json:"clips_partial"`
	ClipsFailed     int      `json:"clips_failed"`
	PeopleConcluded []string `json:"people_concluded"`
	EarliestCapture string   `json:"earliest_capture,omitempty"` // trusted captures only
	LatestCapture   string   `json:"latest_capture,omitempty"`
	UnverifiedDates []string `json:"unverified_dates"` // clips whose only date was untrusted mtime
}

// JSON encodes the Digest as indented JSON with a trailing newline (the
// digest.json manifest). Slices are already [] not null, so the bytes are
// chain-friendly.
func JSON(d Digest) ([]byte, error) {
	b, err := json.MarshalIndent(d, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(b, '\n'), nil
}
