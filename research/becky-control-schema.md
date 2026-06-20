# becky control schema — one JSON contract any AI uses to read STATE and emit EDITS

**The "full context" layer for the music-app pivot.** This defines a single, deterministic
JSON contract so that ANY model (a local Qwen3-4B, a cloud Claude, a keyword parser) can (a)
**read** the current musical project state and (b) **emit edits** — and have those edits map
cleanly onto the THREE backends becky can now drive: **Hydrogen (OSC)**, **Maschine 2
standalone (MIDI)**, and **kdenlive / melt (video)**.

Date: 2026-06-19. This doc is the *contract*; it does not add code beyond `internal/midilive`
+ `cmd/becky-midi` (Part 1, the MIDI-send route). It is the sibling of
[`research/agent-control.md`](agent-control.md): that doc covers the *model/prompt/constraint*
side (which GGUF, GBNF grammar, token budget, propose-preview-apply UX). **This doc covers the
STATE + EDIT JSON shapes and how each edit reaches a real backend.** Read both before building
the control layer; where they overlap, the action schema here is the same superset as
agent-control.md §2.2 — kept identical on purpose.

---

## 0. Bottom line (the design, up front)

1. **One state object, one edit object.** The AI reads `BeckyProjectState` (a compact snapshot)
   and returns `BeckyEditBatch` (`{summary, edits[]}`). Both are plain, minifiable JSON. This is
   the exact shape becky already uses everywhere else (`dawmodel`, `drummachine`,
   `internal/canvas` propose/apply), so it costs nothing new conceptually.

2. **Edits are backend-agnostic; a thin ADAPTER lowers each edit to whichever backend owns the
   target.** `set_tempo` becomes an OSC message to Hydrogen, a tempo-meta on a Maschine MIDI
   render, or a project-profile change in kdenlive — the AI never knows or cares which. The
   adapter is deterministic Go; the AI emits the same `{"op":"set_tempo","bpm":142}` regardless.

3. **Propose → preview → apply is mandatory** (becky's hard rule + `internal/canvas` already
   implements it). The AI's `BeckyEditBatch` is a *proposal*. Nothing touches a backend until the
   human approves. Every applied edit is logged via `habits.AppendCorrectionLog` so becky learns
   Jordan's habitual fixes.

4. **Deterministic + degrade-never-crash.** Same state + same edit batch → same backend calls.
   An edit whose target/range is illegal is dropped-with-a-note, never a panic. A backend that is
   not running yields a typed degrade error and a partial apply, not a crash.

---

## 1. The STATE object — what the AI reads

`BeckyProjectState` is a compact, read-only snapshot. It is assembled by becky (the AI never
ingests raw project files — same "give the model only what it needs" funnel as the 500GB
forensic path). Keep it **minified** in practice; expanded here for readability. Field names and
types mirror becky's real Go types (`drummachine.Machine`/`Kit`/`Pad`, `dawmodel.DrumGrid`,
`music.Project`) so serialisation is a straight marshal.

```jsonc
{
  "schema": 1,                       // BeckyProjectState schema version
  "domain": "music",                 // "music" | "video"  — which backend family is active
  "backend": "maschine",             // "hydrogen" | "maschine" | "kdenlive" — the live target (informational)

  // ---- transport / global (music) ----
  "tempo": 140,                      // BPM
  "swing": 0.12,                     // 0..1  (0 == straight; see groove note §4.4)
  "key": "F#min",                    // optional musical key
  "bars": 4,                         // length of the active pattern/arrangement
  "ppq": 96,                         // ticks per quarter (Maschine/SMF resolution)

  // ---- the kit: 16 pads (mirrors drummachine.Kit / drummachine.Pad) ----
  "kit": {
    "name": "BVKER Trap Soul",
    "pads": [
      {"index":0,"name":"Kick","midiNote":36,"sample":"X:/Splice/.../kick_808.wav","level":1.0,"pan":0.0,"chokeGroup":0,"mute":false},
      {"index":1,"name":"Snare","midiNote":38,"sample":"X:/.../snare.wav","level":0.9,"pan":0.0,"chokeGroup":0,"mute":false},
      {"index":2,"name":"Hat","midiNote":42,"sample":"X:/.../chh.wav","level":0.8,"pan":0.1,"chokeGroup":1,"mute":false}
      // … up to 16
    ]
  },

  // ---- the active pattern as a step grid (mirrors dawmodel.DrumGrid) ----
  // ONE selection is sent in full; the rest of the bank is referenced by name, not dumped.
  "selection": {
    "kind": "drum_pattern",          // "drum_pattern" | "notes" | "video_timeline"
    "pattern": 1,
    "steps": 16,
    "lanes": [
      {"name":"kick","note":36,"grid":"x...x...x...x..."},   // one char per 16th step (x=hit, .=rest)
      {"name":"snare","note":38,"grid":"....x.......x..."},
      {"name":"hat","note":42,"grid":"x.x.x.x.x.x.x.x."}
    ]
  },

  // ---- mixer / FX buses (mirrors becky-wire's music.Project routing graph) ----
  "buses": [
    {"name":"Drums","fx":["comp","glue"],"volumeDb":-3.0},
    {"name":"Master","fx":["limiter"],"volumeDb":0.0}
  ]
}
```

