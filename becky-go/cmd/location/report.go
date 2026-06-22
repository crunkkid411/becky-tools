package main

import (
	"time"

	"becky-go/internal/location"
	"becky-go/internal/osintexport"
)

// Report is the exact JSON schema emitted to stdout (SPEC §3b). Field names and
// shapes mirror the existing osintexport.Sidecar / framematch / identify structs
// so the seam is familiar.
type Report struct {
	Tool              string         `json:"tool"`
	GeneratedAt       string         `json:"generated_at"`
	FingerprintMethod string         `json:"fingerprint_method"` // phash | features | features+phash
	Crop              string         `json:"crop"`
	ClipCount         int            `json:"clip_count"`
	Clips             []ClipReport   `json:"clips"`
	Rooms             []RoomReport   `json:"rooms"`
	RoomCount         int            `json:"room_count"`
	Dwellings         []DwellReport  `json:"dwellings"`
	DwellingCount     int            `json:"dwelling_count"`
	Verdict           VerdictReport  `json:"verdict"`
	PairVerdicts      []PairVerdict  `json:"pair_verdicts"`
	ReviewRequired    []ReviewItem   `json:"review_required"`
	Degraded          []DegradedClip `json:"degraded"`
	Notes             string         `json:"notes"`
}

type ClipReport struct {
	Index          int             `json:"index"`
	Path           string          `json:"path"`
	SHA256         string          `json:"sha256,omitempty"`
	Duration       float64         `json:"duration"`
	KeyframeCount  int             `json:"keyframe_count"`
	RoomID         string          `json:"room_id,omitempty"`
	RoomConfidence float64         `json:"room_confidence"`
	MultiRoom      bool            `json:"multi_room"`
	Segments       []SegmentReport `json:"segments,omitempty"`
	DecorHash      string          `json:"decor_hash,omitempty"`
	Metadata       ClipMetadata    `json:"metadata"`
}

type SegmentReport struct {
	RoomID string  `json:"room_id"`
	Start  float64 `json:"start"`
	End    float64 `json:"end"`
}

type ClipMetadata struct {
	GPS               string `json:"gps"`
	CaptureTime       string `json:"capture_time"`
	CaptureTimeSource string `json:"capture_time_source"`
}

type RoomReport struct {
	RoomID                 string  `json:"room_id"`
	Label                  string  `json:"label"`
	ClipIndices            []int   `json:"clip_indices"`
	MemberCount            int     `json:"member_count"`
	Cohesion               float64 `json:"cohesion"`
	RepresentativeKeyframe string  `json:"representative_keyframe,omitempty"`
	DecorFeatures          string  `json:"decor_features"`
}

type DwellReport struct {
	DwellingID string   `json:"dwelling_id"`
	RoomIDs    []string `json:"room_ids"`
	Basis      []string `json:"basis"`
}

type VerdictReport struct {
	Headline   string   `json:"headline"`
	Level      string   `json:"level"`
	Confidence float64  `json:"confidence"`
	Basis      []string `json:"basis"`
}

type PairVerdict struct {
	A           int         `json:"a"`
	B           int         `json:"b"`
	Level       string      `json:"level"`
	Confidence  float64     `json:"confidence"`
	Signals     PairSignals `json:"signals"`
	Basis       string      `json:"basis"`
	ExhibitHint string      `json:"exhibit_hint,omitempty"`
}

type PairSignals struct {
	DecorHashHamming int     `json:"decor_hash_hamming"`
	ColorChi2        float64 `json:"color_chi2"`
	FeatureInliers   float64 `json:"feature_inliers"` // -1 when unavailable
}

type ReviewItem struct {
	A      int    `json:"a"`
	B      int    `json:"b"`
	Reason string `json:"reason"`
}

type DegradedClip struct {
	Index  int    `json:"index"`
	Path   string `json:"path"`
	Reason string `json:"reason"`
}

const provenanceNote = "Room fingerprints are decor-band perceptual signals, not a geolocation conclusion."

