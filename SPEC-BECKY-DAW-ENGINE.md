# SPEC-BECKY-DAW-ENGINE.md — the real-time audio engine that turns becky-canvas into a Cubase replacement

> **STATUS: design only — NOT built (2026-06-15).** Deep-research pass on Go+cgo
> real-time audio (miniaudio/WASAPI on Win10), sample-accurate MIDI sequencing,
> built-in C++ instruments/FX, the deterministic routing DAG as a *live* audio
> graph, and **selective** plugin hosting (CLAP-first, VST3 behind the same host
> interface). This is the audio-engine layer *under* `SPEC-BECKY-CANVAS.md` — that
> spec named the parts (miniaudio, RtMidi, CLAP, the routing DAG); this one
> specifies how they become a playing, instrument-hosting, plugin-capable DAW core.
> Awaits Jordan's go/no-go. **Citations inline; open decisions at the end.**

---

## 0. The brief, in Jordan's words

Jordan is a pro producer who tracks in **Cubase** and finds it AI-hostile. He wants
becky-canvas to **replace Cubase's core**: a real-time engine that *plays* the
`becky-compose` MIDI stems, *hosts* instruments and effects, and supports
**selective VST3** integration. Three load-bearing constraints from him:

1. **MIDI-first.** He customizes the stems. The engine is a sequencer that drives
   instruments, not an audio-clip arranger. SMF + `project.json` in, sound out.
2. **"LEGO building blocks where each stem has context of the others."** The
   stems are not isolated — drums duck the music, the 808 sits on its own bus, the
   lead reacts to the chord track. That context **already lives in
   `project.json`** (becky-compose ships the routing/sidechain edges). The engine's
   job is to make that context *audible and live*, not to rebuild it by hand.
3. **C++ wherever Go isn't ideal.** Go orchestrates; C/C++ does the audio thread,
   the DSP, and the plugin host. This matches the CANVAS stack (Go control plane,
   C audio callback).

This spec is opinionated about **where the Go↔C++ line falls** and **why "selective"
VST3 is the correct scope**, not a limitation.

---

## 1. The non-negotiable: the audio callback thread

A DAW lives or dies on one rule: **the audio callback must never block, never
allocate, never lock, and never enter a garbage-collected runtime.** Every glitch
("xrun", crackle, dropout) is a callback that missed its deadline.

### 1.1 Why this forces a C audio thread in a Go app

Go's runtime can **stop-the-world for GC** at any time. If the audio callback runs
Go code, a GC pause lands *inside* the deadline and you hear it. The field-proven
pattern in Go audio is therefore: **the callback is pure C, reading from a
C-allocated lock-free ring buffer that never enters the Go runtime**, which makes
it immune to GC stop-the-world pauses. This is exactly how the PortAudio/Go binding
ships its real-time path (a C-allocated SPSC ring, callback reads entirely in C),
and the reasoning generalizes to miniaudio. citeturn0search0turn0search6

**Decision:** becky's audio callback is **C/C++**, not a Go callback. Go produces
work *ahead of time* and hands it across a lock-free queue; Go never runs on the
audio thread. (This is stricter than "use malgo's Go callback" — see §6.2 for why
we deliberately do *not* take the easy malgo-Go-callback path.)

### 1.2 The device layer: miniaudio via WASAPI on Win10

- **Library:** **miniaudio** (single-header C, public-domain/MIT-0), reached from
  Go via **`gen2brain/malgo`** (cgo bindings). malgo requires cgo but **links
  nothing extra on Windows/macOS** (only `-ldl` on Linux), so the build stays
  clean. citeturn0search3turn0search4 We use malgo for **device enumeration,
  open/start/stop** (control plane, runs on a Go goroutine), but install our **own
  C callback** for the realtime path (§6.2).
- **Backend:** WASAPI. **Latency reality on Win10 (important, cited):** WASAPI
  *shared mode* on older non-`IAudioClient3` paths floors at the default device
  period — typically **~10 ms / 480 frames** — *regardless* of requested buffer
  size. **Win10's `IAudioClient3`** lets drivers report smaller periods (5/3/1 ms);
  **WASAPI exclusive mode** goes lower still but takes the device. citeturn0search1turn0search7
  So: ship **shared mode** by default (coexists with the OS, ~10 ms — fine for
  composing/playback), and offer **exclusive mode** as an opt-in "tracking" toggle
  for low latency. Set `performanceProfile = low_latency`; expose
  `periodSizeInFrames` (default the device period; allow 128 for exclusive). citeturn0search5