For a **piano-roll** selection, `selection` is notes instead of a grid (rows are
`[pitch, start_beats, dur_beats, velocity]`, the AbletonMCP/REMI convention from
agent-control.md §1.4):

```jsonc
"selection": {"kind":"notes","track":9,"notes":[[60,0.0,0.5,100],[63,0.5,0.5,90]]}
```

For the **video** domain, `selection` is a timeline (maps to kdenlive/melt — see §5.4):

```jsonc
{
  "schema": 1, "domain": "video", "backend": "kdenlive",
  "fps": 30, "width": 1920, "height": 1080,
  "selection": {
    "kind": "video_timeline",
    "tracks": [
      {"name":"V1","clips":[
        {"src":"X:/Footage/a.mp4","in":0.0,"out":12.5,"at":0.0},
        {"src":"X:/Footage/b.mp4","in":3.0,"out":8.0,"at":12.5}
      ]}
    ]
  }
}
```

### 1.1 Why this shape

- **Compact + flat where it's high-volume.** Grids are one char per step; note rows are 4-element
  arrays. That is where the 30–60% token savings land (agent-control.md §1.3). The nested
  graph (kit, buses) stays minified JSON.
- **Stable-prefix-first for prompt caching.** When sent to a local model, order it
  `[schema + kit + buses + tempo/key]` (stable) then `[selection]` (volatile) so `cache_prompt`
  makes repeated turns near-instant (agent-control.md §1.5).
- **One marshal from becky's real types.** `kit.pads[]` is `[]drummachine.Pad` minus the audio
  internals; `selection.lanes[]` is `dawmodel.DrumGrid.Lanes` rendered as grid strings; `buses[]`
  is the `music.Project` bus list. No new model invented.

---

## 2. The EDIT object — what the AI emits

`BeckyEditBatch` is the proposal. Identical structure to agent-control.md §2.2 (a flat,
enum-discriminated action list — the shape small models get right and a GBNF grammar locks
tight). `summary` is a one-line plain-English description the human sees as the headline.

