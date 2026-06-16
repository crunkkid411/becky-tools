package canvas

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// ─── extractFirstJSON ─────────────────────────────────────────────────────────

func TestExtractFirstJSON(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		input string
		want  string // "" means not found
	}{
		{
			name:  "clean JSON",
			input: `{"kind":"pitch","summary":"up one","before":"C4","after":"C#4","delta":1}`,
			want:  `{"kind":"pitch","summary":"up one","before":"C4","after":"C#4","delta":1}`,
		},
		{
			name:  "JSON with leading chatter",
			input: "llama.cpp timing: 42ms\n\n{\"kind\":\"gain\",\"summary\":\"louder\",\"before\":\"-6 dB\",\"after\":\"-3 dB\",\"delta\":3}",
			want:  `{"kind":"gain","summary":"louder","before":"-6 dB","after":"-3 dB","delta":3}`,
		},
		{
			name:  "JSON with trailing chatter",
			input: `{"kind":"trim","summary":"shorter","before":"len 480","after":"len 240","delta":-240} [end of output]`,
			want:  `{"kind":"trim","summary":"shorter","before":"len 480","after":"len 240","delta":-240}`,
		},
		{
			name:  "JSON wrapped in model chatter both sides",
			input: "BOS token\nSome preamble\n{\"kind\":\"text\",\"summary\":\"rename\",\"before\":\"Bass\",\"after\":\"Bass Vox\",\"delta\":0}\n<|end|>",
			want:  `{"kind":"text","summary":"rename","before":"Bass","after":"Bass Vox","delta":0}`,
		},
		{
			name:  "escaped quote inside string",
			input: `{"kind":"text","summary":"rename to \"Lead\"","before":"old","after":"Lead","delta":0}`,
			want:  `{"kind":"text","summary":"rename to \"Lead\"","before":"old","after":"Lead","delta":0}`,
		},
		{
			name:  "no JSON in string",
			input: "the model failed and printed nothing useful",
			want:  "",
		},
		{
			name:  "empty string",
			input: "",
			want:  "",
		},
		{
			name:  "unclosed brace",
			input: `{"kind":"pitch"`,
			want:  "",
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := extractFirstJSON(tc.input)
			if got != tc.want {
				t.Errorf("extractFirstJSON(%q)\ngot  %q\nwant %q", tc.input, got, tc.want)
			}
		})
	}
}

// ─── parseModelResponse ───────────────────────────────────────────────────────

