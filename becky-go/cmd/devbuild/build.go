// build.go — the pure(ish) orchestration core: the plan -> write -> install ->
// run -> fix loop (ported from Mark-XXXIX's _build_project). main.go is only
// flag parsing + wiring; every decision lives here so it is unit-testable.
package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"time"

	"becky-go/internal/config"
	"becky-go/internal/llmlocal"
)

// Options is becky-devbuild's parsed input.
type Options struct {
	Description string
	Language    string // "python" (full loop) | "javascript"/"typescript" (plan+write only, this slice)
	ProjectName string
	RootDir     string // output root; a project subdir is created under it
	Timeout     int    // seconds, per run attempt
	MaxAttempts int    // fix-loop cap
	OpenEditor  bool
}

// Result is becky-devbuild's stdout JSON envelope.
type Result struct {
	OK           bool     `json:"ok"`
	Error        string   `json:"error,omitempty"`
	Degraded     bool     `json:"degraded,omitempty"`
	ProjectName  string   `json:"project_name,omitempty"`
	ProjectDir   string   `json:"project_dir,omitempty"`
	EntryPoint   string   `json:"entry_point,omitempty"`
	RunCommand   string   `json:"run_command,omitempty"`
	Language     string   `json:"language,omitempty"`
	Files        []string `json:"files,omitempty"`
	Dependencies []string `json:"dependencies,omitempty"`
	Attempts     int      `json:"attempts,omitempty"`
	AutoInstalls int      `json:"auto_installs,omitempty"`
	Working      bool     `json:"working,omitempty"`
	LastOutput   string   `json:"last_output,omitempty"`
	Message      string   `json:"message,omitempty"`
	Model        string   `json:"model,omitempty"`
}

const buildTimeout = 15 * time.Minute // whole-run ceiling: plan + N writes + fix attempts

// Run executes the full build for a validated Options. It never panics —
// every failure path returns Result{OK:false, Error:...}.
func Run(cfg config.Config, opt Options, logf func(string, ...any)) Result {
	model, _, label := cfg.Qwen()
	client := llmlocal.NewWarmClient(model, cfg.LlamaServer, logf)
	defer client.Close()
	if err := client.Available(); err != nil {
		return Result{OK: false, Degraded: true, Error: "local model unavailable: " + err.Error()}
	}

	ctx, cancel := context.WithTimeout(context.Background(), buildTimeout)
	defer cancel()

	logf("devbuild: planning project (%s)...", label)
	plan, err := planProject(ctx, client, opt.Description, opt.Language)
	if err != nil {
		return Result{OK: false, Error: "planning failed: " + err.Error(), Model: label}
	}
	if len(plan.Files) == 0 {
		return Result{OK: false, Error: "planner returned an empty file list", Model: label}
	}

	projectName := opt.ProjectName
	if projectName == "" {
		projectName = plan.ProjectName
	}
	projectName = sanitizeName(projectName)
	if projectName == "" {
		projectName = "becky_project"
	}

	root := opt.RootDir
	if root == "" {
		root = defaultRoot()
	}
	projectDir := filepath.Join(root, projectName)
	if err := os.MkdirAll(projectDir, 0o755); err != nil {
		return Result{OK: false, Error: "could not create project dir: " + err.Error(), Model: label}
	}

	entryPoint := plan.EntryPoint
	if entryPoint == "" {
		entryPoint = "main.py"
	}
	runCommand := plan.RunCommand
	if runCommand == "" {
		runCommand = "python " + entryPoint
	}

	logf("devbuild: project=%s files=%d entry=%s", projectName, len(plan.Files), entryPoint)

	// Dependency-order heuristic: files declaring fewer internal imports go
	// first (ported verbatim from the original's sort key - a shallow
	// heuristic, not a true topological sort, but it is what the source tool
	// shipped and it holds for the small flat projects this tool targets).
	sortedFiles := append([]fileSpec(nil), plan.Files...)
	sort.SliceStable(sortedFiles, func(i, j int) bool {
		return len(sortedFiles[i].Imports) < len(sortedFiles[j].Imports)
	})

	fileCodes := map[string]string{}
	var written []string
	for _, fi := range sortedFiles {
		if fi.Path == "" {
			continue
		}
		logf("devbuild: writing %s...", fi.Path)
		code, err := writeFile(ctx, client, opt.Language, opt.Description, plan.Files, fi, fileCodes)
		if err != nil {
			logf("devbuild: failed to write %s: %v", fi.Path, err)
			continue
		}
		if err := saveFile(projectDir, fi.Path, code); err != nil {
			logf("devbuild: failed to save %s: %v", fi.Path, err)
			continue
		}
		fileCodes[fi.Path] = code
		written = append(written, fi.Path)
	}
	if len(written) == 0 {
		return Result{OK: false, Error: "no project files could be written", ProjectDir: projectDir, Model: label}
	}

	base := Result{
		ProjectName:  projectName,
		ProjectDir:   projectDir,
		EntryPoint:   entryPoint,
		RunCommand:   runCommand,
		Language:     opt.Language,
		Files:        written,
		Dependencies: plan.Dependencies,
		Model:        label,
	}

	if opt.Language != "python" {
		base.OK = true
		base.Degraded = true
		base.Message = fmt.Sprintf("wrote %d file(s) for language %q; the automated install/run/fix loop only covers python this slice - run it manually", len(written), opt.Language)
		return base
	}

	if len(plan.Dependencies) > 0 {
		logf("devbuild: %s", installPipDeps(plan.Dependencies, projectDir))
	}

	if opt.OpenEditor {
		tryOpenEditor(projectDir)
	}

	return runFixLoop(ctx, client, opt, plan, projectDir, fileCodes, base, logf)
}

