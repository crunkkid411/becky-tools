# Becky Review — Checklist (supersedes GUI-CHECKLIST-STATUS.md)

**Why this file exists:** Jordan live-tested the app on 2026-07-23 and found
`GUI-CHECKLIST-STATUS.md` listed working features as missing and missed real
bugs entirely — it is stale and proven wrong. Do not trust it. This file
replaces it as the source of truth. It does NOT edit or delete that file
(another agent has it checked out).

**How this was built:** every distinct requirement in `user-feedback-log.md`
(13 dated entries, 2026-06-23 → 2026-07-20), deduplicated, with **newest wins**
when two entries conflict. Every status below is verified against the CURRENT
code (`native/becky-review/main.cpp`, `engine.cpp`, `becky-go/`) — a function
name + line number, or the grep that returned zero hits — never against a
status document. Read-only audit; no code was changed to produce this file.

**Audit date:** 2026-07-23.

---

## ARCHITECTURE GUARDRAIL (hard rule — read before touching anything below)

Becky Review 3 is a **native C++ / Win32 / Dear ImGui / D3D11** application
with an **in-process libavcodec + D3D11VA** video engine. It stays that way.
Jordan's own words: *"make sure we don't revert anything back to using web or
wpf or mpv or anything like that."*

**BANNED — never re-propose, never build toward:**
web / browser tech (WebView2, HTML/CSS/JS UI, CDP-driven UI, any embedded
browser) · WPF / C# / .NET / XAML · mpv (deleted 2026-07-23, replaced by the
in-process engine) · Shotcut / MLT · Gio · GStreamer · Flyleaf · LibVLCSharp ·
Media Foundation · the embedded-llama.dll-inside-the-program /
shared-state-built-in-model direction (2026-06-23, abandoned).

A **behavior** Jordan asked for (loading bar, drag-and-drop, a screenshot
button) is still a valid requirement even if first requested in a dead-tech
era — build it native. An **implementation-tech** request tied to a dead
stack (Flyleaf, Media Foundation, mpv questions) is dead — never resurrect it.

---

## DO THESE NEXT (ranked by value to Jordan's daily forensic-review work)

Only 5 genuinely unmet items survived verification against current code. The
app is in much better shape than the log alone suggests — most of the 100+
requests in the log are already built. In order:

1. **Restore the normal mouse pointer over the timeline.** He asked for an
   I-beam on 2026-06-30, then reversed that decision. The I-beam is still
   there today, which is now wrong. `main.cpp:3164`. **Small** — delete/guard
   one `SetMouseCursor` call.
2. **Make "Load Reel" show .json files first**, not buried among txt/xml.
   `main.cpp:4553-4568` — native Windows file-open dialog, default filter
   mixes all 3 types. **Small** — default `ofn.nFilterIndex` to the
   "Becky reel (*.json)" filter.
3. **Add the 2 tiny hashmarks inside the white playhead flag** (his reference
   photo `playhead.JPG` shows them). No matching draw code found. **Small.**
4. **Make the secondary "return-to" playhead stock look like a real black
   bar**, not a thin flashing line — his spec was two IDENTICAL bars, only
   one carrying the moving white flag. The underlying mechanic (click sets a
   return point, flashes on move, edits target it during playback) already
   works — `main.cpp:1900-1913, 3881-3886`. This is a visual-shape mismatch
   only. **Small/Medium.**
5. **Add a "not yet indexed" icon to qmd search results** (parallel to the
   green "+" transcribe icon), so Jordan can tell which hits haven't been
   embedded yet. No matching code found. **Small.**

**Also worth a 5-minute live check, not a code fix:** the "preparing..."
re-decode-feeling lag Jordan reported on every split (2026-07-17) was
reported against the OLD mpv/proxy engine, deleted 2026-07-23. The new
engine's waveform-peak cache is keyed per **source file**, not per clip
(`main.cpp:2712-2723`), which should mean a split inside an already-loaded
source never re-triggers "preparing." Code says it should be fixed; only
Jordan splitting clips in a real session can confirm it actually feels fixed.

Everything else Jordan asked for across all 13 log entries was found
implemented and working in the current code — see the checklist below for
the evidence on each one.

---

## Timeline — mechanics

