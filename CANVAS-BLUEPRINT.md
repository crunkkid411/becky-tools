# CANVAS-BLUEPRINT.md — the integration spine for Becky Canvas (the Cubase replacement)

> **Purpose.** Becky Canvas is meant to be Jordan's full **CUBASE/Maschine replacement**: ONE
> native app with a drum machine, piano roll, vocal/audio tracks, mixer + routing, and VST
> hosting — every one of them **AI-controllable AND fully manually controllable** (every click a
> human can do in Cubase). This document is the *integration blueprint* so a parallel agent army
> can build it without colliding: it names the ONE spine every panel plugs into, gives a disjoint
> per-component contract, and orders the work.
>
> **Status of this doc:** map + plan, written 2026-06-19 after reading the code. It does **not**
> add code. The honesty section below is blunt about what is real vs stubbed — read it first so
> nobody "builds on" a sine tone thinking it's a finished feature.
>
> **Authority order when docs disagree:** `GUI-RULES.md` (CANONICAL, ratified 2026-06-19) wins over
> `SPEC-BECKY-CANVAS.md` (design-only, 2026-06-15, pre-dates the ratified stack). Where this
> blueprint and either of those disagree, fix this file and tell Jordan (CLAUDE.md §5 rule).
> This doc belongs on the CLAUDE.md §5 "Doc map" beside GUI-RULES.md and SPEC-BECKY-CANVAS.md.

---

## 0. BRUTALLY HONEST CURRENT STATE (evidence-backed)

**Does the Canvas window open today?** Yes. `cmd/canvas/gui.go` (`//go:build gui`) opens a real
Gio (`gioui.org`, Direct3D 11, no cgo) window — verified in code: `app.Window`, dock of neon icon
buttons, a central canvas, an agent box, a collapsible output panel. `build-all-tools.bat` builds
it with `-tags gui` to `bin/becky-canvas.exe`. The handoff log records it launching live
(`becky-clip-work/verify-launch.png`-class evidence in CLAUDE.md §6). The headless `//go:build !gui`
`main.go` is a deterministic scene-dumper (emits `scene.json`) so `go build ./...` / CI stay green.

**What panels/modes exist in `cmd/canvas`?** Five *modes* are declared (`internal/canvas.Mode`):
`ask`, `video`, `daw`, `midi`, `drum`. What is actually drawn/interactive:

| Mode | In the canvas window today | Reality |
|---|---|---|
| `ask` | The agent box routes a typed phrase to a tool `.exe` and streams output. | **Real** (keyword routing + exec; `gui_tools.go`). Not a chat brain — it matches a keyword to a tool. |
| `drum` | A clickable **4×16** step grid (kick/snare/hat/clap), painted as neon squares. | **Real grid, but a TOY model.** It is a local `drumGrid struct { cells [4][16]bool }` in `gui_drum.go` — **not** `internal/drummachine` (16 pads, velocity, swing, kits, patterns/scenes/song). No velocity, no kit, 4 lanes hard-coded. |
| `midi` (piano) | A placeholder. ▶/■ transport shows but "piano — open a project.json, then ▶". | **Stub.** No piano-roll rendering, no note editing. `internal/dawmodel.pianoroll.go` (the real editable model) is **not** wired into the window. |
| `daw` | A scene of track lanes + clips drawn from a loaded `project.json`. | **Display-only.** `canvas.Scene` renders lanes/clips; there is **no** mixer UI, no fader/pan/route editing in the window. |
| `video` | A mode flag with no renderer. | **Stub in canvas.** (Real video lives in a *different* binary — `cmd/becky-nle` + `internal/videopreview` + the Rust sidecar.) |
| draw / record | Pen strokes on canvas; an 8-second mic record button. | **Real-ish** (pen is real; record execs `becky-daw-engine --record`, audio build only). |

**What makes sound today?** ▶ Play does **not** synth in-process. It serializes the toy drum grid
to a temp `project.json` and execs the sibling `becky-daw-engine --play-pattern-audio` (`gui_play.go`).
That engine's real sampler path (`internal/audioengine/sampler_engine.go`) is genuine
(velocity→gain, AmpEnv, Hermite resample, declick) and `--play-machine`/`--render-machine` are real
on `drummachine.Machine`. But the **canvas itself only knows how to feed it a 4×16 on/off grid**,
so none of that richness reaches Jordan through the Canvas window.

