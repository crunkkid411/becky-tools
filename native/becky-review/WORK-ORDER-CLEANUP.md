# WORK ORDER — becky-review native: stability + speed cleanup

**Context:** The app works and keeps up with Jordan's editing speed for the first time.
The job is to REMOVE dead weight, FIX silent failures, and SPLIT the monolith so future
agent sessions can make surgical edits without rebuilding from scratch.

**Rules for any agent touching this code:**
- DELETE, don't rebuild. MODIFY, don't replace.
- engine.cpp works. The edit flow works. The render loop works.
- Never add a "fallback" that silently degrades to a path that never worked.
- If something fails, the USER MUST SEE IT. No silent no-ops.
- Build with `_build.bat` and verify the exe launches before marking done.

---

## P0 — Remove GStreamer entirely (dead code since cycle 23)

Waveforms decode via ffmpeg now (see `decodeWindow` / `runPipeCapture`). GStreamer is:
- Still linked: gstreamer-1.0.lib, gstapp-1.0.lib, gstvideo-1.0.lib, gobject-2.0.lib, glib-2.0.lib
- Still initialized: `gstInitSEH()` with SEH wrapper because it CRASHES on dual-install PATH
- Still deployed: ~30 libg*.dll, libgst*.dll files in the exe folder
- Still checked: `g_gstAvailable` atomic + every call site that guards on it

**What to do:**
1. In `_build.bat`: remove the 5 GStreamer .lib entries and the 3 /I include paths
2. In `main.cpp`: remove `#include <gst/gst.h>`, `<gst/app/gstappsink.h>`, `<gst/video/video.h>`
3. Remove `gstInitSEH()` function and its call in main()
4. Remove `g_gstAvailable` atomic and every `if (!g_gstAvailable)` check
5. Remove the GLib pool-spawner warmup hack (fakesrc pipeline)
6. Remove any remaining GStreamer pipeline code in peaksProcessBatch (should already be ffmpeg)
7. Delete the ~30 GStreamer/GLib DLLs from the deploy folder (libg*.dll, libgst*.dll, etc.)

**Done when:** `_build.bat` compiles without GStreamer, exe launches, waveforms still load.

---

## P1 — Buffer crash.log + gate PACK boundary logging

`crashLog()` calls `flush()` on every write. `loadTimelineView()` logs 10 PACK boundary
lines per call. At Jordan's editing speed (rapid splits), that's 50-100 synchronous disk
flushes per second on the UI/render thread.

**What to do:**
1. Gate PACK boundary logging behind `BECKY_REVIEW_PACK_LOG=1` env var (like editLog/scrubLog)
2. Replace `crashLog()`'s per-call `flush()` with a buffered writer:
   - Accumulate lines in a string buffer
   - Flush every 500ms OR on process exit OR on TERMINATE/SIGABRT
   - Keep the mutex, keep the thread tag, keep the format
3. The `stageMark()` slow-span logger (>80ms) should still flush immediately (it's rare)

**Done when:** crash.log still captures everything, but `loadTimelineView` no longer
blocks the UI thread with 10 disk flushes per edit.

---

## P2 — Never silently drop edits (queue + re-target)

Current behavior: `g_editsInFlight` gates a second edit on the same clip id. If Jordan
presses S twice in 30ms, the second press is DISCARDED silently. Line 7315: `if (!res.ok) continue;`
swallows engine rejections with zero UI feedback.

**What to do:**
1. When an edit is gated (clip id already in-flight), QUEUE it in a per-clip pending list
   instead of discarding it. When the in-flight edit resolves and the timeline reloads,
   re-target the queued edit at the correct post-split clip id and fire it.
2. When `!res.ok` in the UI drain loop, show a visible indicator (g_renderMsg flash:
   "Edit rejected by engine") instead of `continue`.
3. The `g_editsInFlight` set stays as a gate for IMMEDIATE dispatch, but gated edits
   go into a `g_editsPending` queue that drains after each timeline reload.

**Done when:** 5 rapid S presses produce 5 splits (or 4 splits + a visible "rejected"
message if the engine truly can't). Never a silent no-op.

---

## P3 — Visual distinction for preview/non-engine clips

Clips without an engine id (preview clips from search auditions, space-bar play, or
failed add_clip) look identical to real clips. Every edit on them either no-ops or
triggers a slow promote round-trip.

**What to do:**
1. In the timeline draw code, render clips with empty `c.id` with:
   - A dashed or dotted bottom border (vs solid for engine-backed)
   - Slightly reduced opacity (0.85 alpha vs 1.0)
   - A small "preview" badge or icon in the corner if the clip is wide enough
2. When a promote succeeds (editWorker patches the real id), the clip redraws as solid
   on the next timeline reload — no action needed, loadTimelineView handles it.
3. The tooltip on hover should say "Preview clip — first edit will register it" for
   id-empty clips vs the normal label for engine-backed clips.

**Done when:** Jordan can instantly SEE which clips are real and which are previews,
without having to try an edit and wonder why nothing happened.

---

## P4 — Split main.cpp into modules

**STATUS (2026-07-25): MODULE MAP added to main.cpp header. Extraction is a
multi-session task — one module per session, build after each. The boundaries
are documented in the MODULE MAP comment at the top of main.cpp.**

9,509 lines in one file. No agent can safely modify it. Split into:

| File | Contents |
|------|----------|
| `main.cpp` | Window creation, D3D device, render loop, input dispatch, font/style setup |
| `engine_seam.cpp/.h` | Go engine bridge (Engine struct, engineStart/Call/CallAsync/Reader), editWorker, drainAsync |
| `waveform.cpp/.h` | Peaks struct, peaksProcessBatch, decodeWindow, runPipeCapture, BPK3 cache, BgWorkPool |
| `timeline_draw.cpp/.h` | drawTimeline(), clip rendering, ruler, playhead, drag gestures, selection |
| `ui_panels.cpp/.h` | Library panel, search, transcript, Q&A/ask-becky, overlay, toolbar buttons |
| `captions.cpp/.h` | Caption system, rebuildDerivedCaptions, captionTryUndo |

**Rules for the split:**
- No logic changes. Pure move + add headers.
- Shared state (g_track, g_compDur, etc.) lives in a `state.h` with extern declarations.
- Each .cpp includes only what it needs.
- `_build.bat` compiles all .cpp files (already does engine.cpp, just add the new ones).
- Build and verify after EACH file is extracted (not all at once).

**Done when:** main.cpp is under 2000 lines, each module is under 2000 lines, app builds
and runs identically.

---

## P5 — Warm audio decoder pool for reel playback

`audioDecLoop` opens/closes each source file from scratch per segment per playback pass.
A 20-clip reel from 10 sources = 20 avformat_open_input + seek + decode per play.

**What to do:**
1. Add a small decoder pool (like videoLoop's `SrcPool`) to audioDecLoop:
   - Key by source path, keep up to 8 open AVFormatContext+AVCodecContext+SwrContext
   - On segment start: pool.get(source) instead of fresh open
   - On generation change (seek/rate/stop): flush but DON'T close (reuse on next pass)
   - On shutdown or pool overflow: close oldest
2. The seek within a warm decoder uses av_seek_frame (already proven in videoLoop)
3. atempo filter graphs are cheap to rebuild per-segment (no file I/O), leave as-is

**Done when:** Reel playback with 10+ clips from different sources has no audible gap
at segment boundaries (beyond the inherent decode latency). crash.log shows pool hits
instead of fresh opens.
