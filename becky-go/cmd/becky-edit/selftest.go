package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"becky-go/internal/config"
	"becky-go/internal/seam"
)

// runSelftest is the one-command OFFLINE proof (no Shotcut, no GPU, no network)
// that the becky-edit engine actually works end to end — the "provable handoff"
// the standards require (STANDARDS-WORKFLOW §7). It builds a synthetic case folder
// (an empty .mp4 + a real .srt), then drives the REAL bridge through the REAL
// code path: index the folder, search the transcript, add+trim a clip, mirror a
// human playhead edit, render, and run the multi-step agent loop (with a scripted
// model so it is deterministic and GPU-free), then approve the proposal. Every
// step is asserted; the measurable summary is printed as JSON. Exit 0 = proven.
func runSelftest(logf func(string, ...any)) error {
	dir, err := os.MkdirTemp("", "becky-edit-selftest-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)

	video := filepath.Join(dir, "bounty.mp4")
	if err := os.WriteFile(video, []byte("not-real-video-bytes"), 0o644); err != nil {
		return err
	}
	srt := "1\n00:00:18,000 --> 00:00:24,000\nevery penguin in the building is mine\n\n" +
		"2\n00:02:00,000 --> 00:02:10,000\ni'll pay you for the cat\n"
	if err := os.WriteFile(filepath.Join(dir, "bounty.srt"), []byte(srt), 0o644); err != nil {
		return err
	}

	// The agent step uses a deterministic scripted model (no GPU): add a fade, done.
	model := &selftestModel{replies: []string{
		`{"tool":"add_fade","args":{"id":"c1","kind":"out","seconds":1.5}}`,
		`{"done":true,"message":"Added a fade out to c1."}`,
	}}
	br := NewBridge(config.Load(), model, "", logf)
	h := &harness{br: br}

	// 1. ping
	h.call("ping", map[string]any{})
	// 2. open the folder.
	open := h.call("open_folder", map[string]any{"path": dir})
	mustEqFloat(h, "videos", open["videos"], 1)
	mustEqFloat(h, "with_transcripts", open["with_transcripts"], 1)
	// 3. search the transcript.
	srch := h.call("search", map[string]any{"query": "penguin"})
	if cnt := asFloat(srch["count"]); cnt < 1 {
		h.fail("search found no hits for 'penguin'")
	}
	// 4. add a clip from the hit.
	add := h.call("do", map[string]any{"verb": "add_clip", "args": map[string]any{"source": video, "in": 18.0, "out": 24.0}})
	mustEqFloat(h, "rev after add", add["rev"], 1)
	assertHost(h, add["host"], "timeline.append")
	// 5. trim it.
	trim := h.call("do", map[string]any{"verb": "trim_clip", "args": map[string]any{"id": "c1", "out": 22.0}})
	mustEqFloat(h, "rev after trim", trim["rev"], 2)
	// 6. mirror a HUMAN playhead edit from the host (the shared-state-from-both-sides path).
	ev := h.call("event", map[string]any{"name": "playhead", "args": map[string]any{"seconds": 5.0}})
	if dg, _ := ev["digest"].(string); !contains(dg, "playhead=5.0s") {
		h.fail("event(playhead) did not update the shared state digest")
	}
	// 7. render the compilation.
	rnd := h.call("do", map[string]any{"verb": "render", "args": map[string]any{"overlay": true}})
	assertHost(h, rnd["host"], "render.export")
	// 8. run the AI agent loop (scripted model) and approve its proposal.
	ag := h.call("agent", map[string]any{"goal": "add a fade out to clip c1"})
	if applied, _ := ag["applied"].([]any); len(applied) != 1 {
		h.fail(fmt.Sprintf("agent should propose 1 edit, got %v", ag["applied"]))
	}
	propID, _ := ag["proposal_id"].(string)
	if propID == "" {
		h.fail("agent returned no proposal id")
	}
	apv := h.call("approve", map[string]any{"id": propID})
	// 9. confirm the fade landed in the committed shared state.
	st := h.call("state", map[string]any{})
	dg, _ := st["digest"].(string)
	if !contains(dg, "fadeOut") {
		h.fail("after approve, the committed state should show the fadeOut effect:\n" + dg)
	}

	if h.err != nil {
		return h.err
	}

	summary := map[string]any{
		"ok":                true,
		"videos":            open["videos"],
		"with_transcripts":  open["with_transcripts"],
		"search_hits":       srch["count"],
		"rev_after_trim":    trim["rev"],
		"agent_applied":     1,
		"rev_after_approve": apv["rev"],
		"proof":             "indexed a folder, searched it, edited + rendered the timeline, ran the agent loop, and committed its edit — all offline.",
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(summary)
}

// harness drives the bridge and records the first failure.
type harness struct {
	br  *Bridge
	err error
}

func (h *harness) call(name string, args any) map[string]any {
	raw, _ := json.Marshal(args)
	resp := h.br.Dispatch(context.Background(), seam.CommandMsg{Type: seam.TypeCommand, ID: "t", Name: name, Args: raw})
	if !resp.OK {
		h.fail(fmt.Sprintf("%s failed: %s", name, resp.Error))
		return map[string]any{}
	}
	var data map[string]any
	if len(resp.Data) > 0 {
		_ = json.Unmarshal(resp.Data, &data)
	}
	return data
}

func (h *harness) fail(msg string) {
	if h.err == nil {
		h.err = fmt.Errorf("%s", msg)
	}
}

func mustEqFloat(h *harness, what string, got any, want float64) {
	if asFloat(got) != want {
		h.fail(fmt.Sprintf("%s: want %g, got %v", what, want, got))
	}
}

func assertHost(h *harness, host any, wantName string) {
	arr, ok := host.([]any)
	if !ok || len(arr) == 0 {
		h.fail("expected host command " + wantName + ", got none")
		return
	}
	first, _ := arr[0].(map[string]any)
	if name, _ := first["name"].(string); name != wantName {
		h.fail(fmt.Sprintf("expected host command %q, got %v", wantName, first["name"]))
	}
}

func asFloat(v any) float64 {
	if f, ok := v.(float64); ok {
		return f
	}
	return -1
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && indexOf(s, sub) >= 0
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}

// selftestModel is a deterministic scripted ctlagent.Model for the offline proof.
type selftestModel struct {
	replies []string
	i       int
}

func (m *selftestModel) Complete(_ context.Context, _, _ string) (string, error) {
	if m.i >= len(m.replies) {
		return `{"done":true,"message":"end"}`, nil
	}
	r := m.replies[m.i]
	m.i++
	return r, nil
}