**The load-bearing finding — there are FOUR project models, and the Canvas window is wired to the
weakest one:**

1. `internal/canvas.Scene` — the **display** model the Gio window renders (lanes, clips, routing
   edges, transport, corrections). Built **from `music.Project`**, not from the editable models. No
   per-note, no mixer-strip edit ops. (`internal/canvas/scene.go`, `load.go`.)
2. `internal/dawmodel.Arrangement` — the **rich editable** model: MIDI+audio tracks, clips, notes,
   `Strip` (gain/pan/mute/solo/bus), `Bus` (+sidechain), corrections log, and **pure immutable edit
   verbs** for piano-roll (`AddNote/MoveNotes/ResizeNotes/SetVelocity/Transpose`), drum grid
   (`DrumGridOf/SetStep/Compile/ApplyDrumGrid`), and mixer (`SetGain/SetPan/RouteTo/AddSidechain`).
   **This is the real DAW model — and `cmd/canvas` does not import it for editing** (it imports it
   only in `gui_play.go` to build a throwaway play arrangement).
3. `internal/drummachine.Machine` — the **Maschine-class groovebox**: 16 pads (`sampler.Sound` per
   pad), pattern bank, scenes, song, choke groups. Converts losslessly to/from `dawmodel.DrumGrid`
   (`Pattern.ToDrumGrid` / `PatternFromDrumGrid`). **`cmd/canvas` does not use it at all.**
4. `internal/music.Project` — the **compose routing manifest** (per-track `.mid` refs + routing
   edges). This is what `canvas.Scene` is loaded from.

The other Becky GUIs are **further along than the Canvas in their own lanes** and prove the
patterns: `cmd/drummachine` (a separate Gio window) already imports `drummachine` + `machinectl` +
`samplelib` + `sampler` and loads real kits / plays real samples (CLAUDE.md §6, branch
`claude/drummachine-kit-wiring-20260619`). `cmd/becky-nle` is a separate Gio NLE on
`internal/videopreview` + the Rust sidecar. The C++ VST3/ASIO audio host is built and **verified on
Jordan's real 309-plugin library** via `internal/audiohost` + `internal/seam`.

**So the real task is NOT "build a DAW from scratch."** Almost every piece exists. The task is
**convergence**: make `cmd/canvas` host the *editable* models and the existing native sidecars
through ONE spine, instead of being a fifth, thinner re-implementation. The single biggest risk to
this project is **building more parallel models/panels instead of wiring the ones that exist.**

---

## 1. THE SPINE — the one set of types every panel plugs into

Everything below is **existing code**. The spine is four planes. Do not invent parallel types for
any of them.

```
                    ┌──────────────────────────────────────────────────────────────┐
                    │                    cmd/canvas  (Gio shell)                    │
                    │   ONE owner. Reads the project, renders panels, emits edits.  │
                    └──────────────────────────────────────────────────────────────┘
   reads ▲ renders                      │ emits typed edits                 ▲ events
         │                              ▼                                   │
 ┌───────┴────────────┐   ┌─────────────────────────────┐   ┌──────────────┴───────────────┐
 │  PROJECT PLANE      │   │   AI-CONTROL PLANE          │   │   AUDIO/VIDEO PLANE (sidecars)│
 │  (single source of  │   │   propose → preview → apply │   │   over the NDJSON seam        │
 │   musical truth)    │   │                             │   │                               │
 │  internal/dawmodel  │   │  internal/canvas transform  │   │  internal/seam  (Sidecar)     │
 │   .Arrangement      │   │   (Transformer, Proposal,   │   │  internal/audiohost (VST3)    │
 │  internal/drummachine│  │    Apply, overlay)          │   │  internal/videopreview (NLE)  │
 │   .Machine          │   │  internal/machinectl (drum) │   │  becky-audio-host.exe (C++)   │
 │  internal/sampler   │   │  internal/studio  (routing) │   │  becky-video-preview.exe(Rust)│
 │   .Sound / Kit16    │   │  internal/drumcmd (beats)   │   │  becky-daw-engine (Go+cgo)    │
 │  internal/music     │   │  research/becky-control-    │   │                               │
 │   .Project (import) │   │   schema.md = the JSON ABI  │   │                               │
 └─────────────────────┘   └─────────────────────────────┘   └───────────────────────────────┘
```

### 1.1 PROJECT PLANE — the single source of musical truth

