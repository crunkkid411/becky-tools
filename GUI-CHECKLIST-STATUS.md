# GUI-CHECKLIST-STATUS.md — Becky Review 3 vs. the 120 acceptance criteria

**Audited 2026-07-20 (05:10) against `native/becky-review/main.cpp` at commit `84d7d75`, 6096 lines, READ IN FULL**,
plus the Go engine it drives (`becky-go/cmd/clip/*.go`, `becky-go/internal/edl/`, `becky-go/internal/footage/`,
`becky-go/cmd/becky-hits`, `becky-go/cmd/becky-judge`) and `Open Forensic Hits.bat`.

> ## THIS FILE SUPERSEDES EVERY EARLIER AUDIT IN THIS REPO.
> The previous version of this file was written before a large night of work and was **wrong in both
> directions**. It listed Up/Down zoom, Redo, Skip Quiet's `g_thrOn` toggle, ruler pan, per-clip colour
> and the extend-one-frame buttons as ABSENT. **All six are present and were verified by reading the
> surrounding code, not by grep.** An auditor who trusted that file concluded the orchestrator had
> lied. If you are about to cite an older audit: don't. Cite this one, or re-read the file.

**Method.** Every line of `main.cpp` was read. Grep was used only to locate candidates; each one was
then read in context before being called present or absent. Items marked MEASURED carry a number
produced this session by driving the real window on Jordan's real 88-clip reel
(`X:/Videos/2025/11_November/Rendered/post_constantly.reel.json`) with `BECKY_REVIEW_FRAME_TRACE` and
`BECKY_REVIEW_EDIT_LOG` recording.

## SUMMARY

| Status | Count |
|---|---|
| **DONE** | **103** |
| **PARTIAL** | **11** |
| **ABSENT** | **3** |
| **UNVERIFIED** (needs a human eye or a long session) | **3** |
| **Total** | **120** |

*(Updated 2026-07-23: rows 18, 19, 41, 59, 68, 76, 105 moved to DONE — see each row's evidence.
Original audit below was against `84d7d75`; these 7 landed in `2c397ae`/`2648c33`/`d3e7db0`/
`b5a7b19`/`858711d`, all now on master. Nothing else in this file was re-audited.)*

### What changed this session

Three fixes landed, each measured before it was called done:

1. **`7c0fe4e`** — `drainAsync()` moved out of the middle of `drawTimeline()`. Async replies that
   replace `g_track` were being delivered while `drawTimeline` was partway through reading it. It did
   not crash only because no live reference happened to survive that one line. Also made `add_clip`
   async (it held a 6s timeout on the UI thread).
2. **`a39132b`** — **dragging a caption did nothing.** Caption-move and the newer ruler drag-to-pan had
   both been given gesture kind 11, and the pan branch is tested first, so caption-move was dead code.
   Fixed by giving caption-move kind 12.
3. **`84d7d75`** — **Render Selection froze the window for 2,173 ms.** Render had been made async;
   Render Selection sat one line below it and was missed, still holding a 300s timeout on the UI
   thread. Screenshot (20s, ffmpeg) was the same.

### Three findings that correct the record

1. **The "2 second lag on every new clip" (feedback9) is real, but it was NOT `add_clip`.** Measured on
   the real reel, `add_clip` is **4–16 ms** and produces **zero** frames over 100 ms. The 2-second stall
   reproduces on **Render Selection**: a single **2,173.5 ms** frame, now **47.4 ms**. His number was
   right; the suspected cause was wrong.
2. **"Every time I split a clip it does the 'preparing' thing again" is FIXED and stays fixed under
   load.** A 20-press split burst on the warm reel: `peaksJobsEnqueued` stayed **flat at 88** across all
   5 timeline reloads (89→93 clips), i.e. **zero** new decode work per split — the I-6 bar the code
   documents — and the screenshot shows **no "preparing" hatch anywhere**. 0 frames over 100 ms.
