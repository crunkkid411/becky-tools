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
- [ ] `timeline.add_track {kind}` -> `kind=="audio" ? TD->addAudioTrack() : TD->addVideoTrack()`
      (timelinedock.h:136/137, return the new index). **Verify:** a new track row appears.
- [ ] `timeline.remove {id}` (LEAVE a gap) -> resolve id->(t,c), `TD->lift(t, c)` (timelinedock.h:144).
      **NOTE the inversion:** Shotcut `lift` = leave blank. **Verify:** the clip becomes a blank, gap stays.
- [ ] `timeline.ripple_delete {id}` (CLOSE the gap) -> `TD->remove(t, c)` (timelinedock.h:142 — Shotcut
      `remove` == ripple-delete). **Verify:** clip gone AND the following clips shift left.
- [ ] `timeline.select {ids}` -> build `QList<QPoint>` of `QPoint(clip, track)` per id, `TD->setSelection(list)`
      (timelinedock.h:72). **Verify:** the clip(s) show selected (highlighted border).
- [ ] `timeline.marker {pos,text}` -> `Markers::Marker m; m.start=m.end=frame(pos); m.text=text;
      m.color=Settings.markerColor(); TD->markersModel()->append(m);` (markersmodel.h:85;
      `#include "models/markersmodel.h"`, `settings.h`). **Verify:** a marker tick appears on the ruler.
- [ ] `track.mute {track,on}` -> read the model's `IsMuteRole` for that track; if it differs from `on`,
      `TD->toggleTrackMute(track)` (timelinedock.h:152). **Verify:** the track's mute icon toggles.
- [ ] `timeline.move {id,track,pos}` -> resolve id->(fromTrack,clip),
      `TD->moveClip(fromTrack, args.track, clip, frame(pos), /*ripple*/false)` (timelinedock.h:158).
      **Verify:** the clip moves to the new position/track.
- [ ] `timeline.trim {id,in|out}` -> resolve id->(t,c); read current in/out via the model
      (`InPointRole`/`OutPointRole` or `getClipInfo(t,c)`), compute a FRAME DELTA, then
      `TD->trimClipIn(t,c,c,delta,false,false)` or `TD->trimClipOut(t,c,delta,false,false)`
      (timelinedock.h:160/162 — delta is a frame delta, NOT an absolute). **Verify:** clip length changes.
- [ ] `timeline.split {id,at}` -> resolve id->(t,c),
      `MAIN.undoStack()->push(new Timeline::SplitCommand(*TD->model(), std::vector<int>{t},
      std::vector<int>{c}, frame(at)))` (`#include "commands/timelinecommands.h"`, `<vector>`;
      ctor at timelinecommands.cpp:1119). **Verify:** the clip splits into two at the playhead.

All of the above are GUI-thread-safe and undo-wrapped (they push QUndoCommands — good, keep it).

## 4. Phase C — the harder verbs (wire what's clean; DOCUMENT precisely what needs more)
These need more than a one-liner. Do them if you can verify them; otherwise leave the
`(pending host wiring)` log AND write the exact remaining step in this file so it's not lost.
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
