package hum

import (
	"bytes"
	"testing"
)

// melodyFeatures builds a synthetic C-major arpeggio (C E G C) as native stub
// notes plus matching onsets at 120 BPM, the simplest end-to-end fixture.
func melodyFeatures() Features {
	return Features{
		Engine:      "fixture",
		DurationSec: 2.0,
		Onsets:      []float64{0.0, 0.5, 1.0, 1.5},
		Notes: []StubNote{
			{Onset: 0.0, Dur: 0.5, Midi: 60, Confidence: 0.95}, // C
			{Onset: 0.5, Dur: 0.5, Midi: 64, Confidence: 0.92}, // E
			{Onset: 1.0, Dur: 0.5, Midi: 67, Confidence: 0.90}, // G
			{Onset: 1.5, Dur: 0.5, Midi: 72, Confidence: 0.88}, // C
		},
	}
}

func TestAnalyze_EndToEndCMajor(t *testing.T) {
	res := Analyze(melodyFeatures(), DefaultOptions())
	if res.Tool != "becky-hum" || res.SchemaVersion != SchemaVersion {
		t.Fatalf("envelope wrong: %s v%d", res.Tool, res.SchemaVersion)
	}
	if res.Key.Compose != "C" {
		t.Errorf("key = %q, want C", res.Key.Compose)
	}
	if res.Tempo.BPM != 120 {
		t.Errorf("tempo = %d, want 120", res.Tempo.BPM)
	}
	if len(res.Notes) != 4 {
		t.Errorf("notes = %d, want 4", len(res.Notes))
	}
	if res.Compose == "" {
		t.Error("compose command should be populated (closes the loop to becky-compose)")
	}
	if len(res.Lane.Notes) != 4 {
		t.Error("lane must mirror the notes (visual-first substrate)")
	}
}

func TestAnalyze_SkippedFeaturesDegrade(t *testing.T) {
	res := Analyze(Features{Skipped: true, Reason: "no model"}, DefaultOptions())
	if !res.Degraded || res.Reason == "" {
		t.Errorf("skipped features should degrade with a reason, got %+v", res)
	}
	if len(res.Notes) != 0 {
		t.Errorf("no notes expected on a skipped extraction, got %d", len(res.Notes))
	}
}

func TestAnalyze_KeyHintHonored(t *testing.T) {
	opt := DefaultOptions()
	opt.KeyHint = "Am"
	res := Analyze(melodyFeatures(), opt)
	if res.Key.Compose != "Am" || res.Key.Method != "key-hint" {
		t.Errorf("key hint not honored: %+v", res.Key)
	}
}

func TestAnalyze_Deterministic(t *testing.T) {
	a := Analyze(melodyFeatures(), DefaultOptions())
	b := Analyze(melodyFeatures(), DefaultOptions())
	if a.Key != b.Key || a.Tempo.BPM != b.Tempo.BPM || len(a.Notes) != len(b.Notes) {
		t.Fatal("Analyze not deterministic across runs")
	}
}

func TestMelodySMF_DeterministicBytes(t *testing.T) {
	res := Analyze(melodyFeatures(), DefaultOptions())
	a := MelodySMF(res.Notes, res.Tempo.BPM, 480).Bytes()
	b := MelodySMF(res.Notes, res.Tempo.BPM, 480).Bytes()
	if !bytes.Equal(a, b) {
		t.Error("MIDI bytes not deterministic for identical notes")
	}
	if !bytes.HasPrefix(a, []byte("MThd")) {
		t.Error("output is not a Standard MIDI File (missing MThd header)")
	}
}

func TestCorrectedSMF_AppliesSuggestions(t *testing.T) {
	// An off-key note should produce a corrected MIDI that differs from the raw one.
	feats := Features{
		Engine: "fixture", DurationSec: 1.0, Onsets: []float64{0.0, 0.5},
		Notes: []StubNote{
			{Onset: 0.0, Dur: 0.5, Midi: 60, Confidence: 0.9}, // C (in C major)
			{Onset: 0.5, Dur: 0.5, Midi: 61, Confidence: 0.6}, // C# (off-key)
		},
	}
	opt := DefaultOptions()
	opt.KeyHint = "C"
	res := Analyze(feats, opt)
	raw := MelodySMF(res.Notes, res.Tempo.BPM, 480).Bytes()
	corrected := CorrectedSMF(res.Notes, res.Tempo.BPM, 480).Bytes()
	if bytes.Equal(raw, corrected) {
		t.Error("corrected MIDI should differ from raw when an off-key note was suggested")
	}
}

func TestAnalyze_FixtureExtractorRoundTrips(t *testing.T) {
	// The cloud-testable Extractor returns canned Features the pipeline consumes.
	ex := FixtureExtractor{Feats: melodyFeatures()}
	feats, err := ex.Extract("hum.wav", "basic-pitch", "cpu")
	if err != nil {
		t.Fatalf("fixture extract: %v", err)
	}
	res := Analyze(feats, DefaultOptions())
	if res.Key.Compose != "C" {
		t.Errorf("round-trip key = %q, want C", res.Key.Compose)
	}
}
