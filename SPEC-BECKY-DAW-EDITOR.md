# SPEC-BECKY-DAW-EDITOR.md — the editing UX (piano roll, drum machine, arrangement, mixer)

> **STATUS: design only — NOT built (2026-06-15).** The **editor / UI layer** that
> turns `becky-canvas` (SPEC-BECKY-CANVAS.md) into the thing a producer actually
> edits in: a shared in-memory editable MIDI model, a **piano roll**, a **drum
> machine / step sequencer**, and an **arrangement + mixer** over `project.json`'s
> routing DAG. This is the surface for `becky-compose`'s stems (SPEC-BECKY-COMPOSE.md,
> BUILT). It is **NOT** the audio engine — synthesis, VST/CLAP, the real-time
> callback, and the sidechain *render* live in SPEC-BECKY-DAW-ENGINE.md (parallel).
> This spec owns the **model and the editing interactions**; the engine owns the
> sound. Goal stated by Jordan (pro producer, 15+ yrs, escaping Cubase): "becky
> drum machine, piano roll, becky daw functionality" — lightweight, AI-friendly,
> deterministic, MIDI-first (he tweaks the stems). Awaits go/no-go.

---

## 1. Intent (read carefully)

Cubase is a 30-year retained-mode monolith that is **not AI-friendly** (no clean
state to hand a model, 100-click routing) and **heavy**. becky already generates
the hard part — a full, theory-correct, multi-track arrangement
(`becky-compose`) — so the editor's job is **not** "be a DAW from scratch." It is:

1. **Make the generated stems editable in place** — move/add/delete/quantize notes,
   nudge velocities, redraw a drum grid — without ever leaving becky's deterministic
   model. The stems are a *starting point a producer tweaks*, not a black box.
2. **Round-trip any MIDI** — becky-compose output **or** an imported `.mid` (a
   Cubase export, a loop pack, a stem from anywhere) parses back into the same
   model and is editable identically. Generation and import converge on one model.
3. **Keep the "LEGO context" the user likes about ACE-Step** — editing one stem
   shows the others as context (chord tones under the melody, the kick under the
   808), and "regenerate just this stem, keep the rest" is a **deterministic**
   operation, not a re-roll of the whole song ([ACE-Step lego/complete tasks][aslego]).
4. **Beat Cubase where becky's posture is structurally better** — one-declaration
   sidechain (already in `project.json`), genre-aware generate, no-click routing,
   a model-readable project. Match Cubase where a producer's muscle memory demands
   it (key editor, drum editor, quantize, automation), defer the long tail.

The editor is **immediate-mode** (Dear ImGui `DrawList`, per SPEC-BECKY-CANVAS §3):
the whole UI is rebuilt every frame from the model, so "edit a note" is "mutate the
model; next frame redraws" — no widget tree, no signal/slot graph, no retained
scene to keep in sync ([Dear ImGui immediate-mode + DrawList][imgui], [ocornut/imgui][imguigh]).
That is exactly why it is cheap to morph modes and trivial to hand the model to an AI.

---

## 2. The shared editable MIDI model (the load-bearing layer)

Everything below edits **one** model. Today `becky-compose` builds `Song` →
`[]NamedTrack` → `*Track` (`internal/music/`), and `Track` is an **append-only,
write-once** structure: `Track.events []Event` is unexported, `Event.ord` is
unexported, and the only mutators are `Note()/NoteOn()/NoteOff()/Program()` —
great for *emitting* a fixed arrangement, useless for *editing* one. We extend the
model with an editable note layer and a reader, **without breaking the byte-stable
writer** (the determinism test in `music_test.go` must stay green).

### 2.1 An editable note view (`internal/music/edit.go`, NEW)

The writer stores low-level paired note-on/note-off `Event`s. Editing wants whole
**notes** (start, duration, pitch, velocity, channel) you can move as a unit. Add an
editable note model that is the *source of truth while editing* and **compiles down
to the existing `Track`** for rendering/saving — reusing `Track.Note()` so the
byte-stable encoder (`File.Bytes()`) is untouched.

