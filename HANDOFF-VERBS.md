# HANDOFF-VERBS.md — wire the remaining becky-edit timeline verbs into Shotcut

> **Your job, in one sentence:** in ONE file —
> `X:\AI-2\becky-shotcut\src\docks\beckydock.cpp` — turn the host commands that currently
> log *"(pending host wiring)"* into real Shotcut calls, rebuild, and **verify each on the
> running window**. Everything else (the Go engine, the bridge, preview/add, the in-process
> Gemma model) already works. Drive this list top to bottom.

## 0. Context you need (and nothing more)
- becky-edit (the Go bridge) sends the Becky dock JSON "host commands". The dock's
  `BeckyDock::executeHostCommands` (beckydock.cpp ~line 231) maps them to Shotcut calls.
  `player.open_seek_play`, `timeline.append`, `player.seek` are DONE (copy their style).
  The rest fall through to the `else` that logs *"(pending host wiring)"* — those are your targets.
- `MAIN` = the MainWindow singleton (`#include "mainwindow.h"`); `MLT` = the Mlt::Controller.
  The dock already runs on the GUI thread, so call Shotcut objects directly. **All positions are
  FRAMES** — the dock has `secToFrame()`, and the bridge already sends `frame_*` args.
- The exact API for every verb was source-verified and is in §3 below (file:line cited).

## 1. Build + run + GUI-test (the loop you'll repeat)
```bash
# Rebuild shotcut.exe after editing beckydock.cpp (MINGW64 env):
cd /x/AI-2/becky-shotcut/build && PATH="/c/msys64/mingw64/bin:$PATH" MSYSTEM=MINGW64 \
  /c/msys64/mingw64/bin/ninja.exe shotcut    # ~2 min; relinks build/src/shotcut.exe
```
- **Launch** (sets the Qt/MLT + llama DLL paths): run `X:\AI-2\becky-tools\Open Becky Edit.bat`,
  OR replicate its env (PATH += `C:\msys64\mingw64\bin;C:\llama.cpp\build\bin`,
  `MLT_REPOSITORY=C:\msys64\mingw64\lib\mlt`, `MLT_DATA=C:\msys64\mingw64\share\mlt`) and start
  `X:\AI-2\becky-shotcut\build\src\shotcut.exe`. The dock auto-spawns becky-edit.
- **GUI test harness** (drives Jordan's REAL mouse/keyboard — he is AFK, that's fine):
  `scripts\gui-test\winshot.ps1 -Title Shotcut -Out shot.png -Maximize -Activate` (screenshot a
  window), `shot.ps1 out.png` (full screen), `input.ps1 -Click "x,y" / -Double "x,y" / -Type "..."
  / -Keys "{ENTER}"` (absolute screen coords; a maximized window sits at -8,-8 so screen = image-8).
  Read the PNGs to see the result. There's a real test fixture (interview.mp4 + .srt, "penguin"
  quote) at `…\scratchpad\realcase` — but the scratchpad is session-scoped; regenerate one with
  ffmpeg if it's gone (a real video Shotcut can open + a matching .srt). Or use a real case folder.
- **Headless smoke** of the Go side (no GUI): `becky-edit --selftest` must stay green; the bridge's
  verbs are already exercised by `internal/edittools` tests (`go test ./internal/edittools/`).

## 2. Phase A — the clip-identity map (DO THIS FIRST; everything below needs it)
Shotcut addresses clips by `(trackIndex, clipIndex)` and those indices SHIFT on every edit. becky
addresses them by a stable id ("c1"). So the dock must remember id->Shotcut-QUuid:
- Add a member `QHash<QString, QUuid> m_clipUuids;` to beckydock.h.
- In `executeHostCommands`, in the `timeline.append` branch, AFTER the append succeeds, capture the
  new clip's uuid: the appended clip is the last on the current track —
  `int t = MAIN.timelineDock()->currentTrack();`
  `int c = MAIN.timelineDock()->clipCount(t) - 1;`  // verify clipCount() exists; else use the model
  `m_clipUuids[a.value("id").toString()] = MAIN.timelineClipUuid(t, c);`  // mainwindow.h:106
