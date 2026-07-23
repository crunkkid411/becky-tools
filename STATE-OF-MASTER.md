## 6. Live handoff — current branch status

> **The full branch-by-branch history lives in `HANDOFF-LOG.md`** (newest-first, every cloud/local
> handoff). This section keeps ONLY the *current state of `master`* + what's pending for Jordan.
> When you finish a branch: write the detailed entry to the **TOP of `HANDOFF-LOG.md`** and update
> the short summary here. **Do NOT let this section grow into a full log**
> "Awaiting Jordan's Approval" goes at the bottom of this file

### Current state of master (as of 2026-07-22 18:00, `ecc35ec`)

Green and pushed. `go build/vet/test ./...` clean + `gofmt` clean-modulo-CRLF (the repo's `.go`
blobs are CRLF throughout — cosmetic on Windows per §4, CI-green on Linux); the lone `cmd/tts` test
FAIL is pre-existing/environmental and machine-dependent (the local TTS model is present, so
"degrades when no model" inverts); `build-all-tools.bat` builds all `.exe`s, INCLUDING the
`becky-review-engine.exe` alias (its own build script silently didn't, once — see below). Recent
landings (details in `HANDOFF-LOG.md`):

- **The 20-hour Becky Review 3 hardening run (2026-07-21 22:00 → 2026-07-22 18:00, cloud →
  free-fleet → local, → `ecc35ec`):** a cloud branch merged H-1's shared-state verbs and H-7's Go
  half (`c71c9fd`); the build script itself was broken — `build-all-tools.bat` had literal `0x08`
  backspace bytes eating `bin\becky`, so `becky-review-engine.exe` never rebuilt (`aff70ee`,
  fixed). Captions actually fixed this time (0 one-word lines, was 8, `a263f81`); a "missing
  waveform" turned out to be a clip with no audio track, not a bug (`b3ae48b`). **H-7 shipped
  end-to-end** — an amber Forensic button runs the whole pipeline in-app (`c58b2b5`), and the
  judge now gets the case guide plus a backfilled qmd index, proven on 2 real judged hits
  (`ac74c09`). Renders are now hard-blocked from ever landing on the `E:` evidence drive
  (`reel.ProtectedDrive`, 5 tests, `96caf1e`). The free-fleet day shift then landed 5 more
  UI-freeze/UX fixes (transcript view, a selection-sync crash, the Load Reel freeze, 4 remaining
  UI-thread freezes). The evening close-out fixed all 4 bugs the AM driver found, including
  **the 0xc0000409 crash root cause** — an ImGui style-stack underflow from the 2x button, not a
  buffer overrun (`ecc35ec`). **Verified end state:** deployed from `ecc35ec` on the real
  727-video library, idle CPU 1.4% (was 192%), full 12-point driver checklist passes. Open items
  in `CONTINUE-HERE.md`.

- **Becky Review 3 idle-CPU root cause + Segoe UI font (2026-07-20 PM, local, `2c6fb53`):** the
  app burned **490% of one core sitting idle** because the video pane repositioned and
  force-repainted mpv's overlapping `--wid` child HWND *every frame*; DWM recomposited that region
  60×/sec across ~12 thread-pool threads. Now touched only when the rect actually changes →
  **46.9%**, and identical whether the window is focused or not. This was Jordan's "slow as fuck /
  clicking is slow / 10-15fps playback / 50% CPU at idle" in one bug. Four other theories (flip vs
  bitblt swap chain, Present/DwmFlush/waitable-timer pacing, uncapped render loop, WARP) were each
  measured and ruled out — see `HANDOFF-LOG.md`, don't re-test them. Also: real **Segoe UI** base
  font (was ImGui's ProggyClean bitmap), non-blocking scrub, playhead no longer jitters backward,
  and the **visual critic is genuinely registered and firing** now (`scripts/visual-critic.ps1` →
  `BeckyVisualCritic`, every 2h, free models only) after being claimed-but-absent twice.
  **Open + now top priority: mpv is the wrong engine for an NLE** (Jordan's call, and the
  measurements agree) — playback is still ~488% UI + ~548% mpv with the GPU pinned, and it owns
  the sub-frame cutpoint error. Replacement direction (libavcodec/NVDEC direct, frame-exact
  backward-seek-then-decode-forward, own swap chain) is written up at the top of `CONTINUE-HERE.md`.

- **Vegas import + cut-snapped captions (2026-07-19, local, `brn-native-review`):** Jordan's Vegas
  edits now reach becky (`becky-otio --import`, Vegas EDL TXT + FCP7 XML), and captions are timed to
  the CUT POINTS instead of the raw transcript (`becky-subtitle`, `internal/subs`) so they stop
  flashing at cuts — the `cli-cut` `build_master_srt` logic that never got ported to Go. One call
  does the lot (`--edit` imports, auto-transcribes, chunks, burns); one-click `Caption This Edit.bat`.
  Verified on a real 88-cut edit: both Vegas files import to 88 clips agreeing to 0.31 ms, and the
  burned captions still match an INDEPENDENT transcript at 149 s (zero cumulative drift).
  **Two findings worth carrying forward:** (1) `cli-cut`'s 0.120 s pause constant does not transfer
  to Parakeet (49% of its words have `end == start`, so ordinary speech reads as a 0.16-0.24 s "gap"
  and every word became its own caption) — the threshold is now derived from the transcript's own
  p90; (2) **`becky-reel render` drifts +1.27 s over 88 cuts** (renders at 30.0 fps against 29.97
  sources and does not quantise per-clip durations to output frames) — harmless for Jordan today
  because he renders from Vegas, but it is a real bug and any caption burned onto a *becky-reel*
  render would desync by ~38 frames. Not fixed here.

- **Becky Review round-5 fixes — 12 reported bugs + 3 new features, all live-verified (2026-07-02,
  local):** the real root cause of "scroll-to-zoom randomly stops working" was found —
  `round(2*1.15)=round(2.3)=2`, a rounding fixed point at small zoom values that NO amount of
  wheel-scrolling can escape (only a bigger jump, like the +/- buttons' 1.5x, crosses it) — fixed in
  `setZoom` (`ui/app.js`). Also fixed: Ctrl+Left/Right getting stuck re-landing on the current clip's
  own edges instead of traversing the whole timeline; cutting/trimming a clip while the timeline is
  actively playing used to leave mpv silently drifting on the stale pre-edit EDL (now reloads
  immediately at the same position); splitting a clip left the pre-split (left) half selected instead
  of the new right half; left-panel mouse+keyboard selection showing two highlights at once after
  arrow-key nav; the CMX3600 `.edl` writer used a video-only `"V"` channel, so Vegas Pro imports had no
  audio (now `"AA/V"`; `becky-hits` also emits this sidecar). Plus: `render/` output folder excluded
  from search/browse, the named toast popups removed, playhead restyled, Up/Down zooms when the list
  isn't focused, Spacebar plays the keyboard-selected row, a screenshot button
  (`Screenshot_NNNN.png`), and a non-intrusive busy bar for slow (>1s) engine calls. Every item verified
  against a real running instance (CDP + real Win32 mouse/keyboard input, real ffmpeg renders, real mpv
  playback) — not just code review. Nothing left for local; this session did both halves itself.

- **becky-transcribe gap-fill — long-clip transcripts no longer come up ~48 min short (2026-06-29, local,
  `claude/transcribe-audio-gapfill`):** sources whose audio drops out mid-stream (yt-dlp livestream VODs) have
  ~48 min of timeline with NO audio packets; plain `ffmpeg` extraction dropped the gaps -> a too-short WAV ->
  every Parakeet timestamp compressed (a 2:58:04 video's transcript ended at 02:10). Fix = one filter,
  `-af aresample=async=1:first_pts=0` in `extractAudio` (`cmd/transcribe/main.go`), so silence fills the gaps
  and the WAV matches the video timeline. No-op on clean files (byte-identical). Proven end-to-end: the
  TakingBack2007 video now transcribes to 02:58:14. Regression test `TestExtractAudioFillsTimelineGaps`. Details
  in `HANDOFF-LOG.md`.
- **WHORETANA — the native voice GUI Jordan opens (2026-06-29, local, master `1ff1e06`):** `gui/Whoretana`
  (WPF, .NET 8) — a cyan glitch HUD with a SkiaSharp particle **orb** you talk to. Orb idle/listening
  (mic-reactive)/**speaking (emergent face, mouth lip-syncs to TTS amplitude)** under datamosh glitch;
  Deacon Flock title; `#22E8FF` + `#ff3366` only, no purple. Live tool grid from `becky-catalog --json`,
  workflow buttons, ops menu, circular CLI launchers, VU dial, electric chat box. Chat + hold-to-talk route
  through **`becky-voice.exe`** (NDJSON; red tier confirms) with a `becky-ask` fallback; STT `becky-transcribe`,
  TTS `becky-tts` (lip-sync). Also shipped **becky-voice Phases 1-2** (`cmd/becky-voice` + `internal/pack` +
  `packs/`; `--selftest` 5/5). Launch: Desktop "Whoretana" or `Open Whoretana.bat`. Verified by mouse/keyboard
  + screenshots. **Left (Jordan's key):** Gemini 2.5 Flash realtime (HANDOFF-BECKY-VOICE Phase 3.1) needs
  `GEMINI_API_KEY` + a realtime Python helper — clean next step on the working local loop.

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

### Awaiting Jordan's go/no-go (spec landed, NOT yet built)

- **OCR ensemble + adversarial corroboration (`SPEC-OCR-ENSEMBLE.md`, landed 2026-06-28).** The
  *spec* is on master (multi-model OCR ensemble + adversarial ≥2-engine corroboration; adds the
  Unlimited-OCR long-doc slot; GLM-OCR↔PaddleOCR-VL A/B; a mandatory leaderboard-sweep process fix;
  claim/INBOX-3 in `COLLAB-PROTOCOL.md`). It is design only — **nothing is built yet.** Before
  anyone codes `internal/ocrfuse`, Jordan ratifies and settles the §10 open decisions (doc-slot A/B,
  threshold T, critical classes, long-doc in v1?, agreement tol, escalate-only vs `--thorough`);
  then cloud can build the deterministic core with no models.