# SPEC-HANDOFF-HARDENING — make "Get Becky Updates" never strand Jordan again

**Status:** ASSIGNED TO CLOUD (overnight task, created by local 2026-06-17).
**Owner:** cloud agent. **Lane:** new `claude/handoff-hardening-*` branch.
**Build policy:** normal offline/deterministic tool → auto-buildable (no rule-break;
see Jordan's build-approval policy). Build it, don't just spec it.

---

## Why this exists (the incident)

On 2026-06-17 Jordan double-clicked the **Get Becky Updates** button and it left him
stuck for an hour. The button (`get-becky-updates.ps1`; Go port = `cmd/handoff` +
`internal/handoff`) installs finished cloud work with zero typing. Root cause was
**three** structural limits:

1. **It installs only the single NEWEST cloud branch per click**, and only on a clean
   fast-forward. **7** `claude/*` branches had piled up → it could never catch up.
2. **Every cloud branch appends to the same two logbook files** (`CLAUDE.md` §6 +
   `COLLAB-PROTOCOL.md` work registry), so any second branch *collides on docs* even
   though the tool code never collides (each tool is its own `cmd/`+`internal/` dir).
   The button can't resolve a collision → it punts to the assistant every time.
3. **A prior interrupted assistant run left the repo mid-cherry-pick**, after which the
   button bailed on "unsaved changes" on EVERY later click — one failed run poisons all
   future clicks until a human/agent cleans up.

### Already fixed (DO NOT redo) — on master as of 2026-06-17
`.gitattributes` now sets `merge=union` on `CLAUDE.md` + `COLLAB-PROTOCOL.md`, so
append-only collisions auto-resolve (keep both sides). Problem #2's *doc* half is
solved. This spec covers the remaining three hardening items.

---

## What to build (3 requirements)

Keep the **`internal/handoff` architecture**: all side effects (git, `go build/test`,
file reads) behind the tiny `Runner` interface; every judgement call is a **pure
function** that is table-driven tested with a fake Runner (no real git). Mirror any new
logic into `get-becky-updates.ps1` **or** — strongly preferred end state — make the
`.ps1` a thin wrapper that just calls `becky-handoff`, so there is ONE source of truth.

### R1 — Drain the whole queue, not just the newest branch
- Today `PickNewestCloudBranch` returns one branch; the orchestrator installs it and
  exits. Change it to install **every** not-yet-merged `claude/*` branch in one run.
- Order **oldest-first** (commit date) so natural build order/dependencies hold.
- Per branch: merge → `go build ./...` → `go test ./...` → keep if green, else
  `reset --hard` to that branch's pre-merge base (never push a dud) → push → delete the
  merged branch. Then continue to the next branch.
- A branch whose §6 says work is left, or that fails build/test, is **skipped (left in
  place)** with a clear reason — it must NOT block the ready branches behind it.
- New pure fn: `PickAllCloudBranches(forEachRef string, notMerged func(string) bool) []string`
  (ordered, not-merged). Keep `Decide` per branch. End-of-run prints a per-branch
  summary: installed / skipped-needs-review / failed.

### R2 — Self-heal a poisoned working tree
- Today any dirty tree / in-progress op makes the button bail forever.
- At startup detect a leftover op: `.git/MERGE_HEAD`, `.git/CHERRY_PICK_HEAD`,
  `.git/rebase-merge`, `.git/rebase-apply`, or a dirty tree.
- Classify, then act:
  - **handoff residue that is recoverable** (an in-progress merge/cherry-pick the button
    itself started; or conflicts only in the two union-merged logbook files) → recover
    automatically (`merge --abort` / `cherry-pick --abort` / reset to the recorded base)
    and proceed.
  - **genuine uncommitted USER work** (changes that are not handoff artifacts) → hand to
    the assistant WITH that context. **Never silently destroy uncommitted user work.**
  - When unsure → hand off, do not auto-clean. Conservative by default.
- New pure fn: `ClassifyTreeState(porcelain string, inProgress InProgressFlags) TreeState`
  where `TreeState ∈ {Clean, HandoffResidueRecoverable, UserDirty}`. Table tests.

### R3 — Stop two cloud agents editing the same tool (enforce COLLAB R4)
- Today two branches both extended `becky-pipeline` → a real CODE conflict the button
  cannot auto-resolve (this is correctly a human/AI judgement call — that part is fine;
  the goal is to DETECT it and route just those branches to the assistant while still
  draining the rest).
- New pure fn: `OverlappingBranches(branchDirs map[string][]string) [][]string` — given
  each queued branch's touched `cmd/<tool>` / `internal/<pkg>` dirs, return the groups of
  branches that share a code dir. The orchestrator hands those specific branches to the
  assistant and installs the non-overlapping ones normally.
- Optional bonus: a registry-lint check (every `cmd/*` has a `COLLAB-PROTOCOL.md`
  registry row; no two open branches claim the same row).

---

## Constraints (becky house style)
- Deterministic, offline, **degrade-never-crash**. Same input → same output.
- Preserve the existing safety: build+test the MERGED result, roll back on any failure,
  never push a dud, never prompt in the unattended button path.
- Plain-English console output for a non-developer.
- Pure judgement functions + faked-`Runner` table tests; no real network/git in tests.

## Acceptance criteria (Definition of Done)
- `go build ./...`, `go vet ./...`, `go test ./...` green; `gofmt -l .` clean.
- New table-driven tests for `PickAllCloudBranches`, `ClassifyTreeState`,
  `OverlappingBranches` (faked Runner).
- `build-all-tools.bat` builds `becky-handoff.exe` (auto-discovered).
- A short documented manual-test recipe: seed ≥2 queued branches (one not-ready, one
  code-overlap pair) + an in-progress merge, and show drain installs the ready/
  non-overlapping ones, skips the rest with reasons, and recovers the poisoned tree.

## Files
- `becky-go/internal/handoff/handoff.go` (+ new `*_test.go`) — the pure logic.
- `becky-go/cmd/handoff/main.go` — orchestrator wiring (the real Runner).
- `get-becky-updates.ps1` — mirror, or collapse into a thin `becky-handoff` caller.
- `.gitattributes` — already done (union merge); leave as-is.

## Left for local (after cloud builds it)
Run `build-all-tools.bat`, then verify on the real Windows button with a live
multi-branch queue. The deterministic Go core is fully cloud-testable.