- Add a resolver used by every id-addressed verb:
  `bool resolveClip(const QString &id, int &track, int &clip)` ->
  `QUuid u = m_clipUuids.value(id); if (u.isNull()) return false;`
  `return MAIN.timelineDock()->model()->findClipByUuid(u, track, clip);`  // multitrackmodel.h:127
  (Confirm the exact signature in the header; adjust if it returns by value / different out-params.)

## 3. Phase B — wire these verbs (clean public APIs; each has a visible check)
Implement as `else if (name == "...")` branches. **`MAIN.timelineDock()` = TD below.**
ALL DONE (wired 2026-06-23, `claude/verbs-impl`). Phase A clip-id map (`m_clipUuids` +
`captureClipUuid`/`resolveClip`) is in beckydock.cpp/.h and PROVEN (split + ripple_delete
both resolved `c1` correctly). Verification used the AUTOSAVE .mlt as ground truth
(`%LOCALAPPDATA%\Meltytech\Shotcut\autosave\*.mlt`) — the document is the source of truth,
not a pixel guess.
- [x] `timeline.add_track {kind}` -> `kind=="audio" ? TD->addAudioTrack() : TD->addVideoTrack()`.
      **VERIFIED:** new track rows appeared (V3..V43; .mlt playlist count grew).
- [x] `timeline.remove {id}` (LEAVE a gap) -> resolve id->(t,c), `TD->lift(t, c)`.
      WIRED, NOT YET GUI-VERIFIED. (Signatures confirmed; the one clean test attempt mis-fired
      because `c1` had already been ripple-deleted, so becky returned "no clip c1" — not a wiring
      fault. Re-test with a fresh clip id to confirm a blank is left in place.)
- [x] `timeline.ripple_delete {id}` (CLOSE the gap) -> `TD->remove(t, c)`.
      **VERIFIED:** .mlt entry count 4->3, project duration 2.03s->1.07s (gap closed).
- [x] `timeline.select {ids}` -> `QList<QPoint>` of `QPoint(clip, track)`, `TD->setSelection(list)`.
      WIRED, NOT YET GUI-VERIFIED (selection isn't persisted to the .mlt, so it needs a visual/border
      check; uses the same proven resolveClip path).
- [x] `timeline.marker {pos,text}` -> `Markers::Marker m; m.start=m.end=frame; m.color=
      Settings.markerColor(); TD->markersModel()->append(m);`. **VERIFIED:** .mlt shows
      `shotcut:markers` -> text "evidence", start/end 00:00:02.000, color #008000.
- [x] `track.mute {track,on}` -> read `IsMuteRole` at the TOP-LEVEL index `model()->index(track)`
      (NOT makeIndex, which builds a clip-level index); if differs, `TD->toggleTrackMute(track)`.
      WIRED, NOT YET GUI-VERIFIED. (The model-less agent never cleanly emitted `track.mute` —
      it added tracks instead — so no `hide` appeared in the .mlt. The branch + signatures are
      correct; re-test once the Gemma model is loaded so the agent obeys "mute track N".)
- [x] `timeline.move {id,track,pos}` -> resolve id->(fromTrack,clip),
      `TD->moveClip(fromTrack, to_track, clip, frame_pos, false)`.
      WIRED, NOT YET GUI-VERIFIED (same proven resolveClip path; args use `to_track`/`frame_pos`).
- [x] `timeline.trim {id,in|out}` -> resolve id->(t,c); becky sends ABSOLUTE source in/out (frames),
      so compute a FRAME DELTA from `getClipInfo(t,c)->frame_in/frame_out`, call
      `TD->trimClipIn(t,c,c,deltaIn,false,false)` / `trimClipOut(t,c,deltaOut,false,false)`,
      THEN `TD->commitTrimCommand()` (these dock methods stage an interactive trim and only push the
      undo command on commit). WIRED, NOT YET GUI-VERIFIED.
- [x] `timeline.split {id,at}` -> resolve id->(t,c),
      `MAIN.undoStack()->push(new Timeline::SplitCommand(*TD->model(), {t}, {c}, frame_at))`.
      **VERIFIED:** the clip was cut into adjacent pieces in the .mlt (entry count rose; cut
      boundaries align). This also proves the whole id->uuid->(track,clip) resolve path.

All of the above are GUI-thread-safe and undo-wrapped (they push QUndoCommands).