3. **A claim repeated in the source comments is false.** `setOverlayMode`'s comment says "the engine's
   bridge dispatches one verb at a time". It does not: `becky-go/cmd/clip/main.go:231` handles every
   request in its own goroutine, and every slow verb (`ExportReel`, `Ask`, `OpenFolder`,
   `TranscribeAll`) copies state under `a.mu` and **releases the lock before the slow work**. Do not
   base a fix on that comment.

---

## THE 120

### LAYOUT

| # | Requirement | Status | Evidence |
|---|---|---|---|
| 1 | Timecode ruler with tick marks | **DONE** | `main.cpp:3141-3153` — major ticks + `fmtTime` labels + 4 minor ticks per step |
| 2 | Thumbnail = small FIXED icon at clip's left | **DONE** | `:2769` `thumbH = laneH > 70 ? 40.0f : 0.0f` (fixed, never zoom-scaled); drawn at `x0+3` `:3210` |
| 3 | Thumbnail inset so it never covers the waveform | **DONE** | `:2770-2771` `headerH = max(labelH, thumbH+4); wy0 = aY+2+headerH` |
| 4 | Clips 2× taller; toolbar/ruler/thumbs unchanged | **PARTIAL** | Fixed sizes confirmed (`rulerH=22`, `thumbH=40`), but lane height is the residual of `timelineH = max(212, availH*0.26)` (`:5045`) — no explicit doubling. **Needs his eye, not a grep.** |
| 5 | Audio waveform inside the clip body | **DONE** | `:3187` `drawWave(...)` per clip; renderer `:2302` |
| 6 | Toolbar buttons never shift; render-selection greyed not hidden | **DONE** | `fixedButton` `:920` sizes to the widest label of every state; applied to Play/Pause, 3-state Overlay, Skip Quiet, `Render Selection (000)` `:5560`; `BeginDisabled`, never hidden |
| 7 | Remove prev/next frame buttons (keep shortcuts) | **DONE** | No such buttons. Frame stepping survives on Left/Right `:4657-4676` |
| 8 | Two arrow buttons extending the clip one frame | **DONE** | `:5460-5478` `<+1f##extl` / `+1f>##extr`, one `set_trim` each (one undo per press), at the clip's OWN source fps |
| 9 | Playhead all black, top larger, covers the ruler | **DONE** | `:2267` `COL_PLAYHEAD = IM_COL32(0,0,0,255)`; head spans `p.y+1 → p.y+20` vs `rulerH=22` `:3300-3309` |
| 10 | Two tiny vertical hashmarks in the white part | **DONE** | `:3309-3310` two `AddLine` at `px±2.5f` in `COL_PHGRIP` |
| 11 | Second playhead "stock", no moving white head | **DONE** | `:3294-3299` plain `AddLine`, no flag/triangle |
| 12 | Screenshot button saving `Screenshot_0001`, incrementing | **PARTIAL** | Button exists and is now async `:5580`, but the ENGINE names the file `<sourcestem>_<t>s.png` (`becky-go/cmd/clip/export.go:290`). No `Screenshot_NNNN` counter exists — searched `Screenshot_`, `%04d.*png`. **Engine-side fix.** |
| 13 | becky-clip button on the timeline | **ABSENT** | Searched `becky-clip`, `beckyclip` in `main.cpp` — zero hits |
| 14 | Non-intrusive semi-transparent >1s loading bar | **DONE** | `:5991-6019` `SetNextWindowBgAlpha(0.62f)`, `NoInputs\|NoNav`, gated on `nowSec()-since > 1.0` |
| 15 | Playback-threshold toggle, running-person icon | **DONE** | `:5532` flips `g_thrOn`; icon `ICON_RUN` (U+E805, a real Segoe MDL2 glyph) `:904`; amber when ON |
| 16 | Threshold ON → draggable horizontal bar | **DONE** | Bar `:3285`, hit test `onThresholdBar`, drag gesture kind 7 `:2966-2971` |
| 17 | Below-threshold sections visually dimmed | **DONE** | `:2282` `COL_QUIETDIM` over `g_quietRanges` `:3272-3277` |
| 18 | Three sorts: date-newest (default), name Z-A, "most relevant" | **DONE** | Commit `858711d`. Library date/name pills unchanged (`sortLibrary`/`g_sortMode`, no `score` field on a video). "Most relevant" lives on the HITS panel instead — `g_hitRelevance`/`applyHitSort()` (`:4046-4048`), sorts `g_hits` by `.score` desc; pill at `:6029` |
| 19 | "Most relevant" = crown icon, no text, blue | **DONE (deviated on purpose)** | Same commit. Blue pill (`IM_COL32(0x00,0xAE,0xEF,255)`), but labelled "relevant" not a crown — comment at `:6084-6093` documents that Segoe MDL2's loaded E700-EDFF range has no crown glyph (all 1792 checked), and a word beats an undecodable icon |
| 20 | qmd toggle switch | **DONE** | `:5131` `pillButton("smart", g_smartSearch, ...)` — persistent toggle, blue when on, Enter runs the armed mode |
| 21 | Un-indexed results show an "index" icon | **DONE** | The library card draws a green circled **+** when there is no transcript and a tick when there is (`drawLibraryCard` `:3830-3842`), with a tooltip |
| 22 | After "back", just-viewed transcript gets a green outline | **DONE** | `:3398-3400` `AddRect(..., IM_COL32(0x14,0xFF,0x39,255))` on `g_libJustViewedIdx` |
| 23 | Transcript minimal — filename not repeated per segment | **DONE** | `:5192` `line = c.timecode + "  " + c.text`; the name appears once, in the header |
| 24 | Transcript flows as ONE continuous document | **PARTIAL** | `:5190-5196` one `Selectable` per cue in a child window — a row list with `ItemSpacing(9,7)` gaps, no reflow. Closer to the old boxed layout than to audapolis |
| 25 | Review "?" questions in the right panel, clickable to answer | **DONE** | Cards `:5744-5790`; **"Answer this"** `:5777` retargets the one ask box, and Send routes to the engine's `save_answer` `:4009` with `{id, question, answer}` (verb confirmed at `bridge.go:172-177`) |
| 26 | Overlay order Date, Timecode, filename | **DONE** | `:1501-1522` `overlayLines()` |
| 27 | `ORIG TC 01:32:34:02` (space after TC) | **DONE** | `:1506` |
| 28 | Overlay Date includes the timezone | **DONE** | `:1504` `"Date: " + c.date + " UTC"` |
| 29 | Overlay button is a 3-state toggle, default on-but-hidden | **DONE** | `g_ovMode = 1` default `:1407`; cycle `:5451` |
| 30 | "Load reel" lists .json at the TOP | **PARTIAL** | `:3946-3951` the DEFAULT filter is `*.txt;*.xml;*.json` so reels show immediately and videos are excluded — but `.json` alone is 4th in the dropdown and nothing forces .json to sort first |
| 31 | Timeline thumbnails in a `timeline_thumbnails` subfolder | **DONE** | `becky-go/cmd/clip/export.go:501-511`; test `export_dest_test.go:62` |

