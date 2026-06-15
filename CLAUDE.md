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

---

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

- **Corroborate, then CONCLUDE — don't hedge.** ≥2 independent signals agreeing →
  state the conclusion plainly. A lone weak signal → "unknown"/candidate. A flood
  of maybes a human must sort = tool failure.
- **Recall is for DETECTION, not NAMING.** Surface every face/voice; attach a NAME
  only when corroborated.
- **Offline + deterministic.** No network at runtime; same input → same output
  (fixed seeds). The only "AI in the loop" is an explicit local model call.
- **Degrade, never crash.** Missing model/ffmpeg/audio → typed degrade error and a
  partial result, not a panic.
- **Paths may be Windows paths even when running on Linux/CI.** Use
  `internal/pathx` (separator-agnostic Base/Dir), not `filepath.Base` on a value
  that originated as a `C:\...` path. (This is why CI is green on Linux.)

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
- `CLAUDE.md` (this file) — how we work + live handoff.
- `COLLAB-PROTOCOL.md` — how the two agents (cloud + local) share this repo without
  clobbering: lane rules, the work registry (claim before you build), and the async
  inbox between us. Read before committing.
- `README.md` — project overview, tool catalog, non-obvious decisions.
- `SKILL.md` — how to *use* the tools (human + agent usage guide).
- `FORENSIC-OUTPUT-PHILOSOPHY.md` — how findings must be reported. Governs every
  human-facing output.