func TestParseModelResponse(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		stdout    string
		wantKind  string
		wantSum   string // substring that MUST appear in Summary
		wantErr   bool
		errSubstr string // substring that MUST appear in error message
	}{
		{
			name:     "clean pitch response",
			stdout:   `{"kind":"pitch","summary":"Transpose up one semitone","before":"C4","after":"C#4","delta":1}`,
			wantKind: "pitch",
			wantSum:  "Transpose",
		},
		{
			name:     "clean gain response with leading chatter",
			stdout:   "model loaded\n{\"kind\":\"gain\",\"summary\":\"Raise volume by 3 dB\",\"before\":\"-6 dB\",\"after\":\"-3 dB\",\"delta\":3}\ndone",
			wantKind: "gain",
			wantSum:  "volume",
		},
		{
			name:     "timing response",
			stdout:   `{"kind":"timing","summary":"Move clip forward one beat","before":"tick 0","after":"tick 120","delta":120}`,
			wantKind: "timing",
			wantSum:  "Move",
		},
		{
			name:     "trim response",
			stdout:   `{"kind":"trim","summary":"Shorten region by half a bar","before":"len 480","after":"len 240","delta":-240}`,
			wantKind: "trim",
			wantSum:  "Shorten",
		},
		{
			name:     "route response",
			stdout:   `{"kind":"route","summary":"Send to sidechain bus","before":"main out","after":"sidechain","delta":0}`,
			wantKind: "route",
			wantSum:  "sidechain",
		},
		{
			name:     "text response",
			stdout:   `{"kind":"text","summary":"Rename track to Lead Vox","before":"Bass","after":"Lead Vox","delta":0}`,
			wantKind: "text",
			wantSum:  "Rename",
		},
		{
			name:     "structure response",
			stdout:   `{"kind":"structure","summary":"Add new drum track","before":"3 tracks","after":"4 tracks","delta":1}`,
			wantKind: "structure",
			wantSum:  "Add",
		},
		{
			name:     "unknown kind passes through",
			stdout:   `{"kind":"unknown","summary":"Could not classify instruction","before":"","after":"","delta":0}`,
			wantKind: "unknown",
			wantSum:  "classify",
		},
		{
			name:      "no JSON at all",
			stdout:    "I'm sorry, I cannot help with that.",
			wantErr:   true,
			errSubstr: "no JSON object found",
		},
		{
			name:      "malformed JSON",
			stdout:    `{"kind": "pitch", "summary":}`,
			wantErr:   true,
			errSubstr: "JSON unmarshal",
		},
		{
			name:      "missing kind field",
			stdout:    `{"summary":"something","before":"C4","after":"D4","delta":2}`,
			wantErr:   true,
			errSubstr: "empty kind",
		},
		{
			name:      "missing summary field",
			stdout:    `{"kind":"pitch","before":"C4","after":"D4","delta":2}`,
			wantErr:   true,
			errSubstr: "empty summary",
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			mr, err := parseModelResponse(tc.stdout)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error, got mr=%+v", mr)
				}
				if tc.errSubstr != "" && !strings.Contains(err.Error(), tc.errSubstr) {
					t.Errorf("error %q does not contain %q", err.Error(), tc.errSubstr)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if mr.Kind != tc.wantKind {
				t.Errorf("Kind = %q, want %q", mr.Kind, tc.wantKind)
			}
			if tc.wantSum != "" && !strings.Contains(mr.Summary, tc.wantSum) {
				t.Errorf("Summary %q does not contain %q", mr.Summary, tc.wantSum)
			}
		})
	}
}

// ─── mapKind ─────────────────────────────────────────────────────────────────

func TestMapKind(t *testing.T) {
	t.Parallel()
	tests := []struct {
		input string
		want  ChangeKind
	}{
		{"pitch", ChangePitch},
		{"PITCH", ChangePitch},
		{"timing", ChangeTiming},
		{"Timing", ChangeTiming},
		{"trim", ChangeTrim},
		{"gain", ChangeGain},
		{"route", ChangeRoute},
		{"text", ChangeText},
		{"structure", ChangeStructure},
		{"unknown", ChangeUnknown},
		{"", ChangeUnknown},
		{"anything_else", ChangeUnknown},
	}
	for _, tc := range tests {
		tc := tc
		t.Run(tc.input, func(t *testing.T) {
			t.Parallel()
			got := mapKind(tc.input)
			if got != tc.want {
				t.Errorf("mapKind(%q) = %q, want %q", tc.input, got, tc.want)
			}
		})
	}
}

// ─── buildPrompt ─────────────────────────────────────────────────────────────

func TestBuildPrompt(t *testing.T) {
	t.Parallel()
	scene := NewScene(ModeDAW)
	scene.Title = "My Song"
	sel := Selection{Kind: SelectNote, TrackID: "t1", NoteIdx: 3}

	prompt := buildPrompt(scene, sel, "transpose up one octave")

	mustContain := []string{
		"My Song",
		"transpose up one octave",
		`"kind"`,
		"pitch|timing|trim|gain|route|text|structure|unknown",
		`"before"`,
		`"after"`,
		`"delta"`,
		"No markdown",
	}
	for _, want := range mustContain {
		if !strings.Contains(prompt, want) {
			t.Errorf("prompt missing %q", want)
		}
	}
}

// ─── buildModelArgs ───────────────────────────────────────────────────────────

func TestBuildModelArgs(t *testing.T) {
	t.Parallel()
	args := buildModelArgs("/path/to/model.gguf", "the prompt")
	argStr := strings.Join(args, " ")
	for _, want := range []string{"/path/to/model.gguf", "--temp", "0", "--seed", "42", "the prompt"} {
		if !strings.Contains(argStr, want) {
			t.Errorf("buildModelArgs output %v missing %q", args, want)
		}
	}
}

// ─── ModelTransformer.Propose via fakeRunner ─────────────────────────────────

