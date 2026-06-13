// model_test.go — headless verification of the becky-ask shell. Driving a bubbletea
// TUI needs a real terminal, so these tests exercise the Model-Update-View and the
// router directly (no PTY): they prove the window sizes, renders the "Ask Becky:"
// prompt + footer copy, echoes the user's line, answers catalog questions offline, and
// shows the honest placeholder for everything else.
package main

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// sized returns a model that has received an initial WindowSizeMsg, mimicking what
// bubbletea sends on startup so the viewport is ready and View() renders the full UI.
func sized(t *testing.T) model {
	t.Helper()
	m := newModel()
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	return updated.(model)
}

func TestViewRendersPromptAndFooter(t *testing.T) {
	// Arrange
	m := sized(t)

	// Act
	out := m.View()

	// Assert — the original look: the "Ask Becky:" prompt and the exact footer copy.
	if !strings.Contains(out, "Ask Becky:") {
		t.Errorf("View() missing the \"Ask Becky:\" prompt; got:\n%s", out)
	}
	if !strings.Contains(out, "Type your question and press Enter | q: quit") {
		t.Errorf("View() missing the original footer hint; got:\n%s", out)
	}
}

func TestSubmitEchoesQuestionAndReplies(t *testing.T) {
	// Arrange
	m := sized(t)
	m.input.SetValue("can becky find where Shelby appears?")

	// Act — Enter submits.
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(model)
	out := m.View()

	// Assert — the question is echoed and a catalog match (appearances) is surfaced.
	if !strings.Contains(out, "Shelby") {
		t.Errorf("transcript did not echo the question; got:\n%s", out)
	}
	if !strings.Contains(out, "appearances") {
		t.Errorf("expected an offline catalog answer naming the appearances op; got:\n%s", out)
	}
	if m.input.Value() != "" {
		t.Errorf("input should be cleared after submit, got %q", m.input.Value())
	}
}

func TestRouteQuestionCatalogHit(t *testing.T) {
	// Arrange / Act
	reply := routeQuestion("how do I transcribe a video?")

	// Assert — names the transcribe tool and shows a runnable example.
	if !strings.Contains(reply, "becky-transcribe") {
		t.Errorf("expected transcribe in catalog reply; got:\n%s", reply)
	}
}

func TestRouteQuestionPlaceholderForUnknown(t *testing.T) {
	// Arrange / Act — a request with no catalog keyword falls to the honest placeholder.
	reply := routeQuestion("please write me a poem about the moon")

	// Assert — it must point at the spec and not pretend to have a brain.
	if !strings.Contains(reply, "SPEC-BECKY-ASK.md") {
		t.Errorf("placeholder should reference the spec; got:\n%s", reply)
	}
}

func TestEmptyEnterIsNoOp(t *testing.T) {
	// Arrange
	m := sized(t)
	before := len(m.transcript)

	// Act — Enter on an empty line.
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(model)

	// Assert — nothing added.
	if len(m.transcript) != before {
		t.Errorf("empty submit changed transcript length: %d -> %d", before, len(m.transcript))
	}
}

func TestCtrlCQuits(t *testing.T) {
	// Arrange
	m := sized(t)

	// Act
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyCtrlC})

	// Assert — a quit command is returned.
	if cmd == nil {
		t.Fatal("ctrl+c should return a command")
	}
	if msg := cmd(); msg == nil {
		t.Fatal("ctrl+c command produced no message")
	} else if _, ok := msg.(tea.QuitMsg); !ok {
		t.Errorf("ctrl+c should produce tea.QuitMsg, got %T", msg)
	}
}

func TestBareQQuitsOnlyWhenInputEmpty(t *testing.T) {
	// Arrange — typing "q" inside a question must NOT quit.
	m := sized(t)
	m.input.SetValue("query about q")

	// Act
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("q")})

	// Assert — no quit while the input has content.
	if cmd != nil {
		if msg := cmd(); msg != nil {
			if _, ok := msg.(tea.QuitMsg); ok {
				t.Error("bare q should not quit while input is non-empty")
			}
		}
	}
}

// sizedWithTarget returns a sized model that already has a dropped video target
// and the quick-action menu, mimicking a drag-drop launch.
func sizedWithTarget(t *testing.T) model {
	t.Helper()
	tgt := resolveTarget([]string{makeFile(t, t.TempDir(), "clip.mp4")})
	m := newModelWith(tgt, nil)
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	return updated.(model)
}

