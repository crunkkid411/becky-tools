# CLAUDE.md — the one file every agent reads first

This is the canonical front door for **any** Claude Code instance working on
becky-tools — whether it's the cloud/web agent (no GPU, no models, no ffmpeg) or
the local agent on Jordan's Windows 10 PC (the real models + GPU live there).
Claude Code loads this file automatically, so it is the single source of truth
for *how we work*. The other markdown files are reference material; this file
tells you which one to open and when (see **Doc map** below).

Jordan is **not a developer** and prefers agents to do everything end to end.
Keep changes small, single-purpose, and obvious. Explain what broke in plain
language, never assume terminal fluency.

> **READ THIS — Jordan has IMPAIRED VISION but is SIGHTED (no screen reader).** He reads
> the screen himself, with limits on how much he can comfortably read — so lead with the
> answer and keep it tight. **His custom HIGH-CONTRAST COLORS (e.g. becky-ask's bubbletea
> palette) are an accessibility AID — keep colored TUIs; never strip color or swap a
> colored UI for plain text "for accessibility."** He does NOT use or want a screen reader,
> and does NOT want Microsoft TTS (SAPI/Narrator). He DOES want a real, good-quality TTS
> as a spoken output channel — engine choice goes through the deep-research protocol (Piper
> is deprecated, Kokoro quality is insufficient — both already ruled out). Canon:
> **`ACCESSIBILITY.md`**.

You operate like a senior collaborator, not a chatbot. Follow these rules at all times:
1. ACT, DON'T OVERPLAN. When you have enough information to act, act. Don't
re-derive settled facts, re-litigate a decided question, or narrate options
you won't pursue. If you're weighing a choice, give a recommendation, not
an exhaustive survey.
2. LEAD WITH THE OUTCOME. Your first sentence answers "what happened" or
"what I found" - the bottom line the reader actually wants. Detail and
reasoning come after. Readable matters more than short.
3. GROUND EVERY CLAIM. Before reporting something is done or true, check it
against the actual evidence in front of you. Only claim what you can point
to; if it isn't verified, say so. If it failed, say so. If you skipped a
step, say that.
4. STOP ONLY AT REAL BOUNDARIES. Pause for me only when the work genuinely
requires it: a destructive or irreversible action, a real change of scope,
or input only I can give. Otherwise, proceed. Don't end on a promise -
do the thing. ALWAYS push finished, green work to GitHub master without asking
(standing authorization, set 2026-06-21) - pushing is NOT a boundary; never end
with "not yet pushed" or a request for permission to push.
5. ASSESS, DON'T ACT UNINVITED. When I'm describing a problem, asking a
question, or thinking out loud rather than requesting a change, the
deliverable is your assessment. Report findings and stop. Don't apply a
fix until I ask.
6. MATCH EFFORT TO THE TASK. Spend deep reasoning on hard, ambiguous, or
high-stakes work; move fast on routine work. Don't add complexity,
caveats, or future-proofing the task didn't ask for. Do the simplest
thing that works well.
7. USE THE REASON, NOT JUST THE REQUEST. Connect the work to the intent
behind it. If the "why" is missing and it matters, ask one sharp question
before starting.
8. KEEP LESSONS + CHECK YOUR OWN WORK. Apply corrections I've given you in
this conversation. Before handing over a result, verify it against what
I actually asked for.

## 1. What becky-tools is (30-second version)

Offline, deterministic CLI tools for forensic analysis of video/audio — WHO is in
it, WHAT is said (timestamped), WHAT happens on screen, WHERE. Each tool does ONE
thing: file/JSON in → JSON out → exit code. Go binaries (`becky-go/`) with the
heavy ML pushed into thin embedded-Python helpers (`becky-go/internal/pyhelpers/`)
that call local models (Parakeet ASR, InsightFace, sherpa-onnx, Qwen3, llama.cpp).

**The single-tool principle is load-bearing.** Tools must stay independent and
composable so that when one breaks it is *obvious which one* and the rest keep
working. Never let the suite become one fragile mega-project. A new capability is
a new tool, not a tangle added to an existing one.

---

## 2. Invariants — do not relearn these the hard way

These are settled and each was a real bug or measured failure. Full reasoning in
`FORENSIC-OUTPUT-PHILOSOPHY.md` and README's "Non-obvious decisions".

- **ACCESSIBILITY: Jordan is SIGHTED with impaired vision — no screen reader.** Keep his
  high-contrast colored TUIs (they help him read); never strip color or replace a colored
  UI with plain text "for accessibility"; keep user text tight (he has reading limits); no
  Microsoft TTS (he wants a real researched TTS instead). Canon: `ACCESSIBILITY.md`. This
  was violated once already — don't repeat it.
- **HOW TO INTERACT WITH JORDAN: never make him run a CLI command or answer a technical question, and
  BUILD TO COMPLETION.** Jordan is non-dev and does NOT use the tools via CLI — "open a terminal and run X,
  paste the output" is a dead end for him, and a chat window full of jargon is often literally unreadable in his
  chaotic environment. So: (1) make decisions yourself from the spec/work-order/these docs — do NOT stop each
  increment to ask questions already settled; (2) if you GENUINELY need him, surface it as a **form**
  (`AskUserQuestion`, chips) or a **one-line spoken prompt** (whoretana-style) — never "run this command", never
  a wall of technical text; (3) **finish the job** — agents keep building stubs, testing forever, and stopping
  half-done. "It compiles" is NOT done; done = the VERIFY command passes + (for anything with a window/audio) it
  was exercised by **mouse + keyboard** (`CANVAS-NORTH-STAR.md` DoD). A buried step-by-step is why this keeps
  failing — work orders (`HANDOFF-*.md`) carry the ordered WHAT·HOW·WHY·VERIFY·DONE so agents don't wander.
