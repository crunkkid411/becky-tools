// Package bounce is the deterministic CORE of "bounce in place" — the standard
// producer move: render ONE track through its FX chain to audio, then replace its
// MIDI clip(s) with the rendered audio clip and mark the heavy FX as baked/bypassed
// so the CPU is freed. The actual audio rendering THROUGH real VST FX is the C++
// audio-host's job (local, hardware); this package owns only the parts that are
// pure, offline, and deterministic:
//
//   - PlanBounce  — WHAT to render and to WHICH wav path (a render plan the engine
//     fills in by rendering the track's notes through its FX chain).
//   - ApplyBounce — the ARRANGEMENT TRANSFORMATION: the post-bounce arrangement
//     where the target track is now a KindAudio track referencing the bounced wav,
//     its MIDI notes removed, and the bounce recorded so the chain is treated as
//     baked.
//
// Both functions are IMMUTABLE (they return a NEW arrangement / value and never
// mutate the input) and DEGRADE-NEVER-CRASH (a missing track is a typed error, not
// a panic).
//
// Representation note (an honest limitation of the current dawmodel): dawmodel.Clip
// has no audio-file-path field and dawmodel.Strip has no FX-chain field. So a
// bounced clip is represented MINIMALLY — a KindAudio track whose clip is NAMED
// after the wav (and carries the wav path in its Name) — and the "FX baked" fact is
// recorded as a dawmodel.Correction (kind "bounce") rather than flipping a real
// per-FX bypass flag. See the package README in ApplyBounce's doc + the task RETURN
// for the recommended dawmodel additions (Clip.File string, Strip.FX with Bypass).
package bounce

import (
	"fmt"
	"path/filepath"

	"becky-go/internal/dawmodel"
)

// CorrectionKindBounce is the dawmodel.Correction.Kind stamped on the arrangement
// when a track is bounced in place, so downstream tools (and the engine) know the
// track's FX chain has been baked into audio and should be bypassed.
const CorrectionKindBounce = "bounce"

// Plan is the render plan for bouncing one track in place. The deterministic core
// produces it; the audio engine / C++ host consumes it: render Track's notes
// THROUGH its FX chain and write the result to WavPath, then call ApplyBounce.
type Plan struct {
	Track      string `json:"track"`          // ID of the track to bounce
	WavPath    string `json:"wavPath"`        // where the engine must write the rendered audio
	NoteCount  int    `json:"noteCount"`      // how many MIDI notes will be rendered (a sanity probe)
	BPM        int    `json:"bpm"`            // transport context the engine renders at
	PPQ        int    `json:"ppq"`            // ticks-per-quarter context
	SourceKind string `json:"sourceKind"`     // dawmodel.KindMIDI | KindAudio of the source track
	Note       string `json:"note,omitempty"` // a plain-language note (e.g. "already audio")
}

// PlanBounce computes the bounce plan for trackID: the wav path (outDir joined with a
// deterministic "<trackID>.bounce.wav" filename) and how many notes the engine will
// render. It NEVER mutates arr. Degrade-never-crash: a nil arrangement or a missing
// track returns a typed error, not a panic. A track that is already KindAudio is a
// valid no-op plan (Note explains it) — the caller can skip rendering.
func PlanBounce(arr *dawmodel.Arrangement, trackID, outDir string) (Plan, error) {
	if arr == nil {
		return Plan{}, fmt.Errorf("bounce: nil arrangement")
	}
	if trackID == "" {
		return Plan{}, fmt.Errorf("bounce: empty track id")
	}
	t, ok := arr.TrackByID(trackID)
	if !ok {
		return Plan{}, fmt.Errorf("bounce: track %q not found", trackID)
	}

	bpm := arr.BPM
	if bpm <= 0 {
		bpm = 120
	}
	ppq := arr.PPQ
	if ppq <= 0 {
		ppq = 480
	}

	p := Plan{
		Track:      trackID,
		WavPath:    bounceWavPath(outDir, trackID),
		NoteCount:  trackNoteCount(t),
		BPM:        bpm,
		PPQ:        ppq,
		SourceKind: t.Kind,
	}
	if t.Kind == dawmodel.KindAudio {
		p.Note = "track is already audio; nothing to render (no-op bounce)"
	} else if p.NoteCount == 0 {
		p.Note = "track has no notes; the engine will render silence"
	}
	return p, nil
}