**Decision (must be ratified before parallel work): `internal/dawmodel.Arrangement` is the Canvas
session model.** Rationale, grounded in the code:

- It is the only model with **edit verbs for all three surfaces at once** — piano roll
  (`pianoroll.go`), drum grid (`drumgrid.go`), and mixer/routing (`mixer.go`) — all pure/immutable,
  all already logging to a corrections log (the preference-learning substrate).
- It already carries **audio tracks** (`Kind: KindAudio`, `Peak` waveform overview) — the
  vocal/audio-track surface needs no new top-level model.
- The drum machine bridges into it **losslessly**: `drummachine.Pattern.ToDrumGrid(kit)` ↔
  `dawmodel.DrumGrid` ↔ `PatternFromDrumGrid`. So the 16-pad groovebox stays its own rich model
  for kit/pads, and its active pattern is a `DrumGrid` lane inside the Arrangement.
- It round-trips through the byte-stable SMF writer (`internal/music` `ParseSMF`/`ToFile`), so
  "open a `.mid`, edit, save" is already deterministic.

`internal/canvas.Scene` is **kept as the read-model the GUI draws** (it is good at that: sorted,
deterministic, lane/clip/routing geometry, viewport math). The convergence work (see §3, Step 1) is
a single new adapter `Arrangement → Scene` so the window renders from the editable model instead of
only from `music.Project`. **Do not delete `canvas.Scene`; do not add a second editable model.**

`internal/music.Project` stays the **import format** (compose output, routing manifest). Loading a
`project.json` becomes: `music.Project → dawmodel.Arrangement` (new, small, Step 1) instead of the
current `music.Project → canvas.Scene` shortcut that loses editability.

### 1.2 AI-CONTROL PLANE — propose → preview → apply (already partly built)

The **contract is already written**: `research/becky-control-schema.md` + `research/agent-control.md`
define `BeckyProjectState` (what the model reads) and `BeckyEditBatch` (`{summary, edits[]}` — a
flat, enum-discriminated action list the model emits, GBNF-lockable). **This is the canonical AI
ABI. Do not invent a different action schema.**

The **mechanism is already built**: `internal/canvas/transform.go` (`Transformer` interface,
`Proposal`, `Apply`, `RejectScene`, `StubTransformer`) + the Gio overlay `gui_overlay.go`
(before/after preview, ✓/✗, Enter/Esc). The rule is law (GUI-RULES §4): **nothing mutates until the
human approves.** The deterministic keyword parsers already exist per surface — `studio` (routing),
`drumcmd` (beats), `machinectl` (drum-machine) — and each **silently degrades** to keyword matching
when no model is present, so the loop works with the model off.

What is missing is the **applier from `BeckyEditBatch` onto `dawmodel.Arrangement`** (today
`Apply` only merges a partial `Scene` patch or logs a correction). That is the one real new piece of
the AI plane (§2.7).

### 1.3 AUDIO/VIDEO PLANE — the native sidecars over the NDJSON seam

**The seam is the law (GUI-RULES §2):** the GUI may only READ engine state and EMIT commands; audio
samples NEVER cross the JSON seam. `internal/seam` is built + tested (`query`/`command`/`event`,
every command async, large-buffer pump, send-on-closed-channel race fixed). On top of it:

- `internal/audiohost.Client` — typed driver for **`becky-audio-host.exe`** (C++ PortAudio + MIT
  VST3 SDK; ASIO when `BECKY_ASIO_SDK` is set). Verbs: `audio.devices/open/start/stop`,
  `vst.scan/load/param.list/param.set`, `note.on/off`, `render`. **Verified on Jordan's real
  309-plugin library** (loaded "808 Studio II", non-silent render corroborated by ffmpeg). This is
  the **VST hosting** surface — it exists and works headlessly.
- `internal/videopreview` — Go client for **`becky-video-preview.exe`** (Rust + wgpu, frame-accurate
  GPU decode + forensic overlay). The **video/vocal-waveform** rendering surface.
- `becky-daw-engine` (Go + cgo miniaudio, `-tags audio`) — the simple in-house play/record/render
  path the Canvas execs today. Keep for quick auditioning; the **C++ host is the destination** for
  real-time VST playback (the genuinely hard remaining piece is attaching a VST `IPlugView` editor
  into the Gio HWND — CLAUDE.md §6 "Wave 3").