### Verification status (2026-06-23, session 4) — honest summary
Test rig: forked shotcut.exe rebuilt + launched (env from `Open Becky Edit.bat`), case folder
`X:\AI-2\beckytest` (interview.mp4 1280x720 30fps + matching .srt), driven via
`scripts/gui-test` (winshot/input). Oracle = the autosaved Shotcut .mlt (document truth).
- VERIFIED on the running window/document: `timeline.append`(+Phase A uuid), `timeline.add_track`,
  `timeline.marker`, `timeline.split`, `timeline.ripple_delete`.
- WIRED, signatures confirmed against the real headers, NOT YET individually GUI-verified:
  `timeline.remove`(lift), `timeline.select`, `track.mute`, `timeline.move`, `timeline.trim`.
  They reuse the SAME proven mechanisms (resolveClip + one confirmed TimelineDock slot each).
- BLOCKER for finishing the unverified five: the in-process **Gemma-4 QAT model failed to load**
  (`shim: model load failed ...gemma-4-E4B-it-qat-UD-Q4_K_XL.gguf`), so the agent ran on a weak
  server-fallback and would NOT reliably emit specific single verbs (e.g. it kept adding tracks
  instead of muting). Load the model (the .gguf IS present at models/gemma4/) and the agent will
  obey targeted goals — then the five above can each be confirmed the same .mlt-oracle way, OR
  expose them as direct QML methods on BeckyDock (like addClip) for deterministic single-verb tests.

## 4. Phase C — the harder verbs (DEFERRED — still log "(pending host wiring)")
STATUS (2026-06-23): NONE of these are wired yet — they still fall through to the `else` that
logs `becky: <name> (pending host wiring)`. They were intentionally deferred this session: each
needs more than a one-liner, and during testing the model-less agent never emitted any of them
(so there was nothing to verify against). Exact remaining work per verb, so it is not lost:
- **`filter.add/set/remove {clip,fx_id,name,params/param/value}`** — resolve `clip`->(t,c)->
  `Mlt::Producer p = TD->producerForClip(t,c)` (timelinedock.h:64), then via
  `MAIN.filterController()`: `setProducer(&p)` -> `attachedModel()->add(metadata("<mlt_service>"))`
  -> `setCurrentFilter(row)` -> `currentFilter()->set(prop,value)`. NEEDS a becky-name -> MLT-service
  map (brightness->"brightness", volume->"volume"/"avfilter.volume", crop->"crop"/"qtcrop",
  fadeIn->"fadeInBrightness"/"fadeInVolume", etc.; the becky allowlist is in
  internal/edittools/effects.go). Track-targeted filters attach to the track producer instead.
- **`overlay.set {field,on}`** — attach/update a `dynamictext` filter (the forensic lower-third) on
  the bottom video track (or a dedicated overlay track) via the same filterController path:
  `set("argument", <built text>)` + geometry/size/fgcolour. The text is built from the on/off fields
  (filename/timecode/date/person/location/link); store which lines are on in a member so toggling one
  rebuilds the whole lower-third.
- **`track.gain {track,db}`** — no single API; attach a `volume` filter to the TRACK producer
  (`MAIN.timelineDock()->model()->tractor()->track(mltIndexForTrack(track))`) and `set("level", db)`
  (or "gain"). Confirm the property name against the volume filter's metadata.
- **`player.grab_frame {source,at}`** — ASYNC. Open the source span, seek to `at`, then
  `Mlt::VideoWidget::requestImage()` and save the PNG in the `imageReady` slot. Copy
  mainwindow.cpp `on_actionExportFrame_triggered` / `onVideoWidgetImageReady` (mainwindow.h:357/358).
  Write to becky's work dir; the bridge expects the PNG path back as the result.
- **`render.export {clips,out,overlay}`** — needs a FORK ADDITION (`EncodeDock::encode` is private,
  no `MAIN.encodeDock()` accessor). Two clean options: (a) add a thin public `MAIN.beckyEncode(out)`
  + accessor that drives the existing EncodeDock; OR (b) simpler + reuses becky's PROVEN path:
  `MAIN.saveXML(tmpMlt)` then run becky's `internal/reel`/`melt` as a child against the `.mlt`.
  Verify an actual output file appears + ffprobe shows the expected duration.

