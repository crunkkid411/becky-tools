package workflowdef

import (
	"reflect"
	"testing"
)

// fakeRunner records the steps it was asked to run and seeds "speakers" from the
// transcribe step, so the engine's live-facts gating is exercised with NO real tools.
func fakeRunner(speakers float64) (RunStep, *[]string) {
	var ran []string
	run := func(s Step, facts Facts) (string, error) {
		ran = append(ran, s.Name())
		if s.Tool == "becky-transcribe" {
			facts["speakers"] = speakers // transcribe establishes the speaker count
		}
		return "ok:" + s.Name(), nil
	}
	return run, &ran
}

// MUST-PASS: a 1-speaker fixture => the executed-step list does NOT include diarize.
func TestProcessVideo_OneSpeaker_SkipsDiarize(t *testing.T) {
	r, err := ProcessVideo()
	if err != nil {
		t.Fatalf("ProcessVideo: %v", err)
	}
	run, ran := fakeRunner(1)
	results := r.Run(Facts{"speakers": 1}, run)
	exec := ExecutedNames(results)

	for _, name := range exec {
		if name == "becky-diarize" {
			t.Fatalf("1-speaker run MUST NOT execute diarize; executed=%v", exec)
		}
		if name == "verify-with-gemma4" {
			t.Fatalf("1-speaker run MUST NOT run the gemma4 check; executed=%v", exec)
		}
	}
	want := []string{"becky-transcribe", "becky-ocr", "transcript"}
	if !reflect.DeepEqual(exec, want) {
		t.Errorf("1-speaker executed = %v, want %v", exec, want)
	}
	if !reflect.DeepEqual(*ran, want) {
		t.Errorf("1-speaker RunStep saw %v, want %v", *ran, want)
	}
}

// MUST-PASS: a 3-speaker fixture => the executed-step list DOES include diarize.
func TestProcessVideo_ThreeSpeakers_RunsDiarize(t *testing.T) {
	r, err := ProcessVideo()
	if err != nil {
		t.Fatalf("ProcessVideo: %v", err)
	}
	run, ran := fakeRunner(3)
	results := r.Run(Facts{"speakers": 3}, run)
	exec := ExecutedNames(results)

	want := []string{"becky-transcribe", "becky-diarize", "becky-ocr", "verify-with-gemma4", "transcript"}
	if !reflect.DeepEqual(exec, want) {
		t.Errorf("3-speaker executed = %v, want %v", exec, want)
	}
	if !reflect.DeepEqual(*ran, want) {
		t.Errorf("3-speaker RunStep saw %v, want %v", *ran, want)
	}
}

// The skip is driven LIVE by what the transcribe step establishes: start with no
// speaker fact, let the fake transcribe set it to 1, and diarize must still be skipped.
func TestRun_LiveFactsGateDiarize(t *testing.T) {
	r, _ := ProcessVideo()
	run, _ := fakeRunner(1) // transcribe will set speakers=1
	results := r.Run(Facts{}, run)
	for _, res := range results {
		if res.Step.Tool == "becky-diarize" && !res.Skipped {
			t.Fatal("diarize should be skipped once transcribe establishes 1 speaker")
		}
	}
}

func TestEvalWhen(t *testing.T) {
	cases := []struct {
		when string
		f    Facts
		want bool
	}{
		{"speakers > 1", Facts{"speakers": 3}, true},
		{"speakers > 1", Facts{"speakers": 1}, false},
		{"speakers > 1", Facts{}, false}, // missing fact => 0
		{"speakers >= 2", Facts{"speakers": 2}, true},
		{"speakers <= 1", Facts{"speakers": 1}, true},
		{"speakers == 0", Facts{}, true},
		{"speakers != 0", Facts{"speakers": 2}, true},
		{"", Facts{}, true}, // empty => always
	}
	for _, c := range cases {
		if got := EvalWhen(c.when, c.f); got != c.want {
			t.Errorf("EvalWhen(%q, %v) = %v, want %v", c.when, c.f, got, c.want)
		}
	}
}

func TestParse_RejectsBadStep(t *testing.T) {
	_, err := Parse([]byte(`{"name":"x","steps":[{"tool":"a","verb":"b"}]}`))
	if err == nil {
		t.Error("a step with both tool and verb must be rejected")
	}
	_, err = Parse([]byte(`{"name":"x","steps":[{}]}`))
	if err == nil {
		t.Error("a step with none of tool/verb/merge must be rejected")
	}
	_, err = Parse([]byte(`{"name":"","steps":[{"tool":"a"}]}`))
	if err == nil {
		t.Error("an empty recipe name must be rejected")
	}
	_, err = Parse([]byte(`{"name":"x","steps":[{"tool":"a","when":"speakers oops"}]}`))
	if err == nil {
		t.Error("an unparseable condition must be rejected at validation")
	}
}

func TestMatches(t *testing.T) {
	r, _ := ProcessVideo()
	if !r.Matches("ok becky, process this video please") {
		t.Error("should match the 'process this video' phrase")
	}
	if r.Matches("what's the weather") {
		t.Error("should not match an unrelated utterance")
	}
}

func TestProcessVideo_ShapeMatchesSpec(t *testing.T) {
	r, err := ProcessVideo()
	if err != nil {
		t.Fatalf("ProcessVideo: %v", err)
	}
	if r.Name != "process-video" {
		t.Errorf("name = %q", r.Name)
	}
	if len(r.Steps) != 5 {
		t.Fatalf("expected 5 steps, got %d", len(r.Steps))
	}
	// diarize + gemma4 must carry the speakers>1 gate; the others must be unconditional.
	for _, s := range r.Steps {
		switch s.Name() {
		case "becky-diarize", "verify-with-gemma4":
			if s.When != "speakers > 1" {
				t.Errorf("%s should be gated on 'speakers > 1', got %q", s.Name(), s.When)
			}
		default:
			if s.When != "" {
				t.Errorf("%s should be unconditional, got when=%q", s.Name(), s.When)
			}
		}
	}
}