> **Honesty flag (unresolved, for Jordan).** `research/becky-control-schema.md` (2026-06-19) routes
> music edits to **Hydrogen (OSC) / Maschine-standalone (MIDI via `internal/midilive`) / kdenlive**,
> while `GUI-RULES.md` (same day, ratified) routes audio to the **C++ VST3/ASIO host** and video to
> the **Rust/wgpu sidecar**. These are two different backend strategies. They are not mutually
> exclusive (the MIDI-out route can drive an *external* Maschine; the C++ host *is* the in-app
> instrument), but **the Canvas must pick ONE default audio destination** or it will grow two
> half-wired engines. This blueprint assumes the **GUI-RULES stack (C++ host) is the in-app
> destination**, and treats the MIDI/OSC route (`internal/midilive` + `cmd/becky-midi`, which is
> built + verified) as an optional "drive my external hardware" adapter, NOT the Canvas's own sound.
> Confirm before building the audio wiring.

---

## 2. PER-COMPONENT CONTRACTS (disjoint ownership)

Each component below names: **what exists**, **what's stubbed**, the **internal/ package that OWNS
it** (the agent working that component edits only that package + tests), and **how it reaches the Gio
shell**. The rule that prevents collisions: **agents build/extend `internal/*` packages in
parallel; `cmd/canvas` is touched by a SINGLE integration owner (§3), never in parallel.**

> File-ownership table (claim a row before you build — COLLAB-PROTOCOL.md):

| Component | OWNS (edit here, in parallel) | `cmd/canvas` files (single-owner pass only) | Sidecar |
|---|---|---|---|
| Project spine adapter | `internal/dawmodel` (+ new `arrangement_scene.go` / `internal/canvasbridge`) | `gui.go` (hold the `*Arrangement`) | — |
| Drum machine | `internal/drummachine`, `internal/sampler`, `internal/samplelib` | `gui_drum.go` (replace toy grid) | `becky-audio-host` / `becky-daw-engine` |
| Piano roll | `internal/dawmodel` (pianoroll verbs — exist) | `gui_piano.go` (NEW) | host for playback |
| Audio/vocal tracks | `internal/dawmodel` (audio Track/Peak), `internal/dsp` (peaks) | `gui_audio.go` (NEW) | `becky-audio-host` (record/monitor) |
| Mixer / routing | `internal/dawmodel` (mixer verbs — exist), `internal/studio` (plain-English routing — exists) | `gui_mixer.go` (NEW) | host (apply gain/FX) |
| VST rack | `internal/audiohost` (exists), `internal/seam` (exists) | `gui_vst.go` (NEW) | `becky-audio-host` (C++) |
| Transport / arrange | `internal/audioengine` (`Transport`, `sequencer.go` — exist), `internal/dawmodel` (arrangement) | `gui_transport.go` (extend `gui_play.go`) | host clock |
| AI control | `internal/canvas` (transform/overlay — exist), `internal/machinectl`/`studio`/`drumcmd` (parsers — exist) + NEW `internal/ctledit` applier | `gui_overlay.go` (exists), agent box in `gui.go` | — |

### 2.1 Drum machine

- **Exists:** `internal/drummachine.Machine` (16 pads, pattern bank, scenes, song, choke);
  `sampler.Sound`/`Kit16` (velocity layers, RR, AmpEnv); `samplelib` (drive scanner) + kit loaders
  (`kitload.go`, `kitportable.go`); the **real audio render** (`audioengine/sampler_engine.go`); the
  lossless `Pattern↔dawmodel.DrumGrid` bridge. A **better drum window already exists** in
  `cmd/drummachine` (real kit loading + sample browser + `machinectl` AI box).
- **Stubbed in Canvas:** the `cmd/canvas` drum mode is the 4×16 `drumGrid` toy — no pads, no
  velocity, no kit, no swing, no patterns.
- **Owner:** `internal/drummachine` (+ `sampler`/`samplelib`). These are done; the work is GUI-side.
- **Wires into shell:** replace `gui_drum.go`'s `drumGrid` with a renderer over the active
  `Arrangement` drum lane (`DrumGridOf`) **or** an embedded `drummachine.Machine` pad grid — reuse
  `cmd/drummachine`'s proven Gio drawing/picker code rather than re-deriving it. Clicks call
  `DrumGrid.SetStep` / `Machine.With*` (immutable) → new Arrangement on the App.

### 2.2 Piano roll

