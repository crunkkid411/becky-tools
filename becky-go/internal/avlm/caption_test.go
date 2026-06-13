package avlm

import (
	"strings"
	"testing"
)

// TestBuildSynthUserIncludesCaptionsAndAudio verifies the Stage-2 user message
// embeds the per-frame captions (with their timestamps) and the audio tone.
func TestBuildSynthUserIncludesCaptionsAndAudio(t *testing.T) {
	caps := []FrameCaption{
		{Timestamp: 5.0, Text: "Two people; male behind female."},
		{Timestamp: 6.0, Text: "Male hand near female right hip."},
	}
	out := buildSynthUser("CONSOLIDATE-MARKER", caps, "calm and upbeat")
	for _, want := range []string{
		"CONSOLIDATE-MARKER",
		"[5.0s]", "male behind female",
		"[6.0s]", "near female right hip",
		"AUDIO TONE", "calm and upbeat",
		"JSON array",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("synth user message missing %q\n---\n%s", want, out)
		}
	}
}

// TestBuildSynthUserOmitsAudioWhenEmpty confirms the audio block is skipped when
// no tone was produced (audio analysis degraded or disabled).
func TestBuildSynthUserOmitsAudioWhenEmpty(t *testing.T) {
	out := buildSynthUser("p", []FrameCaption{{Timestamp: 1, Text: "x"}}, "")
	if strings.Contains(out, "AUDIO TONE") {
		t.Errorf("audio block should be omitted when tone is empty:\n%s", out)
	}
}

// TestBuildSynthUserLabelsFrameFile confirms the per-frame block tags each
// caption with its frame file name so the synthesis can cite it for a contact
// observation (and the gate can resolve it to a real path).
func TestBuildSynthUserLabelsFrameFile(t *testing.T) {
	caps := []FrameCaption{{Timestamp: 6, Text: "hand on hip", Frame: `C:\tmp\frame_0007.jpg`}}
	out := buildSynthUser("p", caps, "")
	if !strings.Contains(out, "frame file: frame_0007.jpg") {
		t.Errorf("synth user message must label the frame file:\n%s", out)
	}
}

// TestTwoStageDefaults checks the defaults fill sane values for unset options.
func TestTwoStageDefaults(t *testing.T) {
	o := TwoStageOptions{}
	twoStageDefaults(&o)
	if o.FPS != 1.0 {
		t.Errorf("FPS default = %v, want 1.0", o.FPS)
	}
	if o.WindowSec != 30 {
		t.Errorf("WindowSec default = %v, want 30", o.WindowSec)
	}
	if o.CaptionMaxTokens != 320 {
		t.Errorf("CaptionMaxTokens default = %d, want 320", o.CaptionMaxTokens)
	}
	if o.SynthMaxTokens != 8192 {
		t.Errorf("SynthMaxTokens default = %d, want 8192", o.SynthMaxTokens)
	}
	if o.Temperature != 0.2 || o.Seed != 42 {
		t.Errorf("temp/seed defaults = %v/%d, want 0.2/42", o.Temperature, o.Seed)
	}
}

// TestTwoStageDefaultsPreservesSet confirms explicitly-set values are not
// overwritten by defaults.
func TestTwoStageDefaultsPreservesSet(t *testing.T) {
	o := TwoStageOptions{FPS: 2, WindowSec: 10, CaptionMaxTokens: 100, SynthMaxTokens: 999, Temperature: 0.7, Seed: 7}
	twoStageDefaults(&o)
	if o.FPS != 2 || o.WindowSec != 10 || o.CaptionMaxTokens != 100 || o.SynthMaxTokens != 999 || o.Temperature != 0.7 || o.Seed != 7 {
		t.Errorf("defaults overwrote set values: %+v", o)
	}
}