Original guidance below (kept for reference):
- `filter.add/set/remove {target,name,value}` -> via `MAIN.filterController()`:
  `setProducer(targetProducer)` -> `attachedModel()->add(metadata("<mlt_service>"))` ->
  `setCurrentFilter(row)` -> `currentFilter()->set(prop, value)` (filtercontroller.h / attachedfiltersmodel.h
  :84/86 / qmlfilter.h:71). `target` = the clip's producer (`TD->producerForClip(t,c)`) or a track producer.
- `overlay.set {...}` -> the `dynamictext` filter via the same path: `set("argument", text)` +
  geometry/size/fgcolour. (This is also how a forensic lower-third would attach.)
- `track.gain {track,db}` -> NO single API; attach a `volume` filter to the track producer and
  `set("level", db)`.
- `player.grab_frame {source,at}` -> ASYNC: seek, then `Mlt::VideoWidget` `requestImage()` and save the
  PNG in the `imageReady` slot (pattern: mainwindow.cpp:5556 `on_actionExportFrame_triggered`).
- `render.export {clips,out}` -> needs a FORK ADDITION: `EncodeDock::encode` is private + there's no
  `MAIN.encodeDock()` accessor. Either add a thin public `beckyEncode(target)` + accessor, OR (simpler,
  reuses becky's proven path) `MAIN.saveXML(tmpMlt)` then run becky's `internal/reel`/`melt` as a child
  against the `.mlt`. Pick one, implement, verify an actual output file appears.

## 5. Rules (non-negotiable — this is the becky bar)
- **"Compiles" is NOT done.** A verb is done only when you SAW it work on the running window
  (screenshot/clip the before+after). Report each verb as: verified, wired-but-not-verified, or deferred.
  Be honest — a wrong claim here is worse than an unfinished verb.
- One `beckydock.cpp` only (+ its `.h`). Don't touch the Go side; it already emits these correctly.
- Confirm every signature against the REAL header before calling it (the line numbers above are a
  guide; the fork may differ slightly). If a call doesn't exist as written, find the real one — don't guess.
- Commit on a branch in becky-shotcut, then `git checkout master && git merge --ff-only <branch>`
  (origin is upstream mltframework — do NOT push becky-shotcut; commits stay local). Update this
  file's checkboxes + a short "what I verified" note when done.
- Read `CLAUDE.md` (section 2 invariants, section 3 build) first. The full background is
  `HANDOFF-SHOTCUT-FORK.md` (session 3) and `HANDOFF-LOG.md` (top) — but THIS file is the actionable list.

## 6. WHY THE GEMMA MODEL "STOPPED RESPONDING" — hypotheses for a fresh instance (DO NOT assume it's broken code)
*(Written 2026-06-23 by the session that built the in-process model. Jordan: it worked in my tests,
failed for the verb subagent, and is now "unresponsive" for him. I did NOT debug this further — here
is the context + my best hypotheses, ranked, so a fresh instance starts ahead. The model FILE is fine;
it loaded + drove the agent correctly in my isolated tests minutes before this was written.)*

**THE LEADING HYPOTHESIS — VRAM exhaustion + a dual-load design flaw (most likely; check FIRST):**
- The GPU is an **8 GB RTX 3070 Laptop**. The model is **~4 GB on GPU** (43 layers) + KV cache + a
  ~556 MB CUDA compute buffer + the CUDA context. So the model needs **~4.5-5 GB**, and it ONLY fits
  when the GPU is nearly clean.
- **Live evidence at write time:** with Jordan's setup running there was a **stray `llama-server.exe`
  (PID 7360) AND a `becky-edit.exe` AND TWO `shotcut.exe`**, and only **~4.2 GB VRAM free**. That is
  not enough for the model, so the load stalls/fails and the Ask box hangs = "unresponsive."
- **The design flaw that makes it WORSE than before:** `cmd/becky-edit/model.go::newLocalModel` tries
  the IN-PROCESS model first, and on failure FALLS BACK to spawning the warm `llama-server`. Under VRAM
  pressure BOTH can end up holding VRAM at once (the failed in-process attempt may not fully free its
  CUDA context before the server spawns), so they **double-book** the GPU and neither gets enough.
  Before the in-process change, only the warm server ran (one ~4 GB tenant). **It worked for me because
  my tests killed every stray first (clean 7 GB free); it failed for the subagent + Jordan because
  Shotcut + leftover processes were eating the GPU.**
- **First things to try (likely fixes):** (1) kill EVERY `shotcut.exe`, `becky-edit.exe`, AND
  `llama-server.exe`, confirm `nvidia-smi` shows ~7 GB free, then relaunch ONCE via the updated
  `Open Becky Edit.bat` and test the Ask box. (2) If it still won't fit alongside Shotcut, make the
  load fit: I already added a VRAM-degrade retry to the shim (commit d857d0b - partial then CPU
  offload), but it only helps if `becky-go\becky-edit.exe` is the freshly rebuilt one
  (`scripts/build-becky-edit-llama.ps1`); a partial/CPU load is SLOWER but in-process. (3) Consider
  NOT running in-process + warm-server concurrently - pick one, or have the in-process failure path
  explicitly free CUDA (Close) before the server spawns. (4) Lower the model's footprint: fewer GPU
  layers, smaller `n_ctx` (it's 4096 in model.go; the agent prompts are small, 2048 is plenty), or a
  smaller quant.

**SECOND HYPOTHESIS - the wrong llama.dll loads (ABI mismatch -> hang/crash, NOT a clean error):**
- There are **8+ `llama.dll` builds on disk** that a fresh instance won't expect:
  `C:\llama.cpp\build\bin\llama.dll` (the RIGHT one - what becky-edit's import lib + the AVLM use),
  but ALSO `C:\llama.cpp\build\bin.bak-3306dba\`, `…\bin.bak-b9551\`, `…\ninja-build-bin\`,
  `C:\llama.cpp\build-src\bin\`, `X:\becky_breakdown\bin\`, `…\compiler_build\bin\`,
  `X:\AI-2\hj_scripts\llama-agents\qwen3-4b\`. **becky-edit.exe is LOAD-TIME linked to `llama.dll`**
  (the in-process build - confirmed it imports `llama.dll` + `ggml.dll`), so whichever `llama.dll` is
  found FIRST on PATH at startup wins. The launcher prepends `C:\llama.cpp\build\bin`, but if any other
  copy is earlier on PATH (or a `.bak` got swapped in), a version-mismatched dll loads and the model
  call can HANG rather than error cleanly. Verify the loaded one is `C:\llama.cpp\build\bin\llama.dll`
  and that its header (`C:\llama.cpp\include\llama.h`, the one the shim compiled against) matches it.

**THIRD HYPOTHESIS - becky-edit.exe is now a load-time-linked binary that DIES at startup without the
DLLs:** before the in-process change, `becky-edit.exe` was the warm-server build with NO llama
dependency - it always started. Now it imports `llama.dll`/`ggml.dll`, so if it is launched WITHOUT
`C:\llama.cpp\build\bin` on PATH (e.g., the Desktop "Becky Edit" shortcut points at a stale launcher,
not the updated `Open Becky Edit.bat`), the WHOLE bridge fails to start -> the Becky dock is dead
(no folder/search/preview/add either). (At write time becky-edit WAS running, so this wasn't the
active cause then - but a stale shortcut would reproduce it. A robust fix: make becky-edit DELAY-LOAD
llama.dll so the bridge always starts and only the agent verb degrades, OR keep
`becky-go\becky-edit.exe` as the portable warm-server build and spawn the in-process model as a
SEPARATE on-demand binary.)

**Non-obvious facts the fresh instance won't know:**
- `C:\llama.cpp\build\bin\llama-server.exe` is only **~9.7 KB** - suspiciously small for llama-server;
  it may be a wrapper/stub, so the warm-server fallback path may itself be fragile. Worth checking what
  it actually is before trusting the fallback.
- The in-process model loads LAZILY on the FIRST `agent` call (not at becky-edit startup), so
  folder/search/preview/add work fine even when the model is the problem - the symptom is ONLY the Ask
  box hanging, which reads as "the model stopped working" while the rest is fine.
- The in-process binding is build-tagged `llamacgo`; `becky-go\becky-edit.exe` must be the cgo build
  (~13.6 MB, imports llama.dll) for any of this to apply - a plain `go build`/`build-all-tools` loop
  produces the portable ~10 MB warm-server build with no llama dependency.
