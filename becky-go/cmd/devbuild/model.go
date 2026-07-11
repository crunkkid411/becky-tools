// model.go — the three LLM-calling stages (plan / write / fix), ported from
// Mark-XXXIX's _plan_project / _write_file / _fix_files. Every call goes
// through the local warm client (see build.go's Run); a call failure is a
// plain error the caller degrades from, never a panic.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"becky-go/internal/llmlocal"
)

// fileSpec is one planned file. Field names/JSON tags match the plan JSON the
// model is asked to return.
type fileSpec struct {
	Path        string   `json:"path"`
	Description string   `json:"description"`
	Imports     []string `json:"imports"`
}

// projectPlan is the planner's full output.
type projectPlan struct {
	ProjectName  string     `json:"project_name"`
	EntryPoint   string     `json:"entry_point"`
	Files        []fileSpec `json:"files"`
	RunCommand   string     `json:"run_command"`
	Dependencies []string   `json:"dependencies"`
}

const planSystemPrompt = "You are a senior software architect. Reply with ONLY valid JSON, no markdown fences, no explanation."

// planProject asks the model for a minimal dependency-ordered file plan.
func planProject(ctx context.Context, client *llmlocal.Client, description, language string) (projectPlan, error) {
	user := fmt.Sprintf(`Create a minimal, complete file plan for this project.

Language: %s
Description: %s

Return ONLY valid JSON - no markdown, no explanation:
{
  "project_name": "snake_case_name",
  "entry_point": "main.py",
  "files": [
    {"path": "main.py", "description": "Entry point - what it does and which modules it imports", "imports": ["utils.helpers"]},
    {"path": "utils/helpers.py", "description": "Helper utilities - what functions it exposes", "imports": []}
  ],
  "run_command": "python main.py",
  "dependencies": ["requests"]
}

Rules:
1. List files in DEPENDENCY ORDER - files with no imports come first, entry point comes last.
2. "imports" lists every other project module this file imports (dot-notation, e.g. "utils.helpers").
3. Keep it minimal - only files truly needed.
4. Entry point must be in the files list.
5. Use relative paths only.
6. Standard library modules do NOT go in "dependencies".`, language, description)

	raw, err := client.Chat(ctx, planSystemPrompt, user, llmlocal.Options{MaxTokens: 1024})
	if err != nil {
		return projectPlan{}, err
	}
	var plan projectPlan
	if err := json.Unmarshal([]byte(stripFences(raw)), &plan); err != nil {
		return projectPlan{}, fmt.Errorf("planner returned invalid JSON: %w (raw: %.200s)", err, raw)
	}
	return plan, nil
}

// writeCodeMaxTokens caps a single generated file. Qwen3.5-4B's context here
// is 8192 (see build.go's NewClientCtx-equivalent - the warm client already
// requests a larger context so prompt + a full source file both fit).
const writeCodeMaxTokens = 2048

// writeFile generates one file's complete source, given the whole plan for
// context and the already-written dependency files it imports from.
func writeFile(ctx context.Context, client *llmlocal.Client, language, description string, allFiles []fileSpec, fi fileSpec, already map[string]string) (string, error) {
	var fileList strings.Builder
	for i, f := range allFiles {
		fmt.Fprintf(&fileList, "  [%d] %s: %s\n", i+1, f.Path, f.Description)
	}

	var depCtx strings.Builder
	for _, dotted := range fi.Imports {
		depPath := strings.ReplaceAll(dotted, ".", "/") + ".py"
		if code, ok := already[depPath]; ok {
			depCtx.WriteString(fmt.Sprintf("\n\n--- %s (you must import from this) ---\n%s", depPath, truncateChars(code, 2000)))
		}
	}

	system := fmt.Sprintf("You are a senior %s developer. Output ONLY raw code - no explanation, no markdown, no triple backticks. Write COMPLETE, RUNNABLE code, no placeholders, no TODOs.\n%s", language, langRules(language))

	user := fmt.Sprintf(`Project goal: %s

Complete project file structure (dependency order):
%s
%s

Write the complete code for: %s
Purpose: %s
%s

Code for %s:`, description, fileList.String(), depBlock(depCtx.String()), fi.Path, fi.Description, importNote(fi.Imports), fi.Path)

	raw, err := client.Chat(ctx, system, user, llmlocal.Options{MaxTokens: writeCodeMaxTokens})
	if err != nil {
		return "", err
	}
	return stripFences(raw), nil
}

