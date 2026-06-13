// manifest.go — the becky-framematch JSON manifest schema and the small value
// types it is built from. The manifest is the re-runnable record of one pass:
// it names both sources (path + SHA-256), the sampling parameters, the ranked
// candidate frame pairs, and every honest enhancement applied. Re-running with
// adjusted flags (threshold / interval / enhance) produces a new manifest over
// the same copies — this is a loop, not a one-shot.
//
// Candidate-not-conclusion: every pair is a CANDIDATE for a human to confirm
// (plus realtor/listing corroboration). The manifest never declares "same place".
package main

// ManifestNote is the fixed honesty note carried on every manifest. It states
// the boundary: the tool surfaces candidates, it does not conclude.
const ManifestNote = "Candidate same-location/same-object frame pairs for HUMAN review. " +
	"A low perceptual-hash distance means the frames LOOK alike — it is NOT proof " +
	"they are the same place. Confirm visually and corroborate (realtor/listing/witness)."

// SourceInfo records one input source: its path, content fingerprint, and the
// media facts a reviewer needs to trust the frames came from it untouched.
type SourceInfo struct {
	Label      string  `json:"label"`       // "A" or "B" (used in file names + the exhibit)
	Path       string  `json:"path"`        // original source path (only ever READ)
	Kind       string  `json:"kind"`        // "video" or "images"
	SHA256     string  `json:"sha256"`      // SHA-256 of the source file (video) or "" for an image folder
	Duration   float64 `json:"duration"`    // seconds (video; 0 for image folder)
	FPS        float64 `json:"fps"`         // source frame rate (video; 0 for image folder)
	Resolution string  `json:"resolution"`  // "WxH" of the source (video; first image for a folder)
	FrameCount int     `json:"frame_count"` // number of frames sampled/hashed from this source
}

// Frame is one sampled, hashed frame from a source. The timestamp is the
// position in the source video (seconds); for an image folder it is the file's
// position in the (sorted) list expressed as an index-second so the exhibit
// still has a stable label.
type Frame struct {
	SourceLabel string  `json:"source_label"` // "A" or "B"
	Index       int     `json:"index"`        // 0-based sample index within its source
	Timestamp   float64 `json:"timestamp"`    // seconds into the source video (or image index)
	TimeLabel   string  `json:"time_label"`   // human "M:SS.s" (video) or the image file name
	Path        string  `json:"path"`         // extracted frame copy on disk (slash path)
	Sidecar     string  `json:"sidecar"`      // provenance JSON sidecar for this frame
	Hash        string  `json:"hash"`         // 16-char hex aHash
}

// Pair is one candidate cross-source match: a frame from A and a frame from B
// that are within --threshold Hamming bits of each other.
type Pair struct {
	Rank          int       `json:"rank"`             // 1-based, lowest Hamming first
	Hamming       int       `json:"hamming"`          // perceptual-hash distance (0 = identical hash)
	Similarity    float64   `json:"similarity"`       // 1 - hamming/64, a [0,1] readability score
	WhatToLookFor string    `json:"what_to_look_for"` // one-line reviewer hint (the matching region)
	A             Frame     `json:"frame_a"`          // the A-source frame
	B             Frame     `json:"frame_b"`          // the B-source frame
	Comparison    string    `json:"comparison_image"` // side-by-side labeled PNG for this pair (slash path)
	Enhancements  []Enhance `json:"enhancements"`     // every honest edit applied to a COPY for this pair
}

// Enhance is one logged, honest image adjustment applied to a COPY of a frame.
// Only brightness/contrast/gamma/saturation (ffmpeg `eq`) and optional manual
// crop/rotate are recorded here; geometry-warping, AI generation, and cloning
// are never performed by this tool, so they can never appear in this log.
type Enhance struct {
	Frame      string  `json:"frame"`       // which frame ("A" or "B") the edit was applied to
	SourcePath string  `json:"source_path"` // the unedited extracted frame (input to the edit)
	OutputPath string  `json:"output_path"` // the enhanced COPY (output; source frame untouched)
	Filter     string  `json:"filter"`      // the exact ffmpeg filter string applied
	Brightness float64 `json:"brightness"`  // eq brightness delta (-1..1, 0 = none)
	Contrast   float64 `json:"contrast"`    // eq contrast (1 = none)
	Gamma      float64 `json:"gamma"`       // eq gamma (1 = none)
	Saturation float64 `json:"saturation"`  // eq saturation (1 = none)
	Note       string  `json:"note"`        // plain-language reason ("reveal detail in over-exposed shot")
}

// Manifest is the full machine-readable record printed to stdout (or --output)
// and written to <output-dir>/manifest.json so the exhibit page can be rebuilt.
type Manifest struct {
	Tool           string     `json:"tool"`
	GeneratedAt    string     `json:"generated_at"`    // RFC3339
	OutputDir      string     `json:"output_dir"`      // slash path
	ExhibitHTML    string     `json:"exhibit_html"`    // the self-contained HTML exhibit (slash path)
	Interval       float64    `json:"interval"`        // seconds between samples (0 if --fps used)
	FPS            float64    `json:"fps"`             // samples per second (0 if --interval used)
	Threshold      int        `json:"threshold"`       // max Hamming distance for a candidate pair
	MaxPairs       int        `json:"max_pairs"`       // cap on emitted pairs
	EnhanceApplied bool       `json:"enhance_applied"` // whether any honest enhancement ran
	SourceA        SourceInfo `json:"source_a"`
	SourceB        SourceInfo `json:"source_b"`
	PairCount      int        `json:"pair_count"`
	Pairs          []Pair     `json:"pairs"`
	Notes          string     `json:"notes"` // the fixed candidate-not-conclusion note
}
