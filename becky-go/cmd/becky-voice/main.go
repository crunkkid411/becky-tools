// Command becky-voice is the deterministic core of becky-whoretana: the
// always-on voice driver (HANDOFF-BECKY-VOICE.md Phase 1 Step 1.1).
//
// It reads a NDJSON intent stream from stdin and writes NDJSON events to
// stdout. The realtime model (Phase 3) feeds the intents; here every intent
// is pure Go — no mic, no model, no network.
//
// Usage:
//
//	becky-voice                  # NDJSON loop (stdin → stdout)
//	becky-voice --selftest       # scripted assertions, exits 0 on PASS
//	becky-voice gen-responses    # write responses.json to ./responses.json
//	becky-voice gen-responses --out /path/responses.json
//
// STDIN schema (one JSON object per line):
//
//	{"type":"intent","text":"transcribe this","target":"/file.mp4","id":"t1"}
//	{"type":"confirm","id":"t2"}
//	{"type":"cancel","id":"t2"}
//	{"type":"set_pack","pack":"reaper","id":"t3"}
//
// STDOUT schema (one JSON object per line):
//
//	{"type":"result","id":"t1","text":"Done.","clip":"becky-transcribe.ok.1",
//	 "tool":"becky-transcribe","argv":["becky-transcribe","/file.mp4"],
//	 "tier":"green","action":"run"}
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"becky-go/internal/catalog"
	"becky-go/internal/pack"
	"becky-go/internal/voiceresp"
	"becky-go/internal/voicerules"
	"becky-go/internal/workflowdef"
)

// --- NDJSON wire types (FIXED SCHEMA — GUI field names must match exactly) ---

// IntentMsg is one line of the NDJSON intent stream from the mic/model/GUI.
type IntentMsg struct {
	Type    string `json:"type"`             // "intent"|"confirm"|"cancel"|"set_pack"
	Text    string `json:"text,omitempty"`   // utterance text
	Target  string `json:"target,omitempty"` // optional file/folder path
	Pack    string `json:"pack,omitempty"`   // pack name for set_pack
	Confirm bool   `json:"confirm,omitempty"`
	ID      string `json:"id,omitempty"`
}

// EventMsg is one line of the NDJSON event stream written to stdout.
type EventMsg struct {
	Type        string   `json:"type"`             // "reply"|"need_confirm"|"refused"|"result"|"error"
	ID          string   `json:"id,omitempty"`     // echoed intent id
	Text        string   `json:"text"`             // spoken line from voiceresp
	Clip        string   `json:"clip,omitempty"`   // "tool.outcome.index" deterministic clip id
	Tool        string   `json:"tool,omitempty"`   // becky-tool verb or ""
	Argv        []string `json:"argv,omitempty"`   // exact argv that ran/would run
	Tier        string   `json:"tier,omitempty"`   // "green"|"yellow"|"red"
	Action      string   `json:"action,omitempty"` // "run"|"await_confirm"|"refused"|"none"
	NeedConfirm bool     `json:"need_confirm,omitempty"`
}

// --- Router ---

type pendingConfirm struct {
	id   string
	tool string
	argv []string
	tier catalog.Tier
}

type execResult struct {
	exitCode int
	stdout   string
}

// Router is the stateful voice driver. All methods are sequential (one goroutine).
type Router struct {
	rules      voicerules.Rules
	respMap    voiceresp.Map
	chooser    *voiceresp.Chooser
	activePack pack.Pack
	lastTool   string // updated after each successful execute
	pending    *pendingConfirm
	counter    int
	out        *bufio.Writer
	audit      *bufio.Writer
	execFn     func(ctx context.Context, argv []string) execResult
}

// NewRouter builds a Router with the default pack and safe default rules.
func NewRouter(out, audit *bufio.Writer) *Router {
	respMap := voiceresp.Generate()
	r := &Router{
		rules:      voicerules.Default(),
		respMap:    respMap,
		chooser:    voiceresp.NewChooser(respMap),
		activePack: pack.DefaultPack(),
		out:        out,
		audit:      audit,
	}
	r.execFn = r.realExec
	return r
}

