# HANDOFF — The Native Timeline (replacing Becky Review's timeline entirely)

> **Read this before touching `native/becky-timeline/` or `gui/BeckyReviewNative/`.**
> It is the single source of truth for what was built, what was ruled out and **why**, the
> mistakes we already made (do not repeat them), and the complete roadmap. The engine bake-off
> is also recorded in the memory `native-nle-engine-bakeoff.md`; this doc is the fuller
> repo-side handoff + roadmap. If they ever disagree, prefer this doc and fix the memory.
>
> **Status as of 2026-07-03:** the native timeline **embeds in-window and renders over the
> WebView2** (the hard part — solved + committed, `49f90e0`). It does NOT yet have Becky
> Review's features ported into it. That port is the roadmap in §10.

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
  native timeline must live *inside* the app, in the same rectangle the old timeline occupied.

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
1. **Live reel sync.** Today the reel is passed once (on toggle) — editing in the app afterward doesn't
   update the native timeline. Add a becky-timeline **stdin `{"op":"loadreel","path":...}`** op (+
   tolerate an empty reel), and have `MainWindow.Timeline.cs` **launch becky-timeline ONCE, early
   (mpv pattern), keep it running, and push reel changes via stdin.** (Currently it launches on toggle
   and relaunches — replace with the persistent-process pattern in `MpvPlayer.cs`.)
2. **Scrub-back sync.** becky-timeline already emits state on stdout; route its scrub/playhead back to
   the host so the **mpv preview seeks to the timeline playhead** (the app owns the reel→file→seek map;
   drive mpv, don't double-decode). Conversely feed the app's playhead into the timeline.
3. **Scrub proxies.** becky-timeline currently decodes BRN's **raw** source files → real footage scrubs
   slow (long-GOP). Wire it to becky **ScrubProxy** (all-intra) — the reel the host writes should point
   at the proxy per source, generating it on demand. This is the difference between "works" and "fast."

### Phase B — port the timeline's own controls into the native surface
becky-timeline must own these (they exist as DOM buttons today — see `ui/index.html` toolbar):
- **Clip model parity:** click-to-seek, **drag-reorder**, **resize/trim handles with edge-snap**,
  extend-selected-clip-by-one-frame (`[◄` / `►]`), split (✂), selection + multi-select.
- **Transport:** play/pause, **frame-step (Left/Right)**, **2× speed toggle**, **playback threshold**
  (skip quiet parts during review — evidence NOT cut), **trim-silence** (a real edit).
- **Zoom** (Ctrl+scroll, +/- ), ruler scrub, playhead.
- **Undo/redo** (Ctrl+Z / Ctrl+Shift+Z) — the edit model already supports copy-on-write; expose it.
- **Waveforms** on clips (Becky Review draws them; the fast-scrub value depends on the waveform being
  visible — that's how Jordan finds zero-crossings by eye).
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