```go
// internal/music/edit.go (NEW — pure Go, no new deps)

// Note is an editable note: a single object the UI moves/resizes/retunes as a unit.
type Note struct {
    ID    uint64 // stable identity across edits (selection, undo); never reused
    Start int    // absolute ticks
    Dur   int    // ticks (>0)
    Pitch int    // 0..127
    Vel   int    // 1..127
    Ch    int    // 0..15
}

// Clip is an editable, mutable track: the model the piano roll / drum grid edit.
// One Clip per stem. Notes are kept sorted by (Start, Pitch) for hit-testing.
type Clip struct {
    Name    string
    Channel int
    Program int       // GM program, -1 = percussion/none
    Notes   []Note
    nextID  uint64
}

// Mutators return NEW state semantics at the call site (immutability rule): each
// returns the affected note IDs so the UI/undo log can record a reversible delta.
func (c *Clip) Add(n Note) uint64
func (c *Clip) Move(ids []uint64, dTicks, dPitch int) // drag
func (c *Clip) Resize(ids []uint64, dDur int)         // edge-drag length
func (c *Clip) SetVel(ids []uint64, vel int)          // velocity lane
func (c *Clip) Delete(ids []uint64)
func (c *Clip) Quantize(ids []uint64, grid int, strength float64, swing float64) // §2.4

// Compile renders the Clip into a write-once music.Track (reuses Track.Note),
// so the existing byte-stable SMF writer saves it unchanged.
func (c *Clip) Compile() *Track
```

A `Project` (editing session) is `[]*Clip` + the loaded `music.Project` routing +
tempo/key/PPQ. Generation populates Clips from `Song`; import populates them from
the reader (§2.3). **One model, two sources.**

### 2.2 Edit history (undo/redo, deterministic)

Edits are **reversible deltas** (`type editOp` with `apply`/`invert`), pushed to a
stack — not snapshots of the whole song. KISS: a slice of ops + a cursor. This is
also the **AI command surface**: the inline command DSL (SPEC-BECKY-CANVAS §4) emits
the *same* ops ("quantize melody 1/16 0.8"), so an AI edit and a mouse edit share one
reversible path and one audit log. Edits never touch the audio thread (engine spec).

### 2.3 An SMF **reader** — round-trip import (`internal/music/smfread.go`, NEW)

`smf.go` today is **write-only** (confirmed: `File.Bytes()` encodes; there is no
decode path). To edit imported MIDI we need a parser: `.mid` → `[]*Clip`.

**Decision — hand-roll, matching the existing writer (recommended), with
[`gitlab.com/gomidi/midi/v2/smf`][gomidismf] named as the fallback.** Rationale:

- The writer is ~165 lines, zero deps, and already defines the byte layout
  (MThd/MTrk, VLQ, running-status-free status bytes, the meta set we emit). A
  reader that is its exact inverse is ~150 lines, **keeps `internal/music`
  dependency-free**, and keeps CI cgo/dep-free per CLAUDE.md §3. We control both
  ends, so round-trip (`Bytes()` → parse → `Compile()` → `Bytes()`) is testable to
  byte-identity for becky-compose output.
- `gomidi/midi/v2` is **MIT** and has a clean read API
  (`smf.ReadFile` → `SMF.Tracks` → `[]Event{Delta, Message}`, with
  `msg.GetNoteOn/GetNoteOff/GetMetaTempo/GetMetaMeter`) ([gomidi smf docs][gomidismf]),
  but the v2 module pulls a broader dependency tree (it targets live MIDI I/O /
  ports too), which is more surface than this package needs. Adopt it **only** if
  hand-rolled parsing of real-world third-party `.mid` files (running status,
  SMPTE time, format-0, odd meta) gets fiddly — then wrap it behind the same
  `ReadSMF` signature so callers don't change.

```go
// internal/music/smfread.go (NEW)

// ReadSMF parses Standard MIDI File bytes into editable Clips. It pairs note-on
// with the matching note-off (or note-on vel=0), reconstructs absolute ticks from
// VLQ deltas, reads tempo/timesig/track-name meta, and handles format 0 and 1.
func ReadSMF(b []byte) (*ParsedSong, error)

type ParsedSong struct {
    PPQ   int
    BPM   int        // from set-tempo meta (first), 120 default
    Num,Den int      // time signature, 4/4 default
    Clips []*Clip
}
```