- **ASIO note (license trap):** ASIO would give the lowest latency, **but as of the
  Oct 2025 relicensing ASIO is GPLv3-or-proprietary** (see §5.1). Linking the ASIO
  SDK would impose GPLv3 on becky-canvas or require a Steinberg agreement. **We do
  not link ASIO.** WASAPI exclusive covers the low-latency need without the license
  tax. citeturn1search0turn1search6

### 1.3 The Go↔C handoff (lock-free, SPSC, no alloc on the audio thread)

Two lock-free **single-producer/single-consumer** queues across the boundary, both
C-allocated so the audio thread never touches Go memory:

```
 Go transport goroutine ──(MIDI event ring: tick-stamped note on/off)──▶  C audio thread
 Go control plane       ──(graph-swap mailbox: atomic ptr to compiled schedule)─▶ C audio thread
 C audio thread         ──(meter/peak ring: RMS, peak, playhead samples)──────▶ Go UI (drains per frame)
```

- The **MIDI ring** is filled by Go a block (or several) ahead of the playhead and
  drained sample-accurately in C (§2.3). SPSC lock-free ring buffers rely on atomic
  indices, no locks — the standard real-time-audio structure. citeturn0search1turn0search2
- The **graph swap** is a single atomic pointer flip: Go compiles a new schedule
  off-thread, publishes the pointer; C picks it up at the next block boundary; the
  old schedule is reclaimed by Go after a grace block (RCU-style). No locks, no
  frees on the audio thread.
- **No-alloc/no-lock discipline on the C side:** all buffers preallocated at
  graph-compile time; the callback only does arithmetic and pointer-following.
  Fixed buffer size per block (64–512 frames) keeps scheduling deterministic;
  variable buffers are only needed for plugin-delay-compensation/transport edges,
  which we handle at block boundaries. citeturn0search4

---

## 2. The MIDI sequencer / transport

### 2.1 Reading becky-compose into a playable timeline

Input is exactly what `becky-compose` already emits (SPEC-BECKY-COMPOSE §1, §5):
per-track SMF stems (`drums.mid`…`sfx.mid`) or the combined `song.mid`, plus
`project.json` (routing). **PPQ = 480** is shared between compose and this engine —
no resolution mismatch. The Go loader (`internal/dawengine/seq`):

1. Parses each MTrk into a flat, tick-sorted **event list** per track: note-on,
   note-off, program change, tempo, time-sig, track-name. (becky already has a
   from-scratch SMF *parser* in `music_test.go` — promote it to a real reader.)
2. Builds a **tempo map** (list of `{tick, microsPerQuarter}`) so tempo changes are
   honored; PPQ + tempo define real time. citeturn2search5turn2search6
3. Maps each track to a **graph instrument node** by name using `project.json`
   (drums→sampler, bass→synth, etc.), so the timeline knows *which instrument*
   each event drives.

### 2.2 Transport (Go-side timekeeper)

A Go transport object owns `playing`, `playheadTicks`, `loop{a,b}`, `tempoMap`. It
is the **timekeeper that is separate from but linked to the scheduler** — the
standard DAW split. citeturn2search6 It does **not** run on the audio thread; it
produces events ahead of the playhead and stamps them in **absolute sample frames**
so the C side needs no tempo math in the callback.

### 2.3 Sample-accurate scheduling (the core trick)

Per audio block of `N` frames, the C callback knows the block's start sample
`S` and end `S+N`. Any MIDI event with sample-stamp in `[S, S+N)` is dispatched to
its instrument **at the exact sample offset within the block** — this is the
textbook sample-accurate path: take the buffer's start/end in samples and fire the
events that fall inside this render cycle. citeturn2search7turn2search0 Tick→sample
conversion happens **once, in Go**, when the event is pushed to the ring:

```
samplesPerTick = (60 / bpm) * sampleRate / PPQ          // recomputed per tempo segment
eventSample    = tempoMap.tickToSample(event.tick)      // absolute frame
```

