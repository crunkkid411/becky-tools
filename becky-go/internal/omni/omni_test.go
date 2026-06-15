// omni_test.go — table-driven unit tests for the deterministic governance core:
// governance validation (the sensitive master switch), policy + Omnibox-sandbox
// generation, the Windows no-sandbox DEGRADE path, manifest consumption, and the
// faked-Omnigent StubRunner (no Python, no model, no network).
//
// Fact-Forcing-Gate self-certification:
//  1. Callers: `go test ./internal/omni/...`; same-package tests.
//  2. No-dup: first _test.go in internal/omni (package just created).
//  3. Data shape: synthetic Request/pirun.Manifest built inline; no data files.
//  4. Verbatim instruction: "use subagents in parallel.. build everything".
package omni

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"runtime"
	"strings"
	"testing"

	"becky-go/internal/pirun"
)

func TestParseRequest_and_LoadManifest(t *testing.T) {
	reqJSON := `{"schema":"becky-omnigent/request@1","goal":"sweep","governance":{"sandbox":"offline"}}`
	if _, err := ParseRequest([]byte(reqJSON)); err != nil {
		t.Fatalf("ParseRequest: %v", err)
	}
	// A valid manifest round-trips.
	m := pirun.Manifest{
		Schema:       pirun.SchemaManifest,
		Goal:         "sweep",
		BuiltinTools: pirun.BuiltinNone,
		Auth:         "subscription",
		Tools:        []pirun.ManifestTool{{Tool: "becky-transcribe"}},
	}
	b, _ := json.Marshal(m)
	got, err := LoadManifest(b)
	if err != nil {
		t.Fatalf("LoadManifest: %v", err)
	}
	if len(got.Tools) != 1 || got.Tools[0].Tool != "becky-transcribe" {
		t.Errorf("manifest tools not parsed: %+v", got.Tools)
	}
	// Wrong schema and empty-tools manifests are rejected.
	if _, err := LoadManifest([]byte(`{"schema":"nope","tools":[{"tool":"x"}]}`)); err == nil {
		t.Error("expected schema mismatch error")
	}
	if _, err := LoadManifest([]byte(`{"schema":"becky-harness/manifest@1","tools":[]}`)); err == nil {
		t.Error("expected empty-tools error (default-deny would permit nothing)")
	}
}

func TestValidateGovernance(t *testing.T) {
	cases := []struct {
		name    string
		req     Request
		wantErr bool
	}{
		{"offline default ok", Request{Goal: "g"}, false},
		{"bad sandbox", Request{Governance: Governance{Sandbox: "wide-open"}}, true},
		{"bad fs_writes", Request{Governance: Governance{FSWrites: "everything"}}, true},
		{"bad share", Request{Governance: Governance{Share: "world"}}, true},
		{"output-only needs dir", Request{Governance: Governance{FSWrites: FSWritesOutputOnly}}, true},
		{"output-only with dir ok", Request{Governance: Governance{FSWrites: FSWritesOutputOnly, OutputDir: `X:\out`}}, false},
		{"research needs hosts", Request{Governance: Governance{Sandbox: SandboxResearch}}, true},
		{"research with hosts ok", Request{Governance: Governance{Sandbox: SandboxResearch, AllowHosts: []string{"api.example.com"}}}, false},
		{"sensitive + hosted refused", Request{Auth: "subscription", Governance: Governance{Sensitive: true}}, true},
		{"sensitive + local ok", Request{Auth: "local", Governance: Governance{Sensitive: true}}, false},
		{"sensitive + public refused", Request{Auth: "local", Governance: Governance{Sensitive: true, Share: SharePublic}}, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := ValidateGovernance(c.req)
			if (err != nil) != c.wantErr {
				t.Fatalf("ValidateGovernance err=%v wantErr=%v", err, c.wantErr)
			}
		})
	}
}

// TestSensitiveMasterSwitch proves sensitive:true forces auth:local + offline + private,
// overriding convenience (SPEC §5.1).
func TestSensitiveMasterSwitch(t *testing.T) {
	req := Request{
		Auth: "local",
		Governance: Governance{
			Sensitive: true,
			Sandbox:   SandboxFull, // must be forced back down to offline
			Share:     SharePublic, // (would be rejected by Validate; here we test the forcing)
		},
	}
	g, auth := req.effectiveGovernance()
	if auth != "local" {
		t.Errorf("sensitive auth = %q, want local", auth)
	}
	if g.Sandbox != SandboxOffline {
		t.Errorf("sensitive sandbox = %q, want offline (forced down from full)", g.Sandbox)
	}
	if g.Share != SharePrivate {
		t.Errorf("sensitive share = %q, want private", g.Share)
	}
}

