// Package handoff is becky's cross-platform port of the "Get Becky Updates"
// button (get-becky-updates.ps1). It automates the cloud->local handoff: fetch
// from origin, pick the newest unmerged claude/* branch, build + test it on this
// machine, then DECIDE — either fast-forward-merge it into master (when it is
// clean, finished, and green) or hand it to a human with plain-language
// next-steps (when anything is off).
//
// The whole point of the split here is testability: every side effect (git, go
// build/test, reading CLAUDE.md) is hidden behind the tiny Runner interface, and
// the two judgement calls — "which branch?" and "merge or hand off?" — are PURE
// functions (PickNewestCloudBranch, Decide) so they are table-driven tested with
// a fake Runner and never touch git or the network.
package handoff

import (
	"strings"
)

// Runner is the seam between the pure decision logic and the real world. The
// production implementation shells out to git / go; tests inject a fake that
// returns canned output, so no test ever runs git or hits the network.
type Runner interface {
	// Run executes a command (args[0] is the program) and returns its combined
	// output. A non-nil error means a non-zero exit or a launch failure.
	Run(args ...string) (string, error)
	// ReadFile reads a file (e.g. a CLAUDE.md) as text.
	ReadFile(path string) (string, error)
}

// Action is what the tool decided to do.
type Action string

const (
	// ActionUpToDate — no unmerged cloud branch; nothing to install.
	ActionUpToDate Action = "up-to-date"
	// ActionMerge — green + finished; safe to fast-forward-merge and push.
	ActionMerge Action = "merge"
	// ActionHandoff — needs a human/assistant; do NOT merge.
	ActionHandoff Action = "handoff"
)

// Decision is the deterministic outcome of one handoff check. It is what the CLI
// renders (plain language or --json) and is fully determined by its inputs.
type Decision struct {
	Action   Action `json:"action"`
	Branch   string `json:"branch,omitempty"`    // short branch name, e.g. claude/foo
	Subject  string `json:"subject,omitempty"`   // tip commit subject, for humans
	Reason   string `json:"reason"`              // plain-language why
	NextStep string `json:"next_step,omitempty"` // what the human should do (handoff only)
}

// handoffNextStep is the single plain-language instruction we give a non-dev when
// a branch can't be auto-installed: re-run the assistant, which finishes safely.
const handoffNextStep = "Tell Claude: \"grab the latest cloud branch\" and it will finish this safely."

// Inputs is everything the pure Decide function needs. Gathering these is the
// side-effecting part (done by the orchestrator); judging them is pure.
type Inputs struct {
	Branch       string // short cloud branch name ("" => none found)
	Subject      string // tip commit subject
	BuildOK      bool   // `go build ./...` succeeded on the merged result
	TestOK       bool   // `go test ./...` succeeded on the merged result
	IsFastFwd    bool   // local master can fast-forward to the branch (no overlap)
	Section6Text string // the branch's CLAUDE.md (we scan §6 "Left for local")
}

// Decide is the heart of the tool: a pure function mapping gathered facts to one
// of three actions. Order matters — the cheapest "nothing to do" check first,
// then every reason a finished-looking branch must still be handed off, and only
// a branch that clears them all is merged. It never panics.
func Decide(in Inputs) Decision {
	if strings.TrimSpace(in.Branch) == "" {
		return Decision{
			Action: ActionUpToDate,
			Reason: "Nothing new from the cloud helper — everything it sent is already installed.",
		}
	}
	if reason, blocked := blockingReason(in); blocked {
		return Decision{
			Action:   ActionHandoff,
			Branch:   in.Branch,
			Subject:  in.Subject,
			Reason:   reason,
			NextStep: handoffNextStep,
		}
	}
	return Decision{
		Action:  ActionMerge,
		Branch:  in.Branch,
		Subject: in.Subject,
		Reason:  "Builds and all tests pass, and CLAUDE.md says nothing is left for the local agent — safe to install.",
	}
}

// blockingReason returns the first plain-language reason (if any) a finished-
// looking branch must NOT be auto-merged. Pure; kept tiny so Decide stays flat.
func blockingReason(in Inputs) (string, bool) {
	switch {
	case !in.BuildOK:
		return "It did not build cleanly on this PC, so a human/assistant should look (nothing was changed).", true
	case !in.TestOK:
		return "A test did not pass on this PC, so a human/assistant should look (nothing was changed).", true
	case !in.IsFastFwd:
		return "This update overlaps work you already have (not a fast-forward), so it needs the assistant to combine it safely.", true
	case !readyForLocal(in.Section6Text):
		return "CLAUDE.md does not say \"Left for local: nothing\" — this update still needs review or hands-on work.", true
	}
	return "", false
}

// readyForLocal scans a branch's CLAUDE.md for the §6 "Left for local agent"
// line and reports whether it says nothing is left to do. It mirrors the
// PowerShell check: find the line, and treat it as ready only when it contains
// "nothing". A missing line is NOT ready (we can't tell => hand off). Pure.
func readyForLocal(claudeMd string) bool {
	line, found := leftForLocalLine(claudeMd)
	if !found {
		return false
	}
	return strings.Contains(strings.ToLower(line), "nothing")
}

// leftForLocalLine returns the first line mentioning the "Left for local" marker
// (case-insensitive, tolerant of "Left for local" / "Left for local agent").
func leftForLocalLine(claudeMd string) (string, bool) {
	for _, raw := range strings.Split(claudeMd, "\n") {
		if strings.Contains(strings.ToLower(raw), "left for local") {
			return strings.TrimSpace(raw), true
		}
	}
	return "", false
}

// PickNewestCloudBranch parses `git for-each-ref --sort=-committerdate` output
// (newest first, one ref per line) and returns the FIRST claude/* branch that is
// not yet merged into master. notMerged reports merge status for a candidate;
// injecting it keeps this function pure and git-free for tests. The returned
// name is the short branch name with any "origin/" prefix stripped.
func PickNewestCloudBranch(forEachRef string, notMerged func(ref string) bool) string {
	for _, raw := range strings.Split(forEachRef, "\n") {
		ref := strings.TrimSpace(raw)
		if ref == "" {
			continue
		}
		short := shortBranch(ref)
		if !isCloudBranch(short) {
			continue
		}
		if notMerged(ref) {
			return short
		}
	}
	return ""
}

// shortBranch strips a leading remote prefix, leaving "claude/topic". Git refs
// always use '/', so a simple prefix trim is correct and host-OS-independent.
func shortBranch(ref string) string {
	ref = strings.TrimPrefix(ref, "refs/remotes/")
	ref = strings.TrimPrefix(ref, "origin/")
	return ref
}

// isCloudBranch reports whether a short branch name is a cloud lane (claude/*).
func isCloudBranch(short string) bool {
	return strings.HasPrefix(short, "claude/")
}
