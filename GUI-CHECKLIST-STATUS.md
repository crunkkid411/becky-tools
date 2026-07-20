> # ⚠ THIS FILE IS STALE — DO NOT TRUST IT
>
> Audited against main.cpp at ~4988 lines, BEFORE the night of 2026-07-19/20.
> The file is now ~6000 lines and much of what is marked ABSENT below is DONE.
>
> **Verified present by grep on 2026-07-20 04:50, despite being marked ABSENT here:**
> Up/Down zoom (`VK_UP`/`VK_DOWN`) · Redo (7 refs) · Skip Quiet (`g_thrOn` IS
> assigned) · ruler drag-to-pan (gesture `kind = 11`) · per-source clip colour
> (the engine sends `Color:`) · extend-by-one-frame buttons.
>
> This staleness already caused a real failure: an adversarial auditor read this
> file, saw those items marked ABSENT, and concluded the orchestrator had lied
> about fixing them. A stale audit is worse than no audit.
>
> A fresh re-audit is in progress and will REPLACE this file wholesale.

# GUI-CHECKLIST-STATUS.md — audit of `native\becky-review\main.cpp` against all 120 acceptance criteria

Audited 2026-07-20 against `X:\AI-2\becky-tools\native\becky-review\main.cpp` (4988 lines, read in full)
plus the engine it drives (`becky-go\cmd\clip\*.go`, `becky-go\internal\edl\*.go`,
`becky-go\internal\footage\discover.go`, `becky-go\cmd\becky-hits`, `becky-go\cmd\becky-judge`)
and `Open Forensic Hits.bat`. Read-only audit; no source file was modified.

## SUMMARY

| Status | Count |
|---|---|
| **DONE** | **69** |
| **PARTIAL** | **31** |
| **ABSENT** | **11** |
| **UNCHECKABLE** (needs a human eye on a running window) | **9** |
| **Total** | **120** |

### Two findings that change the picture

1. **The engine never sends a clip colour.** `ClipView` (`becky-go/cmd/clip/app.go:565-579`) has
   no `Color` field and `timelineLocked()` (`app.go:945-965`) never sets one. `main.cpp:1798-1802`
   parses `"color"` but always misses, so **every clip on the timeline renders in the single default
   `#00AEEF` blue** (`main.cpp:1255`). The painting side is finished and correct; the data is missing.
   This is items **32, 33, 36, 37, 109** — five checklist items, all of them tagged `[COLOR]`
   (accessibility aid).
2. **The playback-threshold feature is unreachable dead code.** `g_thrOn` is declared `false`
   (`main.cpp:1691`) and is **never assigned anywhere in the file** — no button, no key, no menu.
   The bar drawing, the drag gesture (kind 7), the dimming, and the seamless playback skip all exist
   and look correct, but nothing can ever turn them on. This is items **15, 16, 17** — the feature
   Jordan called "the single biggest breakthrough if done correctly".

---

## THE 120

### LAYOUT

