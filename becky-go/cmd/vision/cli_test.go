// cli_test.go — regression tests for becky-AI-Agent-review-1.md acceptance
// criterion 8's two RESOLUTION-section "Left open" gaps, closed this tick:
//  1. the bare-usage path (no --image) silently ignored --json, printing
//     plain stderr text + exit 2 with nothing on stdout even when JSON was
//     requested.
//  2. a real processing failure (e.g. a missing --image file) returned
//     {"degraded":true,...} with no top-level "ok" field.
//
// Unlike smoke_test.go (build tag "llm"), this file needs no GPU/model and
// always runs under a plain `go test ./...` — both cases fail fast on a
// file-existence check before any model/llama-server would ever spawn (see
// internal/vision's checkInputs and internal/avlm's Ready()+os.Stat gates).
package main

import (
	"bytes"
	"encoding/json"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// buildCLIExe compiles today's cmd/vision source to a temp exe. Separate
// helper from smoke_test.go's buildVisionExe (build tag "llm") so this file
// has no name collision when both are compiled together (`go test -tags=llm`).
func buildCLIExe(t *testing.T) string {
	t.Helper()
	exePath := filepath.Join(t.TempDir(), "becky-vision-cli-test.exe")
	cmd := exec.Command("go", "build", "-o", exePath, ".")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("go build becky-vision: %v\n%s", err, out)
	}
	return exePath
}

// runCLI runs exePath with args and returns stdout/stderr/exit code (0 on a
// clean exit, matching os/exec.ExitError.ExitCode() otherwise).
func runCLI(t *testing.T, exePath string, args ...string) (stdout, stderr string, exitCode int) {
	t.Helper()
	cmd := exec.Command(exePath, args...)
	var outBuf, errBuf bytes.Buffer
	cmd.Stdout = &outBuf
	cmd.Stderr = &errBuf
	err := cmd.Run()
	stdout, stderr = outBuf.String(), errBuf.String()
	if err == nil {
		return stdout, stderr, 0
	}
	if ee, ok := err.(*exec.ExitError); ok {
		return stdout, stderr, ee.ExitCode()
	}
	t.Fatalf("run %s %v: %v", exePath, args, err)
	return "", "", -1
}

func TestBareUsageError_respectsJSONFlag(t *testing.T) {
	exePath := buildCLIExe(t)

	t.Run("no flags at all: unchanged plain stderr, exit 2", func(t *testing.T) {
		stdout, stderr, code := runCLI(t, exePath)
		if code != 2 {
			t.Errorf("exit code: got %d want 2 (documented in this package's main.go header)", code)
		}
		if strings.TrimSpace(stdout) != "" {
			t.Errorf("plain (non-json) usage error must not write to stdout, got %q", stdout)
		}
		if !strings.Contains(stderr, "usage:") {
			t.Errorf("stderr should carry the usage line, got %q", stderr)
		}
	})

	t.Run("--json with no --image: JSON envelope on stdout, exit 2", func(t *testing.T) {
		stdout, _, code := runCLI(t, exePath, "--json")
		if code != 2 {
			t.Errorf("exit code: got %d want 2", code)
		}
		var env map[string]any
		if err := json.Unmarshal([]byte(stdout), &env); err != nil {
			t.Fatalf("--json usage error should print valid JSON to stdout, got %q: %v", stdout, err)
		}
		if ok, _ := env["ok"].(bool); ok {
			t.Errorf("envelope ok should be false, got %+v", env)
		}
		if _, has := env["error"]; !has {
			t.Errorf("envelope missing \"error\" field: %+v", env)
		}
	})
}

func TestDegradedResult_carriesOKField(t *testing.T) {
	exePath := buildCLIExe(t)
	missing := filepath.Join(t.TempDir(), "does-not-exist.png")

	stdout, _, code := runCLI(t, exePath, "--image", missing, "--json")
	// internal/vision's degrade-never-crash contract is DELIBERATE and
	// suite-wide (README.md "Degrade gracefully ... exit 0"; CLAUDE.md
	// "Degrade, never crash") - this fix adds the missing "ok" field WITHOUT
	// flipping the exit code (becky-AI-Agent-review-1.md's RESOLUTION "Left
	// open" flagged that as a real behavior change needing a caller audit
	// across becky-tools/Whoretana/MissionControl first - out of scope here).
	if code != 0 {
		t.Errorf("exit code: got %d want 0 (degrade-never-crash stays intentional)", code)
	}
	var res map[string]any
	if err := json.Unmarshal([]byte(stdout), &res); err != nil {
		t.Fatalf("degrade output should be valid JSON, got %q: %v", stdout, err)
	}
	if degraded, _ := res["degraded"].(bool); !degraded {
		t.Errorf("expected degraded:true, got %+v", res)
	}
	okVal, hasOK := res["ok"]
	if !hasOK {
		t.Fatalf("envelope missing \"ok\" field entirely: %+v", res)
	}
	if ok, _ := okVal.(bool); ok {
		t.Errorf("ok should be false on a degraded result, got %+v", res)
	}
}