- [x] Ctrl+scroll zooms/pans ONLY the timeline, nothing else affected — 2026-06-30, *"Ctrl + scrool should only zoom on the timeline"*. `main.cpp:3273` (wheel handler gated on `hovered`).
- [x] Zoom centers on the **playhead** (newest wins over "toward the mouse", 2026-06-30 → 2026-07-02) — *"let's change mouse wheel zoom to zoom in on the playhead"*. `main.cpp:3263-3266` (`zoomAnchorX`).
- [x] Zoomed all the way out no longer locks at the end of the last clip — 2026-07-03, *"End of last clip should not cause limited user navigation"*. `main.cpp:3260` — `g_scrollSec` has no upper clamp.
- [x] Ctrl+click / Shift+click multi-select clips — 2026-06-30. `main.cpp:3524-3536` (`g_sel` set, ctrl toggles, shift range-selects).
- [x] "Render Selection (N)" button for multi-selected clips — 2026-06-30. `main.cpp:6780`.
- [x] Toolbar buttons never shift position (Render Selection grays out instead of vanishing) — 2026-07-03, *"buttons on the timeline toolbar should not move"*. `main.cpp:6781` (`BeginDisabled`, comment cites the exact complaint).
- [x] Dedicated "extend selected clip 1 frame" left/right buttons (separate from prev/next-frame) — 2026-06-30. `main.cpp:6663,6672`.
- [x] Prev/Next-frame buttons removed from the toolbar, function + shortcut kept — 2026-07-03, *"remove ... buttons themselves are just clutter"*. No button text found; frame-step logic intact at `main.cpp:5559-5579`.
- [x] Left/Right arrow = 1-frame step at the clip's own source fps — 2026-06-30/07-01. `main.cpp:5559-5579`.
- [x] Ctrl+Left/Right jumps to the previous/next edit point **anywhere on the timeline** (fixes "stuck at clip edge", reported broken 3 times) — 2026-06-30 → 2026-07-03 (newest/CRITICAL). `main.cpp:3114-3126` (`prevBoundary`/`nextBoundary` scan the whole timeline, not one clip) + `5559-5579`.
- [x] Up/Down arrow zooms timeline in/out when timeline (not library) is focused — 2026-07-02. `main.cpp:5555-5557`.
- [x] Click + drag the scroll wheel (middle mouse) pans without rearranging clips — 2026-07-01. `main.cpp:3279-3283`.
- [x] Dragging one of several selected clips moves them all together — 2026-07-03. `main.cpp:3429,3730` (`g_gest.group`).
- [x] Esc key ALSO deletes the selected clip (2nd hotkey alongside Delete) — 2026-07-01. `main.cpp:5348-5372` (`VK_ESCAPE`).
- [x] "split clip" popup and all the little toast popups ("Removed 1 clip", "Reordered clip", "Undo", "Redo") are gone — 2026-07-01/07-02. No toast strings found anywhere in `main.cpp`.
- [x] After a split, the NEW clip after the playhead is the one selected, not the one before — 2026-07-02. `becky-go/cmd/clip/app.go:908-921` (the half **after** the cut gets the new id) + `main.cpp:5704-5705` (`g_sel.insert(newId)`).
- [x] Ctrl+Z undoes a split in one clean step, not a 3-press reorder/delete/restore sequence — 2026-07-03. `becky-go/cmd/clip/app.go:906` — split is ONE `pushUndoLocked()` call, not several.
- [x] Ripple edit keeps the playhead's position relative to its clip — 2026-07-02/07-03. `main.cpp:5710` (`rippleCurSec`).
- [x] Drag video files onto the timeline from any folder (Explorer) — 2026-07-02/07-03. `main.cpp:2552-2609,3174-3184,4940` (full `WM_DROPFILES` pipeline).
- [x] Screenshot button, saves as Screenshot_0001 and increments — 2026-07-02. `main.cpp:6810-6833`.
- [x] 2x playback speed button + `Shift+Spacebar` shortcut — 2026-07-01. `main.cpp:5268-5269,6621-6624`.
- [x] Non-intrusive loading bar / work indicator for anything over ~1 second — 2026-07-02, *"can we get a loading bar?"*. `main.cpp` work-indicator system (`beginWork`/`endWork`, the ">1s work indicator" comment block ~6899-6914).
- [x] **Playback Threshold** (auto-editor jumpcut skip-under-threshold on the timeline, draggable bar, dimmed skipped ranges, seamless playback skip, toggle icon) — 2026-07-03, his biggest-potential-breakthrough ask. Fully built: `main.cpp:2088-2101` (`emitThreshold`), `3315-3524` (drag), `3865-3879` (draw + dim), `6707-6750` (toggle button + tooltip).
- [x] Clips are 2x as tall, small fixed thumbnail kept out of the waveform/cut area — 2026-07-03. `main.cpp:3235-3242` ("E-11" comment matches verbatim).
- [x] Color coding fills the whole clip, not a thin stripe — 2026-06-30(2).
- [x] Selected clip = filled/opaque, not a yellow outline — 2026-06-30(2). `main.cpp:4411` ("selection is a FILL, never a yellow/white outline").
- [ ] **Playhead: 2 tiny vertical hashmarks inside the white flag** (his reference photo) — 2026-07-03, *"add 2 tiny vertical hashmarks inside the white part of the playhead, see reference photo: playhead.JPG"*. No hashmark draw code found near the playhead flag (`main.cpp:3889-3897` draws only the flag rect/triangle/grip lines). **Small.**
- [ ] **Secondary playhead "stock" should be a matching black bar, not a thin line** — 2026-07-03, *"Add a second Playhead Stock (the black bar)... 2 identical black bars, but only one of them has the white playhead"*. The mechanic works (click sets it, flashes like a text cursor, edits target it during playback) but it draws as a plain 2px line, not a bar matching the primary playhead's shape — `main.cpp:3881-3886` vs. `3889-3897`. **Small/Medium, cosmetic only.**
- [ ] **I-beam cursor over the timeline is now WRONG** — SUPERSEDED REQUEST, see below. **Small.**

