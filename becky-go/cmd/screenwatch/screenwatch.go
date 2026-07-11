// screenwatch.go — the pure, testable core of becky-screenwatch: the baked-in
// winning becky-vision config, the stall-detection prompt, and Classify (the
// model's free text -> a {stalled,state,confidence,reason} verdict). main.go is
// just flag parsing + wiring; every decision lives here so it can be unit-tested
// against fixture strings WITHOUT a GPU, a model, or the llama binary present.
package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"becky-go/internal/vision"
)

// ToolName is the stable identifier emitted in every Verdict.
const ToolName = "becky-screenwatch"

// DefaultModelDir is the WINNING becky-vision config proven in
// hj-mission-control\docs\RECOVERY.md "becky-vision gate results" Test 5:
// the 1.6B LFM2.5-VL model called DIRECTLY (one fast call, no escalation
// chain, no second server spin-up) with a POINTED prompt read the frozen
// permission prompt correctly, completely, and in seconds. --qwen (4m42s) was
// rejected as too slow for a watchdog. We bake that config in here.
const DefaultModelDir = `X:/AI-2/becky-tools/models/lfm2.5-vl-1.6b/`

// DefaultPrompt is the pointed stall-detection instruction. It keeps the proven
// "read the exact text, is a question waiting" core of RECOVERY.md Test 5 and
// extends it to also name error/crash dialogs and idle screens, and asks for a
// one-word label on the first line so Classify has a strong primary signal (with
// a keyword fallback for when the tiny model ignores the format).
const DefaultPrompt = "You are a screen-stall detector. Look at the screen and read all the text on it. " +
	"On the FIRST line write exactly ONE word: " +
	"WAITING (a dialog, prompt, or question is waiting for the user to answer - a yes/no question, an OK/Cancel choice, a permission or consent request, or a text box awaiting input), " +
	"ERROR (an error, warning, or crash message box is shown), " +
	"IDLE (nothing is waiting - an empty desktop, or a normal command prompt with no pending question), " +
	"or ACTIVE (an application is running normally with nothing waiting). " +
	"Then on the next lines quote the exact prompt, question, error, or button text you can see."

// State values for a Verdict. stalled == (state is waiting_input or error_dialog).
const (
	StateWaitingInput = "waiting_input" // a modal/prompt/consent is blocking on a human answer
	StateErrorDialog  = "error_dialog"  // an error/crash/warning box is shown (blocks until dismissed)
	StateActive       = "active"        // an app is running normally, nothing waiting
	StateIdle         = "idle"          // empty desktop / bare shell prompt, nothing waiting
	StateUnknown      = "unknown"       // the model degraded or gave no usable signal
)

// Verdict is becky-screenwatch's stdout JSON: a clear stalled/not-stalled call a
// text watchdog can act on, plus what the model saw and how sure we are.
type Verdict struct {
	Tool       string  `json:"tool"`
	Image      string  `json:"image"`
	Stalled    bool    `json:"stalled"`
	State      string  `json:"state"`
	Reason     string  `json:"reason"`
	Confidence float64 `json:"confidence"`
	Model      string  `json:"model,omitempty"`
	Degraded   bool    `json:"degraded"`
	Error      string  `json:"error,omitempty"`
}

// Options configures one Watch call.
type Options struct {
	Image    string // path to the screenshot to judge (REQUIRED unless Capture)
	Capture  bool   // grab the primary/virtual display first (Windows only), then judge
	ModelDir string // model dir to discover the VL GGUF in (DefaultModelDir when empty)
	Prompt   string // the instruction (DefaultPrompt when empty)
	Bin      string // llama-mtmd-cli.exe (vision.DefaultBin when empty)
	NGL      int    // GPU layers (vision.DefaultNGL when <= 0)
	KeepShot bool   // (with Capture) keep the captured PNG instead of deleting it
}

