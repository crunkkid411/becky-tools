# HANDOFF-SHOTCUT-FORK.md — build Shotcut on this PC + fork in the Becky dock, to completion

> **Live work log for the becky-edit host fork (started 2026-06-23, local agent).** Jordan's
> directive: I am the local model — download the Gemma models, perform the actual Shotcut fork,
> finish the build to completion, and if context fills, hand off here and continue in a new loop.
> This doc is the resumable state: check the boxes, read the live log at the bottom, continue.

## SCRUB PROXY DECISION (2026-06-27) — becky pre-generates intra-frame `.scrub` proxies

Per `HANDOFF-PROXY-SNAPPINESS.md` Step 4, the chosen Shotcut-fork proxy approach is
**(B) becky pre-generates `.scrub` files; the dock points the PREVIEW producer at them** —
NOT Shotcut's built-in Proxy feature. Reason: Shotcut's default proxy is plain long-GOP
H.264, which "still scrubs slowly"; the fix is an **intra-frame, constant-frame-rate** proxy
(every frame a keyframe → a seek decodes one frame), which becky now builds the same way for
both this fork and becky-clip.

- **Tool:** `becky-proxy --src <clip> [--out <cache dir>]` (`cmd/becky-proxy`) → writes
  `<stem>.scrub.mp4` (all-intra H.264, web/MLT-playable) and prints JSON incl. `intra_frame`,
  `cfr`. Engine: `reel.ScrubProxy` (`internal/reel/proxy.go`). Tunable via `BECKY_PROXY_CODEC`
  (`h264`|`dnxhr`|`mjpeg`) / `BECKY_PROXY_RES`. A fresh proxy is cached (mtime check) so repeat
  opens are instant.
- **Wiring (remaining, local/host work):** in `beckydock.cpp`, when a clip is opened for
  PREVIEW/timeline, shell out to `becky-proxy --src <clip> --out <case>/.beckyproxy`, read the
  `proxy` path from the JSON, and open THAT producer in the preview. **Keep the ORIGINAL path on
  the timeline clip for render/export** — forensic export must be frame-accurate to the source,
  never the low-res proxy. (This mirrors becky-clip, where `ProxyFor` already routes preview
  through `reel.ScrubProxy`.)
- **Proven (2026-06-27):** `becky-proxy --selftest` and a run on a real interview clip
  (`interview-2026-05-14.mp4`: web-safe h264 but 1 keyframe/60 frames → the old `Proxy()`
  short-circuited it) both confirm becky's proxy is `intra_frame: true` (60/60 keyframes) +
  `cfr: true`. The only open gate is Jordan confirming it *feels* smooth when scrubbed in the fork.

## ✅ SESSION 3 (2026-06-23) — IN-PROCESS Gemma-4 (llama.dll) + dimensions fix + the verb map

Two of Jordan's three asks DONE + verified; the third (remaining HostCommand verbs) has an
exact work order below.

**1. IN-PROCESS Gemma-4 QAT via llama.dll (cgo) — BUILT, WIRED, VERIFIED.** No more warm
llama-server child; the GGUF loads INTO the becky-edit process and drives the agent loop.
- `internal/llamacpp/` — a build-tagged cgo binding (`//go:build llamacgo`): `llama_shim.{c,h}`
  (thin completion shim on the new llama.cpp API: `llama_model_load_from_file` →
  `llama_init_from_model` → tokenize → `llama_decode` → `llama_sampler_*` → detokenize, greedy
  for deterministic JSON) + `binding_cgo.go` (Load/Complete/Close) + `binding_stub.go`
  (`//go:build !llamacgo`, no-op, so the DEFAULT module/CI build stays pure-Go + cgo-free).
  The `.c` carries a `//go:build llamacgo` line so `go build ./...` ignores it. **CRITICAL: this
  build-tag split is what keeps CI/cloud green** — do not import llamacpp unconditionally without it.
