// singleshot.go — the scriptable, non-interactive entry to becky-ask.
//
// This is a PURE ADDITION beside the interactive colored bubbletea TUI (which
// stays the untouched default — ACCESSIBILITY.md, SPEC-ASK-SINGLESHOT.md §1). It
// answers ONE request and exits, printing plain linear text (no ANSI) for a
// script/file/pipeline consumer:
//
//	becky-ask --question "can becky transcribe?"
//	becky-ask --question "transcribe this" --target clip.mp4 [--run]
//	becky-ask --image frame_0007.png --question "is there a person on screen?"
//	becky-ask --question "..." --json
//
// It does NOT fork a second brain: a text question routes through the EXISTING
// intent -> router path (route(ctx, cli, q, t)); an image question shells the
// sibling becky-vision binary (the single-tool principle). Every model boundary
// degrades, never crashes — a missing model/binary yields an honest plain answer
// and exit 0.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

// ssFlags are the single-shot flags, parsed before the TTY check in main. The
// presence of --question/--ask (or --image) is what selects single-shot mode;
// without one, becky-ask falls through to its existing TUI / no-TTY behavior.
type ssFlags struct {
	question string // --question / --ask : the request (selects single-shot)
	image    string // --image <file>     : ask ABOUT this image (routes via the VLM)
	target   string // --target <path>    : optional file/folder the question refers to
	run      bool   // --run              : execute a classified action (default: show, don't do)
	asJSON   bool   // --json             : emit one JSON object instead of plain text
	extra    []string
}

// isSingleShot reports whether an explicit single-shot flag was given. Only then
// does becky-ask take the scriptable path — never by accident.
func (f *ssFlags) isSingleShot() bool {
	return strings.TrimSpace(f.question) != "" || strings.TrimSpace(f.image) != ""
}

// parseSingleShotFlags pulls the single-shot flags out of argv WITHOUT using the
// stdlib flag package, so any leftover args (e.g. a path dragged onto the exe)
// pass through unchanged to the TUI/no-TTY paths exactly as before. Both
// "--flag value" and "--flag=value" forms are accepted; "--ask" is an alias of
// "--question". Anything unrecognized is returned in rest (treated as a dropped
// target by the consumer). Returns (flags, rest) where rest is the non-flag argv.
func parseSingleShotFlags(args []string) (*ssFlags, []string) {
	f := &ssFlags{}
	var rest []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		name, inlineVal, hasInline := splitFlag(a)
		switch name {
		case "--question", "--ask", "-question", "-ask":
			f.question = takeValue(args, &i, inlineVal, hasInline)
		case "--image", "-image":
			f.image = takeValue(args, &i, inlineVal, hasInline)
		case "--target", "-target":
			f.target = takeValue(args, &i, inlineVal, hasInline)
		case "--run", "-run":
			f.run = boolValue(inlineVal, hasInline)
		case "--json", "-json":
			f.asJSON = boolValue(inlineVal, hasInline)
		default:
			rest = append(rest, a)
		}
	}
	f.extra = rest
	return f, rest
}

// splitFlag parses "--name=value" into ("--name","value",true); a bare "--name"
// into ("--name","",false). A non-flag arg is returned with name == the arg.
func splitFlag(a string) (name, val string, hasInline bool) {
	if !strings.HasPrefix(a, "-") {
		return a, "", false
	}
	if eq := strings.IndexByte(a, '='); eq >= 0 {
		return a[:eq], a[eq+1:], true
	}
	return a, "", false
}

// takeValue returns an inline "=value" if present, else consumes the next argv as
// the value (advancing i). A missing trailing value yields "".
func takeValue(args []string, i *int, inlineVal string, hasInline bool) string {
	if hasInline {
		return inlineVal
	}
	if *i+1 < len(args) {
		*i++
		return args[*i]
	}
	return ""
}