// runFixLoop runs the project, and on a real error asks the model to fix the
// offending file(s), up to opt.MaxAttempts times. Ported from
// Mark-XXXIX's _build_project run/fix tail.
func runFixLoop(ctx context.Context, client *llmlocal.Client, opt Options, plan projectPlan, projectDir string, fileCodes map[string]string, base Result, logf func(string, ...any)) Result {
	autoInstalls := 0
	var lastOutput string

	for attempt := 1; attempt <= opt.MaxAttempts; attempt++ {
		logf("devbuild: running project (attempt %d/%d)...", attempt, opt.MaxAttempts)
		lastOutput = runProject(base.RunCommand, projectDir, opt.Timeout)
		base.Attempts = attempt
		base.LastOutput = truncateOutput(lastOutput)

		if !hasError(lastOutput) {
			base.OK = true
			base.Working = true
			base.AutoInstalls = autoInstalls
			base.Message = fmt.Sprintf("project %q is working after %d attempt(s)", base.ProjectName, attempt)
			return base
		}

		if attempt == opt.MaxAttempts {
			break
		}

		errType := classifyError(lastOutput)
		if errType == errDependency && autoInstalls < 3 {
			if pkg, ok := extractMissingModule(lastOutput); ok {
				if installPipPackage(pkg, projectDir) {
					autoInstalls++
					logf("devbuild: auto-installed missing package %q, retrying", pkg)
					continue
				}
			}
		}

		logf("devbuild: fixing errors (type: %s)...", errType)
		updated, err := fixFiles(ctx, client, lastOutput, opt.Description, plan.Files, fileCodes, opt.Language, base.EntryPoint)
		if err != nil {
			logf("devbuild: fix step failed: %v", err)
			continue
		}
		for path, code := range updated {
			if err := saveFile(projectDir, path, code); err != nil {
				logf("devbuild: failed to save fixed %s: %v", path, err)
				continue
			}
			fileCodes[path] = code
		}
	}

	base.OK = false
	base.AutoInstalls = autoInstalls
	base.Error = fmt.Sprintf("could not get %q working after %d attempt(s)", base.ProjectName, opt.MaxAttempts)
	base.LastOutput = truncateOutput(lastOutput)
	return base
}

const maxOutputChars = 3000

func truncateOutput(s string) string {
	if len(s) <= maxOutputChars {
		return s
	}
	return s[:maxOutputChars] + " ...[truncated]"
}

var nameCleanRe = regexp.MustCompile(`[^\w\-]`)

// sanitizeName mirrors the original's re.sub(r"[^\w\-]", "_", proj_name).
func sanitizeName(name string) string {
	name = nameCleanRe.ReplaceAllString(name, "_")
	for len(name) > 0 && name[0] == '_' {
		name = name[1:]
	}
	return name
}

func defaultRoot() string {
	if v := os.Getenv("BECKY_DEVBUILD_ROOT"); v != "" {
		return v
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		home = "."
	}
	return filepath.Join(home, "BeckyDevBuilds")
}

func saveFile(projectDir, relPath, code string) error {
	full := filepath.Join(projectDir, filepath.FromSlash(relPath))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		return err
	}
	return os.WriteFile(full, []byte(code), 0o644)
}

// tryOpenEditor best-effort opens the project in VS Code via PATH (no
// hardcoded install paths - if `code` is not on PATH this is a silent no-op,
// never a failure).
func tryOpenEditor(projectDir string) {
	_ = openEditorCmd(projectDir) // errors are intentionally ignored; see doc comment
}
