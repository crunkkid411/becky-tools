// Package facetrack turns per-frame face DETECTIONS into persistent TRACKS —
// "this face in frame 400 is the same person as in frame 100" — which is the
// capability every stage downstream of detection depends on and which becky has
// never had.
//
// Why this exists (research/shorts-models.md §3.5): becky already runs a
// best-in-class detector (SCRFD-10GF via InsightFace buffalo_l) and a
// best-in-class face embedding (ArcFace w600k_r50), but nothing joins their
// output across time. cmd/identify samples 1 fps, caps at 60 frames, and keeps
// only the single most prominent face per frame, so there is no notion of
// identity persistence at all. Without tracks:
//
//   - an active-speaker model has no stable subject to score,
//   - a 9:16 crop has nothing to follow, so it re-decides every frame and jitters,
//   - "who is on screen during [t0,t1]" cannot be answered by counting frames.
//
// The association here is deliberately NOT a general-purpose MOT tracker.
// BoostTrack and friends are crowd-scale machinery; for the one-to-three faces in
// a talking-head video, geometric overlap plus the ArcFace embedding becky
// already computes is both sufficient and free of a new dependency. If real
// footage proves otherwise, research/shorts-models.md §7 names BoostTrack as the
// escalation.
//
// Deterministic by construction: same detections in, same tracks out, with
// explicit tie-breaks everywhere a score could tie. No model, no I/O, no exec.
package facetrack

import (
	"math"
	"sort"
)

// Detection is one face found in one frame. Vector is optional: with it,
// association survives occlusion and fast motion that pure geometry loses;
// without it, tracking degrades to IoU-only and still works (degrade, never
// crash).
type Detection struct {
	// Frame is the source frame index. Detections are grouped by it, so it must
	// be consistent across the whole input.
	Frame int `json:"frame"`
	// Time is the frame's timestamp in seconds.
	Time float64 `json:"time"`
	// BBox is [x1,y1,x2,y2] in pixels, matching faceembed.Face.BBox.
	BBox [4]float64 `json:"bbox"`
	// Vector is the L2-normalised 512-d ArcFace embedding, or nil.
	Vector []float64 `json:"vector,omitempty"`
	// DetScore is the detector's confidence.
	DetScore float64 `json:"det_score"`
}

// Track is one person's continuous presence, as a time-ordered detection list.
type Track struct {
	ID         int         `json:"id"`
	Detections []Detection `json:"detections"`
}

// Start is the track's first timestamp.
func (t Track) Start() float64 {
	if len(t.Detections) == 0 {
		return 0
	}
	return t.Detections[0].Time
}

// End is the track's last timestamp.
func (t Track) End() float64 {
	if len(t.Detections) == 0 {
		return 0
	}
	return t.Detections[len(t.Detections)-1].Time
}

// Duration is how long the track was on screen, first sighting to last.
func (t Track) Duration() float64 { return t.End() - t.Start() }

// Centroid returns the mean centre of the track's boxes — the anchor a crop
// path follows.
func (t Track) Centroid() (x, y float64) {
	if len(t.Detections) == 0 {
		return 0, 0
	}
	for _, d := range t.Detections {
		cx, cy := center(d.BBox)
		x += cx
		y += cy
	}
	n := float64(len(t.Detections))
	return x / n, y / n
}

// CoverageIn reports how much of the window [t0,t1] this track was actually
// detected in, as a fraction 0..1, plus the number of detections inside it.
//
// This is what makes a track answer becky's real question — "was this person on
// screen during [t0,t1]?" — with a MEASURE rather than a yes/no. A track that
// clips the edge of a window covers a little of it; one present throughout
// covers most of it. The caller decides the threshold and states it, instead of
// a boolean hiding the evidence (FORENSIC-OUTPUT-PHILOSOPHY.md).
//
// Coverage is computed from detection timestamps, so it reflects where the face
// was actually SEEN, not merely that the track spans the window.
func (t Track) CoverageIn(t0, t1 float64) (fraction float64, n int) {
	if t1 <= t0 || len(t.Detections) == 0 {
		return 0, 0
	}
	var first, last float64
	for _, d := range t.Detections {
		if d.Time < t0 || d.Time > t1 {
			continue
		}
		if n == 0 {
			first = d.Time
		}
		last = d.Time
		n++
	}
	if n == 0 {
		return 0, 0
	}
	if n == 1 {
		// A single sighting proves presence at an instant, not across a span.
		return 0, 1
	}
	return (last - first) / (t1 - t0), n
}

