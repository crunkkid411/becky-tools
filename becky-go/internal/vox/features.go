package vox

// This file is the STUB BOUNDARY between deterministic Go and the audio/DSP world
// (SPEC §4.2). The cloud agent CANNOT decode WAV, run pYIN/CREPE, or
// formant-preserving pitch-shift — so both the analysis-input (audio→features) and
// the render-output (apply warp+pitch) steps are interfaces the local agent
// implements by shelling internal/pyhelpers/vox_align.py. Everything downstream
// (dtw/align/comp/pitch) consumes the plain structs these interfaces return and is
// fully unit-testable with synthetic arrays — NO audio, NO DSP libs, NO network.
//
// LOCAL-AGENT CONTRACT — internal/pyhelpers/vox_align.py (SPEC §4.2):
//
//	INPUT  (argv/stdin JSON): {guideWav, altWav, mode:"stack"|"tune", scale{key,mode},
//	         timing{bandMs,weights{onset,mfcc,chroma}}, pitch{engine,f0,crossCheck,
//	         maxShiftSemis}, accept[] (render mask), seed:0}
//	OUTPUT (stdout JSON, analysis SEPARATE from rendered audio):
//	  {"features":{"guide":[{t,onset,mfcc,chroma}],"alt":[...]},  # -> Aligner.Features
//	   "guideNotes":[{startMs,endMs,hz,confidence}],             # F0 notes for pitch
//	   "altNotes":[...], "crossCheckHz":[...],                   # Praat 2nd estimate
//	   "durationsMs":{guide,alt}, "alignedWav":path, "alignedMid":path,
//	   "degraded":null|reason}
//	on any failure: {"skipped":true,"reason":"..."}  (Go side emits a clean degrade)
//
// The Go dtw.go/align.go/comp.go do ALL deterministic work (DTW, warp map,
// confidence, comp scoring); the helper's only job is F0 tracking and the
// formant-preserving render. A FixtureAligner (below) feeds canned Features so the
// whole pipeline runs on the cloud with no audio present.

// Aligner is the analysis-input stub: WAV(s) -> normalized feature sequences + F0
// notes. The local agent implements it over vox_align.py; the cloud uses
// FixtureAligner.
type Aligner interface {
	// Features returns the guide and alt feature sequences plus their detected note
	// lists. A degrade (whisper/over-driven take, missing F0) is Features.Skipped +
	// Reason, not an error.
	Features(guideWav, altWav, mode string) (VoxFeatures, error)
}

// Renderer is the render-output stub: apply an approved warp+pitch plan and write
// the aligned WAV/MIDI. The cloud never calls a real renderer; main.go records the
// intended paths and leaves the bake to the local agent.
type Renderer interface {
	// Render applies the accept-masked plan and returns the written audio/midi paths.
	Render(plan RenderPlan) (alignedWav, alignedMid string, err error)
}

// VoxFeatures is everything the analysis pipeline consumes across the audio
// boundary; tests build it by hand.
type VoxFeatures struct {
	Guide      []FeatureFrame `json:"guide"`
	Alt        []FeatureFrame `json:"alt"`
	GuideNotes []DetectedNote `json:"guideNotes"`
	AltNotes   []DetectedNote `json:"altNotes"`
	CrossCheck []float64      `json:"crossCheckHz,omitempty"` // Praat 2nd-estimate per alt note
	DurGuideMs float64        `json:"durGuideMs"`
	DurAltMs   float64        `json:"durAltMs"`
	Skipped    bool           `json:"skipped,omitempty"`
	Reason     string         `json:"reason,omitempty"`
}

// DetectedNote is an F0-tracked note (pYIN/CREPE) before alignment. Hz is the
// detected pitch; Confidence is the tracker's voicing/HMM confidence.
type DetectedNote struct {
	StartMs    float64 `json:"startMs"`
	EndMs      float64 `json:"endMs"`
	Hz         float64 `json:"hz"`
	Confidence float64 `json:"confidence"`
}

// RenderPlan is the accept-masked instruction the renderer bakes (SPEC §5 step 4):
// the warp map + the notes to move, plus a per-item accept mask so only approved
// edits are applied.
type RenderPlan struct {
	GuideWav string      `json:"guideWav"`
	AltWav   string      `json:"altWav"`
	Mode     string      `json:"mode"`
	WarpMap  []WarpEntry `json:"warpMap"`
	Notes    []VoxNote   `json:"notes"`
	Accept   []bool      `json:"accept"` // per-note: apply this move?
}

// FixtureAligner is the cloud-testable Aligner: it returns canned features so the
// deterministic pipeline runs with no audio. The local agent swaps in a real
// Aligner shelling the pyhelper; the VoxFeatures contract is identical.
type FixtureAligner struct {
	Feats VoxFeatures
	Err   error
}

// Features returns the canned features (ignores its arguments).
func (f FixtureAligner) Features(_, _, _ string) (VoxFeatures, error) {
	return f.Feats, f.Err
}