**Specs (read the one for the tool you're building):**
- `SPEC-BECKY-ASK.md`, `SPEC-BECKY-NEW-TOOL.md`, `SPEC-OCR.md`,
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

**Historical / inbox (context only — not current instructions):**
- `PROGRESS.md` — build-loop tracker/log.
- `TEST-FEEDBACK.md` — hand-off inbox from the test agent.
- `TRANSCRIPT-GAP-FINDINGS.md`, `MORNING-BRIEF-2026-06-09.md` — dated R&D notes.

> If this list and the files ever disagree, this list wins — tell Jordan so it can
> be corrected. New planning docs should be linked here so the root never becomes
> "scattered .md files" again.

---

## 6. Live handoff — current branch status

**Branch `local/build-everything-2026-06-15` (local, 2026-06-15) — "build everything" pass, merged to master.**
Jordan approved (a) auto-building any normal offline tool without asking, only
gating the rule-breaking ones, and (b) building ALL the queued specs, via
parallel subagents. 12 new tools/foundations were built — each a self-contained
new cmd/ + internal/ package with table-driven tests; whole module
`go build/vet/test ./...` green; network/model/native boundaries stubbed behind
interfaces (the documented cloud→local wiring contract). Shipped in 3 waves:
- **Wave 1 (offline, fully done):** `becky-radar` (Chrome/iPhone-history → freshness
  cross-ref), `becky-handoff` (Go port of the Get-Becky-Updates button),
  `becky-vision` (LFM2.5-VL GGUF wrapper via llama-mtmd-cli), + a pure-Go **SMF
  reader** in `internal/music` (MIDI round-trips — the DAW foundation).
- **Wave 2 (online/agent, Jordan-approved rule-breakers):** `becky-research`,
  `becky-palantir`, `becky-harness`, `becky-omni`. Deterministic cores; the
  network/Pi/Omnigent/OpenPlanter calls are stubbed for local wiring.
- **Wave 3 (music/DAW/canvas cores):** `becky-mix` (JST mix.json), `becky-hum`
  (K-S key + tempo + hum→MIDI), `becky-vox` (DTW vocal align), `becky-daw`
  (editable arrangement on the SMF reader), `becky-canvas` (scene model),
  `becky-daw-engine` (device-select rule + transport). **Visual-first + a
  corrections-log preference-learning substrate are first-class.**

*Left for local (real-hardware wiring, NOT cloud's job):* the explicit Phase-2
**native step** — real-time cgo audio (miniaudio/WASAPI) for `becky-daw-engine`,
the ImGui/cgo window for `becky-canvas`, and the audio→features DSP for
`becky-hum`/`becky-vox`. **See `X:\AI-2\dawbase` — a separate MIT C++ DAW (same
"95% deterministic, 5% taste" philosophy) that already has the real
Krumhansl/FFT key+tempo DSP (`analysis.cpp`), miniaudio mic capture with pre-roll
(`capture.cpp`), and a habit-learner (`habits.cpp`). These slot into exactly the
stubs above — port `analysis.cpp`→Go for becky-hum (pure-Go, no cgo) and reuse
`capture.cpp` as the native AudioBackend helper.** Also: run the new tools on real
inputs, smoke-test `becky-vision` on the 1.6B model, and validate each model
stub. The 12 tools each ship Open-Decisions for Jordan at the end of their SPEC.

---

**Branch `claude/omnigent-and-collab-protocol` (cloud, 2026-06-15) — MERGED to master (commit 9e1748c); local ratified the protocol (COLLAB-PROTOCOL inbox).**
Contains two things; please review before merging (the button should launch local
Claude here, not fast-forward, because this line says review is pending):
- **`SPEC-OMNIGENT.md`** — `becky-omni`, the Omnigent meta-harness above Pi (Jordan
  explicitly asked for it). Design only, no Go code. Reconciles with `becky-harness`.
- **`COLLAB-PROTOCOL.md` + §4 anti-collision rules** — the new two-agent protocol,
  authored after the 2026-06-15 merge incident. **Local agent: please read it and
  ratify or amend (reply in its inbox).** Built on top of current `master`, additive
  only — nothing of local's work is touched or reverted.

*Left for local:* review + ratify the protocol, settle the `becky-freshness` ↔
`becky-research` overlap (see COLLAB-PROTOCOL INBOX-1), then merge. No build needed.

---

**Branch:** none active — `claude/affectionate-pascal-z35plh` was merged into
`master` on 2026-06-15 (merge commit `7dbe61d`) and the branch was deleted
(local + remote). Its earlier infra/CI/pathx work had already shipped via PR #1.

**Done (now on `master`):**
- `.github/workflows/ci.yml` — build + test + vet + gofmt on Ubuntu + Windows.
- `internal/pathx` (separator-agnostic Base/Dir) + tests.
- Windows-path unit-test fixes (export `defaultOutput`, osintexport
  `deriveFFprobe`, avlm frame-file labelling). Suite green on Linux + Windows.
- This `CLAUDE.md` as the canonical front door + handoff protocol.
- The three tool specs below (design only — no Go code yet).

**Left for local agent:** nothing — merged after `go build`/`go test` passed green
on Windows (go1.26.1). The three specs below await Jordan's go/no-go before any
code is written (each opts out of the offline invariant — see decisions).

**Three new tool specs drafted (design only — NOT built):**
- `SPEC-DEEP-RESEARCH.md` → `becky-research`: plan → fan-out search → fetch+cache
  → RRF rank/dedup → verify → cited synthesis. Deterministic Go orchestrator +
  thin local-model helper stub; content-addressed source cache for reproducibility.
- `SPEC-OPEN-PALANTIR.md` → `becky-palantir`: thin Go wrapper that prepares becky's
  existing evidence outputs, drives the OpenPlanter project (`ShinMegamiBoson/
  OpenPlanter`) to build a cross-evidence entity graph, and normalizes its output
  into a becky graph schema. Pure-Go `cooccur-only` deterministic floor underneath;
  default offline; web enrichment opt-in + logged.
- `SPEC-AGENT-HARNESS.md` → `becky-harness`: deterministic Go orchestrator
  (`cmd/harness/` + `internal/pirun/`) that drives a Pi agent (`earendil-works/pi`)
  headless over a per-run, default-deny allowlist of becky tools. Universal per
  request (declared tools/model/skill/goal). Faked-Pi unit tests on the cloud side.

Each spec documents the cloud-vs-local build split and an explicit integration/
helper stub contract, so the local agent only wires the model/binary boundary.
Each tool is a NEW class for becky — agentic + online — that opts out of the
offline invariant via an explicit, logged network step while keeping a
deterministic OUTPUT format and degrade-never-crash behavior.

**Open decisions for Jordan are listed at the end of each spec** (search backend,
online-vs-cached default, OpenPlanter license/release pinning, Pi auth/local-model,
which workflows to template first). No Go code written yet — specs are for review
before scaffolding.

**Shipped 2026-06-15 (on `master`, direct local-agent work):**
- **`becky-freshness`** (`cmd/freshness` + `internal/freshness`) — the systemic fix
  for "we missed an upstream model update": a manifest of every external model/tool
  becky pins + a checker that reports what's newer upstream (HF/GitHub/PyPI). Run as
  standard practice. Built + unit-tested + verified live (it flagged PP-OCRv6).
- **`becky-ocr` → PP-OCRv6**: the helper now requests PP-OCRv6 newest-first, auto-
  degrading v6→v5→v4 (the model Jordan flagged in iPhone Chrome). Activating v6 needs
  a rapidocr build that knows `PPOCRV6` + the v6 ONNX weights; safe fallback otherwise.
- **`becky-compose`** (`cmd/compose` + `internal/music`) — deterministic, genre-aware
  multi-track MIDI generator. Genre profile DB (`internal/music/profiles/*.json`) so
  becky "already knows" a genre; emits per-track .mid stems + song.mid + project.json
  routing (loads into SPEC-BECKY-CANVAS's DAG). Pure-Go, offline, tested
  (VLQ/theory/determinism/SMF-parse). Genres: crunkcore, digicore, hyperpop (+ metalcore,
  crabcore landing). `SPEC-BECKY-COMPOSE.md`.
- **`becky-transcribe` GPU auto-fallback** + the autonomous "Get Becky Updates" button
  fix (see earlier commits this day).

**Diagnosis (the iPhone-OCR miss):** Jordan opened PP-OCRv6 in iPhone Chrome as the
example for a tool that reads his browser history to surface updates. That tool was
**never built** (only listed here as queued) — root cause of the miss. It is now
specced as **`becky-radar`** (`SPEC-RADAR.md`): reads the local desktop Chrome
History DB (which carries synced iPhone visits) and cross-references the freshness
manifest. Not built yet.

**Still queued (planned):** **becky-radar** (specced, build pending); **becky-canvas**
(`SPEC-BECKY-CANVAS.md`, native creative GUI — specced, build pending); **becky-handoff**
(cross-platform replacement for `get-becky-updates.ps1`); a **becky-ask UX overhaul**
(clipboard/drag-drop/mouse/clickable). Requested by Jordan 2026-06-14/15.