// Watch captures/reads a screen image, runs the winning becky-vision config over
// it, and classifies the model's answer into a Verdict. It NEVER returns an error
// or panics: a missing model / binary / image, or a capture failure, folds into a
// degraded Verdict (stalled=false, state=unknown) so the caller emits valid JSON
// and exits 0 — a watchdog must never be crashed by its own eyes.
func Watch(opts Options) Verdict {
	v := Verdict{Tool: ToolName, Image: opts.Image, State: StateUnknown}

	image := opts.Image
	if opts.Capture {
		shot, err := captureScreen()
		if err != nil {
			return degrade(v, fmt.Errorf("screen capture failed: %w", err))
		}
		if !opts.KeepShot {
			defer os.Remove(shot)
		}
		image = shot
		v.Image = shot
	}
	if strings.TrimSpace(image) == "" {
		return degrade(v, fmt.Errorf("no --image given and --capture not set (need one screenshot to judge)"))
	}

	res := vision.Describe(vision.Options{
		Image:    image,
		ModelDir: firstNonEmpty(opts.ModelDir, DefaultModelDir),
		Prompt:   firstNonEmpty(opts.Prompt, DefaultPrompt),
		Bin:      opts.Bin,
		NGL:      opts.NGL,
	})
	v.Model = res.Model
	if res.Degraded {
		return degrade(v, fmt.Errorf("vision model degraded: %s", res.Error))
	}

	state, conf, reason := Classify(res.Description)
	v.State = state
	v.Stalled = state == StateWaitingInput || state == StateErrorDialog
	v.Confidence = conf
	v.Reason = reason
	return v
}

// Classify turns the vision model's free-text answer into a (state, confidence,
// reason). It is PURE and deterministic — the whole reason it lives apart from the
// model call so the fixture-string unit tests exercise it with no GPU.
//
// Field lesson (fixture gate, 2026-07-11): the tiny 1.6B model READS on-screen
// text reliably (RECOVERY.md's whole finding) but its one-word LABEL is NOT
// reliable — it reflexively stamped "WAITING" on a plain build log and on a crash
// box. So the primary signal is the BODY keywords (the actual text it transcribed
// off the screen); the label is only a tie-breaker when the body says nothing, and
// a bare "WAITING" with NO corroborating prompt text on screen is NOT a stall
// (that was fixture 3: a completed build log + a bare shell prompt).
//
// Body precedence is error > waiting > idle: a crash box (which also shows an "OK"
// button that looks like a wait signal) is named error_dialog, and a genuine
// prompt is named waiting_input only when explicit DECISION text (yes/no, proceed,
// permission, OK/Cancel) is actually on screen — never for a normal command prompt.
func Classify(text string) (state string, confidence float64, reason string) {
	lower := strings.ToLower(text)
	label := firstLineLabel(text)

	// 1. Body evidence — the transcribed on-screen text — is the trustworthy
	//    signal. Most specific first.
	if bodyState := keywordState(lower); bodyState != "" {
		return bodyState, agreeConf(label, bodyState), snippet(text)
	}

	// 2. No on-screen stall text. Fall back to the label, but a bare "WAITING"
	//    with nothing on screen to back it is the model's reflex, not a real
	//    stall — downgrade it to active (not stalled). IDLE/ACTIVE/ERROR labels
	//    are not over-emitted, so trust them.
	switch label {
	case StateErrorDialog:
		return StateErrorDialog, 0.6, snippet(text)
	case StateIdle:
		return StateIdle, 0.6, snippet(text)
	case StateActive:
		return StateActive, 0.6, snippet(text)
	case StateWaitingInput:
		return StateActive, 0.4, snippet(text)
	}

	// 3. Nothing classifiable at all — not a stall, low confidence.
	return StateActive, 0.35, snippet(text)
}

// agreeConf scores a body-keyword verdict: highest when the model's own label
// agrees, still solid when there was no label, lower when the label disagreed
// (the body text wins, but we are less sure).
func agreeConf(label, bodyState string) float64 {
	switch {
	case label == bodyState:
		return 0.85
	case label == "":
		return 0.7
	default:
		return 0.6
	}
}

// firstLineLabel reads the one-word verdict the prompt asks for on the first
// non-empty line. Tolerant of leading quotes/punctuation and a trailing dash/colon
// ("WAITING - a dialog...", "**ERROR**", "Idle.").
func firstLineLabel(text string) string {
	for _, ln := range strings.Split(text, "\n") {
		f := strings.ToLower(strings.TrimSpace(ln))
		f = strings.TrimLeft(f, "*#>-\"'` \t")
		switch {
		case f == "":
			continue
		case strings.HasPrefix(f, "waiting"):
			return StateWaitingInput
		case strings.HasPrefix(f, "error"):
			return StateErrorDialog
		case strings.HasPrefix(f, "idle"):
			return StateIdle
		case strings.HasPrefix(f, "active"):
			return StateActive
		}
		// first non-empty line was not a bare label — stop; don't scan prose.
		return ""
	}
	return ""
}

