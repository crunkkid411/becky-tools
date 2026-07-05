# HANDOFF — The Native Timeline (replacing Becky Review's timeline entirely)

> **Read this before touching `native/becky-timeline/` or `gui/BeckyReviewNative/`.**
> **START AT §12** — Jordan's open field issues (threshold shape, lag + not-ready overlay,
> click-then-Spacebar focus) and §13, the precedence-resolved ledger of ALL timeline feedback.
> It is the single source of truth for what was built, what was ruled out and **why**, the
> mistakes we already made (do not repeat them), and the complete roadmap. The engine bake-off
> is also recorded in the memory `native-nle-engine-bakeoff.md`; this doc is the fuller
> repo-side handoff + roadmap. If they ever disagree, prefer this doc and fix the memory.
>
> **Status as of 2026-07-03:** the native timeline **embeds in-window and renders over the
> WebView2** (the hard part — solved + committed, `49f90e0`). It does NOT yet have Becky
> Review's features ported into it. That port is the roadmap in §10.
>
> **Reviewed + AMENDED 2026-07-04 (Jordan approved):** four gaps were found and folded into §10 —
> edit-state sync-back (edits were losable on save), audio (was absent from the plan entirely),
> an explicit preview-ownership handover (mpv is the bridge, the native compositor takes over at
> multi-track), and keyboard forwarding for the embedded pane. Also settled: waveforms must be
> computed from REAL audio samples — Becky Review's SVG waveforms are inaccurately drawn and are
> NOT to be ported (Jordan, 2026-07-04).
>
> **SHIPPED 2026-07-04 (Phase A complete + the native half of Phase B):** the embedded timeline is
> LIVE and engine-driven. Persistent process (launched at startup, idles hidden, survives toggles),
> live `loadreel` on every model change, gestures emit semantic events the page routes to the SAME
> engine verbs the DOM used (`set_trim`/`reorder`/`reorder_many`; scrub → `seekTimeline` → mpv;
> Ctrl+Z → engine `undo` reverses native edits — the sync-back invariant holds because the ENGINE
> was the model all along). REAL waveforms: per-source min/max pyramid from the actual samples
> (48 kHz, 750 bins/sec finest, absolute scale), cached at `%LOCALAPPDATA%\becky\peaks\*.bpk`.
> Trim handles with snap + Vegas-style ripple-trim ghost, drag-reorder with dropmark, multi-select
> (Ctrl/Shift), Ctrl+wheel + ArrowUp/Down zoom (view echoed back to the page label), adaptive
> ruler, scrollbar, auto-follow. All verified on REAL footage (`E:\TakingBack2007`) via CDP +
> Win32 input: trim `0:08→0:07`, reorder `c1,c2,c3→c2,c3,c1`, two undos restored both, zoom
> `192→288→402.78 px/s` exact. Most Phase-B "controls" needed NO porting — the page keeps owning
> transport/split/speed/threshold (they act on the model; the native view reflects it). Still open:
> §10.A5 audio + the Phase-C preview handover, proxies (A6), and skipping the hidden DOM build at
> scale (the old Phase-2 perf note in app.js).
>
> **SHIPPED 2026-07-04 (round 2 — Jordan's field feedback fixed + verified):**
> - **Waveforms are now WINDOWED + instant.** Decode SEEKS straight to each clip's window
>   (audio-only: `uridecodebin caps="audio/x-raw" expose-all-streams=false` — the old code decoded
>   the whole file's VIDEO too, fighting mpv for the GPU/disk = the "won't play" complaint),
>   BELOW_NORMAL priority, sentinel-filled full-length arrays + per-second coverage, BPK2 cache
>   (BPK1 reads as fully-covered). Verified: clips from minute ~40 of an UNCACHED 632MB livestream
>   render in ~2s; the timeline stays interactive throughout.
> - **Playback never silently stalls.** `ensurePlaybackProxies` (app.js): the play path awaits the
>   next 60s of windowed scrub proxies (cached = instant; missing = a few seconds WITH the busy
>   toast) before building the EDL — the old raw-source EDL stalled 10-60s with no feedback on
>   Jordan's multi-GB livestreams. Warm ground truth (in-page tracing): add_clip 1-7ms,
>   scrub_segment 90ms cached, timeline_edl 14ms, first frame ~40ms after play.
> - **Zoom fixed — including the CONVENTION.** This app zooms on PLAIN wheel anchored to the
>   playhead, Ctrl+wheel pans (the DOM handler, "Jordan's ask") — the first native build had it
>   backwards. Wheel is now caught by a WH_MOUSE_LL hook (cursor-over-pane, focus-independent —
>   verified with zero focus preparation), and the +/- buttons + Up/Down + any `setZoom` caller
>   forward to the native view (`{"op":"zoom","pps":n}`), whose `{ev:"view"}` echo updates the label.
> - **Playback threshold is native.** The reel push carries `thresholdOn/thresholdLevel`; the native
>   pane draws mirrored draggable level lines + dims quiet stretches computed from the REAL
>   absolute-scale peaks, and streams `{ev:"quiet",ranges}` to the page — `quietIntervals()` prefers
>   those, so the skip logic AND trim-silence (rewritten to cut the complement of the SAME intervals)
>   match what the shading shows, in both modes.
> - **Orphan guard:** embedded becky-timeline exits when its stdin closes or the parent HWND dies
>   (a force-killed app used to leak a GPU process forever). Also: the native-mode page skips the
>   hidden DOM's thumb/wave pumps (invisible ffmpeg work), closing part of the old Phase-2 note;
>   the engine bridge was confirmed already-concurrent (goroutine per verb).
>
> **SHIPPED 2026-07-04 (round 3 — ALL of §12 fixed + most of the §13 ledger; every item verified
> with real Win32 input + CDP on E:\TakingBack2007 footage):**
> - **§12.1 threshold = ONE dB bar.** Lane bottom = -50 dB (skips nothing, level 0), top = 0 dB;
>   label shows dB; level stays 0..1 amplitude on the wire. Verified: drag to near-bottom read
>   -45.2 dB / amp 0.0055 and streamed 8 quiet ranges from the real peaks.
> - **§12.3 click-then-Spacebar.** Native emits `{"ev":"pointer"}` on ANY mousedown; the HOST calls
>   `WebView.Focus()` on it and the PAGE blurs whatever field had focus. Verified: focus the search
>   box → one click on a native clip → `document.hasFocus()=true`, field blurred → Space started EDL
>   playback from the clicked spot instantly.
> - **§12.2 lag, structurally:** (a) native-mode `renderTimeline` SKIPS all hidden-DOM building
>   (verified `document.querySelectorAll('.clip').length == 0` with 4 clips in the model — the old
>   571ms/5k-clip per-edit tax is gone); (b) reel pushes DEDUPE by signature (only real changes reach
>   the pane); (c) waveform decode drops 2→1 workers while playing/gesturing; (d) scrub drags ride
>   mpv KEYFRAME seeks (exact seek on release); (e) mid-playback edits DEFER the EDL reload: a SPLIT
>   never reloads (bit-identical stream — verified 's' during playback: clips 3→4, playing stayed
>   true, position continuous, mpv source untouched), an edit AHEAD of the playhead reloads only as
>   playback approaches it (`pendingReloadFrom` in app.js); (f) per-clip "preparing…" overlay
>   (stripes + dim + label) while the windowed proxy (page `ready` flag) or peaks coverage (native
>   secFilled) is missing — caught live on a fresh source, cleared itself after the proxy confirmed.
> - **§13 landed natively:** source-tinted clips (page palette rides the reel push as per-clip
>   `color`), selected = FULLY OPAQUE + white edge / unselected translucent (no gold), trim handles
>   in the source colour; BLACK playhead with white flag head + 2 grip hashmarks; the secondary
>   STOCK bar (black, blinks while auditioning — pushed via the playhead op as `stock`/`flash`);
>   clip-body click while PLAYING moves the STOCK + selects and never interrupts (native emits
>   `clipclick`, the PAGE decides; pause snapped back to the stock exactly); overscroll past the end
>   (maxScroll = dur − 0.15·view; scrollbar range matches — verified 15.1515 for dur 16.64 @192pps);
>   middle-drag pan; native no longer emits NO-OP reorders (dropped-in-place drags used to push junk
>   undo entries); trim zones widened 7→10px (fast hands missed the edge and got reorders).
> - **Ctrl+Z split bug (ledger) DOES NOT REPRODUCE:** engine split is one atomic verb — verified
>   live: split inserted the right half directly after the left, ONE undo restored the exact
>   pre-split clip list. fb7's report predated the atomic verb.
> - **The native/classic toggle is GONE (Jordan's note):** the page boots straight into the native
>   timeline; the DOM timeline exists only as the automatic tlDead degrade (one auto-restart, then
>   fallback). The old separate-window launcher was deleted from the host.
> - **Traps for the next agent:** ImGui's window PADDING offsets the timeline by ~8px — `tlX = p.x`,
>   so screen-x ≠ comp·pps (bit me in coordinate tests); Windows' "scroll inactive windows" delivers
>   wheel to the unfocused pane so the WH_MOUSE_LL hook DOUBLE-applied — embedded now ignores
>   ImGui's `io.MouseWheel` (the hook is the one wheel source); the host's `WebView.Focus()` on
>   pointer events is what makes click-then-Space work — do not remove it.
> - **Still open:** small corner thumbnails on clips (ledger marks it "later"; waveform-first
>   stands); Phase C/D (preview handover to the native compositor + audio + VRAM ring). Pre-existing
>   unrelated: `becky-go/cmd/tts TestRun_DegradesWhenNoModel` fails at HEAD (becky-voice workstream,
>   untouched here).

---

## 1. The mission (Jordan's context — do not lose this)

Jordan is a **professional video editor who scrubs, cuts, and manipulates a LIVE PLAYING
multi-layer composite faster than 99% of humans.** He finds the exact frame-accurate
zero-crossing edit points *by eye, in real time, as the video plays.* **Every "good enough" NLE
falls behind his hands** — that lag is the whole problem. He still uses **Vegas Pro 18** for real
work because nothing else keeps up.

This project is the **"best performance in the world" timeline** — a custom NLE surface designed
around the fastest human editor, where **interaction latency is the product** (not throughput, not
features-on-paper). The bar is: scrub/cut/trim a live composite with **zero perceptible lag.**

Two hard constraints from Jordan, learned the painful way:
- **It must stay AI-first.** Vegas is dead for us *specifically because it is not AI-controllable*
  (single UI thread, no playhead event, frozen .NET Framework). The whole point is that becky/Claude
  can drive the SAME timeline the human drives. An editor a human loves but an agent can't touch is a
  failure here.
- **"It has to replace the current timeline. Period."** A separate window is a **hard boundary — a
  non-starter.** ("first of all, it's a separate fucking window. hard boundary, fuck that.") The
  native timeline must live *inside* the app, in the same rectangle the old timeline occupied, with the
  same integrations and user-defined customizations established in Becky Review's timeline

Interaction requirements: **A/B roll + PiP/crop**, split/delete/trim/scrub on a live composite.
Transitions don't matter. Captions/filters/grade/audio-treatment stay CLI becky-tools (he pre-treats
footage so the timeline stays barebones and fast).

Working style notes that matter:
- **He is not a developer** and has impaired vision (sighted, no screen reader — keep colored UIs,
  plain language, never make him run CLI commands or "test/feel" things; verify autonomously).
- **"just build the damn thing."** He does not want to be a lab rat clicking through test toys.
  Build the real app, verify it yourself with screenshots + Win32-driven input, and keep iterating —
  **do not stop half-done and hand him a checklist.** ("you keep telling me you'll keep iterating,
  and then you just stop, what the fuck.")

---

## 2. What the two apps are (this is a COPY, and we are replacing the timeline)

There are **two** review apps in the repo:

| App | Path | Role |
|-----|------|------|
| **Becky Review** | `gui/BeckyReview/` | **What Jordan actually uses.** Do not break it. |
| **Becky Review Native** | `gui/BeckyReviewNative/` | **A deliberate throwaway DUPLICATE** of Becky Review, created *specifically so an agent can break it and iterate without asking permission.* Iterate freely here. |

Both are WPF (.NET 8) shells hosting a **WebView2** UI (`ui/index.html` + `ui/app.js`) plus a
**native mpv** video pane. The timeline is currently **DOM (HTML/JS) inside the WebView2**, and that
DOM timeline is the thing that **chokes on complex/large projects** (thousands of clip nodes) — the
reason this whole effort exists.

**THE GOAL (do not reinterpret):** **replace that WebView2 DOM timeline ENTIRELY with the native
GPU timeline (`becky-timeline`), in-window.** *Everything* Becky Review's timeline does — every
button, every integration — must end up driven by / living in the native timeline. Becky Review
(the original) is the "good-enough holdover" while this is built; the native timeline is the real
destination.

---

## 3. What is BUILT + WORKING right now (all committed to master, all screenshot-verified)

### 3a. `native/becky-timeline/` — the native NLE editor
A single-file C++ app (`main.cpp`, ImGui + D3D11 + GStreamer). Built with **MSVC** (not WSL/mingw),
reusing the vendored ImGui + nlohmann under `native/timeline-bench/third_party/`.

Working + verified (screenshot + real Win32 mouse/keyboard):
- **2 independent GStreamer d3d11 (NVDEC) decoders**, each seeked to the playhead's source-time,
  composited (track A full-frame + track B as a top-right PiP). **~100–130 fps** at proxy res.
- **Custom ImGui NLE timeline** (ruler + per-track clip blocks + gold playhead), drag-to-scrub.
- **Edit ops** (one shared `applyOp` path for human AND AI): **split (S), delete+ripple (Del),
  trim in/out (I/O), scrub (drag), play (Space), [G]roup toggle** (split/delete/trim hit BOTH
  tracks in sync so layers stay aligned).
- **AI-in-the-loop:** an NDJSON channel on **stdin** (`{"op":"split","t":30}`) runs the exact same
  `applyOp`; state is emitted on **stdout** after each edit. Human keys and AI ops are literally one
  code path (the "dual operability" requirement).
- **`--reel reel.json`** loader (multi-source): `{sourceA, sourceB, trackA:[{in,out,source?}],
  trackB:[...]}`; per-clip `source` → clips from **different files** on one track (layerLoad rebuilds
  a decoder only when the source actually changes).
- **`--wid <hwnd>`** — renders as a **WS_CHILD** of a given HWND so it can be embedded (see §6).
- Full-res: 1080p all-intra proxies work (~47 fps; the ceiling is the per-frame flush-seek+preroll,
  NOT the composite — a GPU compositor would NOT help; see §5).

### 3b. `gui/BeckyReviewNative/` — the embed
The **`native` button** in the timeline toolbar now **replaces the DOM timeline with
`becky-timeline` rendered INSIDE the window** (over `#timelineHole`), driven by the existing
`timelineRect` / `timelineReel` / `timelineMode` page↔host messages. Verified end-to-end via CDP:
toggling native embeds becky-timeline showing a real 3-clip reel (video + ruler + clips) at 130+ fps,
composited over the WebView2. **This is the breakthrough — see §6 for the two fixes that made it work.**

Files touched: `native/becky-timeline/main.cpp`, `gui/BeckyReviewNative/MainWindow.Timeline.cs`
(the embed host), `MainWindow.xaml.cs` (message routing + `ResolveNativeTimelineExe`),
`ui/app.js` (`setNativeTL` + `pushTimelineReel` sending `source/in/out`), `ui/index.html` (button).
The `TimelineHost`/`TimelineHostElement` WindowsFormsHost was already scaffolded in `MainWindow.xaml`.

**NOTE FROM JORDAN (7-4-2026)** I don't want that native button. If it's helpful to you while building, that's okay. But the final Becky-Review-Native gui should have ONLY the Native timeline with no option for the old one
---

## 4. Commits (master)
```
49f90e0 feat(review-native): native timeline EMBEDS in-window, replacing the DOM timeline  <-- the win
6d1de03 chore(review-native): commit the full Becky Review Native project (was untracked)
2f5a929 feat(review-native): "native" button ... (SUPERSEDED by 49f90e0's embed — old separate-window path is dead code)
2cac901 polish(nle): pre-warm first-clip sources at startup
0e625b8 feat(nle): multi-source reels
022f6b1 feat(nle): becky-timeline --reel loader
699e146 perf(nle): parallel layer decode + full-res proxies
```

---

## 5. Engines & approaches RULED OUT — with data (DO NOT re-litigate)

These were each measured/tried this session or the prior one. Re-testing them is wasted time.

| Ruled out | Why (evidence) |
|-----------|----------------|
| **Vegas Pro (drive it)** | Single-UI-thread affinity, **no playhead event**, frozen .NET Framework 4.x. **Not AI-controllable** — Jordan's 4 past bridges (C#/JS/Python/custom) all failed for this. It's his manual tool, not our platform. |
| **Shotcut / MLT / kdenlive** | MLT's own FAQ: readback-to-system-RAM, VA-API-only, **no NVDEC on Windows** → CPU-bound scrub ceiling. This is *why* the forked Shotcut scrubbed WORSE than the web timeline. Mis-picked once for "fork ease," not quality. Never revisit. |
| **GStreamer GES (d3d11)** | Loads on the 3070, but 2-layer scrub = **~1 fps (~800 ms/seek)**; `keyunit==accurate` proves it's per-seek **engine** overhead (rebuilds its composition every seek), NOT decode → **proxies can't fix it.** Built for render/export, not scrub. |
| **2× libmpv (one per layer)** | Collapses to **~20 fps** on 2 layers even warm (1-layer = 2208 fps warm). Two heavy players contend. libmpv is a single-source *player*, not a compositor. (mpv is still the RIGHT choice for the single preview pane — see §6.) |
| **A shared GStreamer `d3d11compositor` aggregator** | Can't forward a flush-seek to both branches independently — the same shared-coordination trap that killed GES. **Independent decoders sidestep it.** |
| **GPU compositor to speed full-res** | Measured: proxy→HD compose went 10→26 ms, but the readback delta for 16 MB is ~1 ms. The cost is the **per-frame flush-seek + preroll roundtrip**, not the composite. A GPU compositor would NOT move the number. The real levers are a **VRAM frame-cache + non-flushing seeks** (§10). |
| **OpenGL / WGL for the embedded window** | An OpenGL child window **does NOT composite over the WebView2** in WPF airspace (verified: magenta-clear test showed nothing; PrintWindow captured the WebView2, not our GL). See §6. |
| **`DXGI_SWAP_EFFECT_DISCARD` (BitBlt) swapchain** | BitBlt writes to the window DC and is **never DWM-composited** over the WebView2 sibling. This is exactly the z-order wall the *old GDI `TimelineControl`* hit and got abandoned for. See §6. |

**The architecture that WON** (own the hot path, nothing between input and pixels):
- **N independent NVDEC/d3d11 decoders**, one per visible layer, each seeked independently.
- **Own composite** (currently a cheap CPU blend of the PiP region; upgrade to a D3D11 shader).
- **All-intra scrub proxies** (becky **ScrubProxy**) so every seek is one light decode, no GOP walk.
- **Fire both seeks, THEN pull both** (parallel decode) — this technique hit **2325 fps** in the bench.
- **becky's Go `editmodel`/NDJSON** as the shared brain so AI + hands drive the same verbs.

---

## 6. The WebView2 airspace saga + the TWO fixes (the most important section)

**Problem:** the native timeline is a child window that must render ON TOP of the WebView2 (which
draws its own surface over everything). The **mpv video pane already does this** (it's a child HWND
in a WindowsFormsHost, `--wid`), so it's possible. The old GDI `TimelineControl` attempt failed here
("z-order blocker — pane behind Web UI despite SetWindowPos HWND_TOP") and the whole native-timeline
idea stalled for weeks. Here is exactly why it failed and what fixed it — **do not repeat the dead ends:**

1. **It is NOT a z-order problem.** We proved (via `EnumChildWindows`) the embedded `beckytl` window
   is **topmost** — listed above the WebView2's `Chrome_RenderWidgetHostHWND` — and `vis=1`, correctly
   sized. So SetWindowPos/HWND_TOP tricks are a red herring; don't waste time there.
2. **It is NOT a launch-timing problem.** We tried launching early (like mpv, at `Window_Loaded`) vs
   late (on toggle). No difference. Don't chase timing.
3. **FIX #1 — render with D3D11, not OpenGL.** An OpenGL/WGL child window does not composite into the
   DWM surface over the WebView2. mpv works because it uses D3D. We rewrote becky-timeline's display
   from ImGui-OpenGL3/WGL to **ImGui-DX11 + a D3D11 swapchain.** (Standalone it rendered fine either
   way; embedded, only D3D11 has a chance.)
4. **FIX #2 (the real root cause) — use a FLIP-model swapchain.** With `DXGI_SWAP_EFFECT_DISCARD`
   (BitBlt) it STILL showed nothing embedded — because BitBlt writes to the window DC and is not
   DWM-composited over the sibling WebView2. Switching to **`DXGI_SWAP_EFFECT_FLIP_DISCARD`
   (BufferCount=2)** — a flip-model swapchain, which DWM composites — made it appear instantly. **This
   one line is what the old GDI attempt could never beat.** mpv composites for the same reason.

**Diagnostic that cracked it:** `PrintWindow(beckytl, PW_RENDERFULLCONTENT)` returned the *WebView2's*
pixels, proving our swapchain wasn't reaching the DWM compositor at all → pointed straight at the
swap-effect. Keep this trick for future airspace debugging.

**So the embed recipe (proven, reusable):** a separate process rendering with **D3D11 + a
flip-model swapchain**, created on a **WS_CHILD** window whose parent is a **WinForms Panel** hosted
in a **WPF `WindowsFormsHost`** (exactly how mpv's `VideoHost`/`_videoPanel` works). Positioned over a
page "hole" via a `{t:"...Rect", x,y,w,h}` message.

---

## 7. Architecture / file map

```
native/
  becky-timeline/            <- THE native editor
    main.cpp                 <- everything: GStreamer decode, compose, tracks, applyOp,
                                stdin/stdout NDJSON, D3D11 display, --wid embed, --reel loader
    Open Becky Timeline.bat  <- standalone launcher (sets GStreamer env, prefers proxy*_hd.mp4)
    _build.bat               <- GITIGNORED; regenerated by PowerShell each build (see §8)
    .gitignore               <- ignores *.exe/obj/pdb + test reels
  ges-bench/                 <- the engine bake-off harnesses (ges_scrub, gst_scrub_indep,
                                gst_compose, gst_scrubwin) + proxyA/B.mp4 (+ proxy*_hd.mp4, gitignored)
  timeline-bench/            <- ImGui/ImSequencer + libmpv benches; third_party/{imgui,nlohmann}

gui/BeckyReviewNative/       <- the app (throwaway copy of gui/BeckyReview)
  MainWindow.xaml            <- has WebView2 + VideoHost (mpv) + TimelineHost (native timeline) panes
  MainWindow.xaml.cs         <- WebView init, OnWebMessage router, mpv wiring, ResolveNativeTimelineExe
  MainWindow.Timeline.cs     <- the EMBED: hosts becky-timeline --wid in TimelineHostElement,
                                driven by timelineRect/timelineReel/timelineMode
  MpvPlayer.cs               <- the PROVEN airspace pattern to copy (--wid, JSON IPC over a pipe)
  BeckyEngine.cs             <- talks to becky-review-engine.exe (folder index + transcript search)
  ui/index.html, ui/app.js   <- the WebView2 UI (search, transport, timeline DOM, ask-becky chat)
  TimelineControl.cs         <- DEAD (the old GDI attempt). Left in place; delete when convenient.
```

**becky's Go brain (already built, reuse — don't reinvent):** `internal/editmodel` (copy-on-write
shared edit state), `internal/edittools` (24-verb default-deny allowlist), `internal/ctlagent`
(multi-step agent loop), `cmd/becky-edit` (NDJSON stdio bridge). The native timeline's stdin ops are
the same idea; converge them.

---

## 8. Build + run + verify (exact commands)

**becky-timeline** (regenerate `_build.bat` then compile — it links D3D11 + the ImGui-DX11 backend):
```
# _build.bat compiles main.cpp + imgui{.cpp,_draw,_tables,_widgets} + imgui_impl_win32 + imgui_impl_dx11,
# includes third_party/{imgui,imgui/backends,nlohmann} + GStreamer, links:
#   gstreamer-1.0 gstapp-1.0 gstvideo-1.0 gobject-2.0 glib-2.0 d3d11.lib dxgi.lib gdi32 user32
# vcvars: "C:\Program Files (x86)\Microsoft Visual Studio\2022\BuildTools\VC\Auxiliary\Build\vcvars64.bat"
# (the full generator is in the session history; regenerate it, don't hand-edit the gitignored file)
```
Runtime env (both standalone and when BRN launches it):
```
PATH = <G>\bin ; ...            where G = C:\Program Files\gstreamer\1.0\msvc_x86_64
GST_PLUGIN_SYSTEM_PATH_1_0 = <G>\lib\gstreamer-1.0
GST_PLUGIN_FEATURE_RANK = d3d11h264dec:512,d3d11h265dec:512     # FORCE GPU decode
# In PowerShell: $env:GST_PLUGIN_PATH=$null  (do NOT Remove-Item — a safety hook blocks it)
```
**Becky Review Native:** `dotnet build gui/BeckyReviewNative/BeckyReviewNative.csproj -c Release`
(guard your grep: `-match 'error'` false-positives on "0 **Error**(s)" — match `Build succeeded`).

**Verify autonomously (never ask Jordan to test):** BRN honors `BECKY_REVIEW_CDP_PORT` → attach to
the WebView2 via a `System.Net.WebSockets.ClientWebSocket` to `http://localhost:<port>/json`, and
`Runtime.evaluate` `window.chrome.webview.postMessage({...})` to drive it. Screenshot the window with
`Graphics.CopyFromScreen`; enumerate/inspect child windows with `EnumChildWindows`/`PrintWindow`.
Drive real input with `SetCursorPos`/`mouse_event`/`SendKeys`. This is how the whole embed was proven.

---

## 9. Gotchas / traps learned this session (don't step on these again)

- **`gst_parse_launch` treats `\` as an escape** → Windows paths must use forward slashes (`fwdslash()`).
- **fakesink `async=false` suppresses ASYNC_DONE** → seek waits hang. Use `sync=false` only.
- **Two D3D11 devices in one process is fine** (GStreamer's decoder device + our display device coexist).
- **Playback time must accumulate as `double`** (`curSec += dt`), not an int frame counter (truncates to 0).
- **`GetForegroundWindow()==hwnd` is false for an embedded child** → gate global-key handling on
  `!g_parentWid` so typing in BRN's search box can't leak into the timeline (`GetAsyncKeyState` is global).
- **The fact-forcing gate** intercepts every Write/Edit: state who calls the file, no existing
  equivalent (Glob), data fields, and the user's instruction verbatim — the FIRST attempt always errors;
  re-issue the identical call. Also `git commit` to `master` is blocked → branch → `git merge --ff-only`
  → push.
- **`.bat` launchers must be ASCII-only + end with `pause`** (PowerShell 5.1 parses them as ANSI).
- **Verify on real input, not a demo fixture** — a becky GUI is "done" only when it works in Jordan's
  hands on his real data; "it compiles" is never done.

---

## 10. THE ROADMAP — port ALL of Becky Review into the native timeline

The embed shell works; now the native timeline has to *become* the timeline, absorbing every feature
of Becky Review's DOM timeline + its integrations. Ordered by dependency. Each item ends at a
screenshot-verified working state, not "compiles."

### Phase A — make the embedded timeline a live, two-way surface (unblocks everything)
1. **Persistent process + live reel sync.** Today the reel is passed once (on toggle) and the host
   holds **no stdio pipes at all** to the embedded process (verified 2026-07-04: `EnsureEmbeddedTimeline`
   redirects nothing and early-returns while running, so the page's continuous reel re-pushes are
   dropped). Launch becky-timeline **ONCE, early (mpv pattern), redirect stdin+stdout, keep it
   running**, and push reel changes via a new stdin **`{"op":"loadreel","reel":{...}}`** op (tolerate
   an empty reel). The page already re-pushes on every render/scroll — only the host→stdin leg is
   missing. When the native pane is toggled off, **idle** the process (skip render/decode; don't kill).
2. **EDIT-STATE SYNC BACK — the missing invariant; do this before anything else builds on edits.**
   While native mode is on, **becky-timeline's edit state is the MASTER reel.** It already emits state
   on stdout after every edit; the host must consume it and mirror it into the page's clip model
   (guarding against echo — a mirrored update must NOT re-push the reel). Save/Load/Export/EDL/hits
   all read the page/host reel: without this leg, a split made natively is **silently lost on save.**
3. **Playhead sync both ways; mpv is the preview — but only as a BRIDGE (decision 2026-07-04).**
   becky-timeline emits the playhead as `{t, src, srcT}` (it owns the reel→source-time map); the host
   drives the existing mpv pane from it (atomic `loadfile --start=<srcT>` — never load-then-seek, see
   the seek-race memory). App-side seeks (search click) feed the playhead INTO the timeline. While
   embedded under mpv, becky-timeline runs **timeline-UI-only (no video decode)** — no double-decode,
   no second preview. The handover point is fixed: **when multi-track/PiP lands (Phase C), the native
   compositor BECOMES the preview**, and the forensic overlay + audio move native with it. Don't build
   deeper on mpv than this seek coupling.
4. **Keyboard forwarding.** All hotkeys are deliberately dead when embedded (the `!g_parentWid` gate;
   the host even re-focuses the WebView on MouseEnter). The fix is NOT focus games: the page — which
   really has focus — captures keys in native mode and forwards them as the **same NDJSON ops the AI
   uses** (`{"op":"split"}`…). Humans become another NDJSON client; dual-operability taken to its
   conclusion.
5. **Audio — was missing from this roadmap entirely.** becky-timeline decodes video only (verified:
   zero audio code). The bridge state is fine — mpv plays the sound. But the end-state preview (native
   compositor, PiP) has **no sound story**, and Jordan edits by ear: plan a GStreamer audio branch in
   becky-timeline for play-through (it also becomes the master clock for A/V sync) as part of the
   Phase-C preview handover. Until then mpv stays the only audible path — one more reason (3) keeps it.
6. **Scrub proxies — deferred by the timeline-UI-only decision in (3).** While mpv is the preview the
   embedded timeline decodes no video, so proxies now matter only for mpv's own scrub feel (unchanged
   from today's app) and for the Phase-C/D compositor takeover, where all-intra ScrubProxy per source
   (generated on demand) is still the difference between "works" and "fast."

### Phase B — port the timeline's own controls into the native surface
**Sizing warning (2026-07-04): this phase is the BULK of the whole effort.** It reads as a tidy
bullet list but drag-reorder, snap-trimming, multi-select and waveforms are each real custom-widget
work; schedule them as separate verified increments, not one item.

becky-timeline must own these (they exist as DOM buttons today — see `ui/index.html` toolbar):
- **Clip model parity:** click-to-seek, **drag-reorder**, **resize/trim handles with edge-snap**,
  extend-selected-clip-by-one-frame (`[◄` / `►]`), split (✂), selection + multi-select.
- **Transport:** play/pause, **frame-step (Left/Right)**, **2× speed toggle**, **playback threshold**
  (skip quiet parts during review — evidence NOT cut), **trim-silence** (a real edit).
- **Zoom** (Ctrl+scroll, +/- ), ruler scrub, playhead.
- **Undo/redo** (Ctrl+Z / Ctrl+Shift+Z) — implement as a snapshot stack in becky-timeline's OWN state
  (clips are tiny structs; snapshotting is cheap). Do NOT wire the Go editmodel into the hot path for
  this — converge with it later at the verb-schema level, not in-process.
- **ACCURATE waveforms** on clips, computed from the **real audio samples** (min/max peak pyramid,
  zoom-adaptive, cached per source). **Do NOT port Becky Review's SVG waveforms — Jordan confirmed
  2026-07-04 they are inaccurately drawn and not good enough.** He finds zero-crossing cut points by
  eye, so the drawing must be sample-true at high zoom: becky-timeline decodes PCM itself (GStreamer
  audio branch) rather than reusing the engine's coarse peaks.
- **Screenshot** the preview (`Screenshot_0001.png`).

### Phase C — port the surrounding integrations (the "all functionality" part)
Everything Becky Review wraps the timeline with, now feeding the native timeline:
- **Folder → transcript search → clip.** The left panel: point at a folder, `becky-review-engine`
  indexes `.srt` transcripts; **single-click a quote = preview seeks+plays; double-click = clip
  appended to the timeline.** "smart" toggle = qmd hybrid search. Per-file green **"+"** = transcribe
  (Parakeet ASR). This is the core forensic workflow and must drive the native timeline's reel.
- **Forensic overlay** (lower-third: filename + LIVE original timecode + date/link) drawn on the
  preview — `overlay` / `name` toggles. (mpv draws it via ASS osd today; keep it on the preview.)
- **Save / Load / Export** the reel + **CMX3600 `.edl`** (AA/V channel so Vegas gets audio) +
  becky-otio interchange (`.otio/.fcpxml/.kdenlive`). **render selection** / **export** the compilation.
- **Forensic hits reel** — `becky-hits` writes a `{srt,t,q}` hit-list → `BECKY_REVIEW_REEL` preloads
  it onto the timeline (`Open Forensic Hits.bat`). Must preload into the NATIVE timeline.
- **Human-review Q&A panel** — the "?" hit field → Q&A cards → answers in `_forensic_answers.json`
  (an agent routes them to the wiki). NEVER bury questions in markdown.
- **"ask becky" chat** (right panel) — local **Gemma-4 E4B** default / "use Claude" → Claude Code;
  suggestion chips ("find every threat to the host family", "compile every time he offered money for
  the cat", "turn the lower-third on"). This is the **AI-first seam** — it should issue the same
  `editmodel`/NDJSON verbs the native timeline consumes, so **the chat edits the native timeline.**
- **A/B roll + PiP/crop** as first-class (becky-timeline already has 2 tracks + PiP; expose crop/pos/scale).

### Phase D — world-class performance polish (the "fastest editor" bar)
- **VRAM frame-ring cache** per layer → re-scrubbed frames are instant; pre-decode ahead during play.
  (This is THE perf piece for full-res-instant; the current ceiling is per-seek flush+preroll.)
- **Non-flushing / hint seeks** where frame-accuracy allows; keep decoders hot.
- **Pre-warm a decoder per source** to kill the ~130 ms cross-source-cut reload hitch.
- **Own D3D11 composite shader** (replace the CPU PiP blend) for N layers with crop/scale/pos/alpha —
  only once the seek roundtrip stops being the bottleneck (measure first; see §5).

### End state
Becky Review Native opens; the timeline you see + drive is **becky-timeline** (native D3D11), in-window;
search adds clips to it, the overlay + save/load/export/hits/Q&A/chat all operate on it, becky/Claude
drive it through the same verbs, and it scrubs a live composite with zero lag. At that point the
WebView2 DOM timeline is deleted and Becky Review Native becomes the new Becky Review.

---

## 11. Reference

**Reel JSON (what the host writes / becky-timeline reads):**
```json
{ "sourceA":"a.mp4", "sourceB":"b.mp4",
  "trackA":[{"in":5,"out":25,"source":"a.mp4"}], "trackB":[{"in":0,"out":60}] }
```
`source` per clip is optional (falls back to sourceA/sourceB) and enables multi-source. Omit `sourceB`
and `trackB` for a single-track reel (no PiP).

**becky-timeline stdin ops (NDJSON, one per line):** `{"op":"seek|split|delete|trim_in|trim_out|
play|group","t":<sec>,"on":<bool>}`. It emits `{t,dur,playing,group,trackA:[{in,out,start}],trackB:[]}`
on stdout after each edit. (Add `loadreel` in Phase A.)

**Host↔page messages (WebView2):** page→host `{t:"timelineRect",x,y,w,h}` (position the pane over
`tlBodyEl`), `{t:"timelineReel",clips:[{source,in,out}]}` (the current reel), `{t:"timelineMode",on}`
(toggle). `setNativeTL(true)` in `app.js` hides `.tlinner` and sends these; the `native` button calls it.

**CLAUDE.md doc map:** add a pointer to this file in §5 so the root stays the index of truth.

---

## 12. OPEN FIELD ISSUES (2026-07-04 evening) — THE NEXT AGENT STARTS HERE

Jordan used the round-2 build and reported three problems. Fix these FIRST, surgically
(his standing instruction: "half the time when I ask for changes, something else breaks —
take a surgical approach").

> **STATUS 2026-07-04 (round 3): ALL THREE FIXED + VERIFIED on real footage — see the
> round-3 SHIPPED block at the top of this file for the evidence.**

### 12.1 Threshold bar: ONE bar, dB scale — not two mirrored lines
Current native implementation draws TWO mirrored lines around the waveform midline with a
LINEAR amplitude mapping. Jordan: **"The playback threshold should produce only one bar, not 2.
At its lowest it should be silencing nothing (approximately -50 dB), at max it should be
silencing everything (0+ dB)."**
- ONE horizontal bar across the lane (matches `playback-threshold.JPG` + every DAW).
- Bar at the BOTTOM of the lane = threshold -50 dB (skips nothing); bar at the TOP = 0 dB
  (skips everything). Map bar height -> dB: `dB = -50 + 50 * (laneBottom - barY) / laneH`,
  quiet when `20*log10(max|amp|) < dB` (amp = int8 peaks /127; -50 dB is amp ~0.0032).
- Keep: dimmed quiet stretches, drag to adjust, `{ev:"threshold"}` / `{ev:"quiet"}` events.
  The LEVEL sent to the page should stay a 0..1 amplitude (`10^(dB/20)`) so the page's
  engine-peaks fallback + trim-silence keep working unchanged.

### 12.2 "It simply cannot keep up with me" — native timeline lag + INVISIBLE not-ready state
Jordan: extreme lag under his real editing pace; **"If new clips on the timeline need a moment
before I can work with them, we need some type of blatantly obvious overlay because currently
it silently lags like hell."**
- REQUIRED UX: a clip whose windowed proxy and/or peaks are NOT ready yet must be visibly
  marked on the native timeline (e.g. diagonal-striped dim + "preparing..." text on the clip),
  clearing the moment it's ready. The busy toast is not enough. becky-timeline already knows
  peaks-readiness per window (secFilled); the host/page knows proxyReady — feed that into the
  reel push (per-clip `ready:false`) or emit it natively.
- LAG HYPOTHESES to profile IN HIS HANDS (per-verb tracer, 12.5): (a) every edit round-trips
  page->engine->page->full `loadreel` rebuild — loadReelLive clears + rebuilds all tracks and
  re-posts peak jobs each push (job de-dup exists via secFilled, but the push itself is
  per-render); (b) scrub emits at 60/s each triggering `seekTimeline`->mpv seek on raw
  long-GOP; (c) waveform decode jobs contending with mpv on the E:\ HDD during interaction
  (cap is 2; consider 1 while playing); (d) the hidden DOM still re-renders per edit
  (the old Phase-2 note — skip building clip NODES entirely when native is on).
- MEASUREMENT: use the in-page tracer (12.5) — do NOT trust repeated-CDP-poll timings.

### 12.3 Click-then-Spacebar (focus) — most-used command, currently broken sometimes
Jordan: **"if a user clicks the clip or the timeline ruler, then it should be selected, which
means all the keyboard shortcuts for the timeline should affect the timeline instantly.
'click, then spacebar' is one of my most used commands"** — today he must press the toolbar
play button before Space works.
- Root cause: the native pane deliberately refuses focus (WM_MOUSEACTIVATE -> MA_NOACTIVATE)
  so clicks leave keyboard focus WHEREVER IT WAS — a search box, an answer box, or a WPF
  element that isn't the WebView. The page's global keydown then never fires (or
  `typingInField()` eats it).
- Fix direction (small): every native gesture already reaches the page as a `tlEvent`
  (scrub/select). On receiving one, the page should `document.activeElement.blur()`; AND the
  HOST should call `WebView.Focus()` whenever it relays a tlEvent (or on a new
  `{"ev":"pointer"}` emitted on any native mousedown). That makes ANY click on the native
  timeline restore the page's keyboard instantly — same class as the fb6 "clicking the
  timeline must return focus from the search box" fix.

### 12.4 What is ALREADY fixed + verified this round (do not redo)
Windowed instant waveforms; play awaits windowed proxies (no more silent raw stall); wheel
zoom convention (plain wheel = zoom AT PLAYHEAD, Ctrl+wheel = pan) via WH_MOUSE_LL hook;
+/-/Up/Down zoom forwarding; native threshold shading/skip/trim-silence unified on real peaks
(shape needs 12.1); orphan-process guard; typed-guarded NDJSON ops.

### 12.5 Verification harness (reuse, don't reinvent)
- Launch with `BECKY_REVIEW_CDP_PORT=9333`; drive via a WebSocket `Runtime.evaluate` helper
  (the session scratchpad had `cdp.ps1`); screenshot via CopyFromScreen; real input via
  SetCursorPos/mouse_event/keybd_event.
- In-page per-verb tracer (paste via CDP): monkey-patch `window.chrome.webview.postMessage`
  to record `{verb,id,t0}` for `t:'call'` and log reply latency + first `t:'time'` — the ONLY
  trustworthy latency numbers. CDP-poll wall-clock lies (~8.5s constant artifact), and a
  file-row click auto-plays the preview so a later play click may PAUSE (state.playing true).

---

## 13. THE TIMELINE REQUIREMENTS LEDGER (feedback 1->7, precedence-resolved)

Jordan's standing order: consolidate every `## Timeline` requirement from
`becky-review-user-feedback{,2,3,4}.md`, `becky-review-user-lag.md`,
`becky-review-gui-user-feedback{5,6,7}.md` — **later files override earlier ones** (fb7 is
the settled truth after iteration). The NATIVE timeline must satisfy this ledger to replace
the DOM one. Status: [D]=done in DOM timeline, [N]=done in native, [ ]=open in native.

**Zoom / navigation**
- [N] Plain mouse wheel = ZOOM, anchored to the PLAYHEAD (fb6 supersedes fb1's zoom-at-mouse).
- [N] Ctrl+wheel = pan sideways. [N] Middle-click+drag = pan (fb3) — native, verified exact.
- [N] Up arrow = zoom in, Down = zoom out (when the timeline owns the keys).
- [N] Zooming OUT shows EMPTY SPACE past the last clip: maxScroll = dur − 0.15·view (~85% of a
  screen past the end), scrollbar range matches. Verified: scroll 15.1515 for dur 16.64 @192pps.
- [D] Ruler drag scrubs; ruler click moves playhead — and must NOT change the selection (fb7).
- [D] Ctrl+Left/Right walk clip boundaries ACROSS THE WHOLE TIMELINE — verified live in round 3
  (walked 15.12 → 10.8 → 6.9 → 0 across all clips; page-side seekClipEdge holds).

**Playhead + secondary stock (fb7 = canon)**
- [N] TWO black bars: the playhead (white flag head + 2 grip hashmarks, per `playhead.JPG`) and
  the secondary STOCK, both drawn natively now (round 3). Stock rides the playhead op as
  `stock`/`flash`; it blinks while diverged; click-in-clip during playback moves the STOCK
  (native emits `clipclick`, the page decides), selects the clip, never interrupts playback;
  pause returns to the stock (verified to the frame); split during playback applies at the
  live playhead / stock exactly as the DOM did (page logic unchanged); Enter commits.
- [D] After a ripple delete, the playhead keeps its position relative to the underlying clip
  (playback uninterrupted); deleting must not move the VIEW.

**Selection + editing**
- [N] Ctrl+click multi-select; Shift+click range; multi-select drag moves the group.
- [N] Selected clip style: NO gold — the selected clip is FULLY OPAQUE in its source tint with
  a white edge (matches the DOM's clipColor/clipBorder exactly); unselected = translucent.
- [N] Trim handles on clip edges, coloured in the clip's SOURCE colour (fb4); zones widened
  7→10px so fast edge-grabs stop landing as reorders.
- [D] Split selects the clip AFTER the playhead; NO toasts for any timeline edit (fb5 killed
  "Removed 1 clip"/"Reordered"/"Undo"/"Redo"/"split" popups).
- [N] Esc + Delete both delete; S splits; I/O trim. fb7 removes the prev/next-frame BUTTONS
  (keep the arrow-key shortcuts); the extend-1-frame buttons stay.
- [N] Ctrl+Z ordering bug (fb7): DOES NOT REPRODUCE (round 3, live): the engine's atomic split
  verb inserts the right half directly after the left and ONE undo restores the exact pre-split
  list. fb7's report predated the atomic verb. Also fixed adjacent: the native pane no longer
  emits NO-OP reorders (a dropped-in-place drag used to push a junk undo entry).
- [N] Trim/cut flash: native waveforms come from the per-source peaks pyramid which persists
  across every loadreel (never cleared), and the trim ghost redraws from the same data — no
  blank-then-repaint observed through round-3 trims/splits/reorders on real footage.

**Clip appearance**
- [N] Color-code clips BY SOURCE VIDEO (fb7 palette #14FF39, #00AEEF, #DC143C, …): the page's
  session-persistent sourceColorMap rides the reel push as per-clip `color`; the native pane
  tints fill/border/handles with it. Verified: green/blue/crimson for three sources.
- [N] REAL waveforms inside clips (sample-true, absolute scale — never the SVG peaks).
- [ ] Thumbnails: small fixed-size icon at the clip's left edge, positioned so cut points
  stay visible; clips ~2x taller so the WAVEFORM is the dominant visual (fb7). Native has no
  thumbnails yet (waveform-first is correct; add the small corner thumb later).

**Playback aids**
- [N] 2x speed button + Shift+Space; screenshot button; playback threshold (ONE dB bar, 12.1).
- [D] Threshold skips quiet parts seamlessly during playback; evidence never cut (fb7).
- [N] BLATANTLY OBVIOUS per-clip "preparing" overlay (stripes + dim + label) while proxy/peaks
  build (12.2) — verified appearing on a fresh source and self-clearing.

**Integrations (work through the model — verified this round)**
- [D] Right-click menus (clip + left panel): Open in File Browser (select the file), Copy
  File Name (the VIDEO's full name), open transcript at the clip's timecode (fb4).
- [D] Drag footage onto the timeline from ANY folder (fb6+fb7 BOTH ask — RE-VERIFY it works);
  in-app drag from the left panel.
- [D] Export naming schema; EDL with audio (AA/V) for Vegas; thumbnails cached under
  render/timeline_thumbnails (fb7); render-selection button greyed (never removed) when
  nothing is selected.

**The bar (fb7 header, verbatim):** "hundreds of micro-edits will be made during playback each
day. This needs to be efficient and instant. Lag on the timeline can turn a 2 hour human
review session into 4 hours." Interaction latency IS the product (see §1).