// ApplyBounce returns a NEW arrangement in which trackID has been bounced in place:
//
//   - the track becomes dawmodel.KindAudio;
//   - its MIDI clips are replaced by a SINGLE audio clip whose Name is the wav path
//     (the minimal "reference to the rendered file" the current dawmodel allows);
//   - the original MIDI notes are removed (freeing the synth/FX CPU);
//   - the mixer Strip (gain/pan/bus/mute/solo) is PRESERVED so the track sits in the
//     mix exactly where it did;
//   - a dawmodel.Correction (Kind "bounce") is appended recording {Auto: midi,
//     Fixed: <wavPath>} so the FX chain is known to be baked/bypassed.
//
// arr is never mutated. Degrade-never-crash: nil arrangement, empty wav path, or a
// missing track each return a typed error.
func ApplyBounce(arr *dawmodel.Arrangement, trackID, wavPath string) (*dawmodel.Arrangement, error) {
	if arr == nil {
		return nil, fmt.Errorf("bounce: nil arrangement")
	}
	if trackID == "" {
		return nil, fmt.Errorf("bounce: empty track id")
	}
	if wavPath == "" {
		return nil, fmt.Errorf("bounce: empty wav path")
	}
	if _, ok := arr.TrackByID(trackID); !ok {
		return nil, fmt.Errorf("bounce: track %q not found", trackID)
	}

	// AddTrack clones the arrangement (the immutability boundary) and gives us an
	// owned copy to transform; we then overwrite the target track in place on the
	// copy. We avoid AddTrack's append (it would add a new track) and instead build
	// the new arrangement by cloning via an existing immutable op that deep-copies.
	out := cloneArr(arr)

	for i := range out.Tracks {
		if out.Tracks[i].ID != trackID {
			continue
		}
		strip := out.Tracks[i].Strip // preserve the mixer placement
		out.Tracks[i].Kind = dawmodel.KindAudio
		out.Tracks[i].Clips = []dawmodel.Clip{{
			Name:    wavPath,
			Channel: 0,
			Program: -1, // audio: not a GM program
			Offset:  0,  // bounced from the song origin
			// Notes intentionally empty (the MIDI is now baked into audio).
			// Peaks left empty — the engine fills the visual overview after render.
		}}
		out.Tracks[i].Strip = strip
		break
	}

	// Record the bounce so the FX chain is treated as baked/bypassed. dawmodel has no
	// per-FX bypass flag, so the corrections log is the durable marker.
	out.Corrections = append(out.Corrections, dawmodel.Correction{
		Kind:  CorrectionKindBounce,
		Clip:  trackID,
		At:    0,
		Auto:  dawmodel.KindMIDI,
		Fixed: wavPath,
		Genre: out.Genre,
		BPM:   out.BPM,
		Scale: out.Scale,
	})

	return out, nil
}

// IsBounced reports whether trackID has been bounced in place in arr (i.e. a
// CorrectionKindBounce entry exists for it). Lets the engine know to bypass the FX.
func IsBounced(arr *dawmodel.Arrangement, trackID string) bool {
	if arr == nil {
		return false
	}
	for _, c := range arr.Corrections {
		if c.Kind == CorrectionKindBounce && c.Clip == trackID {
			return true
		}
	}
	return false
}

// bounceWavPath is the deterministic wav filename for a bounced track: a missing
// outDir means the current directory. The name is stable so the same bounce always
// targets the same path.
func bounceWavPath(outDir, trackID string) string {
	name := trackID + ".bounce.wav"
	if outDir == "" {
		return name
	}
	return filepath.Join(outDir, name)
}

// trackNoteCount totals the notes across a track's clips.
func trackNoteCount(t dawmodel.Track) int {
	n := 0
	for _, c := range t.Clips {
		n += len(c.Notes)
	}
	return n
}

// cloneArr returns a deep copy of arr using only exported dawmodel surface, so this
// package never mutates the caller's arrangement and never reaches into dawmodel's
// internals. AddTrack deep-clones the whole arrangement; we use a sentinel track ID
// unlikely to collide, then drop it, yielding a clean deep copy.
func cloneArr(arr *dawmodel.Arrangement) *dawmodel.Arrangement {
	const sentinel = "\x00bounce-clone-sentinel\x00"
	out := arr.AddTrack(sentinel, dawmodel.KindMIDI)
	// Drop the sentinel track we just added.
	tracks := out.Tracks[:0]
	for _, t := range out.Tracks {
		if t.ID == sentinel {
			continue
		}
		tracks = append(tracks, t)
	}
	out.Tracks = tracks
	return out
}
