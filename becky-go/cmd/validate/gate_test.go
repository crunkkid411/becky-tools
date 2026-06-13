package main

import (
	"testing"

	"becky-go/internal/avlm"
)

func caps() []avlm.FrameCaption {
	return []avlm.FrameCaption{
		{Timestamp: 5, Text: "man behind woman", Frame: `C:\tmp\frame_0006.jpg`},
		{Timestamp: 6, Text: "hand on hip", Frame: `C:\tmp\frame_0007.jpg`},
	}
}

// A contact observation that cites a real frame keeps its type and gets the full
// path resolved.
func TestGateContactWithCitedFrameKept(t *testing.T) {
	obs := []Observation{{
		Type:         "physical_contact",
		SegmentStart: 6, SegmentEnd: 6,
		Frames: []string{"frame_0007.jpg"},
	}}
	out := gateContactFrames(obs, caps())
	if out[0].Type != "physical_contact" {
		t.Fatalf("contact with a cited frame must stay physical_contact, got %q", out[0].Type)
	}
	if len(out[0].Frames) != 1 || out[0].Frames[0] != `C:\tmp\frame_0007.jpg` {
		t.Fatalf("cited frame must resolve to full path, got %v", out[0].Frames)
	}
}

// A contact observation with NO cited frame but a matching timestamp window is
// kept via the timestamp fallback (a real contact the model forgot to cite).
func TestGateContactTimestampFallback(t *testing.T) {
	obs := []Observation{{
		Type:         "possible_contact",
		SegmentStart: 5, SegmentEnd: 6,
		Frames: nil,
	}}
	out := gateContactFrames(obs, caps())
	if out[0].Type != "possible_contact" {
		t.Fatalf("contact with a timestamp-resolvable frame must keep its type, got %q", out[0].Type)
	}
	if len(out[0].Frames) == 0 {
		t.Fatal("timestamp fallback should link at least one frame")
	}
}

// A contact observation with no resolvable frame at all is DOWNGRADED to visual,
// never emitted as an unverifiable contact claim.
func TestGateContactUnlinkedDowngraded(t *testing.T) {
	obs := []Observation{{
		Type:         "physical_contact",
		SegmentStart: 99, SegmentEnd: 99, // outside any caption window
		Frames:       []string{"does_not_exist.jpg"},
		Significance: "high",
		Rationale:    "model said so",
	}}
	out := gateContactFrames(obs, caps())
	if out[0].Type != "visual" {
		t.Fatalf("unlinkable contact must downgrade to visual, got %q", out[0].Type)
	}
	if len(out[0].Frames) != 0 {
		t.Fatalf("downgraded observation must have no frames, got %v", out[0].Frames)
	}
	if out[0].Significance != "low" {
		t.Errorf("downgraded observation significance should be low, got %q", out[0].Significance)
	}
}

// Non-contact observations pass through untouched (best-effort frame resolve).
func TestGateNonContactPassthrough(t *testing.T) {
	obs := []Observation{{Type: "visual", Finding: "a woman petting a dog"}}
	out := gateContactFrames(obs, caps())
	if out[0].Type != "visual" || out[0].Finding != "a woman petting a dog" {
		t.Fatalf("non-contact observation altered: %+v", out[0])
	}
}

// On near-silence (low % AND few seconds), audio_tone is blanked to a no-speech
// marker and the mismatch flag is cleared.
func TestSuppressToneOnSilence(t *testing.T) {
	f := false
	obs := []Observation{{AudioTone: "subdued and deliberate", ToneContentMatch: &f}}
	// ~0.6s of speech in an 8s window (the dog-clip regime): 7.5%, 0.6s.
	out := suppressToneOnSilence(obs, speechStat{Pct: 7.5, Seconds: 0.6, Known: true})
	if out[0].AudioTone == "subdued and deliberate" {
		t.Fatal("audio_tone must be suppressed on near-silence")
	}
	if out[0].ToneContentMatch == nil || !*out[0].ToneContentMatch {
		t.Fatal("tone-vs-content mismatch must clear on silence")
	}
}

// A tiny utterance with a high percentage but too few seconds is still
// suppressed (the absolute-seconds floor).
func TestSuppressToneFewSecondsHighPct(t *testing.T) {
	obs := []Observation{{AudioTone: "tense"}}
	// A 2s clip that is 40% speech is still only 0.8s — too little to judge tone.
	out := suppressToneOnSilence(obs, speechStat{Pct: 40, Seconds: 0.8, Known: true})
	if out[0].AudioTone == "tense" {
		t.Fatal("tone must be suppressed when absolute speech-seconds are too few")
	}
}

// With real speech (enough % AND seconds), or unknown VAD, the tone is left
// untouched — no regression.
func TestSuppressToneKeepsRealSpeech(t *testing.T) {
	obs := []Observation{{AudioTone: "calm and even"}}
	if out := suppressToneOnSilence(obs, speechStat{Pct: 40, Seconds: 12, Known: true}); out[0].AudioTone != "calm and even" {
		t.Fatalf("tone must survive on a clip with speech, got %q", out[0].AudioTone)
	}
	if out := suppressToneOnSilence(obs, unknownSpeech); out[0].AudioTone != "calm and even" {
		t.Fatalf("tone must survive when VAD is unknown, got %q", out[0].AudioTone)
	}
}