// UseStubExec replaces real shell-out with a no-op stub (exit 0, no binary needed).
// Called by --selftest so all assertions run without any real becky-*.exe.
func (r *Router) UseStubExec() {
	r.execFn = func(_ context.Context, _ []string) execResult {
		return execResult{exitCode: 0, stdout: "selftest-stub"}
	}
}

// Route processes one intent message and returns the event to emit. It does
// NOT write to stdout — the caller (runLoop or selftest) does that.
func (r *Router) Route(ctx context.Context, msg IntentMsg) EventMsg {
	switch msg.Type {
	case "set_pack":
		return r.handleSetPack(msg)
	case "cancel":
		r.pending = nil
		return EventMsg{Type: "reply", ID: msg.ID, Text: "Cancelled.", Action: "none"}
	case "confirm":
		return r.handleConfirm(ctx, msg)
	default:
		return r.handleIntent(ctx, msg)
	}
}

func (r *Router) handleSetPack(msg IntentMsg) EventMsg {
	name := strings.TrimSpace(msg.Pack)
	if name == "" {
		name = strings.TrimSpace(msg.Text)
	}
	p, err := pack.Load(name)
	if err != nil {
		r.logAudit(fmt.Sprintf("set_pack %q failed: %v", name, err))
		return EventMsg{Type: "error", ID: msg.ID,
			Text: fmt.Sprintf("Don't know a pack named %q.", name), Action: "none"}
	}
	r.activePack = p
	r.logAudit(fmt.Sprintf("pack switched to %q (%d tools)", p.Name, len(p.Tools)))
	return EventMsg{Type: "reply", ID: msg.ID,
		Text: fmt.Sprintf("Switched to %s pack. Ready.", p.Name), Action: "none"}
}

func (r *Router) handleConfirm(ctx context.Context, msg IntentMsg) EventMsg {
	if r.pending == nil {
		return EventMsg{Type: "error", ID: msg.ID,
			Text: "Nothing pending to confirm.", Action: "none"}
	}
	p := r.pending
	r.pending = nil
	return r.execute(ctx, msg.ID, p.tool, p.argv, p.tier)
}

func (r *Router) handleIntent(ctx context.Context, msg IntentMsg) EventMsg {
	text := strings.TrimSpace(msg.Text)
	lower := strings.ToLower(text)

	// "fix it" — route to the repair verb for the last tool.
	if isFixIntent(lower) {
		return r.handleFix(msg)
	}

	// Workflow phrase — "process this video", "do the usual", etc.
	if recipe, ok := r.matchWorkflow(text); ok {
		return r.runWorkflow(ctx, msg, recipe)
	}

	// Catalog match filtered to the active pack.
	cap, ok := r.matchInPack(text)
	if !ok {
		r.logAudit(fmt.Sprintf("%q -> no match in pack %q", text, r.activePack.Name))
		return EventMsg{Type: "error", ID: msg.ID,
			Text:   "Didn't catch that — try saying the tool name or 'what can you do'.",
			Action: "none"}
	}

	argv := buildArgv(cap.Verb, msg.Target)

	// Effective tier: most restrictive of rules tier (catalog + rules overrides)
	// and pack tier override.
	tier := r.rules.TierFor(cap.Verb)
	if packTier := r.activePack.TierFor(cap.Verb); tierOrder(packTier) > tierOrder(tier) {
		tier = packTier
	}

	dec := r.rules.GateAction(cap.Verb, false)
	r.logAudit(fmt.Sprintf("%q -> %s tier=%s allowed=%v needConfirm=%v",
		text, cap.Verb, tier, dec.Allowed, dec.NeedConfirm))

	if !dec.Allowed {
		return EventMsg{Type: "refused", ID: msg.ID,
			Text: r.pickLine(cap.Verb, voiceresp.OutcomeError),
			Tool: cap.Verb, Argv: argv, Tier: string(tier), Action: "refused"}
	}

	// Confirm required for YELLOW (once) and RED (always). Pack overrides can tighten.
	needConfirm := dec.NeedConfirm || tier == catalog.TierYellow || tier == catalog.TierRed
	if needConfirm && !msg.Confirm {
		r.pending = &pendingConfirm{id: msg.ID, tool: cap.Verb, argv: argv, tier: tier}
		return EventMsg{
			Type: "need_confirm", ID: msg.ID,
			Text: fmt.Sprintf("That's a %s action — run %s? Say confirm to proceed.", tier, cap.Verb),
			Tool: cap.Verb, Argv: argv, Tier: string(tier),
			Action: "await_confirm", NeedConfirm: true,
		}
	}

	return r.execute(ctx, msg.ID, cap.Verb, argv, tier)
}

