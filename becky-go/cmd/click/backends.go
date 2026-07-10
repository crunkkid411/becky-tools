// backends.go - the real, side-effecting backends for becky-click: it writes the
// three embedded helper scripts into a per-run temp dir and shells out to Windows
// PowerShell (UIA + screenshot) and python (pywinauto win32), then to becky-ocr
// for the render check. Every backend DEGRADES, NEVER CRASHES: a missing
// powershell/python/becky-ocr, a bad exit, or unparseable output becomes a typed
// error the orchestration handles, not a panic. All exec here compiles fine on
// non-Windows (the commands just fail at runtime -> degrade), so the package
// stays green on CI without build tags; the pure logic is tested with fakes.
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	_ "embed"
)

//go:embed scripts/uia_action.ps1
var uiaScript string

//go:embed scripts/win32_action.py
var win32Script string

//go:embed scripts/screenshot.ps1
var shotScript string

// scriptOut is the compact JSON every helper script emits on stdout.
type scriptOut struct {
	OK      bool   `json:"ok"`
	Found   bool   `json:"found"`
	Clicked bool   `json:"clicked"`
	Method  string `json:"method"`
	Error   string `json:"error"`
	Rect    *rect  `json:"rect"`
}

// scriptResult is the parsed backend outcome, richer than located (it carries the
// clicked bit the click closures need).
type scriptResult struct {
	found   bool
	clicked bool
	rect    rect
	method  string
	err     string
}

// newDeps writes the embedded scripts into tmpDir and returns the real backends.
func newDeps(tmpDir string) (deps, error) {
	staged := map[string]string{
		"uia_action.ps1":  uiaScript,
		"win32_action.py": win32Script,
		"screenshot.ps1":  shotScript,
	}
	paths := map[string]string{}
	for name, body := range staged {
		p := filepath.Join(tmpDir, name)
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			return deps{}, fmt.Errorf("cannot stage helper script %s: %w", name, err)
		}
		paths[name] = p
	}
	uiaPath := paths["uia_action.ps1"]
	winPath := paths["win32_action.py"]
	shotPath := paths["screenshot.ps1"]

	return deps{
		tmpDir: tmpDir,
		locateUIA: func(window, name, ct string) located {
			return toLocated(uiaRun(uiaPath, window, name, ct, "locate"))
		},
		clickUIA: func(window, name, ct string) (bool, string) {
			r := uiaRun(uiaPath, window, name, ct, "click")
			return r.clicked, r.err
		},
		locateWin32: func(window, name, ct string) located {
			return toLocated(win32Run(winPath, window, name, ct, "locate"))
		},
		clickWin32: func(window, name, ct string) (bool, string) {
			r := win32Run(winPath, window, name, ct, "click")
			return r.clicked, r.err
		},
		shot: func(r rect, out string) error {
			return runShot(shotPath, r, out)
		},
		ocr:    runOCR,
		settle: func() { time.Sleep(500 * time.Millisecond) },
	}, nil
}

func toLocated(r scriptResult) located {
	return located{found: r.found, rect: r.rect, method: r.method, err: r.err}
}

// uiaRun executes the UIA PowerShell helper in the given mode and parses its JSON.
func uiaRun(scriptPath, window, name, ct, mode string) scriptResult {
	// #nosec G204 - args are our own scratch/authorized target descriptors, passed
	// as discrete argv entries to a fixed script (no shell interpolation).
	cmd := exec.Command("powershell", "-NoProfile", "-ExecutionPolicy", "Bypass",
		"-File", scriptPath, "-Window", window, "-Name", name, "-ControlType", ct, "-Mode", mode)
	return parseScript("uia", cmd)
}

// win32Run executes the pywinauto python helper in the given mode and parses its JSON.
func win32Run(scriptPath, window, name, ct, mode string) scriptResult {
	// #nosec G204 - see uiaRun.
	cmd := exec.Command("python", scriptPath, "--window", window, "--name", name,
		"--control-type", ct, "--mode", mode)
	return parseScript("win32", cmd)
}

// parseScript runs cmd, finds the JSON object line in stdout, and maps it. Any
// exec/parse failure becomes a typed error (degrade, never crash).
func parseScript(backend string, cmd *exec.Cmd) scriptResult {
	out, err := cmd.Output()
	obj, perr := lastJSONObject(out)
	if obj == nil {
		msg := "no result from " + backend + " backend"
		if perr != nil {
			msg += ": " + perr.Error()
		}
		if err != nil {
			msg += " (" + err.Error() + ")"
		}
		return scriptResult{found: false, method: backend, err: msg}
	}
	r := scriptResult{
		found:   obj.OK && obj.Found,
		clicked: obj.Clicked,
		method:  backend,
		err:     obj.Error,
	}
	if obj.Rect != nil {
		r.rect = *obj.Rect
	}
	return r
}

// lastJSONObject returns the last line of out that parses as a scriptOut object.
// PowerShell/python may print warnings before the JSON; we take the last {..} line.
func lastJSONObject(out []byte) (*scriptOut, error) {
	lines := strings.Split(strings.ReplaceAll(string(out), "\r\n", "\n"), "\n")
	var lastErr error
	for i := len(lines) - 1; i >= 0; i-- {
		line := strings.TrimSpace(lines[i])
		if !strings.HasPrefix(line, "{") {
			continue
		}
		var o scriptOut
		if err := json.Unmarshal([]byte(line), &o); err != nil {
			lastErr = err
			continue
		}
		return &o, nil
	}
	return nil, lastErr
}

// runShot invokes the screenshot helper for the given rect.
func runShot(scriptPath string, r rect, out string) error {
	// #nosec G204 - fixed script, numeric coords + our own temp output path.
	cmd := exec.Command("powershell", "-NoProfile", "-ExecutionPolicy", "Bypass",
		"-File", scriptPath,
		"-X", fmt.Sprintf("%d", r.X), "-Y", fmt.Sprintf("%d", r.Y),
		"-W", fmt.Sprintf("%d", r.W), "-H", fmt.Sprintf("%d", r.H), "-Out", out)
	if _, err := cmd.Output(); err != nil {
		return fmt.Errorf("screenshot failed: %w", err)
	}
	if _, err := os.Stat(out); err != nil {
		return fmt.Errorf("screenshot produced no file")
	}
	return nil
}

// runOCR shells out to the installed becky-ocr with --image and returns its raw
// JSON stdout as the recognized-text corpus (see verifyDecision's ponytail note).
func runOCR(pngPath string) (string, error) {
	// #nosec G204 - fixed tool name, our own temp image path.
	cmd := exec.Command("becky-ocr", "--image", pngPath)
	out, err := cmd.Output()
	if err != nil && len(out) == 0 {
		return "", fmt.Errorf("becky-ocr unavailable or failed: %w", err)
	}
	return string(out), nil
}
