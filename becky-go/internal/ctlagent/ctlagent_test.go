package ctlagent

import (
	"context"
	"testing"

	"becky-go/internal/editmodel"
	"becky-go/internal/edittools"
)

// scriptModel returns a fixed sequence of replies, then defaults to "done". It
// makes the loop deterministic so we assert real state transitions, not a live LLM.
type scriptModel struct {
	replies []string
	i       int
}

func (m *scriptModel) Complete(_ context.Context, _, _ string) (string, error) {
	if m.i >= len(m.replies) {
		return `{"done":true,"message":"end of script"}`, nil
	}
	r := m.replies[m.i]
	m.i++
	return r, nil
}

// oneClip builds a project with a single 20s clip c1 on the video track.
func oneClip() *editmodel.Project {
	p := editmodel.New("case", 30)
	vt := p.TrackByIndex(0)
	vt.Clips = []editmodel.Clip{{ID: "c1", Source: `E:\c\a.mp4`, In: 0, Out: 20, Pos: 0}}
	p.ClipSeq = 1
	return p
}

func TestLoopAppliesMultiStepEdit(t *testing.T) {
	start := oneClip()
	m := &scriptModel{replies: []string{
		`{"tool":"trim_clip","args":{"id":"c1","out":8},"thought":"shorten"}`,
		`{"tool":"add_fade","args":{"id":"c1","kind":"out","seconds":2}}`,
		`{"done":true,"message":"Trimmed c1 to 8s and added a fade out."}`,
	}}
	run, err := Run(context.Background(), m, start, "trim c1 to 8s and fade it out", Options{})
	if err != nil {
		t.Fatalf("Run errored: %v", err)
	}
	if !run.Done {
		t.Fatalf("loop should have finished (Done); aborted=%q", run.Aborted)
	}
	if len(run.Applied) != 2 {
		t.Fatalf("want 2 applied calls, got %d", len(run.Applied))
	}
	if len(run.Host) != 2 {
		t.Fatalf("want 2 host commands, got %d", len(run.Host))
	}
	_, _, c1, _ := run.Final.FindClip("c1")
	if c1.Dur() != 8 {
		t.Fatalf("final c1 should be trimmed to 8s, got %g", c1.Dur())
	}
	if len(c1.Effects) != 1 || c1.Effects[0].Name != "fadeOut" {
		t.Fatalf("final c1 should have a fadeOut effect, got %+v", c1.Effects)
	}
	// Propose-preview-apply: the ORIGINAL project must be untouched.
	_, _, orig, _ := start.FindClip("c1")
	if orig.Dur() != 20 || len(orig.Effects) != 0 || start.Rev != 0 {
		t.Fatalf("Run must not mutate the caller's project; got dur=%g fx=%d rev=%d", orig.Dur(), len(orig.Effects), start.Rev)
	}
}

func TestLoopSelfRepairsUnparseableReply(t *testing.T) {
	m := &scriptModel{replies: []string{
		"Sure, let me trim that clip for you.", // no JSON — must be fed back
		`{"tool":"trim_clip","args":{"id":"c1","out":8}}`,
		`{"done":true,"message":"ok"}`,
	}}
	run, err := Run(context.Background(), m, oneClip(), "trim c1", Options{})
	if err != nil {
		t.Fatalf("Run errored: %v", err)
	}
	if !run.Done || len(run.Applied) != 1 {
		t.Fatalf("loop should recover and apply 1 edit; done=%v applied=%d", run.Done, len(run.Applied))
	}
	_, _, c1, _ := run.Final.FindClip("c1")
	if c1.Dur() != 8 {
		t.Fatalf("recovered edit should trim c1 to 8s, got %g", c1.Dur())
	}
	if !hasErrorStep(run) {
		t.Fatalf("the unparseable reply should be recorded as an error step")
	}
}

func TestLoopSelfRepairsToolFailure(t *testing.T) {
	m := &scriptModel{replies: []string{
		`{"tool":"trim_clip","args":{"id":"cX","out":8}}`, // missing clip — tool fails
		`{"tool":"trim_clip","args":{"id":"c1","out":8}}`,
		`{"done":true}`,
	}}
	run, _ := Run(context.Background(), m, oneClip(), "trim c1", Options{})
	if !run.Done || len(run.Applied) != 1 {
		t.Fatalf("loop should recover after a tool failure; done=%v applied=%d", run.Done, len(run.Applied))
	}
	_, _, c1, _ := run.Final.FindClip("c1")
	if c1.Dur() != 8 {
		t.Fatalf("recovered edit should trim c1, got %g", c1.Dur())
	}
}

func TestLoopAbortsOnRepeatedGarbage(t *testing.T) {
	m := &scriptModel{replies: []string{"nope", "still nope", "garbage", "more garbage", "even more"}}
	run, _ := Run(context.Background(), m, oneClip(), "do something", Options{MaxRepairs: 2})
	if run.Done {
		t.Fatalf("loop should NOT report done on endless garbage")
	}
	if run.Aborted == "" {
		t.Fatalf("loop should record why it aborted")
	}
	if len(run.Applied) != 0 {
		t.Fatalf("no edits should have been applied, got %d", len(run.Applied))
	}
}

func TestLoopRespectsStepCap(t *testing.T) {
	m := &scriptModel{replies: []string{
		`{"tool":"set_playhead","args":{"at":1}}`,
		`{"tool":"set_playhead","args":{"at":2}}`,
		`{"tool":"set_playhead","args":{"at":3}}`,
		`{"tool":"set_playhead","args":{"at":4}}`,
	}}
	run, _ := Run(context.Background(), m, oneClip(), "keep moving", Options{MaxSteps: 3})
	if run.Done {
		t.Fatalf("model never said done; run.Done must be false")
	}
	if len(run.Applied) != 3 {
		t.Fatalf("step cap should bound applied calls to 3, got %d", len(run.Applied))
	}
}

func TestEnricherRunsForReadVerbsOnly(t *testing.T) {
	m := &scriptModel{replies: []string{
		`{"tool":"search","args":{"query":"penguin"}}`,
		`{"tool":"trim_clip","args":{"id":"c1","out":8}}`,
		`{"done":true}`,
	}}
	var enrichedVerbs []string
	enrich := func(_ context.Context, call edittools.ToolCall, res edittools.Result, _ []edittools.HostCommand) edittools.Result {
		enrichedVerbs = append(enrichedVerbs, string(call.Verb))
		res.Data = map[string]any{"answer": "1 hit"}
		return res
	}
	run, _ := Run(context.Background(), m, oneClip(), "search then trim", Options{Enrich: enrich})
	if !run.Done {
		t.Fatalf("loop should finish")
	}
	if len(enrichedVerbs) != 1 || enrichedVerbs[0] != "search" {
		t.Fatalf("enricher must run for the read verb 'search' only, got %v", enrichedVerbs)
	}
}

func hasErrorStep(r Result) bool {
	for _, s := range r.Steps {
		if s.Error != "" {
			return true
		}
	}
	return false
}