// fakeRunner replays canned stdout without ever launching a process.
type fakeRunner struct {
	stdout string
	err    error
}

func (f fakeRunner) run(_ string, _ []string) (string, error) {
	return f.stdout, f.err
}

func TestModelTransformer_Propose_FakeRunner(t *testing.T) {
	t.Parallel()

	scene := NewScene(ModeDAW)
	scene.Tracks = []Track{
		{ID: "t1", Name: "Lead", Kind: LaneMIDI},
	}
	sel := Selection{Kind: SelectNote, TrackID: "t1", NoteIdx: 0}
	instruction := "transpose up one semitone"

	tests := []struct {
		name        string
		stdout      string
		runErr      error
		wantErr     bool
		wantKind    ChangeKind
		wantSummary string // substring
		wantBefore  string
		wantAfter   string
		wantDelta   float64
	}{
		{
			name:        "clean pitch JSON",
			stdout:      `{"kind":"pitch","summary":"Transpose note up one semitone","before":"C4","after":"C#4","delta":1}`,
			wantKind:    ChangePitch,
			wantSummary: "semitone",
			wantBefore:  "C4",
			wantAfter:   "C#4",
			wantDelta:   1,
		},
		{
			name:        "JSON buried in chatter",
			stdout:      "llama.cpp 2024 output\n{\"kind\":\"gain\",\"summary\":\"Lower gain by 3 dB\",\"before\":\"-3 dB\",\"after\":\"-6 dB\",\"delta\":-3}\n[end]",
			wantKind:    ChangeGain,
			wantSummary: "Lower gain",
			wantBefore:  "-3 dB",
			wantAfter:   "-6 dB",
			wantDelta:   -3,
		},
		{
			name:        "structure change",
			stdout:      `{"kind":"structure","summary":"Add new synth track","before":"3 tracks","after":"4 tracks","delta":1}`,
			wantKind:    ChangeStructure,
			wantSummary: "Add new synth",
			wantDelta:   1,
		},
		{
			name:        "unknown kind from model",
			stdout:      `{"kind":"unknown","summary":"Could not determine change type","before":"","after":"","delta":0}`,
			wantKind:    ChangeUnknown,
			wantSummary: "determine",
		},
		{
			name:    "exec error",
			runErr:  fmt.Errorf("binary not found"),
			wantErr: true,
		},
		{
			name:    "empty output",
			stdout:  "",
			wantErr: true,
		},
		{
			name:    "malformed JSON",
			stdout:  "{bad json}",
			wantErr: true,
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			mt := newModelTransformer("/fake/bin", "/fake/model.gguf", fakeRunner{stdout: tc.stdout, err: tc.runErr})
			p, err := mt.Propose(scene, sel, instruction)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error, got proposal %+v", p)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if p == nil {
				t.Fatal("got nil proposal without error")
			}
			if p.Kind != tc.wantKind {
				t.Errorf("Kind = %q, want %q", p.Kind, tc.wantKind)
			}
			if tc.wantSummary != "" && !strings.Contains(p.Summary, tc.wantSummary) {
				t.Errorf("Summary %q does not contain %q", p.Summary, tc.wantSummary)
			}
			if tc.wantBefore != "" && p.Before != tc.wantBefore {
				t.Errorf("Before = %q, want %q", p.Before, tc.wantBefore)
			}
			if tc.wantAfter != "" && p.After != tc.wantAfter {
				t.Errorf("After = %q, want %q", p.After, tc.wantAfter)
			}
			if p.Delta != tc.wantDelta {
				t.Errorf("Delta = %v, want %v", p.Delta, tc.wantDelta)
			}
			// Proposal must echo selection and instruction.
			if p.Sel.Kind != sel.Kind {
				t.Errorf("Proposal.Sel.Kind = %q, want %q", p.Sel.Kind, sel.Kind)
			}
			if p.Instruction != instruction {
				t.Errorf("Proposal.Instruction = %q, want %q", p.Instruction, instruction)
			}
			// ID must be deterministic (same formula as StubTransformer).
			wantID := stubID(sel, instruction)
			if p.ID != wantID {
				t.Errorf("Proposal.ID = %q, want %q", p.ID, wantID)
			}
		})
	}
}

