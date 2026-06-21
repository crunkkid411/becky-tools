# Engineering Standards — mandatory for every becky-tools change

**Status: MANDATORY.** Pinned from `CLAUDE.md`. These are the engineering-discipline
rules becky agents (cloud + local) follow on every change. They were adapted from the
ACE-Step-DAW `.claude/rules` set — whose discipline is tighter than what becky had —
re-expressed in becky's terms (Go, the cloud/local handoff). Where becky already had a
rule, this points at it rather than duplicating.

This file governs *how we build*; `ARRANGEMENT-RULES.md` governs *what we build* for
music; `FORENSIC-OUTPUT-PHILOSOPHY.md` governs *how findings are reported*.

---

## 1. The five quality gates — ALL must pass before a branch is "ready"

From `becky-go/`:

1. `go build ./...` — compiles.
2. `go vet ./...` — clean.
3. `go test ./...` — every test passes.
4. `gofmt -l .` — prints nothing (CRLF-only noise on Windows is cosmetic; content must be clean).
5. `build-all-tools.bat` — the real `.exe`s Jordan runs actually build (auto-discovers `cmd/*`).

A cloud agent that can't run #5 says so plainly and leaves it as the local agent's
completion step (but #1–#4 must still pass). **"Compiles" is not "done"** — for any
GUI/audio/canvas work, the gate is the `CANVAS-NORTH-STAR.md` §2 Definition-of-Done
(window opens / ▶ Play makes sound / every button works / no freeze / screenshot), and
the cloud agent honestly hands that visual gate to local.

## 2. Test-driven, and tests that actually prove something

- **TDD cycle (mandatory):** write the failing `*_test.go` first → minimum Go to pass →
  refactor while green → commit. Table-driven, becky's house style.
- **Unit test per feature; regression test per bug.** Every fixed bug ships a test that
  **fails before the fix and passes after** — so it stays fixed. Templates already in the
  tree: `TestRetranscribe_BareSrt_NeverOverwritten` (sha256-proves the bug can't recur),
  the `beatgen.ToDrumGrid` StepTicks regression, `TestAddBass_locksToKick`.
- **Assert specific VALUES, not truthiness.** `!= nil` grounds nothing (it violates the
  "GROUND EVERY CLAIM" invariant). Assert the value: "house kick fires on steps 0/4/8/12",
  "C-E-G-C → C major conf 0.94", "bass pitch class is in the key".
- **Write the adversarial cases in the Red phase.** becky's domains: Windows `C:\` paths on
  Linux CI (`internal/pathx`), missing model/ffmpeg/audio, empty/short/clipped WAVs,
  zero-length transcripts, off-by-bar windowing. **Every degrade path gets a test** proving
  a typed error + partial result, not a panic (the "degrade, never crash" invariant is a
  test mandate, not a hope).
- Run `go test ./...` green BEFORE you start and after each logical unit. Never commit on red.

## 3. The circuit breakers (the genuinely new discipline becky lacked)

- **Max 3 auto-fix rounds on the same failure, then STOP and flag for human review.** Do not
  grind. (This is the antidote to the REAPER ping-pong / em-dash-launcher loops.)
- **After 2 failed attempts at an error, stop guessing and research it** (the deep-research
  skill / a targeted search) before trying again.
- These pair with the existing "STOP ONLY AT REAL BOUNDARIES" invariant: a real boundary
  includes "I've tried this 3 times and it's not converging."

## 4. Research depth — specific and actionable, never vague

When researching a competitor/genre/library, the output must be **specific enough to act on**:

- BAD: "trap has hi-hats." / "Ableton has Group Tracks."
- GOOD: "trap = rolling 1/16 hats with ratchets/rolls landing into beat 4, 130–150 BPM,
  808 glides on the bass." / "Ableton Group Track: nestable, folded shows sub-clip overview,
  Cmd-click multi-select, color cascades to sub-tracks."

This serves the "corroborate, then CONCLUDE — don't hedge" invariant: a vague finding is a
flood of maybes a human must sort, which is tool failure.

## 5. Branches, commits, ownership

- **Lanes stay becky's** (`COLLAB-PROTOCOL.md`): cloud on `claude/<topic>`, local owns
  `master`; one branch = one finished deliverable.
- **Claim before you build** (the work registry) — assign yourself before starting so two
  agents never edit one tool (the recurring `becky-pipeline` / `becky-freshness` collisions).
- **Conventional-commit subject prefixes:** `feat:` / `fix:` / `docs:` / `refactor:` /
  `test:` / `chore:`. Keep becky's existing commit footer (`Co-Authored-By` / `Claude-Session`).
- **Own the PR until it's merged or explicitly handed to local with §6 updated** — don't
  abandon a branch on CI failure / review comments / conflicts. "Don't end on a promise."
- A spec is required before a change spanning **3+ files or a new tool** (becky's `SPEC-*.md`
  discipline + the §5 doc map): a new capability is a new tool, not a tangle.

## 6. What's automated vs. enforced by discipline

- **Automated today:** CI (`.github/workflows/ci.yml`) runs build + test + vet + gofmt on
  Ubuntu **and** Windows for every push/PR.
- **Proposed (cheap) additions:** a local git pre-commit hook running `gofmt -l . && go vet
  ./... && go test ./...` (block on failure), and a Stop-hook running the gates. `build-all-
  tools.bat` success stays the local release gate (CI has no Windows GUI/audio/GPU).