func TestView_ShowsTargetBarAndActionStrip(t *testing.T) {
	// Arrange — a dropped target lights up the bar + the quick-action strip.
	m := sizedWithTarget(t)

	// Act
	out := m.View()

	// Assert — FEATURE 1 (Target bar) + FEATURE 2 (quick actions) are visible.
	if !strings.Contains(out, "Target:") || !strings.Contains(out, "clip.mp4") {
		t.Errorf("expected a 'Target: clip.mp4' bar; got:\n%s", out)
	}
	if !strings.Contains(out, "Transcribe") || !strings.Contains(out, "[1]") {
		t.Errorf("expected the quick-action strip with [1] Transcribe; got:\n%s", out)
	}
}

func TestNumberKey_FiresQuickActionAndGoesBusy(t *testing.T) {
	// Arrange — a target with quick actions; the input is empty so a digit is a hotkey.
	m := sizedWithTarget(t)

	// Act — press "1" (Transcribe). The press is the confirmation (no typing).
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("1")})
	m = updated.(model)

	// Assert — the model goes busy and a run command was returned (FEATURE 2).
	if !m.busy {
		t.Errorf("pressing a quick-action key should mark the model busy")
	}
	if cmd == nil {
		t.Fatalf("pressing a quick-action key should return a run command")
	}
	// The async command, when invoked, yields a runDoneMsg. Transcribe is now a
	// WORKFLOW (it produces a finished, diarized transcript that also surfaces
	// on-screen text), so the headline command is the chained becky-pipeline.
	msg := cmd()
	done, ok := msg.(runDoneMsg)
	if !ok {
		t.Fatalf("expected runDoneMsg, got %T", msg)
	}
	if len(done.res.Command) == 0 || done.res.Command[0] != "becky-pipeline" {
		t.Errorf("quick action 1 (Transcribe) should run the becky-pipeline workflow, got %v", done.res.Command)
	}
}

func TestTypedClearAction_StagesPendingThenYRuns(t *testing.T) {
	// Arrange — a target; type a clear action and submit.
	m := sizedWithTarget(t)
	m.input.SetValue("transcribe this")
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(model)

	// Assert — the command is staged (pending), NOT auto-run (opt-in posture).
	if len(m.pending) == 0 {
		t.Fatalf("a typed clear action should stage a pending command, not auto-run")
	}
	if m.busy {
		t.Errorf("typed action should wait for confirmation, not be busy yet")
	}

	// Act — press y to confirm.
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("y")})
	m = updated.(model)

	// Assert — now it runs.
	if !m.busy || cmd == nil {
		t.Errorf("y should run the pending command (busy=%v cmd=%v)", m.busy, cmd != nil)
	}
}

func TestTypedQuestion_DoesNotStageOrRun(t *testing.T) {
	// Arrange — a target, but a QUESTION. Must not stage a command (FEATURE 4).
	m := sizedWithTarget(t)
	m.input.SetValue("can becky tell where this was filmed?")

	// Act
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(model)

	// Assert — nothing staged, not busy.
	if len(m.pending) != 0 {
		t.Errorf("a question must not stage a command, got %v", m.pending)
	}
	if m.busy {
		t.Errorf("a question must not run a tool")
	}
}

func TestPastedPath_SetsTarget(t *testing.T) {
	// Arrange — a sized model with no target; paste/type a real path.
	m := sized(t)
	clip := makeFile(t, t.TempDir(), "pasted.mp4")
	m.input.SetValue(clip)

	// Act
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(model)

	// Assert — the path became the target (brief: "Also accept paths pasted/typed").
	if !m.target.HasTarget() || m.target.Primary() != clip {
		t.Errorf("pasting a path should set the target; got %+v", m.target)
	}
	if len(m.actions) == 0 {
		t.Errorf("setting a video target should populate quick actions")
	}
}

func TestResolveIntentModel_PointsAtQwen35(t *testing.T) {
	// FEATURE 3 — the default intent model is the user's Qwen3.5-4B GGUF (no
	// substitution). The env override takes precedence when set.
	t.Setenv("BECKY_ASK_MODEL", "")
	if got := resolveIntentModel(); !strings.Contains(got, "Qwen3.5-4B") {
		t.Errorf("default intent model should be Qwen3.5-4B, got %q", got)
	}
	t.Setenv("BECKY_ASK_MODEL", `X:\custom\model.gguf`)
	if got := resolveIntentModel(); got != `X:\custom\model.gguf` {
		t.Errorf("env override ignored, got %q", got)
	}
}

func TestBinPathFor_MissingToolErrorsCleanly(t *testing.T) {
	// A tool that isn't built next to becky-ask must error, not panic.
	if _, err := binPathFor("definitely-not-a-real-tool"); err == nil {
		t.Error("binPathFor should error for a missing tool")
	}
}
