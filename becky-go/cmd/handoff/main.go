// becky-handoff — the cross-platform "Get Becky Updates" button.
//
//	becky-handoff [--json] [--dry-run]
//
// It automates the cloud->local handoff that get-becky-updates.ps1 does today,
// but as a portable Go tool: fetch from origin, find the NEWEST unmerged
// claude/* branch, build + test it on this machine, then DECIDE — fast-forward-
// merge it into master and push (when it is clean, finished, and green) or hand
// it to a human with plain-language next-steps (when anything is off).
//
// This tool is deliberately ONLINE and mutating (it is the one place becky runs
// git fetch/merge/push) — becky's offline forensic tools never call it. The repo
// path defaults to the repo that contains the working dir's becky-go module;
// override with the BECKY_REPO environment variable. With --dry-run it gathers
// facts and prints the DECISION but performs no merge/push/delete.
//
// Exit codes: 0 = up-to-date or merged cleanly; 1 = a step failed or the
// decision was "hand off to a human"; 2 = usage error.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"becky-go/internal/handoff"
)

func main() {
	asJSON := flag.Bool("json", false, "emit the decision as JSON instead of plain language")
	dryRun := flag.Bool("dry-run", false, "gather facts and print the decision, but don't merge/push")
	flag.Parse()
	if flag.NArg() != 0 {
		fmt.Fprintln(os.Stderr, "usage: becky-handoff [--json] [--dry-run]")
		os.Exit(2)
	}

	repo, err := repoRoot()
	if err != nil {
		fmt.Fprintln(os.Stderr, "becky-handoff:", err)
		os.Exit(1)
	}

	decision, err := run(newGitRunner(repo), *dryRun)
	if err != nil {
		fmt.Fprintln(os.Stderr, "becky-handoff:", err)
		os.Exit(1)
	}

	if *asJSON {
		emitJSON(decision)
	} else {
		printReport(decision, *dryRun)
	}
	if decision.Action == handoff.ActionHandoff {
		os.Exit(1) // a human/assistant needs to take over
	}
}

// run orchestrates the side-effecting steps, delegating every judgement to the
// pure functions in internal/handoff. It wraps each failure with context (%w)
// and degrades to an error rather than panicking.
func run(r handoff.Runner, dryRun bool) (handoff.Decision, error) {
	if _, err := r.Run("git", "fetch", "origin", "--prune"); err != nil {
		return handoff.Decision{}, fmt.Errorf("couldn't reach GitHub (check your internet): %w", err)
	}
	refs, err := r.Run("git", "for-each-ref", "--sort=-committerdate",
		"--format=%(refname:short)", "refs/remotes/origin/claude")
	if err != nil {
		return handoff.Decision{}, fmt.Errorf("list cloud branches: %w", err)
	}
	branch := handoff.PickNewestCloudBranch(refs, func(ref string) bool {
		// notMerged: a branch is unmerged if it is NOT an ancestor of master.
		_, err := r.Run("git", "merge-base", "--is-ancestor", ref, "origin/master")
		return err != nil
	})

	in := gatherInputs(r, branch)
	decision := handoff.Decide(in)

	if decision.Action == handoff.ActionMerge && !dryRun {
		if err := installMerge(r, decision.Branch); err != nil {
			return handoff.Decision{}, err
		}
	}
	return decision, nil
}

// gatherInputs collects the facts Decide needs for a chosen branch. When no
// branch was found it returns empty inputs (Decide => up-to-date). Each git read
// degrades to a safe default (build/test/FF false) so an unreadable branch is
// handed off, never merged blindly.
func gatherInputs(r handoff.Runner, branch string) handoff.Inputs {
	if branch == "" {
		return handoff.Inputs{}
	}
	ref := "origin/" + branch
	subject, _ := r.Run("git", "log", "-1", "--format=%s", ref)
	claudeMd, _ := r.Run("git", "show", ref+":CLAUDE.md")
	buildOK, testOK := buildAndTest(r, ref)
	return handoff.Inputs{
		Branch:       branch,
		Subject:      strings.TrimSpace(subject),
		BuildOK:      buildOK,
		TestOK:       testOK,
		IsFastFwd:    isFastForward(r, ref),
		Section6Text: claudeMd,
	}
}

// isFastForward reports whether master can fast-forward to ref (i.e. master is an
// ancestor of the branch — a clean add-on with no overlap to resolve).
func isFastForward(r handoff.Runner, ref string) bool {
	_, err := r.Run("git", "merge-base", "--is-ancestor", "origin/master", ref)
	return err == nil
}