// TestModelTransformer_Propose_EmptySelection covers the guard at the top of Propose.
func TestModelTransformer_Propose_EmptySelection(t *testing.T) {
	t.Parallel()
	mt := newModelTransformer("/fake/bin", "/fake/model.gguf", fakeRunner{
		stdout: `{"kind":"pitch","summary":"ok","before":"C4","after":"C#4","delta":1}`,
	})
	_, err := mt.Propose(NewScene(ModeDAW), Selection{Kind: SelectNone}, "transpose up")
	if err == nil {
		t.Fatal("expected error for empty selection")
	}
}

// TestModelTransformer_Propose_EmptyInstruction covers the instruction guard.
func TestModelTransformer_Propose_EmptyInstruction(t *testing.T) {
	t.Parallel()
	mt := newModelTransformer("/fake/bin", "/fake/model.gguf", fakeRunner{
		stdout: `{"kind":"pitch","summary":"ok","before":"C4","after":"C#4","delta":1}`,
	})
	sel := Selection{Kind: SelectClip, ClipID: "c1"}
	_, err := mt.Propose(NewScene(ModeDAW), sel, "   ")
	if err == nil {
		t.Fatal("expected error for empty instruction")
	}
}

// ─── fake-binary end-to-end test ─────────────────────────────────────────────

// TestModelTransformer_FakeBinary_EndToEnd exercises the full exec path against a
// real temporary script that echoes canned JSON. This verifies the production
// execModelRunner + argument construction works without a GPU or real model file.
func TestModelTransformer_FakeBinary_EndToEnd(t *testing.T) {
	t.Parallel()

	cannedJSON := `{"kind":"pitch","summary":"Transpose note up one semitone","before":"C4","after":"C#4","delta":1}`

	// Write a temporary script that echoes the canned JSON (args are ignored).
	tmpDir := t.TempDir()
	var scriptPath string
	if runtime.GOOS == "windows" {
		scriptPath = filepath.Join(tmpDir, "fake_llama.cmd")
		content := "@echo off\r\necho " + cannedJSON + "\r\n"
		if err := os.WriteFile(scriptPath, []byte(content), 0o755); err != nil {
			t.Fatalf("write fake script: %v", err)
		}
	} else {
		scriptPath = filepath.Join(tmpDir, "fake_llama.sh")
		content := "#!/bin/sh\nprintf '%s\\n' '" + cannedJSON + "'\n"
		if err := os.WriteFile(scriptPath, []byte(content), 0o755); err != nil {
			t.Fatalf("write fake script: %v", err)
		}
	}

	// Bypass PickTransformer and construct ModelTransformer directly so the model
	// path check is skipped (the script ignores all args including -m).
	mt := newModelTransformer(scriptPath, "/nonexistent/model.gguf", execModelRunner{})

	scene := NewScene(ModeDAW)
	sel := Selection{Kind: SelectNote, TrackID: "t1", NoteIdx: 0}
	p, err := mt.Propose(scene, sel, "transpose up one semitone")
	if err != nil {
		t.Fatalf("Propose via fake binary: %v", err)
	}
	if p.Kind != ChangePitch {
		t.Errorf("Kind = %q, want ChangePitch", p.Kind)
	}
	if !strings.Contains(p.Summary, "semitone") {
		t.Errorf("Summary %q does not contain 'semitone'", p.Summary)
	}
	if p.Before != "C4" {
		t.Errorf("Before = %q, want C4", p.Before)
	}
	if p.After != "C#4" {
		t.Errorf("After = %q, want C#4", p.After)
	}
	if p.Delta != 1 {
		t.Errorf("Delta = %v, want 1", p.Delta)
	}
}

// ─── PickTransformer ─────────────────────────────────────────────────────────

// TestPickTransformer_AbsentBinary verifies StubTransformer is returned when the
// binary does not exist. Not parallel: uses t.Setenv.
func TestPickTransformer_AbsentBinary(t *testing.T) {
	t.Setenv(EnvTransformBin, "/nonexistent/path/llama-cli.exe")
	t.Setenv(EnvTransformModel, "/nonexistent/model.gguf")

	tr := PickTransformer()
	if _, ok := tr.(StubTransformer); !ok {
		t.Errorf("PickTransformer with absent binary: got %T, want StubTransformer", tr)
	}
}