```jsonc
{
  "summary": "Half-time the kick, add a snare on beat 4, and bring the drum bus down 3 dB",
  "edits": [

    // ---- DRUM MACHINE / STEP SEQUENCER ----
    {"op":"set_step",      "track":"kick", "pattern":1, "step":4,  "on":true, "velocity":110},
    {"op":"set_pattern",   "track":"snare","pattern":1, "grid":"....x.......x..."},  // full step string
    {"op":"clear_track",   "track":"hat",  "pattern":1},
    {"op":"transform_beat","track":"kick", "pattern":1, "kind":"half_time"},          // half_time|double_time|humanize|busier|sparser|swing|shift
    {"op":"add_fill",      "track":"snare","pattern":1, "into_beat":4, "kind":"roll"},

    // ---- PADS / SOUNDS / KIT ----
    {"op":"load_kit",      "kit":"BVKER/Trap Soul", "track":null},                    // null = whole machine
    {"op":"set_pad_sample","pad":3, "sample":"X:/Splice/.../kick_808.wav"},
    {"op":"set_pad_param", "pad":3, "param":"decay", "value":0.4},                    // tune|decay|pan|level|choke

    // ---- PIANO ROLL / NOTES ----
    {"op":"add_notes",     "track":"lead", "notes":[[60,0.0,0.5,100],[63,0.5,0.5,90]]},
    {"op":"remove_notes",  "track":"lead", "range":[0.0, 2.0]},
    {"op":"transpose",     "track":"lead", "range":"selection", "semitones":12},
    {"op":"quantize",      "track":"lead", "range":"selection", "grid":"1/16", "strength":1.0},
    {"op":"set_velocity",  "track":"lead", "range":"selection", "value":96},

    // ---- TEMPO / GROOVE ----
    {"op":"set_tempo", "bpm":142},
    {"op":"set_swing", "amount":0.16},
    {"op":"set_key",   "key":"F#min"},

    // ---- MIXER / FX (maps to becky-wire's studio graph) ----
    {"op":"set_volume", "target":"drums", "value":"-6dB"},
    {"op":"set_pan",    "target":"lead",  "value":"L30"},
    {"op":"mute",       "target":"hat",   "on":true},
    {"op":"solo",       "target":"kick",  "on":true},
    {"op":"add_fx",     "target":"drums", "fx":"glue_comp", "after":"eq"},
    {"op":"sidechain",  "source":"kick",  "dest":"bass", "amount":0.7},

    // ---- TRANSPORT / ARRANGEMENT ----
    {"op":"transport", "action":"play"},                                             // play|stop|record|loop
    {"op":"set_loop",  "range":[0,4]},
    {"op":"duplicate_pattern", "track":"kick", "from":1, "to":2},

    // ---- VIDEO (kdenlive/melt) ----
    {"op":"add_clip",    "track":"V1", "src":"X:/Footage/b.mp4", "in":3.0, "out":8.0, "at":12.5},
    {"op":"trim_clip",   "track":"V1", "index":0, "in":0.0, "out":10.0},
    {"op":"remove_clip", "track":"V1", "index":1},
    {"op":"set_marker",  "at":4.0, "label":"chorus"}
  ]
}
```

Rules (same as agent-control.md §2.2, restated so this doc stands alone):
- `op` is a **closed enum** — a GBNF grammar makes an unknown verb impossible to emit.
- Args are **flat scalars or short arrays** — small models stay reliable; the grammar stays small.
- `track`/`target`/`range` accept **human-readable references** (`"kick"`, `"track 3"`, `"last"`,
  `"-6dB"`, `"selection"`, `"8 bars"`); the deterministic Go applier resolves them and rejects an
  ambiguous one with a plain-English note.
- This is a **superset for design**; v1 ships the subset in agent-control.md §6 (the verbs that
  map onto existing `dawmodel.DrumGrid` / becky-drum / becky-wire).

---

## 3. The apply pipeline (deterministic, propose-then-apply)

```
BeckyProjectState ──▶ [local model | claude | keyword parser] ──▶ BeckyEditBatch (proposal)
                                                                        │
                                              (1) VALIDATE (Go, deterministic)
                                                  resolve refs, range-check (pitch/vel 0-127,
                                                  step in range, dB sane); drop/flag illegal
                                                  edits with a note — degrade, never crash
                                                                        │
                                              (2) PREVIEW (before → after diff in the UI)
                                                  the internal/canvas "show me, don't do it"
                                                  overlay; ✓ Apply / ✗ Reject (Enter/Esc)
                                                                        │
                                            ┌── ✗ reject: discard (optionally re-propose with feedback)
                                            │
                                            └── ✓ apply:
                                              (3) LOWER each edit to its backend via the ADAPTER (§5)
                                                  hydrogen → OSC | maschine → MIDI | kdenlive → XML/melt
                                              (4) LOG the correction (habits.AppendCorrectionLog)
                                              (5) SNAPSHOT for undo (forensic non-overwrite)
```

