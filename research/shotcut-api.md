# Shotcut API reference — for driving it from a Go bridge ("becky-edit")

> Purpose: a grounded reference so the Go contracts for becky-edit can map cleanly onto the
> **real** Shotcut/MLT API. Every symbol below was verified against the current `master` source of
> `github.com/mltframework/shotcut` (Qt6/QML, MLT+FFmpeg). File paths and symbol names are quoted
> from the actual repo; where a claim came from docs rather than source it is marked.
>
> Bottom line up front: **Shotcut has NO runtime dock/panel plugin API.** Its only runtime
> extension surface is *filter* plugins (QML `meta*.qml`/`ui.qml` dropped into
> `share/shotcut/qml/filters`). A custom "Becky" dock **must be added by forking** and following
> the existing `QDockWidget` pattern. The good news: every dock already exposes its C++ controller
> to its own QML via `setContextProperty`, and the timeline/player/filter models are rich, fully
> scriptable `QObject`s with the exact verbs we need (`open`, `seek`, `play`, `append`,
> `overwrite`, model roles for read-back). So a fork + one dock + a `QLocalSocket` C++ bridge is a
> well-trodden path, not a fight against the architecture.

---

## 1. Custom dock/panel registration

**There is no runtime plugin mechanism for docks.** The official "How to Make a Plugin"
(shotcut.org/notes/make-plugins) documents *only* filter plugins (backend = MLT/frei0r/LADSPA/
libavfilter in C/C++; frontend = QML `meta*.qml` + `ui.qml` + optional VUI overlay), copied to
`share/shotcut/qml/filters/` and discovered at runtime. The docs explicitly do **not** offer a way
to add a custom dock/panel without forking. So: **fork Shotcut, add a `QDockWidget` subclass.**

### The dock pattern to copy

Real dock classes live in `src/docks/`: `TimelineDock` (`timelinedock.h/.cpp`), `FiltersDock`,
`KeyframesDock`, `EncodeDock`, `JobsDock`, `NotesDock`, `SubtitlesDock`, `PropertiesDock`,
`PlaylistDock`, `RecentDock`, `FilesDock`. They subclass `QDockWidget`. `TimelineDock` is the model
to copy because it is QML-hosted and exposes its controller to QML.

Registration happens in `MainWindow::setupAndConnectDocks()` in `src/mainwindow.cpp`:

```cpp
m_timelineDock = new TimelineDock(this);
// ...
addDockWidget(Qt::BottomDockWidgetArea, m_timelineDock);          // dock area
ui->menuView->addAction(m_timelineDock->toggleViewAction());      // View-menu toggle
tabifyDockWidget(m_keyframesDock, m_timelineDock);                // group as a tab
```

To add a `BeckyDock`: create `src/docks/beckydock.{h,cpp}` subclassing `QDockWidget`, then add the
three lines above (`new BeckyDock(this)`, `addDockWidget(...)`, `ui->menuView->addAction(...)`,
optionally `tabifyDockWidget(...)`) in `setupAndConnectDocks()`.

### How QML is wired to C++ in Shotcut

Two mechanisms, both real:

**(a) Global context properties + registered types** — set once in
`src/qmltypes/qmlutilities.cpp` on the shared engine:

```cpp
context->setContextProperty("settings",    &ShotcutSettings::singleton());
context->setContextProperty("application", &QmlApplication::singleton());
context->setContextProperty("profile",     &QmlProfile::singleton());
```

So **every** QML file can call `application.*`, `settings.*`, `profile.*`. `QmlApplication`
(`src/qmltypes/qmlapplication.h`) exposes Q_INVOKABLEs like `audioChannels()`,
`showStatusMessage(msg, timeoutSeconds)`, `timeFromFrames(int)`, `clockFromFrames(int)`,
`copyAllFilters()`, plus Q_PROPERTYs `OS`, `mainWinRect`, `devicePixelRatio`.

Registered QML types (import `org.shotcut.qml 1.0`): `Filter`→`QmlFilter`, `Metadata`→`QmlMetadata`,
`File`→`QmlFile`, `Extension`→`QmlExtension`, `ExtensionFile`→`QmlExtensionFile`,
`KeyframesModel`, `SubtitlesModel`, `Utilities`→`QmlUtilities`. (`Extension`/`ExtensionFile` back
the filter-plugin loader, **not** docks.) `Shotcut.Controls 1.0` registers UI helpers
(`FileDialog`, `MessageDialog`, `ColorDialog`, `EditContextMenu`, etc.).

