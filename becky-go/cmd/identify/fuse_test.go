package main

import (
	"math"
	"testing"
)

// Two modalities agreeing on the same person -> ONE corroborated conclusion whose
// combined confidence exceeds either single signal (the corroboration-is-precision
// rule). This is the central guarantee of the 2026-06-08 philosophy.
func TestFuseCorroboratedVoicePlusFace(t *testing.T) {
	raw := []Identification{
		{Type: "voice", Name: "Shelby", Confidence: 0.80, SpeakerID: "SPEAKER_01"},
		{Type: "face", Name: "Shelby", Confidence: 0.68},
	}
	ids, unids := fuseIdentifications(raw, nil)

	if len(ids) != 1 {
		t.Fatalf("expected 1 fused conclusion, got %d: %+v", len(ids), ids)
	}
	got := ids[0]
	if got.Type != "corroborated" {
		t.Errorf("type = %q, want corroborated", got.Type)
	}
	if got.Name != "Shelby" {
		t.Errorf("name = %q, want Shelby", got.Name)
	}
	if got.Confidence <= 0.80 {
		t.Errorf("combined confidence %.4f should EXCEED the strongest single signal (0.80)", got.Confidence)
	}
	if len(got.CorroboratedBy) != 2 {
		t.Errorf("corroborated_by = %v, want both modalities", got.CorroboratedBy)
	}
	if got.SpeakerID != "SPEAKER_01" {
		t.Errorf("speaker_id = %q, want SPEAKER_01 carried from the voice signal", got.SpeakerID)
	}
	if len(unids) != 0 {
		t.Errorf("no candidates expected, got %+v", unids)
	}
}

// A lone FACE match (no second modality) below the naming rule is NOT named — it is
// demoted to a candidate, never asserted. (A lone 0.50 face is not Shelby.)
func TestFuseLoneFaceDemotedToCandidate(t *testing.T) {
	raw := []Identification{
		{Type: "face", Name: "John Anthony Clancy", Confidence: 0.59},
	}
	ids, unids := fuseIdentifications(raw, nil)

	if len(ids) != 0 {
		t.Fatalf("a lone face match must NOT be a named identification, got %+v", ids)
	}
	if len(unids) != 1 {
		t.Fatalf("expected 1 demoted candidate, got %d", len(unids))
	}
	if unids[0].Candidate != "John Anthony Clancy" {
		t.Errorf("candidate = %q, want John Anthony Clancy", unids[0].Candidate)
	}
	if unids[0].Confidence != 0.59 {
		t.Errorf("candidate confidence = %.4f, want 0.59 (the raw face score)", unids[0].Confidence)
	}
}

// A STRONG lone face match (>= faceSoloFloor) is a confident visual ID and is named
// (clearly typed "face"); a borderline lone face is demoted. This is the "0.94 face
// is not thin, but 0.59 face is" distinction.
func TestFuseStrongSoloFaceStands(t *testing.T) {
	strongRaw := []Identification{{Type: "face", Name: "Shelby", Confidence: 0.94}}
	ids, _ := fuseIdentifications(strongRaw, nil)
	if len(ids) != 1 || ids[0].Type != "face" || ids[0].Name != "Shelby" {
		t.Fatalf("a 0.94 lone face should be named (type face), got %+v", ids)
	}

	borderlineRaw := []Identification{{Type: "face", Name: "Shelby", Confidence: 0.59}}
	ids2, unids2 := fuseIdentifications(borderlineRaw, nil)
	if len(ids2) != 0 {
		t.Fatalf("a 0.59 lone face must NOT be named, got %+v", ids2)
	}
	if len(unids2) != 1 || unids2[0].Candidate != "Shelby" {
		t.Fatalf("a 0.59 lone face should be a candidate, got %+v", unids2)
	}
}

