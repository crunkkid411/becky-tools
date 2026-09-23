# SPEC-BECKY-REVIEW-NATIVE.md — the fork where the timeline goes native

**Status (2026-07-03):** Clone landed + working. Phase 1 timeline is **fully coded, compiles, and
its whole page→host data path is PROVEN** — but **BLOCKED on ONE bug: the native timeline pane
renders BEHIND the WebView2** (WPF airspace z-order), so it's not visible. Everything else works.
**The next agent's single job: make the native pane draw on top → then Phase 1 is done.** §4–§7
are the handoff; read them first.

**One line:** keep every part of Becky Review that works, share the same brain, move only the
timeline to a native surface, never break the app Jordan uses daily.

Read WITH `CANVAS-NORTH-STAR.md` ("compiles ≠ done") and `native/timeline-bench/README.md` (the
benchmark that motivated this: native draws a 5000-clip timeline in 0.25 ms vs WebView2's 571 ms).

---

## 1. Why (settled)

The Becky Review timeline is an HTML/DOM widget with **no virtualization** — it builds one node
per clip and forces a full layout over all of them on every redraw (`ui/app.js`
`renderTimeline` → `reconcileTrack` → `refreshClipGeom`). Measured on Jordan's machine:

| clips | current (WebView2 redraw) | native (ImGui, culled) |
|------:|--------------------------:|-----------------------:|
| 14 (real reel) | 0.5 ms — instant | 0.23 ms — instant |
| 2000 | 104 ms — laggy | 0.30 ms — instant |
| 5000 | 571 ms — frozen | 0.25 ms — instant |

At today's reel sizes there's **no felt difference** — this is a foundation for growth (big cases)
and for what the DOM can't do well: **Vegas-accurate waveforms** (thousands of per-pixel peaks,
redrawn on zoom) and optional **multi-track**. GLFW is irrelevant (window library ≠ draw cost);
the lever is culling, already proven.

## 2. Architecture — fork the FACE, share the BRAIN

Three layers:
- **Brain** — the Go engine (`becky-go/cmd/clip`, shipped as `becky-review-engine.exe` in
  `becky-go/bin`). Owns folder index, qmd search, transcribe, ask, reel/EDL, `peaks` (audio
  waveform data), proxies, export, playback-threshold. **NOT forked. Single source of truth =
  the compatibility contract.**
- **Face** — the WebView2 UI (`ui/*`): colors, keyboard, panels, overlays, playback threshold,
  ask-becky, search. **Copied into the fork as-is, not rewritten.**
- **Shell + video** — WPF (`MainWindow.xaml.cs`) + native mpv overlaid on the page's `#videoHole`
  via `WindowsFormsHost` (`VideoHost`) + `MpvPlayer.cs` (`mpv.exe --wid=<panelHandle>`, driven
  over a JSON IPC pipe). **The timeline was built to reuse this overlay pattern — and that is
  exactly where it broke (see §4).**

**The fork = `gui/BeckyReviewNative/`.** A copy of the shell + face, identity renamed
`BeckyReview`→`BeckyReviewNative` (case-sensitive, so `becky-review-engine`/`beckyreview.local`/
`becky-go` are untouched). It resolves the SAME `becky-go/bin` engine by walking up from its exe
(`BeckyTools.cs:28-36`) → compatibility is automatic (verified: the clone booted the full
21-method `window.beckyReview` API and the shared engine indexed a folder through it).

## 3. What's built this session (exact inventory)

All under `gui/BeckyReviewNative/`. Builds clean: `dotnet build -c Release gui/BeckyReviewNative/BeckyReviewNative.csproj`.

**C#:**
- `TimelineControl.cs` — NEW. A WinForms `Control` with GDI+ `OnPaint`: a SINGLE horizontal track
  (becky's real model, not ImSequencer's row-per-clip) — ruler + clip rects (VIRTUALIZED: only
  draws clips overlapping the visible `[scrollSec, viewEnd]`) + gold playhead. Mouse drag →
  `ScrubRequested(compSec)`; wheel → `ViewChanged(pxPerSec, scrollSec)`. Fed by `SetClips` /
  `SetView` / `SetPlayhead`. Complete + sound.
- `MainWindow.Timeline.cs` — NEW `partial class MainWindow`: `_timeline` field, `SetWindowPos`
  P/Invoke, `StartTimeline()` (control in a Panel in `TimelineHostElement`), `HandleTimelineRect`
  (position + show `TimelineHost` + the `SetWindowPos(HWND_TOP)` attempt), `HandleTimelineReel`
  (parse clips → `SetClips`), `HandleTimelineMode`.