**(b) Per-dock context properties** — each dock injects its own controller into its QML view.
From `TimelineDock` ctor (`src/docks/timelinedock.cpp`):

```cpp
m_quickView.rootContext()->setContextProperty("view",      new QmlView(&m_quickView));
m_quickView.rootContext()->setContextProperty("timeline",  this);          // the TimelineDock itself
m_quickView.rootContext()->setContextProperty("multitrack",&m_model);      // the MultitrackModel
m_quickView.rootContext()->setContextProperty("markers",   &m_markersModel);
m_quickView.rootContext()->setContextProperty("subtitlesModel", &m_subtitlesModel);
```

This is the pattern for the Becky dock: host a `QQuickWidget`/`QQuickView`
(`QmlUtilities::sharedEngine()`), and `setContextProperty("becky", this)` so the QML panel calls
straight into our C++ `BeckyDock`, which in turn talks to the Go bridge. Our dock's C++ also has
direct access to `MAIN` (`MainWindow::singleton()`), `MLT` (`Mlt::Controller::singleton()`), and the
other docks' models — so most "drive Shotcut" verbs are plain C++ calls, with QML only for the
panel UI.

---

## 2. Driving the PREVIEW (open file → seek → play)

The preview/transport is `Player` (`src/player.h`), reached as `MAIN.player()`. The underlying
engine is the MLT controller, `MLT` (`#define MLT Mlt::Controller::singleton()` in
`src/mltcontroller.h`).

**Opening a file** is done at the MainWindow level, not the Player directly:

```cpp
// src/mainwindow.h
bool open(QString url, const Mlt::Properties* = nullptr, bool play = true, bool skipConvert = false);
void open(Mlt::Producer *producer, bool play = true);
```

Internally `MainWindow::open(QString)` calls `MLT.open(urlToOpen, url, skipConvert)` (creates the
`Mlt::Producer`), checks `MLT.producer() && MLT.producer()->is_valid()`, then emits:

```cpp
emit producerOpened();   // signal: void producerOpened(bool withReopen = true);
```

`Player::onProducerOpened(bool play=true)` (a public slot) is connected to this and loads the
producer into the transport. So the canonical "open arbitrary file in the preview" call from our
C++ dock is simply:

```cpp
MAIN.open(QStringLiteral("C:/clips/X.mp4"));   // creates producer, opens in player, starts playing
```

**Seek + play** are `Player` slots:

```cpp
// src/player.h (public slots)
void play(double speed = 1.0);
void pause(int position = -1);
void stop();
void seek(int position);          // position is a FRAME number
void onProducerOpened(bool play = true);
// public:
void setPauseAfterOpen(bool pause);   // open without auto-playing
int  position() const;
void setIn(int);  void setOut(int);   // set the source in/out points (frames)
```

The matching `MLT` (Controller) methods exist too (`MLT.play(speed)`, `MLT.pause(position)`,
`MLT.seek(int)`), but going through `Player` keeps the transport UI in sync.

### Exact call sequence: "open file X, seek to frame T, play (a sub-range)"

```cpp
Player *p = MAIN.player();
p->setPauseAfterOpen(true);          // don't auto-play on open
MAIN.open("C:/clips/X.mp4");         // creates producer + emits producerOpened()
// after producerOpened fires and the player has the producer:
p->setIn(inFrame);                   // optional: trim source in
p->setOut(outFrame);                 // optional: trim source out
p->seek(T);                          // jump to frame T
p->play();                           // play from T
```

Frames are the universal unit. Convert seconds→frames with the project profile fps
(`MLT.profile().fps()`), or use `application.timeFromFrames(int)` / `clockFromFrames(int)` for
display. Relevant signals to confirm state: `Player::seeked(int)`, `played(double)`, `paused(int)`,
`stopped()`, `endOfStream()`.

> Note: `MainWindow::open` is **GUI-thread** work. The Go bridge must marshal these onto the Qt
> main thread (e.g. `QMetaObject::invokeMethod(..., Qt::QueuedConnection)` from the socket-reader
> thread). This is the single biggest correctness gotcha for the C++ side.