**Reader traps to handle (cited MIDI realities):** running status (a data byte where
a status byte is expected → reuse last status); note-off as note-on velocity 0
([gomidi `GetNoteEnd`][gomidismf] models exactly this); format-0 single-track files
(split by channel into Clips); unmatched note-on at track end (close at track end);
SMPTE/negative `division` (degrade to PPQ-only, warn). Unknown meta/sysex →
preserve as an opaque event on the Clip so a save round-trips them rather than
dropping them (forensic discipline: don't silently lose data).

### 2.4 Quantize, snap, grid (shared by piano roll **and** drum machine)

`Quantize(grid, strength, swing)` is one function both editors call. `grid` is in
ticks (`StepTicks=120` = 1/16 at PPQ 480, already in `compose.go`). `strength`
0..1 moves a note that fraction toward the grid line (Cubase "iterative quantize");
`strength=1` is hard snap. `swing` reuses the existing swing math (`compose.go`
delays odd 16ths). This mirrors Cubase's **Use Quantize / iterative quantize** and
the Drum Editor's per-instrument grid ([Cubase Key Editor][cubasekey], [Cubase Drum
Editor grid modes][cubasedrum]). One quantize implementation, two editors, same
deterministic result.

---

## 3. Piano roll (Key Editor) — immediate-mode `DrawList` canvas

Cubase's Key Editor is the reference: a grid, time on X, pitch on Y as a piano
keyboard, with an Inspector for quantize / transpose / scale-correction / legato /
length ([Cubase Key Editor][cubasekey]). becky's is the same mental model, drawn
each frame with `DrawList` ([ImGui DrawList][imgui]).

### 3.1 Layout & view transform

```
+----+--------------------------------------------------+
| pi |  ruler (bars/beats), playhead                    |
| an |--------------------------------------------------|
| o  |                                                  |
| ke |   note grid  (DrawList: gridlines + note rects)  |
| ys |                                                  |
+----+--------------------------------------------------+
| (velocity lane: one bar per selected/visible note)   |
+------------------------------------------------------+
```

A single **view transform** holds `pxPerTick` (zoom X), `pxPerSemitone` (zoom Y),
`scrollTicks`, `scrollPitch`. Every draw and every hit-test goes through
`tickToX/xToTick/pitchToY/yToPitch`. Pan = drag the ruler / space-drag; zoom =
ctrl+wheel (X), ctrl+shift+wheel (Y), around the cursor. Only notes in the visible
tick/pitch window are drawn (cull) — cheap even on a busy arrangement.

### 3.2 Drawing (per frame, DrawList)

- **Grid:** vertical lines at the current snap grid (bold on bar, medium on beat,
  light on subdivision); horizontal row striping per semitone; the left piano keys
  drawn from `pitchToY` (C rows labelled).
- **Scale highlight (the becky edge):** rows **in key** are tinted; out-of-key rows
  are dimmed. The in-key set comes straight from the model we already have —
  `ScaleIntervals(scale)` + `RootPC` (`theory.go`) — so the producer literally sees
  the genre's scale under their hands. Chord-tone rows for the current bar
  (`romanChord` result) get a second, stronger tint (this is also the LEGO-context
  hook, §6).
- **Notes:** filled rects `x=tickToX(Start)`, `w=Dur*pxPerTick`,
  `y=pitchToY(Pitch)`, `h=pxPerSemitone`; fill alpha or hue = velocity; selected =
  bright outline. Drum/percussion clips can render as diamonds (Cubase's drumstick
  affordance) when `Program==-1` ([Cubase Drum Editor][cubasedrum]).

### 3.3 Interaction (the editing verbs)

