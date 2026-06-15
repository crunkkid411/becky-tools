package hum

// This file is the STUB BOUNDARY between deterministic Go and the audio/ML world.
// The cloud agent CANNOT decode WAV, run pYIN/basic-pitch, or call ffmpeg — so the
// audio→features step is an interface the local agent implements by shelling the
// pyhelper. Everything downstream (keyfind/tempo/segment/suggest) consumes the
// plain structs an Extractor returns and is fully unit-testable with synthetic
// arrays — NO audio files, NO DSP libs, NO network on the cloud side.
//
// LOCAL-AGENT CONTRACT (mirrors internal/pyhelpers/pitch_basicpitch.py, SPEC §6):
//
//	stdin/args: --wav <16k-mono.wav> [--engine basic-pitch|pyin] [--device auto|cpu|cuda]
//	stdout (one line JSON):
//	  {"engine","version",
//	   "frames":[{"t":sec,"f0":hz,"voiced":prob}],   // F0 contour -> Features.Frames
//	   "notes":[{"onset":sec,"dur":sec,"midi":int,"bend":cents,"confidence":prob}],
//	   "onsets":[sec,...],                            // onset times -> Features.Onsets (tempo)
//	   "durationSec":float,"normalizeGainDb":float,
//	   "device","engineUsed","fellBack"[,"fallbackReason"]}
//	on any failure: {"skipped":true,"reason":"..."} and exit 0  (Go emits a clean degrade)
//
// The local agent writes a thin Go shim implementing Extractor that runs that
// helper and unmarshals its JSON into Features. The Go side does ALL deterministic
// work; the helper's only job is model inference. A FixtureExtractor (below) feeds
// canned Features so the whole pipeline runs on the cloud with no model present.
type Extractor interface {
	// Extract decodes/normalizes wavPath (16 kHz mono) and returns the F0 contour,
	// optional native notes, onset times, and ingest metadata. A degrade (missing
	// model/ffmpeg, silent take) is returned as Features.Skipped=true + Reason, not
	// an error — degrade-never-crash. A genuine I/O failure is returned as err.
	Extract(wavPath, engine, device string) (Features, error)
}

// Features is the model/DSP output the Go pipeline consumes. It is the ONLY thing
// crossing the audio boundary; tests build it by hand.
type Features struct {
	Engine          string     `json:"engine"`
	Version         string     `json:"version"`
	Frames          []Frame    `json:"frames"`
	Notes           []StubNote `json:"notes,omitempty"`
	Onsets          []float64  `json:"onsets,omitempty"`
	DurationSec     float64    `json:"durationSec"`
	NormalizeGainDb float64    `json:"normalizeGainDb"`
	Device          string     `json:"device,omitempty"`
	FellBack        bool       `json:"fellBack,omitempty"`
	FallbackReason  string     `json:"fallbackReason,omitempty"`
	Skipped         bool       `json:"skipped,omitempty"`
	Reason          string     `json:"reason,omitempty"`
	Peaks           []float64  `json:"peaks,omitempty"` // downsampled waveform envelope (visual lane)
}

// StubNote is a note as the basic-pitch helper emits it (native note output with
// pitch bend). When present, segment.go can use these directly instead of
// re-segmenting the raw contour. Bend is cents of pitch-bend at onset.
type StubNote struct {
	Onset      float64 `json:"onset"`
	Dur        float64 `json:"dur"`
	Midi       int     `json:"midi"`
	Bend       float64 `json:"bend"`
	Confidence float64 `json:"confidence"`
}

// FixtureExtractor is the cloud-testable Extractor: it returns canned Features so
// the deterministic pipeline runs with no audio, no model, no network. The local
// agent swaps in a real Extractor that shells the pyhelper; the contract (Features)
// is identical, so nothing downstream changes.
type FixtureExtractor struct {
	Feats Features
	Err   error
}

// Extract returns the canned features (ignores its arguments).
func (f FixtureExtractor) Extract(_, _, _ string) (Features, error) {
	return f.Feats, f.Err
}