// A STRONG lone voice match stands alone (voice is the reliable modality), still
// carrying its single signal for audit.
func TestFuseStrongSoloVoiceStands(t *testing.T) {
	raw := []Identification{
		{Type: "voice", Name: "Hair Jordan", Confidence: 0.74, SpeakerID: "SPEAKER_00"},
	}
	ids, unids := fuseIdentifications(raw, nil)

	if len(ids) != 1 {
		t.Fatalf("a strong solo voice should be named, got %+v / unids %+v", ids, unids)
	}
	if ids[0].Type != "voice" || ids[0].Name != "Hair Jordan" {
		t.Errorf("got %+v, want a voice identification for Hair Jordan", ids[0])
	}
	if ids[0].Confidence != 0.74 {
		t.Errorf("solo voice confidence = %.4f, want 0.74 (unchanged)", ids[0].Confidence)
	}
}

// A WEAK lone voice match (below voiceSoloFloor) is demoted — even voice does not get
// to name from a borderline single hit.
func TestFuseWeakSoloVoiceDemoted(t *testing.T) {
	raw := []Identification{
		{Type: "voice", Name: "Maybe Someone", Confidence: 0.50, SpeakerID: "SPEAKER_02"},
	}
	ids, unids := fuseIdentifications(raw, nil)
	if len(ids) != 0 {
		t.Fatalf("a weak solo voice must not be named, got %+v", ids)
	}
	if len(unids) != 1 || unids[0].Candidate != "Maybe Someone" {
		t.Fatalf("expected demoted candidate Maybe Someone, got %+v", unids)
	}
}

// A noise-level second modality must NOT promote a lone signal to "corroborated":
// the weak signal is below corroborateMinPerSignal, so only one real modality remains.
func TestFuseNoiseSecondSignalDoesNotCorroborate(t *testing.T) {
	raw := []Identification{
		{Type: "face", Name: "Shelby", Confidence: 0.60},
		{Type: "location", Name: "Shelby", Confidence: 0.10}, // noise — below the per-signal floor
	}
	ids, unids := fuseIdentifications(raw, nil)
	if len(ids) != 0 {
		t.Fatalf("a real face + noise location should NOT corroborate into a named id, got %+v", ids)
	}
	if len(unids) != 1 || unids[0].Candidate != "Shelby" {
		t.Fatalf("expected Shelby demoted to candidate, got %+v", unids)
	}
}

// Existing modality-level unidentified entries pass through fusion untouched, and the
// fused conclusions sort strongest-first.
func TestFusePreservesUnidsAndSortsByConfidence(t *testing.T) {
	raw := []Identification{
		{Type: "voice", Name: "Weak", Confidence: 0.63, SpeakerID: "SPEAKER_09"},
		{Type: "voice", Name: "Strong", Confidence: 0.82, SpeakerID: "SPEAKER_00"},
		{Type: "face", Name: "Strong", Confidence: 0.70},
	}
	preExisting := []Unidentified{{Type: "voice", SpeakerID: "SPEAKER_05", Description: "unidentified speaker"}}
	ids, unids := fuseIdentifications(raw, preExisting)

	if len(ids) != 2 {
		t.Fatalf("expected 2 conclusions (Strong corroborated, Weak solo voice), got %+v", ids)
	}
	if ids[0].Name != "Strong" {
		t.Errorf("strongest conclusion should sort first; got %q first", ids[0].Name)
	}
	// The pre-existing modality unidentified must survive.
	foundPre := false
	for _, u := range unids {
		if u.SpeakerID == "SPEAKER_05" {
			foundPre = true
		}
	}
	if !foundPre {
		t.Errorf("pre-existing unidentified speaker was dropped: %+v", unids)
	}
}

// Noisy-OR combination: two independent 0.7 signals fuse to 1-(0.3*0.3)=0.91.
func TestCombinedConfidenceNoisyOR(t *testing.T) {
	ps := &personSignals{bestVoiceConf: 0.7, bestFaceConf: 0.7}
	got := combinedConfidence(ps)
	want := 1.0 - 0.3*0.3
	if math.Abs(got-want) > 1e-9 {
		t.Errorf("combinedConfidence = %.4f, want %.4f", got, want)
	}
	// A single signal returns itself.
	if c := combinedConfidence(&personSignals{bestVoiceConf: 0.8}); math.Abs(c-0.8) > 1e-9 {
		t.Errorf("single-signal combinedConfidence = %.4f, want 0.8", c)
	}
	// No signals -> 0.
	if c := combinedConfidence(&personSignals{}); c != 0 {
		t.Errorf("no-signal combinedConfidence = %.4f, want 0", c)
	}
}
