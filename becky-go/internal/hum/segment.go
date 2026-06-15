package hum

import "math"

// Monophonic pitch contour -> discrete MIDI notes (SPEC §3 stage 4 segmentation).
// This is the deterministic segmentation logic that runs on whichever engine's
// frames (pYIN floor or basic-pitch). It operates on a Frame array, so it is fully
// testable with a synthetic F0 contour — no audio. The steps mirror the SPEC:
// F0->semitone (voicing-gated), onset on a confident pitch change, median-pitch per
// note, min-note-length filter, optional rhythmic quantization to a beat grid.

// SegmentOptions are the fixed-but-tunable thresholds (the local agent tunes these
// on real off-key takes; the cloud floor uses these defaults).
type SegmentOptions struct {
	VoicedMin     float64 // drop frames below this voicing probability (unvoiced/breath)
	MinNoteSec    float64 // notes shorter than this are dropped (blips/clicks)
	PitchJumpSemi float64 // a jump >= this many semitones starts a new note
	QuantDiv      int     // beat subdivisions to snap to (e.g. 16 = 1/16); 0 = off
	BPM           int     // tempo for quantization (0 with QuantDiv>0 => no snap)
}

// DefaultSegmentOptions are the SPEC's starting thresholds.
func DefaultSegmentOptions() SegmentOptions {
	return SegmentOptions{VoicedMin: 0.5, MinNoteSec: 0.06, PitchJumpSemi: 0.6, QuantDiv: 0}
}

// SegmentNotes turns a frame contour into notes. If stubNotes is non-empty (a
// model like basic-pitch already produced discrete notes), those are used directly
// (and confidence-carried); otherwise the contour is segmented by this code.
func SegmentNotes(frames []Frame, stubNotes []StubNote, opt SegmentOptions) []Note {
	var notes []Note
	if len(stubNotes) > 0 {
		notes = fromStubNotes(stubNotes)
	} else {
		notes = segmentContour(frames, opt)
	}
	notes = dropShort(notes, opt.MinNoteSec)
	if opt.QuantDiv > 0 && opt.BPM > 0 {
		notes = quantize(notes, opt.BPM, opt.QuantDiv)
	}
	for i := range notes {
		notes[i].I = i
	}
	return notes
}

// segmentContour groups contiguous voiced frames into notes, splitting on a pitch
// jump beyond PitchJumpSemi. Each note's MIDI is the MEDIAN semitone over its frames
// (robust to scoops/vibrato, SPEC §3 step 3).
func segmentContour(frames []Frame, opt SegmentOptions) []Note {
	var notes []Note
	var cur []Frame
	flush := func() {
		if len(cur) > 0 {
			notes = append(notes, noteFromFrames(cur))
			cur = nil
		}
	}
	prevMidi := math.NaN()
	for _, f := range frames {
		if f.Voiced < opt.VoicedMin || f.F0 <= 0 {
			flush()
			prevMidi = math.NaN()
			continue
		}
		m := HzToMidiF(f.F0)
		if !math.IsNaN(prevMidi) && math.Abs(m-prevMidi) >= opt.PitchJumpSemi {
			flush()
		}
		cur = append(cur, f)
		prevMidi = m
	}
	flush()
	return notes
}

// noteFromFrames builds one Note from its contiguous voiced frames.
func noteFromFrames(fr []Frame) Note {
	midis := make([]float64, len(fr))
	hzs := make([]float64, len(fr))
	confs := make([]float64, len(fr))
	for i, f := range fr {
		midis[i] = HzToMidiF(f.F0)
		hzs[i] = f.F0
		confs[i] = f.Voiced
	}
	medMidi := medianFloat(midis)
	onset := fr[0].T
	dur := fr[len(fr)-1].T - fr[0].T
	if dur < 0 {
		dur = 0
	}
	return Note{
		OnsetSec:   round4(onset),
		DurSec:     round4(dur),
		Midi:       int(math.Round(medMidi)),
		PitchHz:    round2(medianFloat(hzs)),
		Confidence: round2(meanFloat(confs)),
	}
}

// fromStubNotes adopts model-native notes (basic-pitch). The note's confidence is
// kept; pitch Hz is derived from the MIDI number for the JSON.
func fromStubNotes(sn []StubNote) []Note {
	out := make([]Note, 0, len(sn))
	for _, n := range sn {
		out = append(out, Note{
			OnsetSec:   round4(n.Onset),
			DurSec:     round4(n.Dur),
			Midi:       n.Midi,
			PitchHz:    round2(MidiToHz(float64(n.Midi) + n.Bend/100)),
			Confidence: round2(clamp01(n.Confidence)),
		})
	}
	return out
}

// dropShort removes notes shorter than minSec (kills breath clicks / blips).
func dropShort(notes []Note, minSec float64) []Note {
	if minSec <= 0 {
		return notes
	}
	out := make([]Note, 0, len(notes))
	for _, n := range notes {
		if n.DurSec >= minSec {
			out = append(out, n)
		}
	}
	return out
}

// quantize snaps onset and duration to the nearest beat subdivision (SPEC §3 step
// 5). step = (60/BPM)/div. Duration is kept >= one step so a note never collapses
// to zero. Deterministic rounding.
func quantize(notes []Note, bpm, div int) []Note {
	if bpm <= 0 || div <= 0 {
		return notes
	}
	step := (60.0 / float64(bpm)) / float64(div)
	for i := range notes {
		on := math.Round(notes[i].OnsetSec/step) * step
		steps := math.Round(notes[i].DurSec / step)
		if steps < 1 {
			steps = 1
		}
		notes[i].OnsetSec = round4(on)
		notes[i].DurSec = round4(steps * step)
	}
	return notes
}

// BuildLane produces the VISUAL-FIRST pitch lane (SPEC HARD REQUIREMENT): a
// downsampled, per-frame editable pitch curve plus the note blobs. The GUI draws
// this; moving a point becomes a Correction. maxPoints caps the curve length so a
// long take doesn't bloat the JSON (deterministic stride).
func BuildLane(frames []Frame, notes []Note, maxPoints int) PitchLane {
	if maxPoints <= 0 {
		maxPoints = 600
	}
	stride := 1
	if len(frames) > maxPoints {
		stride = (len(frames) + maxPoints - 1) / maxPoints
	}
	curve := make([]LanePoint, 0, maxPoints)
	for i := 0; i < len(frames); i += stride {
		f := frames[i]
		curve = append(curve, LanePoint{
			T:        round4(f.T),
			MidiF:    round2(HzToMidiF(f.F0)),
			Voiced:   round2(f.Voiced),
			Editable: true,
		})
	}
	return PitchLane{Curve: curve, Notes: notes}
}