// boolValue interprets an optional inline value for a bool flag ("--run=false").
// A bare "--run" is true.
func boolValue(inlineVal string, hasInline bool) bool {
	if !hasInline {
		return true
	}
	switch strings.ToLower(strings.TrimSpace(inlineVal)) {
	case "", "0", "false", "no", "off":
		return false
	}
	return true
}

// singleShotResult is the one structured outcome a single-shot run produces. It
// drives both the plain and JSON renderers (ssformat.go) and the exit-code
// mapping (exitCodeFor) so all three agree.
type singleShotResult struct {
	Question string   `json:"question"`
	Image    string   `json:"image,omitempty"`
	Answer   string   `json:"answer"`
	Kind     string   `json:"kind"`            // question|action|clarify|new_tool|image
	Command  []string `json:"command"`         // staged argv when kind==action, else null
	Ran      bool     `json:"ran"`             // true only when --run executed it
	Source   string   `json:"source"`          // honest provenance
	Degraded bool     `json:"degraded"`        // a model was absent and we fell back
	Error    string   `json:"error,omitempty"` // plain-language reason on degrade/usage
	exitCode int      `json:"-"`               // process exit code (not serialized)
}

// visionAsker abstracts the image-answer step so tests can inject a fake without a
// GPU/model/binary. The real implementation (siblingVisionAsker) shells the
// sibling becky-vision binary; a fake returns a canned vision.Result-shaped reply.
type visionAsker interface {
	ask(ctx context.Context, image, question string) (description string, source string, degraded bool, errMsg string)
}

// activeVisionAsker is the seam the image path uses. Tests swap it for a fake.
var activeVisionAsker visionAsker = siblingVisionAsker{}

// runSingleShot is the single-shot entry point called from main when a single-shot
// flag is present. It returns the process exit code; it prints exactly one answer
// (plain text, or one JSON object with --json) to stdout. It NEVER panics.
func runSingleShot(f *ssFlags) int {
	ctx, cancel := context.WithTimeout(context.Background(), runTimeout)
	defer cancel()

	res := buildSingleShot(ctx, f)
	emitSingleShot(os.Stdout, f, res)
	return res.exitCode
}

// buildSingleShot does all the work (no I/O of the answer) so it is fully
// unit-testable: usage validation, image-vs-text routing, the route() call, the
// optional --run execution, and exit-code mapping.
func buildSingleShot(ctx context.Context, f *ssFlags) singleShotResult {
	q := strings.TrimSpace(f.question)
	img := strings.TrimSpace(f.image)

	// Usage validation (exit 2): an empty question, or --image without --question,
	// or an --image file that does not exist.
	if img != "" {
		if q == "" {
			return usageResult(q, img, "--image needs a --question (ask, don't just describe)")
		}
		if !fileExists(img) {
			return usageResult(q, img, "image not found: "+img)
		}
		return imageSingleShot(ctx, q, img)
	}
	if q == "" {
		return usageResult(q, img, "empty --question")
	}
	return textSingleShot(ctx, f, q)
}

// textSingleShot answers a text question through the EXISTING brain: resolve the
// target, build the (nil-safe) model client, and call route(). It adds no routing
// logic of its own — it is a thin headless caller of route().
func textSingleShot(ctx context.Context, f *ssFlags, q string) singleShotResult {
	t := resolveSingleShotTarget(f)

	// Forensic intercept: a "who is in this?" / "is X on screen?" question about a dropped file
	// goes straight into becky's self-regulating engine (forensic.go) and returns ONE corroborated
	// answer — not a staged becky-identify command the agent would have to run and chain itself.
	if r, ok := forensicSingleShot(ctx, q, t); ok {
		return r
	}

	// Build the local model client exactly as the TUI does; nil-safe — when the
	// model/binary is absent, classify() degrades to the deterministic + keyword
	// catalog path with an honest note. Never hard-fails.
	cli := newLlamaClient(resolveIntentModel(), resolveLlamaServer(), nil)

	d := classify(ctx, cli, q, t)
	r := route(ctx, cli, q, t)

	res := singleShotResult{
		Question: q,
		Kind:     kindString(d.Kind),
		Source:   d.Source,
		Degraded: strings.Contains(d.Source, "unavailable") || strings.Contains(d.Source, "error"),
		exitCode: 0,
	}

	if len(r.Pending) == 0 {
		// Question / clarify / catalog answer / new-tool note: print the reply.
		res.Answer = plainAnswer(r.Reply)
		return res
	}

	// A classified ACTION on a target. Default = show, don't do.
	res.Kind = "action"
	res.Command = r.Pending
	if !f.run {
		res.Answer = commandString(r.Pending)
		return res
	}

	// --run: execute it via the existing runCommand; propagate its exit code.
	out := runCommand(ctx, r.Pending)
	res.Ran = true
	res.Answer = singleShotRunAnswer(out)
	if out.Err != nil {
		res.Error = out.Err.Error()
		res.exitCode = 1
	}
	return res
}