// fixFiles asks the model to rewrite the file(s) implicated by errorOutput.
// Ported from _fix_files: pick the file named in the traceback (plus its
// importers on an import error), or the entry point if none is named.
func fixFiles(ctx context.Context, client *llmlocal.Client, errorOutput, description string, allFiles []fileSpec, fileCodes map[string]string, language, entryPoint string) (map[string]string, error) {
	knownFiles := make([]string, 0, len(fileCodes))
	for p := range fileCodes {
		knownFiles = append(knownFiles, p)
	}
	errorFile, errorLine := parseTraceback(errorOutput, knownFiles)
	errType := classifyError(errorOutput)

	var toFix []string
	if errorFile != "" {
		toFix = append(toFix, errorFile)
		if errType == errImport {
			dotted := strings.TrimSuffix(strings.ReplaceAll(errorFile, "/", "."), ".py")
			for _, fi := range allFiles {
				for _, imp := range fi.Imports {
					if imp == dotted && !contains(toFix, fi.Path) {
						toFix = append(toFix, fi.Path)
					}
				}
			}
		}
	} else {
		toFix = append(toFix, entryPoint)
	}

	updated := map[string]string{}
	for _, path := range toFix {
		current := fileCodes[path]

		var otherCtx strings.Builder
		for p, code := range fileCodes {
			if p != path && code != "" {
				fmt.Fprintf(&otherCtx, "\n--- %s ---\n%s\n", p, truncateChars(code, 1500))
			}
		}

		lineHint := ""
		if errorLine > 0 && path == errorFile {
			lineHint = fmt.Sprintf("\nError appears to be near line %d in this file.", errorLine)
		}

		var fileList strings.Builder
		for _, f := range allFiles {
			fmt.Fprintf(&fileList, "  - %s: %s\n", f.Path, f.Description)
		}

		system := "You are an expert " + language + " debugger. Output ONLY the complete fixed code - no explanation, no markdown, no backticks. Fix ALL errors visible in the error output. Keep working logic intact."
		user := fmt.Sprintf(`Project goal: %s

All project files:
%s
Other files for context (read-only):
%s

File to fix: %s%s
Error type: %s

Error output:
%s

Current (broken) code:
%s

Fixed code for %s:`, description, fileList.String(), truncateChars(otherCtx.String(), 3500), path, lineHint, errType, truncateChars(errorOutput, 2500), current, path)

		raw, err := client.Chat(ctx, system, user, llmlocal.Options{MaxTokens: writeCodeMaxTokens})
		if err != nil {
			return updated, err
		}
		updated[path] = stripFences(raw)
	}
	return updated, nil
}

func langRules(language string) string {
	switch strings.ToLower(language) {
	case "python":
		return `Python rules: type hints on every signature; docstrings on public functions/classes; if __name__ == "__main__": guard in the entry point; relative imports as "from utils.helpers import foo" matching the project structure; no implicit relative imports.`
	case "javascript", "typescript", "js", "ts":
		return `JS/TS rules: ES modules (import/export), not CommonJS; JSDoc on exported functions; handle promise rejections with try/catch in async functions.`
	default:
		return ""
	}
}

func depBlock(ctx string) string {
	if ctx == "" {
		return ""
	}
	return "\nDependencies this file must import from other project files:" + ctx
}

func importNote(imports []string) string {
	if len(imports) == 0 {
		return "This file has no project-internal imports."
	}
	return "This file imports from: " + strings.Join(imports, ", ")
}

func contains(ss []string, s string) bool {
	for _, x := range ss {
		if x == s {
			return true
		}
	}
	return false
}

func truncateChars(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "...[truncated]"
}

var fenceOpenRe = regexp.MustCompile("^```[a-zA-Z]*\r?\n?")
var fenceCloseRe = regexp.MustCompile("\r?\n?```\\s*$")

// stripFences mirrors the original's _strip_fences: models sometimes wrap
// "raw code only" output in a markdown fence anyway; strip it defensively.
func stripFences(s string) string {
	s = strings.TrimSpace(s)
	s = fenceOpenRe.ReplaceAllString(s, "")
	s = fenceCloseRe.ReplaceAllString(s, "")
	return strings.TrimSpace(s)
}