- `cmd/becky-edit/model.go` — `newLocalModel` now PREFERS the in-process model (Gemma chat
  template applied in Go), falling back to the warm server. `main.go` returns (model, closeFn, note).
- **Build:** `scripts/build-becky-edit-llama.ps1` generates mingw import libs (gendef+dlltool
  from `C:\llama.cpp\build\bin\llama.dll` + `ggml.dll`) and builds becky-edit.exe with
  `-tags llamacgo` + CGO to `becky-go\becky-edit.exe` (the dock's spawn path). `build-all-tools.bat`
  calls it best-effort after the loop; the loop's portable warm-server `bin\becky-edit.exe` is the fallback.
- **Runtime:** load-time-linked to llama.dll/ggml.dll, so the launcher (`Open Becky Edit.bat`) now
  puts `C:\llama.cpp\build\bin` on PATH. **Proof:** model loads 43/43 layers on CUDA in ~2s; a
  headless agent run ("find the penguin quote and add it") loaded Gemma in-process and emitted
  `search`→`add_clip`→`timeline.append` correctly. The llama.cpp build is MSVC but its `extern "C"`
  API uses the Win64 calling convention, so mingw cgo links it fine via the dlltool import libs.
- **KNOWN refinement (not blocking):** the 4B over-iterates (added the clip twice before the
  8-step cap) — tune `internal/ctlagent` to let the model emit a "done"/finish so it stops once the
  goal is met. The propose-preview-apply gate catches duplicates meanwhile.

**2. Project-dimensions bug FIXED (becky-shotcut 615dd55).** Importing a vertical clip used to
make a 1920x1080 project. `beckydock.cpp::openSourceClip` now calls `Mlt::Profile::from_producer`
(reads the file's native size) when the profile is non-explicit + the timeline is empty (first
import). **Verified: a 1080x1920 source makes a 1080x1920 30fps project.**

**3. REMAINING HostCommand verbs — work order (the only thing left of the hookup).** The Go side
already emits all of these; only the Shotcut-side call in `beckydock.cpp::executeHostCommands` is
left (they currently log "(pending host wiring)"). Source-verified map (a subagent read the headers):
- **Clip identity FIRST (enabler for all id-addressed verbs):** Shotcut addresses clips by
  (trackIndex, clipIndex) which shift on edits. Store becky-id → `QUuid` when a clip is appended
  (`MAIN.timelineClipUuid(track, clipIndex)`, mainwindow.h:106) and resolve via
  `MultitrackModel::findClipByUuid(uuid, &track, &clip)` (multitrackmodel.h:127) before each edit.
- **NAMING INVERSION (verified):** becky `timeline.remove` (leave gap) → `MAIN.timelineDock()->lift(t,c)`;
  becky `timeline.ripple_delete` (close gap) → `->remove(t,c)`. (Shotcut's remove == ripple.)
- `timeline.move {id,track,pos}` → `moveClip(fromTrack,toTrack,clip,posFrames,ripple)` (timelinedock.h:158).
- `timeline.trim {id,in/out}` → `trimClipIn/trimClipOut(track,clip,oldClip,deltaFrames,ripple,roll)`
  (.h:160/162) — delta is a FRAME delta (read InPointRole/OutPointRole or `getClipInfo`).
- `timeline.split {id,at}` → push `Timeline::SplitCommand(*model, {track}, {clip}, atFrames)` (timelinecommands.cpp:1119).
- `timeline.add_track {kind}` → `addVideoTrack()` / `addAudioTrack()` (return new index, .h:136/137).
- `timeline.select {ids}` → `setSelection(QList<QPoint{clip,track}>)` (.h:72). `timeline.marker` →
  `markersModel()->append(Markers::Marker{start=end=frame,text,color})` (markersmodel.h:85).
- `filter.add/set/remove` → `MAIN.filterController()`: `setProducer(target)` → `attachedModel()->add(metadata("<service>"))`
  → `currentFilter()->set(prop,val)` (filtercontroller.h / attachedfiltersmodel.h / qmlfilter.h).
- `track.mute` → `toggleTrackMute(track)` (.h:152; read IsMuteRole to force a state). `track.gain` →
  NO single API; attach a `volume` filter (set `level`=dB) via the filter path.
- `overlay.set` → `dynamictext` filter via the filter path (`argument`=text, geometry/size/colour).
- `player.grab_frame {source,at}` → ASYNC: seek, `Mlt::VideoWidget::requestImage()`, save in the
  `imageReady` slot (pattern: mainwindow.cpp:5556 on_actionExportFrame_triggered).
- `render.export {clips,out}` → NEEDS FORK ADDITIONS: a public `EncodeDock` encode method +
  `MAIN.encodeDock()` accessor (both private now). Interim: `MAIN.saveXML(path)` then run `melt`/becky's
  `internal/reel` as a child against the `.mlt`.
All are GUI-thread-safe (the dock is on the GUI thread); positions are FRAMES (use the dock's `secToFrame`).

---

## ✅ SESSION 2 (2026-06-23) — ALL reported bugs FIXED + verified on the real GUI

The local agent drove Jordan's actual mouse/keyboard (PowerShell Win32 `SetCursorPos`/
`mouse_event` + screen-capture, scripts in scratchpad) to reproduce + verify every fix on
the running window. **Answer to Jordan's question: this runs on NATIVE WINDOWS** — MSYS2/
MINGW64 produces a native `shotcut.exe`; WSL2 is NOT involved.

**Root cause of "error saving a new project" AND "preview/add failed" was the SAME thing:
Shotcut found ZERO MLT plugins.** Shotcut resolves its MLT repository from the exe location
(`build/lib/mlt`), IGNORING `MLT_REPOSITORY` (it calls `Mlt::Factory::init()` with NULL).
That dir was empty → no producers/consumers → saving a project failed ("There was an error
saving."), and opening/previewing a clip did nothing. The HANDOFF's "preview wired" only
proved the command *flowed*, never that media played.

What was fixed (all verified by screenshot on a real case folder):
1. **MLT plugins deployed** → new project now SAVES; preview now PLAYS the clip. `deploy-mlt.sh`
   (in becky-shotcut) automates copying the MSYS2 modules + data into `build/lib/mlt` + `build/share/mlt`.
2. **`Qt6Core5Compat.dll` + `libebur128.dll` installed** (`pacman -S mingw-w64-x86_64-qt6-5compat
   mingw-w64-x86_64-libebur128`) → `libmltqt6` (qtblend) + `libmltplus` load. Without qt6-5compat,
   libmltqt6 resolved ICU from **kdenlive's incompatible `icuuc78.dll`** on PATH and popped a hard
   "Entry Point Not Found" dialog; without it loaded, every timeline add popped "could not find the
   qtblend plugin." Both gone now. (Only `libmltrtaudio.dll` stays removed — unused, missing dep.)
3. **Add-to-timeline (double-click) rewired** (`beckydock.cpp`): was using `MAIN.open(QString)`
   (the document-open path → "save your changes?" prompt, clip never landed). Now uses the producer
   overload `MAIN.open(Mlt::Producer*, bool)` + `timelineDock()->append(-1)` (auto-creates the track).
   **Verified: the clip lands on a "V1" track, no prompts.** Preview uses the same producer path.
4. **Dock layout**: was a squished sliver. Gave it a minimum size (usable even under Jordan's restored
   layout) + tabified with the Playlist. For the full new default, do **View > Layout > Restore Default
   Layout** once.

**Rebuilt `shotcut.exe` (commit becky-shotcut acffd2b). Launch = `Open Becky Edit.bat` (unchanged).**

**Left for the NEXT increment (NOT blocking — the core forensic loop works):** wire the remaining
HostCommands that still log "(pending host wiring)" — `timeline.move/trim/split/remove`, `filter.*`,
`track.mute/gain`, `overlay.set`, `render.export`, `player.grab_frame`, `vision.*`. The seam + the
deterministic Go side for all of these is already proven; only the Shotcut-side call mapping remains
(table below). Also: run the AI **Ask** agent against the real warm Gemma (download + warm it).

---

## ✅ BUILD COMPLETE (2026-06-23) — the forked Shotcut with the Becky dock RUNS

`shotcut.exe` (becky-shotcut commit `487f41b`) is built and was launched on the PC: the window
opens ("Untitled - Automatic - Shotcut"), the **Becky dock** is compiled in + visible, and it
**spawns the becky-edit Go bridge** (both `shotcut.exe` + `becky-edit.exe` confirmed running — the
dock's QProcess found the bridge and connected). Full stack live: forked Shotcut → BeckyDock (C++)
→ becky-edit (shared state + Gemma agent + tools).

**The recipe that worked (no multi-hour from-source build needed):**
1. MSYS2 deps via a **clean interactive terminal** (the `-Syu` deadlocks non-interactively): drove a
   real `msys2_shell.cmd -mingw64` via keyboard automation → `pacman -Syuu --noconfirm --overwrite "*"`
   (full system upgrade, gcc 15→16) → then the Qt6 + codec stack (`tmp/install-qt6.sh`).
2. **KEY SHORTCUT:** `pacman -S mingw-w64-x86_64-mlt mingw-w64-x86_64-frei0r-plugins` — MSYS2 ships
   **MLT 7.36.1**, which satisfies Shotcut master's `mlt++-7 >= 7.36.0`. This skips building
   FFmpeg/MLT/OpenCV from source entirely. Build is then just Shotcut itself.
3. `cd build && cmake -G Ninja -DCMAKE_BUILD_TYPE=Release -DSHOTCUT_VERSION=26.06.23 .. && ninja`
   → `build/src/shotcut.exe` (configured cleanly; only optional ClangFormat missing).
4. Runtime: `cp build/CuteLogger/libCuteLogger.dll build/src/`; put the qml under
   `build/src/share/shotcut/qml` (copy of `src/qml`); set `MLT_REPOSITORY=/mingw64/lib/mlt`
   `MLT_DATA=/mingw64/share/mlt`.

**Relaunch (in a MINGW64 shell):**
```bash
cd /x/AI-2/becky-shotcut/build/src && MLT_REPOSITORY=/mingw64/lib/mlt MLT_DATA=/mingw64/share/mlt ./shotcut.exe
```
The dock resolves the bridge at `X:/AI-2/becky-tools/becky-go/becky-edit.exe` automatically.

**What's wired now:** single-click a quote = preview (`player.open_seek_play`), double-click = add to
timeline (`timeline.append`), plus folder open / transcript search / the AI agent (propose-preview-
apply). Remaining host commands (filter.*/track.*/move/trim/split/render) log "(pending host wiring)"
and are the incremental next layer — the seam is proven end to end.

## Goal
Make `becky-edit` real on the desktop: build **Shotcut** (Qt6/QML/MLT) on this Windows PC, fork in
a **Becky dock** that talks to the already-built `cmd/becky-edit` Go bridge over a local socket, and
map each becky-edit `HostCommand` to its real Shotcut call. The Go engine half is DONE + proven
(`SPEC-BECKY-NLE.md §8`); this is the host-dependent half.

## Environment found on this machine (2026-06-23)
- **MSYS2 installed** at `C:\msys64` (pacman present; `mingw64/bin/gcc.exe` present). This is
  Shotcut's documented Windows build env.
- **Missing (install via pacman in the MINGW64 shell):** Qt6, mingw64 cmake, ninja, and the
  MLT/FFmpeg dep stack. Shotcut's CI uses a **prebuilt MLT bundle** to skip building MLT/FFmpeg:
  `https://s3.amazonaws.com/misc.meltymedia/shotcut-build/mlt-prebuilt-mingw64-v6.txz`.
- **Shotcut cloned to** `X:\AI-2\becky-shotcut` (shallow). This is the fork (separate GPL-3 repo,
  NOT under becky-go). The Becky dock + socket bridge are added here.
- Disk: X: ~265G free, C: ~217G free. Adequate.
- Recipe (from `research/shotcut-api.md §7`): install MSYS2 -> `pacman -S` mingw64 toolchain + Qt6 +
  deps -> drop in the prebuilt MLT bundle -> `bash scripts/build-shotcut-msys2.sh` (CMake + Ninja).

## Checkboxed work order (update as you go)

### Track A - build environment + stock Shotcut
- [x] A1. Clone Shotcut to `X:\AI-2\becky-shotcut` (done, shallow).
- [ ] A2. Read `scripts/build-shotcut-msys2.sh` + `.github/workflows/build-windows.yml` for the
      EXACT pacman package list + MLT bundle URL + cmake invocation.
- [ ] A3. In the MINGW64 shell (`C:\msys64\msys2_shell.cmd -mingw64 -defterm -no-start`):
      `pacman -Syu` then `pacman -S` the package list (Qt6, cmake, ninja, etc.).
- [ ] A4. Download + extract the prebuilt MLT bundle to where the script expects it.
- [ ] A5. Run `bash scripts/build-shotcut-msys2.sh` (or the cmake steps directly). Iterate on errors.
- [ ] A6. Launch the built `shotcut.exe`; confirm it opens + can open a clip. (Go/no-go spike done.)

### Track B - the Becky dock fork code (can be written before A6 completes)
- [ ] B1. `src/docks/beckydock.{h,cpp}` - `QDockWidget` subclass (copy `TimelineDock`), QML-hosted.
- [ ] B2. `src/qml/becky/BeckyDock.qml` - the panel UI (folder open, transcript/quote search list,
      chat box, propose/preview). Single-click quote = preview; double-click = add to timeline.
- [ ] B3. `src/becky/beckybridge.{h,cpp}` - a `QObject` that spawns `becky-edit` (QProcess) and
      speaks the NDJSON wire (seam: `{type,id,name,args}` / `{type,id,ok,data,error}`); exposes
      Q_INVOKABLE methods to QML + signals for responses/events. ALL Shotcut calls it makes must be
      marshalled to the GUI thread via `QMetaObject::invokeMethod(..., Qt::QueuedConnection)`.
- [ ] B4. Map each becky-edit HostCommand -> Shotcut call (table below) in the bridge's command sink.
- [ ] B5. Emit host signals (`positionChanged`/`selectionChanged`/`appended` + MultitrackModel
      roles) back to becky-edit as `event`s so the shared state stays synced on manual edits.
- [ ] B6. Register `BeckyDock` in `MainWindow::setupAndConnectDocks()` (`new BeckyDock(this)` +
      `addDockWidget` + `ui->menuView->addAction(...->toggleViewAction())`).
- [ ] B7. Add the new sources to the build (CMakeLists.txt / the `.pro`), rebuild.

### Track C - verify end to end
- [ ] C1. Open a real case folder in the Becky dock; single-click a quote -> preview seeks+plays.
- [ ] C2. Double-click quotes -> clips land on the timeline; trim/reorder; export to render/.
- [ ] C3. Run the AI agent against the REAL warm Gemma (download via scripts/get-gemma4-qat.ps1).
- [ ] C4. Manual-edit-then-agent: edit a clip by hand, confirm becky-edit's state mirrors it.

## HostCommand -> Shotcut call map (from research/shotcut-api.md)
| becky-edit HostCommand | Shotcut/MLT call |
|---|---|
| `player.open_seek_play {source,in,out}` | `MAIN.open(path)` -> on `producerOpened()`: `Player::setIn/setOut(frame)`, `seek(in)`, `play()` |
| `player.seek {frame}` | `Player::seek(frame)` |
| `player.grab_frame {source,at}` | open producer + `Player` grab / `QmlApplication` save-frame |
| `timeline.append {track,source,in,out}` | `TimelineDock::append(trackIndex, xmlOrResource)` (producer trimmed to in/out) |
| `timeline.overwrite/insert` | `TimelineDock::overwrite/insert(...)` |
| `timeline.remove {id}` | `TimelineDock::remove(trackIndex, clipIndex)` (undo via QUndoStack) |
| `timeline.move/trim/split` | `TimelineDock` move/trim/split commands |
| `timeline.select {ids}` | `TimelineDock::setSelection(...)` |
| `filter.add/set/remove` | `AttachedFiltersModel::add(QmlMetadata*)` + `QmlFilter::set(name,value)` |
| `track.mute/gain` | MultitrackModel track ops |
| `render.export {clips,out}` | write MLT XML (becky already does) -> `EncodeDock` / `melt`, OR becky's own `internal/reel` |
| read-back | `MultitrackModel` roles (`ResourceRole`/`StartRole`/`DurationRole`/`InPointRole`/`OutPointRole`) + signals |

## RESUME HERE (state as of 2026-06-23, first session)

**Done:**
- Gemma **E4B QAT model downloaded** (`models/gemma4/gemma-4-E4B-it-qat-UD-Q4_K_XL.gguf`, 4.2 GB +
  `mmproj-BF16.gguf`). The AVLM upgrade is now LIVE (config resolves QAT-first). `becky-edit.exe`
  built + selftest passes.
- **Shotcut cloned** to `X:\AI-2\becky-shotcut`. **MLT prebuilt bundle** extracted to MSYS2
  `$HOME` (`/home/only1/{bin,lib,include,Qt}`) — note: this is RUNTIME DLLs, not MLT dev libs, so
  the build script still compiles MLT/FFmpeg/OpenCV from source.
- **Track B (the fork) is WRITTEN + COMMITTED** in becky-shotcut (commit `55febcf`):
  `src/docks/beckydock.{h,cpp}`, `src/qml/becky/BeckyDock.qml`, `src/CMakeLists.txt` (sources added),
  `src/mainwindow.cpp` (dock registered in setupAndConnectDocks + include). Core host commands wired:
  `player.open_seek_play`, `timeline.append`, `player.seek`; the rest log "(pending host wiring)".

**In progress:** MSYS2 `pacman -Syu` + the ~50 build packages (Qt6 stack + codecs) — the big
download. Watch: `ls /c/msys64/mingw64/bin/qmake6.exe` (present == done). The install script is
`scratchpad/install-deps.sh`; re-run it in a MINGW64 shell if `-Syu` needed a restart:
`MSYSTEM=MINGW64 /c/msys64/usr/bin/bash -lc "pacman -S --needed --noconfirm <list from build-windows.yml>"`.

**NEXT (once Qt6 is installed):**
1. Build the deps + a stock Shotcut once (proves the env). The official script clones a FRESH
   shotcut, so to build OUR FORK, build deps via the script then cmake our fork separately, OR
   replace `$HOME/build/src/shotcut` with `X:\AI-2\becky-shotcut` and `ACTION_GET=0` for shotcut.
   Honest fast path once deps exist (run in MINGW64 shell):
   ```bash
   cd /x/AI-2/becky-shotcut
   export QTDIR="$(pkg-config --variable=prefix Qt6Core)"
   export PKG_CONFIG_PATH="$HOME/build/Shotcut/lib/pkgconfig:$PKG_CONFIG_PATH"   # MLT .pc after deps build
   mkdir -p build && cd build
   cmake -G Ninja -DCMAKE_PREFIX_PATH="$QTDIR;$HOME/build/Shotcut" \
         -DCMAKE_INSTALL_PREFIX="$HOME/build/Shotcut" -DSHOTCUT_VERSION=26.06.23 ..
   ninja            # iterate compile errors on beckydock.cpp against the real API
   ```
   NOTE: MLT must be built first (it's not in MSYS2). Easiest: run `bash scripts/build-shotcut-msys2.sh`
   with a conf that builds everything ONCE, then rebuild only our fork's shotcut against the prefix.
2. **Compile-iterate the fork** — likely fixes: `MLT.profile().fps()` accessor name, `QmlView`
   include path, `Logger.h` include, `Player::setIn/setOut/seek/play` exact availability. The API was
   confirmed present (mainwindow.h: `open(QString)`, `player()`, `timelineDock()`, `producerOpened`;
   player.h: `setIn/setOut/seek/play`) but signatures may need tweaks.
3. Set `BECKY_EDIT_BIN=X:\AI-2\becky-tools\becky-go\becky-edit.exe` (or copy it beside shotcut.exe)
   so the dock finds the bridge.
4. Launch `shotcut.exe` → View menu → Becky dock → Open a real case folder → single-click a quote
   (preview seeks+plays) → double-click (clip lands on timeline). Then Track C verifications.

## DEPS SAGA + the real blocker (read before touching MSYS2 again)

The MSYS2 build environment on this PC is **behind the rolling repo** and that is the gating issue:
1. First `pacman -Syu` **deadlocked** on the in-use `msys2-runtime` self-upgrade (classic MSYS2
   non-interactive hang). Killing it **corrupted the pacman local DB** (missing `desc` for
   `msys2-runtime-3.6.9-2` + `xz-5.8.1-1`).
2. **DB REPAIRED** (Jordan's MSYS2 is healthy again — `pacman -Q` works, 147 pkgs): moved the two
   desc-less local-db dirs to `C:\msys64\tmp\pacman-broken-backup\`, then `pacman -U` the cached
   `.pkg.tar.zst` for both → clean descs written. The runtime is now current (3.6.9-2), so the
   deadlock cause is GONE.
3. **Partial-install conflicts** (why a targeted `-S` won't work): (a) `mingw-w64-x86_64-toolchain`
   conflicts with Jordan's **`-git` crt/headers/winpthreads** variants; (b) current Qt6 needs
   `gcc-libs 16.1.0` but he has `gcc 15.1.0`. **Conclusion: a full `pacman -Syu` is REQUIRED** (gcc
   15→16, etc.). That is now running (no deadlock expected since the runtime is current).

**If the full `-Syu` finishes clean → install the Qt6 build stack:** re-run
`scratchpad/install-deps3.sh` (the list WITHOUT the `toolchain` meta — gcc 15→16 is upgraded by the
-Syu, so gcc-libs matches). Verify `ls /c/msys64/mingw64/bin/qmake6.exe`.
**If `-Syu` stalls/half-applies again:** do the documented MSYS2 recovery — close ALL MSYS2/Git-Bash
processes, open a fresh `C:\msys64\msys2_shell.cmd`, run `pacman -Suu` until it reports nothing to do
(this is the one step that genuinely needs a clean restart and may want Jordan to run it). Then the
deps3 install, then the build.

**THEN the build** (multi-hour, from source): `bash scripts/build-shotcut-msys2.sh` builds FFmpeg/
MLT/OpenCV/Shotcut. To build OUR FORK rather than a fresh upstream clone, after the deps+stock build,
cmake the fork: see the "NEXT" block above (build dir + `cmake -G Ninja -DCMAKE_PREFIX_PATH=...`).

## Live log (newest at bottom)
- 2026-06-23: env surveyed (MSYS2 present, Qt6/cmake/ninja missing); Shotcut clone + Gemma E4B QAT
  download done; MLT prebuilt bundle extracted to $HOME. **Becky dock fork written + committed
  (becky-shotcut 55febcf).**
- 2026-06-23: deps hit the MSYS2 runtime-upgrade deadlock; killing pacman corrupted the local DB;
  **DB repaired** (see DEPS SAGA). Found the real blocker: MSYS2 is behind the repo (gcc 15 vs 16) +
  has -git toolchain variants → a **full `pacman -Syu` is required**, now running in the background.
  The from-source build is the step after that. Continued via the background task / a fresh loop.