func TestRenderPolicy_Deterministic(t *testing.T) {
	g := Governance{
		MaxCostUSD:    2,
		AskThresholds: []float64{1.5, 0.5, 1.0}, // unsorted on purpose
		AskOn:         []string{"os_tools"},
		FSWrites:      FSWritesDeny,
	}
	p1 := RenderPolicy(g)
	p2 := RenderPolicy(g)
	if p1 != p2 {
		t.Error("RenderPolicy is not deterministic for identical input")
	}
	if !strings.Contains(p1, "cost.cost_budget") {
		t.Error("cost budget policy missing when MaxCostUSD set")
	}
	if !strings.Contains(p1, "max_cost_usd: 2") {
		t.Errorf("max_cost_usd not rendered: %s", p1)
	}
	// Thresholds must be sorted ascending.
	if !strings.Contains(p1, "ask_thresholds_usd: [0.5, 1, 1.5]") {
		t.Errorf("ask thresholds not sorted: %s", p1)
	}
	if !strings.Contains(p1, "safety.ask_on_os_tools") {
		t.Error("ask_on_os_tools gate missing when os_tools requested")
	}
	if !strings.Contains(p1, "max_tool_calls_per_session") {
		t.Error("tool-call cap always present")
	}
}

func TestRenderPolicy_NoCostNoBudget(t *testing.T) {
	p := RenderPolicy(Governance{}) // no cost cap, no ask_on, writes deny
	if strings.Contains(p, "cost_budget") {
		t.Error("budget policy should be omitted when MaxCostUSD is 0")
	}
	if strings.Contains(p, "ask_on_os_tools") {
		t.Error("ask gate should be omitted when not requested and writes denied")
	}
}

func TestRenderSandbox_OfflineResearchFull(t *testing.T) {
	off := RenderSandbox(Governance{Sandbox: SandboxOffline, FSWrites: FSWritesDeny}, `X:\cases\smith`)
	if !strings.Contains(off, "egress: deny") {
		t.Error("offline sandbox must deny egress (the OS-enforced offline invariant)")
	}
	if !strings.Contains(off, `read_only:`) || !strings.Contains(off, `X:\\cases\\smith`) {
		t.Errorf("case dir not marked read-only (escaped): %s", off)
	}

	res := RenderSandbox(Governance{Sandbox: SandboxResearch, AllowHosts: []string{"b.example.com", "a.example.com"}}, "")
	if !strings.Contains(res, "egress: allow-list") {
		t.Error("research sandbox must use an egress allow-list")
	}
	// Hosts sorted deterministically.
	ai := strings.Index(res, "a.example.com")
	bi := strings.Index(res, "b.example.com")
	if ai < 0 || bi < 0 || ai > bi {
		t.Errorf("allow_hosts not sorted: %s", res)
	}

	full := RenderSandbox(Governance{Sandbox: SandboxFull, FSWrites: FSWritesFull}, "")
	if !strings.Contains(full, "egress: open") {
		t.Error("full sandbox should open egress")
	}
}

func TestRenderSandbox_OutputDirGrant(t *testing.T) {
	s := RenderSandbox(Governance{Sandbox: SandboxOffline, FSWrites: FSWritesOutputOnly, OutputDir: `X:\out`}, `X:\in`)
	if !strings.Contains(s, "read_write:") || !strings.Contains(s, `X:\\out`) {
		t.Errorf("output dir not granted RW: %s", s)
	}
}

func TestGeneratePolicy_WritesBothFiles(t *testing.T) {
	dir := t.TempDir()
	req := Request{Goal: "g", Target: `X:\cases\smith`, Governance: Governance{MaxCostUSD: 2, FSWrites: FSWritesDeny}}
	pp, sp, err := GeneratePolicy(dir, req)
	if err != nil {
		t.Fatalf("GeneratePolicy: %v", err)
	}
	for _, p := range []string{pp, sp} {
		data, err := os.ReadFile(p)
		if err != nil {
			t.Fatalf("read %s: %v", p, err)
		}
		if len(data) == 0 {
			t.Errorf("%s is empty", p)
		}
	}
}