- `MainWindow.xaml` — added `<Border x:Name="TimelineHost">` wrapping `<WindowsFormsHost
  x:Name="TimelineHostElement">`, a sibling of `VideoHost` in `ContentGrid`, initially Collapsed.
- `MainWindow.xaml.cs` — 3 edits: `StartTimeline()` in `Window_Loaded`; 4 cases in the
  `OnWebMessage` switch; `_timeline?.Dispose()` in `OnClosed`.

**Web (`ui/`):**
- `app.js` — the native-TL block (inserted right after the `#videoHole`/`reportVideoRect`
  section): `nativeTL` flag; `reportTimelineRect()` (posts `.tlbody`'s `getBoundingClientRect` as
  `{t:'timelineRect',x,y,w,h}`); `pushTimelineReel()` (maps `state.timeline.clips` →
  `{start:start_sec, dur:dur_sec, label}` + `pxPerSec` + `scroll`); `pushTimelinePlayhead()`;
  `setNativeTL(on)` (hides `.tlinner`, posts `timelineMode`, pushes rect/reel/playhead); the
  `#tlNative` button hook; resize/scroll listeners; and **monkeypatch wrappers on `renderTimeline`
  + `updatePlayhead`** — both are hoisted `function` decls in the one big IIFE, so
  `renderTimeline = function(){ _orig(); if(nativeTL) pushTimelineReel(); }` routes every caller
  through the wrapper. Plus 2 cases in the host-message listener: `timelineScrub` →
  `seekTimeline(m.comp, false)`, `timelineView` → update `state.pxPerSec`.
- `index.html` — a `#tlNative` toggle button in `.tlactions`.

**Message protocol (all via `window.chrome.webview.postMessage` ⇄ host `PostToPage`):**

| dir | message | handler / effect |
|---|---|---|
| page→host | `{t:timelineMode, on}` | `HandleTimelineMode` — on=false hides pane; on=true is a NO-OP (page follows with rect+reel) |
| page→host | `{t:timelineRect, x,y,w,h}` | `HandleTimelineRect` — position + show `TimelineHost` |
| page→host | `{t:timelineReel, clips:[{start,dur,label}], pxPerSec, scroll}` | `HandleTimelineReel` — `SetClips` |
| page→host | `{t:timelinePlayhead, comp}` | `_timeline.SetPlayhead(comp)` |
| host→page | `{t:timelineScrub, comp}` | `seekTimeline(comp, false)` (native drag → seek, same path as a DOM click) |
| host→page | `{t:timelineView, pxPerSec, scroll}` | page updates zoom |

**Also:** Desktop shortcut "Becky Review Native.lnk" → `Open Becky Review Native.bat`. Benchmark
comparison artifact (published to claude.ai). The `native/timeline-bench/` spike + its scaled
reels `becky_14/500/2000/5000.reel.json`.

## 4. THE BLOCKER — the native pane renders BEHIND the WebView2

**Symptom:** toggle "native" on (or post a raw `timelineRect`) → `TimelineHost` goes Visible with a
valid rect and a hosted control, the DOM timeline hides — but the timeline area is **empty black:
no ruler, no clip bars.** The native pane is z-BELOW the WebView2, which paints over it.

**PROVEN — do NOT re-verify, these work:**
- The full page→host data path fires. A **raw** CDP post of `timelineRect` reliably reaches
  `HandleTimelineRect`, which set `TimelineHost.Visibility=Visible` + positioned it + confirmed
  `TimelineHostElement.Child != null`. (I proved this with a temp `StatusLabel.Text =
  "TLrect {w}x{h}@{x},{y} vis child=True"` diagnostic — the status bar showed exactly that.)
- `devicePixelRatio == 1` on Jordan's display → CSS px == WPF DIP, so the rect math is right
  (`.tlbody` rect was `{x:0, y:750, w:1920, h:216}` — valid, >2).
- The clone loads 14 real clips (`becky_14.reel.json`), the toggle flips `nativeTL`, no JS errors.

**Diagnosis:** WPF airspace z-order among MULTIPLE hosted native HWNDs (WebView2 + `VideoHost` +
`TimelineHost`) is unreliable; `TimelineHost` lands BELOW the WebView2's HWND. The mpv `VideoHost`
only *appears* to work because the DOM leaves `#videoHole` EMPTY, so a z-below pane still shows
through the empty region. The timeline area has DOM content, so a z-below pane loses the fight.

**⚠️ CRITICAL UNKNOWN — resolve FIRST (§7 Step 1):** I never confirmed mpv actually composites
*above* the WebView2 in this clone — the video pane was black in every screenshot because no clip
was ever playing. So it is UNKNOWN whether native-above-WebView2 is achievable at all with the
current default WebView2 hosting. That one test decides the whole fix path.

## 5. What I tried — worked / didn't