Steps 1–2 and 4–5 already exist in becky (`internal/canvas` overlay, `internal/habits`,
becky-wire's immutable `Apply`). The new piece the pivot needs is **step 3: the adapter** —
§5 specifies exactly what each `op` lowers to for each backend.

---

## 4. The three backends (capabilities & how becky reaches each)

| Backend | What it is | How becky drives it | Live vs offline | Status in this repo |
|---|---|---|---|---|
| **Hydrogen** | Open-source pattern-based drum machine; has an **OSC** control surface | OSC/UDP messages to its listener (`/Hydrogen/...`) | **Live** (controls the running app in real time) | adapter lives in `internal/hydrogen` (separate effort — NOT in this branch) |
| **Maschine 2 standalone** | NI's groovebox; pads are **MIDI-mappable** (MIDI-learn) | **Live MIDI** via `internal/midilive` (winmm, no cgo) → a virtual port (loopMIDI) → Maschine | **Live** (fires mapped pads now) | **`internal/midilive` + `cmd/becky-midi` — BUILT in THIS branch (Part 1)** |
| **kdenlive / melt** | Open-source NLE (kdenlive) + its headless renderer (melt/MLT) | Write/patch a **kdenlive project XML** (MLT) and/or drive **melt** for render | **Offline** (file-based; render then open) | adapter lives in `internal/kdenlive` (separate effort — NOT in this branch) |

### 4.1 What "maps to a backend" means per domain

- **music** edits (`set_tempo`, `set_step`, `set_pattern`, `transform_beat`, `set_pad_*`,
  mixer/FX, transport) target **Hydrogen OR Maschine** depending on `state.backend`.
- **video** edits (`add_clip`, `trim_clip`, `remove_clip`, `set_marker`) target
  **kdenlive/melt**.
- A few edits are **shared/coordination-only** and don't lower to a backend message — e.g.
  `set_key` (advisory; affects suggestion logic, no Hydrogen/MIDI equivalent).

### 4.2 Important honesty note on parity

The backends are **not** feature-equal, and the adapter must say so rather than pretend. Hydrogen
has no per-note piano-roll, so `add_notes` to Hydrogen is a degrade ("Hydrogen has no melodic
track — apply to the Maschine MIDI target instead"). Maschine via MIDI can *trigger* pads and
play notes but cannot, over MIDI, *load a kit by name* (that's a NI-internal action) — so
`load_kit` against the Maschine backend degrades to a note ("load the kit in Maschine; becky will
map the pads"). Each unmapped (op, backend) pair returns a plain-English "not supported on this
backend" note — **never** a silent no-op and **never** a crash.

### 4.3 Backend selection

`state.backend` (and the parallel `domain`) is the single switch. The applier reads it once and
routes the whole batch. An edit can override per-action only where it makes sense (e.g. a future
`"backend":"maschine"` field on one edit); v1 keeps it batch-level for simplicity.

### 4.4 Groove/swing convention (pick one, document it)

Swing `0.0` == straight, `1.0` == maximum (matches `dawmodel`/`drummachine` where `swingNeutral`
is normalised). When lowering to a backend that uses a different convention (MPC 50%=straight),
the adapter converts. This is the documented choice from `research/maschine-groove-smartplay.md`
— stated here so the schema value is unambiguous.

---

## 5. The ADAPTER — every edit → a concrete backend message

This is the load-bearing table: for each `op`, what the deterministic Go adapter emits to each
backend. `—` means "no native equivalent → degrade with a plain-English note" (§4.2).

### 5.1 Tempo / groove / transport

| edit `op` | Hydrogen (OSC) | Maschine (MIDI) | kdenlive/melt |
|---|---|---|---|
| `set_tempo {bpm}` | `/Hydrogen/BPM <bpm>` (float) | tempo-meta on the SMF/render clock; live: it's the host clock so send MIDI Clock at the new BPM if becky owns the clock | n/a (music) |
| `set_swing {amount}` | `/Hydrogen/...swing` (humanize/swing param) | applied to the step→tick math before notes are sent (becky-drum swing) | n/a |
| `transport {play}` | `/Hydrogen/PLAY` / `TRANSPORT_PLAY` | MIDI **Start** (0xFA) / **Stop** (0xFC) / **Continue** (0xFB) realtime bytes | n/a (music); for video, preview play is a GUI action |
| `set_loop {range}` | `/Hydrogen/...loop` if exposed, else — | loop is becky-side: re-stream the scheduled bar N times (`cmd/becky-midi --bars`) | n/a |
| `set_key {key}` | — (advisory) | — (advisory) | — (advisory) |

### 5.2 Drum machine / step sequencer

| edit `op` | Hydrogen (OSC) | Maschine (MIDI) |
|---|---|---|
| `set_step {track,step,on,velocity}` | `/Hydrogen/TOGGLE_GRID_CELL <pattern> <instrument> <step>` (or NOTE_ON for the cell) | on the next pass, the scheduled bar includes/excludes a NoteOn at that step on the pad's `midiNote` (the `midilive.BuildDrumPattern` schedule is rebuilt from the grid) |
| `set_pattern {track,grid}` | per-cell TOGGLE to match the grid string | rebuild that lane's step schedule from the grid string |
| `clear_track {track}` | clear that instrument's cells | drop that lane from the schedule |
| `transform_beat {kind}` | apply becky-drum transform → re-emit cells | apply becky-drum transform (half/double/humanize/swing) → rebuild the schedule |
| `add_fill {into_beat,kind}` | becky-drum fill → cells | becky-drum fill → schedule |

How Maschine fires: each grid lane has a `midiNote` (GM percussion: kick 36, snare 38, hat 42 —
the `midilive.NoteKick/NoteSnare/NoteHat` constants). `midilive.BuildDrumPattern` turns the grid
into a timed `[]ScheduledMessage`; `cmd/becky-midi send` streams them to the named loopMIDI port;
Maschine (pads MIDI-learned to those notes) fires. **This path is built and verified in Part 1.**

### 5.3 Pads / sounds / mixer / FX

| edit `op` | Hydrogen (OSC) | Maschine (MIDI) | notes |
|---|---|---|---|
| `load_kit {kit}` | `/Hydrogen/...` load drumkit by name (Hydrogen supports named kits) | — (NI-internal; degrade with a note) | becky-side: update `state.kit`; Hydrogen can actually load it |
| `set_pad_sample {pad,sample}` | set instrument layer sample | — (degrade) | Maschine sample-swap is NI-internal |
| `set_pad_param {pad,param,value}` | `/Hydrogen/...` instrument param (pan/level/etc.) | a mapped **MIDI CC** if the producer learned that knob, else — | level/pan often CC-mappable on Maschine |
| `set_volume {target,value}` | `/Hydrogen/...strip volume` | mapped CC (e.g. CC7 on the pad's channel) or — | becky-wire owns the bus-graph truth |
| `mute`/`solo {target}` | `/Hydrogen/...MUTE`/`SOLO` | mapped CC / note, else — | |
| `add_fx`/`set_fx_param`/`sidechain` | — (Hydrogen FX is limited) | — (not over MIDI) | these live in becky-wire's `music.Project` graph; the AI edits the graph, the render/mix applies it |

The mixer/FX edits are primarily **becky-wire graph edits** (the existing `internal/studio`
`Apply`), not live backend messages — the AI changes the routing/FX graph, and the deterministic
mix (becky-mix) realises it. Live OSC/CC is best-effort on top.

### 5.4 Video (kdenlive / melt)

| edit `op` | kdenlive project XML (MLT) | melt (headless render) |
|---|---|---|
| `add_clip {track,src,in,out,at}` | append a `<entry producer=… in=… out=…>` to the track `<playlist>` at position `at` | `melt <src> in=… out=…` as a producer in the tractor |
| `trim_clip {track,index,in,out}` | edit that entry's `in`/`out` | re-emit with new in/out |
| `remove_clip {track,index}` | drop that `<entry>` (insert a `<blank>` to preserve timing if needed) | omit from the producer list |
| `set_marker {at,label}` | add a guide/marker at `at` | n/a (render-time markers are advisory) |

kdenlive's project is an MLT XML document; the adapter patches it deterministically (becky already
re-encodes with ffmpeg for `becky-clip`/`internal/reel`, so the render half is familiar). melt is
the headless renderer when no GUI is wanted. **Note:** `internal/reel` already does frame-accurate
ffmpeg assembly with a forensic timecode overlay; for the *forensic* video path that remains the
tool of record — the kdenlive/melt adapter is for the *creative* NLE path Jordan also wants.

---

## 6. Worked example (end-to-end, music → Maschine)

User (with the kick lane selected) types: *"make the kick half-time and drop the drum bus 3 dB."*

1. becky assembles `BeckyProjectState` (`backend:"maschine"`, the kick lane grid in `selection`).
2. The model returns:
   ```json
   {"summary":"Half-time the kick and lower the drum bus 3 dB",
    "edits":[{"op":"transform_beat","track":"kick","pattern":1,"kind":"half_time"},
             {"op":"set_volume","target":"drums","value":"-3dB"}]}
   ```
3. Validate: both ops legal; `"drums"` resolves to the Drums bus; `-3dB` parses. ✓
4. Preview: the overlay shows the kick grid `x...x...x...x...` → `x.......x.......` (half-time) and
   Drums bus `-3.0 dB` → `-6.0 dB`. Jordan presses **Enter** (✓).
5. Lower to backend:
   - `transform_beat half_time` → becky-drum rebuilds the kick lane → `midilive.BuildDrumPattern`
     produces the new `[]ScheduledMessage` → on the next loop pass `cmd/becky-midi` streams it to
     the loopMIDI port → Maschine's kick pad fires at the half-time positions.
   - `set_volume drums -3dB` → becky-wire graph edit (and a mapped CC7 if the producer learned the
     drum-bus fader).
6. Log both edits via `habits.AppendCorrectionLog`; snapshot for undo.

The AI emitted **one backend-agnostic batch**; the adapter did the Maschine-specific lowering. Swap
`backend` to `"hydrogen"` and the *same* batch lowers to OSC instead — that is the coherence the
pivot needs.

---

## 7. What is built vs designed (honesty)

- **BUILT + verified in this branch (Part 1):** `internal/midilive` + `cmd/becky-midi` — the
  **Maschine MIDI send route**. Real ports enumerated through pure-Go winmm; a kick/snare/hat
  pattern streamed to a real MIDI port (open + all messages sent, exit 0; verified on the GS
  Wavetable synth and by named-port open). The `set_step`/`set_pattern`/`transform_beat` →
  `BuildDrumPattern` → port lowering in §5.2 is real today via `cmd/becky-midi send`.
- **DESIGNED here (this doc):** the `BeckyProjectState` / `BeckyEditBatch` JSON contract and the
  full adapter mapping for all three backends. The Hydrogen OSC adapter (`internal/hydrogen`) and
  the kdenlive/melt adapter (`internal/kdenlive`) are **separate efforts not in this branch** —
  this doc is their contract so they slot in without re-litigating the schema.
- **Already exists in becky and is reused, not rebuilt:** the action-schema shape + model
  constraint (`research/agent-control.md`), the propose/preview/apply overlay
  (`internal/canvas`), the becky-drum transforms (`internal/drumcmd`), the routing/FX graph and
  its `Apply` (`internal/studio`/becky-wire), the drum data model (`internal/drummachine`,
  `internal/dawmodel`), and preference learning (`internal/habits`).

## 8. v1 cut (ship first) vs later

- **v1 (music → one backend):** state object + edit batch + the verbs
  `set_step`, `set_pattern`, `transform_beat`, `set_tempo`, `set_swing`, `transport`,
  `set_volume`/`mute`/`solo`, lowered to **Maschine MIDI** (built) via `cmd/becky-midi`, behind the
  canvas propose/preview/apply overlay. Degrade to the keyword parser when no model is present
  (becky-wire/becky-drum already do this).
- **Later (additive):** the Hydrogen OSC adapter; the kdenlive/melt video adapter + the video
  edits; pad/kit/sample/FX/sidechain lowering; per-edit `backend` override; re-propose-on-reject.

---

## Sources / cross-refs

- [`research/agent-control.md`](agent-control.md) — model, GBNF grammar, token budget, the same
  action-schema superset (§2.2), propose→preview→apply (§5). **Read first.**
- becky types this doc mirrors: `internal/drummachine` (Machine/Kit/Pad), `internal/dawmodel`
  (DrumGrid/Arrangement), `internal/studio` + becky-wire (routing graph), `internal/canvas`
  (propose/preview/apply overlay), `internal/habits` (correction learning),
  `internal/midilive` + `cmd/becky-midi` (the Maschine MIDI route, Part 1).
- Backend references: Hydrogen OSC control surface (`/Hydrogen/...` namespace); NI Maschine
  MIDI-learn (pads as MIDI-mappable triggers); kdenlive/MLT project XML + the melt headless
  renderer.