func TestGeneratePolicy_EmptyDirDegrades(t *testing.T) {
	if _, _, err := GeneratePolicy("", Request{Goal: "g"}); err == nil {
		t.Error("expected an error (not a panic) for an empty run dir")
	}
}

// TestBuildSpec_WindowsNoSandboxDegrade proves the platform-dependent degrade: on Windows
// SandboxSupported is false with a clear note; on Linux/macOS it is true (SPEC §8).
func TestBuildSpec_WindowsNoSandboxDegrade(t *testing.T) {
	req := Request{Goal: "g", Auth: "subscription", Governance: Governance{Sandbox: SandboxOffline}}
	spec := BuildSpec(req, "agent.yaml", "manifest.json", "policy.yaml", "omnibox.yaml", t.TempDir(), "", "")
	switch runtime.GOOS {
	case "windows":
		if spec.SandboxSupported {
			t.Error("on Windows SandboxSupported must be false (no-sandbox degrade)")
		}
		if !strings.Contains(spec.SandboxNote, "NO-SANDBOX") {
			t.Errorf("Windows note should flag no-sandbox: %q", spec.SandboxNote)
		}
	default:
		if !spec.SandboxSupported {
			t.Errorf("on %s the Omnibox sandbox should be supported", runtime.GOOS)
		}
	}
	// Defaults filled.
	if spec.Backend != "cli" {
		t.Errorf("backend default = %q want cli", spec.Backend)
	}
	if spec.ServerURL != "http://localhost:6767" {
		t.Errorf("server url default = %q", spec.ServerURL)
	}
}

func TestBuildSpec_SensitiveForcesLocalOffline(t *testing.T) {
	req := Request{Goal: "g", Auth: "local", Governance: Governance{Sensitive: true, Sandbox: SandboxOffline}}
	spec := BuildSpec(req, "a", "m", "p", "s", t.TempDir(), "cli", "")
	if spec.Auth != "local" {
		t.Errorf("sensitive spec auth = %q want local", spec.Auth)
	}
	if spec.Gov.Share != SharePrivate {
		t.Errorf("sensitive spec share = %q want private", spec.Gov.Share)
	}
}

func TestRun_NilRunnerDegrades(t *testing.T) {
	res, err := Run(context.Background(), nil, OmniSpec{Goal: "g"}, nil)
	if err != nil {
		t.Fatalf("nil runner should degrade (nil err), got %v", err)
	}
	if !res.Degraded || res.DegradeReason == "" {
		t.Errorf("expected a degraded result with a reason, got %+v", res)
	}
}

func TestStubRunner_CapturesSpecAndShareURL(t *testing.T) {
	ev := []json.RawMessage{json.RawMessage(`{"type":"policy","decision":"ALLOW"}`)}
	stub := &StubRunner{Result: OmniResult{
		FinalText: "[]",
		ShareURL:  "http://localhost:6767/s/abc123",
		SessionID: "abc123",
		CostUSD:   0.41,
		Events:    ev,
	}}
	var seen int
	res, err := Run(context.Background(), stub, OmniSpec{Goal: "g", Backend: "cli"}, func(json.RawMessage) { seen++ })
	if err != nil {
		t.Fatalf("Run with stub: %v", err)
	}
	if stub.LastSpec.Goal != "g" {
		t.Error("stub did not record the spec it was given")
	}
	if seen != 1 {
		t.Errorf("onEvent called %d times, want 1", seen)
	}
	if res.ShareURL != "http://localhost:6767/s/abc123" {
		t.Errorf("share URL not surfaced: %q", res.ShareURL)
	}
}

func TestStubRunner_ContextCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	res, err := Run(ctx, &StubRunner{}, OmniSpec{Goal: "g"}, nil)
	if err == nil {
		t.Error("expected an error on a cancelled context")
	}
	if res.Stopped != "timeout" {
		t.Errorf("Stopped = %q want timeout", res.Stopped)
	}
}

func TestStubRunner_ErrSurfaces(t *testing.T) {
	stub := &StubRunner{Err: errors.New("omni not configured")}
	if _, err := Run(context.Background(), stub, OmniSpec{Goal: "g"}, nil); err == nil {
		t.Error("expected the stub error to surface")
	}
}