// error/waiting/idle keyword sets for the fallback classifier. Kept narrow on
// purpose: a signal must be SPECIFIC to a real stall, never a generic "waiting for
// a command" that a normal shell prompt shows.
var (
	errorKW = []string{
		"stopped working", "has crashed", "a problem caused", "error dialog",
		"error message", "error box", "exception", "fatal error", "application error",
		"has stopped", "an error occurred", "warning dialog",
	}
	waitKW = []string{
		"do you want to proceed", "do you want", "waiting for a yes", "yes/no",
		"yes or no", "(y/n)", "y/n", "permission", "consent", "1. yes", "2. yes",
		"3. no", "press any key", "ok/cancel", "ok or cancel", "proceed?",
		"click ok to", "awaiting input", "enter your", "type your", "confirm to",
		"asking the user", "prompt asking", "waiting for the user to",
	}
	idleKW = []string{
		"empty desktop", "blank desktop", "nothing is happening", "no dialog",
		"no prompt", "bare prompt", "no pending", "idle",
		"wallpaper", "empty taskbar", "command prompt with no", "ready for a command",
	}
)

// keywordState applies the fallback classifier with precedence error > waiting >
// idle, returning "" when nothing specific matched.
func keywordState(lower string) string {
	if containsAny(lower, errorKW) {
		return StateErrorDialog
	}
	if containsAny(lower, waitKW) {
		return StateWaitingInput
	}
	if containsAny(lower, idleKW) {
		return StateIdle
	}
	return ""
}

func containsAny(s string, needles []string) bool {
	for _, n := range needles {
		if strings.Contains(s, n) {
			return true
		}
	}
	return false
}

// snippet is a short, single-line "what it saw" for the Verdict.Reason: the model's
// answer collapsed to one line and clipped, so a watchdog log stays readable.
func snippet(text string) string {
	one := strings.Join(strings.Fields(text), " ")
	const max = 220
	if len(one) > max {
		return one[:max] + "..."
	}
	return one
}

func degrade(v Verdict, err error) Verdict {
	v.Degraded = true
	v.State = StateUnknown
	v.Stalled = false
	v.Confidence = 0
	v.Error = err.Error()
	return v
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

// captureScreen grabs the PRIMARY display to a temp PNG via an embedded
// PowerShell System.Drawing.CopyFromScreen block — the exact capture path proven
// in cmd\click\scripts\screenshot.ps1. The PRIMARY monitor (not the whole virtual
// multi-monitor desktop) on purpose: a 3840x1080 dual-monitor grab took the 1.6B
// model >3 min, while a single 1920x1080 primary screen reads in seconds like the
// fixtures. Windows-only; on any other OS (or if powershell is absent) it returns
// an error and Watch degrades cleanly. Returns the PNG path on success.
func captureScreen() (string, error) {
	out := filepath.Join(os.TempDir(), fmt.Sprintf("becky-screenwatch-%d.png", time.Now().UnixNano()))
	cmd := exec.Command("powershell", "-NoProfile", "-ExecutionPolicy", "Bypass", "-Command", capturePS(out))
	var errBuf strings.Builder
	cmd.Stderr = &errBuf
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("%w: %s", err, strings.TrimSpace(errBuf.String()))
	}
	if fi, err := os.Stat(out); err != nil || fi.Size() == 0 {
		return "", fmt.Errorf("capture produced no image at %s", out)
	}
	return out, nil
}

// capturePS returns the PowerShell one-block screen-grab of the PRIMARY display
// to outPath. ASCII-only, Windows PowerShell 5.1 safe.
func capturePS(outPath string) string {
	return "$ErrorActionPreference='Stop';" +
		"Add-Type -AssemblyName System.Windows.Forms;" +
		"Add-Type -AssemblyName System.Drawing;" +
		"$b=[System.Windows.Forms.Screen]::PrimaryScreen.Bounds;" +
		"$bmp=New-Object System.Drawing.Bitmap($b.Width,$b.Height);" +
		"$g=[System.Drawing.Graphics]::FromImage($bmp);" +
		"$g.CopyFromScreen($b.X,$b.Y,0,0,$bmp.Size);" +
		"$bmp.Save('" + outPath + "',[System.Drawing.Imaging.ImageFormat]::Png);" +
		"$g.Dispose();$bmp.Dispose()"
}