### READABILITY / COLOR

| # | Requirement | Status | Evidence |
|---|---|---|---|
| 32 | Exact 8-colour palette, in order, everywhere | **DONE** | Engine `becky-go/cmd/clip/clipcolor.go:14-23` is exactly `#14FF39,#00AEEF,#DC143C,#8A2BE2,#FF57D1,#FFD700,#16F0EA,#FF8C00`; `main.cpp:3539` `kPalette[8]` matches, for Q&A cards and drive chips |
| 33 | The ENTIRE clip body takes the source colour | **DONE** | `:3182` fills the whole clip rect with `IM_COL32(c.r,c.g,c.b, selected?255:62)`. **Verified on screen** — the reel renders green (`#14FF39`, palette slot 1), not the old default blue |
| 34 | Trim handles and borders match the clip colour | **DONE** | `:3191` border, `:3232` handles — both from `c.r/g/b` |
| 35 | Selected clip fully opaque; no outline | **DONE** | `:3182` alpha 255 selected / 62 unselected |
| 36 | Colour persistent per video for the whole project | **DONE** | `clipcolor.go:42-70` — `clipColorBy` is a package-level map that **only ever grows**; nothing deletes. Regression test `clipcolor_test.go:24-42` asserts the fb7 delete-video-2 case |
| 37 | Different sources colour-coded so same-source is obvious | **DONE** | `Color: clipColor(c.Source)` `app.go:970`; parsed at `main.cpp:1915-1919`. Library cards wear the same accent `:3405` |
| 38 | Overlay preview identical size to the render, or no preview | **PARTIAL** | The DEFAULT satisfies the "or" branch (mode 1 = not previewed). Mode 2's ASS is pushed with a fixed `\fs28` `:1547`, not derived from the render's drawtext size — so mode 2 is not PROVEN identical |
| 39 | Never strip/flatten/monochrome "for accessibility" | **DONE** | High-contrast palette throughout; colour-coded status, amber caption lane, neon clip count `:6053` |
| 40 | I-beam cursor over the timeline | **DONE** | `:2699` `SetMouseCursor(ImGuiMouseCursor_TextInput)` |