| attempt | result |
|---|---|
| In-process `WindowsFormsHost` → bare `Control` as `.Child` | pane behind WebView2 |
| Mirror mpv EXACTLY: `WindowsFormsHost` → `Panel` → control inside | **no change** — still behind |
| `SetWindowPos(TimelineHostElement.Handle, HWND_TOP, NOMOVE\|NOSIZE\|NOACTIVATE)` on show | **no change** — did not raise it |
| Hide DOM timeline (`.tlinner{visibility:hidden}`) to make a real hole like `#videoHole` | correct design, but revealed **empty black** → confirms the pane is genuinely not on top (not merely covered by DOM content) |
| DPI-scaling theory | ruled out (dpr=1) |
| `PrintWindow(PW_RENDERFULLCONTENT=2)` to screenshot the pane | **can't** capture airspace child HWNDs — use `CopyFromScreen` (real screen pixels) instead |

## 6. Reproduction + verification recipe (copy-paste)

**Rebuild** (⚠️ STOP the running exe first — a live instance locks it → `MSB3027 … used by another
process`):
```powershell
Get-Process BeckyReviewNative -EA SilentlyContinue | Stop-Process -Force
dotnet build -c Release "X:\AI-2\becky-tools\gui\BeckyReviewNative\BeckyReviewNative.csproj"
```
**Launch debuggable with a real 14-clip reel:**
```powershell
$env:BECKY_REVIEW_CDP_PORT='9334'   # original app uses 9333; use 9334 so they don't collide
$env:BECKY_REVIEW_FOLDER='X:\AI-2\becky-tools\native\timeline-bench'
$env:BECKY_REVIEW_REEL='X:\AI-2\becky-tools\native\timeline-bench\becky_14.reel.json'
Start-Process "X:\AI-2\becky-tools\gui\BeckyReviewNative\bin\Release\net8.0-windows\BeckyReviewNative.exe"
# poll http://127.0.0.1:9334/json/version until 200
```
**Drive the page via CDP** — the harness `scratchpad/cdp-eval.mjs` (node v24 built-in
`fetch`+`WebSocket`, zero deps): fetch `http://127.0.0.1:$CDP_PORT/json`, pick the target whose url
matches `index.html`, open its `webSocketDebuggerUrl`, `Runtime.evaluate` a JS file's contents
(`awaitPromise:true, returnByValue:true`), print the result. Toggle native: a JS file doing
`document.getElementById('tlNative').click()`. Raw-post a rect (bypasses the page):
`window.chrome.webview.postMessage({t:'timelineRect',x:60,y:60,w:800,h:300})`.

**SEE the pane** (Chrome-on-top blocks a normal capture; `CopyFromScreen` needs the app
foreground): `ShowWindow(h,6)`(minimize) → `ShowWindow(h,3)`(maximize) → `SetForegroundWindow(h)`,
wait ~1.2 s, then `Graphics.CopyFromScreen(GetWindowRect(h))`. `CopyFromScreen` reads REAL screen
pixels, so it WILL show an airspace pane if it's on top. Get the hwnd from
`(Get-Process BeckyReviewNative).MainWindowHandle` (FindWindow-by-title is flaky). Window title:
"Becky Review (Native)".

**Fastest C#-side probe:** re-add `StatusLabel.Text = $"TLrect {w}x{h} vis child={TimelineHostElement.Child!=null}"`
in `HandleTimelineRect`, rebuild, capture — the status bar (top-right) tells you in one screenshot
whether a message reached C#.

## 7. Step-by-step for the next agent

**STEP 1 — DECISIVE. Can native content sit above the WebView2 at all?** Launch the clone, load a
clip and PLAY it (CDP: click a video's green "+", then post `{t:'mpv',op:'toggle'}` or press Space;
the reel is already loaded so you can also just seek+play). Foreground + `CopyFromScreen`. **Does
the mpv video show in the center?**
- **YES** → native CAN be above; the difference is mpv = reparented separate process vs my
  timeline = in-process `WindowsFormsHost`. → **STEP 2.**
- **NO** (center black even while playing) → native can't overlay in the current hosting. →
  **STEP 3.**

