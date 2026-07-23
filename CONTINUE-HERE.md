# Start here (read this first, every session)

This file exists so you don't have to ask Jordan anything to pick up where the
last agent stopped. Read it, then go straight to work — don't re-ask him what's
already answered below.

## 2026-07-23: mpv IS DELETED. Becky Review 3 runs the native engine.

The overnight mpv-swap mission (HANDOFF-VIDEO-ENGINE.md, all 8 steps checked
with evidence) landed on master. Video is now an IN-PROCESS engine
(`native/becky-review/engine.cpp`: libavcodec/D3D11VA on its own D3D11 device,
NV12->BGRA into shared textures, ImGui paints the pane; WASAPI audio is the
master clock). No mpv.exe, no pipes, no child HWND, no EDL temp file.

- Measured on the real 88-clip reel: idle **46.9% -> 9.4%**, playback
  **~1036% -> 11.5%** of one core; 104-step scrub churn with zero freezes;
  crash.log clean. Full numbers + screenshots: HANDOFF-VIDEO-ENGINE.md step 7.
- Rollback: `native/becky-review/becky-review-mpv-backup.exe` (the last mpv
  build) sits beside the new exe. Rename it back if the engine misbehaves.
- Jordan's wake-up checks (ears/eyes only): audible sync, scrub across a cut,
  Ctrl+Right frame-exactness vs grab_frame stills.
- The app dir needs the FFmpeg DLLs beside the exe (96 MSYS2 DLLs, already
  in place, gitignored). `_build.bat` stages headers itself; it must link the
  MSYS2 `.dll.a` files BY FULL PATH (GStreamer's lib dir ships an older
  libav that must not win).
- Deliberate ceilings: 2x speed audio time-stretches via libavfilter atempo
  (pitch-preserved, not silent - verified `e7ee4d7`); software-decode
  fallback has no draw path; overlay/captions are ImGui-drawn now.

## The rules that keep getting broken (do not repeat them)

1. **FREE OR OAUTH ONLY. NEVER A PAID API.** Jordan already pays for Claude Max —
   Sonnet 5 / Opus / Haiku come free through the OAuth session (Agent tool with
   model "sonnet", `claude --model sonnet`, or `fleet-run.ps1`). If you reach an
   Anthropic model through a pay-per-token key instead, you are spending money he
   already spent once. On OpenRouter, the only free model is `tencent/hy3:free`.
   **`m3` / `minimax` is NOT free on OpenRouter** — it's free only through the
   local router. He has corrected this twice. Don't ask him again; just follow it.
   A hook (`~/.claude/hooks/block-paid-apis.ps1`) blocks paid calls automatically,
   but don't rely on the hook as your only check — know the rule.
2. **Prose is not protocol.** If a rule actually matters, it has to be a hook or a
   code guard. Writing another paragraph into a `.md` file is not a fix — Jordan
   considers that worse than doing nothing.
3. **Never relay a subagent's "verified" as fact.** If a subagent says something
   works, pull the actual frame/output yourself and look at it, or count it
   yourself. Trusting a subagent's word here already cost two days.
4. **Never make Jordan run a command or answer a technical question.** He is not
   a developer. Decide things yourself from this file and the other handoff docs.
5. Becky Review 3 was supposed to be grown from `native/becky-timeline`
   (see `BUILD_1.md`, `COLLAB-PROTOCOL.md`) and mostly wasn't — prefer reusing
   `becky-timeline` code over writing more C++ from scratch.
6. **The point of Becky Review 3 is `BUILD_1.md` §4-H and §10. Read them before
   you touch it.** The bug-fixing in `HANDOFF-BECKY-REVIEW-3.md` is maintenance,
   not the product. See "The missing half" below — an agent that reads only the
   handoff will rebuild a video player and think it finished.