---

## 3. Appending a clip to the TIMELINE

The timeline is owned by `TimelineDock` (`MAIN`'s `m_timelineDock`; from our dock,
`MAIN.timelineDock()` or hold a pointer). Its data is `MultitrackModel`
(`src/models/multitrackmodel.h`), reachable as `timeline->model()` or the QML `multitrack` context
property. **All structural edits go through `TimelineDock` slots, which wrap `MultitrackModel`
operations in `QUndoCommand`s pushed onto `MAIN.undoStack()`** — so undo/redo "just works."

### Track selection & playhead

```cpp
// src/docks/timelinedock.h
int  currentTrack() const;            void setCurrentTrack(int);   // active track index
int  position() const;                void setPosition(int);       // playhead (frames)
int  clipCount(int trackIndex) const;
int  clipIndexAtPlayhead(int trackIndex = -1);
int  addVideoTrack();  int addAudioTrack();   // returns new track index
int  addTrackIfNeeded(TrackType trackType);
```

### Append / insert / overwrite (the public slots that do the editing, with undo)

```cpp
// src/docks/timelinedock.h  (public slots)
void append(int trackIndex);                                           // appends MLT.producer() (the open source) to track end
void insert (int trackIndex, int position = -1, const QString &xml = QString(), bool seek = true);
void overwrite(int trackIndex, int position = -1, const QString &xml = QString(), bool seek = true);
void appendFromPlaylist(Mlt::Playlist *playlist, bool skipProxy, bool emptyTrack);
```

- `append(trackIndex)` appends the **currently open source producer** (`MLT.producer()`, trimmed to
  its current in/out) to the end of that track. This is the literal "double-click a quote → clip to
  timeline" verb: open the source with in/out set (§2), then `timeline->append(currentTrack())`.
- `insert`/`overwrite` take an **MLT XML string** (`xml`) describing the clip and a frame
  `position` (default `-1` = playhead). XML lets you place a fully-specified clip (with in/out,
  filters) at the playhead without first loading it into the preview.

Under the hood these call `MultitrackModel`:

```cpp
// src/models/multitrackmodel.h
int  appendClip   (int trackIndex, Mlt::Producer &clip, bool seek = true, bool notify = true);
int  insertClip   (int trackIndex, Mlt::Producer &clip, int position, bool rippleAllTracks, bool seek = true, bool notify = true);
void overwriteClip(int trackIndex, Mlt::Producer &clip, int position, bool seek = true);
```

The undo wrappers are `QUndoCommand` subclasses in `src/commands/timelinecommands.{h,cpp}` (e.g.
`Timeline::AppendCommand`, `Timeline::OverwriteCommand`, `Timeline::InsertCommand`,
`Timeline::TrimClipInCommand`, etc.), pushed via `MAIN.undoStack()->push(...)`. **Always drive
edits through the `TimelineDock` slots, not raw `MultitrackModel`, so undo + UI refresh are
correct.**

### In/out points

A clip's in/out are properties of its `Mlt::Producer` (`clip.set_in_and_out(in, out)` / read via
`get_in()`/`get_out()`). For the preview source they are set with `Player::setIn/setOut`. In the
model they surface as the `InPointRole`/`OutPointRole` roles (§4).

---

## 4. READING state back (critical for the Go shared-state mirror)

This is the strongest part of the API for us — the timeline is a `QAbstractItemModel` with rich
roles, and every mutation emits a signal.

### Timeline structure — `MultitrackModel` roles (`src/models/multitrackmodel.h`)

```
NameRole, CommentRole, ResourceRole, ServiceRole, IsBlankRole, StartRole, DurationRole,
InPointRole, OutPointRole, FramerateRole, IsMuteRole, IsHiddenRole, IsAudioRole,
AudioLevelsRole, IsCompositeRole, IsLockedRole, FadeInRole, FadeOutRole, IsTransitionRole,
FileHashRole, SpeedRole, IsFilteredRole, IsTopVideoRole, IsBottomVideoRole, IsTopAudioRole,
IsBottomAudioRole, AudioIndexRole, GroupRole, GainRole, GainEnabledRole
```