// TestPickTransformer_AbsentModel verifies StubTransformer is returned when the
// binary exists but the model GGUF does not. Not parallel: uses t.Setenv.
func TestPickTransformer_AbsentModel(t *testing.T) {
	tmpDir := t.TempDir()
	fakeBin := filepath.Join(tmpDir, "llama-cli.exe")
	if err := os.WriteFile(fakeBin, []byte("dummy"), 0o755); err != nil {
		t.Fatalf("write dummy bin: %v", err)
	}

	t.Setenv(EnvTransformBin, fakeBin)
	t.Setenv(EnvTransformModel, "/nonexistent/model.gguf")

	tr := PickTransformer()
	if _, ok := tr.(StubTransformer); !ok {
		t.Errorf("PickTransformer with absent model: got %T, want StubTransformer", tr)
	}
}

// TestPickTransformer_BothPresent verifies ModelTransformer is returned when both
// binary and model files exist on disk. Not parallel: uses t.Setenv.
func TestPickTransformer_BothPresent(t *testing.T) {
	tmpDir := t.TempDir()
	fakeBin := filepath.Join(tmpDir, "llama-cli.exe")
	fakeModel := filepath.Join(tmpDir, "model.gguf")
	for _, p := range []string{fakeBin, fakeModel} {
		if err := os.WriteFile(p, []byte("dummy"), 0o644); err != nil {
			t.Fatalf("write dummy file %s: %v", p, err)
		}
	}

	t.Setenv(EnvTransformBin, fakeBin)
	t.Setenv(EnvTransformModel, fakeModel)

	tr := PickTransformer()
	if _, ok := tr.(*ModelTransformer); !ok {
		t.Errorf("PickTransformer with both present: got %T, want *ModelTransformer", tr)
	}
}

// TestPickTransformer_FallsBackToStub verifies the StubTransformer degrade path
// still works end-to-end after PickTransformer returns it. Not parallel: uses t.Setenv.
func TestPickTransformer_FallsBackToStub(t *testing.T) {
	t.Setenv(EnvTransformBin, "/nonexistent/bin")
	t.Setenv(EnvTransformModel, "/nonexistent/model.gguf")

	tr := PickTransformer()
	scene := NewScene(ModeDAW)
	sel := Selection{Kind: SelectTrack, TrackID: "kick", Label: "Kick"}
	p, err := tr.Propose(scene, sel, "make it louder")
	if err != nil {
		t.Fatalf("StubTransformer.Propose: %v", err)
	}
	if p == nil {
		t.Fatal("got nil proposal from StubTransformer")
	}
	if p.Kind != ChangeGain {
		t.Errorf("Kind = %q, want ChangeGain", p.Kind)
	}
}

// TestResolveTransformPaths_Defaults verifies the hardcoded defaults are returned
// when the env vars are unset. Not parallel: clears env vars.
func TestResolveTransformPaths_Defaults(t *testing.T) {
	t.Setenv(EnvTransformBin, "")
	t.Setenv(EnvTransformModel, "")

	bin, model := resolveTransformPaths()
	if bin != DefaultTransformBin {
		t.Errorf("bin = %q, want %q", bin, DefaultTransformBin)
	}
	if model != DefaultTransformModel {
		t.Errorf("model = %q, want %q", model, DefaultTransformModel)
	}
}

// TestResolveTransformPaths_EnvOverride verifies env vars win over the defaults.
// Not parallel: uses t.Setenv.
func TestResolveTransformPaths_EnvOverride(t *testing.T) {
	t.Setenv(EnvTransformBin, "/custom/bin")
	t.Setenv(EnvTransformModel, "/custom/model.gguf")

	bin, model := resolveTransformPaths()
	if bin != "/custom/bin" {
		t.Errorf("bin = %q, want /custom/bin", bin)
	}
	if model != "/custom/model.gguf" {
		t.Errorf("model = %q, want /custom/model.gguf", model)
	}
}