func (r *Router) handleFix(msg IntentMsg) EventMsg {
	fixVerb := r.respMap.FixVerb(r.lastTool)
	argv := buildArgv(fixVerb, msg.Target)
	r.logAudit(fmt.Sprintf("fix_verb(%q) -> %s", r.lastTool, fixVerb))
	r.counter++
	return EventMsg{
		Type: "reply", ID: msg.ID,
		Text: fmt.Sprintf("On it — deploying %s.", fixVerb),
		Clip: fmt.Sprintf("%s.ok.%d", fixVerb, r.counter),
		Tool: fixVerb, Argv: argv,
		Tier: string(catalog.TierGreen), Action: "run",
	}
}

func (r *Router) runWorkflow(ctx context.Context, msg IntentMsg, recipe workflowdef.Recipe) EventMsg {
	r.logAudit(fmt.Sprintf("%q -> workflow %q", msg.Text, recipe.Name))
	facts := workflowdef.Facts{}
	if msg.Target != "" {
		facts["target_set"] = 1
	}
	results := recipe.Run(facts, func(step workflowdef.Step, _ workflowdef.Facts) (string, error) {
		res := r.execFn(ctx, buildArgv(step.Name(), msg.Target))
		if res.exitCode != 0 {
			return "", fmt.Errorf("exit %d", res.exitCode)
		}
		return res.stdout, nil
	})
	names := workflowdef.ExecutedNames(results)
	r.counter++
	return EventMsg{
		Type: "reply", ID: msg.ID,
		Text: fmt.Sprintf("Running %s recipe: %s.", recipe.Name, strings.Join(names, ", ")),
		Clip: fmt.Sprintf("%s.ok.%d", recipe.Name, r.counter),
		Tool: recipe.Name, Action: "run",
		Tier: string(catalog.TierGreen),
	}
}

func (r *Router) execute(ctx context.Context, id, tool string, argv []string, tier catalog.Tier) EventMsg {
	r.lastTool = tool
	res := r.execFn(ctx, argv)
	outcome := voiceresp.OutcomeOK
	if res.exitCode != 0 {
		outcome = voiceresp.OutcomeError
	}
	r.counter++
	return EventMsg{
		Type: "result", ID: id,
		Text: r.pickLine(tool, outcome),
		Clip: fmt.Sprintf("%s.%s.%d", tool, string(outcome), r.counter),
		Tool: tool, Argv: argv,
		Tier: string(tier), Action: "run",
	}
}

// matchInPack returns the first catalog match that the active pack offers.
func (r *Router) matchInPack(text string) (catalog.Capability, bool) {
	for _, h := range catalog.MatchCapabilities(text) {
		if r.activePack.Offers(h.Verb) {
			return h, true
		}
	}
	return catalog.Capability{}, false
}

func (r *Router) matchWorkflow(text string) (workflowdef.Recipe, bool) {
	recipe, err := workflowdef.ProcessVideo()
	if err != nil {
		return workflowdef.Recipe{}, false
	}
	return recipe, recipe.Matches(text)
}

func (r *Router) pickLine(tool string, o voiceresp.Outcome) string {
	if line, ok := r.chooser.Choose(tool, o); ok && line != "" {
		return line
	}
	if o == voiceresp.OutcomeOK {
		return fmt.Sprintf("Done — %s finished.", tool)
	}
	return fmt.Sprintf("Ah shit, %s broke.", tool)
}

