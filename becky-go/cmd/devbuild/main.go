// becky-devbuild — the dev-agent auto-fix loop, WHORETANA ask #2 / buildplan
// Phase 3 slice 3, ported from Mark-XXXIX's actions/dev_agent.py. Jordan (or
// an agent) describes a small project ("a CLI that converts CSV to JSON");
// becky-devbuild plans a minimal file layout, writes each file with a LOCAL
// model, installs its dependencies, runs it, and on failure parses the
// traceback and rewrites the broken file — up to --max-attempts tries.
//
//	becky-devbuild --desc "a CLI that converts CSV to JSON" [--lang python]
//	  [--name my_project] [--dir <root>] [--timeout 30] [--max-attempts 5]
//	  [--json]
//	becky-devbuild --selftest   # offline proof of the deterministic core
//	                            # (traceback parsing, error classification,
//	                            # dependency-order sort) - no model, no network
//
// Backend differs from Mark-XXXIX on purpose: the original called Gemini
// (cloud) for planning/writing/fixing. This port uses becky's LOCAL Qwen3.5-4B
// generative model (internal/llmlocal, the same model becky-ask/becky-new-tool
// already reason with) — offline, deterministic (fixed temp/seed), Law 18(a)
// "deterministic gruntwork -> local SLMs" (code generation from a fixed plan
// is repeatable enough to count). One warm server stays resident for the whole
// plan+write+fix run so weights load once (mirrors cmd/becky-edit/model.go).
//
// Scope THIS SLICE: full plan/write/install/run/fix loop for --lang python
// (pip install, python interpreter). --lang javascript/typescript will plan
// and write files (the prompt has JS/TS-specific rules, ported from the
// original) but the automated install/run/fix loop is not wired for them yet
// — the tool says so plainly (degraded:true, a clear message) rather than
// silently reusing Python's pip like the ORIGINAL script did (a real bug in
// Mark-XXXIX: it always shells out to `pip install` regardless of language).
//
// DELIBERATELY DROPPED from the Mark-XXXIX source (AUTOPILOT.md Law 8b -
// DELETE NOTHING OF JORDAN'S. EVER. + it has no place in a becky tool):
// auto-opening VS Code (use --open-editor to opt in), and any notion of a
// fixed "sir"-flavoured spoken response (that belongs to the WHORETANA
// persona layer, not this tool).
//
// Safety: every generated project lives under its OWN fresh directory (default
// <home>\BeckyDevBuilds\<sanitized-name>, override with --dir or
// BECKY_DEVBUILD_ROOT) — this tool creates and runs code only inside that
// sandbox, never touches an existing file of Jordan's outside it. Running the
// generated code is inherent to the tool's job (it is a build-AND-verify
// loop); the only containment is the project directory plus the --timeout on
// every run. TierYellow: confirm once, reversible (nothing outside the
// sandbox is touched, nothing is deleted).
//
// Output: the JSON envelope always goes to stdout (beckyio contract); a human
// summary goes to stderr unless --json is passed. Exit codes: 0 = the project
// ended in a working state; 1 = it did not (plan/model/run failure, or the fix
// loop exhausted its attempts) - always {"ok":false,...} on stdout either way;
// 2 = usage error.
package main

import (
	"fmt"
	"os"
	"strconv"
	"strings"

	"becky-go/internal/beckyio"
	"becky-go/internal/config"
)

const usageLine = `usage: becky-devbuild --desc "<project description>" [--lang python] [--name X] [--dir PATH] [--timeout 30] [--max-attempts 5] [--open-editor] [--json]`

func main() {
	opt, asJSON, verbose, selftest, usageErr := parseArgs(os.Args[1:])

	if selftest {
		os.Exit(runSelftest())
	}
	if usageErr != "" {
		fmt.Fprintln(os.Stderr, usageLine)
		fmt.Fprintln(os.Stderr, "becky-devbuild:", usageErr)
		os.Exit(2)
	}

	logf := func(format string, a ...any) {
		if verbose {
			fmt.Fprintf(os.Stderr, format+"\n", a...)
		}
	}

	cfg := config.Load()
	res := Run(cfg, opt, logf)

	if !asJSON {
		printPlain(res)
	}
	beckyio.PrintJSON(res)
	if !res.OK {
		os.Exit(1)
	}
}

// parseArgs does a position-independent scan (the same fix cmd/notify,
// cmd/websearch and cmd/file already needed: Go's stdlib flag package stops
// parsing at the first bare argument, which would silently swallow a later
// --json into --desc's value).
func parseArgs(args []string) (opt Options, asJSON, verbose, selftest bool, usageErr string) {
	opt.Language = "python"
	opt.Timeout = 30
	opt.MaxAttempts = 5

	for i := 0; i < len(args); i++ {
		a := args[i]
		switch a {
		case "--json", "-json":
			asJSON = true
		case "--verbose", "-verbose":
			verbose = true
		case "--selftest", "-selftest":
			selftest = true
		case "--open-editor", "-open-editor":
			opt.OpenEditor = true
		case "--desc", "-desc", "--description":
			i = takeValue(args, i, &opt.Description, &usageErr, "--desc")
		case "--lang", "-lang", "--language":
			i = takeValue(args, i, &opt.Language, &usageErr, "--lang")
		case "--name", "-name":
			i = takeValue(args, i, &opt.ProjectName, &usageErr, "--name")
		case "--dir", "-dir":
			i = takeValue(args, i, &opt.RootDir, &usageErr, "--dir")
		case "--timeout", "-timeout":
			var raw string
			i = takeValue(args, i, &raw, &usageErr, "--timeout")
			if raw != "" {
				if n, err := strconv.Atoi(raw); err == nil && n > 0 {
					opt.Timeout = n
				} else {
					usageErr = "--timeout wants a positive number of seconds"
				}
			}
		case "--max-attempts", "-max-attempts":
			var raw string
			i = takeValue(args, i, &raw, &usageErr, "--max-attempts")
			if raw != "" {
				if n, err := strconv.Atoi(raw); err == nil && n > 0 {
					opt.MaxAttempts = n
				} else {
					usageErr = "--max-attempts wants a positive number"
				}
			}
		default:
			if strings.HasPrefix(a, "-") {
				usageErr = "unknown flag: " + a
			} else {
				usageErr = "unexpected argument: " + a
			}
		}
	}
	if !selftest && strings.TrimSpace(opt.Description) == "" && usageErr == "" {
		usageErr = "--desc is required"
	}
	return opt, asJSON, verbose, selftest, usageErr
}

func takeValue(args []string, i int, dst *string, usageErr *string, flag string) int {
	if i+1 >= len(args) {
		*usageErr = flag + " wants a value"
		return i
	}
	*dst = args[i+1]
	return i + 1
}

func printPlain(res Result) {
	if !res.OK {
		fmt.Fprintln(os.Stderr, "becky-devbuild:", res.Error)
		if res.LastOutput != "" {
			fmt.Fprintln(os.Stderr, "--- last output ---")
			fmt.Fprintln(os.Stderr, res.LastOutput)
		}
		return
	}
	fmt.Fprintln(os.Stderr, res.Message)
	fmt.Fprintln(os.Stderr, "project:", res.ProjectDir)
	for _, f := range res.Files {
		fmt.Fprintln(os.Stderr, " ", f)
	}
}
