---
name: becky-cloud-handoff
description: "Cloud and local agent handoff for becky-tools: claude/* branches, draft PRs, the provable handoff, work orders, 'grab the latest cloud branch', the Get Becky Updates button, 'Left for local agent'. Cloud agents read this first."
---

# Cloud <-> local handoff

Moved verbatim out of the always-loaded CLAUDE.md files on 2026-10-10 so it is read only when this kind
of work comes up. It has the same authority as CLAUDE.md.

- **The PROVABLE HANDOFF (from `STANDARDS-WORKFLOW.md` §7 + `HANDOFF-TEMPLATE.md`).** Any cloud→local
  handoff of work needing hardware cloud can't touch (audio/GUI/GPU/device/media) is NOT "ready"
  until it ships (1) a **one-command, no-hardware proof cloud already RAN and pasted evidence for**
  (a `--render`/`--selftest`/`--dry-run` that exercises the real code path + is measurable), and (2)
  an **ordered, checkboxed work order of commands** (not prose) the local agent drives to completion.
  "It compiles" is not proof. If you can't hand over a one-command proof, you haven't finished your
  half. This is the standing fix for "I researched it and none of it got wired up."

## 6. Cloud ↔ Local handoff protocol

The two agents split the work along the **model boundary**:

| Cloud / web agent (here)                       | Local agent (Jordan's Win10 PC)            |
|------------------------------------------------|--------------------------------------------|
| Deep research, model/library selection         | Real ML inference + GPU runs               |
| Tool specs (`SPEC-*.md` in the house style)    | ffmpeg / media-dependent end-to-end tests  |
| Go scaffolding, CLI, JSON schema, fusion logic | Wiring the Python helper to the real model |
| **Unit tests** for all deterministic logic     | Accuracy/recall tuning on real evidence    |
| Push to branch + open **draft PR**             | `build-all-tools.bat`, run on real clips   |

**Rules of the baton:**
1. The cloud agent works on its assigned `claude/*` branch and opens a **draft
   PR** — it does **not** push to `master`.
2. Every Python helper the cloud agent can't run is left as a documented stub with
   an explicit input/output contract, so the local agent only has to plug in the
   model call.
3. The **live status of the current branch** lives in section 6 below. The cloud
   agent updates it before ending a session; the local agent reads it first.
4. **THE PROVABLE HANDOFF (mandatory for runtime work — audio/GUI/GPU/device/media).**
   The branch is not "ready" until cloud ships, and has RUN, a **one-command offline
   proof** of the real code path (a `--render`/`--selftest`/`--dry-run` whose output is
   measurable — ffprobe/byte-count/hash), AND an **ordered, checkboxed work order of
   commands** (`LOCAL-WORK-ORDER.md` / `HANDOFF-<topic>.md`, from `HANDOFF-TEMPLATE.md`)
   the local agent drives to completion — NOT prose, NOT "wire it up". §6 points local at
   it with a "do not merge-and-stop" banner. Full rule: `STANDARDS-WORKFLOW.md` §7.

### 7. Copy-paste prompt for the local agent

When the cloud agent has pushed a branch, Jordan pastes this into his **local**
Claude Code (filling in the branch name from the chat or the PR):

> Pull the branch `<BRANCH-NAME>` from origin and check it out (create a local
> tracking branch if needed). Read `CLAUDE.md`, `INDEX.md`, `STATE-OF-MASTER.md` and `HANDOFF-LOG.md` for what
> the cloud agent finished and what's left for you. Then: run `go build ./...` and
> `go test ./...` in `becky-go/` to confirm it's green on this machine; wire up any
> Python helper stubs listed in the handoff to their real local models; run
> `build-all-tools.bat`; and test the new/changed tool against a real clip. Report
> what passed, what degraded, and anything that needs my input. Commit to the same
> branch.

### 8. Minimal trigger — Jordan does NOT paste the long prompt

Jordan is non-dev and copy-pasting the prompt above into the local TUI is broken
and slow for him (observed 2026-06-14). So the local agent must accept a **tiny
trigger** as equivalent to the full prompt. When Jordan says anything like **"grab
the latest cloud branch"** / "pull the cloud agent's work" / "continue the
handoff", do the whole sequence automatically:

1. `git fetch origin`, then check out the **newest** `claude/*` branch.
2. Read section 6 below (what's done / what's left).
3. In `becky-go/`: `go build ./...` and `go test ./...`. (A `gofmt -l .` complaint
   that is only CRLF line-endings on Windows is cosmetic — do not let it block.)
4. If green and the branch is non-blocking, fast-forward merge into `master`,
   push, and delete the merged branch (local + remote). Otherwise report plainly.

Never make Jordan paste the long version. The only thing he should ever have to
say is the short trigger.

**One-click button (shipped 2026-06-14).** `get-becky-updates.ps1` at the repo
root performs exactly this sequence, and a Desktop shortcut ("Get Becky Updates")
runs it — so Jordan installs cloud work with a single double-click, zero typing.
It auto-installs only a clean, finished, fast-forward update whose section 6 says
**nothing** is left for the local agent; for anything else (build/test fails, not a
fast-forward, work still needed, or unsure) it launches Claude with the trigger
above instead of guessing. Honors a `BECKY_REPO` env override (used only for
testing). The queued **becky-handoff** Go tool (§6) is the eventual
cross-platform replacement for this script.