| # | Requirement | Status | Evidence |
|---|---|---|---|
| 1 | Timecode ruler with tick marks | **DONE** | `main.cpp:2969-2981` — major ticks + `fmtTime` labels + 4 minor ticks per step |
| 2 | Thumbnail = small FIXED icon at clip's left, not zoom-scaled | **DONE** | `main.cpp:2652` `thumbH = laneH > 70 ? 40.0f : 0.0f` (fixed); drawn at `x0+3` `main.cpp:3041-3047` |
| 3 | Thumbnail inset so it does not cover the waveform | **DONE** | `main.cpp:2653-2654` `headerH = max(labelH, thumbH+4); wy0 = aY+2+headerH` — waveform band starts below the thumb |
| 4 | Clips 2× taller; toolbar/ruler/thumbnails do NOT change size | **PARTIAL** | Fixed sizes confirmed (`rulerH = 22` `main.cpp:2555`, `thumbH = 40` `:2652`). But lane height is just the residual of `timelineH = max(180, availH*0.26)` (`:4411`) — no explicit doubling; whether it reads as "2× what it was" needs an eye |
| 5 | Audio waveform inside the clip body | **DONE** | `main.cpp:3012-3014` `drawWave(...)` per clip; renderer `:2179-2219` |
| 6 | Toolbar buttons never shift; "render selection (N)" grayed not hidden | **PARTIAL** | Grayed-not-hidden is right: `main.cpp:4671-4690` `BeginDisabled/EndDisabled`, label always drawn. **But buttons DO shift**: `"Play##play"` vs `"Pause##play"` (`:4631`) and the 3-state overlay label `"Overlay: Off" / "Overlay: On (hidden)" / "Overlay: On (shown)"` (`:4643-4646`) are different widths, so every `SameLine` button to their right moves on every click |
| 7 | Remove "previous frame"/"next frame" buttons (keep shortcuts) | **DONE** | No such buttons exist. Searched `main.cpp` for `prev`, `next`, `frame`, `<<`, `>>` — only `"|<<"` (`:4633`), which is go-to-start, not step-a-frame. Frame stepping survives on Left/Right (`:4079-4098`) |
| 8 | Two distinct left/right arrow buttons that extend the selected clip by one frame | **ABSENT** | Searched `main.cpp` for `extend`, `Extend`, `"<"`, `">"`, `ImGui::ArrowButton`, and every `ImGui::Button(` call site — no such control. Trim exists only as `I`/`O` keys (`:3969-4013`) and edge drags (gesture kinds 4/5) |
| 9 | Playhead all black, top portion significantly larger, covers the gray ruler | **DONE** | `main.cpp:2150` `COL_PLAYHEAD = IM_COL32(0,0,0,255)`; shaft `:3130`; head spans `p.y+1 → p.y+20` vs `rulerH = 22` (`:3131-3133`) |
| 10 | The white part contains two tiny vertical hashmarks | **DONE** | `main.cpp:3135-3136` — two `AddLine` at `px±2.5f` in `COL_PHGRIP` (`:2152`) inside the `COL_PHFLAG` head |
| 11 | Second playhead "stock" — identical black bar, no moving white head | **DONE** | `main.cpp:3120-3126` — `AddLine(..., COL_PLAYHEAD, 2.0f)` with no flag/triangle; state `g_stockSec` `:1686` |
| 12 | Screenshot button saving `Screenshot_0001`, incrementing | **PARTIAL** | Button exists (`main.cpp:4693-4707` → `grab_frame`), but the engine names the file `<sourcestem>_<t>s.png` (`becky-go/cmd/clip/export.go:290`), not `Screenshot_NNNN` with an incrementing counter |
| 13 | becky-clip button on the timeline | **ABSENT** | Searched `main.cpp` for `becky-clip`, `beckyclip`, `clip.exe` — the engine binary is launched as `becky-review-engine.exe`/`clip.exe` at boot (`:119-120`) but there is no user-facing becky-clip button anywhere in the timeline or the control rows |
| 14 | Non-intrusive semi-transparent loading bar for anything >1s | **DONE** | `main.cpp:4890-4935` — `SetNextWindowBgAlpha(0.62f)`, `NoInputs|NoNav|NoFocusOnAppearing`, gate `nowSec() - since > 1.0` (`:4904`); counted `beginWork/endWork` `:861-871` |
| 15 | Playback-threshold toggle button, running-person icon | **ABSENT** | `g_thrOn` (`main.cpp:1691`) is never assigned anywhere in the file — grepped every occurrence: `:1691, :1745, :1750, :2702, :2962, :3104, :4282`, all **reads**. No toggle control of any kind, no icon |
| 16 | Threshold ON → horizontal bar across all clips, draggable up/down | **PARTIAL** | Code is complete and looks right — bar `main.cpp:3110-3117`, hit test `:2701-2703`, drag gesture kind 7 `:2806-2811` + `:2861-2863` — but all of it is behind `if (g_thrOn)`, which can never be true (see #15) |
| 17 | Sections below the threshold bar visually dimmed | **PARTIAL** | `main.cpp:3105-3109` `COL_QUIETDIM` over `g_quietRanges`; same unreachable gate as #16 |
| 18 | Left panel: date-created (newest, default), name (Z-A), "most relevant" | **PARTIAL** | One cycling `SmallButton` through 4 modes (`main.cpp:4479-4480`, `sortLibrary` `:3364-3374`). Newest-first default ✓ (`g_sortMode = 0`, `:3207`), Name Z-A ✓ (case 3). **No "most relevant" mode**, and it is one control, not three |
| 19 | "Most relevant" = crown icon button, no text, blue like render-selection | **ABSENT** | Searched `main.cpp` for `crown`, `relevan`, `score` in any UI context — the only `score` is `Hit::score` (`:3293`), never sorted on or surfaced |
| 20 | qmd toggle switch so single-word search still available | **PARTIAL** | Both modes reachable — `"Search"` (`main.cpp:4424`) and `"Smart (qmd)"` (`:4426`) — but they are two separate action buttons, not a persistent toggle switch; the mode is not remembered between searches |
| 21 | Un-indexed results show an "index" icon like the "plus" (transcribe) button | **PARTIAL** | Un-indexed state IS shown, as text: `"  [no transcript]"` (`main.cpp:4497`), and transcription is reachable via a right-click menu item (`:4514`). There is no icon button on the row |
| 22 | After "back", the just-viewed transcript gets a green outline | **DONE** | `main.cpp:4503-4505` — `AddRect(..., IM_COL32(0x14,0xFF,0x39,255))` on `g_libJustViewedIdx`, set at `:4501` and `:4530` |
| 23 | Single video's transcript minimal — filename not repeated per segment | **DONE** | `main.cpp:4472` `line = c.timecode + "  " + c.text` — no name field; the filename appears once, in the header (`:4464`) |
| 24 | Transcript flows as ONE continuous word-processor document | **PARTIAL** | `main.cpp:4470-4475` renders one `ImGui::Selectable` per cue inside a child window — a row list with `ItemSpacing(9,7)` gaps (`:3772`), one line per cue, no text wrapping/reflow. Closer to the old boxed layout than to audapolis |
| 25 | Review "?" questions in the right panel, each clickable to type + submit an answer | **PARTIAL** | Cards render in the right panel (`main.cpp:4771-4813`) with colour, question, `[answered]` state and the existing answer. **There is no per-card answer input or submit** — the engine's `SaveAnswer` verb (`becky-go/cmd/clip/bridge.go:173`) is never called from `main.cpp` |
| 26 | Overlay order: Date, Timecode, filename | **DONE** | `main.cpp:1393-1414` `overlayLines()` — pushes Date, then `ORIG TC`, then the filename/person/location field, then Link |
| 27 | Overlay renders `ORIG TC 01:32:34:02` (space after TC) | **DONE** | `main.cpp:1398` `lines.push_back("ORIG TC " + secondsToTimecode(...))` |
| 28 | Overlay Date includes the timezone, e.g. `Date: 2026-03-02 UTC` | **DONE** | `main.cpp:1396` `lines.push_back("Date: " + c.date + " UTC")` |
| 29 | Overlay button is a 3-state toggle, default = ON but not previewed | **DONE** | `main.cpp:1299` `static int g_ovMode = 1;` (default = on-but-hidden); cycle `:4646` `setOverlayMode((g_ovMode+1)%3)`; `setOverlayMode` keeps the engine's `enabled` in sync `:1451-1468` |
| 30 | "Load reel" window lists .json files at the TOP | **PARTIAL** | `main.cpp:3451-3456` — the DEFAULT filter is `*.txt;*.xml;*.json`, so reels are visible immediately and videos are filtered out entirely. But `.json` is 4th in the filter dropdown, and nothing forces .json to sort first in the listing |
| 31 | Timeline thumbnails written to a `timeline_thumbnails` subfolder | **DONE** | `becky-go/cmd/clip/export.go:503` `dir := filepath.Join(base, "timeline_thumbnails")`; regression test `export_dest_test.go:46-62` asserts it lands under `RenderSubdir` |

### READABILITY / COLOR

| # | Requirement | Status | Evidence |
|---|---|---|---|
| 32 | Exact 8-colour palette, in that order, for every multi-colour assignment | **PARTIAL** | The palette is exact and in order — `main.cpp:3350-3354` `kPalette[8]` = `14FF39, 00AEEF, DC143C, 8A2BE2, FF57D1, FFD700, 16F0EA, FF8C00` — but it is used **only for Q&A cards** (`cardColorFor` `:3355-3360`). The timeline never uses it (see #37) |
| 33 | The ENTIRE clip body takes the source-video colour, not a top stripe | **PARTIAL** | The mechanism is correct — `main.cpp:3008-3010` fills the whole clip rect with `IM_COL32(c.r,c.g,c.b, selected?255:62)`, no stripe. But `c.r/g/b` is the hardcoded default `0,174,239` for every clip (`:1255`) because the engine sends no colour (see the summary finding) |
| 34 | Trim handles and borders match the clip colour, not green | **DONE** | `main.cpp:3017` `brd = IM_COL32(c.r,c.g,c.b,242)`; handles `:3058` `hcol = IM_COL32(c.r,c.g,c.b, selected?255:150)` |
| 35 | Selected clip fully opaque; unselected transparent; no outline | **DONE** | `main.cpp:3008` alpha `255` when selected, `62` when not |
| 36 | Colour persistent per video for the rest of the project | **ABSENT** | No colour is assigned anywhere. Searched `becky-go` for `json:"color`, `Color`, `palette` in `internal/edl/edl.go` and `cmd/clip/app.go` — `edl.Clip` (`edl.go:31-38`) and `ClipView` (`app.go:565-579`) have no colour field |
| 37 | Clips from different source videos colour-coded so same-source is obvious | **ABSENT** | Same root cause as #36 — every clip is `#00AEEF`. The only two clips that ever differ are the local-only previews `seekToSpan` (`main.cpp:3523`) and `addHitToTimeline`'s degrade path (`:3692`), both hardcoded `220,30,60` |
| 38 | Overlay preview identical size to the render, or no preview at all | **PARTIAL** | The DEFAULT satisfies the "or" branch — mode 1 = on-but-not-previewed (`main.cpp:1299`), render unaffected. But mode 2 exists and its ASS is pushed with `res_x=res_y=0` and a fixed `\fs28` (`:1439-1442`), i.e. OSD-relative sizing that is not derived from the render's `drawtext` size — so mode 2 is not proven identical. Also: there is no separate "name" toggle button in this app (`g_overlay.showFilename` is engine-mirrored only, `:1823`) |
| 39 | Never strip/flatten/monochrome the UI "for accessibility" | **DONE** | High-contrast palette present throughout (`main.cpp:2141-2165`, `:3350-3354`), colour-coded status text (`:4385-4386`, `:4761-4763`), amber caption lane (`:2159-2163`) |
| 40 | Mouse cursor over the timeline is an I-beam, not a hand | **DONE** | `main.cpp:2582` `if (hovered) ImGui::SetMouseCursor(ImGuiMouseCursor_TextInput);` |

### INTERACTION

| # | Requirement | Status | Evidence |
|---|---|---|---|
| 41 | Ctrl+Z undo, Ctrl+Shift+Z redo, each with a visible button | **PARTIAL** | Ctrl+Z works and is debounced (`main.cpp:4015-4045`). **No redo path and no buttons** — searched `main.cpp` for `redo`/`Redo`: zero hits. The engine already implements it (`becky-go/cmd/clip/bridge.go:155-156`, `app.go:1035-1036`) |
| 42 | Undo undoes the IMMEDIATE last action | **UNCHECKABLE** | Ordering is right by construction (FIFO `editWorker` `main.cpp:398-457`, engine `pushUndoLocked` per mutation `app.go:1000-1008`), and two real bugs here were fixed (`:412-436`, `:4029-4042`). Settling it needs: split a clip, Ctrl+Z, confirm the clip un-splits rather than moving |
| 43 | Mouse wheel = zoom the timeline | **DONE** | `main.cpp:2669-2674` `applyWheel`: `else { zoomTo(g_pps * pow(1.15, notches), ...) }` |
| 44 | Ctrl + wheel = scroll the timeline sideways | **DONE** | `main.cpp:2671` `if (ctrl) { g_scrollSec = max(0, g_scrollSec + (-notches*100.0)/g_pps); }` |
| 45 | Wheel zoom centers on the playhead | **DONE** | `main.cpp:2664-2668` `zoomAnchorX()` returns the playhead x when on screen, mouse/centre only as fallback |
| 46 | Scroll-to-zoom works at ALL times | **DONE** | `main.cpp:2674` gated only on `hovered` (the timeline `InvisibleButton`), not on focus or panel state; range clamp `:2660` `min(2000, max(0.5, ...))` still permits zoom-in at full zoom-out |
| 47 | Zooming out reveals empty space right of the last clip | **DONE** | `main.cpp:2690` `maxScroll = max(0.0, g_compDur - viewDur*0.15)` — the end of the reel is not a wall; up to 85% of the view can be past it |
| 48 | Up arrow = zoom in, Down arrow = zoom out | **ABSENT** | Searched `main.cpp` for `VK_UP`, `VK_DOWN`, `VK_PRIOR`, `VK_NEXT` — **zero hits**. `ImGuiKey_UpArrow`/`DownArrow` are used only for library list navigation (`:4487-4488`) |
| 49 | Left / Right move the playhead one frame | **DONE** | `main.cpp:4083-4088` and `:4094-4097` — `1.0/sourceFps(c->source)`, per-clip fps (`:1334-1354`) |
| 50 | Ctrl+Left / Ctrl+Right to clip start/end, travelling the WHOLE timeline | **DONE** | `main.cpp:2519-2546` `collectBoundaries` (both tracks + 0 + `g_compDur`, deduped, one shared list for both directions) + `:4080-4082` / `:4092-4093`. Modifier read via `GetAsyncKeyState` on the same clock as the arrows (`:4077-4078`) — the actual root cause, documented `:4046-4076` |
| 51 | Middle-click + drag pans the timeline without rearranging clips | **DONE** | `main.cpp:2676-2680` — adjusts `g_scrollSec` only |
| 52 | Click + drag on the gray ruler pans the timeline sideways | **ABSENT** | A press anywhere with `y < aY` (the ruler band) falls through `clipHit`/`capHit` into the else branch at `main.cpp:2793-2799`, which sets `g_gest.kind = 1` — a **scrub**, not a pan. No `y < aY` special case exists anywhere in `drawTimeline` |
| 53 | Clicking the ruler moves the playhead AND the secondary stock | **PARTIAL** | Playhead: yes, `main.cpp:2795` `curSec = min(xToSec(mx), g_compDur)`. **Stock: no** — `g_stockSec` is only assigned in the clip-click branch (`:2883`) and on pause (`:3907`); the ruler branch never touches it |
| 54 | Ctrl+click multi-select; Shift+click selects the range | **DONE** | `main.cpp:2866-2868` (ctrl toggle + anchor) and `:2869-2877` (shift range from `g_selAnchor`) |
| 55 | Dragging one of several selected clips moves them all | **DONE** | Group collected on press `main.cpp:2783-2786`; drag kind 3 `:2812-2814`; group reorder `:2885-2915` via `reorder_many` |
| 56 | Button to render just the selected clips | **DONE** | `main.cpp:4671-4689` → `export_selection` |
| 57 | During playback, clicking inside a clip sets the return position, playhead unmoved | **DONE** | `main.cpp:2882-2883` `if (!g_playingExt) { curSec = t; ... } else { g_stockSec = t; g_stockFlash = true; }` |
| 58 | Once the return position is used, stop auto-scrolling until a manual scroll | **DONE** | `main.cpp:2686` auto-follow requires `g_stockSec < 0` (and `nowSec() - g_lastUserScroll > 1.5`) |
| 59 | Pause returns the playhead to where playback STARTED; Enter stops it where it is | **PARTIAL** | Pause returns to the **stock** if one was set (`main.cpp:3907`), but nothing records where playback started, so with no stock the playhead just stops wherever mpv left it. **Enter is unhandled** — searched for `VK_RETURN` in the timeline key block: zero hits |
| 60 | Moved stock slowly flashes black/white at text-cursor speed | **DONE** | `main.cpp:3123` `wht = g_stockFlash && fmod(nowSec(), 0.8) >= 0.4` — 0.8s period, 50% duty |
| 61 | Clicking to move the stock also selects that clip, without interrupting playback | **DONE** | `main.cpp:2878-2884` — `g_sel.clear(); g_sel.insert(c.id); emitSelect();` runs first, then the playing branch sets the stock; `playing` is never touched |
| 62 | Cut/split during playback applies at the stock and does not pause playback | **DONE** | `main.cpp:3900` `editT() = (playing && g_stockSec >= 0) ? g_stockSec : curSec`, used by S/Del/O/I (`:3933, :3954, :3976, :3998`); none of them set `playing = false`. Mid-playback edits rebuild the EDL and resume at `curSec` (`:4245`) |
| 63 | After a split, the clip AFTER the playhead is the selected one | **DONE** | `main.cpp:4199-4200` selects `new_id`; the engine's `Split` gives the RIGHT half the new id and the left half keeps the old one (`becky-go/cmd/clip/app.go:862-871`) |
| 64 | After a ripple edit the playhead moves with the ripple | **DONE** | `main.cpp:1500-1502` `rippleCurSec`, applied for remove/trim-out/trim-in at `:4205, :4209, :4213` |
| 65 | 2x playback speed button + Shift+Spacebar | **DONE** | Button `main.cpp:4636`; Shift+Space `:3902-3904`; fed to mpv as `speed` `:4246-4249` |
| 66 | Esc must ALSO delete the selected clip | **DONE** | `main.cpp:3953` `if ((GetAsyncKeyState(VK_DELETE) & 1) \|\| (GetAsyncKeyState(VK_ESCAPE) & 1))` — one shared path |
| 67 | Right-click clip → Open in File Browser / Copy File Name (video, with ext) / Open transcript at that clip's timecode | **PARTIAL** | Menu exists `main.cpp:2758-2767`. Open in File Browser ✓ (`:2763`), Copy File Name ✓ — `baseName(c.source)`, the video with its extension (`:2764`). **"Open Transcript" does not jump to the clip's timecode** — `openTranscript(c.source)` (`:3541-3562`) loads the cue list from the top; nothing scrolls to `c.in` |
| 68 | Right-click a QUOTE or a video in the left panel → Open in File Browser / Copy File Name | **PARTIAL** | Video rows: yes, `main.cpp:4506-4517`. **Search-hit (quote) rows have no context menu at all** — the hits loop `:4444-4459` has no `BeginPopupContextItem` |
| 69 | "Open in file browser" always SELECTS the relevant file | **DONE** | `main.cpp:3430-3433` `explorer.exe /select,"<path>"` |
| 70 | Double-clicking "render selection"/"export" opens the folder with the new .mp4 selected; single-click unchanged | **PARTIAL** | The folder IS opened with the mp4 selected — but on **single** click (`main.cpp:4666`, `:4686`, and `:4730` for EDL). No double-click detection on those buttons, and single-click behaviour was changed rather than left alone |
| 71 | Drag footage onto the timeline from any folder | **DONE** | `DragAcceptFiles` `main.cpp:3730`; `WM_DROPFILES` → SEH-guarded shell parse `:2099-2136`; drained + inserted at the drop position `:2597-2641` → `requestAddExternal` `:3272-3287` (background thread) |
| 72 | The drag thumbnail must not obstruct the drop target | **ABSENT** | Searched `main.cpp` for `IDropTarget`, `RegisterDragDrop`, `IDropTargetHelper`, `DragImage`, `SHDoDragDrop` — none. The app uses `DragAcceptFiles`/`WM_DROPFILES` only, which gives no control over the shell's drag image |
| 73 | Up/Down navigate the left panel FROM the current selection | **DONE** | `main.cpp:4487-4488` — `g_libSel + 1` / `g_libSel - 1`, not from index 0; `g_libScrollPending` scrolls it into view `:4518` |
| 74 | ONE left-panel selection shared by mouse and keyboard | **DONE** | `main.cpp:4494` `sel = (g_libSel == i)` and `:4500` a mouse click writes the same `g_libSel` the arrows move |
| 75 | Spacebar immediately after arrow-selecting plays that clip in the preview | **DONE** | `main.cpp:4531` `if (ImGui::IsKeyPressed(ImGuiKey_Space)) playWholeVideo(g_videos[g_libSel].path, ...)`, guarded on `!WantTextInput` `:4529` |
| 76 | Enter in the left panel = double-click (quote → timeline; video → transcript) | **PARTIAL** | Video → transcript ✓ `main.cpp:4530`. **Quote → timeline is missing** — the Enter handler lives inside the video-library `else` branch and is not reachable while search hits are displayed (`:4436-4460`); hits respond only to mouse click/double-click `:4452-4454` |
| 77 | After "back", the video list returns to the same scroll position | **UNCHECKABLE** | Back only clears `g_cueName`/`g_cues` (`main.cpp:4463`); the `BeginChild("videos")` scroll state is retained by ImGui across frames where the child is not submitted, so it plausibly works — but nothing in the code explicitly restores it. Settle it by scrolling to the bottom of a long list, opening a video, pressing Back |
| 78 | Search WITHIN a specific transcript after opening it | **DONE** | `main.cpp:4465` `InputTextWithHint("##within", "search within this transcript", ...)`; filter `:4471` |
| 79 | Clicking the timeline after typing returns keyboard focus to the timeline | **DONE** | `main.cpp:4953-4956` — a left/right click anywhere in the timeline panel calls `ImGui::SetWindowFocus()`, which deactivates the InputText and drops `WantCaptureKeyboard` next frame |
| 80 | `clips_ORIGINAL_NNNN.mp4` / `clips_compilation_NNNN.mp4`, incrementing from 0001 | **DONE** | `becky-go/cmd/clip/export.go:560-576` (`clips_<slug>` vs `clips_compilation`) + `:142-143` (next-free `_NNNN`) |
| 81 | Forensic-hits must not overwrite `_forensic_hits.json` | **DONE** | `becky-go/cmd/becky-judge/main.go:341-355` `nextForensicHitsPath` → `_forensic_hits1.json`, `_forensic_hits2.json`, … up to 9999 |
| 82 | New hits file while a session is open → ask to save/export, or open a second instance | **ABSENT** | Searched `main.cpp` for a file watcher, a second-instance launch, or a save prompt: `FindFirstChangeNotification`, `ReadDirectoryChanges`, `CreateProcess` (only for mpv `:743`, the engine `:139`, and becky-otio `:3503`), `MessageBox`. `BECKY_REVIEW_REEL` is read once at boot (`:3795`) and never re-checked |
| 83 | Exported EDL files include an audio track | **DONE** | `becky-go/internal/edl/cmx3600.go:61-66` — every event line uses channel `AA/V` (video + 2-ch audio), with the comment naming the exact Vegas-no-audio bug |
| 84 | "Open Forensic Hits.bat" also produces a Vegas-compatible .edl | **DONE** | `becky-go/cmd/becky-hits/main.go:145-151` writes a CMX3600 `.edl` sidecar beside the reel; the .bat invokes `becky-hits.exe --hits … --out "%REEL%"` (`Open Forensic Hits.bat:43`) |

### PERFORMANCE

| # | Requirement | Status | Evidence |
|---|---|---|---|
| 85 | 20 rapid splits → zero "preparing" states, zero dropped frames | **UNCHECKABLE** | Mechanism is in place: peaks dedup against completed windows, queued jobs AND the in-flight window (`main.cpp:1208-1231`), counter `g_peaksJobsEnqueued` `:1233`, per-reload log `:4193-4194`. Settle it by running with `BECKY_REVIEW_EDIT_LOG` set, splitting 20× on a warm reel, and grepping that the counter stops climbing while no clip shows the `"preparing..."` hatch (`:3019-3035`) |
| 86 | Adding a clip whose source is already on the timeline causes no measurable lag | **PARTIAL** | `add_clip` is measured (`main.cpp:3684-3686`, logged to crash.log) and does no proxy work engine-side — but `addHitToTimeline` calls `engineCall(...)` **synchronously on the UI thread** with a 6s timeout (`:3685`), unlike every other edit, which goes through `editWorker` |
| 87 | Background media work never stutters the system cursor: below-normal priority, capped pool | **DONE** | `main.cpp:1038-1039` `THREAD_MODE_BACKGROUND_BEGIN` (drops I/O + memory priority, not just CPU), fallback `BELOW_NORMAL`; pool cap `:1107-1109` `g_decActive < (g_busyHint ? 1 : 2)`; `g_busyHint` set from playing/gesture `:3865` |
| 88 | 5-minute scripted stress drive → no UI-thread stall > 100 ms | **UNCHECKABLE** | The measuring harness exists — `frameTraceTick` writes per-frame `deltaMs` + a `stall` flag and a total (`main.cpp:341-353`, `:4975-4978`) — but the RESULT is not in the code. Run with `BECKY_REVIEW_FRAME_TRACE=<path>` for 5 minutes and read the trailing `stalls_over_100ms=` line |
| 89 | No ~20-second full stall under rapid split/click bursts | **UNCHECKABLE** | Same harness as #88. Note the two known former causes are fixed: the 64KB reply-buffer bug (`main.cpp:150-161`) and the blocking mpv IPC write (`:538-577`) |
| 90 | No ~2 seconds of system-wide lag on every new clip added | **UNCHECKABLE** | Same harness. See #86 for the one remaining UI-thread block on the add path |
| 91 | Caches keyed by SOURCE FILE + resolution level, never by clip | **DONE** | `main.cpp:838` `std::map<std::string, shared_ptr<Peaks>> g_peaks` keyed by source path; three resolution levels `n0/x0, n1/x1, n2/x2` (`:917-918`, selected by span `:2200-2208`); thumbs keyed source+time `:1983-1986`; on-disk cache keyed source+size+mtime `:902-912` |
| 92 | The timeline keeps playing while a new segment is proxied, including ready clips | **DONE** | Playback is a single mpv EDL over the whole reel (`main.cpp:1636-1660`, `:4232-4250`); peaks decode runs on detached background threads (`:1170`) and nothing on the playback path waits on them |
| 93 | Adding a single quote must not block on proxy generation | **PARTIAL** | No proxy generation exists at all, so nothing waits on one. But the add itself is a synchronous UI-thread engine call (`main.cpp:3685`) — same defect as #86 |
| 94 | "Save" must not freeze the program | **PARTIAL** | `"Save Reel"` calls `engineCall("save_reel", …, 20.0)` **on the UI thread** (`main.cpp:4709-4713`). Same pattern for `"Render"` (300 s timeout, `:4659`), `"Load Reel"` (30 s, `:4720`), `"Export EDL"` (30 s, `:4729`), `"Screenshot"` (20 s, `:4699`), `openTranscript` (25 s, `:3545`), `reorder`/`set_trim` (4 s, `:2911-2924`). Each is a hard window freeze for its duration |
| 95 | Adjusting clip lengths / making cuts must not make clips or waveforms flash | **UNCHECKABLE** | Trim drag draws a ghost from gesture state without touching the model (`main.cpp:2994-2998`), and peaks survive reloads via the fill short-circuit (`:1208`). But `clipPreparing()` (`:2220-2231`) paints a hatch + `"preparing..."` over any clip with an unfilled second, which is the most likely flash source. Settle it by trimming a warm clip repeatedly and watching |
| 96 | Deleting clips must not move the timeline view | **PARTIAL** | The delete path never assigns `g_scrollSec` — but `main.cpp:2690-2691` `maxScroll = max(0, g_compDur - viewDur*0.15); g_scrollSec = min(g_scrollSec, maxScroll);` runs every frame, so a delete that shrinks `g_compDur` can silently drag the view left |
| 97 | Timeline stays responsive past ~3 minutes of clips | **UNCHECKABLE** | Needs a real reel and the frame trace (#88) |
| 98 | Timeline responsiveness is a correctness requirement | **UNCHECKABLE** | Meta-criterion. The concrete mechanisms are real: waitable swap chain + `SetMaximumFrameLatency(1)` + wait-at-top-of-frame (`main.cpp:1877-1935`, `:3857`), edits/search off the UI thread (`:398`, `:3620`). The remaining contradiction is #94's synchronous button handlers |
| 99 | Waveforms stream in progressively (coarse then refined) with a placeholder for missing data | **PARTIAL** | Progressive-by-window and never-waited-on ✓ — `secFilled` windows fill over time, `g_fillEpoch` triggers redraw (`main.cpp:1031`), missing bins are simply skipped (`:2209`) and re-requested at most once a second (`:2215-2218`). **Not coarse-then-refined**: the 3-level pyramid is a zoom LOD built from full-resolution data (`:922-936`), not a fast low-quality first pass. The placeholder is a whole-clip hatch overlay (`:3019-3035`), not per-region |
| 100 | No embedded browser engine — native pixels only | **DONE** | Dear ImGui + D3D11 + mpv `--wid` child hwnd. Searched `main.cpp` for `WebView`, `CEF`, `Chromium`, `Edge`, `IWebBrowser`: zero hits |

### MUST-NEVER-DO

| # | Requirement (his words) | Status | Evidence |
|---|---|---|---|
| 101 | "remove the yellow outline around the selected clip" | **DONE** | `main.cpp:3008-3018` — selection is opaque fill, border is the clip's own colour. `COL_DROPMARK` (the only yellow, `:2153`) is declared and never used anywhere in the file |
| 102 | "'split clip' popup should not exist when user makes a cut" | **DONE** | The split path is keypress → `queueEdit` → drain (`main.cpp:3932-3947`, `:4197-4201`) with no UI. The only two popups in the file are `"clipctx"` (`:2756`) and `"rowctx"` (`:4506`), both right-click menus |
| 103 | "all the little popups on the timeline need to go away; 'Removed 1 clip', 'Reordered clip', 'Undo', 'Redo'" | **DONE** | Searched `main.cpp` for `Removed`, `Reordered`, and every `g_renderMsg =` assignment — messages exist only for load/render/save/transcribe/drop outcomes, shown as one dim status line in the video pane (`:4734`), never on the timeline, never for remove/reorder/undo |
| 104 | "'render' folder should NOT appear in search or browse" | **DONE** | `becky-go/internal/footage/discover.go:74-91` `excludedWalkDirs{"render": true}` + `skipExcludedDir`, applied by every `WalkDir` in the package |
| 105 | "when a user clicks within a clip, the playhead should not be affected" | **PARTIAL** | Honoured during playback (`main.cpp:2883` sets the stock, not `curSec`). **Violated when paused** — `:2882` `if (!g_playingExt) { curSec = t; emitScrub(curSec, true); }` moves the playhead to the click |
| 106 | "deleting clips should NOT move the timeline, only the clips affected by the ripple edit" | **PARTIAL** | Same as #96 — `main.cpp:2690-2691` |
| 107 | "ruler click should move the Playhead (and secondary playhead stock), but NOT change the selected clip" | **PARTIAL** | Selection is correctly untouched (the `kind = 1` branch `main.cpp:2793-2799` never writes `g_sel`), and the playhead moves. The **stock is never moved** — same as #53 |
| 108 | "ensure trim handles and borders are not green" | **DONE** | `main.cpp:3017`, `:3058` — both derived from the clip's own `c.r/g/b` |
| 109 | "clips from video 3 must remain `#DC143C` for the rest of the project" | **ABSENT** | No per-source colour exists to persist — see #36/#37 and the summary finding |
| 110 | "buttons on the timeline toolbar should not move" | **PARTIAL** | Same as #6 — `main.cpp:4631` (Play/Pause) and `:4643-4646` (3-state overlay label) change width and shift everything after them |
| 111 | "remove 'previous frame' and 'next frame' buttons" | **DONE** | Same as #7 — they do not exist |
| 112 | "no reason to have the filename listed for every segment" | **DONE** | Same as #23 — `main.cpp:4472` |
| 113 | "it should not automatically close my session" | **DONE** | Searched `main.cpp` for `PostQuitMessage` (only in `WM_DESTROY`, `:2118`), `exit(`, `ExitProcess`, `run = false` (only on `WM_QUIT`, `:3859`). Nothing closes or resets the session on its own |
| 114 | "it should add digits to the end" rather than overwrite | **DONE** | Renders `becky-go/cmd/clip/export.go:142-143`; hit-lists `becky-go/cmd/becky-judge/main.go:341-355` |
| 115 | "overlay preview still looks like shit … make it identical, or do not preview" | **PARTIAL** | Same as #38 — default is "do not preview", but the opt-in preview mode's size is not derived from the render |
| 116 | "thumbnail preview images are littering the 'render' folder" | **DONE** | Same as #31 — `becky-go/cmd/clip/export.go:503` |
| 117 | "ctrl + back/forward no longer navigate beyond the clip" — **CRITICAL** | **DONE** | Same as #50 — `main.cpp:2519-2546` + the modifier-clock root cause fix `:4046-4078` |
| 118 | "Do NOT strip color … for accessibility" | **DONE** | Same as #39 |
| 119 | "No embedded browser engines, ever" | **DONE** | Same as #100 |
| 120 | "the 'hand' mouse icon should be replaced with the 'Ibeam'" | **DONE** | Same as #40 — `main.cpp:2582` |

---

## THE TOP 10 TO DO NEXT

Ranked by value to Jordan specifically: a professional editor, vision-impaired, mouse + keyboard only,
whose bar is "as snappy as Vegas Pro timeline or faster" and for whom colour is an accessibility aid.

### 1. Give clips their source-video colour  — items 32, 33, 36, 37, 109
**Why it matters:** right now every clip on his timeline is the identical blue. He identifies which
video a clip came from by its colour at a glance; without it he has to read a tiny filename label on
every clip, which for him is physically expensive. Five checklist items, one fix.
**Where:** add `Color string \`json:"color"\`` to `ClipView` (`becky-go/cmd/clip/app.go:565-579`) and
assign it in `timelineLocked()` (`app.go:945-965`) from a `map[string]string` of source→hex that only
ever grows, using the exact 8-colour palette in order. `main.cpp` needs no change — `loadTimelineView`
already parses `"color"` at `:1798-1802` and paints the whole body at `:3008`.

### 2. Get the blocking engine calls off the UI thread — items 94, 86, 93, 98
**Why it matters:** pressing Render freezes his entire window for the whole render (300s timeout);
Save Reel, Load Reel, Export EDL, Screenshot and opening a transcript each freeze it too. This is the
exact "FROZE when i tried touching it" complaint, still live in the button handlers.
**Where:** `main.cpp:4658-4733` (the Render / Render Selection / Screenshot / Save Reel / Load Reel /
Export EDL handlers), `:3685` (`addHitToTimeline`), `:3545` (`openTranscript`). The pattern to copy is
already in the file — `requestTranscribe` (`:3240-3262`) / `requestAddExternal` (`:3272-3287`): a
detached thread, a done-deque under a small mutex, drained once per frame in the main loop. Wrap each
in `beginWork()/endWork()` so the >1s indicator (`:4890`) covers them.

### 3. Turn the playback threshold on — items 15, 16, 17
**Why it matters:** he called this "the single biggest breakthrough if done correctly", and the entire
feature is built and unreachable. Skipping silence during review is the single biggest time saver in a
2-hour session.
**Where:** `g_thrOn` (`main.cpp:1691`) needs a control. Add a toggle button in the video-pane control
row next to "2x" (`main.cpp:4636`) or, better, a dedicated timeline-toolbar row, that flips `g_thrOn`,
sets `g_quietDirty = true`, and calls `emitThreshold(true)` (`:1741`). The bar, drag, dim and the
seamless playback skip (`:4282`) then all light up as written.

### 4. Ruler = pan on drag, playhead + stock on click — items 52, 53, 107
**Why it matters:** the ruler is the natural place a Vegas editor grabs to move around, and right now
grabbing it scrubs instead. Three checklist items, one small branch.
**Where:** `main.cpp:2770-2800` — the `if (pressed)` block. Add a `y < aY` (ruler band) case before the
final `else`: on press, set `curSec` **and** `g_stockSec`; introduce a new gesture kind that adjusts
`g_scrollSec` by `io.MouseDelta.x / g_pps` on drag (copy the middle-drag body at `:2678-2679`) and
sets `g_lastUserScroll`.

### 5. Up/Down arrow = zoom in/out — item 48
**Why it matters:** the cheapest item on this list, and it is keyboard zoom for someone who works
mouse+keyboard and pays a physical cost for precise mouse work. Zero hits for `VK_UP`/`VK_DOWN` today.
**Where:** `main.cpp:4079` — alongside the `VK_LEFT`/`VK_RIGHT` handlers. The zoom math currently lives
inside `drawTimeline`'s `zoomTo` lambda (`:2658-2662`); lift it to a file-scope helper taking
`(newPps, anchorSec)` so both the wheel path and the key path call one implementation.

### 6. Stop the toolbar buttons moving — items 6, 110
**Why it matters:** he stated this twice. Every Play/Pause press and every Overlay click shifts the
position of every button to its right, so his muscle memory lands on the wrong control — the precise
class of defect he described as "my muscle memory broke the entire goddamn thing".
**Where:** `main.cpp:4631` (`"Play##play"` / `"Pause##play"`) and `:4643-4646` (the three overlay
labels). Pass an explicit size — `ImGui::Button(label, ImVec2(fixedW, 0))` — sized to the widest label
in each set. `"Render Selection (%d)"` at `:4671` needs the same treatment.

### 7. Redo (Ctrl+Shift+Z) plus visible undo/redo buttons — item 41
**Why it matters:** an editor without redo edits defensively. The engine already implements it —
this is a missing keybinding and two buttons, nothing more.
**Where:** `main.cpp:4015-4045` — the Ctrl+Z block. Read Shift the same way Ctrl is read (`:4077-4078`,
both `0x8000` and the `& 1` latch) and queue `req.verb = "redo"` with the same debounce; the engine verb
is `becky-go/cmd/clip/bridge.go:155-156` (`app.go:1035-1036`). Put the two buttons in the control row
at `main.cpp:4631`.

### 8. Clicking inside a clip must not move the playhead when paused — item 105
**Why it matters:** he wrote it as a MUST-NEVER-DO. Selecting a clip to delete it currently also throws
away his playhead position, so he loses his place every time he selects.
**Where:** `main.cpp:2882` — `if (!g_playingExt) { curSec = t; emitScrub(curSec, true); }`. Selecting
should select; set the stock instead, or do nothing to the position at all. Check the interaction with
memory note "a timeline click navigates PAUSED, never auto-plays" before changing it — that note is
about not auto-playing, not about moving the playhead.

### 9. Deleting a clip must not drag the view — items 96, 106
**Why it matters:** stated twice. After every ripple delete the whole timeline can jump sideways under
his cursor, and re-finding his place after each cut is exactly the "2 hour session becomes 4 hours" tax.
**Where:** `main.cpp:2690-2691` — the unconditional `g_scrollSec = min(g_scrollSec, maxScroll)`. Clamp
only when the user is actually scrolling/zooming (or clamp against a `maxScroll` that keeps at least a
full view's worth of space past the end), not every frame after `g_compDur` shrinks.

### 10. Answer a Q&A card from the right panel — item 25
**Why it matters:** the whole point of the Q&A panel is that a forensic agent's questions reach him
where he already is, and he can answer them like sending a chat. Today the cards are read-only, so the
loop never closes and answers have to be routed some other way. The engine side already exists.
**Where:** `main.cpp:4782-4809` — inside the `if (open)` body of each card, add an `InputText` +
Submit that calls the engine's `SaveAnswer` verb (`becky-go/cmd/clip/bridge.go:173`,
`{"id", "question", "answer"}`) on a background thread, then `refreshCards()` (`main.cpp:3565`) on the
reply.