// imageSingleShot answers an image question by shelling the sibling becky-vision
// binary (via the visionAsker seam). It inherits becky-vision's degrade contract:
// a missing model/binary -> a plain note + exit 0.
func imageSingleShot(ctx context.Context, q, img string) singleShotResult {
	desc, source, degraded, errMsg := activeVisionAsker.ask(ctx, img, q)
	res := singleShotResult{
		Question: q,
		Image:    img,
		Kind:     "image",
		Source:   source,
		Degraded: degraded,
		exitCode: 0,
	}
	if degraded {
		res.Error = errMsg
		res.Answer = "couldn't read the image — the vision model or a file was missing"
		return res
	}
	res.Answer = strings.TrimSpace(desc)
	return res
}

// usageResult builds an exit-2 usage error result with a plain reason.
func usageResult(q, img, reason string) singleShotResult {
	return singleShotResult{
		Question: q,
		Image:    img,
		Kind:     "error",
		Answer:   reason,
		Error:    reason,
		Source:   "usage",
		exitCode: 2,
	}
}

// resolveSingleShotTarget builds the Target from --target (preferred) and any
// leftover positional args (so `becky-ask --question "transcribe this" clip.mp4`
// works), matching the TUI's argv-as-target behavior.
func resolveSingleShotTarget(f *ssFlags) Target {
	var args []string
	if strings.TrimSpace(f.target) != "" {
		args = append(args, f.target)
	}
	args = append(args, f.extra...)
	return resolveTarget(args)
}

// kindString maps a decisionKind to the JSON "kind" string in §4.2.
func kindString(k decisionKind) string {
	switch k {
	case decideAct:
		return "action"
	case decideClarify:
		return "clarify"
	case decideNewTool:
		return "new_tool"
	default:
		return "question"
	}
}

// singleShotRunAnswer renders the outcome of an executed (--run) command as plain
// text: the saved sidecar paths and/or trimmed stdout, or the error.
func singleShotRunAnswer(out runResult) string {
	var b strings.Builder
	for _, s := range out.Saved {
		b.WriteString("Saved: " + s + "\n")
	}
	if s := strings.TrimSpace(out.Stdout); s != "" {
		b.WriteString(s)
	}
	if out.Err != nil {
		if b.Len() > 0 {
			b.WriteString("\n")
		}
		b.WriteString("error: " + out.Err.Error())
	}
	if b.Len() == 0 {
		return "done (no output)"
	}
	return strings.TrimRight(b.String(), "\n")
}

// fileExists reports whether path names an existing file (not a directory).
func fileExists(path string) bool {
	fi, err := os.Stat(path)
	return err == nil && !fi.IsDir()
}

// emitSingleShot writes the answer to w in the chosen format.
func emitSingleShot(w *os.File, f *ssFlags, res singleShotResult) {
	if f.asJSON {
		out := res // copy so command-null marshals as JSON null when empty
		if out.Command == nil {
			out.Command = nil
		}
		enc := json.NewEncoder(w)
		enc.SetEscapeHTML(false)
		_ = enc.Encode(out) // newline-terminated single object
		return
	}
	fmt.Fprintln(w, formatPlain(res))
}