## Left Side-Panel

- [x] Sort toggles: newest⇄oldest, A-Z⇄Z-A — 2026-06-30(2). `main.cpp:6396-6405`.
- [x] Search within one video's transcript once it's opened — 2026-06-30(2). `main.cpp:6285` (`g_withinBuf`, hint "search within this transcript").
- [x] Up/Down arrow navigates the list; mouse-click and arrow-key selection share ONE selection, never fight — 2026-07-02(5). `main.cpp:6450-6451` and the equivalent hit/cue blocks.
- [x] Enter = double-click (quote → timeline, video → transcript) — 2026-07-01. `main.cpp:6212,6298,6520`.
- [x] Spacebar plays the selected library clip — 2026-07-02(5). `main.cpp:6521`.
- [x] Right-click a clip/quote/video: "Open in File Browser" + "Copy File Name" (full filename incl. extension, of the video, never the transcript) — 2026-07-01/07-02. `main.cpp:3372-3381` (clip), `6239-6256` (quote/hit), `6489-6501` (video).
- [x] "Back" button returns to the same scroll position, not the top — 2026-06-30, confirmed fixed as of the 2026-07-03 entry itself.
- [x] Green outline marks the transcript last viewed via Back — 2026-07-03. `main.cpp:3985` (`g_libJustViewedIdx`, comment: *"green outline for the just-viewed video (B-6)"*).
- [x] Flowing, wordprocessor-style single-video transcript (audapolis pattern): continuous prose, timestamps only at real pauses, no per-segment filename repetition — 2026-07-01 → 2026-07-14 (2026-07-14 supersedes the simpler "Descript-style" ask). `main.cpp:6264-6350`, comment cites "audapolis pattern" directly.
- [x] qmd hybrid search with a toggle to fall back to plain keyword search — 2026-06-30(2). `main.cpp:6139` ("smart search").
- [x] "render" output folder never appears in search/browse — 2026-07-02(5). `becky-go/internal/footage/discover.go` `excludedWalkDirs` (comment: *"render... never case footage"*).
- [ ] **qmd "not yet indexed" icon** — 2026-06-30(2), *"we need an 'index' icon for results which have not yet been indexed, similar to our 'plus' button which transcribes"*. `grep "not.*indexed|needsIndex|qmdIndex"` = zero hits. **Small.**
- [ ] **"Load Reel" dialog doesn't put .json files first** — 2026-07-02(5). It's a native Win32 `GetOpenFileNameW` dialog (`main.cpp:4553-4568`), whose default filter ("Edits and reels") mixes .txt/.xml/.json and whose in-dialog sort order is OS/Explorer-controlled, not app-controlled. **Small fix available:** default `ofn.nFilterIndex` to the "Becky reel (*.json)" filter so .json-only is what shows by default (true "sort json to top" isn't available from a stock Windows dialog without building a custom file browser, which is not proportionate here).

## Right Panel / Human Review (Q&A)

- [x] "?" questions from Forensic Hits surface as Q&A cards in the right panel; click a card, type an answer, submit — same gesture as chat — 2026-07-01. Built (also confirmed in project memory as shipped); code at ~`main.cpp:7000-7040`.
- [x] Forensic Hits never overwrites `_forensic_hits.json` — numbers up instead (`_forensic_hits1.json`, `_forensic_hits2.json`...) — 2026-07-01. `becky-go/cmd/becky-judge/main.go:341-355` (`nextForensicHitsPath`).
- [x] Running "Open Forensic Hits.bat" while a session is open doesn't kill it — opens a new instance instead (option b) — 2026-07-01. No single-instance lock exists anywhere in `main.cpp`; `Open Forensic Hits.bat` always calls `Open Becky Review 3.bat`, spawning an independent process.
- [x] Clicking inside the answer box and then clicking the timeline returns keyboard focus to the timeline, whether an answer was submitted or not — 2026-07-01/07-01(lag). `main.cpp:7341-7357` — explicitly cites *"Jordan reported this three separate times (feedback4, feedback6, user-lag)"* and fixes it by moving window focus on any timeline click.

## Export / Render

- [x] Naming: `clips_SOURCE_NNNN.mp4` when all clips share one source, `clips_compilation_NNNN.mp4` when mixed, never overwrites — 2026-07-01. `main.cpp:6752` comment ("F-3/F-4"), engine-side in `renderReel`.
- [x] EDL exports carry an audio track in Vegas Pro (was video-only) — 2026-07-02. `becky-go/internal/edl/cmx3600.go:66` — `"AA/V"` channel designator, comment names the exact bug.
- [x] "Open Forensic Hits.bat" also writes a Vegas-compatible `.edl` — 2026-07-02. `becky-go/cmd/becky-hits/main.go:145-150,294-308`.
- [x] "Open in File Browser" always selects the relevant clip in Explorer — 2026-07-02(5). `main.cpp:4538-4539` (`/select,"path"`).

## Overlay (burned-in provenance text)

- [x] Space after "TC" ("ORIG TC 01:32:34:02") — 2026-07-01. `main.cpp:1634`.
- [x] "Date: ... UTC" — 2026-07-01. `main.cpp:1632`.
- [x] Order: Date, then Timecode, then filename — 2026-07-01. `main.cpp:1629-1650`.
- [x] Preview and render show IDENTICAL overlay text (was preview-only, "SECOND REQUEST") — 2026-07-01. `main.cpp:1592-1650` comment: mirrors `becky-go/internal/reel/drawtext.go` exactly, same field order.
- [x] Overlay 3-way toggle: Off / On-but-hidden-in-preview (default) / On-and-shown — 2026-07-03. `main.cpp:1536` (default = 1), `6631-6635`.

## Colors

- [x] Exact 8-color palette in order (Green #14FF39, Blue #00AEEF, Crimson #DC143C, Purple #8A2BE2, Hot Pink #FF57D1, Yellow #FFD700, Cyan #16F0EA, Orange #FF8C00) — 2026-07-01. `main.cpp:4164-4168`.
- [ ] **Color persistence per source video** (a color must never get reassigned to a different video after clips are removed) — 2026-07-03. **IN PROGRESS** by the other worker right now ("frozen one-color-per-source-video") — not reported as a fresh gap.

## Transcribe / Original-file overlay (2026-06-23 legacy asks)

- [x] "+" button next to a video with no transcript, runs local transcription — 2026-06-23. `main.cpp:4471-4472,6487,6498` (`requestTranscribe`).
- [x] "Transcribe all" — bonus, not explicitly asked but built. `main.cpp:6410-6437`.
- [x] Overlay checkbox showing filename/date/link on preview — 2026-06-23. Evolved into the full per-field overlay toggle system (Date/TC/filename/person/location/link) described above.

## Performance

- [ ] **Live-verify only, not a code gap:** timeline lag with >3 min of clips (2026-07-01-lag) and "preparing..." re-decode feel on every split (2026-07-17) were both reported against the OLD mpv/proxy engine, removed 2026-07-23. Current waveform-peak cache is keyed per **source file** (`main.cpp:2712-2723`), so a split inside an already-loaded source shouldn't retrigger "preparing." Recommend Jordan confirm this feels fixed in a real session rather than trusting static code alone.

---

## IN PROGRESS (another worker is on these right now — not reported as new gaps)

- Single-click a transcript quote wiping the timeline (destructive bug)
- Single-click quote = preview only; double-click quote = add to the right of the playhead, non-destructively
- Single-click a video row opens its transcript
- Clicking a quote highlights it + arrow-key navigation between quotes
- Transcript paragraph breaks with timestamps
- Left-panel draggable splitter / truncated video titles
- Dragging the playhead scrubs instead of panning
- Paused click moves the playhead AND selects the clip (selection always, both play states)
- The 's' split key duplicating/corrupting clips
- Frozen one-color-per-source-video
- The "most relevant" crown sort

## ALREADY CONFIRMED WORKING BY JORDAN (not re-tested here)

Undo/Redo buttons · Enter stops the playhead where it is · Pause returns to
where playback started · audio waveforms on clips · captions · frame-accurate
cuts · mpv removal.

---

## SUPERSEDED / DEAD — deliberately excluded

Ruled out by chronology (newest wins) or by the architecture guardrail above.
Listed here so Jordan can sanity-check the calls.

- **I-beam cursor request (2026-06-30)** — REVERSED. Jordan later asked for
  the normal pointer back. The I-beam is what's actually in the code today
  (`main.cpp:3159-3164`), which makes it a **live NOT-DONE item**, not dead —
  it's listed above under Timeline and in "DO THESE NEXT," not here. Flagging
  the reversal explicitly so the chronology call is visible.
- **Embedded llama.dll / shared-state built-in model (2026-06-23)** — whole
  direction abandoned; becky-review talks to the Go engine over its existing
  verb bridge instead. Not reported as missing.
  - The **specific "apply/reject adds friction" complaint** from that same
    entry is tied to that abandoned chat design. Note for transparency: an
    Apply/Reject pair (`main.cpp:7067-7091`, `g_proposalPending`) is still
    literally present in the current "ask becky" panel today — it just runs
    over the new engine-verb bridge, not the abandoned embedded model. Per
    instruction this is not reported as a fresh gap, but it is real and
    visible in the app if Jordan wants to revisit the friction complaint on
    its own merits.
  - "Yellow popup covering becky's module on quote-hover" (2026-06-23, same
    entry) — no such yellow/covering tooltip found anywhere in current hover
    code; the panel this described no longer exists in this form. Treated as
    moot.
- **Shotcut lag complaint (2026-06-24)** — Shotcut abandoned outright; app is
  native C++/ImGui now.
- **mpv/Flyleaf/LibVLCSharp/Media Foundation/DirectX-question entry
  (2026-07-01-lag)** — these were Jordan asking how the OLD (mpv-based)
  engine worked, not requirements. mpv was deleted 2026-07-23 and replaced
  by an in-process libavcodec/D3D11VA engine. Dead by architecture change.
- **"Proxy made adding a quote painfully slow" (2026-07-02(6))** — proxy/mpv
  wait-time complaint from the pre-rewrite engine; the whole proxy-wait
  mechanism this describes no longer exists in the current architecture.
- **"Timeline doesn't play while a new segment is proxying" (2026-07-02(6))**
  — same mpv/proxy-era mechanism, removed.
- **"Save caused the program to freeze" (2026-06-30)** — reported against
  the very first build attempt, long before the current async-engine-call
  architecture (every mutating action now goes through `engineCallAsync`,
  e.g. `main.cpp:7070`, specifically to kill UI-thread freezes). No freeze
  code path found; recommend a live check if it ever recurs, but not carried
  forward as a known gap.
- **2026-07-20 entry (MediaEditor/velo/Walnut links)** — reference material
  Jordan flagged for research, not a stated requirement. Nothing actionable.
- **Questions, not requirements, throughout the log** — "Are we using a
  Proxy/FFMPEG/Flyleaf/etc?", "how many verification steps were there for
  these clips?", "DOM-rebuild-per-edit — help me understand" — these are
  Jordan asking/thinking out loud, not asking for a change. Not tracked as
  work items.
