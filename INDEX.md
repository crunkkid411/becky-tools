## 5. Index / Doc map — which file, when

**Canonical (read these):**
- `CLAUDE.md` — how we work. **NO LONGER** the *current* handoff state (§6) - moved to STATE-OF-MASTER.md and INDEX.md (Jordan, 7-04-2026).
- `INDEX.md` (this file) - *current* doc map; which file, when. STOP updating in CLAUDE.md; it lives here now.
- `BECKY-USER-GUIDE.md` — **plain, no-fluff guide for USING becky (not building it): the
  watch-a-video commands, the real working-tool list, how to run a workflow `.json` +
  the opt-in agent step, and an OSINT quickstart. For Jordan or any human/agent that just
  wants to RUN becky.** New 2026-07-14. Keep it current when tools/commands change.
- `MASTER.md` - *current* state of master. STOP updating in CLAUDE.md; it lives here now.
- `HANDOFF-LOG.md` — the **full branch-by-branch handoff history** (newest-first). `STATE-OF- MASTER.md`
  carries only the current state; the complete log of every cloud/local session lives here. Append
  finished-branch entries to its TOP; never let CLAUDE.md or STATE-OF-MASTER.md grow back into a full log.
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
- `HANDOFF-VEGAS-CAPTIONS.md` — **the VEGAS captions work order**: `vegas/BeckyCaptions.cs` puts
  becky-timed captions on the timeline you already have open (adapted from louismathy's
  `WhisperAutoSubtitles.cs`, keeping its style dialog + placement, replacing whisper with
  becky-captions and its N-word splitter with becky's chunker). Read it before touching the Vegas
  scripts or becky-subtitle's `--timeline`/`--cues` seam; `vegas/README.md` is the user-facing half.
- `HANDOFF-REAPER-BRAIN.md` — **the REAPER Chat brain v2 work order**: the llama-server brain
  hogged the machine and errored at every REAPER launch; `internal/reaperbrain` is now a
  featherweight :11435 proxy answering via Claude OAuth or OpenCode Zen free models (spend guard
  in code). Local: kill the machine-side llama-server autostart, chat-test in real REAPER.
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
- `HANDOFF-NATIVE-TIMELINE.md` — **THE handoff for the native GPU timeline that REPLACES Becky Review's
  WebView2 DOM timeline in-window** (read before any `native/becky-timeline/` or `gui/BeckyReviewNative/`
  work). Jordan is the fastest human editor alive; every "good enough" NLE lags his hands. Records the
  engine bake-off (what's ruled out + why — Vegas/Shotcut/MLT/GES/2×libmpv), the WebView2 AIRSPACE
  breakthrough (embed via D3D11 + a FLIP-model swapchain — OpenGL/BitBlt don't composite; the weeks-long
  wall), what's BUILT (embeds + scrubs at 130fps, committed `49f90e0`), and the complete roadmap to port
  EVERY Becky Review feature/integration into the native timeline. `gui/BeckyReviewNative` is a DELIBERATE
  throwaway copy of `gui/BeckyReview` — iterate freely; the goal is to replace that timeline entirely.
- `SKILL.md` — how to *build* becky-tools, this is for YOU, not for the agents **USING** them.
  **Its last section, `VIDEO CLIPPING`, is the canon for the shorts pipeline** — every tool and
  flag in the chain, the nine-rung framing ladder, the critic loop, Jordan's measured edit
  standard, the six rules that are law, the traps, and an index of the research behind each
  decision. Written 2026-08-21 so a fresh agent can build a clipping pipeline without
  re-deriving any of it. Read it before touching `becky-short`/`becky-moment`/`becky-hits`.
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
- `SPEC-BECKY-FOREMAN.md` (SPEC ONLY 2026-07-14, NOT built — Jordan: "dont build it yet") —
  **the deterministic job runtime ("Factory Foreman")**: one always-on `becky-foreman.exe`
  watching a work-order FOLDER (`X:\AI-2\becky-jobs\`); anything that writes a JSON file
  files a job; foreman runs catalog verbs station-by-station with validate/retry×3/
  needs-review + GPU lock, notifies MC's existing pipe / Whoretana `say`. Supersedes
  `bridge-layer-proposal.md`; documents the 4 cross-repo IPC drift bugs found 2026-07-14.
  Read before ANY orchestration/bridge/IPC/queue work across becky↔MC↔Whoretana.
- `SPEC-BECKY-CHROME.md` (SPEC ONLY 2026-07-14, NOT built) — **why every CLI tool fails to
  drive Jordan's real Chrome, and the exact fix.** Root cause: Chrome 136+ (Apr 2025)
  IGNORES `--remote-debugging-port` on the default profile, so CDP tools (extension /
  browser-harness / chrome-devtools-mcp) can't attach to his logged-in Chrome and silently
  fall back to a blank browser (= the "sandbox"). Fix = ONE dedicated automation Chrome
  (`--remote-debugging-port=9222 --user-data-dir=X:\AI-2\becky-chrome-profile`, log in once,
  every tool attaches to 9222) + a Win32 OS-level fallback (the "Qwen way"). Read before ANY
  browser-control/OSINT/CDP work.
- `SPEC-BECKY-VIDEO-ENGINE.md` + `HANDOFF-VIDEO-ENGINE.md` — **the mpv replacement
  (2026-07-21): libavcodec/D3D11VA direct playback inside Becky Review 3.** The spec carries
  the decided architecture, the exact FFmpeg API map, and the risk order; the handoff is the
  8-step staged work order (harness first, wire-in last) for the local agent. Read WITH the
  mpv verdict in `CONTINUE-HERE.md`. Does NOT touch render/export.
- `research/videoagent-integration.md` — the VideoAgent→becky **intent→verb mapping**
  (`BUILD-INPUTS.md:29`'s promised doc): how AI-planned edits land as engine verbs on the
  timeline instead of rendered .mp4s; the H-1..H-7 seam status table. Read WITH
  `HANDOFF-VIDEOAGENT-SEAM.md`.
- `research/qwen38-max-video.md` — **Qwen3.8-Max native video: what it actually takes and how
  to use it (2026-08-17, cloud; RESEARCH ONLY, rewritten after the first draft got the
  headline wrong). Read before any hosted-vision or long-video work.** Corrected findings:
  (1) **"entire shows and livestreams" is literal** — one request takes **64 videos × 2 h /
  2 GB each = 128 hours**, natively, no frame extraction; (2) the earlier "100 h can't fit"
  claim was a **token-math error** — Qwen's own card recommends `longest_edge=469,762,048`
  = **224K video tokens for a whole hour-scale video**, ~14× cheaper than per-frame
  arithmetic, so 100 h of high-fidelity review is **~$20–45**, not a barrier; (3) **audio is
  not a listed input modality** (text/image/video in, text out) — the website evidently
  supplies ASR, so on the API **pass a timestamped transcript alongside the video**; the omni
  model is a different one (`qwen3.5-omni-plus`); (4) **Qoder cannot take video at all** —
  images/PDFs only, so the subscription is not the vehicle for this. Use chat.qwen.ai (free,
  ~500 MB uploads) or the Model Studio API (1M free tokens/model, 90 days, Singapore).
- `HANDOFF-SHORTS-2026-08-20.md` — **the CURRENT state of the clipping pipeline** (rewritten
  2026-08-21): what broke and why, the critic loop, LR-ASD as rung 0, what is verified on real
  footage vs what is not, and the ordered next steps. `SKILL.md`'s VIDEO CLIPPING section is the
  reference; this is the running log of the work.
- `research/shorts-models.md` + `HANDOFF-SHORTS-PIPELINE.md` — **the short-form repurposing
  arc (2026-08-15, cloud). Steps 0-2 BUILT + proven; steps 3-6 need the native CV dep Jordan
  ratified. Read before any short-form / 9:16 / auto-reframe / active-speaker work.** Ships
  `becky-moment` (`cmd/becky-moment` + `internal/moment`: structure measured deterministically,
  CONTENT judged by an LLM via **OpenCode Zen**, the two corroborated — disagreement is HELD at
  the lower signal, never averaged; `--selftest` 13/13) and `internal/facetrack` (persistent
  track IDs via IoU + ArcFace, **deliberately not wired** because `face_embed.py` returns only
  one face per frame — the work order is HANDOFF §7 step E). The research doc is the
  model-by-model comparison of what the field's
  OSS shorts tools use vs what becky has: becky's model is BETTER at three stages (SCRFD-10GF
  vs UltraFace — 83.1% vs 43.8% WIDER FACE **Hard**; Falcon-Perception vs GroundingDINO's class;
  Parakeet vs Whisper, incl. why a transducer can't hallucinate a fake "viral moment" into a
  silent gap), and becky has **nothing** at five others (person detection, tracking, ASD,
  crop-path, virality rubric). Names **LR-ASD** (IJCV 2025; 1.0M params, 94.45% mAP AVA) as the
  one new model worth adopting, and CORRECTS an earlier dismissal of MediaPipe/OpenCV — MAR
  variance is obsolete, but the LIBRARIES (Pose Landmarker, 52 blendshapes, optical flow,
  camera-path smoothing) are load-bearing and becky uses neither. **Its §6 is the important
  part:** becky's vision layer is largely the *degrade path* of a stack never fully built,
  because heavy-CV options were repeatedly rejected as "cloud can't build cgo/OpenCV" and then
  never built locally either — an architecture shaped by what the cloud agent could compile,
  which is the real reason vision flopped on the forensic footage. The HANDOFF doc is the
  engineering half: why this pipeline fails if built the usual way (**every seam bug in
  `STATE-OF-MASTER.md` was a wiring failure, not a model failure — and a shorts pipeline fails
  *silently*, since a wrong-speaker crop still renders fine**), the preconditions (fix
  `becky-reel`'s +1.27 s drift FIRST; seam tests not unit tests; every stage declares confidence
  and may REFUSE), and a 6-step build order where step 1 ships value with zero new models.
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