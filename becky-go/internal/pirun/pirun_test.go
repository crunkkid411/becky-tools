// pirun_test.go — table-driven unit tests for the deterministic harness core: request
// parsing, the DEFAULT-DENY allowlist enforcement, the agent-run manifest the omni
// layer consumes, the generated Pi extension, run-dir slugging, and the faked-Pi
// StubRunner (no Node, no model, no network).
//
// Fact-Forcing-Gate self-certification:
//  1. Callers: `go test ./internal/pirun/...`; same-package tests.
//  2. No-dup: first _test.go in internal/pirun (package just created).
//  3. Data shape: synthetic Request/PiSpec built inline; no data files.
//  4. Verbatim instruction: "use subagents in parallel.. build everything".
package pirun

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func boolPtr(b bool) *bool { return &b }

func TestParseRequest(t *testing.T) {
	cases := []struct {
		name    string
		in      string
		wantErr bool
	}{
		{"valid minimal", `{"goal":"do a thing","tools":[{"tool":"becky-transcribe"}]}`, false},
		{"empty goal", `{"goal":"","tools":[]}`, true},
		{"missing goal", `{"tools":[]}`, true},
		{"bad builtin", `{"goal":"g","builtin_tools":"sudo"}`, true},
		{"good builtin", `{"goal":"g","builtin_tools":"read-only"}`, false},
		{"not json", `not json`, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := ParseRequest([]byte(c.in))
			if (err != nil) != c.wantErr {
				t.Fatalf("ParseRequest err=%v wantErr=%v", err, c.wantErr)
			}
		})
	}
}

// TestAllowlist_DefaultDeny is the load-bearing test: a denied/unlisted tool is
// rejected; an allowed (listed) tool passes (SPEC §2.1 default-deny, §7.1 enforcement).
func TestAllowlist_DefaultDeny(t *testing.T) {
	catalog := map[string]bool{
		"becky-transcribe": true,
		"becky-identify":   true,
		"becky-search":     true,
		"becky-ocr":        true,
	}
	req := Request{
		Goal: "sweep",
		Tools: []ToolRequest{
			{Tool: "becky-transcribe"},
			{Tool: "becky-identify", FixedFlags: []string{"--kb", "kb-final"}},
			{Tool: "becky-search", Allow: boolPtr(false)}, // explicit deny
		},
	}
	al, err := BuildAllowlist(req, catalog)
	if err != nil {
		t.Fatalf("BuildAllowlist: %v", err)
	}

	// Allowed tools pass.
	if !al.Permits("becky-transcribe") {
		t.Error("becky-transcribe should be permitted (it was declared)")
	}
	if !al.Permits("becky-identify") {
		t.Error("becky-identify should be permitted (it was declared)")
	}
	// Explicit deny is rejected.
	if al.Permits("becky-search") {
		t.Error("becky-search was allow:false — must be DENIED")
	}
	// Unlisted tool in the catalog is denied by default.
	if al.Permits("becky-ocr") {
		t.Error("becky-ocr was never declared — default-deny must reject it")
	}
	// A tool that does not exist at all is denied.
	if al.Permits("rm-rf-everything") {
		t.Error("an unknown/never-declared tool must be denied")
	}
	// Fixed flags are carried for the permitted tool.
	if got := al.FixedFlags("becky-identify"); len(got) != 2 || got[0] != "--kb" {
		t.Errorf("fixed flags not carried: %v", got)
	}
	// Names are sorted and contain exactly the two permitted tools.
	names := al.Names()
	want := []string{"becky-identify", "becky-transcribe"}
	if strings.Join(names, ",") != strings.Join(want, ",") {
		t.Errorf("Names()=%v want %v (sorted, denied excluded)", names, want)
	}
}

func TestAllowlist_UnknownToolIsHardError(t *testing.T) {
	catalog := map[string]bool{"becky-transcribe": true}
	req := Request{Goal: "g", Tools: []ToolRequest{{Tool: "becky-not-a-real-tool"}}}
	_, err := BuildAllowlist(req, catalog)
	if !errors.Is(err, ErrUnknownTool) {
		t.Fatalf("expected ErrUnknownTool for a tool not in the catalog, got %v", err)
	}
}