// Options tune the association. Use DefaultOptions and adjust.
type Options struct {
	// IoUThreshold is the minimum geometric overlap for a match on position
	// alone. 0.3 tolerates ordinary head movement between adjacent frames.
	IoUThreshold float64
	// CosineThreshold is the minimum ArcFace similarity that can RESCUE a match
	// whose IoU fell short — the case where a head turns fast or is briefly
	// occluded. Set high: a wrong identity join is far worse than a split track,
	// because it silently merges two people into one.
	CosineThreshold float64
	// MaxGapFrames is how many frames a track may go undetected and still be
	// continued rather than ended. Beyond this it is closed.
	MaxGapFrames int
	// MinDetections drops tracks too short to be anything but a false positive.
	MinDetections int
	// IoUWeight balances geometry against appearance when both are available.
	IoUWeight float64
}

// DefaultOptions is the shipped configuration, tuned for talking-head footage.
func DefaultOptions() Options {
	return Options{
		IoUThreshold:    0.30,
		CosineThreshold: 0.55,
		MaxGapFrames:    12,
		MinDetections:   3,
		IoUWeight:       0.5,
	}
}

// Build groups detections into tracks. Input order does not matter — detections
// are sorted by frame first — and the result is deterministic.
func Build(dets []Detection, opt Options) []Track {
	opt = withDefaults(opt)
	if len(dets) == 0 {
		return nil
	}

	frames := groupByFrame(dets)

	var active []*Track
	var done []*Track
	nextID := 1

	for _, fr := range frames {
		// Retire stale tracks BEFORE associating, not after. Only frames that
		// actually contain detections appear in `frames`, so a long absence is an
		// invisible jump in the frame index rather than a run of empty iterations.
		// Closing afterwards let a track that had not been seen for 40 frames
		// still win this frame's detection — silently merging two separate
		// appearances (and, on real footage, two different people who happened to
		// stand in the same spot) into one identity.
		var stillActive []*Track
		for _, t := range active {
			if fr.frame-t.Detections[len(t.Detections)-1].Frame > opt.MaxGapFrames {
				done = append(done, t)
				continue
			}
			stillActive = append(stillActive, t)
		}
		active = stillActive

		matched := associate(active, fr.dets, opt)

		// Extend matched tracks.
		usedDet := make([]bool, len(fr.dets))
		for trackIdx, detIdx := range matched {
			if detIdx < 0 {
				continue
			}
			active[trackIdx].Detections = append(active[trackIdx].Detections, fr.dets[detIdx])
			usedDet[detIdx] = true
		}

		// Unmatched detections start new tracks.
		for i, d := range fr.dets {
			if usedDet[i] {
				continue
			}
			active = append(active, &Track{ID: nextID, Detections: []Detection{d}})
			nextID++
		}
	}
	done = append(done, active...)

	out := make([]Track, 0, len(done))
	for _, t := range done {
		if len(t.Detections) < opt.MinDetections {
			continue
		}
		out = append(out, *t)
	}
	// Stable, meaningful order: earliest sighting first, then by ID.
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Start() != out[j].Start() {
			return out[i].Start() < out[j].Start()
		}
		return out[i].ID < out[j].ID
	})
	return out
}

// frameGroup is one frame's detections.
type frameGroup struct {
	frame int
	dets  []Detection
}

// groupByFrame buckets detections by frame index, in ascending frame order, with
// each frame's detections in a stable order (by box position) so association is
// reproducible regardless of how the caller ordered them.
func groupByFrame(dets []Detection) []frameGroup {
	byFrame := map[int][]Detection{}
	for _, d := range dets {
		byFrame[d.Frame] = append(byFrame[d.Frame], d)
	}
	frames := make([]int, 0, len(byFrame))
	for f := range byFrame {
		frames = append(frames, f)
	}
	sort.Ints(frames)

	out := make([]frameGroup, 0, len(frames))
	for _, f := range frames {
		ds := byFrame[f]
		sort.SliceStable(ds, func(i, j int) bool {
			if ds[i].BBox[0] != ds[j].BBox[0] {
				return ds[i].BBox[0] < ds[j].BBox[0]
			}
			return ds[i].BBox[1] < ds[j].BBox[1]
		})
		out = append(out, frameGroup{frame: f, dets: ds})
	}
	return out
}

// pairing is one candidate track<->detection assignment with its score.
type pairing struct {
	track int
	det   int
	score float64
}

