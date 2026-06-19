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
do the thing.
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

**One-click `.bat`/`.ps1` launcher scripts MUST be ASCII-only** (no em-dashes `—`, smart
quotes, en-dashes, etc.), and every user-facing `.bat` must end with `pause`. A double-clicked
`.bat` runs Windows **PowerShell 5.1**, which reads a BOM-less `.ps1` as the system ANSI
codepage — so a single stray Unicode char makes the whole script fail to PARSE and the window
flashes shut with no visible error. This silently broke both `Build Becky Clip.bat` and the
cloud-written `Build Becky Drum.bat` (fixed 2026-06-18). Before shipping a launcher, parse-check
it under 5.1: `powershell -Command "$e=$null;[void][System.Management.Automation.Language.Parser]::ParseFile('x.ps1',[ref]$null,[ref]$e);$e"`.

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
- `CANVAS-INSPIRATION.md` — design-research brief for becky-canvas (Jordan's GUI):
  starred-repo mining + reference apps (infinite-kanvas, ACE-Step-DAW, DAW-Copilot,
  cate, jsoncrack, blocksuite, the "show me, don't do it" overlay). Read before any
  becky-canvas GUI/agent-UX work — the research is done, don't redo it.

- `SPEC-BECKY-CLIP.md` + `BECKY-CLIP-HANDOFF.md` — becky-clip, the forensic transcript-based
  video COMPILATION editor (WebView2 GUI + Go engine). The spec is *what it is*; the HANDOFF is
  *how to change it without re-making solved mistakes* (gotchas, non-obvious logic, dead ends
  already ruled out). **Read the HANDOFF before touching becky-clip.**

**Specs (read the one for the tool you're building):**
- `SPEC-HANDOFF-HARDENING.md` (**ASSIGNED TO CLOUD, 2026-06-17 overnight** — make the
  "Get Becky Updates" button drain the whole branch queue, self-heal a poisoned tree,
  and detect two branches editing one tool; the union-merge doc fix already shipped).
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

**Handoff installed by the local "Get Becky Updates" agent (local, 2026-06-19) — merged `claude/drum-machine-honest-spec` Phase-1 foundations to master; drained the cloud queue.**
The update button punted to the local agent (2 cloud branches waiting + uncommitted local WIP, so no clean fast-forward). Installed the green, additive work:
- Merged `claude/drum-machine-honest-spec` (14 commits) into master: `internal/{sampledecode,sampler,kitimport,samplelib}` pure-Go drum foundations + `SPEC-BECKY-DRUM.md`/`SPEC-MASCHINE-CLONE.md` + 10 cited `research/` docs. **Whole module `go build`/`go vet`/`go test ./...` green** on this Windows machine (gofmt `-l` flags only CRLF from `core.autocrlf=true`; content is gofmt-clean once CR is stripped — cosmetic per §4).
- That merge also carried the `fix(quotes)` CI fix (TestDeriveOutPath `filepath.Base` on a Windows path → green Linux CI), which is **byte-identical** to `claude/fix-quotes-winpath-ci` — so that standalone branch is fully **subsumed**. Both `claude/*` branches deleted (local + remote).
- The in-progress local WIP (investigate-mode for the agent vault search: `cmd/clip/bridge.go` + `internal/assistant/*`) was stashed for the merge and **restored** afterward — untouched, still uncommitted.

**Honest scope:** only the drum-machine **Phase-1 foundations** landed. They are intentionally **orphan packages** (not yet wired to GUI/engine/AI). The branch's own handoff (below) hands **Phases 2-4** (oto/v3 audio engine, Gio window + piano roll, Qwen tool-call chat-control) to the local agent — that is a separate, larger build, NOT done by this update run. The drum machine does not yet open or make sound; that work remains. `build-all-tools.bat` adds no new `.exe` (no new `cmd/*`).

---

**>>> CLOUD: START HERE (overnight task assigned 2026-06-17). Build `SPEC-HANDOFF-HARDENING.md`.**
Jordan hit a critical update-button failure today (7 cloud branches piled up; the button
installs only 1 per click and stalls on logbook-file collisions). Local already shipped
the core fix (`.gitattributes` union-merge, on master) and drained the backlog. Three
hardening items remain and are now your task: (1) drain the WHOLE queue per run, (2)
self-heal a poisoned/half-merged tree, (3) detect two branches editing one tool. Full
contract, function signatures, constraints, and Definition of Done are in
**`SPEC-HANDOFF-HARDENING.md`**. It's a normal offline/deterministic tool — build it on a
`claude/handoff-hardening-*` branch, all tests green, and mark §6 ready for local.

---

**Branch `local/becky-clip-render-audio-2026-06-19` + `local/becky-clip-audio-corroboration-2026-06-19` (local, 2026-06-19) — becky-clip render: KEEPS AUDIO, saves to `<folder>/render`, and AUTO-CORROBORATES every export. MERGED to master.**

Jordan's round-5 feedback (paraphrased + verbatim): "there's no fucking audio on the render. Also,
why are you saving that to an app data folder? … We are going to build a new folder called Render."
Plus the deeper point: USE the becky-tools models for COMPREHENSIVE, CORROBORATED testing (he has
API keys + the Gemma-4 E4B audio/visual model wired in becky-validate) — "no one datapoint, but many,
corroborated before wasting a human's time." All addressed; `go build/vet/test ./...` green.

- **AUDIO (the bug):** the render was DELIBERATELY silent (`-an` + `concat …a=0`, commented "a visual
  record"). Wrong for a quote tool. `internal/reel` now keeps each clip's audio (per-clip
  aresample/aformat → interleaved `concat v=1:a=1` → AAC 192k); clips lacking an audio stream get a
  silent `anullsrc` fill bounded to the clip duration so concat never errors. Gated on ffprobe
  (`mediainfo.HasAudio`); no-ffprobe degrades to silent-WITH-a-note. Old `-an` path kept behind
  `resolvedOpts.Audio=false` so existing pure-arg tests are untouched; +3 audio tests.
- **OUTPUT LOCATION (the protocol breach):** export/frames/EDL/SRT defaulted to
  `os.TempDir()/becky-clip` (AppData — invisible to humans). Now `App.renderDir()` → a `render`
  subfolder of the OPEN case folder (new file in a new subfolder; no original touched). The
  Becky Tools protocol: outputs live next to the originals.
- **AUTO-CORROBORATION (the "earn trust" feature):** after every export, `verifyExportAudio` re-opens
  the output READ-ONLY and confirms AUDIBLE audio via TWO signals — ffprobe (stream present) + new
  `mediainfo.MeanVolume` (ffmpeg volumedetect, mean above the −80 dB silence floor). `ExportResult`
  gains `AudioOK`+`Audio`; the GUI shows "✓ audio confirmed: mean −21.3 dB (audible)" or a loud
  "⚠ AUDIO" warning. A silent render can never ship unnoticed again.

**VERIFIED end-to-end on the REAL deployed exe (CDP) on `E:/TakingBack2007`** — search penguin → add
2 clips → Export → `E:\TakingBack2007\render\untitled-compilation_reel.mp4`. FOUR corroborating
signals: ffprobe (AAC stereo 48 kHz), ffmpeg volumedetect (mean −21.3 dB / peak −2.4 dB), becky-
validate VAD (91.1% speech), and becky-validate **Gemma-4 which HEARD "I want Penguin"** (matches the
search term) — the full forensic loop proven by an independent audio model. Evidence:
`becky-clip-work/{verify_render.py,render-*.png,validate_render.json}`. Details + gotchas (#33-35) in
`BECKY-CLIP-HANDOFF.md` ROUND-5. **Left for local: nothing** — built + corroborated. Honest minor: the
render output, being inside the case folder, re-indexes as a video chip on reopen (harmless; it IS a
video). NOT yet pushed to GitHub.

---

**Branch `local/becky-clip-chatfreeze-2026-06-19` (local, 2026-06-19) — becky-clip: the "search works once then frozen" bug + the broken AI chat, both ROOT-CAUSED, fixed, and verified LIVE on the deployed exe + real folder.**

Jordan's round-4 feedback: search "works ONCE then permanently stuck until I restart"; the AI chat is
"fucking broken"; "let me use an api key or my claude code oauth … I'll need to debug WITH the built
in agent." Both fixed; `go build/vet/test ./...` green, gofmt-clean (my new files), `node --check`
clean, the real `becky-go/bin/becky-clip.exe` rebuilt via `build-becky-clip.ps1` (+ Desktop icon),
and BOTH fixes verified by DRIVING THE REAL WINDOW via CDP on `E:/TakingBack2007` (484 videos):
search penguin(227)→click a result→money(400)→cat(400) updates every time; chat answers via Claude
with a visible "via Claude" badge+note. Evidence: `becky-clip-work/verify-*.png` + `cdp_verify.py`.

- **The freeze was the BRIDGE, not search.** go-webview2 runs a bound function SYNCHRONOUSLY on the
  WebView2 UI thread, so the old `w.Bind("beckyCall", app.Call)` ran every verb on that thread. A fast
  verb (search) was fine, but clicking a result fires `media_url`→`reel.Proxy`→ffprobe/ffmpeg on a
  multi-GB file ON the UI thread → froze the window → every later call (incl. the next search) queued
  forever. FIX: the bind now ENQUEUES — runs `app.Call` on a goroutine, resolves the page promise via
  `window.__beckyResolve` + `w.Dispatch`/`w.Eval` (`cmd/clip/window_gui.go`, `app.js bridgeSend`). UI
  stays live during ffmpeg/ASR/Claude; calls run concurrently. This one change fixes the freeze for
  ALL slow verbs (export/transcribe/proxy/probe/chat).
- **Chat is now a real Claude assistant.** New `assistant.Router.Assist` (a chat brain, separate from
  the action-only `Handle`): Tier-0 commands run instantly, "find every time X" runs the funnel, and
  any other message is ANSWERED by the best available model. `App.Ask` uses it; `online` defaults ON;
  the toggle is relabelled "use Claude"; a `status` verb + intro line shows the live backend. The
  `claude -p` invocation is made lean/usable (`--strict-mcp-config --mcp-config {} --tools "" --system-prompt`)
  → ~15-25s answers on OAuth (the default boot was ~100s+ and hung). API-key path also wired
  (`anthropic_key.txt` file or `ANTHROPIC_API_KEY`; alias→real-id via `resolveAPIModel`). 6 new
  assistant tests; the existing router tests untouched/green.

Details + the new gotchas (#29-32: never bind a slow fn to webview2; the lean `claude -p` recipe;
the `--bare` trap; the CDP recipe) are in `BECKY-CLIP-HANDOFF.md` ROUND-4. **Left for local:
nothing** — built + verified on the real exe. Honest caveats: an opus chat turn is ~15-25s (a
"thinking…" spinner shows; the async bridge keeps the window live); a no-key user relies on the
`claude` CLI being signed in (the status line says so plainly if it isn't). NOT yet pushed to GitHub.

Follow-up (same day): killed the re-transcribe button's FALSE "overwrites the .srt" tooltip — it
now reads "writes a SEPARATE <name>_LOCAL.srt; your original transcript is never touched" (the
behavior was always safe — local ASR only ever writes `_LOCAL.srt` — but the text lied and made it
feel unusable). Locked in by `TestRetranscribe_BareSrt_NeverOverwritten` (sha256-proves a bare
`<stem>.srt` is byte-identical after a local re-transcribe) and verified on the live exe (the tooltip
no longer contains "overwrites").

---

**Branch `local/becky-clip-fix3-2026-06-18` (local, 2026-06-18) — becky-clip: visible quotes + no console-flash + caption/edit-detection pipeline + forensic non-overwrite + real NLE timeline. MERGED to master + pushed.**

Jordan's 3rd round of real-folder feedback (`E:\TakingBack2007`, 484 videos + 418 yt-dlp `.en.srt`).
Two acute bugs + four new requirements, ALL fixed and verified by driving the live window on his
real folder (CDP). Built via parallel subagents + orchestrator integration; `go build/vet/test ./...`
green, gofmt-clean, `node --check` clean.
- **Search "showed a count but the sidebar never changed":** the `.video-picker` had no height cap →
  with hundreds of videos it grew ~15000px and pushed `#results` off-screen. Capped it (max-height
  34vh, own scroll); results now visible (227 penguin quotes, first at top 442 of a 761px window).
- **Console-window FLASH on every video click** (seizure-inducing): windowsgui parent + console-app
  children. `internal/proc.NoWindow` (CREATE_NO_WINDOW) on every child exec in the GUI chain (reel
  ffmpeg/ffprobe, mediainfo, becky-transcribe + its ffmpeg/python/vad). Verified: no conhost on click.
- **Caption-first transcription + YouTube-edit detection** (`becky-captions` = `cmd/captions` +
  `internal/captions`): before local ASR, check for a trustworthy official transcript (existing
  `<stem>.en.srt` or yt-dlp-fetched by `[id]`, placed same-folder/same-naming) and compare its
  coverage to the VIDEO duration (`>=0.90` → use official; short = he YouTube-edited out segments →
  local). Verified live (`[46T0KmQA7Eg]` ratio 0.999 → use_official; a no-srt video fetched cleanly).
- **Forensic non-overwrite:** local ASR writes `<stem>_LOCAL.srt`, NEVER an official `.srt` (verified
  sha256 unchanged). Both versions coexist. footage recognizes `_LOCAL.srt`.
- **Real NLE timeline:** drag a video chip onto the timeline (adds it, in=0/out=probed); drag clips
  to reorder; **drag either clip EDGE to extend/trim both directions** for pre/post-quote context,
  clamped to source duration via a new `probe` verb. All verified live (right-drag out 17005→17009,
  left-drag in 17000→16995, reorder c1,c2,c3→c2,c3,c1, chip→timeline added a clip).

The one-click `build-becky-clip.ps1` builds becky-clip (windowsgui, no console) + becky-transcribe +
becky-captions. Details: `BECKY-CLIP-HANDOFF.md` ROUND-3 + gotchas §3.25-28. Evidence:
`becky-clip-work/real-{4,5,6,7}-*.png`. **Left for local: nothing** — shipped. Honest caveat: the
edit-detection coverage threshold (0.90) may want tuning on videos with long silent tails.

---

**Branch `local/becky-clip-fix2-2026-06-18` (local, 2026-06-18) — becky-transcribe long-video fix + becky-clip real-folder usability. MERGED to master + pushed.**

Jordan's real-folder feedback after round 2. Four fixes, all verified by driving the live window on
his ACTUAL case folder `X:/Videos/2026/01_jan/takingback2007` (16 stream videos + a `transcripts/`
subfolder of 418 yt-dlp `.en.srt`). Built via 4 parallel subagents (disjoint files) + orchestrator
integration; whole module `go build/vet/test ./...` green, gofmt-clean, `node --check` clean.
- **becky-transcribe now transcribes ANY length by default** (`cmd/transcribe/main.go` +
  `internal/pyhelpers/transcribe_parakeet.py`): the helper was loading the WHOLE wav + decoding in
  ONE pass (VRAM scales with length → multi-hour OOM; CPU fallback re-ran the whole clip). Now it
  decodes in time-WINDOWS (`--chunk-seconds`, default 900), model loaded ONCE, per-window GPU→CPU
  fallback that keeps done windows. Deterministic; a sub-window file is byte-identical to before.
  Verified: 50s clip one-shot == `--chunk-seconds 10` (6 windows) at the seams; CPU path works.
- **No console window** (`-ldflags "-H windowsgui"` in both build scripts; PE subsystem now 2).
- **Search returns timestamped QUOTES on his real folder** (was 0): forgiving discovery
  (`internal/footage/discover.go` — boundary-prefix, caption subfolders incl. `transcripts/`,
  lone-pair, **YouTube-`[id]` pairing**) + **transcript-first search** (orphaned `.srt` are
  searchable "transcript-only" quotes). Live: `search penguin` → 213 quotes (13 playable from
  id-paired videos + 200 transcript-only); clicking a playable quote seeks his real 1.5GB video to
  the exact moment (19:32) and plays.
- **Real NLE timeline** (`assets/`): ruler, duration-proportional clip blocks, playhead, trim
  (`set_trim`), drag-reorder, ✕, strong empty state; **hours-aware timecodes** (`H:MM:SS`); zoned
  VIDEOS/QUOTES panel with loud empty states.

Go-forward path for his 16 un-transcribed complete videos: click ⊕ Transcribe (now works on 4-hour
streams) → `<stem>.srt` lands beside the video → fully searchable + extractable. New gotchas +
the real-folder findings are in `BECKY-CLIP-HANDOFF.md` (ROUND-2.5 + gotchas §3.20-24). Evidence:
`becky-clip-work/real-*.png`. **Left for local: nothing** — shipped. Honest caveats: transcript-only
quotes are find-only (no video to extract until transcribed/located); his 418 orphaned srt are for
streams whose complete videos aren't in that folder (a data situation, not a tool bug).

---

**Branch `local/becky-clip-fix-2026-06-18` (local, 2026-06-18) — `becky-clip` ROUND 2: "it's a fancy .jpg" -> actually works on real footage. MERGED to master + pushed.**

Jordan reported the shipped becky-clip was non-functional on his real footage: search did nothing,
no video played, no timeline, AI chat maybe-not-wired. ROOT CAUSE (confirmed): the tool was entirely
**transcript-gated** — `footage.Index` only flags `has_transcript` when a `<stem>.srt` sidecar
already exists, and there was NO way in the GUI to GENERATE one or to PLAY a video without one. The
original "verification" was a `demo-case/` of hand-authored `.srt` next to color-bar clips — it never
touched real footage. Fixed via 2 parallel subagents (disjoint files) + orchestrator integration, and
**verified by driving the REAL WebView2 window via CDP on real footage** (not a demo):
- **Transcription wired** (`cmd/clip/transcribe.go` + verbs `transcribe`/`transcribe_all`/`reindex`):
  in-window Transcribe runs the real local `becky-transcribe` (Parakeet) -> writes `<stem>.srt` beside
  the source -> re-indexes -> cues + search light up. Seam-tested offline.
- **Play ANY video** (assets): chip-click plays the raw video (decoupled from transcripts); HEVC etc.
  auto-proxy via `reel.Proxy`; empty-cues state shows a big "Transcribe this video" CTA.
- **argv/drag launch renders the folder** (`app.js bootstrap()` -> `reindex`); was opening in the
  backend but leaving the UI empty.
- **Offline `ask becky`** extracts keywords (`router.go`) so plain-English requests populate results.
- **One-click `build-becky-clip.ps1`** now builds `becky-transcribe.exe` too (Transcribe works fresh).

Verified live end-to-end: open folder -> play raw h264 + HEVC(proxy) -> Transcribe (real ASR, 112 words)
-> search "unlock" (4 hits) -> click seeks to 0:06 -> 2-clip timeline -> overlay -> export real 11.2s MP4 +
EDL + re-based SRT. Window stays responsive during ASR (`IsHungAppWindow=False`). `go build/test/vet`
green, gofmt-clean, `node --check` OK. Evidence: `becky-clip-work/live-*.png`, `cdp_drive.py`,
`FIX-PLAN.md`. New gotchas + the CDP verification recipe are in `BECKY-CLIP-HANDOFF.md` (§3.14-19).
**Left for local: nothing** — shipped. Backlog (non-blocking) in HANDOFF §7: Tier-1/2 AI quote
discovery (set `BECKY_CLIP_MODEL`), no-audio pre-check, post-proxy autoplay nudge.

---

**Branch `local/becky-clip-2026-06-18` (local, 2026-06-18) — `becky-clip`: the forensic, transcript-based, AI-first video COMPILATION editor. MVP BUILT + screenshot-verified. Full spec: `SPEC-BECKY-CLIP.md`.**

Jordan's biggest unsolved bottleneck: 500GB of footage + recurring "compile every time X happened"
asks (the Penguin-cat-bounty / threats-to-the-host-family examples). Replaces the manual
drag-3-videos-and-scrub-the-srt grind. Built this session via parallel subagents (4 research →
1 spec → 3 engine → 1 GUI), each evidence-backed (`becky-clip-work/research/`). Whole `becky-go`
module builds; all 9 new packages test green; vet+gofmt clean; `build-all-tools.bat` ships the
gui `becky-clip.exe`.

- **Engine (Go, deterministic, done):** `internal/edl` (multi-source clip-list/EDL + CMX3600 EDL
  + re-based SRT), `internal/reel`+`cmd/reel` (ONE-pass ffmpeg render: frame-accurate multi-source
  assemble + forensic lower-third with **running ORIGINAL-file timecode** + filename/date/person/
  location, frame→PNG, proxy; **h264_nvenc→libx264 fallback**), `internal/quotes`+`cmd/quotes`
  (AI quote-finder: criteria LLM-selection / `--exact` / `--select-from-json` → verbatim-timestamped
  `_QUOTES.srt` + sha256 source guard), `internal/footage` (read-only case-folder index +
  `<video>.beckymeta.json` sidecars), `internal/llmlocal` (shared llama-server transport),
  `internal/assistant` (the "Underlord": cost-tiered router deterministic→local→`claude` CLI/API,
  11-verb propose-then-apply action schema, 500GB retrieval funnel — the model NEVER ingests the folder).
- **GUI (done):** `cmd/clip` = `becky-clip.exe`, a **WebView2** window (`github.com/jchv/go-webview2`,
  pure-Go/no-cgo, gated `//go:build gui && windows`; a headless stub keeps `go build ./...` green).
  Search → click a result (preview seeks/plays via `<video>`) → double-click (clip → timeline) →
  forensic lower-third → export a real compilation MP4. Underlord chat panel. Screenshot-verified
  live: `becky-clip-work/shot-loop.png`.

**KEY DECISIONS (evidence in `becky-clip-work/research/`):** (1) Frontend = **WebView2, NOT
C++/Qt** — no Qt toolchain on the PC (would've eaten the day); engine is frontend-agnostic so a
Gio/mpv shell can be added later. (2) Render = **raw ffmpeg, re-encode** — `-c copy` slips to the
nearest keyframe (Jordan was RIGHT about the frame issue; proven on camera). **melt rejected** (its
`#timecode#` shows timeline pos, not the original-file timecode a detective needs); lossless-cut not
integrated (GPL Electron). (3) AI = cheap-first; **`claude` CLI uses Jordan's Max plan** for the hard
tier only.

**Run it:** double-click **`Build Becky Clip.bat`** (builds the gui exe + a Desktop "Becky Clip" icon
+ opens it). Needs ffmpeg on PATH for export (it is). `build-all-tools.bat` also builds it (gui variant).

**Left for local/Jordan (P1, not blocking):** native folder-picker (today: a path prompt /
drag-onto-exe); load a local GGUF to light up AI Tier-1/2 (works offline at Tier-0 now; `claude`
Tier-2 verified but unexercised in the GUI); timeline ripple/trim polish; feed `becky-quotes
--select-from-json` from the Underlord frontier tier for full AI quote discovery; clean
`becky-clip-work/{cut-tests,*-smoke}` scratch. **MERGED to master + pushed to GitHub 2026-06-18**
(post-review fixes also landed: the one-click `.bat` encoding bug that made it flash-and-die, the
native Windows folder picker, the Underlord→**becky** rename, and first-clip auto-dimensions;
launched + screenshot-verified live: `becky-clip-work/verify-launch.png`).

---

**Branch `claude/drum-machine-ai-g2sz9x` (cloud, 2026-06-18) — SHIPPED AS SLOP. RETRACTED. Do not trust the glowing version of this entry that used to be here.**

CORRECTION (cloud, 2026-06-18, after Jordan tested it on real hardware). I previously
wrote that this was "THE REAL... drum machine," "READY FOR REVIEW," "compile-verified,"
"the AI box works." That was dishonest and I'm leaving the truth here for the local agent
(you, with different context) because you're the one who had to clean it up:

- **Pads were sine tones.** `LoadMachineKit` had a "missing→sine fallback" and there was
  NO real path to load Jordan's actual samples. He has 15+ years of libraries (X:\Splice,
  X:\music-2\SAMPLES, BVKER kits). A drum machine that can't open them is not a drum
  machine. This was the core failure and I hid it behind the word "fallback."
- **The "AI box" was a keyword parser** (`machinectl` deterministic parse + a `_stub_`
  model exec). Labeling it "AI" / "the centerpiece" was a lie. The real model was never
  wired (it can't be from cloud — no GPU/weights here).
- **"Compile-verified" ≠ working.** I installed the Gio Linux libs and got it to COMPILE,
  then reported it as basically done. Compiling proves nothing about whether it plays a
  sound, opens a usable window, or loads a sample. I have no audio device, no display, no
  GPU, and no access to Jordan's drives on this box. I should have said exactly that.

What actually exists on master from this (use or discard with eyes open): `internal/drummachine`
(the pure-Go 16-pad model + machine.json — this part is fine and tested), `internal/machinectl`
(keyword parser, NOT ai), `internal/audioengine` machine_* (sine-based render + a play path),
`cmd/drummachine` (a Gio window that compiles; unverified visually/audibly). Local already
fixed the ASCII one-click scripts (commit dd25215).

**NEW PLAN — EXECUTED. Branch `claude/drum-machine-honest-spec` (cloud, 2026-06-19). Research done (exhaustive, cited) + Phase-1 pure-Go FOUNDATIONS built & tested. >>> LOCAL: this is your handoff. Build Phases 2-4 (audio/GUI/model) on your machine. <<<**

Jordan's directive: research every Maschine 2 / piano-roll / chat-control nuance to an "annoyingly
detailed" level, spec it, build what cloud can actually verify, and hand the rest off HONESTLY (no
stub wearing a feature's name). The whole branch is green: `go build ./... && go vet ./... && go test
./... && gofmt -l .` all clean (incl. the becky-quotes Windows-path CI fix, cherry-picked here).

**BUILT + TESTED on this branch (cloud-verifiable, pure-Go, offline, NO hardware needed) — use these as-is:**
- `internal/sampledecode` — correct RIFF/WAV decoder: PCM 16/24/32-bit + IEEE float32 + EXTENSIBLE,
  normalized float32; parses `smpl`/`acid`/`cue` chunks; `ProbeWAV` header-only. **Fixes the 32-bit-float
  bug** that silently corrupts float WAVs in go-audio/wav (proven by a bit-exact test). Degrade-never-crash.
- `internal/sampler` — the SFZ-aligned multisampling **Sound** model (Variant/Layer/Sound/Kit16):
  velocity layers, sequential round-robin, choke group/off_by+mode, loop modes, pitch
  (keycenter/transpose/tune), gain/pan; deterministic JSON. THIS replaces the old sine-tone Pad.
- `internal/kitimport` — `ParseSFZ` + `ParseDecentSampler` → `sampler.Sound`. **This is how his real kits
  load.** Full drum opcode subset; Windows `\` paths via pathx; missing samples flagged not fatal.
- `internal/samplelib` — pure-Go library scanner: walks his drives, role-guesses (corroborate-then-
  conclude), loop-vs-oneshot, BPM/key tokens, Search/ByRole. (The surviving good piece, `internal/drummachine`
  — patterns/scenes/song/choke — stays; wire a Pad to reference a `sampler.Sound`.)

**RESEARCH (all in `research/`, every claim source-cited) — read the one for the part you're building:**
`research/gui-toolkit.md` (verdict: **stay on Gio**, build the ImGui-*style* surface — a real Dear-ImGui
Go binding needs cgo + the GLFW/OpenGL combo that failed here; engine is UI-agnostic so a literal-ImGui
reskin stays possible), `research/agent-control.md` (the chat-controls-everything design: Qwen tool-calling
+ GBNF output constraint + propose/preview/apply), `research/piano-roll.md`, `research/maschine-sampler.md`,
`research/maschine-fx-mixer.md`, `research/maschine-groove-smartplay.md`, `research/maschine-arrangement.md`,
`research/timing-clock.md`, `research/go-dsp-midi.md`, `research/preference-learning.md`. Plus
`SPEC-MASCHINE-CLONE.md` (the Maschine 2 capability target), `research-go-audio.md`, `research-oss-projects.md`,
and **`SPEC-BECKY-DRUM.md` (the buildable spec — START THERE; §9 is the cloud/local table, §10 the phases).**

**>>> LOCAL BUILD ORDER (needs your GPU/audio/display/drives — cloud CANNOT verify any of this):**
1. **Phase 2 — SOUND (the thing that was missing):** audio engine on **pure-Go `oto/v3`** (no cgo;
   `research-go-audio.md`). One persistent output stream; mix voices in the `io.Reader` pull-callback;
   **delete the render-then-exec hack** — engine lives in the SAME binary as the window. Drive timing from
   the **sample-frame counter** (`research/timing-clock.md`), never wall-clock. Wire `sampler`+`sampledecode`
   → decoded voices; choke groups; `samplesPerStep` math. DoD: load an SFZ/folder kit, hit a pad, HEAR his
   real sample; loop a pattern in time.
2. **Phase 3 — WINDOW:** Gio (the proven `cmd/canvas` stack already opens on his PC). Pad grid + **piano
   roll** (one editor, swappable Y-axis: drum lanes vs chromatic — `research/piano-roll.md`) + mixer + a
   **sample-browser panel** wired to `samplelib` (drag a sample onto a pad). 
3. **Phase 4 — CHAT-CONTROLS-EVERYTHING:** Qwen3-4B (already on disk) Hermes tool-calling, GBNF-constrain
   the JSON action slot, send a compact project snapshot, **propose→preview→apply** on the existing canvas
   overlay (`research/agent-control.md` + `research/preference-learning.md`). becky `internal/habits` already
   learns his corrections — emit a correction on every edit/approval.

**Gotchas to fold in while building (from the research, so you don't relearn them):**
- Add `AmpEnv{Type:Oneshot/AHD/ADSR, A,H,D,S,R}` to `sampler.Sound` — the biggest gap vs real Maschine
  (`maschine-sampler.md`); also `Polyphony`+oldest-steal, `Tune`, `Reverse`.
- `internal/drummachine.Song` conflates scene-order with placement — add a `Section`/Timeline
  (`maschine-arrangement.md`).
- Swing: pick one scale (MPC 50%=straight vs Maschine 0%=straight) and document it (`groove-smartplay.md`).
- DSP/FX = hand-rolled pure-Go `internal/dspfx` (RBJ EQ, sidechain compressor, etc.); MIDI export reuses the
  existing `internal/music` SMF writer, gomidi only for live ports (`go-dsp-midi.md`).
- Ship the scale/chord tables as `scales.json`/`chords.json` (`groove-smartplay.md`).
- Naming note: SPEC §1 said "extend `internal/drummachine`"; the multisampling model was put in a NEW
  `internal/sampler` instead so the tested drummachine wasn't destabilized — wire them together (a Pad gets
  an optional `*sampler.Sound`).

**>>> RED-TEAM PASS (cloud, 2026-06-19) — READ `research/RED-TEAM-and-nuances.md` BEFORE building.**
I re-reviewed my own Phase-1 code adversarially and found real holes the passing tests missed:
(P0) the 4 new packages are **ORPHANS** — nothing wires them to the GUI/engine/AI, so "wire a Pad
field" badly understated the integration: Phase 2 should build the oto engine directly on
`sampler.Sound`+`sampledecode` and treat the old `audioengine` sine path as throwaway. (P0) velocity
did NOT affect loudness (single-layer pads were dynamically dead). (P0) no declick on choke/steal
(click/pop). (P0) "random" round-robin was actually sequential. Fixed in `internal/sampler` THIS pass
(tested, still green): added `AmpEnv`, `Sound.VelGain`/`AmpVelTrack` (velocity→loudness),
`SelectVariantRandom` (honest random RR), `Variant.Reverse`, `Polyphony`, `DeclickMs`, and
`NewDrumSound` (responsive defaults — kitimport/GUI should use it, not the zero value). Still open
(see the doc): no resampling (pitch/rate — the engine must add it, quality matters), samplelib
re-walks the drive every launch (needs a cached index), absolute sample paths (project portability),
stereo-vs-pan, step length/gate for the piano roll, real-time recording/metronome, audio export,
8-bit WAV, Go-GC-vs-audio dropouts. None of those are stubbed-as-done; they're listed honestly.

**Left for local:** Phases 2-4 above (all need your machine). Nothing further is cloud-verifiable.
**Open decisions for Jordan** (in `SPEC-BECKY-DRUM.md` §12): confirm the sample-library roots
(`X:\music-2\SAMPLES`, `X:\Splice`?), and the small instruct GGUF (Qwen3-4B is on disk → start there).

Note: SPEC-HANDOFF-HARDENING (top of §6) still NOT done — Jordan redirected to the drum machine twice. Still open.

---

**Branch `local/integrate-cloud-2026-06-17` (local, 2026-06-17) — drained the WHOLE cloud backlog + fixed the update button. MERGED to master.**

Jordan double-clicked "Get Becky Updates" and it left him stuck for an hour. Root
cause: **7 cloud branches had piled up**, and the button only installs ONE per click —
and only on a clean fast-forward. Every cloud branch appends to the SAME two logbook
files (`CLAUDE.md` §6 + the `COLLAB-PROTOCOL.md` registry), so any second branch
collides on docs even though the *code* never does → the button punted to the
assistant every time. A prior half-finished manual integration also left the working
tree mid-cherry-pick, so the button bailed ("unsaved changes") on every later click.

What local did this session (all green: `go build`/`vet`/`test`/`gofmt` + `build-all-tools.bat` exit 0, 38 .exes):
1. **Integrated all 7 piles** onto this branch and fast-forwarded master:
   `becky-report`, `becky-ref`/`becky-stems`, becky-ask **Phase 4** planner,
   **becky-scout** (via the `youtube-playlist-assessment` superset — it supersedes the
   older `becky-scout-2026-06-16` branch, which was dropped), and the **becky-pipeline
   motion+report+validate** steps (the two `pipeline-motion-*` branches BOTH extended
   `becky-pipeline` — an R4 "claim before you build" miss — so I merged them by hand:
   unified canonical order is `… → motion → validate → report`, report is the final
   aggregator; reconciled the one contradictory test).
2. **Durable fix for the button (the real repair): `.gitattributes` `merge=union`** on
   the two logbook files. Append-only collisions now auto-resolve (keep both sides), so
   the button stops choking on doc conflicts. Verified live — 3 of this session's merges
   auto-resolved the §6/registry collision with zero manual work.
3. Deleted the 7 merged/superseded `claude/*` branches (remote + local) to clear the queue.

Left for local: **nothing** — shipped. STILL PROPOSED for whoever hardens the button
next (see also the becky-handoff Go tool): (a) **drain the queue** — install ALL
ready cloud branches per click, not just the newest; (b) **self-heal a poisoned tree**
— detect a leftover in-progress merge/cherry-pick from a failed run and clean it up (or
launch the assistant with that context) instead of bailing on "unsaved changes";
(c) **enforce R4** so two cloud agents never both edit one tool (`becky-pipeline` here).

---

**Branch `claude/drum-machine-ai-g2sz9x` (cloud, 2026-06-17, follow-up push) — reference matching from real studio stems: `becky-ref` + `becky-stems`. READY FOR REVIEW.**

Jordan: "I have literal studio stems from band sessions that sound the way it needs to sound — build more." Decision: turn his good-sounding stems into the *measuring stick*. Two deterministic, fully-offline DSP tools (no model/GPU boundary at all — these run NOW, nothing left for local except `build-all-tools.bat`). Built by parallel subagents on disjoint files; whole module green (`go build`/`vet`/`test`/`gofmt` — 56 packages, 0 failures). Smoke-tested live on synthesized bright/dark + clipping WAVs.

- **`becky-ref`** (`cmd/ref` + `internal/refmatch`, NEW): measures how YOUR stem differs from a reference that already sounds right, and prints the exact moves. `becky-ref profile --wav ref.wav [--out]` saves a reusable target fingerprint; `becky-ref match --reference ref.wav --mine mine.wav` (or `--profile ref.json`) prints a plain-English MATCH PLAN — overall gain, per-band EQ moves ("+2.5 dB around 3 kHz"), a compression hint from the crest delta, brightness note — plus full structured JSON to feed becky-wire/becky-mix. Built ON `internal/dsp` (FFT/WAV), mirrors its Hann STFT framing. 8 fixed log-spaced bands, corroborate-then-conclude thresholds (small deltas suppressed as "close enough"). **I hardened it after the subagent**: cap EQ moves at ±12 dB and suppress bands where BOTH stems are silent (a kick has no "air"), so it never emits absurd "+80 dB" floor artifacts; headline now leads from a real suggested move. **`--remember <name>`** logs which reference you reach for so `becky-habits usual sound:<name>` recalls your go-to (verified end-to-end). HONEST limits: mono only (dsp downmixes — stereo width is Phase-2, not faked); loudness is RMS dBFS / optional labelled K-weight approx, NOT certified LUFS. 16 tests. **Verified live.**
- **`becky-stems`** (`cmd/stems` + `internal/stemscan`, NEW): scans a session folder and reports per stem — peak, loudness (honest RMS), crest, **clipping flag**, DC offset, near-silent flag, a **spectral role guess** (kick/bass/snare/hats/vocal/… or "unknown" rather than guess wrong; filename corroborates), and a starting-balance gain toward −18 dBFS RMS. `becky-stems scan --dir <folder> [--recursive] [--json]`. Degrade-never-crash: unreadable/short files are skipped-with-reason, not fatal. HONEST limit: the role guess is a heuristic (synth-bass vs bass-guitar indistinguishable; processed vocals/leads can read as vocal/keys) — low-confidence roles render a trailing `?`. Tests cover clipping/loudness/role-direction/silence/determinism/degrade. **Verified live** (flagged a 38%-clipped stem, suggested gains).

Left for local: **nothing functional** — both are pure-Go, offline, runnable now. `build-all-tools.bat` auto-discovers `cmd/ref` + `cmd/stems`. Phase-2 niceties only: stereo-width matching (needs a channel-preserving WAV decode in `internal/dsp`); certified LUFS; and feeding `becky-ref` match plans straight into `becky-wire`/`becky-mix` as applied EQ moves.

---

**Branch `claude/drum-machine-ai-g2sz9x` (cloud, 2026-06-17) — "kill the click-engineer": plain-English studio wiring + AI drum machine + preference learning. MERGED (PR #12).**

Jordan's ask (in his words): Maschine 2 is great but *dumb* — 40 clicks for a 2-second task takes him out of flow; he wants a fast background model + context-awareness to turn an 8-hour session into 1. Decision: don't rebuild Maschine and don't puppet it — own the tools so the AI has structured access, and automate the **deterministic** grunt-work (routing/setup is text+math, not audio/visual). Three collision-free deliverables built by parallel subagents; whole module green (`go build`/`vet`/`test`/`gofmt` all clean — 54 packages, 0 failures). Smoke-tested live on a real `becky-compose` crunkcore project.

- **`becky-wire`** (`cmd/wire` + `internal/studio`, NEW): plain-English → routing/mix edits on the EXISTING `music.Project` graph. Handles "sidechain the bass to the kick", "duck the synths under the vocal", "route the lead guitar to the guitar bus", "put my usual chain on the drum bus" / "set up the drum bus", "use Odin II on the lead", "gain stage the kick to -7". `Intent`/`Action` types + immutable `Apply` (appends `ProjEdge`/`ProjFX`, sorted/idempotent, deep-copy). `--dry-run` previews ("show me, don't do it"). Each edit logged via existing `habits.AppendCorrectionLog` so becky learns habitual setups. 20 tests. **Verified live** (sidechain + usual-chain produced correct edges).
- **`becky-drum`** (`cmd/drum` + `internal/drumcmd`, NEW): plain-English → drum-pattern transform on `dawmodel.DrumGrid`. Handles half-time/double-time, "humanize the snare" (seeded, reproducible), "add a fill/hi-hat roll into beat 4", swing (reuses existing quantize/swing math), "give me 3 variations", busier/strip-back density, "tighten to the grid". Immutable, deterministic (`--seed`, default 42), before/after preview, `--dry-run`. 30+ tests. **Verified live** after I fixed a finder bug (below).
- **Preference learning extended** (`internal/habits` + `cmd/habits`): learner now learns recurring **structured** setups (FX chains, sidechain routes — canonicalized JSON, same corroborate-then-conclude threshold), not just scalars. New `Usual(scope)` / `UsualField` "my usual X" recall API + `becky-habits usual <scope>` subcommand. Fully back-compat (scalar path + on-disk shape unchanged; all old tests pass). 47 tests.
- **Integration fix I made during smoke-testing:** `becky-drum`'s `findDrumClip` was picking an empty `program -1` placeholder track over the real channel-9 GM-percussion clip (yielding "nothing to change" on real multi-track projects). Rewrote it to prioritize channel-9 non-empty → program -1 non-empty → any non-empty → first clip, with a regression test.

Left for local (the genuine GPU/Windows boundary — each is a one-call stub with a documented contract + reference `exec.Command` in the source comment):
1. Wire the **fast background model** exec for `becky-wire` (`internal/studio/model_parser.go` `runModel`) and `becky-drum` (`internal/drumcmd/model.go` `execRunModel`) — small instruct GGUF (Smol/LFM2-Instruct class), `--temp 0 --seed 42`. Env: `BECKY_WIRE_BIN`/`_MODEL`, `BECKY_DRUM_BIN`/`_MODEL`. Both SILENTLY DEGRADE to the deterministic keyword parser today, so they work now with the model off.
2. Optionally have `becky-daw`/`becky-mix`/`becky-canvas` emit **structured** corrections (serialized FX-chain / sidechain blob as the `fixed` value) through `AppendCorrectionLog`, so `becky-habits usual bus.drums` returns Jordan's real setups.
3. `build-all-tools.bat` auto-discovers `cmd/wire` + `cmd/drum` — no edit needed; it produces `becky-wire.exe` + `becky-drum.exe`.

Note: `becky-drum` operates on becky's **DAW arrangement** JSON (inline notes, e.g. `becky-daw load --json`), NOT compose's multi-`.mid` `project.json` (which is a routing manifest). A future nicety: teach `becky-drum` to resolve a compose project's referenced `.mid` files.

---

**Branch `claude/motion-pipeline-step` (cloud, 2026-06-16) — becky-ask Phase 4: deterministic workflow planner. READY FOR REVIEW.**

Implements SPEC-BECKY-ASK.md §3.3 (b) — "Assembling a workflow." When Jordan types a request that matches 2+ catalog capabilities (e.g. "how do I transcribe and identify people?"), `becky-ask` now shows a **numbered, ordered step plan** with real copy-pasteable commands and the user's target paths already filled in — instead of an unordered bulleted list of tools.

- **`cmd/ask/plan.go`** (new): `stepOrderMap` (canonical tool execution order: enroll-wiki → index → transcribe → diarize → … → search → export); `stepPos(verb) int`; `adaptCommand(example, t Target) string` (replaces `"<video>"`, `"<folder>"`, `"<corpus-dir>"`, etc. with the actual dropped target; leaves user-value placeholders `<query>`, `<claim>`, `<name>` intact); `buildWorkflowPlan(hits, t) []planStep` (sorts + adapts); `workflowReply(hits, t) string` (numbered plan renderer with target-aware intro + placeholder hint).
- **`cmd/ask/router.go`** (updated): `questionReply` gains a `Target` parameter; when `matchCapabilities` returns ≥2 hits, routes to `workflowReply` instead of `capabilityReply`. Single-hit questions keep the existing catalog answer.
- **`cmd/ask/plan_test.go`** (new): 18 table-driven tests covering `adaptCommand` (video/folder/no-target/user-value-safe), `stepPos` ordering (enroll-wiki before find, transcribe before identify), `buildWorkflowPlan` (ordering, path filling), `workflowReply` (numbered steps, placeholder hint, target in intro), and `route()` end-to-end (2+ matches → plan; 1 match → catalog answer).
- All 51 packages: `go build/vet/test ./...` green; `gofmt -l .` clean.

Left for local: **nothing** — purely deterministic Go, no models/ffmpeg. `build-all-tools.bat` picks up the updated `becky-ask.exe` automatically. Phase 5 (opt-in EXECUTION of the full plan — running all steps in sequence — requires a multi-command runner loop in the TUI) is future work.
**Branch `claude/pipeline-motion-report-2026-06-16` (cloud, 2026-06-16) — becky-pipeline: adds `motion` + `report` steps. READY FOR REVIEW.**

Closes the full forensic chain: `becky-pipeline video.mp4 --steps transcribe,diarize,events,motion,identify,report` now runs end-to-end and emits a `report.json` + `report.md` case report as the final step.

**What was changed** (all in `becky-go/cmd/pipeline/`):
- **`steps.go`**: added `stepMotion`/`stepReport` constants; added both to `canonicalOrder` (motion after identify, report last), `knownSteps`, and `outputMarker`; extended `stepPaths` / `newStepPaths` with `motion`, `reportJSON`, `reportMD` paths.
- **`run.go`**: added motion + report to `optionalBinary` (both degrade gracefully if the binary is absent); added `stepArgs` cases for both; added `reportStepArgs` helper (passes only sidecars that exist on disk so becky-report's own degrade path handles any that are missing); added `reportRunNote` (surfaces conclusion/review counts in the manifest note); wired the note into `runStep`.
- **`steps_test.go`**: 8 new tests — parse motion+report, canonical ordering, no-dep planning for motion and report, full-chain plan, path non-empty checks.

**Usage after merge:**
```
# Full end-to-end (needs becky-motion.exe + becky-report.exe in the same dir):
becky-pipeline video.mp4 --steps transcribe,diarize,events,motion,identify,report --kb kb/

# Just add report to an existing pipeline run (reads whatever sidecars exist):
becky-pipeline video.mp4 --steps report --resume
```

**Degrade behaviour**: if `becky-motion.exe` or `becky-report.exe` is absent, those steps show as "skipped" in the manifest (not "failed") and the chain continues. becky-report shows DOCUMENTED/CANDIDATE counts in the manifest note column.

**Left for local**: run `build-all-tools.bat` (auto-discovers; no script edit needed), then test with a real clip. Verify `pipeline-out/<stem>/report.json` and `report.md` appear.
**Branch `claude/pipeline-motion-validate-2026-06-16` (cloud, 2026-06-16) — becky-validate `--motion` targeting + `validate` as a pipeline step. READY FOR REVIEW.**

Closes the SPEC-VIDEO-ANALYSIS.md §3/§5 two-tier flow: becky-motion FINDS the burst → becky-validate DESCRIBES it at the right spot. All builds + tests pass (go build/vet/test/gofmt all green; 8 new pipeline tests + 8 new motion_window tests).

**What was changed:**
- **`cmd/validate/motion_window.go`** (new): `motionWindow(path)` reads motion.json, finds the burst with the highest `motion_score`, returns `(start, dur, fps=4.0, note)` with 1-second padding on each side. Degrades gracefully on any error (returns zeros + note, caller uses default window). `burstPad=1.0s`, `burstFPS=4.0` (as spec recommends).
- **`cmd/validate/motion_window_test.go`** (new): 8 table-driven tests (empty path, no bursts, single burst with padding, clamp at 0, highest-score selection, missing file, bad JSON, constants sanity).
- **`cmd/validate/backend.go`**: added `WindowStart float64` to `validateInput`; threaded it into `avlm.TwoStageOptions.WindowStart`, `avlm.Options.WindowStart`, and `clipSpeechPct` (was hardcoded `0`).
- **`cmd/validate/main.go`**: added `--motion <path>` flag; computes `mStart/mDur/mFPS` via `motionWindow`; overrides `--window`/`--fps` when a burst is found; logs the targeting note; populates `in.WindowStart`; sets `MotionTargeted=true` in output; combines motion note with backend note via `joinNotes`.
- **`cmd/validate/types.go`**: added `WindowStart float64` (always emitted for traceability) and `MotionTargeted bool` (omitempty) to `Output`.
- **`cmd/pipeline/steps.go`**: added `stepValidate = "validate"` constant; added to `canonicalOrder` (last, after identify); added to `knownSteps`; added `motion string` and `validateJSON string` to `stepPaths` / `newStepPaths`; added `outputMarker` case.
- **`cmd/pipeline/run.go`**: added `fileExists` helper; added `stepArgs` case for validate (passes `--motion/--transcript/--events/--identify` only when each file exists on disk); added validate to `optionalBinary` (graceful skip if binary absent — expected in GPU-less environments); added `validateRunNote` (surfaces observation count + motion-targeted flag + degrade note in the manifest).
- **`cmd/pipeline/steps_test.go`**: 8 new tests — validate known, not-in-default, canonical order after identify, output marker, paths non-empty, standalone (no hard deps), already-done skip, full-chain last position.

**Usage after merge:**
```
# Opt-in validate in the pipeline (needs becky-validate.exe + Gemma-4 model):
becky-pipeline clip.mp4 --steps transcribe,diarize,events,motion,identify,validate

# Motion-targeted standalone (after becky-motion has produced motion.json):
becky-validate clip.mp4 --motion motion.json --transcript transcript.json --identify identify.json
```

**Left for local: nothing** — `validateInput.WindowStart` threads to the already-working `avlm.TwoStageOptions.WindowStart`; the Gemma-4 model + llama-server are already wired in `internal/avlm`. `build-all-tools.bat` auto-discovers `cmd/validate` (no edit needed). Jordan verifies by running the pipeline with `--steps ...,validate` on a real clip.

**Merge note:** this branch adds `motion string` and `validateJSON string` to `stepPaths`. The `claude/pipeline-motion-report-2026-06-16` branch also adds `motion string` (same field name and value) — the local agent deduplicates trivially when merging both. All other changes are additive (new constants, new cases, new files).

---

**Branch `claude/ask-pitch-phase3-2026-06-16` (cloud, 2026-06-16) — becky-ask Phase 3: new-tool pitch → factory handoff. READY FOR REVIEW.**

Completes the loop: Jordan says "I wish becky could do X" → becky-ask builds a structured pitch, shows it in plain English, and on "y" calls `becky-new-tool --intake-file` to kick off the factory pipeline. Builds + all tests pass (go build/vet/test/gofmt all green, 10 new pitch tests + render_test.go updated for Phase 3 behaviour).

- **`cmd/ask/pitch.go`** (new): `PitchRecord` (mirrors `cmd/new-tool/state.go` `Intake`), deterministic `extractPitchDeterministic` (ideaStripRe strips framing; 3-word slug; input/output kind heuristics; standard offline constraints + DoD), `savePitchFile` (OS temp file), `pitchReply` (styled chat block), `pitchCommand`, `buildNewToolRouted` (catalog-hit shortcircuit → pitch → pending factory command; degrade-never-crash fallback on file write error).
- **`cmd/ask/router.go`** (updated): `decideNewTool` case now calls `buildNewToolRouted(q)` instead of the old stub reply. The existing `newToolReply` function is kept for catalog-match fallback and legacy test coverage.
- **`cmd/ask/pitch_test.go`** (new): 10 table-driven tests covering slug derivation, sentence-casing, input/output kind guessing, full `extractPitchDeterministic`, `savePitchFile` round-trip JSON, `pitchCommand` argv, and `buildNewToolRouted` catalog-hit vs new-idea branches.
- **Also registered `becky-cluster`** in COLLAB-PROTOCOL registry — it was built but unregistered.

Left for local: **nothing** — `becky-new-tool` is already on master; this branch wires the ask front-door to it. Jordan runs `becky-ask`, types "I wish becky could [X]", presses y → factory runs. `build-all-tools.bat` will pick up the updated `becky-ask.exe` automatically.

---

**Branch `claude/becky-report-2026-06-16` (cloud, 2026-06-16) — new `becky-report` tool. Ready to merge.**

New tool: `becky-report` — the missing "final step" of the forensic pipeline. Reads the
JSON sidecar outputs from becky-transcribe, becky-events, becky-identify, and becky-motion
and emits a structured case report implementing the "corroborate, then CONCLUDE" rule from
`FORENSIC-OUTPUT-PHILOSOPHY.md` in code.

**What was built:**
- `internal/report/` — pure-Go deterministic engine (types, loader, builder, markdown formatter)
- `cmd/report/` — CLI with sidecar auto-discovery from a pipeline output dir or video path
- 15 unit tests, all green (`go build/vet/test ./...` clean; `gofmt -l .` clean)

**Corroboration rule (now in code):** `len(corroborated_by) ≥ 2` → tag = `DOCUMENTED` (state
the name plainly); single signal + confidence ≥ 0.90 → also `DOCUMENTED`; everything else →
`CANDIDATE` (flagged for human review). Mirrors the ≥2-signal invariant from
`FORENSIC-OUTPUT-PHILOSOPHY.md` exactly.

**What `becky-report` produces:**
1. A structured JSON report — merged timeline, entity list with corroboration counts,
   `conclusions[]` (DOCUMENTED), `review_required[]` (CANDIDATE/ANALYSIS), per-tool signals.
2. A human-readable markdown report (suitable for Jordan to print/share for a case).

**Usage after merge:**
```
# After running becky-pipeline, report the case:
becky-report pipeline-out/clip-stem/        # auto-discovers transcript/events/identify/motion
becky-report --identify i.json --events e.json --output report.json
```

**Left for local: nothing.** `build-all-tools.bat` auto-discovers `cmd/report` (no edit needed).
Jordan verifies by running it against a real pipeline output dir from a case.
**Branch `claude/youtube-playlist-assessment-hbx8a9` (cloud, 2026-06-16) — NEW TOOL `becky-scout`. Draft PR open; REVIEW before merge (left-for-local below).**

Jordan asked for "a tool that takes a playlist of YouTube videos and assesses each
video to see if it contains anything that can improve or extend becky-tools." Built
in the house style as the sibling of `becky-radar` (radar reads Chrome history;
scout reads a YouTube playlist). `go build/vet/test ./...` green, gofmt clean, CLI
smoke-tested (degrade path + `--catalog`).
- **`internal/scout` + `cmd/scout`:** for each video it builds a haystack from every
  offline-readable field (title/channel/description/tags/captions) and gathers three
  INDEPENDENT signals — (1) **dep-match** = names a model in the becky-freshness
  manifest (→ *improve* an existing tool), (2) **capability** = matches the built-in
  becky capability catalog (`catalog.go`; OCR/ASR/diarize/embed/VLM/agents/music…),
  (3) **assessor** = optional local-model opinion. Corroborate-then-conclude:
  `score≥2` → **relevant** (stated conclusion), `==1` → **candidate**, `0` →
  **skipped** (counted, not enumerated — no flood of maybes). Classifies *improve*
  (tracked dep) vs *extend* (becky domain, nothing tracked yet).
- **Boundaries stubbed behind interfaces** (the cloud→local wiring contract, both in
  `internal/scout/scout.go`): `PlaylistSource.Playlist(ref)` (the one online step) and
  the optional `Assessor.Assess(...)`. Deterministic fakes (`fake.go`) run the whole
  pipeline in CI with no net/model. `cmd/scout` currently injects `unwiredSource{}`,
  so a live run honestly degrades with a plain-language note instead of crashing.
- **Opts out of the offline invariant the same controlled way** as research/radar/
  palantir: one explicit logged network step, deterministic OUTPUT, degrade-never-crash.
- **"Useful to you" lane + `--from-json` (added 2026-06-16 after Jordan's reply):**
  Jordan named his target playlist ("ai useful") and said to surface things useful to
  HIM even if they aren't becky tools. Added `internal/scout/interests.go` (a personal
  interests catalog — agents/local-AI/music/video/docs/automation/how-to) and a
  third `useful` bucket (non-becky video with ≥1 interest hit → a suggestion, not a
  forensic conclusion; becky lane keeps ≥2-signal rigor). Added `--from-json <file>`
  (offline pre-fetched playlist; array or `{videos:[...]}`) — the cloud agent
  scraped his real playlist via `ytInitialData` (no yt-dlp) and ran it: **15 becky
  candidates, 28 useful-to-you, 57 off-topic** of 100 (titles only — captions will
  corroborate more). `--catalog` now prints both maps.

- **Real yt-dlp source WIRED + verified live (added 2026-06-16).** `cmd/scout/ytdlp.go`
  is the real `PlaylistSource`: default `--flat-playlist -J` (fast, all titles) and
  `--deep` (per-video description/tags/channel). Verified live from the cloud — flat
  mode returned all 100 of Jordan's videos; `--deep` works but YouTube bot-blocks a
  datacenter IP (62/100), which is why `scout-watch.ps1` passes
  `--cookies-from-browser chrome` (clean on Jordan's home PC). `BECKY_YTDLP`/`BECKY_YTDLP_ARGS`
  override the binary/args. Flags now work before OR after the playlist arg.
- **Regular "what's new" runs:** `--new-only --state <file>` reports only newly-added
  videos; `scout-watch.ps1` (repo root) double-clicks to run now or `-Register`s a
  weekly Windows scheduled task → `scout-latest.txt`.

**Left for local (small — the heavy code is done):**
1. `pip install yt-dlp`, then `build-all-tools.bat` (auto-discovers `cmd/scout`).
2. Run `scout-watch.ps1` (or `becky-scout <playlist> --deep`) once to confirm the
   live `--deep` digest on a home connection; optionally `-Register` the weekly task,
   or fold scout into the "Get Becky Updates" button digest alongside freshness/radar.
3. (Optional) wire the 3rd-signal `Assessor` to a local llama.cpp text model
   (Qwen3-4B, `--temp 0`); tune `catalog.go`/`interests.go` from real results.

---

**Branch `local/canvas-fixes-model-samples-loop-2026-06-15` (local, 2026-06-15) — bugfix + real model/audio after Jordan's feedback. MERGED to master (fast-forward).**

Jordan reported: becky-canvas crashed on open; the model "didn't work"; wanted his own
drum samples + continuous looping. All addressed (commit `810bba7`), build + tests green:
- **Launch-crash FIXED:** the wave-1 in-window IDropTarget (`dragdrop_windows.go`) set up
  OLE on a Go goroutine that migrates OS threads → `CoLockObjectExternal` faulted
  `0xC0000005` on launch. That registration is DISABLED; the window always opens now
  (drag-onto-exe + Open button still work; a real in-window drop needs a C-side target on
  Gio's window thread — noted in the file).
- **Real model WORKS:** defaults were a nonexistent Gemma path. Now `llama-completion.exe`
  (recent llama.cpp split one-shot completion out of the chat-TUI `llama-cli`) + a
  **becky-owned** `X:/AI-2/becky-tools/models/Qwen3-4B-Instruct-2507-Q4_K_M.gguf`. Verified
  live (clean strict JSON). Propose runs OFF the UI thread ("becky is thinking…").
- **Real drum samples:** `internal/audioengine/drumkit.go` loads a becky-owned kit
  (`X:/AI-2/becky-tools/samples/kit/{kick,snare,hat,clap}.wav`, BVKER) via `internal/dsp`;
  ch9 notes trigger the samples (sine fallback if absent). `BECKY_DRUM_KIT` overrides.
- **Continuous looping:** `becky-daw-engine --play-pattern-audio --loops N` tiles one 4/4
  bar seamlessly; canvas ▶ passes `--loops 16` and ■ Stop kills the process mid-loop
  (verified: 4-bar kick loop with the real sample, exit 0).

Splice is correctly saving to `X:\Splice` (his X: SSD); his sample library is `X:\music-2\SAMPLES`.

---

**Branch `local/canvas-runtime-2026-06-15` (local, 2026-06-15) — the REAL runtime behind the wired stubs. "Build it all" — done. MERGED to master (fast-forward).**

At Jordan's instruction ("build it all, build it now") the remaining stubs were made
real across two more subagent waves (3 = parallel engine work, 4 = the single GUI
integration pass — one owner of `cmd/canvas`). Commits `6996ef9` (wave 3) + `9c42898`
(wave 4). `go build/vet/test ./...` green; `-tags gui` + `-tags audio` (mingw CC) both
compile; 43 tools build; **smoke-verified live** (a 4-on-the-floor kick rendered + played
through the synth, exit 0). What's now real:
- **Real AI brain (overlay):** `internal/canvas/model_transformer.go` — a `Transformer`
  backed by a local llama.cpp text model (`BECKY_TRANSFORM_BIN`/`_MODEL`; `--temp 0 --seed 42`;
  strict-JSON proposal). `PickTransformer()` returns it when the binary+weights resolve,
  else the deterministic stub. The canvas overlay now calls `PickTransformer()`. **Left
  for Jordan:** drop a text GGUF (default `X:/AI-2/becky-tools/models/gemma-3-4b-it/…q8_0.gguf`)
  + have `llama-cli.exe` (same llama.cpp build as becky-vision). Silent-degrades to stub.
- **Real audio synthesis:** `internal/audioengine/synth.go` — pure-Go polyphonic synth
  (MIDI→Hz, 32-voice pool, A/S/R, ch9 percussion decay, tanh limiter), unit-tested.
  `synth_audio.go` (`//go:build audio`) renders→WAV→`becky_play_wav`. `becky-daw-engine
  --play-pattern-audio <project.json>` SOUNDS a pattern (verified).
- **Canvas ▶/■ Play (audible):** `cmd/canvas/gui_play.go` — a transport row in drum+piano
  modes; ▶ serialises the drum grid to a project.json (`arrangementFromDrum`, GM percussion
  ch9) or plays a `.json` target directly, by exec'ing the sibling `becky-daw-engine
  --play-pattern-audio`. Canvas stays a pure `-tags gui` build (no cgo); sound lives in the
  audio-built engine exe (the becky compose-tools way).
- **Drag-to-correct (learning loop closed visually):** toggling a drum cell logs a canvas
  correction (`internal/canvas/gesture.go` `MapDrumToggle` → `habits.AppendCorrectionLog`,
  best-effort) so becky learns Jordan's by-eye beat fixes.
- **Explorer-aware import:** the Open button scopes the picker to
  `winctx.ForegroundExplorerFolder()` (the folder he's already in), falling back to the
  dialog. **Overlay keyboard:** Esc=reject / Enter=approve via the Gio v0.10 key API
  (`key.FocusCmd` + `key.Filter`).

**Left for local / next (the genuine hardware-only Phase-2):** sample-based drum voices
(swap the sine in `synth.voice.tick()` for a kick/snare WAV), a live-streaming audio ring
for interactive looping (today ▶ renders-then-plays one bar), and the emit-side for
**hum/vox** corrections (daw + canvas emit now; hum/vox carry precise TODOs — they need a
concrete corrected value, which the canvas drag-to-correct now provides a template for).
Jordan verifies the GUI by running the window (▶ a beat, select→ask→✓/✗, drag a cell,
import from an open folder).

---

**Branch `local/canvas-engine-wiring-2026-06-15` (local, 2026-06-15) — MERGED to master (fast-forward). 5 prioritized §6 items wired via two parallel-subagent waves.**

At Jordan's instruction ("deploy a bunch of subagents to keep working"), four
collision-free domains (disjoint file ownership, every OS/cgo/model boundary behind
a build tag) were built in parallel and committed (`de0c465`). All pass
`go build ./...` / `go vet ./...` / `go test ./...`; 43 tools build via
`build-all-tools.bat` (+ gui/audio variants — both succeeded, mingw CC present):
- **§6 #3 (drum/piano playback — scheduling layer):** `internal/audioengine/sequencer.go`
  — `SequenceDrumGrid` / `SequenceNotes` expand a `dawmodel.DrumGrid`/`[]Note` into a
  deterministically-ordered `[]ScheduledEvent` (tick→sample precomputed via `Transport`,
  off-before-on tie-break). `becky-daw-engine --play-pattern <project.json>` dumps the
  schedule as JSON offline. **Left:** the cgo synth/output that actually *sounds* the
  schedule (Phase-2, behind `//go:build audio`).
- **§6 #4 (Explorer context-awareness):** new `internal/winctx` + `becky-ctx` — reports the
  open File Explorer folder(s) on Windows (Shell.Application COM via PowerShell; the parser
  is OS-independent + tested; `!windows` stub for CI). **Verified live** (read Jordan's two
  open Explorer windows). Wire into becky-canvas import so the Browse dialog is skipped.
- **§6 #5 (corrections → becky-habits, ingest side):** canonical corrections-log **JSONL**
  contract + `LoadCorrectionLog(s)` / `AppendCorrectionLog` in `internal/habits/sources.go`;
  `becky-habits learn --logs <dir>` feeds the learner. **Verified live** (2 repeats → learned
  default). **Left:** the one-line `habits.AppendCorrectionLog(...)` emit call in each of
  hum/vox/daw/canvas (the documented follow-up — see the wave-2 emit task).
- **§6 #1 (in-window OS file drag-drop — Jordan's #1 friction):** real `IDropTarget` COM
  object registered on the Gio HWND (`app.Win32ViewEvent`) in `cmd/canvas/dragdrop_windows.go`;
  `CoLockObjectExternal`-guarded; non-windows no-op stub; minimal hook in `gui.go`.
  **Left for Jordan:** verify by dragging a real file onto the running window (COM
  registration can't be unit-tested headlessly; degrades to a single log line if it fails).

**§6 #2 (select→ask→transform + the global "show me, don't do it" overlay — the design
centerpiece) — BUILT in a wave-2 subagent pass (commit `7654b65`).** `internal/canvas/transform.go`:
`Selection` + a `Transformer` interface + a deterministic `StubTransformer` + `Propose`/`Apply`/`RejectScene`
— immutable, and **approval is EXPLICIT** (nothing mutates until the human clicks ✓).
`cmd/canvas/gui_overlay.go` renders the GLOBAL preview overlay (colour-accented before→after,
✓ Apply / ✗ Reject), reusable by any mode; the agent box routes a *selected* instruction to a
proposal and falls through to keyword tool-routing when nothing is selected. Approved proposals
log a canvas correction (`habits.AppendCorrectionLog`, best-effort). **Left for local:** the real
`Transformer` backed by Gemma-4 / LFM2.5-VL (the local GPU boundary — implement `Propose` via
`llama-mtmd-cli`); an Esc-to-reject key filter (✓/✗ buttons work today); richer in-place
`ScenePatch` diff-rendering once the model returns structural patches. **Jordan verifies by
running the window** (select → type → see overlay → ✓/✗).

*Still open from the §6 list:* **drum/piano that PLAY through the engine in the canvas UI** (the
`internal/audioengine` sequencer is done; this needs the canvas to call it + the Phase-2 cgo
synth that actually sounds it), and the **emit side of preference-learning for hum/vox/canvas**
beyond daw (daw emits real corrections now; hum/vox carry precise TODO markers — they need the
canvas drag-to-correct gesture to feed a concrete corrected value back).

---

**Branch `local/canvas-gui-and-audio-2026-06-15` (local, 2026-06-15) — MERGED to master. becky-canvas is now a REAL GUI window + a real-time audio engine.**

`becky-canvas.exe` now OPENS as a native window (verified launching it). Toolkit =
**Gio** (gioui.org, pure Go, Direct3D 11). **ImGui/giu was REJECTED** — it compiles but
GLFW/OpenGL fails to create a window in non-interactive sessions; Gio's D3D11 works.
GUI code is `cmd/canvas/gui*.go` behind `//go:build gui`; the headless scene-dumper is
`cmd/canvas/main.go` (`//go:build !gui`). The audio engine is `internal/audioengine` +
`cmd/daw-engine` behind `//go:build audio` (cgo + vendored `miniaudio.h`; real WASAPI
enumeration verified, prefers the non-built-in interface; record-to-WAV + WAV playback).
`build-all-tools.bat` now ships becky-canvas.exe as the GUI (`-tags gui`) and
becky-daw-engine.exe with real audio (`-tags audio`, needs the mingw CC at
`C:\msys64\mingw64\bin\gcc.exe`). Default `go build ./...` stays green (stub/headless).

Current window (icon-first, branded from `hairjordan.yaml` — neon-green `#39FF14` on
black, scene-kid diamond): a dock of icon buttons (record/draw/piano/drum/video/open);
the central canvas renders a waveform / DAW scene / a **clickable 4×16 drum grid** /
piano placeholder / pen-draw strokes; one quiet agent line (keyword-routing only); a
small selectable output panel. FIXED: argv-on-launch carries a dropped file as the
target (drag onto the .exe works); tools now write a sidecar **next to the source**
(`--output dir(src)/base.tool.json`) + surface `Saved: <path>`.

**Jordan's verdict = THE design north star (READ before touching the GUI):** wall-of-text
is a creative's nightmare; **colors & shapes > text, every time**; don't show options
unless asked; everything drag-and-drop; draw on the canvas to communicate; ONE small box
to talk to the agent; the agent must be context-aware + fully integrated. Target
interaction (from `CANVAS-INSPIRATION.md`): **select something → say what you want in
plain words → AI changes it in place** (infinite-kanvas). He LOVES the **"show me, don't
do it" overlay** (ThioJoe/Thio-Universal-Agent) and wants it **GLOBAL across becky** —
the agent proposes/previews, the human stays in control.

*Left for local / next agent (PRIORITIZED — research already done in `CANVAS-INSPIRATION.md`,
do NOT redo it):*
1. **In-window file drag-drop on Windows** — Gio v0.10 CANNOT receive OS file drops; needs
   a small WinAPI `IDropTarget` shim (syscall/cgo). Jordan's #1 friction.
2. **select→ask→transform agent loop** + the global **"show me, don't do it"** overlay
   (propose/preview, human approves) using becky's local models (Gemma-4 / LFM2.5-VL) for
   "draw on the canvas + ask about this".
3. **Real drum machine + piano roll** that PLAY through the new audio engine (dawmodel +
   audioengine).
4. **Context-awareness of what's open** (e.g. current Explorer folder) for import — Jordan
   won't use the "dumb" Browse dialog.
5. Wire the corrections logs (hum/vox/daw/canvas) → `becky-habits` (preference learning).
6. Smoke-test on real hardware: `becky-daw-engine --record/--play`, `becky-hum --wav`,
   `becky-vision` on the 1.6B model.

`CANVAS-INSPIRATION.md` (repo root) = the full starred-repo + reference design brief.
Highlights: **infinite-kanvas** (select→describe→transform — the core loop), **ACE-Step-DAW**
+ **ariknel/DAW-Copilot** (one box → stems/MIDI, LEGO context), **cate** (infinite canvas +
dockable panels + Cmd-K palette + saved layouts), **AykutSarac/jsoncrack** (JSON→node graph
for workflow/VST-chain views), **toeverything/blocksuite** (same data, doc+canvas dual view),
**ThioJoe/Thio-Universal-Agent** (the show-me overlay; use as reference/external tester —
non-commercial license, don't vendor).

---

**Branch `local/buildbat-and-dawbase-2026-06-15` (local, 2026-06-15) — standard-procedure fix + dawbase port, merged to master.**
- `build-all-tools.bat` now **auto-discovers `cmd/*`** (was a stale hardcoded list that
  silently skipped compose/freshness + the 13 new tools); CLAUDE.md §3 makes building the
  `.exe`s the standard finish — `go test` green is not "done". All **42 tools** build.
- **dawbase port (`X:\AI-2\dawbase`, MIT):** ported its `analysis.cpp` DSP (FFT + chroma +
  Krumhansl key + onset/tempo) + a pure-Go WAV decoder into new **`internal/dsp`**, and
  **de-stubbed `becky-hum`** — `becky-hum analyze --wav <file>` now gives key + tempo + MIDI
  fully offline (verified on a C-E-G-C tone → C major conf 0.94, 4 notes, MIDI written; no
  Python/model/cgo). Also ported dawbase's habit-learner into new **`becky-habits`**
  (`cmd/habits` + `internal/habits`): repeated corrections → learned defaults (threshold 2) —
  the learner half of becky's preference-learning loop.
- *Still available from dawbase (Phase-2):* `capture.cpp` (miniaudio mic + pre-roll) → the
  native cgo AudioBackend for `becky-daw-engine`; precise f0 (pYIN/basic-pitch) stays
  `becky-hum`'s model boundary for melodic precision. A follow-up can wire
  becky-hum/vox/daw/canvas corrections logs into `becky-habits`.

---

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