func TestAllowlist_NilCatalogSkipsCheck(t *testing.T) {
	req := Request{Goal: "g", Tools: []ToolRequest{{Tool: "anything"}}}
	al, err := BuildAllowlist(req, nil) // nil catalog = skip catalog membership check
	if err != nil {
		t.Fatalf("nil catalog should skip the check: %v", err)
	}
	if !al.Permits("anything") {
		t.Error("with a nil catalog the listed tool should still be permitted")
	}
}

func TestBuildManifest_Deterministic(t *testing.T) {
	catalog := map[string]bool{"becky-transcribe": true, "becky-identify": true}
	req := Request{
		Goal:         "name mismatch sweep",
		Target:       `X:\cases\smith\clips`,
		BuiltinTools: "", // -> defaults to none
		Auth:         "", // -> defaults to subscription
		Model:        ModelSpec{Provider: "anthropic", ID: "sonnet"},
		Limits:       Limits{MaxTurns: 40, MaxBudgetUSD: 2, TimeoutSec: 1800},
		Tools: []ToolRequest{
			{Tool: "becky-identify", FixedFlags: []string{"--kb", "kb-final"}},
			{Tool: "becky-transcribe"},
		},
	}
	al, err := BuildAllowlist(req, catalog)
	if err != nil {
		t.Fatalf("BuildAllowlist: %v", err)
	}
	m1, err := BuildManifest(req, al)
	if err != nil {
		t.Fatalf("BuildManifest: %v", err)
	}
	if m1.Schema != SchemaManifest {
		t.Errorf("schema = %q want %q", m1.Schema, SchemaManifest)
	}
	if m1.BuiltinTools != BuiltinNone {
		t.Errorf("builtin default = %q want none", m1.BuiltinTools)
	}
	if m1.Auth != "subscription" {
		t.Errorf("auth default = %q want subscription", m1.Auth)
	}
	// Tools must be sorted regardless of request order (no map-order reliance).
	if len(m1.Tools) != 2 || m1.Tools[0].Tool != "becky-identify" || m1.Tools[1].Tool != "becky-transcribe" {
		t.Fatalf("tools not sorted deterministically: %+v", m1.Tools)
	}
	// Byte-stable: marshalling twice yields identical bytes.
	b1, err := MarshalManifest(m1)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	al2, _ := BuildAllowlist(req, catalog)
	m2, _ := BuildManifest(req, al2)
	b2, _ := MarshalManifest(m2)
	if string(b1) != string(b2) {
		t.Error("manifest JSON is not byte-stable for identical input")
	}
	// The manifest round-trips into a typed struct (omni consumes it).
	var back Manifest
	if err := json.Unmarshal(b1, &back); err != nil {
		t.Fatalf("manifest does not round-trip: %v", err)
	}
}

func TestRenderExtension_OnlyAllowlistedTools(t *testing.T) {
	tools := []ToolSpec{
		{Name: "becky-transcribe", Description: "transcribe media", FixedFlags: nil},
		{Name: "becky-identify", FixedFlags: []string{"--kb", "kb-final"}},
	}
	src := RenderExtension(tools)
	if !strings.Contains(src, `name: "becky-transcribe"`) {
		t.Error("generated extension missing becky-transcribe registerTool")
	}
	if !strings.Contains(src, `name: "becky-identify"`) {
		t.Error("generated extension missing becky-identify registerTool")
	}
	if strings.Contains(src, "becky-search") {
		t.Error("generated extension leaked a tool that was NOT in the allowlist")
	}
	// Fixed flags are baked into the shim.
	if !strings.Contains(src, `["--kb", "kb-final"]`) {
		t.Errorf("fixed flags not baked into the extension:\n%s", src)
	}
	// One registerTool per tool, plus the helper signature is present.
	if got := strings.Count(src, "pi.registerTool("); got != 2 {
		t.Errorf("registerTool count = %d want 2", got)
	}
	if !strings.Contains(src, "export default function") {
		t.Error("extension missing the default-export entrypoint")
	}
}