### INTERACTION

| # | Requirement | Status | Evidence |
|---|---|---|---|
| 41 | Ctrl+Z undo, Ctrl+Shift+Z redo, **each with a visible button** | **DONE** | Commit `2648c33`. Buttons at `:6488`/`:6491` (`ico(ICON_UNDO...)`/`ico(ICON_REDO...)`, plain `ImGui::Button` — the same pattern Save/Load/Screenshot use for constant-label icon buttons), leftmost on row 2, always enabled, each with a tooltip naming its chord |
| 42 | Undo undoes the IMMEDIATE last action | **UNVERIFIED** | Ordering is right by construction (FIFO `editWorker` `:399`, engine `pushUndoLocked` per mutation). Settle by: split, Ctrl+Z, confirm it un-splits rather than moving |
| 43 | Mouse wheel = zoom | **DONE** | `:2799` `applyWheel` |
| 44 | Ctrl+wheel = scroll sideways | **DONE** | `:2796` |
| 45 | Wheel zoom centres on the playhead | **DONE** | `:2789-2793` `zoomAnchorX()` prefers the playhead |
| 46 | Scroll-to-zoom works at ALL times | **DONE** | `:2799` gated only on `hovered`, not on focus or panel state |
| 47 | Zooming out reveals space right of the last clip | **DONE** | `:2812` `maxScroll = max(0, g_compDur - viewDur*0.15)` |
| 48 | Up arrow zoom in, Down zoom out | **DONE** | `:4657-4660` sets `g_zoomReq`, spent in `drawTimeline` `:2803` through the SAME `applyWheel` path as the wheel; guarded on `!g_libFocused` so it never fights list nav |
| 49 | Left/Right move the playhead one frame | **DONE** | `:4665`, `:4673` — `1.0/sourceFps(...)`, per-clip fps |
| 50 | Ctrl+Left/Right travel the WHOLE timeline | **DONE** | `collectBoundaries` `:2642` (both tracks + 0 + `g_compDur`, deduped, one shared list) + modifier read on the same clock `:4650` |
| 51 | Middle-click drag pans | **DONE** | `:2805-2809` |
| 52 | Click+drag on the ruler pans sideways | **DONE** | `:2923` gesture kind 11; pan body `:2960-2966` |
| 53 | Clicking the ruler moves playhead AND stock | **DONE** | `:2918-2919` sets `curSec` and `g_stockSec` |
| 54 | Ctrl+click multi-select; Shift+click range | **DONE** | `:3033-3044` |
| 55 | Dragging one of several selected moves them all | **DONE** | group collected on press `:2937`, `reorder_many` `:3078` |
| 56 | Button to render just the selection | **DONE** | `:5560` → `export_selection`, now async |
| 57 | Click in a clip during playback sets the return position | **DONE** | `:3050` `else { g_stockSec = t; g_stockFlash = true; }` |
| 58 | Stop auto-scrolling once the return position is used | **DONE** | `:2808` auto-follow requires `g_stockSec < 0` |
| 59 | Pause returns to where playback STARTED; Enter stops it where it is | **DONE** | Commit `d3e7db0`. `g_playStartSec` recorded on the play-start edge (`:5699`); `stopPlayback(curSec, playing, returnToStart)` (`:1968`) is the one shared stop path — Pause/Space passes `true` (stock else playStart), Enter (`:5245`, `VK_RETURN`) passes `false` (stays exactly where stopped) |
| 60 | Moved stock flashes black/white | **DONE** | `:3297` `fmod(nowSec(), 0.8) >= 0.4` |
| 61 | Clicking the stock also selects that clip, without interrupting playback | **DONE** | `:3046-3051` selection runs first, then the playing branch sets the stock; `playing` untouched |
| 62 | Cut/split during playback applies at the stock | **DONE** | `editT()` `:4402`, used by S/Del/O/I; none set `playing = false` |
| 63 | After a split, the clip AFTER the playhead is selected | **DONE** | `:4795` selects `new_id` (the engine gives the RIGHT half the new id); `loadTimelineView` clears the old selection first `:1930` |
| 64 | After a ripple edit the playhead moves with the ripple | **DONE** | `rippleCurSec` `:1608`, applied at `:4801/4805/4809` |
| 65 | 2× playback button + Shift+Space | **DONE** | Button `:5427`; Shift+Space `:4404-4406` |
| 66 | Esc must ALSO delete the selected clip | **DONE** | `:4487` one shared path with Delete |
| 67 | Right-click clip → File Browser / Copy File Name / Open transcript **at that clip's timecode** | **PARTIAL** | Menu complete `:2898-2908`; Copy File Name gives the VIDEO name with extension. But **Open Transcript loads from the top** — the engine's `transcript` verb takes only `name` (`bridge.go:55`) and `Cue` carries no index, so there is nothing to jump to. **Engine-side gap** |
| 68 | Right-click a QUOTE or a video in the left panel | **DONE** | Commit `b5a7b19`. Hit rows now have the same menu (`hitctx` popup, `:6076-6091`): Add to Timeline, Open in File Browser, Copy File Name, Copy Quote, Transcribe |
| 69 | "Open in file browser" always selects the file | **DONE** | `:3875` `explorer.exe /select,"<path>"` |
| 70 | **Double**-clicking render/export opens the folder with the mp4 selected | **PARTIAL** | The folder IS opened with the mp4 selected — but on **single** click (`:5556`, `:5573`). Single-click behaviour was changed rather than left alone |
| 71 | Drag footage onto the timeline from any folder | **DONE** | `DragAcceptFiles` `:4222`; `WM_DROPFILES` → SEH-guarded parse `:2193`; drop → `requestAddExternal` (background thread) |
| 72 | The drag thumbnail must not obstruct the drop target | **ABSENT** | Searched `IDropTarget`, `RegisterDragDrop`, `IDropTargetHelper`, `DragImage` — none. `WM_DROPFILES` gives no control over the shell's drag image |
| 73 | Up/Down navigate the left panel FROM the current selection | **DONE** | `:5263-5264` `g_libSel ± 1`, with `SetScrollHereY` to keep it in view |
| 74 | ONE left-panel selection shared by mouse and keyboard | **DONE** | `:5294` a mouse click writes the same `g_libSel` the arrows move |
| 75 | Spacebar after arrow-selecting plays that clip | **DONE** | `:5330`, guarded on `!WantTextInput` |
| 76 | Enter = double-click (quote → timeline; video → transcript) | **DONE** | Commit `b5a7b19`. Enter-on-selected-hit calls `addHitToTimeline` (`:6125-6127`), same as double-click; Up/Down move `g_hitSel` first so Enter always acts on the drawn selection |
| 77 | After "back", the video list returns to the same scroll position | **UNVERIFIED** | Back only clears `g_cueName`/`g_cues`; ImGui retains the child's scroll across frames where it is not submitted, so it plausibly works, but nothing restores it explicitly |
| 78 | Search WITHIN a specific transcript | **DONE** | `:5187` `##within` + filter `:5191` |
| 79 | Clicking the timeline returns keyboard focus to it | **DONE** | `:6041-6044` a left/right click in the timeline panel calls `SetWindowFocus()`, dropping `WantCaptureKeyboard` next frame |
| 80 | `clips_ORIGINAL_NNNN.mp4` / `clips_compilation_NNNN.mp4` | **DONE** | `export.go:565-580` + `nextSequencedPath` `:552-560`, starts `_0001`, never overwrites |
| 81 | Forensic hits must not overwrite | **DONE** | `becky-judge/main.go:344-356` `_forensic_hits1.json`, `2`, … |
| 82 | New hits file while a session is open → ask to save, or second instance | **ABSENT** | No file watcher (`FindFirstChangeNotification`, `ReadDirectoryChanges` — zero hits); `BECKY_REVIEW_REEL` is read once at boot and never re-checked |
| 83 | Exported EDL includes an audio track | **DONE** | `internal/edl/cmx3600.go:66` — every event uses channel `AA/V`, with a comment naming the Vegas bug |
| 84 | "Open Forensic Hits.bat" also produces a Vegas .edl | **DONE** | `becky-hits/main.go:145-151` writes a CMX3600 `.edl` beside the reel; `.bat:44` invokes it |

