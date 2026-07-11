// runner.go — the deterministic, model-free half of the loop: install pip
// dependencies, run the project with a timeout, parse a Python traceback, and
// classify the error. Ported from Mark-XXXIX's _install_dependencies,
// _run_project, _try_auto_install, _parse_traceback, _classify_error,
// _has_error. No LLM call touches this file, so it is fully unit-testable and
// is exactly what --selftest exercises offline.
package main

import (
	"context"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"

	"becky-go/internal/pathx"
)

const (
	errDependency = "dependency_error"
	errSyntax     = "syntax_error"
	errImport     = "import_error"
	errRuntime    = "runtime_error"
	errNone       = "none"
)

// resolvePython finds a python interpreter on PATH once; pip installs and the
// project itself both run through the SAME interpreter so installed packages
// are visible to the run.
func resolvePython() string {
	for _, name := range []string{"python", "python3"} {
		if p, err := exec.LookPath(name); err == nil {
			return p
		}
	}
	return "python" // let it fail with a clear "not found" at run time
}

// classifyError buckets an error blob the same way the original does.
// NOTE: "importerror"/"cannot import" is checked under BOTH dependency_error
// and import_error below (ported verbatim from the source's branch order,
// including its overlap - dependency_error wins for any "importerror" text
// since that check runs first).
func classifyError(output string) string {
	low := strings.ToLower(output)

	if strings.Contains(low, "no module named") || strings.Contains(low, "modulenotfounderror") || strings.Contains(low, "importerror") {
		return errDependency
	}
	if strings.Contains(low, "syntaxerror") || strings.Contains(low, "invalid syntax") {
		return errSyntax
	}
	if strings.Contains(low, "cannot import") || strings.Contains(low, "importerror") {
		return errImport
	}
	for _, marker := range []string{
		"traceback", "exception", "error:", "nameerror", "typeerror",
		"attributeerror", "valueerror", "keyerror", "indexerror",
		"zerodivisionerror", "filenotfounderror", "permissionerror",
	} {
		if strings.Contains(low, marker) {
			return errRuntime
		}
	}
	return errNone
}

// hasError decides whether a run's combined output represents a real failure.
// A timeout is treated as "probably a long-running app" (success-shaped), and
// empty output is not an error.
func hasError(output string) bool {
	low := strings.ToLower(output)
	if strings.Contains(low, "timed out") {
		return false
	}
	if strings.TrimSpace(output) == "" {
		return false
	}
	return classifyError(output) != errNone
}

var tracebackRe = regexp.MustCompile(`(?i)File ["']([^"']+\.py)["'],\s+line\s+(\d+)`)

// parseTraceback finds the DEEPEST (last-printed) frame in a Python traceback
// that names one of the project's known files, and returns that file's
// project-relative path plus the line number. Ported from _parse_traceback:
// Python prints the outermost frame first and the actual failure point last,
// so matches are walked in reverse.
func parseTraceback(output string, knownFiles []string) (file string, line int) {
	matches := tracebackRe.FindAllStringSubmatch(output, -1)
	for i := len(matches) - 1; i >= 0; i-- {
		rawPath := matches[i][1]
		rawName := pathx.Base(rawPath)
		for _, pf := range knownFiles {
			if pathx.Base(pf) == rawName || pf == rawPath || strings.HasSuffix(rawPath, pf) {
				n, _ := strconv.Atoi(matches[i][2])
				return pf, n
			}
		}
	}
	return "", 0
}

var missingModuleRe = regexp.MustCompile(`(?i)No module named ['"]([a-zA-Z0-9_\-.]+)['"]`)

// extractMissingModule pulls the package name out of a ModuleNotFoundError,
// normalized the way pip package names usually are (underscore -> hyphen,
// drop any submodule after the first dot).
func extractMissingModule(output string) (string, bool) {
	m := missingModuleRe.FindStringSubmatch(output)
	if m == nil {
		return "", false
	}
	pkg := strings.ReplaceAll(m[1], "_", "-")
	if i := strings.Index(pkg, "."); i >= 0 {
		pkg = pkg[:i]
	}
	return pkg, pkg != ""
}

var depNameRe = regexp.MustCompile(`[>=<!]`)

// installPipDeps installs whichever of `dependencies` are not already
// present (checked with `pip show`), then a single `pip install` for the
// rest. Always returns a human-readable status string; never returns an
// error (non-fatal by design, matching the original).
func installPipDeps(dependencies []string, projectDir string) string {
	if len(dependencies) == 0 {
		return "no external dependencies"
	}
	python := resolvePython()

	var toInstall []string
	for _, dep := range dependencies {
		pkgName := strings.TrimSpace(depNameRe.Split(dep, 2)[0])
		if pkgName == "" {
			continue
		}
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		err := exec.CommandContext(ctx, python, "-m", "pip", "show", pkgName).Run()
		cancel()
		if err != nil {
			toInstall = append(toInstall, dep)
		}
	}
	if len(toInstall) == 0 {
		return "all dependencies already installed: " + strings.Join(dependencies, ", ")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	args := append([]string{"-m", "pip", "install"}, toInstall...)
	cmd := exec.CommandContext(ctx, python, args...)
	cmd.Dir = projectDir
	out, err := cmd.CombinedOutput()
	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return "dependency install timed out (non-fatal)"
		}
		return "install warning (non-fatal): " + truncateChars(string(out), 200)
	}
	return "installed: " + strings.Join(toInstall, ", ")
}

// installPipPackage installs a single package (the auto-install-on-
// ModuleNotFoundError path) and reports success/failure only.
func installPipPackage(pkg, projectDir string) bool {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, resolvePython(), "-m", "pip", "install", pkg)
	cmd.Dir = projectDir
	return cmd.Run() == nil
}

// runProject runs runCommand inside projectDir with a hard timeout, capturing
// combined stdout+stderr. A timeout is reported as likely-success (a
// long-running server/GUI), matching the original. runCommand is split on
// PLAIN whitespace (no shell quoting) - matching the original Python source's
// own `run_command.split()` - so a planned run_command with a quoted/spaced
// argument will split incorrectly; the planner is asked for simple commands
// like "python main.py" and that is the supported shape.
func runProject(runCommand, projectDir string, timeoutSec int) string {
	parts := strings.Fields(runCommand)
	if len(parts) == 0 {
		return "no run command configured"
	}
	if strings.EqualFold(parts[0], "python") {
		parts[0] = resolvePython()
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(timeoutSec)*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, parts[0], parts[1:]...)
	cmd.Dir = projectDir
	out, err := cmd.CombinedOutput()

	if ctx.Err() == context.DeadlineExceeded {
		return "timed out after " + strconv.Itoa(timeoutSec) + "s - long-running app (server/GUI) is likely working"
	}
	if err != nil {
		if _, ok := err.(*exec.ExitError); !ok {
			return "run error: " + err.Error()
		}
	}
	if len(out) == 0 {
		return "ran with no output"
	}
	return string(out)
}

// openEditorCmd best-effort launches VS Code on projectDir via PATH only (no
// hardcoded install paths - see main.go's --open-editor doc comment).
func openEditorCmd(projectDir string) error {
	code, err := exec.LookPath("code")
	if err != nil {
		return err
	}
	return exec.Command(code, projectDir).Start()
}