func TestGenerateExtension_WritesFile(t *testing.T) {
	dir := t.TempDir()
	spec := PiSpec{RunDir: dir, Tools: []ToolSpec{{Name: "becky-transcribe"}}}
	p, err := GenerateExtension(spec)
	if err != nil {
		t.Fatalf("GenerateExtension: %v", err)
	}
	if filepath.Base(p) != "becky-tools.ext.ts" {
		t.Errorf("unexpected ext path %q", p)
	}
	data, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("read generated ext: %v", err)
	}
	if !strings.Contains(string(data), "becky-transcribe") {
		t.Error("written extension missing the tool")
	}
}

func TestGenerateExtension_EmptyRunDirDegrades(t *testing.T) {
	if _, err := GenerateExtension(PiSpec{RunDir: ""}); err == nil {
		t.Error("expected an error (not a panic) for an empty RunDir")
	}
}

func TestRunDirSlug(t *testing.T) {
	cases := []struct{ in, want string }{
		{"For every clip, transcribe it", "for-every-clip-transcribe-it"},
		{"", "run"},
		{"!!!", "run"},
		// pathx.Base strips everything up to the last separator, so a goal that begins
		// with a Windows path keeps only the trailing words (deterministic on any host).
		{`X:\cases\smith do the sweep`, "smith-do-the-sweep"},
	}
	for _, c := range cases {
		if got := RunDirSlug(c.in); got != c.want {
			t.Errorf("RunDirSlug(%q)=%q want %q", c.in, got, c.want)
		}
	}
	// Never produces a double dash or a leading/trailing dash.
	got := RunDirSlug("a---b   c")
	if strings.Contains(got, "--") || strings.HasPrefix(got, "-") || strings.HasSuffix(got, "-") {
		t.Errorf("slug %q has bad dashes", got)
	}
}

func TestRun_NilRunnerDegrades(t *testing.T) {
	res, err := Run(context.Background(), nil, PiSpec{Goal: "g"}, nil)
	if err != nil {
		t.Fatalf("nil runner should degrade (nil err), got %v", err)
	}
	if !res.Degraded || res.DegradeReason == "" {
		t.Errorf("expected a degraded result with a reason, got %+v", res)
	}
}

func TestStubRunner_RecordsSpecAndReplaysEvents(t *testing.T) {
	ev := []json.RawMessage{json.RawMessage(`{"type":"tool_execution_end"}`)}
	stub := &StubRunner{Result: PiResult{
		FinalText: "[]",
		Turns:     3,
		CostUSD:   0.12,
		Events:    ev,
		ToolCalls: []ToolCallRecord{{Tool: "becky-transcribe", Exit: 0}},
	}}
	var seen int
	res, err := Run(context.Background(), stub, PiSpec{Goal: "g", BuiltinTools: "none"}, func(json.RawMessage) { seen++ })
	if err != nil {
		t.Fatalf("Run with stub: %v", err)
	}
	if stub.LastSpec.Goal != "g" {
		t.Errorf("stub did not record the spec it was given")
	}
	if seen != 1 {
		t.Errorf("onEvent called %d times, want 1 (one canned event replayed)", seen)
	}
	if res.Turns != 3 || res.CostUSD != 0.12 || res.FinalText != "[]" {
		t.Errorf("stub result not returned verbatim: %+v", res)
	}
}

func TestStubRunner_ErrPathDegrades(t *testing.T) {
	stub := &StubRunner{
		Result: PiResult{Degraded: true, DegradeReason: "pi CLI not found", Stopped: "error"},
		Err:    errors.New("boom"),
	}
	res, err := Run(context.Background(), stub, PiSpec{Goal: "g"}, nil)
	if err == nil {
		t.Error("expected the stub error to surface")
	}
	if !res.Degraded {
		t.Error("expected a degraded result alongside the error")
	}
}

func TestStubRunner_ContextCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	stub := &StubRunner{}
	res, err := Run(ctx, stub, PiSpec{Goal: "g"}, nil)
	if err == nil {
		t.Error("expected an error on a cancelled context")
	}
	if res.Stopped != "timeout" {
		t.Errorf("Stopped = %q want timeout", res.Stopped)
	}
}