`frames_per_beat = sampleRate / (bpm/60)` is the same identity the field uses. citeturn2search1
Result: notes land on the right sample, not the right *block* — tight enough for a
pro producer, and **deterministic** (same project + same sample rate → same sample
offsets), honoring becky's determinism ethos.

---

## 3. Built-in instruments (C++ DSP nodes, MIDI-driven)

The genre stems must make sound **with zero plugins installed** (offline,
self-contained — becky's ethos). Two built-in instrument node types, both C++:

### 3.1 Sampler (drums, 808, one-shots)

- **MVP library:** **TinySoundFont** — a SoundFont2 synth in a **single C/C++
  file**, zlib-licensed, no dependencies, already builds on Win64. citeturn3search5
  It gives us multi-zone, velocity-mapped, pitched sample playback (perfect for a
  GM drum kit + a pitched 808) for almost no integration cost.
- **Upgrade path:** **sfizz** (BSD-2-Clause, SFZ format, sample-based synth that
  reuses existing SFZ instrument libraries) when Jordan wants real producer kits. citeturn3search0
  Same node interface; swap the engine behind it.
- **MIDI drive:** note-on(pitch,vel)→pick zone, set gain from the velocity ladder
  (`ghost40…hard118`, shared with compose), trigger voice; note-off→release. The
  808 is one pitched zone with `glide` honored via pitch ramp (compose already
  plans pitch-bend glide bytes — SPEC-BECKY-COMPOSE §7).

### 3.2 Synth (bass, chords, melody, lead, counter)

- A small **subtractive synth** node in C++: poly voice allocator, 2 osc
  (saw/square/sine/pulse) + sub, ADSR, state-variable filter (LP/HP/BP) with
  envelope+key tracking, unison/detune. ~300 lines of well-trodden DSP; no external
  dep needed for the MVP, so it links cleanly and stays deterministic.
- **MIDI drive:** note-on→steal/allocate voice; channel/program from `project.json`
  selects a patch (a named parameter preset per track role: `bassPatch`,
  `leadPatch`…). `extensions[]`/`voicing` from the compose profile inform the patch
  defaults so a genre "sounds right" out of the box.

Both instrument types are **graph nodes** (§4) with the same `process(block)` ABI
as FX, so the scheduler treats instruments and effects uniformly.

---

## 4. The deterministic routing DAG as a *live* audio graph

`project.json` is already a deterministic DAG (CANVAS §5, COMPOSE §5):
**nodes** = sources/tracks/buses/FX/sends; **edges** = audio or
control/**sidechain**. This engine **compiles that JSON into a lock-light process
schedule** and runs it every block.

### 4.1 Compile (Go, off the audio thread)

1. **Validate + resolve** node names/edges; reject cycles (a real audio cycle is an
   error; sidechain edges are *not* cycles — they're control taps).
2. **Topological sort** via **Kahn's algorithm** → fixed per-block processing
   order; independent nodes may be grouped into layers for optional multicore. This
   is exactly the pattern proven by `audio_graph` (DAG, Kahn, no per-block alloc)
   and `tracktion_graph` (topological graph built to solve PDC + multicore). citeturn1search1turn1search3
3. **Sidechain constraint (the LEGO context):** a sidechain edge `{from:B,
   to:A.compressor.sidechain}` forces **B to be scheduled in the same/earlier layer
   than A** so A's detector sees B's *current-block* signal — the WildFX rule
   ("sidechain sources and consumers reside in the same layer"). citeturn1search1
   becky-compose ships these edges (kick ducks the music bus and the 808 — COMPOSE
   §5), so "each stem has context of the others" compiles straight from the
   manifest: **one declared edge, not 100 clicks**.
4. **Preallocate** every node's I/O buffer and scratch; emit a flat array of
   `{node, inputs[], outputs[], params}` — the **compiled schedule**. Publish via
   the atomic graph-swap pointer (§1.3).

### 4.2 Run (C, on the audio thread)

Walk the flat schedule in order; each node does `process(in, out, N)`; buses sum
their inputs; sends copy taps; the sidechain detector reads the tap buffer. **No
allocation, no locks, no graph traversal logic** on the audio thread — it's a
straight-line walk of a precompiled list. Determinism: topo-order is fixed,
stochastic FX use fixed seeds → **same project == same render** (CANVAS §5).

### 4.3 Built-in FX nodes (C++)

Ship the minimum a mix needs, all as graph nodes with the uniform ABI:

| Node | Notes |
|------|-------|
| **Gain / pan** | trivial; per-track + per-bus |
| **EQ** | biquad bands (low-shelf/peak/high-shelf), TPT/SVF form |
| **Compressor** | with **sidechain input port** — the detector reads the sidechain tap buffer, not its own input; this is what makes the compose "kick ducks music" edge real |
| **Delay** | tempo-synced (uses the transport tempo map), feedback, filtered |

(Reverb/saturation later.) These four + the two instruments are enough to **play a
full becky-compose project and hear the routing**, which is the whole point.

---

## 5. Selective plugin hosting — CLAP-first, VST3 behind the same interface

This is the part that makes it a Cubase *replacement* rather than a toy. The
research changes the CANVAS spec's recommendation, so read §5.1 carefully.

### 5.1 The licensing landscape changed (Oct 2025) — this is decisive

- **CLAP** ("CLever Audio Plugin") is **MIT-licensed**, community-owned, a **C ABI**
  (binds to any language), with a **single event queue for MIDI + parameter changes
  + timing**, **MIDI 2.0 / per-note modulation**, and a threading model that
  outperforms VST3. No NDA, no fees. citeturn0search11turn0search12turn1search1
- **VST3 SDK is now MIT** as of **VST 3.8.0 (Oct 20 2025)** — previously a
  proprietary/dual-GPLv3 agreement you had to *sign*. The MIT move **eliminates the
  signed-agreement requirement** and lets permissive projects incorporate the SDK
  freely. citeturn1search0turn1search6 **This overturns the CANVAS spec's
  assumption** that VST3 carries a "license tax" to be deferred — that was true
  before Oct 2025, and is **no longer true**.
- **ASIO is the opposite** — now **GPL3-or-proprietary** (see §1.2). So the trap
  moved: VST3 got *easier*, ASIO got *stickier*. citeturn1search0

**Conclusion:** CLAP-first is still right (cleaner C ABI, MIT, better threading,
MIDI 2.0), but **VST3 is now a legitimate second target with no license tax** —
worth supporting because Jordan's existing Cubase plugin collection is
overwhelmingly VST3. Both go **behind one becky host interface**.

### 5.2 Why "selective" is the right scope (not a limitation)

A general-purpose plugin host is a multi-year project: full param/automation,
plugin GUIs (each plugin draws its own window), state save/load, preset handling,
PDC, sandboxing crashy plugins, scanning thousands of plugins. **becky doesn't need
that.** Jordan wants **a few trusted plugins** (his go-to compressor, his synth) in
an otherwise self-contained, deterministic engine. "Selective" means:

- An **allowlist** of plugin paths Jordan declares (same default-deny posture as
  `becky-harness`/CANVAS §4) — not a scan-the-whole-system plugin manager.
- **Audio + MIDI + parameter automation** supported; **plugin GUIs optional** (host
  generic param sliders first; embed the plugin's own window later).
- **Determinism caveat, logged:** a third-party plugin is opaque and may not be
  bit-deterministic. Hosting one is an **explicit opt-out of the determinism
  invariant**, declared per-node in `project.json` (`"deterministic": false`) and
  surfaced in the UI — consistent with how the other agentic specs opt out of the
  offline invariant explicitly and visibly.

This keeps the engine small and honest: built-in nodes stay deterministic; a
declared plugin is a labeled exception, not a hole in the model.

### 5.3 The C++ host shim (one interface, two backends)

```
internal/dawengine (Go)  ──cgo──▶  becky_pluginhost (C++)
                                      ├── clap_backend   (links CLAP MIT headers; clap-host pattern)
                                      └── vst3_backend   (links VST3 MIT SDK; optional build tag)
                                    exposes a single C ABI:
                                      bh_load(path) -> handle
                                      bh_activate(handle, sampleRate, maxBlock)
                                      bh_process(handle, audioIn[], audioOut[], midiEvents[], N)
                                      bh_param_set(handle, id, value)   // automation
                                      bh_param_list(handle) -> [{id,name,min,max,default}]
                                      bh_state_save/load(handle) -> bytes
                                      bh_unload(handle)
```

- **CLAP backend:** use the official **`free-audio/clap`** headers (single C ABI)
  and follow the **`free-audio/clap-host`** reference for load/activate/process,
  the **single event queue** (push note + param + transport events, read param
  outputs), and the C++ glue/proxy layer that catches threading bugs. citeturn0search7turn0search11
- **VST3 backend (optional, behind a build tag):** link the now-MIT VST3 SDK; map
  our buffers/events onto VST3 `process()`, parameters onto its param queue. Kept
  behind `//go:build vst3` so model-free / SDK-free CI never needs it.
- **A becky FX/instrument node** wraps a `bh_*` handle and presents the **same
  `process(block)` ABI** as the built-in nodes (§4) — so the graph scheduler is
  agnostic to "built-in vs CLAP vs VST3". Parameter automation = the Go transport
  streaming `bh_param_set` (or queued param events) at block boundaries; MIDI = the
  same sample-stamped events from §2.3 handed in as the backend's event format.

### 5.4 Reference engines we deliberately do *not* embed

**JUCE `AudioProcessorGraph`** and **`tracktion_graph`** solve exactly this problem
and are battle-tested — but both are **GPL/commercial** (JUCE per-company license;
Tracktion Engine GPL/commercial, and it *also* drags in a JUCE license). citeturn1search1turn1search4
Embedding either would impose GPL or a paid license on becky-canvas. **We borrow
their *architecture* (Kahn topo-sort, PDC at block boundaries, same-layer
sidechain) but write our own small graph** so becky stays MIT/permissive and
offline-by-default. This is a conscious build-vs-buy call, logged here.

---

## 6. The Go↔C++ boundary (precise)

### 6.1 Who owns what

| Lives in **C/C++** (cgo, build-tagged) | Lives in **Go** (pure, CI-green) |
|----------------------------------------|----------------------------------|
| The audio **callback** (miniaudio C path) | **Transport**: play/stop/loop, tempo map, playhead |
| All **DSP**: sampler, synth, EQ/comp/delay | **SMF + project.json** parsing → timeline/graph model |
| The **plugin host** (CLAP + optional VST3) | **Graph compile**: validate, Kahn topo-sort, prealloc, publish |
| The **lock-free rings** (C-allocated) | **MIDI scheduling**: tick→sample, fill the MIDI ring ahead of playhead |
| Per-block buffer math, voice allocation | **UI** (ImGui via giu), file IO, config, the becky command DSL |

Rule of thumb (matches CANVAS): **Go decides *what* and *when* ahead of time; C++
does *the work* under the deadline.** Go never runs on the audio thread.

### 6.2 Why not just use malgo's Go callback?

malgo *can* call a Go function per buffer — the easy path. We reject it for the
realtime mixer because that Go callback is exposed to **GC stop-the-world** and cgo
call overhead per buffer. Instead we use malgo for **control** (enumerate/open/
start/stop on a goroutine) and register a **pure-C data callback** that drains the
C ring and runs the compiled schedule — the GC-immune pattern proven by the
PortAudio/Go realtime binding. citeturn0search0turn0search6 (malgo's Go callback
is acceptable only for the trivial Phase-0 "play one sampler" spike before the ring
exists.)

### 6.3 Build tags so CI stays green (CLAUDE.md §3)

```
//go:build cgo        → internal/dawengine/native (miniaudio, DSP, clap host)
//go:build cgo && vst3 → vst3 backend
(default / no cgo)    → internal/dawengine/stub: parse SMF+project.json, compile the
                        graph, validate determinism, *render nothing* (or render to a
                        deterministic offline WAV via a pure-Go reference mixer)
```

The **graph model, SMF reader, tempo map, topo-sort, and schedule compiler are
pure Go** and fully unit-tested on model-free Linux/Windows CI (Kahn order,
sidechain-same-layer, tick→sample, tempo-map correctness, determinism of the
compiled schedule). Only the *sound-making* C path needs cgo, and it's gated.
A **pure-Go offline reference mixer** (non-realtime, renders the built-in nodes to
WAV) is worth building: it gives CI an audible-correctness check **and** a
deterministic "bounce" feature, without any cgo. citeturn1search4

---

## 7. Module layout (extends CANVAS §6)

```
becky-go/internal/dawengine/
  seq/        smf_reader.go  tempomap.go  transport.go  schedule.go   // pure Go, tested
  graph/      compile.go (Kahn, prealloc, sidechain-layer)  model.go  // pure Go, tested
  refmix/     mixer.go       // pure-Go offline reference renderer → WAV (CI audible check)
  native/     //go:build cgo — device.go (malgo control) + cgo bridge to:
    cpp/      engine.cpp (C audio callback + ring drain + schedule walk)
              sampler.cpp synth.cpp  fx_gain.cpp fx_eq.cpp fx_comp.cpp fx_delay.cpp
              pluginhost/ host.cpp clap_backend.cpp  vst3_backend.cpp (//go:build vst3)
  ring/       ring.go (Go side) + ring.h (C SPSC ring, C-allocated)
becky-go/cmd/canvas/modes/daw/   // the canvas Mode that drives all of the above
```

This is the **audioengine/** referenced in CANVAS §6, fleshed out. It is a `Mode`
inside `cmd/canvas` — not a separate binary — so it shares the one window, GPU
context, and embedded model.

---

## 8. Phased build plan

- **Phase 0 — playback of becky-compose stems (the proof).** Pure-Go SMF reader +
  tempo map + transport; cgo miniaudio out; **TinySoundFont** sampler + the small
  synth; **sample-accurate** scheduling of one project's notes. No graph yet (fixed
  track→instrument→master). Goal: **press play on a `becky-compose` project and
  hear the genre.** (malgo Go-callback acceptable here.)
- **Phase 1 — the live routing DAG + built-in FX.** Compile `project.json` → Kahn
  schedule; pure-C callback + C ring (retire the Go callback); gain/EQ/comp/delay;
  **sidechain edge from compose plays as ducking** (kick→music, kick→808). This is
  the "LEGO context" milestone. Pure-Go reference mixer + determinism tests.
- **Phase 2 — CLAP host (selective).** `becky_pluginhost` with the CLAP backend;
  declare one trusted CLAP plugin in `project.json`; audio + MIDI + generic param
  sliders + automation; `"deterministic:false"` labeling. Generic params first,
  plugin GUI later.
- **Phase 3 — VST3 backend (now MIT) behind the same interface.** `//go:build vst3`;
  load one trusted VST3 from Jordan's Cubase collection through the *same* host ABI;
  param/automation/state. Optional embedded plugin GUI window.
- **Phase 4 — producer polish.** sfizz sampler upgrade; WASAPI exclusive "tracking"
  mode; tempo-synced delay/reverb; piano-roll edits write back to the timeline;
  bounce-to-WAV via the reference mixer; multicore layer execution if needed.

---

## 9. Top risks

- **WASAPI shared-mode latency floor (~10 ms).** Mitigation: ship shared mode for
  composing (fine), exclusive mode opt-in for tracking; expose period size; document
  the floor so it's not a surprise. citeturn0search1turn0search7
- **GC on the audio thread.** Mitigation: pure-C callback + C-allocated ring; Go
  never on the audio thread; the easy malgo-Go-callback path only in the Phase-0
  spike. citeturn0search0
- **cgo build weight** (miniaudio + DSP + CLAP + optional VST3 SDK). Mitigation:
  build tags per CLAUDE.md §3; vendor + pin each C dep; CI stays cgo-free via the
  pure-Go model + reference mixer.
- **Third-party plugin = nondeterminism + crashes.** Mitigation: selective
  allowlist; `"deterministic:false"` labeling; (later) out-of-process sandbox for
  crashy plugins. Keep built-ins as the deterministic default.
- **Plugin GUI hosting is its own swamp.** Mitigation: generic param sliders first;
  embed plugin windows only in Phase 3+, only for the few declared plugins.
- **Sample-accurate timing drift across tempo changes.** Mitigation: tick→sample
  off the tempo map in Go, unit-tested against hand-computed vectors; PPQ=480 shared
  with compose. citeturn2search5

---

## 10. Open decisions for Jordan

1. **Latency posture:** shared-mode default (~10 ms, coexists with Windows) with an
   opt-in exclusive "tracking" mode — acceptable? Or do you need exclusive/low
   latency as the default (takes over the audio device)?
2. **VST3 now or later:** VST3 went **MIT in Oct 2025**, so the old "defer VST3"
   reasoning is gone. Still do **CLAP-first then VST3** (Phase 2→3), or jump
   straight to **VST3** since your Cubase plugins are VST3? (CLAP-first is cleaner
   to build; VST3-first matches your existing collection.)
3. **Which "few trusted plugins"** should the selective host target first? Name 2–3
   (e.g. a compressor + a synth) so Phase 2/3 validates against real ones.
4. **Sampler engine:** start on **TinySoundFont** (tiny, SF2) and upgrade to
   **sfizz** (SFZ, real producer libraries) later — or go straight to sfizz?
5. **Determinism vs plugins:** confirm the rule — **built-ins stay bit-deterministic;
   any hosted plugin is a declared, UI-labeled `deterministic:false` exception.** OK?
6. **Reference mixer:** build the pure-Go offline renderer (gives CI an audible-
   correctness check *and* a bounce-to-WAV feature with zero cgo) — worth the effort,
   or skip until Phase 4?
7. **Scope check:** is "selective host + built-in instruments/FX + plays compose
   stems with live routing" the right line for a **Cubase-core replacement**, or do
   you also need audio-clip recording/tracking (mic/line in) in the engine's first
   real version?

---

> **Cloud-vs-local build split.** Cloud agent: the **pure-Go** layer (SMF reader,
> tempo map, transport, graph compile/Kahn/sidechain-layer, schedule, reference
> mixer) + all its unit tests + the cgo bridge *headers* and documented stubs.
> Local agent (Jordan's GPU/Windows box): wire the **C/C++** audio callback, DSP
> nodes, and plugin host to real miniaudio/CLAP/VST3, run `build-all-tools.bat`,
> and test against a real `becky-compose` project on real hardware. Each C boundary
> is left as a documented stub with an explicit `bh_*` / callback contract so the
> local agent only plugs in the native side. (CLAUDE.md §4 baton.)

### Sources

- Go realtime audio / GC-safe C callback + ring: pkg.go.dev gordonklaus/portaudio; gen2brain/malgo — https://pkg.go.dev/github.com/gordonklaus/portaudio , https://pkg.go.dev/github.com/gen2brain/malgo
- Lock-free ring buffer pattern: https://medium.com/@nathanbcrocker/implementing-a-lock-free-ring-buffer-in-go-ee36bba220ea
- miniaudio WASAPI latency (shared-mode floor, IAudioClient3, period size): https://github.com/mackron/miniaudio/issues/949 , https://github.com/mackron/miniaudio/discussions/1084 , https://learn.microsoft.com/en-us/windows-hardware/drivers/audio/low-latency-audio
- VST3 → MIT (3.8.0, Oct 2025), ASIO → GPL3: https://www.theaudioprogrammer.com/content/steinbergs-vst3-asio-sdks-go-open-source , https://www.kvraudio.com/news/steinberg-moves-vst-3-sdk-to-mit-open-source-license-asio-now-gplv3-65179 , https://librearts.org/2025/11/steinberg-relicenses-vst3-and-asio/
- CLAP (MIT, C ABI, single event queue, MIDI 2.0, threading): https://github.com/free-audio/clap , https://github.com/free-audio/clap-host , https://www.bitwig.com/stories/clap-the-new-audio-plug-in-standard-201/ , https://librearts.org/2022/06/introducing-clap/
- Graph scheduling (Kahn topo-sort, no per-block alloc, same-layer sidechain): https://lib.rs/crates/audio_graph , https://arxiv.org/pdf/2507.10534 , https://github.com/Tracktion/tracktion_engine
- JUCE / Tracktion licensing (GPL/commercial — reference only): https://docs.juce.com/master/classAudioProcessorGraph.html , https://forum.juce.com/t/gpl-compliance-question-when-redistributing-tracktion-engine-in-a-juce-project/68043
- Sample-accurate MIDI scheduling / PPQ / transport: https://cp3.io/posts/sample-accurate-midi-timing/ , https://forum.loopypro.com/discussion/46251/let-s-talk-about-midi-sequencer-timing
- Built-in instruments (TinySoundFont zlib single-file; sfizz BSD/SFZ): https://github.com/schellingb/TinySoundFont , https://zynthian.org/engines/_engine-list/engine-sfizz
