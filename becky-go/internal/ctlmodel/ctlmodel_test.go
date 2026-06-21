package ctlmodel

import (
	"errors"
	"testing"

	"becky-go/internal/ctledit"
)

// fakeRunner returns a canned stdout/err so tests never exec a real binary.
type fakeRunner struct {
	out string
	err error
}

func (f fakeRunner) run(bin, model, prompt, grammar string) (string, error) {
	return f.out, f.err
}

func TestKeywordProposer(t *testing.T) {
	b := Keyword().Propose("set tempo to 100", testArr())
	if len(b.Edits) != 1 || b.Edits[0].BPM != 100 {
		t.Errorf("keyword proposer = %+v, want one set_tempo 100", b)
	}
}

func TestPickProposer_NoModelGivesKeyword(t *testing.T) {
	t.Setenv(EnvCtlBin, "/nonexistent/llama")
	t.Setenv(EnvCtlModel, "/nonexistent/model.gguf")
	if _, ok := PickProposer().(KeywordProposer); !ok {
		t.Errorf("PickProposer with absent paths: got %T, want KeywordProposer", PickProposer())
	}
}

func TestModelProposer_UsesModelOutput(t *testing.T) {
	mp := ModelProposer{
		fallback: Keyword(),
		exec:     fakeRunner{out: `{"summary":"m","edits":[{"op":"mute","target":"lead","muted":true}]}`},
	}
	b := mp.Propose("turn off the lead", testArr())
	if len(b.Edits) != 1 || b.Edits[0].Op != ctledit.OpMute || b.Edits[0].Target != "lead" {
		t.Errorf("model output = %+v, want one mute/lead edit", b)
	}
}

func TestModelProposer_DegradesOnError(t *testing.T) {
	mp := ModelProposer{
		fallback: Keyword(),
		exec:     fakeRunner{err: errors.New("boom")},
	}
	// Keyword can handle this, so we get the keyword result, not a crash.
	b := mp.Propose("set tempo to 90", testArr())
	if len(b.Edits) != 1 || b.Edits[0].Op != ctledit.OpSetTempo || b.Edits[0].BPM != 90 {
		t.Errorf("degrade-on-error = %+v, want keyword set_tempo 90", b)
	}
}

func TestModelProposer_DegradesOnEmptyEdits(t *testing.T) {
	mp := ModelProposer{
		fallback: Keyword(),
		exec:     fakeRunner{out: `{"summary":"nothing","edits":[]}`},
	}
	b := mp.Propose("mute the bass", testArr())
	if len(b.Edits) != 1 || b.Edits[0].Op != ctledit.OpMute {
		t.Errorf("degrade-on-empty = %+v, want keyword mute edit", b)
	}
}

func TestModelProposer_NilExecIsKeywordOnly(t *testing.T) {
	mp := ModelProposer{fallback: Keyword()} // exec == nil
	b := mp.Propose("solo the drums", testArr())
	if len(b.Edits) != 1 || b.Edits[0].Op != ctledit.OpSolo {
		t.Errorf("nil exec = %+v, want keyword solo edit", b)
	}
}

func TestExecRunner_IsStub(t *testing.T) {
	// The shipped runner is a documented stub: it must error so ModelProposer degrades.
	if _, err := (execRunner{}).run("a", "b", "p", "g"); err == nil {
		t.Error("execRunner.run should return errModelStub until the local agent wires it")
	}
}

func TestModelProposer_EndToEndAppliesViaCtledit(t *testing.T) {
	arr := testArr()
	mp := ModelProposer{
		fallback: Keyword(),
		exec:     fakeRunner{out: `{"summary":"retune","edits":[{"op":"transpose","track":"lead","semitones":5}]}`},
	}
	b := mp.Propose("brighten the lead", arr)
	next, res, err := ctledit.Apply(arr, b, nil)
	if err != nil || res.Applied != 1 || res.Skipped != 0 {
		t.Fatalf("apply model batch: applied=%d skipped=%d err=%v", res.Applied, res.Skipped, err)
	}
	// lead's note pitch 60 should now be 65.
	tr, _ := next.TrackByID("lead")
	if tr.Clips[0].Notes[0].Pitch != 65 {
		t.Errorf("transposed pitch = %d, want 65", tr.Clips[0].Notes[0].Pitch)
	}
}