Per clip you can read: source file (`ResourceRole`), MLT service (`ServiceRole`), timeline start
(`StartRole`) and length (`DurationRole`), trim (`InPointRole`/`OutPointRole`), whether it's a gap
(`IsBlankRole`), name, speed, fade in/out, whether it has filters (`IsFilteredRole`), and the
identity hash (`FileHashRole`). Tracks are the top-level rows; clips are child rows. Helpers:
`MultitrackModel::trackList()`, `tractor()` (the root `Mlt::Tractor`),
`TimelineDock::producerForClip(trackIndex, clipIndex)`, `clipCount(trackIndex)`.

> Cleanest bulk read for the Go mirror: call `MLT.XML()` / `Controller::XML(Service*)` to serialize
> the whole project (or a service) to **MLT XML** and parse it Go-side. Use the model roles only
> for incremental, per-clip deltas. (See §6 for the XML path.)

### Playhead & selection — `TimelineDock`

```cpp
int position() const;                                  // playhead frame
const QList<QPoint> selection() const;                 // QPoint(clipIndex, trackIndex) per selected clip
QVariantList selectionForJS() const;                   // same, for QML
int selectedTrack() const;  bool isMultitrackSelected() const;
Mlt::Producer producerForClip(int trackIndex, int clipIndex);
int clipIndexAtPlayhead(int trackIndex = -1);
int clipIndexAtPosition(int trackIndex, int position);
```

### Signals to keep the mirror in sync

`TimelineDock`: `positionChanged(int)`, `selectionChanged()`, `currentTrackChanged()`,
`seeked(int)`, `durationChanged()`, `clipMoved(...)`, `clipOpened(Mlt::Producer*)`,
`selected(Mlt::Producer*)`.
`MultitrackModel`: `appended`, `inserted`, `overWritten`, `modified`, `created`, `closed`,
`seeked`, `durationChanged` (plus the standard `rowsInserted/rowsRemoved/dataChanged`).
`Player`: `seeked(int)`, `played(double)`, `paused(int)`, `stopped()`.
`MainWindow`: `producerOpened(bool)`.

Our C++ dock connects to these and forwards a compact NDJSON delta to the Go bridge on each.

### Applied filters / params

The filters shown for the *currently selected* clip live in `AttachedFiltersModel`
(`src/models/attachedfiltersmodel.h`), managed by `FilterController`
(`src/controllers/filtercontroller.h`). See §5 for read/write detail. To read filter params for an
arbitrary clip, grab its producer (`producerForClip`) and iterate `Mlt::Filter`s on it (the same
data `AttachedFiltersModel::getService(row)` exposes), or read them out of the project MLT XML.

---

## 5. Filters / effects (add + set params)

`FilterController` (`src/controllers/filtercontroller.h`) is the orchestrator:

```cpp
MetadataModel        *metadataModel();      // catalog of all available filters
AttachedFiltersModel *attachedModel();      // filters on the current clip
QmlMetadata          *metadata(const QString &id);
QmlMetadata          *metadataForService(Mlt::Service *service);
QmlFilter            *currentFilter() const;
void  setProducer(Mlt::Producer *producer = 0);   // which clip's filters we're editing (slot)
void  setCurrentFilter(int attachedIndex);        // slot
signals: currentFilterChanged(QmlFilter*, QmlMetadata*, int);  filterChanged(Mlt::Service*);
```

**Add a filter to the current clip** via `AttachedFiltersModel`
(`src/models/attachedfiltersmodel.h`):

```cpp
add(QmlMetadata *meta);          // add by metadata (a known filter id)
addService(Mlt::Service*);       // add an existing MLT service
remove(int row);  move(int from, int to);  pasteFilters();
getService(int row);  getMetadata(int row);
// roles: TypeDisplayRole, PluginTypeRole
signals: changed(), addedOrRemoved(Mlt::Producer*);
```

Flow: `attachedModel->add(filterController->metadata("<id>"))` appends the filter and makes it
current; the model attaches to whatever producer was set via `FilterController::setProducer(...)`.

**Get/set filter parameters** via `QmlFilter` (`src/qmltypes/qmlfilter.h`) — this is the real
read/write surface for params and keyframes:

```cpp
QString get(QString name, int position = -1);
double  getDouble(QString name, int position = -1);
QColor  getColor (QString name, int position = -1);
QRectF  getRect  (QString name, int position = -1);
void    set(QString name, QString value, int position = -1);
void    set(QString name, double value, int position, mlt_keyframe_type);
void    set(QString name, const QRectF &rect, int position, mlt_keyframe_type);   // + int/bool/QColor overloads
int     keyframeCount(const QString &name);
Mlt::Animation getAnimation(const QString &name);
// Q_PROPERTYs: in, out, duration, animateIn, animateOut, presets, path
// accessors: Mlt::Producer &producer();  Mlt::Service &service();  objectNameOrService()
```

The filter **catalog** (every available effect, its id, its `ui.qml`, default params) is
`MetadataModel` populated from the `meta*.qml` files under `share/shotcut/qml/filters/`. Filter
*ids* are the `name` of the MLT/frei0r service (read off `Metadata`/`QmlMetadata`). So the Go side
can offer "add filter <id> with params {...}" and the dock maps it to
`attachedModel->add(metadata(id))` + a series of `QmlFilter::set(name, value)` calls.

---

## 6. External-process integration (Shotcut ↔ Go)

Shotcut has **no built-in IPC/scripting/automation surface** to reuse (no embedded Python, no RPC,
no command server). The realistic options, in order of recommendation:

### A. (Recommended) A C++ `QObject` bridge inside the fork holding a `QLocalSocket`