| Gesture | Action | Model call |
|---|---|---|
| double-click empty | create note at snapped tick/pitch, default dur=grid | `Clip.Add` |
| drag note body | move (snapped) in time + pitch | `Clip.Move` |
| drag note right edge | resize length | `Clip.Resize` |
| drag in velocity lane | set velocity of selected/under-cursor | `Clip.SetVel` |
| marquee / shift-click | selection set (IDs) | (UI state) |
| Del | delete selection | `Clip.Delete` |
| Q | quantize selection at grid/strength/swing | `Clip.Quantize` |
| ctrl+Z / ctrl+shift+Z | undo / redo | edit history (§2.2) |

Hit-testing is compositor-cheap: notes are sorted; binary-search the visible tick
window, then linear within. Drag uses a "live delta" preview (draw the moved rect)
and commits one reversible op on mouse-up (so a drag is one undo step). All gestures
snap to the §2.4 grid unless a modifier (alt) holds "no snap." Inspector panel mirrors
the Cubase Key Editor Inspector: transpose ±, legato, fixed-length, scale-correct
(snap out-of-key notes into the profile scale) — each is a batch model op.

---

## 4. Drum machine / step sequencer

**The becky-compose drum grids already _are_ a step sequencer.** A `DrumVoice` is
`{note, grid[16], vel, rolls[]}` (genre.go); a profile's `drums.patterns` is a rack
of voices (kick/clap/hat/ohat…). The drum machine is the **direct visual editor of
that structure** — the same 16 cells the generator reads, now clickable.

### 4.1 The grid editor

```
        1   2   3   4   5  ...                         16     swing [ 0.54 ]
kick   [#] [ ] [ ] [ ] [ ] [ ] [#] [ ] [ ] [ ] [#] [ ] [ ] [ ] [ ] [ ]   vel●
clap   [ ] [ ] [ ] [ ] [#] [ ] [ ] [ ] [ ] [ ] [ ] [ ] [#] [ ] [ ] [ ]   vel●
hat    [#] [#] [#] [#] [#] [#] [#] [#] [#] [#] [#] [#] [#] [#] [R] [#]   vel●
ohat   [ ] [ ] [#] [ ] [ ] [ ] [#] [ ] [ ] [ ] [#] [ ] [ ] [ ] [#] [ ]   vel●
```

- **One row per voice**, drawn with `DrawList` (cells = filled/empty rects). Click
  toggles a cell; right-drag paints a velocity (cell shade = velocity, mapped to the
  named ladder `ghost/soft/normal/accent/hard` from genre.go). This is *exactly* the
  `grid[16]` + `vel` fields, edited in place.
- **Accents:** per-cell velocity above `normal` renders brighter (Cubase drum-map
  accents). **Rolls:** a cell flagged a roll shows an `R`; a small popup edits
  `{n, ramp}` → writes a `Roll` into the voice. These are the existing `Roll` type,
  surfaced.
- **Swing knob:** one control bound to the profile `swing` (0.5..0.75); the generator
  already delays odd 16ths by `(swing-0.5)*2*StepTicks` (compose.go) — the knob just
  edits that number and the preview re-times live.
- **Pattern chaining:** bars/sections come from the arrangement (§5). A "patterns"
  strip lets a voice carry **A/B/fill** variants chained per section (verse=A,
  drop=B, last-bar=fill) — a thin extension of the profile (a `patternsByName` map),
  defaulting to today's single grid so existing profiles are unchanged.

### 4.2 Reads/writes the profile **and** the MIDI model (two-way)

The drum machine is the one editor that touches **both** representations:

- **Reads** the profile grids on load (a generated song) **or** derives a grid from
  an **imported** drum clip (quantize note starts of channel-9 notes to the 16-step
  lattice → reconstruct `grid[]`). So an imported beat is editable on the grid too.
- **Writes** edits straight to the in-memory `DrumVoice`/`Clip`. "Apply" recompiles
  the drum `Clip` (each cell → `Track.Note` via the existing `genDrums` tick math:
  `b.start + step*StepTicks + swing + jitter`), so the edited beat saves through the
  same byte-stable writer and stays deterministic.
- **"Save as profile"** (the multiplier): the edited grids/swing/rolls serialize
  back to a `profiles/<id>.json` (genre.go schema) — Jordan's tweak becomes a reusable
  genre, the same research-once-cache-forever loop SPEC-BECKY-COMPOSE §6 describes.