**STEP 2 — run the timeline as a reparented SEPARATE PROCESS, exactly like mpv.** This is the
ORIGINAL sidecar plan (I switched to in-process C# and hit the airspace wall). `native/timeline-bench/`
already renders a reel + drag-scrubs; adapt it: (a) create its window as `WS_CHILD` of a parent
HWND passed via `--parent <hwnd>` (instead of a top-level window — see its `src/main.cpp`
`CreateWindowW`); (b) read the reel from stdin as NDJSON instead of `clips.json`; (c) write scrub
comp-seconds to stdout. Then in `MainWindow`, replace `TimelineHostElement.Child = panel` with the
mpv pattern: put a WinForms `Panel` in `TimelineHostElement`, launch `timeline-bench.exe --parent
<panel.Handle>`, drive it over stdio. **`MpvPlayer.cs` (`--wid=<handle>` + IPC) is the exact
template — copy its structure.** The timeline .exe would need to build into the fork's `runtime/`
(mirror `fetch-mpv.ps1` / the csproj copy).

**STEP 3 — cleanest if OVERLAP is the problem: give the native timeline its OWN non-overlapping WPF
region.** The z-fight only exists because two native surfaces overlap. Restructure the WPF: the
WebView2 fills the TOP (find | video | chat + the timeline TOOLBAR), and the native timeline is a
separate WPF row BELOW it (`DockPanel.Dock="Bottom"`), NOT overlaid. No overlap = no z-fight = it
just draws. Cost: split the timeline — keep `.tlhead` (toolbar) in the web UI, move only the
`.tlbody` track to the native row; shrink the web grid to exclude the track. This is the ponytail
route (delete the problem, don't fight it). The in-process `TimelineControl.cs` is reused as-is —
it just lives in a non-overlapping host.

**STEP 4 — heaviest, last resort: WebView2 `CoreWebView2CompositionController`.** Switch the
WebView2 from default HwndHost mode to visual/DComp hosting, which lets you z-order the web visual
against native visuals properly. Big change to `InitWebViewAsync`.

**Also resolve the one open discrepancy:** I once observed a real toggle NOT updating StatusLabel
while a RAW post DID — but that was confounded by the minimize/maximize dance. Re-add the
diagnostic and do a CLEAN toggle capture. `setNativeTL`'s `reportTimelineRect` is byte-identical to
the raw post, so it SHOULD post; confirm there's ONLY the airspace bug, not a second page bug.

## 8. After the pane is visible: finish Phase 1, then Phase 2

- **Phase 1 done** = native timeline renders the reel + drag-scrubs (video follows, frame-accurate)
  + toggle works, everything else unchanged, original untouched. Verify per §6 on the real reel
  with real mouse+keyboard (CANVAS-NORTH-STAR DoD).
- **Keyboard/focus** (the one integration risk): the native surface owns mouse; keyboard stays with
  the web UI (the `MouseEnter → WebView.Focus()` in `StartTimeline` is the seed; forward keydowns
  from the native HWND if a native shortcut is needed). mpv already coexists this way.
- **Phase 2** (Jordan's priorities are single-track + **accurate waveforms**): route native edits
  (trim/split/reorder) back through the SAME engine verbs the web UI uses (engine = source of truth,
  both views re-fetch on change). Waveforms: `becky-go/cmd/clip/peaks.go` already decodes real PCM
  but coarsely (8 kHz, 200 buckets `max(abs)`, per-clip normalized — why they read as a "guide").
  Upgrade to true min/max per screen-pixel + absolute levels + a cached peak file; draw natively.
  Add as a new engine VERB (additive) — never fork the engine.
- **Phase 3** (deferred by Jordan): multi-track (separate A/V lanes). Single-track for now.

## 9. Compatibility contract (load-bearing)

Both apps talk to the same `becky-go` engine, same reel format + verbs. Never fork the engine.
becky-hits, `BECKY_REVIEW_REEL`, the forensic pipeline, every CLI keep working with the native app
for free. New capability = a new VERB, never a parallel engine.

## 10. Gotchas the next agent won't know

- **STOP `BeckyReviewNative.exe` before every `dotnet build`** — a running instance locks the exe →
  `MSB3027`. (I wasted a cycle capturing stale code because of this.)
- **Fact-forcing + branch-safety hooks are ON:** every Write/Edit first demands you state (1) who
  calls the file, (2) no existing equivalent (Glob), (3) data fields, (4) the user's instruction
  verbatim — then RETRY the same op (the first attempt always errors; the retry passes). `git
  commit` to master is blocked (branch, then FF). Bash/git calls may demand quoting the instruction.
- **`ui/*` is copied to `bin/…/net8.0-windows/ui/` by the csproj (PreserveNewest)** — a rebuild
  ships app.js/index.html edits; no manual copy needed.
- **CDP is opt-in** via `BECKY_REVIEW_CDP_PORT` (`MainWindow.xaml.cs:80`); a normally-launched
  instance has NO debug socket. F12 devtools (`AreDevToolsEnabled`) is NOT the remote port.
- **The `SetWindowPos(HWND_TOP)` in `HandleTimelineRect` is a harmless but INEFFECTIVE leftover** —
  delete it if you take STEP 2 or 3.
- Nothing is committed yet — it's all local, ready to commit once the pane shows (personal profile,
  branch → FF master).