Add a `BeckyBridge : QObject` to the fork that opens a `QLocalSocket` (Windows named pipe) /
`QTcpSocket` to `localhost`, reads **NDJSON** commands, and dispatches them on the Qt main thread to
`MAIN`, `MAIN.player()`, the `TimelineDock`, and `FilterController`. It emits NDJSON state deltas
back by connecting to the signals in §4. This is the cleanest fit: it lives in the dock's C++,
which already has full access to every model and singleton, and it keeps the Go engine
(becky-clip's `internal/footage`/`quotes`/`edl`/`reel`/`assistant`) as the brain. The one hard
rule: marshal every Shotcut call onto the GUI thread (`QMetaObject::invokeMethod(target, ...,
Qt::QueuedConnection)`), because the socket reader is a worker thread and all of `MainWindow`/
`Player`/`Timeline` is GUI-thread-only.

### B. `QProcess` spawning the Go bridge with NDJSON over stdio

The fork's `BeckyBridge` instead `QProcess::start("becky-edit-bridge")` and exchanges NDJSON over
the child's stdin/stdout. Simpler lifecycle (Shotcut owns the child), but couples the processes;
A's socket model is more robust for a long-running, separately-launched Go engine. Same GUI-thread
marshalling rule applies.

### C. MLT XML (`.mlt`) as the bulk hand-off path (use alongside A or B)

This is the high-leverage trick for *coarse-grained* operations and for the read-back mirror:

- **Write** — Go composes a full project as MLT XML and Shotcut opens it as a project:
  `Controller::openXML(const QString &filename)` (`src/mltcontroller.h`), or `MAIN.open("proj.mlt")`
  (MainWindow detects `.mlt` and opens it as the project, emitting `producerOpened()` and
  `profileChanged()`). becky-tools **already writes MLT XML**, so "Go builds the cut → Shotcut
  loads it" is essentially free.
- **Read** — `Controller::XML(Service* = nullptr, bool withProfile=false, bool withMetadata=true)`
  serializes the live project to an MLT XML string; `saveXML(filename, ...)` writes it to disk. The
  Go mirror can pull this on demand and parse it, rather than tracking every role delta.

Recommended division: **A** for live, fine-grained verbs (open/seek/play/append/select/set-filter)
and per-event deltas; **C** for "load this whole cut" and periodic/full state resync.

---

## 7. Windows build reality (honest paragraph)

Building Shotcut on Windows is **moderate, not nightmarish — because the project ships a working
recipe**. The official CI (`.github/workflows/build-windows.yml`) builds **natively on a
`windows-latest` runner under MSYS2/MinGW64** (not MSVC, not cross-compiled), using **Qt 6** and a
**prebuilt MLT+FFmpeg bundle** downloaded from S3
(`https://s3.amazonaws.com/misc.meltymedia/shotcut-build/mlt-prebuilt-mingw64-v6.txz`) plus a pile
of pacman packages (x264/x265/libvpx/cairo/…); the actual build is `bash
scripts/build-shotcut-msys2.sh` (CMake + Ninja, MLT built with `MOD_QT6=ON`). So the path is:
install MSYS2, `pacman -S` the mingw64 toolchain + Qt6 + deps, drop in the prebuilt MLT bundle, run
the script — a few hours the first time, reproducible after. The biggest real cost is the
MLT/FFmpeg dependency stack, which the prebuilt bundle removes. **kdenlive (also MLT) is a *harder*
fork target on Windows**, not easier: it pulls the full KDE Frameworks (KF6) dependency tree and is
typically built via Craft, which is heavier to stand up than Shotcut's self-contained MSYS2 script.
For a forensic NLE where we add one dock + a socket bridge, **Shotcut is the lighter, better-
documented fork**, and it speaks the MLT XML becky already produces.

---

## Recommended integration shape for the Go bridge

**Architecture:** Fork Shotcut → add `src/docks/beckydock.{h,cpp}` (`QDockWidget` + `QQuickWidget`
hosting the Becky QML panel, registered in `MainWindow::setupAndConnectDocks()`) → add
`BeckyBridge : QObject` holding a `QLocalSocket`/named-pipe server. QML panel is thin; all driving
is C++ into the existing singletons/models. Go (becky-clip engine) is the brain; it speaks NDJSON
over the socket. **Every inbound command is marshalled to the Qt GUI thread.**

| Go verb (NDJSON in) | Shotcut call (on GUI thread) |
|---|---|
| `preview {path,in,out,seek}` | `MAIN.player()->setPauseAfterOpen(true)`; `MAIN.open(path)`; on `producerOpened`: `player->setIn(in)`, `setOut(out)`, `seek(seek)`, `play()` |
| `play` / `pause` / `stop` / `seek {frame}` | `MAIN.player()->play()` / `pause()` / `stop()` / `seek(frame)` |
| `appendClip {path,in,out,track}` | open source w/ in/out (as above, paused) → `timeline->setCurrentTrack(track)` → `timeline->append(track)` |
| `insertClip {xml,track,pos}` / `overwriteClip` | `timeline->insert(track,pos,xml)` / `timeline->overwrite(track,pos,xml)` |
| `addTrack {kind}` | `timeline->addVideoTrack()` / `addAudioTrack()` |
| `select {track,clip}` | `timeline->setSelection(...)` / `setCurrentTrack` |
| `addFilter {clip,id,params}` | `filterController->setProducer(producerForClip)` → `attachedModel->add(metadata(id))` → `QmlFilter::set(name,val)` per param |
| `loadProject {mltPath}` | `MLT.openXML(path)` or `MAIN.open(path)` (`.mlt`) |
| `saveProject {path}` | `MLT.saveXML(path)` |

**State read back (NDJSON out):** the dock connects to `TimelineDock::positionChanged/`
`selectionChanged/seeked`, `MultitrackModel::appended/inserted/overWritten/modified`,
`Player::seeked/played/paused/stopped`, `MainWindow::producerOpened`, and
`FilterController::currentFilterChanged`. On each, emit a compact delta. For a full snapshot, push
`MLT.XML()` and let Go parse it. The Go shared-state mirror = {tracks[], clips[]
(resource/start/duration/in/out/service/filtered), playhead, selection, currentFilter} — every one
of those fields is directly queryable via `MultitrackModel` roles + `TimelineDock`/`FilterController`
getters above.

**Units:** all positions are **frames**; convert with `MLT.profile().fps()`.
**Hard correctness rule:** all Shotcut/MLT objects are GUI-thread-only — the socket reader must
`QMetaObject::invokeMethod(..., Qt::QueuedConnection)` for every call, never touch them directly.

---

### Sources
- Shotcut source (`master`): `src/docks/timelinedock.{h,cpp}`, `src/models/multitrackmodel.h`,
  `src/models/attachedfiltersmodel.h`, `src/controllers/filtercontroller.h`, `src/player.h`,
  `src/mainwindow.{h,cpp}`, `src/mltcontroller.h`, `src/qmltypes/qmlapplication.h`,
  `src/qmltypes/qmlfilter.h`, `src/qmltypes/qmlutilities.cpp`, `.github/workflows/build-windows.yml`
  — github.com/mltframework/shotcut
- "How to Make a Plugin" — shotcut.org/notes/make-plugins (filter-plugin runtime mechanism; no dock API)