---

## 5. Arrangement + mixer

### 5.1 Arrangement timeline (sections + clip lanes)

The arrangement already exists as data: `profile.arrangement[]` =
`{name,bars,energy,tracks[]}` (intro/verse/build/drop/verse2/drop2/outro in the
crunkcore profile). The arrangement view draws that as a **section ruler** (coloured
blocks by `energy`) over **per-track lanes**, one lane per stem, each lane showing
its clip(s) as blocks on the timeline.

- **Section ops:** drag a section edge (change `bars`), reorder sections (drag),
  duplicate (drop2 = drop), toggle which tracks are active in a section (the
  `tracks[]` set — this is what makes a "drop" fuller than a "verse"). Each is a
  deterministic edit to the arrangement model; **regenerate** then re-renders only
  the affected stems with the section's `energy` (the generator is already
  energy-gated: density, hat thinning, lead/counter `activeFromEnergy`).
- **Clip arrangement:** move/duplicate/split a clip on a lane (classic DAW arrange).
  A clip is a `Clip` + a lane offset; splitting is a model op on note Start.
- **Comping (deferred to v1.5):** lanes-with-takes is a Cubase staple
  ([Cubase comping/lanes][cubasekey]); for a MIDI-first generative tool it is lower
  priority than the grid/roll, so it is **explicitly later** (§7).

### 5.2 Mixer over the `project.json` routing DAG

`project.json` is already a routing graph: tracks → buses (`bus.808/drums/music/fx`)
→ `bus.master` → `out.main`, plus the two declared sidechain edges (kick → music
comp sidechain; kick → 808 sidechain) and a compressor on `bus.music`
(project.go, SPEC-BECKY-COMPOSE §5). The mixer is a **view of that DAG**, not a new
structure:

- **One channel strip per `ProjTrack`** (fader, pan, mute/solo, the bus it routes
  to via `Out`), **one strip per `ProjBus`**, master last. Faders/pans are gain
  params the **engine** reads; this editor only edits the graph + params (the audio
  is SPEC-BECKY-DAW-ENGINE.md).
- **Sends & the one-declaration sidechain (the Cubase-killer):** adding a sidechain
  in Cubase is the canonical "100 clicks" (create bus, set output, add compressor,
  pick sidechain input, route send). In becky it is **one declared edge**
  `{from: "src.drums.kick", to: "<comp>.sidechain", kind:"sidechain"}` — the mixer
  exposes it as a single right-click "duck this off the kick," appends one
  `ProjEdge`, and the engine auto-creates the send/detector/routing (named
  deterministically). The graph stays plain JSON → content-addressed, reproducible,
  and **model-readable by an AI** (SPEC-BECKY-CANVAS §5).
- **Routing edits** add/remove `ProjEdge`s and `ProjBus`es. A topo-sort check (the
  engine's `Render.TopoSort`) rejects cycles at edit time with a plain message.

The mixer therefore edits the *same* `project.json` becky-compose emits and any DAW
can read — so a becky mix is portable and inspectable, unlike a Cubase `.cpr`.

---

## 6. The "LEGO context" UX (what the user specifically asked for)

The user likes ACE-Step's framing: *"LEGO blocks where each stem has context of the
others."* In ACE-Step that is the **lego/complete** task — generate or complete one
stem with the **source audio encoded as conditioning context** for the rest
([ACE-Step advanced tasks][aslego]). becky does the **deterministic, MIDI-domain
analogue**, which is *better* for an editor because the context is exact, not latent:

### 6.1 Context overlay while editing one stem

When you edit a stem, the others are drawn as **dimmed context** in the same canvas:

- Editing **melody/lead** → the current bar's **chord tones** (`romanChord` result,
  already computed per bar in `compose.go`) are highlighted rows + ghost notes, and
  the **bass/808** root is a ghost line. You are never guessing the harmony — the
  LEGO underneath is visible.