7. **Becky Review 3 runs `becky-review-engine.exe`, NOT `becky-clip.exe`.** Both
   are built from `cmd/clip`. On 2026-07-20 the alias was three hours stale and a
   whole night of engine fixes was invisible in the GUI while every test passed —
   the exact shape of "works on my machine". `build-all-tools.bat` now builds the
   alias explicitly. If a Go change does not show up in the app, check this first.
8. **Output goes with the footage that made it.** Never the cwd, never a
   hardcoded drive. A render lands in a `Rendered/` subfolder of its source; a
   proxy/still lands beside its source. This is enforced in
   `internal/reel/reel_output_test.go` because as prose it silently drifted and
   put Jordan's YouTube edits on E:\ — a removable forensic drive holding
   evidence for a criminal case.

## 2026-07-20 PM — THE REAL PERF ROOT CAUSE (found + fixed), and the mpv verdict

**Read this before touching performance. Four earlier theories were wrong and are
already ruled out — do not re-test them.**

### The bug: we repositioned mpv's child window 60 times a second

Jordan: *"it's slow as fuck… clicking is slow… 10 or 15 fps playback… it's using about
50% of my cpu just sitting idle."* All one cause.

The video pane called `MoveWindow(..., bRepaint=TRUE)` **and** `ShowWindow` on mpv's
overlapping `--wid` child HWND **every single frame**, even when the pane had not moved
one pixel. (With no reel loaded the other branch called `ShowWindow(SW_HIDE)` every frame
instead — which is why even an EMPTY app burned 3.4 cores.) Moving and force-repainting an
overlapping child window at 60Hz makes DWM recomposite that whole region every frame, and
that work fans out across ~12 driver/compositor thread-pool threads.

| idle, maximized, his real 88-clip reel | background | foreground |
|---|---|---|
| before | **490%** of one core | 412% |
| after  | **46.9%** | 44.9% |

~10x. Under half a core, and now identical whether the window is focused or not.
Fix: `SetWindowPos` with `NOREDRAW\|NOCOPYBITS` **only when the rect actually changes**,
and `ShowWindow` only on a transition (`g_mpvChildShown`). `crash.log` now logs the rect
changing twice per session instead of 60x/second — that log line is the regression canary.

### Four theories that were MEASURED AND WRONG — don't repeat them