- **Exists:** `internal/dawmodel.pianoroll.go` — `AddNote`, `DeleteNotes`, `MoveNotes`,
  `ResizeNotes`, `SetVelocity`, `Transpose`, all pure/immutable, note IDs stable across edits,
  velocity overrides logged as corrections. `internal/canvas` already has a `PitchLane` data
  placeholder on each `Track.Lane` for the GUI to fill.
- **Stubbed:** there is **no piano-roll rendering or interaction** in `cmd/canvas` at all
  (`midi` mode is a placeholder line).
- **Owner:** `internal/dawmodel` (verbs done — only render/hit-test geometry helpers may be added,
  e.g. note-rect ↔ tick/pitch, mirroring `internal/canvas/viewport.go`'s time↔pixel math).
- **Wires into shell:** NEW `cmd/canvas/gui_piano.go` (`//go:build gui`): draw note blobs from the
  selected clip's `Notes`; drag = `MoveNotes`, edge-drag = `ResizeNotes`, the velocity lane =
  `SetVelocity`, double-click = `AddNote`. Y-axis swaps between chromatic (piano) and drum lanes —
  one editor, two axes (research/piano-roll.md).

### 2.3 Audio / vocal tracks

- **Exists:** `dawmodel.Track{Kind: KindAudio}` + `Peak{Tick,Min,Max}` (waveform overview points);
  `internal/dsp` (pure-Go WAV decode + FFT, used by becky-hum) can compute peaks; `internal/audioengine`
  `--record`/`--play` (cgo) records WAV; `becky-vox` (DTW vocal align) + `becky-hum` (hum→MIDI) are
  the vocal-processing tools.
- **Stubbed:** no audio-track lane editing in the window; the waveform visual in `gui_waveform.go`
  draws a *dropped file*, not an arrangement audio clip.
- **Owner:** `internal/dawmodel` (audio Track/Peak — exist; may add a `WithAudioClip`/peak-attach
  verb) + `internal/dsp` (peak extraction). Frame-accurate waveform *rendering* can stay Gio
  (cheap min/max bars) — no sidecar needed for audio overview.
- **Wires into shell:** NEW `cmd/canvas/gui_audio.go`: render audio clips as min/max waveform bars
  on their lane; trim/move are clip-level edits on the Arrangement; record routes through
  `becky-audio-host` (or the cgo daw-engine) and lands a WAV clip on the selected track.

### 2.4 Mixer / routing

- **Exists:** `dawmodel.mixer.go` — `Strip{Gain,Pan,Mute,Solo,Bus}`, `Bus{Out,Sidechain}`,
  `SetGain/SetPan/SetMute/SetSolo/RouteTo/AddSidechain` (immutable; gain overrides logged).
  `internal/studio` turns **plain English** ("sidechain the bass to the kick", "use Odin II on the
  lead") into routing edits on `music.Project` — **the Cubase-killer "one declaration, not 100
  clicks"** is already implemented and tested. `internal/mixplan` builds a deterministic mix.
- **Stubbed:** no mixer UI in the Canvas window (no faders, no routing view).
- **Owner:** `internal/dawmodel` (mixer verbs — done) + `internal/studio` (NL routing — done).
  Reconcile the one seam: `studio` edits a `music.Project` graph; the Canvas mixer edits
  `dawmodel.Strip/Bus`. A small mapping (`music.ProjEdge` ↔ `dawmodel.Bus.Sidechain`/`Strip.Bus`)
  belongs in the **project-spine adapter** (§3 Step 1), not duplicated.
- **Wires into shell:** NEW `cmd/canvas/gui_mixer.go`: one channel strip per track (fader=`SetGain`,
  pan=`SetPan`, mute/solo flags); a routing view drawing `Bus`/sidechain edges (the
  `canvas.RouteEdge` geometry already exists in `Scene`). Applying gain/FX live = a seam `command`
  to `becky-audio-host`.

### 2.5 VST rack

- **Exists and is VERIFIED:** `internal/audiohost.Client` over `internal/seam` →
  `becky-audio-host.exe` (C++, MIT VST3 SDK). `ScanVST` (crash-isolated child-process probe),
  `LoadVST`, `ParamList`, `SetParam`, `NoteOn/Off`, `Render`. Proven on Jordan's 309-plugin library.
- **Stubbed:** no VST UI in any Gio window yet; and the **genuinely hard open problem** (CLAUDE.md
  §6 Wave 3) is attaching a plugin's `IPlugView` **editor** into the Gio window's parent HWND — the
  one piece that needs real Win32/Gio-internals work. Param-list editing (sliders from `ParamList`)
  needs **no** editor embedding and should ship first.
- **Owner:** `internal/audiohost` + `internal/seam` (done) — plus a thin `internal/vstrack` mapping
  a track's instrument/insert chain to loaded `InstanceID`s if state needs to persist in the
  Arrangement (small, additive).
- **Wires into shell:** NEW `cmd/canvas/gui_vst.go`: a "scan" panel listing `ScanVST` results; load
  onto a track → show `ParamList` as labelled sliders (`SetParam` on drag); play routes notes via
  `NoteOn/Off`. **Editor-window embedding is a separate, later, single-focus task** — do not block
  the rack on it.

### 2.6 Transport / arrange

- **Exists:** `internal/audioengine.Transport` (BPM/PPQ/tick↔sample) + `sequencer.go`
  (`SequenceDrumGrid`/`SequenceNotes` → deterministically-ordered `[]ScheduledEvent`). `cmd/canvas`
  has a ▶/■ row (`gui_play.go`) that execs `becky-daw-engine`.
- **Stubbed:** play is one-bar-render-then-exec, drum-only; no playhead during playback, no arrange
  view, no song/scene timeline. `dawmodel` has no top-level Section/Timeline (red-team gap noted in
  CLAUDE.md §6).
- **Owner:** `internal/audioengine` (transport/sequencer — exist) + `internal/dawmodel` (add a
  `Section`/timeline if arrange-mode is built — additive).
- **Wires into shell:** extend `gui_play.go`/NEW `gui_transport.go`: feed the whole Arrangement (not
  just the drum grid) to the engine/host; draw a moving playhead from `event` ticks over the seam;
  arrange view places patterns/sections on a bar ruler.

### 2.7 AI control (cross-cutting — single applier, many parsers)

- **Exists:** the overlay + `Transformer`/`Proposal`/`Apply` (`internal/canvas`); the three keyword
  parsers (`studio`, `drumcmd`, `machinectl`) each with a `ModelParser` stub that degrades to
  keywords; the canonical JSON ABI (`research/becky-control-schema.md`).
- **Stubbed:** there is **no applier that takes a `BeckyEditBatch` and mutates a
  `dawmodel.Arrangement`** (today `Apply` only merges a `Scene` patch or logs a correction); the
  real local-model `Transformer` is a stub (`PickTransformer` returns `StubTransformer` until a
  model binary resolves — the local GPU boundary).
- **Owner:** NEW `internal/ctledit` (the deterministic `BeckyEditBatch → Arrangement` applier:
  resolve human-readable refs, range-check, drop-illegal-with-a-note, apply via the existing
  immutable verbs, log to `habits`). This is **the one substantial new package** the AI plane needs;
  it is pure Go, headless-testable, and disjoint from the GUI. The model `Transformer` is the local
  agent's GPU boundary (wire `llama-completion.exe` + GBNF per `agent-control.md` §3–4).
- **Wires into shell:** the agent box (already in `gui.go`) builds a compact `BeckyProjectState`
  from the current Arrangement selection, calls the `Transformer`, renders the returned batch in the
  **existing overlay**, and on ✓ runs `ctledit.Apply`. The "show me, don't do it" UX is already
  built — only the state-build + applier are new.

---

## 3. INTEGRATION ORDER (reused vs newly built)

The order is chosen so the spine lands first, panels attach independently, and `cmd/canvas` is only
ever edited by one owner per step. **"Reused" = exists today; "New" = the only code to write.**

**Step 0 — RATIFY (no code).** Confirm with Jordan: (a) `dawmodel.Arrangement` is the Canvas
session model; (b) the GUI-RULES C++ host is the in-app audio destination (MIDI/OSC route is
optional external-hardware); (c) `cmd/canvas` is the single converged app (vs keeping
`cmd/drummachine` + `cmd/becky-nle` separate and embedding them as modes). Without (a)–(c), parallel
work collides.

**Step 1 — THE SPINE (single owner; blocks everything).**
- *New:* an `Arrangement → canvas.Scene` (render adapter) and `music.Project → Arrangement` (import
  adapter), replacing the lossy `music.Project → Scene` path in `internal/canvas/load.go`. Map
  `music.ProjEdge`/routing ↔ `Strip.Bus`/`Bus.Sidechain` here once. Place it in `internal/dawmodel`
  if no import cycle results; if `dawmodel`→`canvas` would cycle, put it in a tiny new
  `internal/canvasbridge` package that imports both (decide at Step 1, with `go build`).
- *cmd/canvas:* `gui.go` holds `*dawmodel.Arrangement` as the source of truth; `scene` becomes a
  derived render-cache rebuilt after each edit. (Single integration pass.)
- *Reused:* `canvas.Scene` geometry/viewport, `music` SMF reader, the corrections log.
- **Done when:** opening a `project.json` shows lanes from the Arrangement, and a no-op round-trip
  (`Arrangement → Scene → render`) is byte-stable in a test.

**Step 2 — PANELS, IN PARALLEL (disjoint `internal/*`; each lands its own `gui_*.go` via the
integration owner).** All four models the panels need already exist, so these are mostly
GUI-rendering tasks over done verbs:
- **2a Drum machine** → reuse `cmd/drummachine`'s proven Gio grid/kit/browser; bind to the
  Arrangement drum lane / `drummachine.Machine`.
- **2b Piano roll** → `gui_piano.go` over `dawmodel.pianoroll` verbs.
- **2c Mixer/routing** → `gui_mixer.go` over `dawmodel.mixer` verbs + `studio` NL routing.
- **2d Audio/vocal tracks** → `gui_audio.go` over `dawmodel` audio Track/Peak + `dsp` peaks.

  These four touch **different new `gui_*.go` files** and **different `internal/*` packages**, so the
  agents do not collide; the integration owner merges each `gui_*.go` as it lands (one file per
  panel, appended to the Flex layout — same pattern as today's `layoutWorkColumn`).

**Step 3 — AI CONTROL (one new package, then a single GUI wire-up).**
- *New:* `internal/ctledit` (`BeckyEditBatch → Arrangement` applier, deterministic, tested).
- *cmd/canvas:* build `BeckyProjectState` from the selection; render the batch in the existing
  overlay; ✓ → `ctledit.Apply` → log to `habits`.
- *Reused:* the overlay, the parsers, `PickTransformer`. *Local boundary:* the real model
  `Transformer` (GPU).

**Step 4 — REAL AUDIO DESTINATION (local-verified).**
- Route transport/play from the Arrangement to **`becky-audio-host`** over the seam (not just the
  cgo daw-engine); draw a live playhead from `event`s.
- **VST rack** (`gui_vst.go`): scan/load/param-edit via `internal/audiohost` (all verified
  headlessly). **Editor-window (`IPlugView`) embedding into the Gio HWND is a separate, later,
  single-focus task** — the one genuinely hard remaining piece (CLAUDE.md §6 Wave 3); param sliders
  ship without it.

**Step 5 — VIDEO/VOCAL preview + arrange polish.** Wire `internal/videopreview` (Rust sidecar) into
a Canvas `video` mode (or embed `cmd/becky-nle` as a mode); add a `Section`/arrange timeline to
`dawmodel`. Frame-accurate scrub is local-verified only (no headless path).

**Reused-vs-new summary:** ~90% reused. Genuinely **new** code = three things: the
`Arrangement↔Scene` adapter (Step 1), the `gui_*.go` panel renderers (Step 2, thin — they call
existing verbs), and `internal/ctledit` (Step 3). Everything else — the models, the edit verbs, the
overlay, the seam, the VST host, the sampler engine, the NL routing parser — exists and in several
cases is verified on Jordan's hardware.

---

## 4. HONEST RISKS (the real ones)

1. **Parallel-model proliferation (HIGHEST risk, and it is a process risk, not a tech one).** There
   are already four project models and the Canvas is wired to the weakest. An agent army told to
   "build the piano roll / mixer / drum machine" will, by default, each invent their own state and a
   fifth/sixth/seventh model is born. **Mitigation:** Step 0 ratification + Step 1 lands the single
   spine FIRST; every panel agent is handed the Arrangement verb it must call and forbidden from
   adding a top-level model. This blueprint's whole reason to exist.

2. **Two GUIs already do what the Canvas should (`cmd/drummachine`, `cmd/becky-nle`).** Convergence
   means either embedding them as Canvas modes or porting their wiring in. Re-deriving their work is
   the wasteful default. **Mitigation:** Step 2a explicitly *reuses* `cmd/drummachine`'s Gio code;
   Step 5 reuses `cmd/becky-nle`. Decide in Step 0 whether Canvas absorbs them or stays a fourth app
   (the brief says ONE app — absorb).

3. **Making sound at all / the audio-destination split is unresolved.** Two same-day docs prescribe
   different engines (C++ VST3/ASIO host vs Hydrogen-OSC/Maschine-MIDI/kdenlive). Building toward
   both yields two half-engines. **Mitigation:** §1.3 picks the C++ host as the in-app destination
   and the MIDI/OSC route as optional external-hardware control; **Jordan must confirm.** Until then,
   the cgo `becky-daw-engine` audition path keeps the Canvas audible.

4. **cgo / native sidecars are local-only and unverifiable in CI.** The C++ host, the Rust video
   sidecar, and the cgo audio backend cannot be built or run in headless CI. This is inherent
   (GUI-RULES §3) and the seam was designed around it: **the Go brain + the NDJSON seam are fully
   unit-testable with a faked sidecar** (`internal/seam/fake.go`, `internal/audiohost/client_test.go`
   prove it). **Mitigation:** keep every panel's *logic* in pure-Go `internal/*` (tested in CI); let
   only the thin `gui_*.go` + the real sidecar runs be Jordan-verified. Never claim an audio/video
   feature "works" from a green `go test` — it must be heard/seen on the UR12/RTX 3070 (the
   verify-on-real-input rule, MEMORY.md).

5. **Gio limits (real, but bounded).** (a) **No in-window OS file drop on Windows** — confirmed
   against Gio v0.10 in `gui.go`'s own comment; the argv-on-launch + picker paths work, and the
   native sidecars get OS drop for free. Don't relitigate it. (b) **Gio cannot render video pixels**
   — that's exactly why video is a Rust sidecar, not a regression. (c) **Embedding a VST `IPlugView`
   editor into a Gio HWND** is the one genuinely hard unsolved piece (needs the window's parent HWND
   via `app.Win32ViewEvent`); param-slider editing sidesteps it entirely and ships first.

6. **Determinism vs a real-time, model-driven app.** The offline+deterministic invariant (CLAUDE.md
   §2) must survive live audio + a local model. The model is the only non-deterministic element, and
   the design already contains it: GBNF + temp-0 + single-slot for the proposal, and a **fully
   deterministic Go applier** downstream (`ctledit`), so model wobble degrades gracefully rather than
   corrupting the project. Audio rendering stays seeded (`sampler_engine` `--seed`). Keep the model
   strictly on the propose side of propose→approve→apply.

7. **The `canvas.Scene` ⇄ `dawmodel.Arrangement` adapter is a real surface that can drift.** Two
   representations of the same session means a mapping that must stay lossless in the edit→render
   direction. **Mitigation:** make `Scene` a pure *projection* of the Arrangement (one-way, rebuilt
   after each edit — never edited directly), and cover the projection with a round-trip test. The
   Arrangement is the only thing ever mutated.

---

## 5. One-paragraph bottom line for the orchestrator

Becky Canvas **opens today** as a real Gio window, but it is wired to the *weakest* of four existing
project models and most "modes" are placeholders (the drum grid is a 4-lane toy; piano roll, mixer,
and VST are not in the window at all). **Almost nothing needs to be invented** — the rich editable
model (`dawmodel.Arrangement` with piano-roll/drum/mixer verbs), the Maschine-class drum machine
(`drummachine` + `sampler` + `samplelib`), the plain-English routing parser (`studio`), the
propose→preview→apply overlay (`internal/canvas`), the NDJSON seam, and the **VST3 host verified on
Jordan's 309 plugins** (`audiohost`) all exist. The job is **convergence on one spine**: ratify
`dawmodel.Arrangement` as the session model (Step 0), land the `Arrangement→Scene` adapter so the
window renders from it (Step 1), then build the four panels in parallel over *existing* edit verbs in
disjoint `internal/*` packages with `cmd/canvas` edited by a single integration owner (Step 2), add
the one new pure-Go piece — the `BeckyEditBatch→Arrangement` applier (Step 3) — and finally route
real audio/VST through the C++ host and video through the Rust sidecar, local-verified (Steps 4–5).
The dominant risk is not technical; it is **agents building parallel models instead of wiring the
ones that exist** — which is the exact thing this blueprint is here to prevent.
