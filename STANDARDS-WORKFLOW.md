# Workflow Standards — how becky agents take work from idea to merged

**Status: MANDATORY.** Pinned from `CLAUDE.md`. Adapted from the ACE-Step-DAW `AGENTS.md`
("this document is the law"), its `opsx/` spec-cycle, and its agent roster — re-expressed for
becky's two-agent (cloud + local), branch-based, file-logbook reality. Reconciles with, and does
NOT replace, `COLLAB-PROTOCOL.md` (lanes + registry) or `CLAUDE.md` §4–§6 (the handoff).

These are the disciplines becky was missing. Where becky already had the rule, this points at it.

---

## 1. Propose → preview → apply, with an explicit approval gate (the most-repeated rule)

Every mutating action previews the change and gets approval before it touches state. This is
becky's pinned **"show me, don't do it"** overlay (`CANVAS-NORTH-STAR.md`) made universal: the
canvas agent box proposes a `ctledit` batch and the human clicks ✓ before `applyArr` runs; a CLI
that changes a project supports `--dry-run`. Nothing irreversible happens without a preview.

## 2. Spec-first for anything non-trivial

A new tool, or a change spanning **3+ files**, needs a `SPEC-*.md` (or an explicit
acceptance-criteria checklist in the §6 handoff) **before** code. Each spec/handoff entry uses the
**executable-plan shape** so "compiles ≠ done" stops recurring:

> **Problem → Root Cause → Solution → Verification → Files to Touch**

"Verification" names the concrete evidence (a test asserting a value, an ffprobe line, a
screenshot), not "it should work."

## 3. The two-reviewer rule (becky was missing this)

A `claude/*` branch is "ready for local" only after **two independent passes**, both recorded in
§6:

1. **Reviewer #1 — mechanical:** `/code-review` on the diff (run the skill; don't eyeball), plus
   the five gates from `STANDARDS-ENGINEERING.md` green.
2. **Reviewer #2 — behavioral:** the local agent's verify step — `build-all-tools.bat` + the real
   run (real clip / window opens / ▶ Play makes sound), the `CANVAS-NORTH-STAR.md` Definition-of-Done.

The "Get Becky Updates" button fast-forwards a `claude/*` branch only when §6 says *"Left for local:
nothing"* AND a review pass is recorded.

## 4. Named stances (lift the discipline, not the agent zoo)

- **Reviewer stance:** run mechanical checks, never visual self-assessment; verdict is
  Approve / Changes / Reject with file:line.
- **Tester stance:** extract every MUST/SHALL from the spec, cross-reference each to a test, file
  the gaps. (becky's "GROUND EVERY CLAIM" applied to specs.)
- **Researcher stance:** competitive research at *interaction-detail* level before building a
  GUI/DAW feature, cited in `research/*.md` (becky already does this — keep it a gate).
- **Explore / assess stance:** think-only — investigate, diagram (ASCII), question assumptions,
  **never implement, never auto-capture**; offer to record findings. This IS becky's rule #5
  ("ASSESS, DON'T ACT UNINVITED").

## 5. Subagent task shape

When orchestrating build subagents (becky does this heavily), hand each the fixed skeleton
(`BUILD-AGENT-BRIEFING.md`): failing test first → minimal Go → `go build/vet/test` green →
`build-all-tools.bat` → real verification → conventional commit → **own it to completion**. One
deliverable per subagent; no unrelated refactoring; disjoint file ownership (no two subagents in
one file).

## 6. Enforcement (not just discipline)

becky enforced these only by convention; make them un-skippable:

- **Quality-gate hook (commit + Stop):** `scripts/git-hooks/pre-commit` runs, from `becky-go/`,
  `go build ./... && go vet ./... && go test ./... && gofmt -l .` and blocks on failure (CRLF-only
  gofmt noise on Windows is the documented escape). Install once with `scripts/install-hooks.sh`.
  A harness **Stop hook** running the same gate is the structural fix for "green-by-claim" — wire
  via the `update-config` skill.
- **Push-to-`master` guard:** the cloud lane never pushes `master` (a hook can enforce the
  `COLLAB-PROTOCOL` lane rule instead of trusting it).
- **Compaction-preservation:** when context compacts, preserve the modified-file paths, the active
  `claude/*` branch, the last `go build/vet/test` result, and the open §6 handoff items.

## 7. The Provable Handoff (MANDATORY for every cloud→local runtime handoff)

The recurring becky failure was NOT "cloud can't do GPU" — it was that "LEFT FOR LOCAL: wire up
X" was unverifiable prose, so it got merged-and-skipped while the easy 80% shipped. The fix, now
standard: **every handoff of work that needs hardware cloud can't touch (audio, a GUI window, the
GPU, a real device, ffmpeg/media) ships two artifacts, and the branch is not "ready" without them.**

1. **A one-command, no-hardware PROOF that cloud RAN and pasted evidence for.** Add an offline
   entry point — a `--render` / `--selftest` / `--dry-run` / `--export` flag — that exercises the
   SAME code path the GUI/device will, writes an artifact, and is measurable (ffprobe, a byte
   count, a hash, an enumerated result). Run it; paste the numbers. "It compiles" / "the model
   exists" is NOT proof. **If you can't hand over a one-command proof, you haven't finished your
   half.** (Worked example: `becky-daw-engine --render-arrangement` renders the canvas drum
   chain offline → cloud measured peak −2.1 dBFS before handing off.)
2. **An ordered, checkboxed work order — commands, not prose** (`LOCAL-WORK-ORDER.md` or
   `HANDOFF-<topic>.md`). Each step: the exact command, the expected output, DONE-WHEN (observable),
   what to paste back. No step is "wire it up" — name the files/functions to connect. The §6 entry
   points local at it with a "do NOT merge-and-stop" banner; the branch isn't done until every box
   is checked WITH evidence.

Copy `HANDOFF-TEMPLATE.md` to start. This subsumes the older "documented stub with an input/output
contract" baton rule: a stub is fine, but the path through it must be provable by one command.

## 8. What was deliberately NOT adopted

OpenSpec's `openspec` CLI, GitHub-Issues-as-task-DB, `npx tsx daw-cli` (becky's Go CLIs replace it
1:1), and the neural `generate` prompt-builder (becky is deterministic-by-doctrine —
`ARRANGEMENT-RULES.md`). becky is already ahead on one-click launchers, offline/deterministic
generation, and immutable-verb editing (their `_pushHistory` analog is `applyArr`).