// buildAndTest verifies the branch's tree builds and tests green without leaving
// the working copy on it: it checks the ref out detached, runs build then test,
// and always returns to the previous branch. Both default to false on any error
// so a branch we can't verify is handed off, never merged.
func buildAndTest(r handoff.Runner, ref string) (buildOK, testOK bool) {
	if _, err := r.Run("git", "checkout", "--detach", ref); err != nil {
		return false, false
	}
	defer func() { _, _ = r.Run("git", "checkout", "-") }()
	if _, err := r.Run("go", "build", "./..."); err != nil {
		return false, false
	}
	if _, err := r.Run("go", "test", "./..."); err != nil {
		return true, false
	}
	return true, true
}

// installMerge performs the real install: fast-forward-merge the branch into
// master, push, and delete the merged branch (local + remote). Every git failure
// is wrapped; a non-fast-forward is already excluded by Decide.
func installMerge(r handoff.Runner, branch string) error {
	if _, err := r.Run("git", "checkout", "master"); err != nil {
		return fmt.Errorf("switch to master: %w", err)
	}
	if _, err := r.Run("git", "merge", "--ff-only", "origin/"+branch); err != nil {
		return fmt.Errorf("fast-forward merge %s: %w", branch, err)
	}
	if _, err := r.Run("git", "push", "origin", "master"); err != nil {
		return fmt.Errorf("push master to GitHub: %w", err)
	}
	_, _ = r.Run("git", "push", "origin", "--delete", branch) // tidy-up; ok if gone
	_, _ = r.Run("git", "branch", "-D", branch)
	return nil
}

// emitJSON prints the decision as indented JSON.
func emitJSON(d handoff.Decision) {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(d); err != nil {
		fmt.Fprintln(os.Stderr, "encode:", err)
		os.Exit(1)
	}
}

// printReport writes a plain-language summary for a non-developer.
func printReport(d handoff.Decision, dryRun bool) {
	fmt.Println("GET BECKY UPDATES")
	fmt.Println(strings.Repeat("=", 60))
	switch d.Action {
	case handoff.ActionUpToDate:
		fmt.Println("All caught up! " + d.Reason)
	case handoff.ActionMerge:
		fmt.Printf("Found new work: %q\n", d.Subject)
		if dryRun {
			fmt.Println("WOULD INSTALL (dry-run, nothing changed): " + d.Reason)
		} else {
			fmt.Println("Installed: " + d.Reason)
		}
	case handoff.ActionHandoff:
		fmt.Printf("Found new work: %q\n", d.Subject)
		fmt.Println("NOT installed: " + d.Reason)
		if d.NextStep != "" {
			fmt.Println(d.NextStep)
		}
	}
}

// repoRoot resolves the becky-tools repo path. BECKY_REPO wins (matching the
// PowerShell button); otherwise it walks up from the working directory to find
// the repo that contains a becky-go/go.mod, falling back to the parent of cwd.
func repoRoot() (string, error) {
	if env := strings.TrimSpace(os.Getenv("BECKY_REPO")); env != "" {
		return env, nil
	}
	wd, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("get working dir: %w", err)
	}
	for dir := wd; ; {
		if fileExists(filepath.Join(dir, "becky-go", "go.mod")) {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return filepath.Dir(wd), nil // best effort; git will report a clear error if wrong
}

func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

// gitRunner is the production Runner: it shells out to git (in the repo) and to
// go (in repo/becky-go). It is the ONLY place real commands run.
type gitRunner struct {
	repo string
}

func newGitRunner(repo string) *gitRunner { return &gitRunner{repo: repo} }

// Run executes args[0] with the rest as arguments. git runs in the repo root; go
// runs in the becky-go module dir. Combined output is returned for diagnostics.
func (g *gitRunner) Run(args ...string) (string, error) {
	if len(args) == 0 {
		return "", fmt.Errorf("no command")
	}
	cmd := exec.Command(args[0], args[1:]...) //nolint:gosec // fixed local toolchain
	if args[0] == "go" {
		cmd.Dir = filepath.Join(g.repo, "becky-go")
	} else {
		cmd.Dir = g.repo
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		return string(out), fmt.Errorf("%s: %w", strings.Join(args, " "), err)
	}
	return string(out), nil
}

// ReadFile reads a file relative to the repo root.
func (g *gitRunner) ReadFile(path string) (string, error) {
	b, err := os.ReadFile(filepath.Join(g.repo, path))
	if err != nil {
		return "", fmt.Errorf("read %s: %w", path, err)
	}
	return string(b), nil
}