// buildReport assembles the full JSON report from the engine outputs. method is
// "phash"/"features"/"features+phash"; cropName is the crop label.
func buildReport(
	clips []location.Clip,
	cr location.ClusterResult,
	dwellings []location.Dwelling,
	verdict location.Verdict,
	method, cropName string,
	pairsOfInterest [][2]int,
	keyframeRep map[string]string,
) Report {
	r := Report{
		Tool:              "becky-location v1.0.0",
		GeneratedAt:       time.Now().UTC().Format(time.RFC3339),
		FingerprintMethod: method,
		Crop:              cropName,
		ClipCount:         len(clips),
		RoomCount:         len(cr.Rooms),
		DwellingCount:     len(dwellings),
		Notes:             provenanceNote,
	}

	// Clips.
	for _, c := range clips {
		if c.Degraded != "" {
			r.Degraded = append(r.Degraded, DegradedClip{Index: c.Index, Path: c.Path, Reason: c.Degraded})
			continue
		}
		cr2 := ClipReport{
			Index:          c.Index,
			Path:           c.Path,
			SHA256:         c.SHA256,
			Duration:       c.Duration,
			KeyframeCount:  c.KeyframeN,
			RoomID:         cr.RoomOf[c.Index],
			RoomConfidence: clipConfidence(c.Index, cr),
			DecorHash:      osintexport.HashHex(c.Print.DecorHash),
			Metadata: ClipMetadata{
				GPS:         c.GPS,
				CaptureTime: c.CaptureTime,
			},
		}
		r.Clips = append(r.Clips, cr2)
	}

	// Rooms.
	for _, room := range cr.Rooms {
		rep := RoomReport{
			RoomID:        room.ID,
			Label:         room.Label,
			ClipIndices:   room.Clips,
			MemberCount:   len(room.Clips),
			Cohesion:      room.Cohesion,
			DecorFeatures: "decor band (top + side margins): ceiling line, wall corners, trim",
		}
		if kf, ok := keyframeRep[room.ID]; ok {
			rep.RepresentativeKeyframe = kf
		}
		r.Rooms = append(r.Rooms, rep)
	}

	// Dwellings.
	for _, d := range dwellings {
		r.Dwellings = append(r.Dwellings, DwellReport{DwellingID: d.ID, RoomIDs: d.RoomIDs, Basis: d.Basis})
	}

	// Verdict.
	r.Verdict = VerdictReport{
		Headline:   verdict.Headline,
		Level:      string(verdict.Level),
		Confidence: verdict.Confidence,
		Basis:      verdict.Basis,
	}

	// Pair verdicts (for the requested pairs of interest, else high-confidence
	// same-room pairs from the clustering).
	r.PairVerdicts = buildPairVerdicts(clips, cr, pairsOfInterest)

	// Review-required (weak links).
	for _, w := range cr.WeakLinks {
		r.ReviewRequired = append(r.ReviewRequired, ReviewItem{A: w.A, B: w.B, Reason: w.Reason})
	}

	// Ensure non-nil slices for stable JSON.
	if r.Clips == nil {
		r.Clips = []ClipReport{}
	}
	if r.Rooms == nil {
		r.Rooms = []RoomReport{}
	}
	if r.Dwellings == nil {
		r.Dwellings = []DwellReport{}
	}
	if r.PairVerdicts == nil {
		r.PairVerdicts = []PairVerdict{}
	}
	if r.ReviewRequired == nil {
		r.ReviewRequired = []ReviewItem{}
	}
	if r.Degraded == nil {
		r.Degraded = []DegradedClip{}
	}
	return r
}

// clipConfidence reports how strongly a clip belongs to its room (the room
// cohesion; a singleton room is 1.0).
func clipConfidence(idx int, cr location.ClusterResult) float64 {
	id := cr.RoomOf[idx]
	for _, room := range cr.Rooms {
		if room.ID == id {
			return room.Cohesion
		}
	}
	return 0
}