1. **Flip-model vs bitblt swap chain.** Changed `FLIP_DISCARD` → `DISCARD`: 490% → 470%. Not it.
2. **Present/vsync pacing.** `Present(1,0)` vs `DwmFlush` vs a high-res waitable timer: all
   ~400%. (DwmFlush also only paces while the window is FOREGROUND — backgrounded it returns
   instantly and the loop spins. Don't use it for pacing.) Not it.
3. **Render loop running uncapped.** Measured 61 fps with the frame trace the whole time. Not it.
4. **WARP / software rendering.** The GPU counter showed real 3D-engine use. Not it.

The tell was that idle CPU collapsed to **29% the instant the window was minimized** (no
frames drawn ⇒ no `MoveWindow`), and that the hot threads were ~12 **ntdll thread-pool**
threads — i.e. the compositor, not our code. If perf regresses, measure minimized-vs-visible
FIRST; it splits "our render path" from "everything else" in one step.

**Also fixed in the same commit** (real improvements, none of them the root cause):
`emitScrub` no longer blocks the UI thread on a synchronous engine round-trip whose reply was
thrown away (a coalescing seek worker takes the latest position); the playhead no longer
jitters BACKWARD a frame during playback (the between-update extrapolation overshot, then
re-anchoring to mpv's clock snapped it back).

### SETTLED: mpv is the WRONG engine for an NLE. Next phase is libavcodec direct.

Jordan, 2026-07-20: *"mpv is a lazy dev choice and inappropriate for an NLE."* He is right,
and the measurements agree. Playback still costs ~488% (UI) + ~548% (mpv) with the GPU pinned
at 99.8%, and **that part is architectural, not a bug** — the idle fix above does not touch it.

What mpv structurally costs us:
- an **overlapping child HWND** — the thing that caused the spin above, and a permanent DWM
  composition conflict with our own swap chain;
- every seek is an **IPC round-trip over a named pipe** — the only reason scrubbing ever
  needed an async worker;
- our clock has to be **synced from mpv's reported `time-pos`** (a player's clock) — the sole
  source of the playhead jitter;
- **the sub-frame cutpoint error Jordan reported lives here.** The reel data is frame-exact —
  verified: every `in`/`out` in `post_constantly.reel.json` lands exactly on a frame at true
  29.97 (30000/1001). The error is in mpv's EDL playback, not our import math
  (`internal/edl/vegasimport.go` snaps to the frame grid correctly);
- no CPU access to decoded frames without decoding a second time.

**The decided replacement** (matches the earlier native-NLE bake-off: "own compositor —
independent NVDEC/D3D11 decoders + own shader + VRAM ring"):
`libavformat` + `libavcodec` directly · hardware decode via **d3d11va / NVDEC** (copy-back only
when pixels are needed on the CPU) · **`av_seek_frame` + `AVSEEK_FLAG_BACKWARD`, then decode
FORWARD to the exact target frame** — this is the standard frame-exact seek and is precisely
what mpv cannot give us · a **frame ring/cache around the playhead** for zero-latency scrubbing ·
draw the frame as a **D3D11 texture inside our own swap chain**.

That one move deletes the child window, the DWM conflict, the IPC seek latency, the clock
jitter and the sub-frame cut error together. It is a substantial build, not a patch — but it
is the difference between "a player embedded in an app" and an NLE.

## What's already done — MEASURED. Read the scope line first.

**Scope, so this list is not read as more than it is:** everything below is the
RENDER/CAPTION/PLAYBACK pipeline plus timeline input. It is NOT the product.
The product is §"The missing half" (H-4..H-7) and the 120-item
`GUI-ACCEPTANCE-CHECKLIST.md`, and most of that is still unbuilt. An adversarial
audit on 2026-07-20 flagged this exact section for reading like "the app is
done" — it is not, it is the plumbing under the app.

- **A real edit session was driven with mouse + keyboard and passed, 2026-07-20
  ~01:50.** Not a smoke test, not a fixture — his actual 88-clip
  `post_constantly` reel, driven with Win32 mouse clicks and keystrokes, with a
  screenshot examined at every step (kept in the session scratchpad `e2e/`):
  click a clip → selects; `S` → splits at 4.7s; `Ctrl+Z` → **one press** reverses
  the whole split; `Space` → plays, `Space` → pauses; `Ctrl+Right` ×3 → 4.7s to
  13.2s across three edit points. App still alive afterwards, `crash.log` clean
  (0 errors), captions rendering throughout. This is the `CANVAS-NORTH-STAR.md`
  DoD (exercised by mouse + keyboard), actually met.
- **There is a finished, postable video.** `X:\Videos\2025\11_November\Rendered\
  post_constantly.captioned.mp4` — 269MB, 4500 frames at 30000/1001, audio
  present (aac 48kHz stereo, 150.144s). Built 2026-07-20 00:57 from his real
  Vegas `.xml`.
- Captions burn in. EVIDENCE, not a relayed claim: the frame at t=130s was
  extracted with ffmpeg and looked at directly — it reads "27 times a day",
  white bold, black outline, low-centred, and the number stayed with its unit
  (his rule). Re-extract any frame yourself if you doubt it; do not take this
  line's word for it either.
- Render is frame-exact. The reel computes to 150.183s = 4501 frames; the output
  is 4500 because **Jordan's own Vegas render is 4500 frames** — the burn is
  frame-for-frame identical to its input, and audio is `-c:a copy` so it is
  bit-identical. The one-frame delta is in Vegas's render, not in ours.
- Two different Vegas exports of the same edit produce identical cut points on
  all 88 clips, to 0 microseconds.
- Audio plays during playback.
- The Becky Review 3 window opens in about 1 second.
- Building captions takes about 12 seconds, costs nothing, and rotates across
  free models automatically.
- A scheduled Windows task ("BeckyModelHeartbeat") checks in on the free models
  every 30 minutes and writes `X:\AI-2\fleet\model-heartbeat.json`.

## SETTLED 2026-07-20: native wins. Stop re-opening this.

Jordan, in his own words, going to bed on 2026-07-20:

> "NATIVE MATTERS - WPF and Shotcut literally could not keep up with how fast i
> work the time line (i'm one of the fastest video editors in the world)... the
> becky-review-native app FROZE when i tried touching it (cuz i'm too fast - i
> wasn't even trying; literally my muscle memory broke the entire goddamn
> thing). you gotta make it work, and make it as snappy as Vegas Pro timeline
> (or faster)"

So:

- **`native/becky-review` (C++/ImGui) is the app.** WPF froze under his real
  input rate. That is disqualifying and no amount of layout polish fixes it.
- **`gui/BeckyReviewNative` (WPF) is the REFERENCE for LAYOUT AND FEATURES ONLY** —
  ten rounds of his feedback live in its design. Copy what it looks like and
  what it does. Never copy how it is built.
- **Responsiveness IS correctness here.** He is a professional editor whose
  muscle memory outruns the app. A feature that is right but janky has failed.
  The bar is Vegas Pro's timeline or faster.
- He also said the choice of timeline was the agent's call. This is the call.
  Do not spend another night re-deciding it.

## The missing half — what Becky Review 3 is actually FOR

Jordan caught this on 2026-07-20: *"i had fable 5 do a deep dive on
HKUDS/VideoAgent and the features there. if the opencode model misses what we
decided from that topic, then becky 3 is missing core features."* He was right.

The decision is `BUILD_1.md` §10 (L578-621), from that deep dive. The finding:
VideoAgent renders finished video directly and has **no editable intermediate
representation** — so becky's job is to keep its intent→workflow brain and
**replace its "render the video" terminal step with "emit becky engine verbs"**,
so an AI edit lands on Jordan's timeline instead of rendering around him. His
own words (`BUILD-INPUTS.md:13`): *"human review optional right on the timeline
instead of burning it all together as an .mp4."*

Status, checked 2026-07-20 — **the app half is mostly not built**:

| | Requirement (`BUILD_1.md:477-484`) | State |
|---|---|---|
| H-4 | `apply_edit_batch` — a whole AI pass is ONE undo span | **BUILT (Go), deliberately not called raw from C++.** `cmd/clip/edit_batch.go` (one `pushUndoLocked` before the op loop), dispatch `bridge.go:223-232`, tests in `edit_batch_test.go`. The C++ side never calls the verb directly — it doesn't need to: every approved chat edit routes `applyActions` → `ApplyEditBatch`, so Jordan already gets one-press undo. The raw verb only matters for a future planner. |
| H-5 | `event` stream announcing AI activity **without blocking Jordan's editing** | **BUILT, both sides.** Go: `app.go:121-136` (`EventEmitter`/`emitEvent`), `app.go:1336/1347/1350`, NDJSON writer `main.go:195`. C++: reader branch `main.cpp:186-213`, capped 50-deep deque, passive render in the frame loop. |
| H-6 | plain-language intent in chat → timeline edits he can see/adjust/undo, via the existing `ask`/`apply_proposal` seam (**never fork it**) | **BUILT, both sides, seam NOT forked.** Go `app.go:1336-1350` + `bridge.go:212-222`; C++ ask / apply / reject + the proposal card. |
| H-7 | forensic path in-app: query → qmd recall → becky-judge → becky-hits reel on the timeline | **DONE 2026-07-22.** `forensic_query` (Go, landed 2026-07-21) runs becky-judge → becky-hits and lands the reel via `LoadReel`; the C++ entry point — an amber Forensic button in the ask-becky panel — landed 2026-07-22 (`c58b2b5`), same async shape as `apply_proposal`. Live-proven: 2 real judged hits on the `E:\TakingBack2007` corpus. |

> **Corrected 2026-07-20 PM by a full code audit.** The three "not found" entries above were
> wrong — H-5 and H-6 are real and H-4 is a defensible deviation, verified in BOTH languages.
> H-7 is the genuine gap. Believe the code, not the older prose further down this file.

### Two more things that audit found, worth fixing before H-7

- **`Open Forensic Hits.bat` launched the WRONG APP** — it `call`ed `Open Becky Review.bat`,
  i.e. `gui\BeckyReview\...\BeckyReview.exe`, the deprecated **WPF + WebView2** build that froze
  under Jordan's input rate and violates acceptance items 100/119 ("no embedded browser engine,
  ever"). So the entire forensic workflow landed in the dead app. **Fixed** — it now calls
  `Open Becky Review 3.bat`; the `BECKY_REVIEW_*` env vars were always correct and are inherited.
- ~~**H-1 shared state is DEAD CODE, both directions.**~~ **FIXED 2026-07-21 (cloud):**
  `seek`, `set_select` and `set_threshold` now exist in `bridge.go`; the state lands in
  `assistant.Context.Timeline` (Playhead/Selected/SkipQuiet*) and the prompt's TIMELINE block
  prints it, so "delete this clip"/"split here" can resolve against where Jordan actually is.
  Unit-tested through the bridge (`TestCallH1SharedStateVerbs`). The three C++ worker threads
  needed no change — they were already firing the right payloads at a table that didn't know
  them. (Historical context kept: this dead seam was also why making `emitScrub` async was
  free — it was blocking the UI thread on a round-trip to a verb that did not exist.)

Constraints that go with it: **no MCP server, no separate AI tool surface** —
the AI uses the SAME shared-state JSON / engine-verb seam the human UI uses
(`BUILD-INPUTS.md:18-22`). The Go engine is never forked; new capability is an
additive verb (`BUILD_1.md:27-28`).

Also missing: `BUILD-INPUTS.md:29` promised `research/videoagent-integration.md`
(the intent→verb mapping). It was never written. `research/` has only
`velo-logic-mining.md` from that batch.

**Why this section exists:** `HANDOFF-BECKY-REVIEW-3.md` has ZERO mentions of
VideoAgent, §10, or H-4..H-7. It is entirely render/audio/caption bug work. An
agent working from that handoff alone would never learn the product spec exists
— which is exactly what happened.

## Night of 2026-07-19/20 — what actually changed, and how it was proved

Every item below was verified by RUNNING it and looking, not by the tests passing.

| Fix | Evidence |
|---|---|
| **The freeze.** Render/Save/Export EDL called the engine on the UI thread with timeouts up to 300s — the window was dead, not slow | Clicked Render, then pressed Ctrl+Right twice mid-render: playhead moved 0.0→10.3s, frame changed, work counter advanced |
| **Ctrl+arrow "sticks at the clip edge."** The modifier was being dropped — arrows read a LATCHED bit, Ctrl read an INSTANTANEOUS one, and a fast chord kept the arrow and lost the modifier | Same 4 presses: 0.1s before, **14.5s** after |
| **Every clip drew a black thumbnail placeholder.** `resolveSource` only accepted footage inside the browsed folder | Thumb cache 0 → 10 files; chips visible on every clip |
| **Every clip was the same blue.** `ClipView` had no `Color` field so the app's parse always missed | Timeline now violet (`#8A2BE2`, this source's permanent colour) |
| **`becky-review-engine.exe` was 3 hours STALE** — build-all-tools only built `becky-clip.exe`, so a night of engine fixes was invisible while every test passed | Rebuilt; the two fixes above only appeared after this |
| **Skip Quiet was unreachable.** `g_thrOn` declared false, never assigned — 6 reads, 0 writes | Clicked the new toggle: threshold bar appears (`-17 dB`), quiet regions dim |
| **Panels overlapped 3 ways; headings hidden under the menu bar; filenames sliced** | Screenshot diff vs becky-review-native |
| **~50ms of input lag** — DXGI queued 3 frames, Present blocked after rendering | Waitable swap chain, MaximumFrameLatency(1), wait moved to top of loop |
| Escape deletes · timeline click restores keyboard · I-beam cursor · opaque selection (no white ring) · Up/Down zoom · >1s work indicator | Each driven and screenshotted |
| **Renders no longer land on E:** (his criminal-case evidence drive) — the destination came from the browsed folder, not the footage | Test fails if any path starts with `E:`; all 5 export call sites fixed |
| Captions: 202 cues/34 one-word/7 stutters → **150/8/1** | Counted independently, twice |
| **Redo** (Ctrl+Y / Ctrl+Shift+Z) — the engine had it all along, the app had zero references to it | split → undo → redo, three states screenshotted |
| **Toolbar buttons stop moving.** Play/Pause, Overlay and Skip Quiet each changed width with their label, shifting everything to their right | Button row cropped in both states and stacked — identical layout |
| **Extend selected clip by exactly one frame** (`<+1f` / `+1f>`), at that clip's own source rate | Timeline geometry shifts across its full width on click |
| **Ruler**: drag pans, click sets playhead + stock (it used to scrub) | Span moved 0:00–0:20 → 0:08–0:26 with the playhead held at 14.7s |
| **Up/Down zoom** | 4 presses: 0:20 span → 0:06 with half-second ticks |
| **A delete no longer drags the view sideways** — the scroll clamp ran every frame | Only intervenes when the view is past all content, never mid-gesture |

**`GUI-CHECKLIST-STATUS.md` is now STALE** — it was written before most of the
above. Items it lists as ABSENT that are now done: 15, 36, 37, 48, 52, 109 (plus
8). Re-audit before trusting its counts.

## WHERE IT ACTUALLY STANDS (2026-07-20 ~05:00, measured)

`GUI-CHECKLIST-STATUS.md` was RE-AUDITED against the current ~6100-line
main.cpp by reading it, not grepping it. **96 DONE / 17 PARTIAL / 4 ABSENT /
3 UNVERIFIED**, up from 69/31/11/9 at midnight. That file supersedes every
earlier audit; an older copy of it caused a real failure by being confidently
wrong in both directions.

The lag he reported on 2026-07-17 — *"every new clip on the timeline makes
everything - even my mouse - lag super bad for like 2 seconds"* — was found and
measured. It was **Render Selection**, sitting one line below Render, still
holding a 300s timeout on the UI thread after Render was made async:

| pressing Ctrl+Right during a Render Selection | worst frame | frames / 4.9s |
|---|---|---|
| before | **2,173 ms** | 407 |
| after | **47 ms** | 590 |

`addHitToTimeline` was suspected and cleared by measurement (4-16ms, zero
stalls). The remaining synchronous `engineCall` sites were also measured and
none stalls (median 16.0ms, 0 frames over 100ms in a full drag/scrub/split/
render session) — converting them is real risk for no measured gain. The
frame-trace harness is compiled in; revisit if one ever shows a stall.

## ~~KNOWN LANDMINE~~ — RESOLVED 2026-07-20, kept only for the reasoning

> **This is FIXED — do not go hunting it.** `drainAsync()` was moved OUT of `drawTimeline()`
> and into the main loop; `main.cpp` even carries a marker comment where it used to sit
> ("drainAsync used to be called HERE. It is not anymore"). Verified by code audit
> 2026-07-20 PM. The description below is retained because the reasoning is worth keeping,
> not because the hazard is live.

## KNOWN LANDMINE (historical) — flagged, deliberately not defused

`apply_proposal`'s async completion callback mutates `g_track` from inside
`drainAsync()`, which runs inside `drawTimeline()`. Traced 2026-07-20: it does
NOT crash today, because `drainAsync()` sits at the one point where the prior
`g_track` read has completed and everything after it re-reads `.size()`
bounds-checked. But it is a live hazard for whoever next touches that seam —
add a read before that point and it becomes a use-after-invalidate.

Fixing it properly means restructuring `drainAsync`, which every async caller
now depends on. That was judged too risky to do blind at 4am. If you are working
in `drainAsync` or the proposal path, fix this FIRST, with a test.

## What's left — in order

0. ~~**REPLACE mpv WITH A REAL VIDEO ENGINE (libavcodec direct).**~~ **DONE 2026-07-23
   (local, `local/video-engine` + `local/video-engine-swap` → `c21537e`).** mpv is deleted;
   libavcodec/D3D11VA decodes in-process, ImGui paints, WASAPI is the audio-master clock. Idle
   CPU 46.9% → 9.4%, playback ~1036% → 11.5% of one core (~90x); audio drift 0.01ms max; a
   25-seeks/sec scrub storm never blocked the UI. Full detail: `HANDOFF-LOG.md` top entry,
   `HANDOFF-VIDEO-ENGINE.md`.

1. ~~**Caption wording**~~ **FIXED 2026-07-22 (local, `local/fix-caption-strands`), measured
   on the real footage.** The 2026-07-21 cloud fix was verified INCOMPLETE on
   `post_constantly`: 5 one-word cues survived (posting/anything/videos/i/probably +
   a 33ms "learned") and none sits on a real pause (silencedetect: 81–117ms dips at
   best). Three more root causes fixed in `internal/subs`, each with a regression
   test pinning the exact grouping: RepairDangling stranding a word by pushing a
   dangler out of a two-word line; splitAtBiggestPause's lone tier (split ranking is
   now tiered, and a chunk that can only strand stays whole within the 28-char burn
   width); and float noise flickering ASR-quantised 0.32s gaps across the derived
   0.32s threshold (gapEps). Deterministic run after: **155 cues, 0 one-word lines,
   longest line 28 chars, narrowest cue 234ms**; "a thousand videos" is one cue
   again and "the fundamentals learned | against another creator" replaced the 33ms
   strand.
2. ~~**H-6/H-7 are the product.**~~ **DONE 2026-07-22.** H-4, H-5 and H-6 were
   already built and wired both sides. **H-7's C++ entry point landed** — the
   amber Forensic button in the ask-becky panel calls
   `engineCall("forensic_query", {query})` async, same shape as
   `apply_proposal` (`c58b2b5`). The judge now gets the case guide (rubric +
   aliases) and the qmd `_md` index was backfilled (58 srt→md, was 3 weeks
   stale) — live-proven on 2 real judged hits on the `E:\TakingBack2007`
   corpus (`ac74c09`). Recall quality is still rough — see item 3 below.
3. ~~**qmd forensic search needs tightening.**~~ **FIXED 2026-07-23 (local,
   `659b9c5`), live-verified by driving the real app.** `qmd.Search`'s hybrid
   path now sends a structured `lex: <query>\nvec: <query>` document instead
   of a bare query, skipping qmd's LLM query-expansion pass — measured 4.9s vs
   16.5-18.6s (the reported "16.6s typical, one 25s timeout" was exactly that
   expansion cost blowing past the native UI's 25s wait). `footage.GrepTranscripts`/
   `GrepOrphans` now use AND semantics over a multi-word query's non-stopword
   terms (single-word search unchanged): "cheated on by my wife" driven in the
   live app went from a 10k-hit blob to **20 quotes in 282ms**; smart search on
   the same query: **14 quotes, hybrid mode, 910ms**. Recall breadth (top-20
   cap) is still there — not re-touched.
4. ~~**Nothing auto-converts a new `.srt` to the qmd `_md` index.**~~ **FIXED
   2026-07-23 (local, `659b9c5`).** New `internal/qmdindex` converts a
   transcript into qmd's `.md` locator format; wired into `transcribeOne`
   (converts the instant a transcript completes) and into `ForensicQuery` (a
   catch-up `Sweep` before every judge run, for transcripts that land any other
   way). Locators are matched by their frontmatter `source:` field, not
   filename, so an earlier converter generation's different naming scheme
   (the 2026-07-22 manual backfill) is recognized and updated in place —
   caught live: an early version of this matched by filename and wrote 1128
   duplicates in one Sweep before the fix. Live-verified on the real corpus:
   found and correctly indexed 114 transcripts that had piled up since the
   backfill, 0 duplicates, idempotent on re-run (`qmd update` confirmed
   "113 new, 1 updated, 1193 unchanged").
5. **`GUI-CHECKLIST-STATUS.md`** has all 120 acceptance items audited against the
   code — 69 DONE / 31 PARTIAL / 11 ABSENT / 9 needing a human eye — with a
   ranked top-10 and the exact function each change belongs in. Items 1, 2, 3 and
   5 of that top-10 are now done; **#4 (ruler drag = pan, click = playhead+stock)
   is the next one.**
6. ~~Known-but-unfixed, from the audit: Play/Pause and the 3-state Overlay button
   change label WIDTH...~~ **STALE, verified fixed 2026-07-23 (code-checked, not
   doc-trusted).** A `fixedButton()` helper (main.cpp ~L842) sizes every toggling
   button to its widest possible label and is already used on Play/Pause (L6060),
   Overlay (L6099), Skip Quiet (L6203), Render Selection, and both extend buttons
   - none of them shift their neighbors anymore. Redo has both a toolbar button
   (L6166, `ICON_REDO`) and key chords (Ctrl+Y / Ctrl+Shift+Z, L5080-5081) - already
   wired. `g_scrollSec`'s clamp (L3029) is gated `g_gest.kind == 0` (only applies
   with no active gesture), so a mid-drag/delete no longer drags the view sideways.
   This whole item appears to predate a later fix pass; do not re-implement it.
7. `cmd/tts`'s `TestRun_DegradesWhenNoModel` fails machine-dependently on this
   box (pre-existing) — the local TTS model is present here, so the
   "degrades when no model" case inverts. Not a regression, just noise in this
   machine's `go test`.
8. **Fleet lane status (2026-07-22):** "Overnight Autopilot Guard" was
   re-armed (its schedule had expired); a new "Becky Claude Deadman" task
   (every 2h) removes `X:\AI-2\fleet\PAUSE` if the repo has had no commits for
   2h — that file is the lane switch between the Claude agents and the free
   fleet. It is currently PRESENT, so the free fleet is standing down.
9. ~~**2x speed plays silent.**~~ **FIXED 2026-07-23 (`e7ee4d7`), verified in code.**
   `engine.cpp` runs a libavfilter `atempo` graph in `audioDecLoop` (~L757-903);
   any playback rate now plays time-stretched audio, not silence.
10. **No software-decode draw path.** The engine assumes D3D11VA hardware decode; it has never
    failed on this machine, but there is no fallback frame path if it ever does.
11. **FFmpeg DLL closure needs slimming.** The app dir ships the full 96-DLL MSYS2 build
    (gitignored, works today). Planned: swap to a 5-DLL self-contained FFmpeg shared build —
    do NOT rebuild FFmpeg from source to get there.

## Jordan's real footage to test with

`X:\Videos\2025\11_November\Rendered\post_constantly.xml`
`X:\Videos\2025\11_November\Rendered\post_constantly.mp4`
`X:\Videos\2025\11_November\Rendered\post_constantly.srt`