### PERFORMANCE

| # | Requirement | Status | Evidence |
|---|---|---|---|
| 85 | 20 rapid splits → zero "preparing", zero dropped frames | **DONE — MEASURED** | 20-press burst on the warm 88-clip reel: `peaksJobsEnqueued` **flat at 88** across all 5 reloads (89→93 clips) = zero new decode work per split; **0 frames over 100 ms**, worst 31.8 ms; screenshot shows **no hatch**. Dedup at `:1294` `peaksWindowFilled` + in-flight/queued checks `:1338-1339` |
| 86 | Adding a clip whose source is already on the timeline → no measurable lag | **DONE — MEASURED** | 4 real adds: engine round trip **4.4 / 7.3 / 4.8 / 16.4 ms**; **0 frames over 100 ms** |
| 87 | Background work never stutters the cursor | **DONE** | `:481` `THREAD_MODE_BACKGROUND_BEGIN` (drops I/O + memory priority) with `BELOW_NORMAL` fallback; decode pool capped `:1216` `g_decActive < (g_busyHint ? 1 : 2)` |
| 88 | 5-minute stress drive → no UI stall > 100 ms | **PARTIAL** | Harness exists and was run: **3,784 frames of boot + reel load = 0 stalls**, plus a split burst and a render, all 0. But not a continuous 5-minute Jordan-speed drive |
| 89 | No ~20-second full stall | **DONE — MEASURED** | 0 frames over 100 ms across every run this session; worst observed after the fixes is 47.4 ms |
| 90 | No ~2 seconds of lag on every new clip | **DONE — MEASURED** | The 2s stall was **Render Selection**, not the clip add: **2,173.5 ms → 47.4 ms** (`84d7d75`). Clip adds measured 4–16 ms |
| 91 | Caches keyed by SOURCE FILE + resolution, never by clip | **DONE** | `:839` `g_peaks` keyed by source path; three LOD levels `n0/x0, n1/x1, n2/x2`; on-disk `.bpk` keyed source+size+mtime `:1010` |
| 92 | Timeline keeps playing while a segment is proxied | **DONE** | Playback is one mpv EDL over the whole reel `:1744`; peaks decode is on detached background threads and nothing on the playback path waits |
| 93 | Adding a quote must not block on proxy generation | **DONE** | No proxy generation on this path at all, and as of `7c0fe4e` the add itself is async |
| 94 | "Save" must not freeze the program | **DONE** | `save_reel` is `engineCallAsync` `:5598` |
| 95 | Trims/cuts must not make clips or waveforms flash | **UNVERIFIED** | Trim drag draws a ghost from gesture state without touching the model `:3161-3165`, and peaks survive reloads. `clipPreparing()` is the only plausible flash source and it did NOT trigger in the split burst. **Needs his eye during real trimming** |
| 96 | Deleting clips must not move the timeline view | **DONE** | `:2829` the clamp now only intervenes when the view is scrolled past EVERYTHING, and never mid-gesture (it used to run unconditionally every frame) |
| 97 | Responsive past ~3 minutes of clips | **PARTIAL** | The 150 s / 88-clip reel measures clean (median 16 ms). No 3-minute-plus reel was tested |
| 98 | Responsiveness is a correctness requirement | **DONE — MEASURED** | Median frame **16.0 ms** (a solid 60 fps) across every run; 0 stalls after the fixes. Waitable swap chain + `SetMaximumFrameLatency(1)` + wait-at-top-of-frame `:2024-2025`, `:4419` |
| 99 | Waveforms stream in progressively, never waited on | **PARTIAL** | Progressive by WINDOW and never blocking ✓ (`secFilled`, `g_fillEpoch`, missing bins skipped `:2321`). **Not coarse-then-refined**: the 3-level pyramid is a zoom LOD built from full-resolution data, not a fast first pass |
| 100 | No embedded browser engine | **DONE** | Dear ImGui + D3D11 + mpv `--wid`. Searched `WebView`, `CEF`, `Chromium`, `IWebBrowser` — zero hits |