func (r *Router) emit(e EventMsg) {
	b, _ := json.Marshal(e)
	r.out.Write(b)
	r.out.WriteByte('\n')
	r.out.Flush()
}

func (r *Router) logAudit(line string) {
	if r.audit != nil {
		fmt.Fprintln(r.audit, line)
		r.audit.Flush()
	}
}

// --- pure helpers ---

func isFixIntent(lower string) bool {
	return lower == "fix it" || lower == "fix" || lower == "fix that" ||
		strings.HasPrefix(lower, "fix it ")
}

func buildArgv(verb, target string) []string {
	if target != "" {
		return []string{verb, target}
	}
	return []string{verb}
}

func tierOrder(t catalog.Tier) int {
	switch t {
	case catalog.TierRed:
		return 2
	case catalog.TierYellow:
		return 1
	default:
		return 0
	}
}

// --- real shell-out (bypassed in selftest mode) ---

// binPathFor resolves verb(.exe) next to the running binary, then ./bin/ —
// mirrors cmd/ask/run.go so becky-voice finds the same tool binaries.
func binPathFor(verb string) (string, error) {
	name := verb
	if runtime.GOOS == "windows" && !strings.HasSuffix(name, ".exe") {
		name += ".exe"
	}
	var dirs []string
	if exe, err := os.Executable(); err == nil {
		dirs = append(dirs, filepath.Dir(exe))
	}
	if wd, err := os.Getwd(); err == nil {
		dirs = append(dirs, wd, filepath.Join(wd, "bin"))
	}
	for _, d := range dirs {
		cand := filepath.Join(d, name)
		if fi, err := os.Stat(cand); err == nil && !fi.IsDir() {
			return cand, nil
		}
	}
	return "", fmt.Errorf("%s not found next to becky-voice (build it into bin/)", name)
}

func (r *Router) realExec(ctx context.Context, argv []string) execResult {
	if len(argv) == 0 {
		return execResult{exitCode: 1, stdout: "empty argv"}
	}
	bin, err := binPathFor(argv[0])
	if err != nil {
		return execResult{exitCode: 1, stdout: err.Error()}
	}
	c := exec.CommandContext(ctx, bin, argv[1:]...)
	out, _ := c.Output()
	code := 0
	if c.ProcessState != nil {
		code = c.ProcessState.ExitCode()
	}
	return execResult{exitCode: code, stdout: string(out)}
}

// --- NDJSON loop ---

func runLoop(r *Router) {
	ctx := context.Background()
	sc := bufio.NewScanner(os.Stdin)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var msg IntentMsg
		if err := json.Unmarshal([]byte(line), &msg); err != nil {
			r.emit(EventMsg{Type: "error",
				Text: fmt.Sprintf("parse error: %v", err), Action: "none"})
			continue
		}
		r.emit(r.Route(ctx, msg))
	}
}

// --- entry point ---

func main() {
	if len(os.Args) >= 2 {
		switch os.Args[1] {
		case "--selftest":
			os.Exit(runSelfTest())
		case "gen-responses":
			genResponses()
			return
		}
	}
	out := bufio.NewWriter(os.Stdout)
	audit := bufio.NewWriter(os.Stderr)
	runLoop(NewRouter(out, audit))
}

func genResponses() {
	outPath := "responses.json"
	args := os.Args[2:]
	for i := 0; i+1 < len(args); i++ {
		if args[i] == "--out" {
			outPath = args[i+1]
			break
		}
	}
	m := voiceresp.Generate()
	b, err := m.JSON()
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	if err := os.WriteFile(outPath, b, 0o644); err != nil {
		fmt.Fprintf(os.Stderr, "write %s: %v\n", outPath, err)
		os.Exit(1)
	}
	fmt.Printf("wrote %s (%d bytes, %d tools)\n", outPath, len(b), len(m))
}