- Editing **bass/808** → the **kick** pattern is ghosted on the timeline (so the
  808 locks to the kick — the genre's mono-low discipline), and the chord root per
  bar is shown. This is the same `kickSteps(p)` relationship `genBass` already uses,
  made visual.
- Editing **drums** → the bar's `energy` and section name are shown so fills land
  where the arrangement wants them.

This is pure model overlay (no audio, no AI) — the data is already in `Song`/`bars`.

### 6.2 Deterministic "regenerate one stem, keep the others"

The killer operation: select a stem, "regenerate (seed+1 / new feel), keep
everything else." Because generation is a **pure function** of `(profile, key, bpm,
seed)` with **per-track seeds** already (`perSeed(seed, name)` in compose.go), we
regenerate **one** track's stream while passing the others as **fixed context**:

```go
// internal/music/regen.go (NEW) — the deterministic "lego" op.
// Re-runs ONE track's generator with a new per-track seed, against the EXISTING
// bars/chords/energy (the other stems' harmonic+rhythmic context), leaving every
// other Clip byte-identical. Same (song, track, newSeed) => identical new stem.
func RegenTrack(s *Song, p Profile, track string, newSeed int64) *Clip
```

Because the chord/energy timeline (`bars`) and the other tracks are held constant,
the new melody still sits on the same chords, the new fill still respects the drop's
energy — "context of the others," deterministically, with a seed you can reproduce.
This is the LEGO promise with becky's offline+deterministic invariant intact: no
diffusion re-roll of the whole song, no network, same inputs → same stem. An edited
stem can be **locked** (excluded from regenerate) so hand-tweaks survive a re-roll
of its neighbours — the literal "keep these blocks, swap that one."

---

## 7. Cubase feature-parity gap analysis

The producer's muscle memory is Cubase. This maps each core Cubase pillar to the
becky-canvas plan, marks **v1 / later**, and calls out where becky **wins**. Sources:
[Cubase Key Editor][cubasekey], [Cubase Drum Editor][cubasedrum], [Cubase 15
features][cubase15], [SOS Cubase editors intro][cubasesos].

| Cubase feature | becky-canvas plan | Scope | Where becky wins / loses |
|---|---|---|---|
| **Key Editor (piano roll)** | §3 DrawList piano roll: zoom/pan, draw/move/resize, velocity lane, marquee | **v1** | **Win:** scale + **chord-tone highlight from the genre profile** (Cubase has scale assist, but becky's comes free from the same theory the song was generated with). |
| **Drum Editor / step grid** | §4 16-step grid = the becky-compose drum grids, clickable; accents, rolls, swing | **v1** | **Win:** the grid **is** the generative source; edits feed back into a reusable profile. Cubase's drum map is display-only. |
| **Quantize (incl. iterative / swing)** | §2.4 one `Quantize(grid,strength,swing)` for both editors | **v1** | Parity; becky's swing is the same number the generator uses. |
| **Scale correction / chord track** | scale-correct as a batch op; chords come from the Roman-numeral progression in the model | **v1** | **Win:** becky *starts* from a validated progression; the "chord track" is the source of truth, not an afterthought. |
| **MixConsole (faders/pan/mute/solo/sends)** | §5.2 mixer as a view of `project.json` | **v1 (params), engine renders** | Parity on surface; **win** on portability (plain JSON vs `.cpr`). |
| **Routing / buses / group channels** | §5.2 edit `ProjBus`/`ProjEdge`; topo-sort guard | **v1** | **Win, decisively:** one-declaration sidechain & no-click bus routing vs Cubase's many-click flow. |
| **Sidechain compression** | §5.2 one declared `sidechain` edge | **v1 (declare), engine applies** | **Win:** the headline differentiator (SPEC-BECKY-CANVAS §5). |
| **Automation (volume/pan/param lanes)** | automation lane = a time→value envelope on a param; drawn like the velocity lane | **v1 (basic volume/pan/macro); full param automation later** | becky-deterministic envelopes; AI can *write* automation via the DSL (Cubase can't). |
| **VST/VSTi / CLAP plugin hosting** | **out of scope here** — SPEC-BECKY-DAW-ENGINE.md (CLAP first, VST3 deferred) | **later** | Deliberate: built-in FX for MVP; this is the engine's job. |
| **Comping / take lanes** | §5.1 noted; lanes-with-takes | **later (v1.5)** | Lower value for a MIDI-first generative tool; explicitly deferred. |
| **Audio recording / warp / VariAudio** | **out of scope** (becky is MIDI-first; audio is forensic ingest elsewhere) | **out** | Not the product. becky edits MIDI stems; pitch-correct-audio is a different tool. |
| **Score editor / notation** | not planned | **out** | Out of scope; the piano roll is the editor. |
| **MIDI import/export** | §2.3 reader + existing writer = full round-trip | **v1** | **Win:** the project is the open `project.json` + per-stem `.mid`, not a locked session. |
| **Genre-aware generate** | becky-compose (BUILT) | **already shipped** | **No Cubase equivalent.** This is the whole premise. |

**Net:** v1 is a credible **Key Editor + Drum Editor + routing mixer + MIDI
round-trip** — the daily-driver core a producer lives in — with becky winning on
routing, scale/chord awareness, AI-editability, and project portability. The honest
gaps (full automation, comping, plugin hosting, audio warp) are scoped **later** or
**to the engine spec**, never hand-waved.

---

## 8. Phased build plan (tied to SPEC-BECKY-CANVAS phases)

Sequenced **model-first** so each UI layer edits a model that already round-trips,
and aligned to the canvas phases (canvas Phase 1 = MVP w/ drum mode; Phase 2 = piano
roll + mixer).

- **Phase E0 — editable model + reader (no UI; pure Go, CI-green):**
  `edit.go` (`Note`/`Clip`/mutators/`Compile`), `smfread.go` (`ReadSMF`), edit
  history (§2.2), `Quantize`. Round-trip test to **byte-identity** for compose
  output; import test on a third-party `.mid` fixture. *No cgo, no models* — runs in
  the same green CI as `becky-compose`. **This unblocks everything and is fully
  buildable cloud-side.**
- **Phase E1 — drum machine (canvas MVP mode #2):** the 16-step grid editor over
  Clips/profile (§4), swing knob, accents/rolls, "save as profile." Lightest UI,
  reuses the canvas drum mode from SPEC-BECKY-CANVAS §6, proves the
  model↔grid↔profile loop.
- **Phase E2 — piano roll + mixer (canvas Phase 2):** the §3 DrawList key editor
  (zoom/pan/draw/move/resize/velocity/scale-highlight) and the §5.2 mixer view over
  `project.json`. Piano roll is the biggest UI surface; mixer is mostly a view of an
  existing graph.
- **Phase E3 — arrangement + LEGO context:** the §5.1 section/lane timeline, the §6
  context overlays, and `RegenTrack` (deterministic "regenerate one stem"). Depends
  on E0–E2 being solid.
- **Phase E4 — polish/parity fill:** automation lanes, pattern chaining (A/B/fill),
  comping (v1.5), Inspector parity (legato/fixed-length/transpose batches).

**Build split (cloud ↔ local), per CLAUDE.md §4:**

| Cloud / web agent | Local agent (Jordan's PC) |
|---|---|
| `edit.go`, `smfread.go`, quantize, edit-history, `RegenTrack` — all pure Go | Wire the model into the live ImGui `DrawList` canvas (cgo, on-machine) |
| Unit tests: round-trip byte-identity, import fixture, quantize vectors, regen determinism | Hit-testing/zoom/pan feel; real `.mid` imports from Cubase exports |
| Reader trap coverage (running status, fmt-0, vel-0 note-off) on fixtures | Drum-grid/profile save loop tested against real beats; mixer over a real engine |

The entire **model + reader + editing logic is cloud-buildable and unit-testable
with no GPU/audio** — the UI/cgo wiring is the only local-only part, exactly the
becky model boundary.

---

## 9. Top risks

- **Determinism regression (HIGH):** the edit layer must not perturb the byte-stable
  writer. *Mitigation:* edits live in `Clip`; `Compile()` only ever calls the
  existing `Track.Note`; CI keeps the existing determinism test **plus** a new
  round-trip test as a tripwire.
- **Reader vs real-world `.mid` (MEDIUM):** third-party files use running status,
  format 0, SMPTE division, weird meta. *Mitigation:* fixtures for each; degrade
  (PPQ-only, preserve opaque meta) never crash; `gomidi/midi/v2` (MIT) is the
  drop-in fallback behind `ReadSMF` if hand-rolling gets fiddly ([gomidi smf][gomidismf]).
- **Immediate-mode perf on dense arrangements (MEDIUM):** thousands of notes ×
  60fps. *Mitigation:* cull to the visible window; sorted notes + binary search;
  this is what `DrawList` is for ([ImGui][imgui]) — re-verify on the Phase-0 giu
  spike (SPEC-BECKY-CANVAS §8).
- **Model/UI coupling drift (MEDIUM):** two sources (generate, import) into one
  model. *Mitigation:* `Clip` is the single editable type; both paths converge on
  it; the AI DSL and the mouse share `Clip` mutators + edit history.
- **Scope creep toward "a full Cubase" (MEDIUM):** §7 explicitly scopes audio
  warp / notation / comping **out or later**; the single-tool principle (CLAUDE.md
  §1) keeps the editor an editor.

---

## 10. Open decisions for Jordan

1. **SMF reader:** confirm **hand-rolled reader** matching becky's writer (no new
   deps, byte-round-trip-testable), with `gitlab.com/gomidi/midi/v2` (MIT) as the
   fallback only if real-world `.mid` parsing gets hairy? (Recommended: hand-roll.)
2. **First editor mode:** drum machine first (lightest, reuses the canvas MVP drum
   mode, proves model↔profile loop) — confirm, vs piano roll first (closer to the
   "key editor" muscle memory but a bigger UI lift)?
3. **"Save as profile":** should an edited drum grid / swing be writable back to a
   `profiles/<id>.json` by default (turns every tweak into a reusable genre), or only
   on explicit "save as new genre"? (Recommended: explicit, to keep the shipped DB
   curated.)
4. **Regenerate-one-stem feel:** does "regenerate" bump the per-track seed (same
   profile, new variation) only, or also expose a couple of knobs (density,
   contour)? v1 = seed-only (simplest, fully deterministic); knobs in E4.
5. **Velocity/automation lane scope for v1:** velocity lane + basic volume/pan
   automation only, deferring full per-param automation to E4 — acceptable?
6. **Import target:** is the priority importing **Cubase MIDI exports** specifically
   (so test against real `.cpr`→`.mid` exports), or general `.mid` from anywhere?
   (Affects which traps to harden first.)

---

> **Citations.** Editable-MIDI / SMF reader: [gomidi/midi v2 `smf` (MIT, read API)][gomidismf].
> Immediate-mode rendering: [Dear ImGui DrawList / immediate mode][imgui],
> [ocornut/imgui][imguigh]. Cubase parity: [Key Editor][cubasekey],
> [Drum Editor][cubasedrum], [Cubase 15 features][cubase15], [SOS editors intro][cubasesos].
> LEGO/stem-context: [ACE-Step advanced tasks (lego/extract/complete)][aslego].
> The in-repo facts (write-only `smf.go`, `Track`/`Event`/`Clip`-able model,
> `project.json` routing, per-track seeds, scale/chord theory) are from
> `becky-go/internal/music/` as of 2026-06-15.

[gomidismf]: https://pkg.go.dev/gitlab.com/gomidi/midi/v2/smf
[imgui]: https://www.blog.brightcoding.dev/2026/05/22/stop-building-boring-midi-tools-this-c-imgui-keyboard-will-blow-your-mind
[imguigh]: https://github.com/ocornut/imgui
[cubasekey]: https://www.steinberg.help/r/cubase-pro/15.0/en/cubase_nuendo/topics/midi_editors/midi_editors_key_editor_r.html
[cubasedrum]: https://www.soundonsound.com/techniques/cubase-midi-drum-editor
[cubase15]: https://www.steinberg.net/cubase/new-features/
[cubasesos]: https://www.soundonsound.com/techniques/introduction-cubase-part-2-recording-key-drum-list-editors
[aslego]: https://deepwiki.com/ace-step/ACE-Step-1.5/5.4-advanced-tasks-(lego-extract-complete)
