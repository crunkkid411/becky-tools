package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"becky-go/internal/config"
	"becky-go/internal/seam"
)

// newTestBridge builds a bridge over a synthetic case folder + a scripted model.
// It returns the bridge, the fixture video path, and the folder path.
func newTestBridge(t *testing.T) (b *Bridge, video, dir string) {
	t.Helper()
	dir = t.TempDir()
	video = filepath.Join(dir, "bounty.mp4")
	if err := os.WriteFile(video, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	srt := "1\n00:00:18,000 --> 00:00:24,000\nevery penguin in the building\n"
	if err := os.WriteFile(filepath.Join(dir, "bounty.srt"), []byte(srt), 0o644); err != nil {
		t.Fatal(err)
	}
	model := &selftestModel{replies: []string{
		`{"tool":"add_fade","args":{"id":"c1","kind":"out"}}`,
		`{"done":true,"message":"done"}`,
	}}
	return NewBridge(config.Load(), model, "", nil), video, dir
}

func call(t *testing.T, b *Bridge, name string, args any) seam.ResponseMsg {
	t.Helper()
	raw, _ := json.Marshal(args)
	return b.Dispatch(context.Background(), seam.CommandMsg{Type: seam.TypeCommand, ID: "1", Name: name, Args: raw})
}

func dataOf(t *testing.T, resp seam.ResponseMsg) map[string]any {
	t.Helper()
	if !resp.OK {
		t.Fatalf("command failed: %s", resp.Error)
	}
	var d map[string]any
	_ = json.Unmarshal(resp.Data, &d)
	return d
}

func TestUnknownCommandFails(t *testing.T) {
	b, _, _ := newTestBridge(t)
	if resp := call(t, b, "explode", nil); resp.OK {
		t.Fatalf("unknown command should fail")
	}
}

func TestOpenFolderIndexesTranscripts(t *testing.T) {
	b, _, dir := newTestBridge(t)
	d := dataOf(t, call(t, b, "open_folder", map[string]any{"path": dir}))
	if d["videos"].(float64) != 1 || d["with_transcripts"].(float64) != 1 {
		t.Fatalf("open_folder: got %v", d)
	}
}

func TestDoAddClipCommitsState(t *testing.T) {
	b, video, _ := newTestBridge(t)
	d := dataOf(t, call(t, b, "do", map[string]any{"verb": "add_clip", "args": map[string]any{"source": video, "in": 18.0, "out": 24.0}}))
	if d["rev"].(float64) != 1 {
		t.Fatalf("add_clip should commit (rev 1), got %v", d["rev"])
	}
	// A failed verb must NOT bump the committed rev.
	d2 := dataOf(t, call(t, b, "do", map[string]any{"verb": "remove_clip", "args": map[string]any{"id": "ghost"}}))
	if d2["rev"].(float64) != 1 {
		t.Fatalf("a failed do must leave rev at 1, got %v", d2["rev"])
	}
}

func TestEventMirrorsHumanEdit(t *testing.T) {
	b, _, _ := newTestBridge(t)
	d := dataOf(t, call(t, b, "event", map[string]any{"name": "playhead", "args": map[string]any{"seconds": 7.0}}))
	if dg, _ := d["digest"].(string); !contains(dg, "playhead=7.0s") {
		t.Fatalf("event(playhead) should update the shared digest, got %v", d["digest"])
	}
	// An unknown event is rejected.
	if resp := call(t, b, "event", map[string]any{"name": "telepathy"}); resp.OK {
		t.Fatalf("unknown event should fail")
	}
}

func TestAgentProposeApproveCommits(t *testing.T) {
	b, video, _ := newTestBridge(t)
	call(t, b, "do", map[string]any{"verb": "add_clip", "args": map[string]any{"source": video, "in": 0.0, "out": 10.0}})
	ag := dataOf(t, call(t, b, "agent", map[string]any{"goal": "fade out c1"}))
	applied, _ := ag["applied"].([]any)
	if len(applied) != 1 {
		t.Fatalf("agent should propose 1 edit, got %d", len(applied))
	}
	id, _ := ag["proposal_id"].(string)
	// Proposal must NOT have committed yet: the live state still has no fadeOut.
	st := dataOf(t, call(t, b, "state", nil))
	if dg, _ := st["digest"].(string); contains(dg, "fadeOut") {
		t.Fatalf("agent must not commit before approve")
	}
	// Approve commits it.
	dataOf(t, call(t, b, "approve", map[string]any{"id": id}))
	st2 := dataOf(t, call(t, b, "state", nil))
	if dg, _ := st2["digest"].(string); !contains(dg, "fadeOut") {
		t.Fatalf("after approve the committed state should have the fade, got %v", st2["digest"])
	}
}

func TestRejectDiscardsProposal(t *testing.T) {
	b, video, _ := newTestBridge(t)
	call(t, b, "do", map[string]any{"verb": "add_clip", "args": map[string]any{"source": video, "in": 0.0, "out": 10.0}})
	ag := dataOf(t, call(t, b, "agent", map[string]any{"goal": "fade out c1"}))
	id, _ := ag["proposal_id"].(string)
	dataOf(t, call(t, b, "reject", map[string]any{"id": id}))
	// Re-approving a rejected id must fail.
	if resp := call(t, b, "approve", map[string]any{"id": id}); resp.OK {
		t.Fatalf("approving a rejected proposal should fail")
	}
}

func TestAgentDegradesWithoutModel(t *testing.T) {
	b := NewBridge(config.Load(), nil, "agent disabled: no model", nil)
	d := dataOf(t, call(t, b, "agent", map[string]any{"goal": "do something"}))
	if d["done"].(bool) {
		t.Fatalf("agent should not report done without a model")
	}
}
