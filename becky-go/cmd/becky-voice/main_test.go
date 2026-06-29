package main

import (
	"bufio"
	"bytes"
	"context"
	"testing"
)

// newTestRouter returns a Router with stub exec (no real binaries needed).
func newTestRouter() *Router {
	out := bufio.NewWriter(&bytes.Buffer{})
	audit := bufio.NewWriter(&bytes.Buffer{})
	r := NewRouter(out, audit)
	r.UseStubExec()
	return r
}

func TestRouteGreenAutoRuns(t *testing.T) {
	r := newTestRouter()
	e := r.Route(context.Background(), IntentMsg{Type: "intent", Text: "transcribe this video", ID: "t1"})
	if e.Type != "result" {
		t.Errorf("type=%q want result", e.Type)
	}
	if e.Action != "run" {
		t.Errorf("action=%q want run", e.Action)
	}
	if e.Tier != "green" {
		t.Errorf("tier=%q want green", e.Tier)
	}
	if e.Tool != "becky-transcribe" {
		t.Errorf("tool=%q want becky-transcribe", e.Tool)
	}
}

func TestRouteRedNeedsConfirm(t *testing.T) {
	r := newTestRouter()
	// Use "export this" — "export" is the keyword for becky-export (TierRed).
	// "findings" must be avoided: it contains "find" which is a TierGreen op.
	e := r.Route(context.Background(), IntentMsg{Type: "intent", Text: "export this", ID: "t1"})
	if e.Type != "need_confirm" {
		t.Errorf("type=%q want need_confirm", e.Type)
	}
	if !e.NeedConfirm {
		t.Errorf("need_confirm=false want true")
	}
	if e.Action != "await_confirm" {
		t.Errorf("action=%q want await_confirm", e.Action)
	}
	// VALUE: pending must be set so confirm can fire it
	if r.pending == nil {
		t.Error("pending is nil after RED intent — confirm would have nothing to run")
	}
}

func TestRouteConfirmExecutesPending(t *testing.T) {
	r := newTestRouter()
	r.Route(context.Background(), IntentMsg{Type: "intent", Text: "export this", ID: "t1"})
	e := r.Route(context.Background(), IntentMsg{Type: "confirm", ID: "t1"})
	if e.Type != "result" {
		t.Errorf("type=%q want result", e.Type)
	}
	if e.Action != "run" {
		t.Errorf("action=%q want run", e.Action)
	}
	// pending must be cleared after confirm
	if r.pending != nil {
		t.Error("pending not cleared after confirm")
	}
}

func TestRouteCancelClearsPending(t *testing.T) {
	r := newTestRouter()
	r.Route(context.Background(), IntentMsg{Type: "intent", Text: "export this", ID: "t1"})
	e := r.Route(context.Background(), IntentMsg{Type: "cancel", ID: "t1"})
	if e.Type != "reply" {
		t.Errorf("type=%q want reply", e.Type)
	}
	if r.pending != nil {
		t.Error("pending not cleared after cancel")
	}
}

func TestRouteSetPackSwitchesPack(t *testing.T) {
	r := newTestRouter()
	e := r.Route(context.Background(), IntentMsg{Type: "set_pack", Pack: "reaper", ID: "t1"})
	if e.Type != "reply" {
		t.Errorf("set_pack: type=%q want reply", e.Type)
	}
	// VALUE: after switching, the active pack name must change
	if r.activePack.Name != "reaper" {
		t.Errorf("activePack.Name=%q want reaper", r.activePack.Name)
	}
}

func TestRouteReaperPackBlocksTranscribe(t *testing.T) {
	r := newTestRouter()
	// switch to reaper pack
	r.Route(context.Background(), IntentMsg{Type: "set_pack", Pack: "reaper"})
	// now try to transcribe — not in reaper pack
	e := r.Route(context.Background(), IntentMsg{Type: "intent", Text: "transcribe this video"})
	if e.Type != "error" {
		t.Errorf("type=%q want error (transcribe not in reaper pack)", e.Type)
	}
}

func TestRouteFixItDeploysFix(t *testing.T) {
	r := newTestRouter()
	// run a green tool first to populate lastTool
	r.Route(context.Background(), IntentMsg{Type: "intent", Text: "transcribe this video", ID: "t1"})
	e := r.Route(context.Background(), IntentMsg{Type: "intent", Text: "fix it", ID: "t2"})
	// VALUE: action must be run and Tool must be the fix-verb
	if e.Action != "run" {
		t.Errorf("fix-it: action=%q want run", e.Action)
	}
	if e.Tool == "" {
		t.Error("fix-it: Tool empty, want fix-verb name")
	}
}

func TestRouteUnknownTextReturnsError(t *testing.T) {
	r := newTestRouter()
	e := r.Route(context.Background(), IntentMsg{Type: "intent", Text: "xyzzy gibberish not a command"})
	if e.Type != "error" {
		t.Errorf("type=%q want error for unknown text", e.Type)
	}
}

func TestRouteClipIDIsUnique(t *testing.T) {
	r := newTestRouter()
	// run the same command twice — clip IDs must differ (counter increments)
	e1 := r.Route(context.Background(), IntentMsg{Type: "intent", Text: "transcribe this video", ID: "t1"})
	e2 := r.Route(context.Background(), IntentMsg{Type: "intent", Text: "transcribe this video", ID: "t2"})
	if e1.Clip == "" || e2.Clip == "" {
		t.Error("Clip ID empty")
	}
	if e1.Clip == e2.Clip {
		// VALUE: same tool, second call must produce a different clip id
		t.Errorf("Clip ids identical %q — counter not incrementing", e1.Clip)
	}
}

func TestRouteIDEchoed(t *testing.T) {
	r := newTestRouter()
	e := r.Route(context.Background(), IntentMsg{Type: "intent", Text: "transcribe this", ID: "my-id-99"})
	if e.ID != "my-id-99" {
		t.Errorf("ID=%q want my-id-99 (id must be echoed)", e.ID)
	}
}