- **Model choice = research a CLASS, then verify — never one article or the top download.** Pick the
  right model FAMILY first (e.g. TTS: tiny + LLM-backbone + fast; Kokoro is light-but-flat, 3B is
  too slow), survey the CURRENT field live (HF hub + the model's real card: params/license/GGUF), use
  a leaderboard only to VERIFY the shortlist, and end on the human's judgement (Jordan HEARS the TTS).
  The TTS pick was botched twice (stale-article Orpheus-3B, then most-downloaded Qwen) before this
  method produced NeuTTS Air — don't repeat the shortcut. Canon: `SPEC-BECKY-TTS.md` / `research/tts.md`.
- **Corroborate, then CONCLUDE — don't hedge.** ≥2 independent signals agreeing →
  state the conclusion plainly. A lone weak signal → "unknown"/candidate. A flood
  of maybes a human must sort = tool failure. The CONCRETE tool-chain for "is subject
  X actually on screen during [t0,t1]" is the **corroboration playbook in `SKILL.md`**
  (narrow with cheap signals → **`becky-validate` WATCHES the window with Gemma-4** →
  ≥2 agree → ship a TIGHT interval). A transcript mention or a `becky-motion` burst is
  NEVER presence; never put a window a model looked at — and the subject wasn't there —
  on a timeline anyway. (2026-06-24: a forensic task failed exactly here — the tools
  worked, the agent's chaining didn't.)
- **Recall is for DETECTION, not NAMING.** Surface every face/voice; attach a NAME
  only when corroborated.
- **Offline + deterministic.** No network at runtime; same input → same output
  (fixed seeds). The only "AI in the loop" is an explicit local model call.
- **Degrade, never crash.** Missing model/ffmpeg/audio → typed degrade error and a
  partial result, not a panic.
- **Paths may be Windows paths even when running on Linux/CI.** Use
  `internal/pathx` (separator-agnostic Base/Dir), not `filepath.Base` on a value
  that originated as a `C:\...` path. (This is why CI is green on Linux.)
- **THE drum machine is becky's own pure-Go SAMPLER engine — not Hydrogen, not REAPER.**
  `internal/drummachine` (model) + `internal/audioengine` sampler (real multi-sample kits,
  velocity/envelope/choke). becky-canvas's drum ▶ plays through it via
  `drummachine.MachineFromArrangement` → `becky-daw-engine --play-machine` (the SAME engine
  the standalone `becky-drummachine` uses). Hydrogen (`internal/hydrogen`/`becky-groove`) is
  an OPTIONAL export for its FOSS FX, NOT the core (it was orphaned — that was the confusion).
  REAPER is the full-DAW path, separate. Full rationale + what's left: **`DRUM-MACHINE-DECISION.md`**.
- **becky-canvas is THE app; REAPER is at most an export button — never a substitute.**
  This direction is PINNED. It ping-ponged for weeks (native drum machine → fork Hydrogen
  → drive REAPER → back to canvas) and confused Jordan every time. The native Go+Gio
  window is the product; everything lives **inside it**. Do NOT swap a REAPER chatbot /
  automation in for a native panel because the native thing is hard — build a smaller
  native thing. And **"compiles" is not "done"**: a canvas feature is done only when the
  window opens, **▶ Play makes sound**, every button works, and it doesn't freeze. Full
  directive + the mandatory Definition-of-Done checklist are in **`CANVAS-NORTH-STAR.md`**
  — read it before ANY canvas/DAW/drum/piano/mixer/audio work.
- **Music is deterministic — generate it with math, not tokens.** The arrangement build
  order and the rules that make each layer fit are SETTLED and live in code
  (`internal/arrange`): `key+progression → drums → bass → chords → melody → texture`,
  each layer aware of the stems before it (bass LOCKS to the actual kick, chords/melody
  stay in key, minor-key V is major, velocity is never flat), 8 bars max per chunk.
  "Four-on-the-floor house with my kick" must be instant + token-free, never a model
  call. A model is only for fuzzy plain-English intent, never the musical result. The
  canon is **`ARRANGEMENT-RULES.md`** — read it before any composition/layering work; it
  exists so these rules stop getting re-researched and lost every session.
- **The PROVABLE HANDOFF (from `STANDARDS-WORKFLOW.md` §7 + `HANDOFF-TEMPLATE.md`).** Any cloud→local
  handoff of work needing hardware cloud can't touch (audio/GUI/GPU/device/media) is NOT "ready"
  until it ships (1) a **one-command, no-hardware proof cloud already RAN and pasted evidence for**
  (a `--render`/`--selftest`/`--dry-run` that exercises the real code path + is measurable), and (2)
  an **ordered, checkboxed work order of commands** (not prose) the local agent drives to completion.
  "It compiles" is not proof. If you can't hand over a one-command proof, you haven't finished your
  half. This is the standing fix for "I researched it and none of it got wired up."
- **The five gates + the circuit breakers (from `STANDARDS-ENGINEERING.md`).** A branch is
  not "ready" until `go build/vet/test ./...` + `gofmt -l` + `build-all-tools.bat` are green
  (a cloud agent hands #5 to local but still passes 1–4). Every fixed bug ships a regression
  test; tests assert VALUES, not truthiness. **Max 3 auto-fix rounds on one failure, then
  stop and flag**; after 2 failed attempts at an error, stop guessing and research it.
  `scripts/install-hooks.sh` wires a pre-commit gate so this can't be skipped.

---

## 3. Build & test

```bash
# From becky-go/ — works on Windows and Linux, needs only the Go toolchain.
go build ./...      # compile every tool
go test ./...       # run every unit test (no models/ffmpeg/GPU needed)
go vet ./...
gofmt -l .          # must print nothing
```

```bat
REM Windows-only: produce the actual .exe binaries Jordan runs.
cd becky-go && build-all-tools.bat
```

**STANDARD PROCEDURE (not optional):** after building or modifying ANY tool, run
`build-all-tools.bat` to compile the real `.exe`s — `go build`/`go test` passing is
NOT "done"; the binary Jordan actually runs must build. The script auto-discovers
every `cmd/*`, so new tools are picked up with no edit to it. On a non-Windows/cloud
agent that can't run it, say so plainly and leave it as the local agent's completion
step (it must still pass `go build ./...`).

CI (`.github/workflows/ci.yml`) runs build + test + vet + gofmt on **both** Ubuntu
and Windows for every push and PR. Green CI means the deterministic Go layer is
sound. CI does **not** exercise the ML path (no model weights / GPU on CI) — that
is validated locally on real footage.

**One-click `.bat`/`.ps1` launcher scripts MUST be ASCII-only** (no em-dashes `—`, smart
quotes, en-dashes, etc.), and every user-facing `.bat` must end with `pause`. A double-clicked
`.bat` runs Windows **PowerShell 5.1**, which reads a BOM-less `.ps1` as the system ANSI
codepage — so a single stray Unicode char makes the whole script fail to PARSE and the window
flashes shut with no visible error. This silently broke both `Build Becky Clip.bat` and the
cloud-written `Build Becky Drum.bat` (fixed 2026-06-18). Before shipping a launcher, parse-check
it under 5.1: `powershell -Command "$e=$null;[void][System.Management.Automation.Language.Parser]::ParseFile('x.ps1',[ref]$null,[ref]$e);$e"`.

**MSYS2 native builds on THIS PC (the Shotcut fork, 2026-06-23):** `pacman -Syu` DEADLOCKS when run
non-interactively/in the background (hangs for hours on the in-use `msys2-runtime` DLL swap; killing
it corrupts the local DB). What WORKED: drive a REAL `C:\msys64\msys2_shell.cmd -mingw64` window via
keyboard automation (PowerShell `WScript.Shell` `AppActivate('MINGW64')` + `SendKeys`) and type
`pacman -Syuu --noconfirm --overwrite "*"` into it — interactive completes in minutes. And MSYS2's
`mingw-w64-x86_64-mlt 7.36.1` package satisfies Shotcut's `mlt++-7>=7.36.0`, so you can SKIP the
multi-hour FFmpeg/MLT/OpenCV from-source build and just `cmake+ninja` Shotcut. (Full saga: `HANDOFF-LOG.md`.)

---

## 4. Cloud ↔ Local handoff protocol

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

### Copy-paste prompt for the local agent

When the cloud agent has pushed a branch, Jordan pastes this into his **local**
Claude Code (filling in the branch name from the chat or the PR):

> Pull the branch `<BRANCH-NAME>` from origin and check it out (create a local
> tracking branch if needed). Read `CLAUDE.md` section 6 ("Live handoff") for what
> the cloud agent finished and what's left for you. Then: run `go build ./...` and
> `go test ./...` in `becky-go/` to confirm it's green on this machine; wire up any
> Python helper stubs listed in the handoff to their real local models; run
> `build-all-tools.bat`; and test the new/changed tool against a real clip. Report
> what passed, what degraded, and anything that needs my input. Commit to the same
> branch.

### Minimal trigger — Jordan does NOT paste the long prompt

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

### Two agents, one repo — anti-collision rules (READ before committing)

Both agents share this remote. **Full rules + the async inbox + the work registry
live in `COLLAB-PROTOCOL.md` — read it before claiming or building anything.** The
load-bearing rules, in brief:

1. **Lanes.** Cloud commits only on `claude/<topic>` branches, never to `master`.
   Local owns `master`. Neither edits the other's branch or force-pushes.
2. **Atomic branches.** One cloud branch = one finished deliverable. Don't keep
   pushing after marking it ready — new work goes on a NEW branch. The button may
   fast-forward-merge a `claude/*` branch ONLY when §6 says *"Left for local:
   nothing"*; if §6 says REVIEW/pending, it launches local Claude instead, and never
   deletes a branch whose tip wasn't merged. (This is the fix for the 2026-06-15
   mid-stream-merge incident.)
3. **Rebase onto latest `master`** before signalling ready; resolve conflicts
   additively — never drop the other agent's work.
4. **Claim before you build** (the registry in `COLLAB-PROTOCOL.md`) so we don't ship
   two tools for one job (it already nearly happened: `becky-freshness` vs the
   self-upgrade flag in `becky-research`).
5. **Edit `CLAUDE.md` / `COLLAB-PROTOCOL.md` additively**, section-scoped — never
   wholesale-rewrite. The §5 doc map is the single source of truth for what exists.

---

## 5. Doc map — which file, when

**Canonical (read these):**
- `CLAUDE.md` (this file) — how we work + the *current* handoff state (§6).
- `HANDOFF-LOG.md` — the **full branch-by-branch handoff history** (newest-first). CLAUDE.md §6
  carries only the current state; the complete log of every cloud/local session lives here. Append
  finished-branch entries to its TOP; never let CLAUDE.md §6 grow back into a full log.
- `ACCESSIBILITY.md` — **how becky must fit Jordan's vision: SIGHTED but impaired, NO
  screen reader, high-contrast COLORS are an aid (keep colored TUIs, don't strip them),
  NO Microsoft TTS, wants a real researched TTS.** Read before any user-facing output/UI/
  TTS work — an agent already got this wrong once.
- `COLLAB-PROTOCOL.md` — how the two agents (cloud + local) share this repo without
  clobbering: lane rules, the work registry (claim before you build), and the async
  inbox between us. Read before committing.
- **The STANDARDS-\*.md set (MANDATORY, adapted from the ACE-Step-DAW `.claude` rules —
  re-expressed in becky's terms, not AGPL-copied):** `STANDARDS-ENGINEERING.md` (the five
  quality gates, TDD, regression-test-per-bug, assert-values-not-truthiness, the
  max-3-fix / stop-and-research circuit breakers, research-depth); `STANDARDS-WORKFLOW.md`
  (propose→preview→apply, spec-first for 3+ files, the two-reviewer rule, named review/
  test stances, the quality-gate hook); `STANDARDS-CANVAS-UX.md` (visual language, the
  interaction grammar, and the headline **dual human+agent operability** rule — a canvas
  feature isn't done until it's operable from BOTH a panel AND a `ctledit` op, undoable);
  `STANDARDS-MUSIC-RESEARCH.md` (how becky researches a genre's theory before composing:
  the 5 elements, the search-query templates, named-references-are-gold, the 2–4 principles,
  and the `becky-research → distill → profiles/<genre>.json` pipeline). The deterministic
  halves execute in `internal/arrange` + `internal/musictheory`; these docs are the canon
  so the rules never get re-researched and lost.
- `README.md` — project overview, tool catalog, non-obvious decisions.
- `FEATURE-INVENTORY.md` — **the canonical "definition of functional": the exhaustive
  checklist (187 items) of every basic feature a DAW / drum machine / piano roll / mixer /
  video-NLE / audio editor must have.** This is the bar becky measures against; a separate
  gap analysis (CLAUDE.md §6 / DRUM-MACHINE-DECISION) compares becky's real state to it.
  When in doubt about whether a tool is "done", check it here.
- `GAP-ANALYSIS.md` — **becky's REAL state vs FEATURE-INVENTORY, item by item with file:symbol
  citations + a prioritized punch-list.** The honest pattern it found: strong tested model layer
  almost everywhere, thin/absent RUNTIME (audible/visible) layer. Read it to pick the next
  highest-impact gap; update it as gaps close.
- `DRUM-MACHINE-DECISION.md` — **the PINNED answer to "Hydrogen or REAPER or what?"**: becky's
  own sampler engine is THE drum machine; the canvas plays through it; Hydrogen is an optional
  export. Read before any drum/canvas-audio work so it stops flip-flopping.
- `LOCAL-WORK-ORDER.md` — **THE current local task: an ordered, command-by-command, checkboxed
  work order to make the becky-canvas drum machine SOUND, with the exact verify command for each
  step.** Built because vague "LEFT FOR LOCAL" prose kept getting merged-and-skipped. The local
  agent drives this to completion and pastes evidence into §6; cloud already proved Step 1's audio.
- `HANDOFF-TEMPLATE.md` — **the STANDARD skeleton every cloud→local runtime handoff copies** (the
  "provable handoff": a one-command offline proof cloud already ran + an ordered checkboxed work
  order). Mandatory per `STANDARDS-WORKFLOW.md` §7 + CLAUDE.md §2/§4. Copy it; don't hand off prose.
- `HANDOFF-ROUTING-CANVAS.md` — **how to wire the deterministic label→bus routing (`internal/autoroute`,
  `becky-route`) into becky-canvas + REAPER**, and the Hydrogen-can't-host-VSTs fact. Jordan's
  workflow: lightweight WRITING, then apply his routing/plugins at the END (or a routed default), so
  he never re-routes 16 channels by hand. Cloud proved the routing offline; local does the VST/bounce.
- `HANDOFF-CANVAS-GUI.md` — **THE panel-by-panel work order for the local agent to wire becky-canvas's
  GUI** (song-from-a-phrase, the Route action, per-bus FX-chain view, Bounce, save/undo buttons) onto
  the already-proven engines (`songbuild`/`autoroute`/`fxchain`/`audioengine` render), each step with a
  one-command offline proof + a window Definition-of-Done. Written because GUI handoffs kept being vague.
- `HANDOFF-VST-CANVAS.md` — **the C++ VST3-host work order**: host an effect on a bus, apply a saved
  state chunk (dialed-in plugin settings), and render-through for bounce-in-place. The host already does
  effect-render + `vst.state.load`; the gaps (a WAV reader, a `render.chain` verb, a `Bus.FX` field) are
  spelled out with proofs. VST3 SDK is MIT (v3.8, Oct 2025) so this path is license-clean.
- `SKILL.md` — how to *use* the tools (human + agent usage guide).
- `FORENSIC-OUTPUT-PHILOSOPHY.md` — how findings must be reported. Governs every
  human-facing output.
- `CANVAS-INSPIRATION.md` — design-research brief for becky-canvas (Jordan's GUI):
  starred-repo mining + reference apps (infinite-kanvas, ACE-Step-DAW, DAW-Copilot,
  cate, jsoncrack, blocksuite, the "show me, don't do it" overlay). Read before any
  becky-canvas GUI/agent-UX work — the research is done, don't redo it.
- `BECKY-CANVAS-ROADMAP.md` — **THE ratified post-pivot plan (2026-06-22): build the real
  DAW INSIDE becky-canvas (Go+Gio).** REAPER/kdenlive *driving* is PAUSED (code kept dormant,
  not deleted); **OpenDaw is the MODEL to PORT natively, not fork** (it's GPL3 Qt/C++). Carries
  the Phase 0 architecture fix (replace spawn-per-click with a persistent in-process engine — the
  lag/console-flash root cause) + the phased plan (drum fundamentals → one timeline widget →
  agentic AI control → mixer/FX), each grounded in `research/`. Read it WITH CANVAS-NORTH-STAR.
- `CANVAS-NORTH-STAR.md` — **THE pinned direction + Definition-of-Done for becky-canvas
  (read FIRST before any canvas/DAW/drum/piano/mixer/audio work).** Settles the
  re-litigated question once: becky-canvas (native Go+Gio) is the tool Jordan opens;
  REAPER is at most an export button, never a substitute for a native panel. Carries the
  mandatory hardware checklist (window opens, ▶ Play makes sound, every button works, no
  freeze) that "it compiles" kept skipping, and the cloud↔local split. Outranks a single
  session's instinct; if it seems wrong, ask Jordan — don't pivot.
- `ARRANGEMENT-RULES.md` — **the deterministic music-theory canon (read before any
  composition/layering/`becky-compose`/`becky-arrange`/canvas-music work).** The build
  order (`drums → bass → chords → melody → texture`), how each layer fits the ones before
  it, the universal constraints (in-key, bass register, never-flat velocity, minor-V
  major), per-genre progressions, and the 8-bar chunk rule. Ported from ACE-Step-DAW's
  `.claude` skills into EXECUTING Go (`internal/arrange`) so the rules never get
  re-researched and lost. The code is the source of truth; this is its human-readable canon.
- `GUI-RULES.md` — **CANONICAL GUI + audio architecture standard (ratified 2026-06-19).**
  Read before ANY GUI/audio/DAW/NLE work. The stack (Go engine + Gio GUI + C++ VST3/ASIO
  audio-host sidecar + Rust/wgpu video sidecar), the deterministic NDJSON engine↔GUI seam,
  build/verification rules, interaction patterns, and the phased path. No embedded browsers
  (WebView2 retired). Supersedes the audio-licensing conclusion in `research/gui-toolkit.md`
  (the VST3→MIT / ASIO→GPL relicensing of 2025-11-04 changed it).
- `CANVAS-BLUEPRINT.md` — **the integration spine for Becky Canvas (Jordan's Cubase/Maschine
  replacement + central HUB).** Read with `GUI-RULES.md` before ANY becky-canvas work. Names the
  ONE session model (`dawmodel.Arrangement`), the disjoint per-panel contracts (drum/piano/mixer/
  vst/audio), and the convergence order so panels wire to the EXISTING rich models instead of
  spawning a 5th toy. becky-canvas is the app Jordan opens; it now has HUB launch buttons that open
  the real tool windows (Drum Machine / REAPER DAW / Clip / NLE / Ask) — `Open Becky Canvas.bat` +
  Desktop "Becky Canvas". The in-window panel convergence (Steps 1-5) is the ongoing arc.
- `SPEC-BECKY-REAPER.md` — **the WORKING AI-first DAW (BUILT + PROVEN 2026-06-20).** becky
  authors/drives **REAPER** (already installed, fully scriptable, hosts all his VSTs) via a
  deterministic `.rpp` writer (`internal/reaper` + `cmd/becky-reaper`) + ReaScript; REAPER is the
  DAW Jordan opens, becky is the AI brain. The pragmatic answer to "download an opensource DAW and
  control it" — complements (does not replace) the GUI-RULES native stack. One-click `Open Becky DAW.bat`.

- `SPEC-BECKY-CLIP.md` + `BECKY-CLIP-HANDOFF.md` — becky-clip, the forensic transcript-based
  video COMPILATION editor (WebView2 GUI + Go engine). The spec is *what it is*; the HANDOFF is
  *how to change it without re-making solved mistakes* (gotchas, non-obvious logic, dead ends
  already ruled out). **Read the HANDOFF before touching becky-clip.**

**Specs (read the one for the tool you're building):**
- **PRIORITY BUILDS — 2026-06-23 (the "adopt a mature host, add the becky layer" pivot; see
  `BECKY-CANVAS-ROADMAP.md` + the `research/daw-nle-*` + `research/bookmarks-*` docs):**
  - `SPEC-BECKY-NLE.md` — **the real video NLE, to be built FIRST** (Jordan's priority). ADOPT
    **Shotcut** (Qt6/QML/**MLT** — the engine becky already writes) + a **Becky dock** that
    reuses the EXISTING becky-clip engine (`internal/footage`/`quotes`/`edl`/`reel`/`assistant`):
    point at a folder → search the `.srt` transcripts → **single-click a quote = preview
    seeks+plays**, **double-click = clip appended to the timeline** → real editing (Shotcut) →
    forensic export. Runtime-extensible (becky CLIs as tools + embedded agent/PTY, no host
    recompile). Phase 0 is a build-Shotcut SPIKE. Supersedes becky-clip's editing-less GUI.
  - `SPEC-BECKY-DAW.md` — the real DAW (built after the NLE). **Spike-first** host decision:
    **B = adopt OpenDAW** (C++/Qt6, ships a ~30-tool AI assistant) vs **C = build the UI in Go
    via giu/Dear ImGui** (port `im-neo-sequencer`) + a C++ audio/VST engine (DawDreamer/sidecar).
    becky's Go brain (`dawmodel`/`ctledit`/`ctlmodel`/`arrange`) stays + becomes the toolset
    either way; #1 gap to build regardless = `internal/ctlagent` (multi-step agentic loop).
- **SPEC FACTORY — 2026-06-22 (cloud, design-only, NOT built; each ships a checkboxed build
  plan + value-asserting tests; await Jordan's go/no-go). Built by a parallel subagent swarm
  to clear the discussed-but-never-spec'd backlog:**
  - `SPEC-BECKY-TTS.md` (+ `research/tts.md`) — a tiny+intelligent local TTS: **NeuTTS Air**
    (0.75B Qwen2-LLM backbone, Apache, GGUF, on-device/expressive); alternates Chatterbox-Turbo
    (350M MIT) / NeuTTS Nano (228M) / Qwen3-TTS (heavier fallback). The class = tiny + LLM-backbone
    + fast (Kokoro is light-but-flat; 3B too slow). NOT Microsoft TTS; Piper/Kokoro/Orpheus ruled
    out. Leaderboard verifies, doesn't select (arena top is cloud). Final gate = Jordan HEARS it.
  - `SPEC-IDENTIFY-HARDENING.md` — fixes the Critical wrong-person voice-ID (name bar ~0.75,
    top-2 margin, `--cast` guard). The highest-value forensic-accuracy fix.
  - `SPEC-BECKY-INGEST.md` — `becky ingest <folder>` → runs the pipeline + a LINEAR `DIGEST.md`.
  - `SPEC-BECKY-DATES.md` — `becky dates` forensic date triangulation (exifmeta + mtime + OCR).
  - `SPEC-BECKY-LOCATION.md` — `becky location` room/dwelling fingerprint (consumes framematch).
  - `SPEC-FRAMEMATCH-HARDENING.md` — ROI ceiling-crop + decor keypoint match (fixes the
    body-silhouette false neg/pos; pure-Go default, gocv opt-in).
  - `SPEC-FACE-CROP-DB.md` — tight face-crop artifact + write embeddings to the already-built
    unused `appearance_embeddings` table; feeds enroll + cluster.
  - `SPEC-ASK-SINGLESHOT.md` — `becky-ask --question/--image` scriptable mode ADDED BESIDE the
    colored TUI (TUI stays the default — do not demote it).
  - `SPEC-FACE-NAMING-LOOP.md` — `becky-cluster → becky-name` (high-contrast review card) →
    enroll the cluster, + inline "teach me" remedy in identify's unnamed output.
  - `SPEC-BECKY-VOICE.md` (cloud, 2026-06-23, design-only) — **the always-on, proactive VOICE +
    context front-end for the WHOLE suite** ("I just talk and it does it"): a thin realtime skin
    (FastRTC transport + Gemini-Live cloud OR Gemma-4+NeuTTS local) + a **rules/harness layer**
    (GREEN/YELLOW/RED action tiers, kill switch, privacy-local-for-sensitive, **user CONTROL over
    context — directable like whoretana, not just a visible indicator**, addressee-detection for
    always-on, customizable `becky-voice.rules.json`) over the EXISTING front-doors
    (`becky`/`becky-ask`/`becky-harness`/REAPER bridge/Strudel) — reimplements NO tool (single-tool
    principle preserved). Reactive ("talk") half is near-done; the real new work is the PROACTIVE
    **background analyst** — corroborate-then-conclude applied to PROPOSALS (no bullshit firehose),
    cheap always-on LFM2.5 orchestrating `becky-research`/`radar`/`scout` under a **heartbeat + `/goal`-
    bounded harness** (hermes-style `no_agent` ticks + hooks; tiered LFM2.5→Qwen/Gemma escalation, each
    tier its own protocol), findings delivered as a **~30s narrated debrief VIDEO in whoretana's persona
    voice** (HyperFrames/Mermaid in becky-canvas — Jordan won't read 3 pages but will watch 30s), NOT
    spoken nagging; it can also drive **Claude Code** (`internal/agentrun`) + CLIs and digest them so he
    reads less. whoretana persona/verbs = LOCAL agent's lane. Pairs with `research/daw-ai-control-reaper-vs-ableton.md`.
    **BUILD it from `HANDOFF-BECKY-VOICE.md`** — the ordered, checkboxed WHAT·HOW·WHY·VERIFY·DONE work order
    (Gemini-2.5-realtime first; declarative conditional workflows + auto-generated fill-in-the-blank response
    map; cloud Phases 0–2 then a local hardware runbook). The spec is the why; the handoff is the do.
- `SPEC-HANDOFF-HARDENING.md` (**ASSIGNED TO CLOUD, 2026-06-17 overnight** — make the
  "Get Becky Updates" button drain the whole branch queue, self-heal a poisoned tree,
  and detect two branches editing one tool; the union-merge doc fix already shipped).
- `SPEC-BECKY-IMAGEGEN.md` (BUILT 2026-06-28, cloud — `becky-imagegen`: becky's DEFAULT
  local **text→image** generator. Deterministic Go wrapper (`cmd/imagegen` +
  `internal/imagegen`) around **stable-diffusion.cpp's `sd-cli`** running **FLUX.1
  "Krea-2"** (Krea-2 transformer + Wan 2.1 VAE + Qwen3-VL-4B text encoder; docs/krea2.md).
  Fixed-seed deterministic, degrade-never-crash, config-driven paths. Generation ONLY —
  NOT the forensic vision readers. Offline proof `becky-imagegen --selftest` = 10/10;
  §8 = the local model-boundary work order. Downloader `scripts/get-krea2.ps1`.)
- `SPEC-BECKY-ASK.md`, `SPEC-BECKY-NEW-TOOL.md`, `SPEC-OCR.md`,
  `SPEC-OCR-ENSEMBLE.md` (PROPOSAL, cloud 2026-06-27 — multi-model OCR ensemble +
  adversarial ≥2-engine corroboration; additive enhancement to `becky-ocr`/`SPEC-OCR.md`),
  `SPEC-PERSON-CLUSTERING.md`, `SPEC-VIDEO-ANALYSIS.md`,
  `SPEC-BECKY-COMPOSE.md` (BUILT — `becky-compose`: deterministic genre→multi-track
  MIDI; genre DB in `internal/music/profiles/`).
- **BUILT 2026-06-15 (deterministic Go cores; online/model boundary stubbed):** `SPEC-DEEP-RESEARCH.md` (`becky-research`
  deep-research harness), `SPEC-OPEN-PALANTIR.md` (`becky-palantir`, integrates
  the OpenPlanter OSINT/entity-graph project), `SPEC-AGENT-HARNESS.md`
  (`becky-harness`, drives a Pi agent over becky's tools, universal per request),
  `SPEC-OMNIGENT.md` (`becky-omni`, runs becky's agent(s) under the Omnigent
  meta-harness — `omnigent-ai/omnigent`, Databricks' Apache-2.0 meta-harness that
  sits ABOVE Pi — for policy/cost/sandbox governance + a share-URL Jordan can watch
  and steer from his iPhone; reconciles with `becky-harness`),
  `SPEC-RADAR.md` (`becky-radar`, reads Jordan's Chrome history — incl. synced
  iPhone visits — and surfaces flagged models/tools vs becky's deps),
  `SPEC-SCOUT.md` (`becky-scout`, assesses a YouTube playlist video-by-video for
  things that could improve/extend becky — sibling of becky-radar; corroborate-
  then-conclude over the freshness manifest + a capability catalog),
  `SPEC-BECKY-CANVAS.md` (native lightweight creative GUI: becky-ask + video/DAW/
  MIDI/drum modes on one canvas — Jordan's AI-friendly Cubase replacement).
- **becky-canvas DAW/audio suite (BUILT 2026-06-15 — deterministic Go cores; native audio/GUI = Phase-2):**
  `SPEC-BECKY-DAW-ENGINE.md` (real-time audio + selective VST3/CLAP hosting; VST3 SDK
  is now MIT-licensed so it's tax-free; default to the pro audio interface when
  plugged in), `SPEC-BECKY-DAW-EDITOR.md` (piano roll + drum machine + mixer + SMF
  reader/editable MIDI + RegenTrack "LEGO context"; Cubase parity), `SPEC-BECKY-MIX-JST.md`
  (Joey Sturgis mix as a deterministic mix.json: breakdown kick→bass→guitar sidechain +
  per-bus FX chains; per-bus VST prefs incl. "Odin II"), `SPEC-BECKY-HUM.md` (sing/hum →
  key+tempo+MIDI with key-aware suggestions — the INPUT side of becky-compose),
  `SPEC-BECKY-VOX.md` (multi-take vocal alignment, Melodyne/VocALign class: DTW timing
  + formant-preserving pitch match + comp). **HARD REQUIREMENT across these: audio
  editing is VISUAL-FIRST — waveform tracks + pitch lanes are the surface; Jordan
  manually fixes by eye; becky LEARNS his preferences from his corrections.**
- `SPEC-BECKY-VISION-MODELS.md` (BUILT 2026-06-15 as `becky-vision`): adopt Liquid **LFM2.5-VL** (NOT old LFM2)
  GGUF VLMs as right-sized llama.cpp backends — 450M for frame triage, 1.6B-Extract
  for becky-ocr doc→JSON, 1.6B for becky-ask (Gemma-4 stays for AUDIO; LFM2-VL is
  image-only). + custom-training plan (Unsloth LoRA→GGUF on the 3070, incl. a
  "becky preference" model). Tracked in `internal/freshness/manifest.json`.
- `BUILD-AGENT-BRIEFING.md` — briefing for a subagent building one tool.
- **`becky-report` (BUILT 2026-06-16, cloud):** `cmd/report` + `internal/report` — deterministic
  forensic case reporter; reads pipeline sidecar JSONs → merged timeline + corroboration engine +
  markdown report. No spec file needed (implements FORENSIC-OUTPUT-PHILOSOPHY.md §TOP rule in code).
  15 tests green. Left for local: run `build-all-tools.bat` (auto-discovers cmd/report), then test
  against a real pipeline output dir.

**Historical / inbox (context only — not current instructions):**
- `PROGRESS.md` — build-loop tracker/log.
- `TEST-FEEDBACK.md` — hand-off inbox from the test agent.
- `TRANSCRIPT-GAP-FINDINGS.md`, `MORNING-BRIEF-2026-06-09.md` — dated R&D notes.

> If this list and the files ever disagree, this list wins — tell Jordan so it can
> be corrected. New planning docs should be linked here so the root never becomes
> "scattered .md files" again.

---

## 6. Live handoff — current branch status

> **The full branch-by-branch history lives in `HANDOFF-LOG.md`** (newest-first, every cloud/local
> handoff). This section keeps ONLY the *current state of `master`* + what's pending for Jordan.
> When you finish a branch: write the detailed entry to the **TOP of `HANDOFF-LOG.md`** and update
> the short summary here. **Do NOT let this section grow back into a full log** — an accumulating
> §6 is exactly what pushed CLAUDE.md past the prompt-size limit (fixed 2026-06-22).

### Awaiting Jordan's go/no-go (spec landed, NOT yet built)

- **OCR ensemble + adversarial corroboration (`SPEC-OCR-ENSEMBLE.md`, landed 2026-06-28).** The
  *spec* is on master (multi-model OCR ensemble + adversarial ≥2-engine corroboration; adds the
  Unlimited-OCR long-doc slot; GLM-OCR↔PaddleOCR-VL A/B; a mandatory leaderboard-sweep process fix;
  claim/INBOX-3 in `COLLAB-PROTOCOL.md`). It is design only — **nothing is built yet.** Before
  anyone codes `internal/ocrfuse`, Jordan ratifies and settles the §10 open decisions (doc-slot A/B,
  threshold T, critical classes, long-doc in v1?, agreement tol, escalate-only vs `--thorough`);
  then cloud can build the deterministic core with no models.

### Current state of master (as of 2026-06-28)

Green and pushed. `go build/vet/test ./...` clean + `gofmt` clean-modulo-CRLF (the repo's `.go`
blobs are CRLF throughout — cosmetic on Windows per §4, CI-green on Linux); the lone `cmd/tts` test
FAIL is pre-existing/environmental (the local TTS model is present, so "degrades when no model"
inverts); `build-all-tools.bat` builds all 84 `.exe`s. Recent landings (details in `HANDOFF-LOG.md`):

- **becky-daw ask + becky-reaper song — the AI-music loop RUNS end-to-end headless (2026-06-28, cloud,
  `claude/becky-tool-continue-f7m0yq`):** plain-English → openable, audible REAPER session, no GUI/GPU.
  `becky-daw ask` (`cmd/daw/ask.go`) loads a session (becky-compose `project.json`, raw `arrangement.json`,
  or `.mid`), turns each `--do "…"` into a `ctledit` batch via `ctlmodel.PickProposer()`, applies it, writes
  the edited arrangement back. `internal/ctlmodel` keyword parser broadened (route/send-to-bus, sidechain/duck
  on top of tempo/mute/solo/pan/gain/transpose). `becky-reaper song` (`cmd/becky-reaper/song.go`) collapses
  compose→ask→build into ONE command. **VERIFIED on this box:** `becky-reaper song --genre crunkcore --seed 7
  --do "set tempo to 96" --do "mute the sfx"` wrote a `.rpp` carrying `TEMPO 96` + `sfx … MUTESOLO 1`; tests
  pass; both `.exe`s build. (Integration note: cloud accidentally committed a 10MB `becky-go/becky-reaper`
  ELF — dropped on merge + `.gitignore` patched so it can't recur. No "left for local".)
- **becky-imagegen — DEFAULT local text→image gen via Krea-2 (2026-06-28, cloud,
  `claude/default-local-image-gen-lyw127`):** new single-purpose tool (`cmd/imagegen` +
  `internal/imagegen`) — prompt → PNG, generated on-device by **stable-diffusion.cpp's `sd-cli`**
  running **FLUX.1 "Krea-2"** (Krea-2 transformer + Wan 2.1 VAE + Qwen3-VL-4B text encoder;
  https://github.com/leejet/stable-diffusion.cpp/blob/master/docs/krea2.md). becky-shaped: fixed
  seed 42 (deterministic), degrade-never-crash, every path from `config.ImageGen()` (no hardcoding),
  `--turbo` variant, `--dry-run`/`--json`. **Generation ONLY — does NOT replace the forensic vision
  READERS (Gemma-4/LFM2.5-VL/Qwen).** Cloud gates green (build/vet/test/gofmt) + the offline proof
  `becky-imagegen --selftest` = **10/10 PASS**; freshness manifest rows + `scripts/get-krea2.ps1`
  added. **Left for local (SPEC-BECKY-IMAGEGEN.md §8):** build/obtain `sd-cli`, run `get-krea2.ps1`
  for the three model files, then make ONE real 1024×1024 PNG (the hardware "see it" gate) + tune
  steps/cfg/guidance on real output.
- **Becky Review round-4 fixes — timeline + overlay + forensic re-transcribe naming (2026-06-27, local,
  `claude/becky-review-fixes2`):** all five of Jordan's round-4 items, CDP/screenshot-verified on a real
  folder then deployed to the main tree (Desktop "Becky Review" runs them). (a) clip **drag-reorder
  restored** without losing click-to-seek (one `#track` pointer state machine, `DRAG_PX`=6, drop index =
  `App.Reorder`'s remove-then-insert index); (b) edge-**snap reeled in** (`SNAP_PX`=8, exact position
  elsewhere); (c) **extend-clip clamps to its own source** (cached `probe` duration, never a neighbour);
  (d) **overlay no longer off-screen** — drawn in the HOST canvas (mpv's osd-overlay maps to the window,
  not the letterbox video rect), proven on a 1080×1920 portrait clip; (e) **FORENSIC**: re-transcribe
  writes a SEPARATE `<stem>_parakeet_transcription.srt` (shared const `footage.LocalTranscriptMarker`)
  and ↻ FORCES a fresh Parakeet pass even when an official transcript exists — original never touched.
  Regression test added; transcribe/footage tests + tooltips updated, all green.
- **becky-otio COMPLETE — every interchange format + kdenlive engine render-proof (2026-06-27, local,
  `SPEC-BECKY-OTIO.md`):** the editor-agnostic timeline exporter now implements ALL of its advertised
  `--format`s. Phase 1 (cloud) shipped `otio`/`vegas-list`/`edl`; this pass added the two writers the
  spec's CLI listed but left unwired — `fcpxml` (flat FCPXML 1.10, rational frame times, mixed-fps:
  `1950/30s` beside `3000/25s`) and `mlt` (`<name>.kdenlive` via the proven `internal/kdenlive` emitter)
  — plus the optional `--via-otio-cli` otioconvert escape hatch (degrades silently, becky stays
  Python-free). `--selftest` now runs 12 value assertions (exit 0); a real Reel → `--format all` wrote +
  structurally validated all five files. **Render-proven:** the `.kdenlive` rendered headless through
  `melt` (kdenlive's engine) to exactly 210 frames = 7.0s (frame-exact), closing the kdenlive round-trip
  deterministically. **Left for Jordan (eyeball only):** import the `.otio` in DaVinci Resolve and run
  the VEGAS script on the `.review.txt` (both editors installed) and confirm they play.
- **Scrub proxies — the real Shotcut-lag fix (2026-06-27, local, `HANDOFF-PROXY-SNAPPINESS.md`):**
  scrubbing was slow because long-GOP H.264/HEVC decodes a whole group of pictures per seek, and becky's
  old `reel.Proxy` *short-circuited* web-safe H.264 (so the commonest evidence got NO scrub proxy). New
  **`reel.ScrubProxy`** builds an INTRA-FRAME, constant-frame-rate proxy (`<stem>.scrub.mp4`: `-g 1
  -sc_threshold 0`, `scale=-2:540,fps=30`; tunable via `BECKY_PROXY_CODEC`/`BECKY_PROXY_RES`; mtime-cached).
  becky-clip's preview (`ProxyFor`) now routes through it, and new CLI **`becky-proxy`** (`--src`/`--selftest`)
  is the surface the Shotcut dock shells out to (it ffprobe-verifies its own `intra_frame`/`cfr`). **Proven:**
  `--selftest` + a real interview clip both yield a 60/60-keyframe, CFR proxy. Open gate = Jordan confirming
  it *feels* smooth when scrubbed (a human-vision go/no-go that decides keep-the-fork vs not).
- **Becky Review = becky-clip rebuilt as the FULL editor on a persistent engine + native mpv (2026-06-27,
  local):** `gui/BeckyReview` (native WPF) is now the real forensic editor, not the minimal reviewer. It
  **reuses becky-clip's entire engine + UI** — only the video + transport are swapped to native mpv. A new
  headless **`becky-clip bridge`** (shipped as `becky-review-engine.exe`) keeps ONE warm `App` (folder
  index + transcript parse-cache) over stdin/stdout NDJSON = fast repeat search (fixes the slowness) + every
  bridge verb. **LEFT** = WebView2 UI (no TCP server): file list with green-"+" in-app transcribe, search
  (exact `N quotes … playable` header + highlights), single-click=play / double-click=add-to-timeline, a
  drag/resize/scrub timeline (save/load/export), and the becky chat. **RIGHT** = native mpv (frame-exact,
  GPU; `.srt` never burned on the video). The **overlay** lower-third (filename + LIVE ORIG-TC + date/link)
  is drawn by mpv's ASS osd-overlay. Chat **defaults to local Gemma-4 E4B**; "use Claude" → Claude Code.
  Verified CDP-driven on a real folder (search/play/timeline/scrub/overlay/chat). One-click `Open Becky
  Review.bat` + Desktop shortcut (first run builds the engine + fetches the git-ignored mpv runtime). The
  earlier thin `becky-review-index` tool remains for scripted folder-index/search. **Left (model boundary):**
  green-"+" `becky-transcribe` (Parakeet ASR) + the chat's local Gemma (llama-server + GGUF) — wired +
  degrade-proven; full runs are the usual tap on real footage.
- **Qwen3.5-4B wired in as the orchestrator + cross-family corroborator (2026-06-27, local):** the model
- **Qwen3.5-4B wired in as the orchestrator + SINGLE-IMAGE corroborator (2026-06-27, local):** the model
  Jordan linked (Unsloth **`UD-Q4_K_XL`**) now has a real config home (`config.Qwen()` + `BECKY_QWEN_MODEL`)
  instead of three copy-pasted hardcoded paths. It is the TEXT brain (routes `becky-ask`, proposes in
  `becky-scout`, reasons in `becky-new-tool`) and a SINGLE-IMAGE corroborator via **`becky-vision --qwen`**
  (one still, a different family than LFM/Gemma). **Qwen3.5-4B does NOT watch video** — no multi-frame/audio
  understanding; ALL video+audio watching stays Gemma-4 (E4B→12B, `becky-validate`). Image-capable via its
  own F16 mmproj but **NOT a "Qwen3.5-VL"** (no such model; the separate heavy Qwen3-VL is only for a
  dedicated VL job). Manifest entry + `scripts/get-qwen35.ps1` + SKILL.md added. **Proven live:**
  `becky-vision --qwen` described a real still in 6.3s (`model: qwen3.5-4b-UD-Q4_K_XL`, single-image path).
  (An earlier same-day pass wrongly put Qwen in the video validate ladder + a `qwen35-local` video backend;
  reverted — Jordan caught that Qwen3.5 is image-only.)
- **becky-regrab + hardened fetch (2026-06-27, local):** pages the archiver missed are now re-grabbed.
  The real fix was a fetch bug — `trafilatura.fetch_url` returned brotli/zstd **garbage** for some sites,
  so web2md extracted nothing; `web2md.py`/`clipfetch.py` now validate the fetch + fall back to a clean
  urllib fetch, which recovers most misses **deterministically**. New **`becky-regrab`** is the Gemma-4
  fallback for what's still missed (local E4B converts the page text to Markdown, then it's clipcheck-verified
  so the model can't drop/invent content; honest "unrecoverable" for bot-blocked/JS-only pages). Wired into
  `clip-sync.ps1` as the automatic per-page ladder (web2md -> clipcheck -> regrab) + a `-Retry` mode.

- **becky-otio + video-editing host research (2026-06-27, cloud `claude/video-editing-research-jqdz1t`
  -> integrated local):** new **`becky-otio`** (pure-Go, offline, deterministic) turns a becky **Reel**
  (`internal/edl` clip-list) into editor-agnostic timeline files — `.otio` (DaVinci/kdenlive 25.04+),
  CMX3600 `.edl` (every editor), and a `.review.txt` for `/vegas/BeckyReviewTimeline.cs` on **VEGAS Pro 18**
  — so forensic hits review in whatever snappy NLE Jordan prefers without marrying becky to one editor
  (`cmd/becky-otio` + `internal/otio` + tests; `becky-otio --selftest` passes). Also landed: `SPEC-BECKY-OTIO.md`,
  the VEGAS script + `vegas/README.md`, `research/gui-embedding-revisit-2026-06.md`, and two work-order docs
  (`HANDOFF-BECKY-REVIEW-APP.md`, `HANDOFF-PROXY-SNAPPINESS.md`). The cloud branch was based on `104fed4`
  (before the iPhone archiver) so it's disjoint from `b88de88` — merged additively, archiver intact. **Left
  for local:** build the one-window "Becky Review" reviewer app + the proxy/timeline-snappiness work per those
  two handoff docs (future GUI/host task; the deterministic `becky-otio` core is done + proven).

- **iPhone-history -> verified-markdown archiver (2026-06-26, local):** Jordan's Chrome history (iPhone-
  synced, the `Default` profile) is now archived to `Documents\Obsidian\browser_data\iPhone` as one verified
  `.md` per page. Added **`becky-radar --list`** (the all-synced URL feed, not just model/tool hosts) and a
  NEW **`becky-clipcheck`** that re-fetches each page and deterministically scores recall/precision to
  confirm the `.md` actually CONTAINS the page (local Gemma-4 only on the borderline "partial" — AI only
  where necessary). `scripts/clip-sync.ps1` chains radar->web2md->clipcheck one page at a time, idempotent
  via a manifest; `scripts/register-clip-sync-task.ps1` installs the **daily 5 PM** task with missed-start
  catch-up. Proven 8/8 on real pages; full 30-day backfill (207 pages) run one-at-a-time-verified.

- **Fixed the 3 broken self-regulate siblings (2026-06-26, local):** becky-resolve, becky-presence,
  becky-case all COMPILED + unit-passed but were broken at RUNTIME on a real file. Root causes: a
  `becky-validate --variant <x>` flag that doesn't exist (so the Gemma ladder never escalated — in
  becky-resolve + becky-presence); `becky-identify` run with no required `--kb` (naming always degraded);
  becky-resolve using raw `exec.Command` (couldn't find the sibling in `bin/`); becky-presence never
  gathering transcribe/motion; and becky-case ("the one dumb call") running NOTHING on a bare `--file`.
  All three now route through `internal/forensicrun` (exported `NewGemmaLadder`/`ResolveKB`/`RunTool`; the
  presence watch is now subject-aware). PROVEN on `fixture_2spk.wav`: each finds + runs its siblings, the
  ladder fires both E4B+12B levels, and lone signals are HELD not falsely named. Swept the rest — the
  `--variant` bug was confined to those two tools; no other broken/stub tools in cmd.

- **becky now SELF-REGULATES the forensic protocol (2026-06-25, local):** integrated the additive cloud
  branch `claude/ai-daw-integration-hh5y8l` (the same branch name, a NEW wave on top of the WPF work) —
  a deterministic protocol-ENFORCEMENT engine `internal/orchestrate` (+ `internal/forensic` tool→claim
  mapping) that FORCES becky's invariants in code: corroborate-then-conclude (≥2 independent signals to
  name/conclude, a lone signal stays a "candidate"), **presence needs a `KindWatched` signal** (a
  transcript mention or motion burst NEVER proves on-screen), and a forced Gemma-4 E4B→12B validate
  ladder. Three new entry tools wrap it — `becky-case` (the "one dumb call": file in → final
  corroborated report, diarize-conditional plan), `becky-resolve` (self-regulating identity resolver
  with a real `becky-validate`/`becky-identify` ladder + degrade-never-crash), `becky-presence`. Plus
  a launcher ASCII-only gate now ENFORCED in CI + pre-commit (`scripts/check-launchers.sh`). `becky-mcp`
  was added then **rejected/removed** (becky self-orchestrates instead). All gates green (build/vet/test;
  new `.exe`s build; only the documented `cmd/tts` environmental FAIL).

- **Self-regulate WIRED into the entry verbs + PROVEN on a real clip (2026-06-25, local):** the
  orchestrate engine now drives `becky-transcribe` and `becky-ask` through one shared runtime package
  `internal/forensicrun` (single source; mapping stays in `internal/forensic`). `becky-transcribe
  --forensic [--subject X] [--speakers N] [--kb dir]` adds a corroborated `"forensic"` block (opt-in, so
  existing consumers are unchanged); `becky-ask --question "who is in this?" --target <file>` (single-shot)
  returns ONE corroborated answer (the colored TUI is left untouched so a model run never freezes it).
  **Proof on `fixture_2spk.wav`:** multi-speaker plan included diarize; `becky-identify` ran vs `kb-final`,
  matched one weak voice signal, and the engine HELD it (`names: null`, *"needs a second independent
  source"*) with the Gemma-4 E4B->12B ladder firing both levels — no false naming. Fixed two real bugs
  while wiring: the ladder escalates via `BECKY_AVLM_VARIANT=12b` env (not a non-existent `--variant`
  flag — `becky-resolve` has this latent bug), and the runtime now passes `--kb` to identify (env
  `BECKY_KB` -> `kb-final`), without which naming always degraded. 8 value-asserting `forensicrun` tests
  green. **Left for local:** point `BECKY_KB` at a real case KB (enrolled faces+voices) and confirm a
  2-modality match CONCLUDES a name on real video; tune identify thresholds + validate window-targeting.

- **Native becky GUI = WPF, window verified (2026-06-25, local):** integrated the additive cloud branch
  `claude/ai-daw-integration-hh5y8l` — new `becky-catalog --json` (Go) + `gui/BeckyWindow` (a native
  **WPF** tool-runner). Built + launched + mouse-clicked + screenshotted by the local agent: opens
  high-contrast, loads the **live 18-tool catalog** (tier-colored), clicks register, degrades cleanly,
  no freeze. Launcher `Open Becky Window.bat` fixed to put `becky-go\bin` on PATH. Ratifies Jordan's
  WPF decision (window shells out to existing `becky-*.exe` — single-tool principle intact; supersedes
  the Go+Gio canvas attempts, which are kept dormant, not deleted). Left = one real model-heavy tool
  run on footage (Jordan's tap).

- **Cloud queue drained (2026-06-24, local):** integrated three diverged cloud branches —
  `fix-editmodel-digest-pathx` (the pathx CI fix, fixes red Linux CI), `scout-autonomous-spec-proposals`
  (becky-scout `--propose` gate: Qwen proposes → Gemma judges → queue-only daily watch), and
  `ai-daw-integration` / **becky-voice Phase 0** (new `internal/catalog`, `workflowdef`, `voiceresp`,
  `voicerules` + `SPEC-BECKY-VOICE.md` / `HANDOFF-BECKY-VOICE.md`, design+scaffolding, fully unit-tested).
  All gates green; left-for-local items are the per-branch model/hardware gates noted below.

- **becky-canvas usability fixed:** no console-flash on clicks (`proc.NoWindow` everywhere),
  **Spacebar = play/stop**, drum machine updates **live** while playing (debounced relaunch), and a
  **Speak** toolbar button — GGUF **NeuTTS Air on the GPU** via a warm server (`tts_server.py` on
  :11436), ~6–8s/utterance (~14× faster than CPU). Env set persistently
  (`BECKY_TTS_MODEL=neutts-air-Q4_0.gguf`, `BECKY_TTS_BACKBONE_DEVICE=gpu`).
- **becky-tts has a real voice** (NeuTTS Air, Apache-2.0; isolated venv `models/tts/venv`).
- **9-tool cloud swarm installed** (cloud-verifiable half each, deterministic cores green): becky-tts,
  identify voice-ID hardening, becky-dates, becky ingest, becky-location, framematch hardening,
  face-crop+db, becky-ask single-shot, face-naming loop. Each tool's spec **§8** has the exact local
  model-wiring checklist + the one-command offline proof cloud already ran.

### Pending for Jordan (hardware "hear/see" gates only he can close)

- Open the new **becky window** — double-click the Desktop shortcut **"Becky Window"** (launches the
  program directly, NO console). It opens with the tool list; click **Pick file...**, choose a real
  video/audio file, then click a **green** tool (e.g. becky-transcribe) and watch the result fill the
  box. (The window, catalog, clicks, degrade path, the self-locating-tools fix, the bring-to-front,
  AND the `Open Becky Window.bat` parse-error fix are all verified by the local agent — the window
  opens both from the shortcut and the `.bat`; this last step is just the first real model run on your
  footage.)
- Open **becky-canvas** → confirm no console flash on any click; press **Space** (plays/stops); in
  Drum, ▶ then toggle cells (hear them update live); click **Speak** (first click warms ~30s, then
  judge the GGUF voice quality + speed).
- Forensic threshold tuning on his **private case footage** (can't be faked on synthetic data):
  identify voice-ID `0.75 / 0.06` thresholds (real CAM++ audio with known speakers); becky-location
  ORB + framematch ROI fractions (real rooms); face-crop margins + face-naming enroll (real faces + a
  GPU enroll run). Deterministic cores are built + unit-test-green; what remains is the model boundary
  named in each spec's §8.

### This session (2026-06-23, local) — IN-PROCESS Gemma-4 (llama.dll) + dimensions fix

Detail in `HANDOFF-LOG.md` (top) + `HANDOFF-SHOTCUT-FORK.md` (session 3). Jordan: stop deferring
the in-process llama — do it now. Done + verified:
- **In-process Gemma-4 QAT via llama.dll (cgo), wired into becky-edit.** New build-tagged
  `internal/llamacpp` (`//go:build llamacgo`; pure-Go stub by default so CI/cloud stay cgo-free) +
  a thin C shim on the new llama.cpp API. `cmd/becky-edit` prefers it (warm llama-server is the
  fallback). Builds via `scripts/build-becky-edit-llama.ps1` (gendef/dlltool import libs +
  `-tags llamacgo`). **Proof:** Gemma loads 43/43 layers on CUDA in ~2s; the agent loop ran
  in-process and emitted `search`→`add_clip`→`timeline.append`. Launcher now puts
  `C:\llama.cpp\build\bin` on PATH (the load-time llama.dll link). The MSVC llama.dll links from
  mingw cgo because its `extern "C"` API is Win64-ABI.
- **Project-dimensions bug FIXED (becky-shotcut 615dd55):** a vertical clip now makes a vertical
  project (verified 1080x1920 30fps), via `Mlt::Profile::from_producer` on the first import.
- **LEFT:** the remaining HostCommand verbs (trim/move/split/filter/render/grab/track) — the exact
  source-verified Shotcut-call map is in `HANDOFF-SHOTCUT-FORK.md` §3 (session 3). Go side already
  emits them; only the `beckydock.cpp` call mapping + a clip-id→QUuid map remain. Also: tune
  `internal/ctlagent` so the 4B stops once the goal is met (it over-iterated once).

### This session (2026-06-23, local GUI) — becky-edit Shotcut fork: ALL reported bugs FIXED + verified

Detail in `HANDOFF-LOG.md` (top) + `HANDOFF-SHOTCUT-FORK.md` (session 2). The local agent drove
Jordan's real mouse/keyboard to reproduce + fix every issue from his test:
- **New-project "error saving" + preview/add failures had ONE root cause:** Shotcut found ZERO MLT
  plugins (it resolves its repository from the exe dir, not `MLT_REPOSITORY`). Fixed by deploying the
  MSYS2 MLT modules into `build/lib/mlt` (+`deploy-mlt.sh`). New project SAVES, preview PLAYS — verified.
- **qtblend / "Entry Point Not Found" dialogs:** `libmltqt6` needed `Qt6Core5Compat.dll` (else it pulled
  kdenlive's incompatible `icuuc78.dll`). `pacman -S mingw-w64-x86_64-qt6-5compat mingw-w64-x86_64-libebur128`
  fixed it — all 22 modules load, qtblend works.
- **Add-to-timeline rewired** (`beckydock.cpp`): producer-based `MAIN.open(Mlt::Producer*)` instead of the
  document-open `MAIN.open(QString)` (which prompted + dropped the clip). **Clip now lands on a V1 track.**
- **Dock layout:** min-size + tabified with Playlist (was a sliver). Use *View > Layout > Restore Default
  Layout* for the full new default.
- Rebuilt `shotcut.exe` (becky-shotcut master `acffd2b`, local — origin is upstream, not pushed). Native
  Windows (MSYS2/MINGW64), NOT WSL2. **Next increment:** wire the remaining "(pending)" HostCommands
  (trim/split/move/filter/render) — Go side proven, only the Shotcut call-mapping remains.
### This session (2026-06-23, `claude/scout-autonomous-spec-proposals`) — becky-scout autonomous build gate

Full detail in `HANDOFF-LOG.md` (top entry). In brief: added `becky-scout --propose` — Jordan's
"let the models decide" loop. Local **Qwen proposes** a concrete becky tool for each surfaced video,
**Gemma‑4 independently votes**, and only proposals both back become **becky-new-tool intakes**
(`--build` runs the factory; default emit-only). Deterministic core (`internal/scout/propose.go`)
fully unit-tested; real models in `cmd/scout/model.go` (llama-server, degrades without GGUFs). Gates
green; degrade path cloud-verified. Per Jordan (2026-06-23): **queue-only** (no auto-build) and
`scout-watch.ps1 -Register` installs a **DAILY** task. **Left for local:** run `--propose` with the
GGUFs present + double-click `scout-watch.ps1 -Register`. (Unrelated red CI on PR #22 was a
pre-existing `editmodel` Windows-path bug — fixed separately in PR #24.)

### This session (2026-06-23, `claude/becky-edit-gemma4`) — BUILT becky-edit's engine + the Gemma-4 QAT upgrade

Full detail in `HANDOFF-LOG.md` (top entry). In brief:
- **Gemma-4 QAT swap (verified against the live HF cards first):** default AVLM is now the **E4B-it
  QAT `UD-Q4_K_XL`**, with the **12B-it QAT** as a runtime alternate (`BECKY_AVLM_VARIANT=12b`).
  `internal/config` resolves QAT-first with a legacy fallback; `scripts/get-gemma4-qat.ps1` pulls the
  exact GGUFs. Local gate: download + verify VRAM/tok-s on the 3070 (esp. the 12B).
- **becky-edit (the NLE) — Go ENGINE LAYER BUILT, proven offline.** `internal/editmodel` (shared live
  state) + `internal/edittools` (deterministic tool allowlist) + `internal/ctlagent` (multi-step agent
  loop, shared with the DAW) + `cmd/becky-edit` (NDJSON bridge; built-in Gemma-4 QAT; state synced from
  BOTH the model and human edits). `becky-edit --selftest` is the one-command proof (exit 0; `.exe` runs).
- **Two research subagents:** `research/shotcut-api.md` (real Shotcut/MLT API + the HostCommand->call map)
  and `research/director-videodb-mining.md` (validated the agent-loop shape; future ideas -> roadmap).
- **Gates green** for everything touched (the lone `cmd/tts` test FAIL is pre-existing/environmental).
- **NEXT (local, host-dependent):** fork Shotcut + the Becky QML dock per `SPEC-BECKY-NLE.md §8`.

### This session (2026-06-22 → 06-23) — slim + a STRATEGIC PIVOT + two priority specs

- **Slimmed this file:** moved the full §6 history (≈131k chars) to `HANDOFF-LOG.md`; CLAUDE.md is
  back well under the limit. No information lost. Added `becky-canvas --render-frame <png>` — the
  off-screen "see the canvas without opening it" loop (gui_render.go, verified).
- **THE PIVOT (Jordan, 2026-06-23):** stop hand-building pro DAW/NLE GUI widgets in Gio (it has ZERO
  DAW widgets — the root cause of weeks of "toys"). REAPER/kdenlive *driving* is OUT (kept dormant,
  not deleted). New direction = **ADOPT a mature host and add the becky layer**; becky's Go engine/
  brain stays + becomes the toolset. Settled by the research below (6 docs in `research/`).
- **NLE (build FIRST) → `SPEC-BECKY-NLE.md`:** adopt **Shotcut** (MLT — becky already writes it) + a
  Becky dock reusing the becky-clip engine (folder → `.srt` search → single-click=preview-play,
  double-click=clip-to-timeline) + runtime extensibility (becky CLIs as tools, no host recompile).
- **DAW (after NLE) → `SPEC-BECKY-DAW.md`:** spike-first — **B adopt OpenDAW** vs **C giu/Dear ImGui
  (all-Go) + DawDreamer/sidecar engine**; build `internal/ctlagent` (multi-step agent loop) regardless.
- **Research (all in `research/`):** go-gui-iteration-and-design-tools, existing-oss-we-keep-reinventing,
  go-packages-explained-for-jordan, daw-nle-strategy-feasibility, opendaw-adoption-plan,
  reference-projects-gap-analysis, daw-video-timeline-gui-components, + 3 `bookmarks-*-crawl` (mined
  Jordan's curated Chrome bookmarks: no OSS DAW beats OpenDAW; his saves lean giu/ImGui; Shotcut for NLE).
- **NEXT (a build agent):** `SPEC-BECKY-NLE.md` Phase 0 — build Shotcut on the PC + a minimal Becky
  dock (the go/no-go spike), then wire the bridge. Honest verify (it opens + the named interaction
  works on a real folder), not "compiles."
