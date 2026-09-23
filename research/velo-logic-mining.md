Warning: MCP server(s) failed to start: huggingface-skills. Continuing with built-in tools and any servers that did connect. Re-run with QWEN_CODE_DEBUG=1 to see per-server reasons.

✨ cua-driver v0.8.3 is available (you have v0.5.1).
   Update with: cua-driver update
   Release notes: https://github.com/trycua/cua/releases/tag/cua-driver-rs-v0.8.3

I don't have a file-writing tool in this environment. Here is the complete report — you can save it to `ARCHITECTURE_REPORT.md`:

---

# Velo Architecture — Engineering Logic-Mining Report

> **Purpose:** Extract reusable architectural ideas from the velo (GPLv3) C++20/Qt6 NLE for a from-scratch Dear ImGui reimplementation. No code is copied; only design patterns, data-flow reasoning, and concurrency strategies are documented.

---

## Table of Contents

1. [Coalescing Preview System](#1-coalescing-preview-system)
2. [Decoder Lanes Caching](#2-decoder-lanes-caching)
3. [Unified Model & Recursive Mutex Coordination](#3-unified-model--recursive-mutex-coordination)
4. [Fluid Timeline Input Under Burst Load](#4-fluid-timeline-input-under-burst-load)
5. [Supporting Patterns Worth Stealing](#5-supporting-patterns-worth-stealing)

---

## 1. Coalescing Preview System

### 1.1 Problem Statement

During fast scrubbing, the GUI emits frame requests at mouse-event rate (hundreds per second). Decoding and compositing a single frame takes 5–80 ms depending on codec, effects, nesting depth, and resolution. A naive FIFO queue would accumulate hundreds of stale requests; the preview would lag seconds behind the user's scrub position.

### 1.2 How velo Solves It: The Single-Slot Overwrite Worker

**Key files:** `src/engine/RenderWorker.h`, `src/engine/RenderWorker.cpp`, `src/ui/PreviewWidget.cpp`

The `RenderWorker` is a `QThread` subclass with a **single-slot request mailbox**:

```
State:  m_seqId, m_t, m_scale   (the pending request)
        m_hasRequest = false     (slot empty/full flag)
        m_mutex (QMutex)         (protects the mailbox)
        m_cond (QWaitCondition)  (wakes the worker)
        m_busy (QAtomicInt)      (observable by the GUI thread)
```

**Write path — `requestFrame(seqId, t, scale)`** (called from GUI thread):
1. Lock `m_mutex`.
2. Overwrite `m_seqId`, `m_t`, `m_scale` with the latest values.
3. Set `m_hasRequest = true`.
4. `m_cond.wakeAll()`.
5. Unlock.

**Read path — `run()`** (worker thread main loop):
1. Lock `m_mutex`.
2. While `!m_hasRequest && !m_quit` → `m_cond.wait(&m_mutex)`.
3. **Snapshot** `seqId`, `t`, `scale` into locals.
4. Set `m_hasRequest = false` (clears the slot).
5. Unlock.
6. Set `m_busy = 1`.
7. `Compositor::renderFrame(seqId, t, scale)` — the expensive work.
8. Set `m_busy = 0`.
9. Emit `frameReady(img, seqId, t)` via Qt signal (queued connection → GUI thread).

### 1.3 Why It Works

| Scenario | What happens |
|----------|-------------|
| GUI sends request while worker is idle | Worker wakes immediately, renders that frame. |
| GUI sends 50 requests while worker renders one frame | All 50 overwrite the same slot. When the worker finishes and loops back, it sees only the **last** request. 49 intermediate frames are never rendered. |
| GUI checks `busy()` during playback tick | `PreviewWidget::tick()` gates with `if (!m_worker->busy())` before calling `requestFrame()` — avoids flooding during real-time playback when the timer fires faster than the renderer can keep up. |

This is **last-writer-wins coalescing** — not a queue, not a priority system, just a single overwrite-able slot. The key insight is that in a scrubbing context, only the *most recent* frame matters; every prior request is stale by definition.

### 1.4 The Playback Loop Integration

**File:** `src/ui/PreviewWidget.cpp` — `tick()`, `setPlaying()`, `requestRender()`

During playback, a `QTimer` at 15 ms intervals drives the loop:

```
tick():
  t = audioEngine.clock()           // audio hardware clock is the master
  if t <= wallStart: t = wallClock  // fallback when no audio device
  setPlayhead(seqId, t)             // updates the model
  if !worker.busy():
    worker.requestFrame(seqId, t, scale)
```

The audio clock (`AudioEngine::clock()`) derives from `QAudioSink::processedUSecs()` — the number of samples the audio hardware has actually consumed. This makes the playhead slave to audio output, eliminating A/V drift by construction.

During **scrubbing** (not playing), the `playheadChanged` signal triggers `requestRender()` directly — which calls `requestFrame()` — and the coalescing naturally handles rapid scrub events.

### 1.5 Re-implementing in Dear ImGui + Worker Thread

**Architecture for an ImGui NLE:**

```
┌─────────────────────────┐     ┌──────────────────────┐
│  Main Thread (ImGui)    │     │  Render Thread        │
│                         │     │                       │
│  ImGui::NewFrame()      │     │  loop:                │
│  timeline widgets       │     │    wait on condition   │
│  if scrubbing:          │     │    snapshot request    │
│    atomic_store(req)    │────▶│    clear flag          │
│    condvar.notify()     │     │    render frame        │
│  if frameReady:         │     │    atomic_store(result)│
│    upload to GPU tex    │◀────│    condvar.notify()    │
│  ImGui::Image(tex)      │     │                       │
└─────────────────────────┘     └──────────────────────┘
```

**Key design decisions:**

1. **Atomic request struct**: Pack `{seqId, time, scale}` into a lock-free atomic slot (C++20 `std::atomic<Request>` with a trivially-copyable struct, or a spinlock-guarded struct if the payload is too large for lock-free). The atomic store is the coalescing mechanism.

2. **Double-buffered result**: The worker writes to buffer A while the main thread reads buffer B, then they swap. This avoids the main thread ever blocking on the render thread.

3. **ImGui integration**: In the render loop, check an atomic `frameReady` flag. If set, upload the new image to a GPU texture (e.g., `D3D11_TEXTURE2D` or OpenGL `glTexSubImage2D`) and update the `ImTextureID`. The `ImGui::Image()` call always draws whatever texture is current — no stalls.

4. **`busy()` equivalent**: Use `std::atomic<bool> busy` so the ImGui main loop can skip submitting a new request when the worker hasn't finished the previous one. During real-time playback, this naturally throttles to the worker's actual throughput.

5. **No queue, no ring buffer**: The single-slot overwrite is strictly better than a bounded queue for this use case. A queue of depth N would render N-1 stale frames after the user stops scrubbing.

---

## 2. Decoder Lanes Caching

### 2.1 Problem Statement

A cross-dissolve transition between two cuts of the same media file requires decoding **two frames from the same file at two different time positions** for every single output frame. A naive single-decoder-per-file cache would seek back and forth between the two positions every frame, destroying throughput. The same problem arises with nested sequences reusing footage and picture-in-picture layouts.

### 2.2 Data Structures

**Key files:** `src/media/MediaCache.h`, `src/media/MediaCache.cpp`

```cpp
// Per-decoder entry
struct Entry {
    std::shared_ptr<VideoDecoder> dec;  // wraps AVFormatContext + AVCodecContext
    qint64 lastUse = 0;                 // monotonic tick for LRU eviction
};

// The cache (one instance per Compositor, i.e., per consumer thread)
QHash<QString, QVector<Entry>> m_decoders;  // path → lane array
QHash<QString, QImage> m_stills;            // path|maxW → decoded still image
qint64 m_tick = 0;                          // monotonic counter
int m_epoch = 0;                            // global invalidation sentinel
```

Each `VideoDecoder` internally tracks:
- `m_frameT` — PTS of the currently decoded frame (seconds)
- `m_frameDur` — expected frame duration (1/fps)
- `m_lastImage` / `m_lastImageT` / `m_lastImageW` — cached output of the last `sws_scale` conversion (avoids re-converting if the same frame is requested again at the same or smaller width)

### 2.3 Lane Selection Algorithm

`MediaCache::videoFrame(path, t, maxW, approx)`:

```
1.  lanes = m_decoders[path]
2.  For each lane i:
      d = t - lane[i].dec->position()
      sequential = (d >= -ε && d < 3.0)    // at-or-ahead, within 3s
      score = sequential ? d : 1e6 + |d|
    Pick lane with lowest score.
    needSeek = !sequential for best lane.

3.  If no lane exists, or (needSeek && lanes.size() < 3):
      Evict global LRU if total decoders >= 12.
      Spawn a new VideoDecoder for this file.
      Append as new lane.

4.  lanes[best].lastUse = ++m_tick
5.  return lanes[best].dec->frameAt(t, maxW, approx)
```

**The scoring function** is the heart of the system. A lane whose decoder is already positioned at or just behind `t` (within 3 seconds — matching `VideoDecoder`'s internal seek threshold) gets a low score proportional to the forward distance. A lane that would need to seek backwards or jump far ahead gets a score of `1e6 + |d|`, making it less attractive than spawning a new lane.

**Hard cap of 3 lanes per file** prevents pathological growth. **Global cap of 12 total decoders** across all files keeps memory bounded (each `VideoDecoder` holds an open `AVFormatContext` + `AVCodecContext` + frame buffers, ~20–80 MB depending on codec).

### 2.4 VideoDecoder Seek Strategy

`VideoDecoder::frameAt(t, maxW, approx)`:

```
covers = haveFrame && t ∈ [frameT - ε, frameT + 3s)

If covers:
  Return m_lastImage if it matches (same frame, sufficient width).

If approx:
  If !covers: seekTo(t), decodeNext() → keyframe only.
  (One seek + one decode per request. Fast but imprecise.)

If exact:
  behind = frameT > t + ε
  farAhead = !haveFrame || t > frameT + 3.0
  If behind || farAhead: seekTo(t)
  Decode forward until frameT + frameDur > t.
  (Guard: max 5000 decode iterations to handle corrupt streams.)
```

The `approx` flag is set when `speedAbs > 100.0` (passed down from the compositor when the accumulated speed of nested clip chains exceeds 100×). At extreme speeds, every frame is effectively a random seek anyway, so decoding just the nearest keyframe is both faster and visually indistinguishable.

### 2.5 Global Epoch Invalidation

```cpp
static QAtomicInt s_cacheEpoch{0};
void MediaCache::invalidateAll() { s_cacheEpoch.fetchAndAddRelaxed(1); }
```

Called when media files are relocated on disk (`Document::relocateMedia()`). Each `MediaCache::videoFrame()` call checks the epoch and drops all decoders/stills if it changed. This is lazy invalidation — no cross-thread coordination needed.

### 2.6 Re-implementing in Dear ImGui

**Data structure:**
```cpp
unordered_map<string, vector<DecoderLane>> lanes_;   // path → lanes
unordered_map<string, CachedImage> stills_;           // path → decoded still
```

**Lane selection** — same algorithm. The scoring function is the key intellectual property to reproduce.

**Decoder lifecycle:**
- Each lane wraps your platform decoder (FFmpeg `AVCodecContext`, Media Foundation, etc.).
- Lane creation is expensive (open file, find stream, allocate codec context). Gate it behind the "needSeek && lanes < 3" condition.
- LRU eviction: maintain a global `uint64_t tick` counter, stamp each lane on use, evict the minimum-tick lane when the global pool exceeds a cap.

**Cross-dissolve integration:** The compositor iterates video tracks bottom-to-top. When a clip has `transIn.type == CrossDissolve` and the current time is within the transition duration, it draws the **previous clip** (fading out) and the **current clip** (fading in) with smoothstep-interpolated opacity. Both clips call `clipSource()` → `MediaCache::videoFrame()` for the same file at different times, which is what triggers the multi-lane allocation.

---

## 3. Unified Model & Recursive Mutex Coordination

### 3.1 The Document as Single Source of Truth

**Key files:** `src/core/Document.h`, `src/core/Document.cpp`

`Document` owns:

| State | Type | Consumers |
|-------|------|-----------|
| `m_project` | `Project` (media list, sequences, clips, keyframes) | GUI, compositor, audio mixer, exporter |
| `m_playheads` | `QHash<QString, double>` (per-sequence playhead times) | GUI, preview |
| `m_selectedClips` | `QSet<quint64>` | GUI only |
| `m_clipboard` | `QList<Clip>` | GUI only |
| `m_undoStack / m_redoStack` | `QList<QByteArray>` (JSON snapshots) | GUI only |
| `m_mutex` | `QRecursiveMutex` | **Everyone** |

### 3.2 Why Recursive Mutex

A `QRecursiveMutex` allows the same thread to lock it multiple times without deadlocking. This is necessary because:

1. **UI code holds the mutex while calling Document methods that also lock it.** Example: `Document::addMediaClip()` locks the mutex, then calls `ensureTrackCount()` which accesses `seq.videoTracks` — if `ensureTrackCount` were a separate mutex-guarded method, you'd deadlock with a non-recursive mutex.

2. **The compositor locks the mutex during `renderFrame()`, which calls `renderSequence()`, which calls `clipSource()`, which calls `m_cache.videoFrame()` — all while the same thread holds the mutex.** The recursive mutex allows this natural call-tree without explicit lock-management.

3. **The audio mixer's `mix()` locks the mutex, then calls `mixSequence()` recursively (for nested sequences), which accesses `m_project` repeatedly.** Same thread, same mutex, recursive locking.

### 3.3 Thread Coordination Protocol

```
┌──────────────────────────────────────────────────────────────────────┐
│                         Document::m_mutex                           │
│                       (QRecursiveMutex)                             │
├──────────────┬──────────────┬──────────────┬────────────────────────┤
│  GUI Thread  │ RenderWorker │ AudioEngine  │    Exporter            │
│              │   Thread     │  (callback)  │    Thread              │
├──────────────┼──────────────┼──────────────┼────────────────────────┤
│ Holds mutex  │ Locks in     │ Locks in     │ Locks in               │
│ during all   │ renderFrame  │ mix() for    │ run() for each         │
│ model        │ (entire      │ each audio   │ renderFrame() call     │
│ mutations    │ compositing  │ buffer       │ and mix() call         │
│              │ pass)        │ callback     │                        │
├──────────────┼──────────────┼──────────────┼────────────────────────┤
│ Emits Qt     │ Emits        │ QAudioSink   │ Emits progress/        │
│ signals      │ frameReady   │ pull-mode    │ finished via Qt        │
│ (queued)     │ (queued)     │ callback     │ signals (queued)       │
└──────────────┴──────────────┴──────────────┴────────────────────────┘
```

**GUI Thread:**
- Calls `Document::beginUndoStep()` → locks mutex, snapshots project to JSON.
- Mutates the model (split, move, trim, etc.) → holds mutex.
- Calls `Document::notifySequenceChanged()` → emits signal.
- Signal handlers in `PreviewWidget` call `requestRender()` → `RenderWorker::requestFrame()` (no mutex needed — writes to the worker's mailbox).

**RenderWorker Thread:**
- `Compositor::renderFrame()` locks the mutex for the entire compositing pass.
- During the lock, reads `m_project` (sequences, tracks, clips, keyframes) freely.
- `MediaCache` is **per-compositor** (not shared), so no additional locking needed for decoder state.
- After rendering, emits `frameReady` signal — a **queued connection** in Qt, meaning the slot runs on the GUI thread's event loop.

**AudioEngine (audio callback thread):**
- `AudioMixer::mix()` locks the mutex at the start, holds it for the entire mix pass.
- Reads the same `m_project` data the compositor reads.
- `AudioReader` instances are per-mixer (keyed by `ctx * prime + clipId` to distinguish nested instances), so no cross-thread decoder sharing.
- The `MixDevice::readData()` callback (invoked by `QAudioSink`'s audio thread) calls `mixer.mix()` — blocking on the mutex if the GUI thread is mid-mutation.

**Exporter Thread:**
- Creates its own `Compositor` and `AudioMixer`, each with a pointer to the same `m_mutex`.
- Renders audio to a temp WAV (locking mutex per 1-second block).
- Pipes video frames to ffmpeg (locking mutex per frame via `comp.renderFrame()`).
- Checks `m_cancel` (atomic int) between frames for cancellation.

### 3.4 Contention Analysis

The recursive mutex is the **serialization point**. Worst case:
- GUI mutation (split, ~1 ms) blocks the render thread and audio callback.
- Render pass (~5–80 ms) blocks GUI mutations and audio callback.
- Audio mix callback (~2–10 ms per 4096-sample block at 48kHz) blocks GUI and render.

In practice, audio callbacks are the most latency-sensitive. A 4096-sample buffer at 48 kHz is ~85 ms. The mutex hold during `mix()` is typically 2–10 ms (reading the model is fast; the expensive part is the `AudioReader::read()` calls which decode audio — but those are sequential reads, not seeks). This is well within the budget.

**Risk for Dear ImGui reimplementation:** If the render pass takes 80 ms and the audio callback fires during that window, audio glitches (buffer underrun). Mitigation: pre-mix audio into a ring buffer on a separate thread, so the audio callback only reads from the ring buffer and never blocks on the model mutex.

### 3.5 Undo via JSON Snapshots

```cpp
void Document::beginUndoStep() {
    QMutexLocker lock(&m_mutex);
    m_undoStack.append(QJsonDocument(m_project.toJson()).toJson(Compact));
    if (m_undoStack.size() > 100) m_undoStack.removeFirst();
    m_redoStack.clear();
}
```

The entire `Project` is serialized to compact JSON (~50–500 KB for typical projects). Undo/redo deserializes back. This is:
- **Simple:** no inverse-operation tracking, no command pattern.
- **Safe:** every undo step is a complete, consistent snapshot.
- **Cheap enough:** JSON serialization of a typical project takes <1 ms.
- **Bounded:** capped at 100 entries (FIFO eviction).

For a Dear ImGui reimplementation, this same approach works well. The alternative (command-pattern undo with inverse operations) is more memory-efficient but dramatically more complex to implement correctly, especially with operations like "split all tracks at playhead" that create many clips atomically.

---

## 4. Fluid Timeline Input Under Burst Load

### 4.1 The Deferred-Commit Drag Pattern

**Key files:** `src/ui/TimelineWidget.h`, `src/ui/TimelineWidget.cpp`

The timeline never mutates the model during a drag. Instead:

**On `mousePressEvent`:**
1. Hit-test the click position → identify clip, edge, volume line, etc.
2. Snapshot the original state of all affected clips into `m_dragOrig` (a `QHash<quint64, Clip>`).
3. Record the grab offset (`m_grabDt = timeAt(mouseX) - clip.start`).
4. Set `m_drag` enum to the appropriate mode (MoveClips, TrimLeft, TrimRight, etc.).
5. **No model mutation. No undo step.**

**On `mouseMoveEvent` (fired potentially hundreds of times per second):**
1. Compute proposed delta: `delta = (mouseT - grabDt) - origClip.start`.
2. Apply snapping to the **group's outer edges** (not individual clips).
3. Clamp delta so nothing moves before t=0.
4. Store delta in `m_moveDelta` and track delta in `m_moveTrackDelta`.
5. Call `update()` → triggers `paintEvent` which draws ghost outlines at the proposed position.
6. **Still no model mutation.**

**On `mouseReleaseEvent`:**
1. Call `commitMove()`:
   - `beginUndoStep()` — one undo step for the entire drag.
   - Remove all dragged clips from the model.
   - Re-insert them at `orig.start + m_moveDelta` on the target track.
   - `overwriteInsert()` handles any overlap resolution.
   - `fixupClipIds()` assigns IDs to any clips created by the overwrite split.
   - `notifySequenceChanged()` — one signal, one re-render.
2. Clear drag state.

### 4.2 Why This Keeps the Timeline Fluid

| Concern | How it's addressed |
|---------|-------------------|
| 100+ mouseMove events/sec | Each move only computes a float delta and calls `update()`. No mutex lock, no model mutation, no signal emission. Cost: ~0.01 ms per event. |
| Undo history pollution | One undo step per completed drag, not per mouseMove event. |
| Visual feedback during drag | Ghost outlines drawn from `m_dragOrig` + `m_moveDelta` — purely visual, no model involvement. |
| Snap computation | `snapTime()` scans all clip edges in the sequence (O(n) in clip count). For typical projects (hundreds of clips), this is <0.1 ms. |
| Linked A/V sync | `withLinked()` expands the selection to include linked partners before the drag starts. The entire linked group moves as one unit. |

### 4.3 Group Snapping

The snap algorithm considers the **entire selection's outer boundaries**:

```
groupStart = min(orig.start for all dragged clips)
groupEnd   = max(orig.end() for all dragged clips)

s1 = snapTime(groupStart + delta, dragIds)  // snap left edge
s2 = snapTime(groupEnd + delta, dragIds)    // snap right edge

Pick whichever snap is closer to the proposed delta.
```

The `dragIds` set is passed as an **ignore list** — the snap function skips edges of clips being moved (otherwise a clip would snap to its own original position).

`snapTime()` considers:
- Time zero
- The playhead position
- Every clip's start and end across all tracks (video + audio)
- Threshold: 9 pixels at the current zoom level

If the Alt key is held, or the magnet is toggled off, snapping falls through to frame-quantization only (`snapFrame(t)` rounds to the nearest frame boundary).

### 4.4 Ambiguous Grab Resolution

When the user clicks on an audio clip's volume line, the intent is ambiguous: do they want to **edit the volume** or **move the clip**? velo uses a `VolumeOrMove` drag mode:

```
mousePress on volume line:
  m_drag = VolumeOrMove
  m_volStart = clip.volume.base()

first mouseMove with manhattan distance > 6:
  if |dy| > |dx|: m_drag = VolumeLine  (vertical → edit volume)
  else:           m_drag = MoveClips   (horizontal → move clip)
```

This is a clean pattern for any Dear ImGui NLE: defer the commit until the drag direction reveals intent.

### 4.5 Razor Tool — Instant Split

When the Razor tool is active, `mousePressEvent` immediately calls `Document::splitAt()` on click — no drag, no deferred commit. This is the one case where a mouse press directly mutates the model, because the operation is atomic and the user expects instant visual feedback.

### 4.6 The ZoomBar Navigator

**File:** `src/ui/TimelineWidget.cpp` — `ZoomBar` class

The horizontal scrollbar doubles as a zoom navigator (Premiere-style):
- **Drag the middle** → scroll (pan the timeline).
- **Drag the left/right edge** → zoom in/out (change `m_pxPerSec` while anchoring the opposite edge).
- **Click outside the handle** → jump-scroll (center the view on the click position).

The handle has a minimum width of 24 pixels so it's always grabbable even at extreme zoom-out.

For Dear ImGui: this can be implemented as a custom widget with `ImGui::InvisibleButton()` + `ImGui::GetIO().MouseClicked/DragDelta`.

---

## 5. Supporting Patterns Worth Stealing

### 5.1 Per-Compositor MediaCache (Thread-Local Decoder Pools)

Each `Compositor` instance owns its own `MediaCache`. The preview's `RenderWorker` creates one `Compositor` in its `run()` method (on the worker thread). The `Exporter` creates another on its thread. They never share decoder state.

This means:
- No cross-thread decoder contention.
- No need for decoder-level locks.
- Each thread's decoder lanes evolve independently based on that thread's access pattern.

**For Dear ImGui:** Each worker thread that touches decoded frames should have its own decoder pool. The model mutex serializes access to the project structure; the decoder pools are thread-local and mutex-free.

### 5.2 Adaptive Decode Quality

```cpp
// In clipSource():
double sx = max(abs(clip.scaleX.at(local)), 0.01);
int maxW = min(m->width, ceil(m->width * sx * scale)) + 2;
```

The compositor computes the maximum pixel width the frame will occupy on screen and passes it as `maxW` to the decoder. The decoder's `sws_scale` scales directly to that width — never decoding at full resolution and then downsampling. At 1/4 preview quality, a 4K source decodes to 960×540, saving ~12× pixel throughput.

Additionally, the `scale` parameter (1.0, 0.5, 0.25, 0.125) is user-selectable in the preview quality dropdown, providing a manual override.

### 5.3 Speed-Aware Decode Mode

```cpp
// Compositor tracks accumulated speed through nesting:
QImage renderSequence(seq, t, scale, depth, opaqueBg, speedAbs = 1.0);

// In clipSource():
const double effSpeed = speedAbs * abs(clip.speed);
img = m_cache.videoFrame(path, srcT, maxW, effSpeed > 100.0);
//                                                    ^^^^^^^^^^^
//                                        approx=true when speed > 100×
```

When the effective playback speed exceeds 100× (e.g., a nested sequence played at 10× inside a clip played at 15× = 150×), the decoder switches to `approx` mode, which accepts the nearest keyframe instead of decoding the entire GOP. This prevents the preview from stalling during extreme speed ramps.

### 5.4 Audio Scrub with Backpressure

```cpp
void AudioEngine::scrub(const QString &seqId, double t) {
    if (m_playing) return;                          // no-op during playback
    if (m_scrubSink->bytesFree() < bytes) return;   // drop if prev burst still playing
    // mix 40ms of audio and push to sink
}
```

Audible scrubbing (hearing audio while dragging the playhead) uses a push-mode audio sink with a 90 ms buffer. If the buffer still has data from the previous scrub burst, the new burst is silently dropped. This prevents a rapid drag from flooding the audio system with overlapping bursts.

### 5.5 Text Rendering Cache

```cpp
// Compositor::renderText():
QString key = text + "|" + family + "|" + pixelSize + "|" + bold + italic + ...;
auto it = m_textCache.find(key);
if (it != m_textCache.end()) return it.value();
// ... render with QPainterPath (stroke + fill) ...
if (m_textCache.size() > 32) m_textCache.clear();
m_textCache.insert(key, img);
```

Text clips are rendered to an image and cached by a composite key. The cache is per-Compositor and bounded (flushes entirely at 32 entries — simpler than LRU for a small cache).

### 5.6 Waveform Service (Background Peak Building)

```cpp
class WaveformService : public QObject {
    QVector<float> peaks(path, durationHint);  // returns cached, or starts build
signals:
    void peaksReady(path);                     // timeline repaints when done
};
```

Waveform peaks are computed in the background via `QtConcurrent::run()`. The timeline paints clips without waveforms until the peaks are ready, then repaints. Peaks are mono max-amplitude per 10 ms window — small enough to cache in memory (4.8 MB for a 1-hour file).

The peak computation streams the audio file in 1-second blocks, computing 100 peak values per block (one per 10 ms). This is O(n) in file duration and runs once per file.

### 5.7 Effect System — Registration-Based Modularity

```cpp
// Effects.h
struct EffectDesc {
    QString id, name, category;
    QList<EffectParamDesc> params;
    std::function<void(QImage&, const QMap<QString,double>&, double)> apply;
    bool isTransition, isAudio;
};

// Effects.cpp — adding a new effect is one call:
registerEffect({
    "blur", "Gaussian Blur", "Blur",
    {{"radius", "Radius", 0, 50, 5, 1, 1}},
    [](QImage &img, const QMap<QString,double> &p, double scale) { ... }
});
```

Adding a new effect is a single `registerEffect()` call. The UI (EffectsPanel), serialization (JSON), keyframing (AnimatedParam), and audio gain computation all work off the registry automatically. This is a clean pattern for an ImGui NLE — effects are data-driven, not hard-coded.

### 5.8 Pitch-Preserving Time Stretch (Granular Overlap-Add)

**File:** `src/engine/AudioEngine.cpp`

When `clip.preservePitch` is true and `clip.speed != 1.0`, the audio mixer uses granular overlap-add:
- Hann-windowed grains of 2048 samples (~43 ms at 48 kHz).
- 50% overlap (hop = 1024 samples).
- Each grain reads source at 1× from the position dictated by the clip speed.
- Grains are scheduled by absolute clip-local output sample index, so independently rendered blocks align without carrying state.

This is a self-contained DSP technique that works identically in an ImGui reimplementation. The key insight is that grain scheduling is **stateless** — derived entirely from the output sample index, not from any running state between mix callbacks.

---

## Summary: The Five Core Ideas to Extract

| # | Pattern | Key Insight | Complexity |
|---|---------|-------------|------------|
| 1 | **Single-slot coalescing worker** | Only the latest frame request matters during scrubbing; overwrite, don't queue. | Low |
| 2 | **Multi-lane decoder cache with proximity scoring** | Score each decoder lane by how close it is to the requested time; spawn new lanes for distant positions. | Medium |
| 3 | **Recursive mutex as shared-model coordinator** | One mutex shared by GUI/render/audio/export; recursive locking allows natural call trees. | Low |
| 4 | **Deferred-commit drag with ghost rendering** | Store original state + delta during drag; mutate model only on mouse release; one undo step per drag. | Low |
| 5 | **Audio-clock-driven playback with busy-gating** | Playhead slaves to audio hardware clock; render requests gated by worker busy flag. | Low |

These five patterns, combined with the supporting techniques (adaptive decode quality, speed-aware approximate decoding, per-thread decoder pools, data-driven effects, granular pitch-preserving stretch), constitute the architectural DNA that makes an NLE timeline feel fluid under extreme input rates.