// associate greedily assigns this frame's detections to active tracks, best
// score first. Greedy (not Hungarian) is deliberate: with one to three faces the
// assignments are near-trivial, and greedy with an explicit tie-break is easy to
// reason about and reproduce. Returns, per active track, the detection index it
// matched, or -1.
func associate(active []*Track, dets []Detection, opt Options) []int {
	out := make([]int, len(active))
	for i := range out {
		out[i] = -1
	}
	if len(active) == 0 || len(dets) == 0 {
		return out
	}

	var pairs []pairing
	for ti, t := range active {
		last := t.Detections[len(t.Detections)-1]
		for di, d := range dets {
			if s, ok := score(last, d, opt); ok {
				pairs = append(pairs, pairing{track: ti, det: di, score: s})
			}
		}
	}
	// Best first; ties broken by track then detection index so the result cannot
	// depend on map iteration or slice order.
	sort.SliceStable(pairs, func(i, j int) bool {
		if pairs[i].score != pairs[j].score {
			return pairs[i].score > pairs[j].score
		}
		if pairs[i].track != pairs[j].track {
			return pairs[i].track < pairs[j].track
		}
		return pairs[i].det < pairs[j].det
	})

	takenTrack := make([]bool, len(active))
	takenDet := make([]bool, len(dets))
	for _, p := range pairs {
		if takenTrack[p.track] || takenDet[p.det] {
			continue
		}
		takenTrack[p.track] = true
		takenDet[p.det] = true
		out[p.track] = p.det
	}
	return out
}

// score rates a track's last detection against a new one, and reports whether
// the pair is eligible to match at all.
//
// Eligibility is the important half. A pair qualifies when the geometry agrees
// (IoU over threshold) OR — when both carry embeddings — appearance agrees
// strongly enough to rescue a geometric miss. Appearance alone can join across a
// fast head turn; geometry alone works when embeddings are absent. Requiring
// BOTH would drop every occlusion; requiring neither would merge strangers who
// happen to stand in the same place.
func score(prev, next Detection, opt Options) (float64, bool) {
	iou := IoU(prev.BBox, next.BBox)
	cos, hasVec := cosine(prev.Vector, next.Vector)

	geomOK := iou >= opt.IoUThreshold
	appearOK := hasVec && cos >= opt.CosineThreshold
	if !geomOK && !appearOK {
		return 0, false
	}
	if !hasVec {
		return iou, true
	}
	w := opt.IoUWeight
	return w*iou + (1-w)*cos, true
}

// IoU is the intersection-over-union of two [x1,y1,x2,y2] boxes. Degenerate or
// inverted boxes score 0 rather than producing a negative area.
func IoU(a, b [4]float64) float64 {
	ax1, ay1, ax2, ay2 := order(a)
	bx1, by1, bx2, by2 := order(b)

	ix1, iy1 := math.Max(ax1, bx1), math.Max(ay1, by1)
	ix2, iy2 := math.Min(ax2, bx2), math.Min(ay2, by2)
	iw, ih := ix2-ix1, iy2-iy1
	if iw <= 0 || ih <= 0 {
		return 0
	}
	inter := iw * ih
	areaA := (ax2 - ax1) * (ay2 - ay1)
	areaB := (bx2 - bx1) * (by2 - by1)
	union := areaA + areaB - inter
	if union <= 0 {
		return 0
	}
	return inter / union
}

// order normalises a box so x1<=x2 and y1<=y2.
func order(b [4]float64) (x1, y1, x2, y2 float64) {
	x1, x2 = math.Min(b[0], b[2]), math.Max(b[0], b[2])
	y1, y2 = math.Min(b[1], b[3]), math.Max(b[1], b[3])
	return
}

// center returns a box's centre point.
func center(b [4]float64) (x, y float64) {
	x1, y1, x2, y2 := order(b)
	return (x1 + x2) / 2, (y1 + y2) / 2
}

// cosine returns the cosine similarity of two embeddings and whether both were
// present and usable. ArcFace vectors arrive L2-normalised, so this is a dot
// product — but the magnitudes are divided out anyway, so a caller that supplies
// un-normalised vectors still gets a correct similarity instead of a silently
// wrong one.
func cosine(a, b []float64) (float64, bool) {
	if len(a) == 0 || len(b) == 0 || len(a) != len(b) {
		return 0, false
	}
	var dot, na, nb float64
	for i := range a {
		dot += a[i] * b[i]
		na += a[i] * a[i]
		nb += b[i] * b[i]
	}
	if na == 0 || nb == 0 {
		return 0, false
	}
	return dot / (math.Sqrt(na) * math.Sqrt(nb)), true
}

func withDefaults(opt Options) Options {
	d := DefaultOptions()
	if opt.IoUThreshold <= 0 {
		opt.IoUThreshold = d.IoUThreshold
	}
	if opt.CosineThreshold <= 0 {
		opt.CosineThreshold = d.CosineThreshold
	}
	if opt.MaxGapFrames <= 0 {
		opt.MaxGapFrames = d.MaxGapFrames
	}
	if opt.MinDetections <= 0 {
		opt.MinDetections = d.MinDetections
	}
	if opt.IoUWeight <= 0 || opt.IoUWeight > 1 {
		opt.IoUWeight = d.IoUWeight
	}
	return opt
}
