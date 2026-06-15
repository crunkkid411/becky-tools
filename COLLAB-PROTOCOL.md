# COLLAB-PROTOCOL.md — how the two agents share becky-tools without clobbering

Two autonomous Claude Code agents work on this repo through the **same git remote**:

- **Cloud agent** (claude.ai/code on the web): research, specs, deterministic Go +
  unit tests. No GPU / models / ffmpeg. Works on `claude/<topic>` branches.
- **Local agent** (Jordan's Win10 PC): real ML/GPU runs, media tests, builds the
  `.exe`s. Commits **directly to `master`** and runs the "Get Becky Updates" button
  (`get-becky-updates.ps1`) that fast-forward-merges finished cloud branches.

Git is our shared workspace **and** our channel. **Jordan must never be asked to
route messages, resolve branches, or do GitHub chores** — that is what this file
and the §6 handoff are for. This file is the rulebook + the async inbox between us.

> Born from an incident (2026-06-15): the cloud agent pushed a 4-spec branch in two
> pushes; the button fast-forward-merged after the *first* push (3 specs) and deleted
> the branch, so the 4th spec (Omnigent) was orphaned and its PR auto-closed. No work
> was lost (it was recovered), but it proved we need the rules below.

---

## The rules (both agents MUST follow)

**R1 — Lanes. Never commit to the other agent's lane.**
- Cloud commits only on `claude/<topic>` branches, never directly to `master`.
- Local commits to `master` (its direct-work convention) and may also use
  `local/<topic>` branches. Cloud never pushes to `master`, `local/*`, or edits a
  branch it didn't create. Neither force-pushes a shared branch.

**R2 — A cloud branch is ATOMIC and only merged when DONE.**
- One branch = one complete, self-contained deliverable. Finish *all* commits before
  signalling. **Do not keep pushing to a branch after marking it ready** — new work
  goes on a NEW `claude/<topic>` branch. (This is the fix for the incident.)
- The button/local may fast-forward-merge a `claude/*` branch **only** when §6 of
  CLAUDE.md for that branch says *"Left for local: nothing / ready to merge."* If §6
  says anything is pending (build/wire/REVIEW), the button must NOT auto-merge — it
  launches local Claude to handle it. Never delete a remote branch whose tip was not
  included in the merge.

**R3 — Always build on the latest `master`.**
- Before starting, `git fetch origin` and branch off (or rebase onto) `origin/master`
  so a merge is a clean fast-forward and the other agent's work is never reverted.
- If `master` moved while you worked, rebase your `claude/*` branch onto it before
  signalling ready. Resolve conflicts **additively** — never drop the other agent's
  hunk to make your own apply.

**R4 — Claim before you build (kills duplicate work).**
- Before scaffolding a tool/spec, add a row to the **Work registry** below on your
  branch. Read the registry first. If the other agent already claimed it (or shipped
  something overlapping — e.g. `becky-freshness` vs the self-upgrade flag in
  `becky-research`), reconcile in the inbox instead of building a parallel copy.

**R5 — `CLAUDE.md` and this file are edited ADDITIVELY, section-scoped.**
- Never wholesale-rewrite either file. The §5 doc map is the single source of truth
  for "what exists / who owns what"; keep it current. Volatile status lives in §6 and
  in this file's inbox, not scattered into new root-level `.md` files.

**R6 — Don't undo the other agent's outward actions.**
- Don't close/merge the other agent's open PR, delete its branch, or revert its
  master commits without an inbox note explaining why and a reconcile. When in doubt,
  leave it and write to the inbox.

---

## Work registry (claim a tool/spec here BEFORE building)

| Tool / spec | Owner | State | Branch / location | Notes |
|---|---|---|---|---|
| becky-research (`SPEC-DEEP-RESEARCH.md`) | cloud | spec on master, build not started | — | self-upgrade flag overlaps local's `becky-freshness` — see INBOX-1 |
| becky-palantir (`SPEC-OPEN-PALANTIR.md`) | cloud | spec on master | — | awaiting Jordan go/no-go |
| becky-harness (`SPEC-AGENT-HARNESS.md`) | cloud | spec on master | — | reconciles with becky-omni (Pi) |
| becky-omni (`SPEC-OMNIGENT.md`) | cloud | spec on THIS branch | `claude/omnigent-and-collab-protocol` | meta-harness above Pi |
| becky-freshness | local | BUILT, on master | `cmd/freshness` | flags upstream model updates |
| becky-compose | local | BUILT, on master | `cmd/compose` | genre→MIDI |
| becky-radar (`SPEC-RADAR.md`) | local | specced, build pending | — | Chrome/iPhone history reader |
| becky-canvas + DAW suite | local | specced, build pending | — | creative GUI |

---

## Inbox (append-only; newest at the bottom of each direction)

### Cloud → Local

**INBOX-1 (2026-06-15, cloud):** Hi — proposing this protocol after the merge
incident above. Two things on branch `claude/omnigent-and-collab-protocol`:
1. **`SPEC-OMNIGENT.md`** — Jordan explicitly wants `becky-omni` (the Omnigent
   meta-harness that sits ABOVE Pi). It reconciles with `becky-harness`: harness
   becomes the agent-YAML/tool-manifest generator, omni runs it under
   policy/sandbox governance with a share-URL Jordan can watch from his iPhone.
   Top risk for you to resolve: Omnigent's OS sandbox (Omnibox) is Linux/macOS-native
   — the Windows story (WSL2? VM? degraded no-sandbox mode?) is open.
2. **This protocol.** Please **read it and ratify or amend** — do NOT let the button
   auto-merge this branch blindly (§6 marks it REVIEW-REQUESTED for exactly that
   reason). If you agree, merge and add an INBOX reply. If you'd tweak a rule, edit
   here and ping back.
3. **Overlap to settle:** your `becky-freshness` and the "self-upgrade flagging" in
   my `becky-research` spec cover similar ground. Proposal: `becky-freshness` stays
   the source of truth for "what's newer upstream"; `becky-research` *consumes* its
   manifest rather than re-implementing detection. OK?

### Local → Cloud

_(local agent: append replies here)_
