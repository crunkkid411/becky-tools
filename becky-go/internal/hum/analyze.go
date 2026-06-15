package hum

import (
	"fmt"

	"becky-go/internal/music"
)

// analyze.go is the deterministic orchestrator (SPEC §3 stages 2-6). It takes the
// Features that crossed the audio boundary (from any Extractor) plus the run
// options, runs key/tempo/segment/suggest, builds the visual lane + corrections
// log, and assembles the Result. It also renders the melody to a Standard MIDI File
// via the EXISTING internal/music writer (no new MIDI code). Pure + deterministic:
// same Features + same Options => byte-identical Result and MIDI.

// Options are the run knobs the CLI passes through (SPEC §4 flags).
type Options struct {
	Wav        string
	KeyHint    string // skip key detection if set (still reported as the key)
	Genre      string // resolve tempo octave + chord context from this profile
	Engine     string // "basic-pitch" | "pyin" (echoed; the stub picks the real path)
	QuantDiv   int    // 16 = snap to 1/16; 0 = raw human timing
	TPQ        int    // ticks-per-quarter for the MIDI (default 480, compose's grid)
	Segment    SegmentOptions
	Suggest    SuggestOptions
	MaxLanePts int
}

// DefaultOptions returns the SPEC defaults (raw timing, basic-pitch engine label).
func DefaultOptions() Options {
	return Options{
		Engine:     "basic-pitch",
		TPQ:        480,
		Segment:    DefaultSegmentOptions(),
		Suggest:    DefaultSuggestOptions(),
		MaxLanePts: 600,
	}
}

// Analyze runs the full deterministic pipeline on already-extracted Features and
// returns the Result. A skipped/empty extraction degrades to a partial result
// (whatever the floor produced) rather than crashing — degrade-never-crash.
func Analyze(f Features, opt Options) Result {
	res := Result{
		Tool:          "becky-hum",
		SchemaVersion: SchemaVersion,
		Engine:        chooseEngine(opt.Engine, f.Engine),
		Deterministic: true,
		Input: InputInfo{
			Wav:             opt.Wav,
			DurationSec:     round2(f.DurationSec),
			NormalizeGainDb: round2(f.NormalizeGainDb),
		},
	}
	if f.Skipped {
		res.Degraded = true
		res.Reason = orDefault(f.Reason, "feature extraction skipped")
	}

	seg := opt.Segment
	seg.QuantDiv = opt.QuantDiv
	notes := SegmentNotes(f.Frames, f.Notes, seg)

	res.Key = resolveKey(opt.KeyHint, notes)
	res.Tempo = EstimateTempo(onsetsOf(f, notes), tempoOpts(opt.Genre))

	if opt.QuantDiv > 0 && res.Tempo.BPM > 0 { // re-quantize now that tempo is known
		seg.BPM = res.Tempo.BPM
		notes = SegmentNotes(f.Frames, f.Notes, seg)
		res.Key = resolveKey(opt.KeyHint, notes)
	}

	chord := tonicTriad(res.Key.Compose)
	res.Corrections = Suggest(notes, res.Key.Compose, chord, opt.Suggest)
	res.Notes = notes
	res.Lane = BuildLane(f.Frames, notes, opt.MaxLanePts)
	res.Lane.Peaks = f.Peaks
	res.Compose = composeCommand(opt.Genre, res.Key.Compose, res.Tempo.BPM)
	return res
}

// MelodySMF renders the transcribed notes to a Standard MIDI File using the
// existing music.File writer (SPEC: "no new MIDI writer"). Onsets/durations in
// seconds are converted to ticks at the result tempo + TPQ. Channel 0, one track.
func MelodySMF(notes []Note, bpm, tpq int) *music.File {
	if tpq <= 0 {
		tpq = 480
	}
	if bpm <= 0 {
		bpm = 120
	}
	f := music.NewFile(tpq)
	tr := f.AddTrack()
	tr.Name(0, "becky-hum melody")
	tr.Tempo(0, bpm)
	tr.TimeSig(0, 4, 4)
	tr.Program(0, 0, 0)
	ticksPerSec := float64(bpm) / 60.0 * float64(tpq)
	for _, n := range notes {
		start := int(n.OnsetSec * ticksPerSec)
		dur := int(n.DurSec * ticksPerSec)
		if dur < 1 {
			dur = tpq / 8 // a 1/32-note floor so a note is always audible
		}
		tr.Note(start, dur, 0, n.Midi, velocityFor(n.Confidence))
	}
	return f
}

// CorrectedSMF renders the SUGGESTED pitches (where present) — the opt-in
// melody.corrected.mid (SPEC §3 stage 5). The raw MelodySMF is always kept too;
// becky never silently overwrites.
func CorrectedSMF(notes []Note, bpm, tpq int) *music.File {
	out := make([]Note, len(notes))
	copy(out, notes)
	for i := range out {
		if out[i].Suggestion != nil {
			out[i].Midi = out[i].Suggestion.Midi
		}
	}
	return MelodySMF(out, bpm, tpq)
}

// resolveKey uses the hint when supplied (still reporting it as the detected key),
// else runs K-S over the duration-weighted PCP.
func resolveKey(hint string, notes []Note) KeyResult {
	if hint != "" {
		rootPC, scale := music.ParseKey(hint)
		return KeyResult{
			Root: pcNames[rootPC%12], Scale: scale, Compose: hint,
			Method: "key-hint", Confidence: 1,
		}
	}
	return DetectKey(PitchClassProfile(notes))
}

// onsetsOf prefers the model-provided onset times; if absent, it derives onsets
// from the segmented notes so tempo estimation always has something to work with.
func onsetsOf(f Features, notes []Note) []float64 {
	if len(f.Onsets) > 0 {
		return f.Onsets
	}
	out := make([]float64, len(notes))
	for i, n := range notes {
		out[i] = n.OnsetSec
	}
	return out
}

// tonicTriad returns the MIDI notes of the key's tonic triad as default chord
// context for the suggestion engine (reusing music.Triad).
func tonicTriad(composeKey string) []int {
	rootPC, scale := music.ParseKey(composeKey)
	iv := music.ScaleIntervals(scale)
	return music.Triad(rootPC, iv, 0, 4, false)
}

// tempoOpts maps a genre id to its BPM window for octave resolution. Unknown/empty
// genre => no window (the floor falls back to nearest-120).
func tempoOpts(genre string) TempoOptions {
	if genre == "" {
		return TempoOptions{}
	}
	if _, err := music.ResolveProfile(genre); err != nil {
		return TempoOptions{}
	}
	// Conservative shared window for the fast hyperpop/crunkcore family; the local
	// agent can widen per-profile. Keeps becky from reporting an octave error.
	return TempoOptions{GenreLo: 120, GenreHi: 200}
}

func composeCommand(genre, key string, bpm int) string {
	g := genre
	if g == "" {
		g = "crunkcore"
	}
	return fmt.Sprintf("becky-compose --genre %s --key %s --bpm %d --melody melody.mid", g, key, bpm)
}

func chooseEngine(want, got string) string {
	if got != "" {
		return got
	}
	if want != "" {
		return want
	}
	return "pyin"
}

func velocityFor(conf float64) int {
	v := 64 + int(conf*48) // 64..112 by confidence
	if v > 127 {
		v = 127
	}
	if v < 1 {
		v = 1
	}
	return v
}

func orDefault(s, def string) string {
	if s == "" {
		return def
	}
	return s
}