### MUST-NEVER-DO

| # | Requirement (his words) | Status | Evidence |
|---|---|---|---|
| 101 | "remove the yellow outline around the selected clip" | **DONE** | `:3182-3191` selection is an opaque fill; border is the clip's own colour. `COL_DROPMARK` (the only yellow) is declared and never used |
| 102 | "'split clip' popup should not exist" | **DONE** | Split is keypress → `queueEdit` → drain, no UI. The only two popups are the two right-click menus |
| 103 | "all the little popups on the timeline need to go away" | **DONE** | No message is emitted for remove/reorder/undo/redo; `g_renderMsg` is one dim status line in the video pane `:5626`, never on the timeline |
| 104 | "'render' folder should NOT appear in search or browse" | **DONE** | `internal/footage/discover.go:81-83` `excludedWalkDirs{"render":true}`, applied by all four WalkDir visitors |
| 105 | "when a user clicks within a clip, the playhead should not be affected" | **DONE** | Commit `2c397ae`. A clip-body click (`:3552-3574`) now only selects; `curSec` is untouched. While playing it still sets the STOCK (a deliberate exception, see #57/#61) — paused, nothing but the selection changes |
| 106 | "deleting clips should NOT move the timeline" | **DONE** | Same as #96 — `:2829` |
| 107 | "ruler click moves playhead + stock, but NOT the selection" | **DONE** | `:2916-2920` — kind 11 never writes `g_sel`; both `curSec` and `g_stockSec` are set |
| 108 | "ensure trim handles and borders are not green" | **DONE** | `:3191`, `:3232` both from `c.r/g/b` |
| 109 | "video 3 must remain #DC143C for the rest of the project" | **DONE** | `clipcolor.go:56-70` map only grows; test `clipcolor_test.go:24-42` |
| 110 | "buttons on the timeline toolbar should not move" | **DONE** | `fixedButton` `:920` for every variable-label control; the zoom group sits at a FIXED x `:6071` so a changing clip count cannot shove it |
| 111 | "remove 'previous frame' and 'next frame' buttons" | **DONE** | They do not exist |
| 112 | "no reason to have the filename listed for every segment" | **DONE** | `:5192` |
| 113 | "it should not automatically close my session" | **DONE** | `PostQuitMessage` only in `WM_DESTROY`; no `exit(`/`ExitProcess` |
| 114 | "it should add digits to the end" rather than overwrite | **DONE** | `export.go:552-560`; `becky-judge/main.go:344-356` |
| 115 | "overlay preview … make it identical, or do not preview" | **PARTIAL** | Same as #38 — the default is "do not preview", but the opt-in mode's size is not derived from the render |
| 116 | "thumbnail preview images are littering the 'render' folder" | **DONE** | Same as #31 |
| 117 | "ctrl + back/forward no longer navigate beyond the clip" — CRITICAL | **DONE** | Same as #50 — one shared boundary list + the modifier-clock root-cause fix `:4600-4650` |
| 118 | "Do NOT strip color … for accessibility" | **DONE** | Same as #39 |
| 119 | "No embedded browser engines, ever" | **DONE** | Same as #100 |
| 120 | "the 'hand' mouse icon should be replaced with the 'Ibeam'" | **DONE** | Same as #40 |

---

## WHAT REMAINS, RANKED BY VALUE TO JORDAN

> **UPDATE 2026-07-23:** all five items ranked #1-#5 below are DONE and on master
> (`2c397ae`, `2648c33`, `d3e7db0`, `b5a7b19`, `858711d` — see rows 105/57, 41, 59, 68/76, 18/19
> above for the current line numbers and evidence). This section was audited against `84d7d75`;
> three days and ~15 more commits landed before anyone updated it, and a later session was
> dispatched to redo work that had already shipped. **Before handing this section out as a work
> order, re-grep the row's evidence against the CURRENT file** — the same rule this doc's own
> preamble already states for everything else, which this section itself failed to follow.

He is a professional editor, sighted with impaired vision, mouse+keyboard only, for whom reading is
physically expensive and responsiveness is correctness. Repetition across his ten documents is the
strongest signal — it means it was ignored the first time.

### 1. ~~Clicking a clip while PAUSED still throws away his playhead~~ — items 105, 57 — DONE (`2c397ae`)

### 2. ~~Undo and Redo have no visible buttons~~ — item 41 — DONE (`2648c33`)

### 3. ~~Enter does not stop the playhead, and Pause does not return to where playback started~~ — item 59 — DONE (`d3e7db0`)

### 4. ~~Search-hit rows have no right-click menu and no Enter~~ — items 68, 76 — DONE (`b5a7b19`)

### 5. ~~"Most relevant" sort, with the crown icon~~ — items 18, 19 — DONE (`858711d`); shipped as a blue text pill, not a crown glyph — see row 19

### Deliberately NOT recommended, with the measurement that justifies it

- **Converting the remaining synchronous `engineCall` sites wholesale.** After `84d7d75` the ones left
  on a UI path are `emitScrub` (2s timeout, fires during drags), `reorder`/`set_trim` on drag release
  (4s), `load_reel` (30s), `openTranscript` (25s) and `playWholeVideo`'s probe (8s). They look alarming,
  but **measured, none of them stalls**: a full drag / scrub / split / render session produced **0 frames
  over 100 ms and a median of 16.0 ms**. Those verbs are in-memory and return in single-digit ms. The two
  that were genuinely slow (real ffmpeg work — Render Selection and Screenshot) are fixed. Converting the
  rest means capturing `curSec` / `playing` / `lastComposed` by reference across an async boundary in five
  more places, which is real new risk for no measured gain. **Revisit the moment a frame trace shows a
  stall on one of them** — the harness is already compiled into the binary.
- **Item 4 (clips 2× taller) and item 95 (flashing).** Both are "does it LOOK right" judgements a
  screenshot cannot settle. They need Jordan's eye for thirty seconds, not another agent's opinion.

## HOW TO REPRODUCE ANY MEASUREMENT IN THIS FILE

```
cd X:\AI-2\becky-tools\native\becky-review && cmd /c _build.bat
set BECKY_REVIEW_FRAME_TRACE=trace.csv
set BECKY_REVIEW_EDIT_LOG=edit.log
set BECKY_REVIEW_REEL=X:/Videos/2025/11_November/Rendered/post_constantly.reel.json
becky-review.exe
```
`trace.csv` is one row per frame (`frame,tSec,deltaMs,stall`) with a trailing
`# total_frames=N stalls_over_100ms=N`. `edit.log` carries `peaksJobsEnqueued=` once per timeline
reload — the number that must stay FLAT across a split burst.
