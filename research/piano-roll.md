# Piano Roll — Exhaustive, Source-Cited Capability Research

**Purpose:** define the build target for a piano-roll editor living *inside* a
Maschine-class drum machine (becky-canvas / the becky-go DAW suite). Every claim
below is cited to a primary doc or repo. Where a feature does **not** exist in a
reference product, that is stated explicitly (and cited) so we don't build a myth.

**Date:** 2026-06-19. Reference products: Native Instruments **Maschine 2**
(the spiritual parent — a drum machine *with* a piano roll), **Ableton Live 12**,
**FL Studio**, **Cubase 15 / Key Editor**, plus OSS implementations (Signal, LMMS,
Helio, webaudio-pianoroll).

---

## 0. The one decision that frames everything: Maschine's two-view model

Maschine does **not** call its melodic editor a "piano roll" as a top-level mode.
It has ONE **Pattern Editor** (the "Event area") with two *views* toggled by buttons:

- **Group view** — all 16 Sounds shown as horizontal rows; pitch is irrelevant;
  this is the **drum-mode** layout (one lane per pad/sound).
- **Keyboard view** — chromatic pitches for a *single focused* Sound, with vertical
  piano keys on the left (octaves labelled on each C); this is the **melodic-mode**
  layout, i.e. the **piano roll**.

Both views share the *same* editing tools, grids, velocity model, control lane,
copy/paste, and quantize. The only difference is what the vertical axis means
(sound-slot vs. pitch) and whether vertical drag re-assigns a sound or transposes a
pitch. This is the single most important architectural takeaway: **drum lanes and a
chromatic piano roll are the same editor with a swapped Y-axis mapping.**
Source: NI Maschine Software Manual, "Working with Patterns and Clips."
<https://native-instruments.com/ni-tech-manuals/maschine-software-manual/en/working-with-patterns-and-clips>

In Keyboard/Piano-roll view: clicking a key triggers the focused Sound at that
pitch; for NKS instruments the original instrument's key colours are shown; if a
scale is selected on the controller, the in-scale keys are coloured and out-of-scale
keys are uncoloured; hovering shows note names; keyswitches show their labels. (ibid.)

---

## 1. Note entry & editing — the five mouse modes (Maschine)

Maschine's Pattern Editor offers **five mouse/edit modes** (right-click context menu
or shortcut). becky should treat these as the canonical interaction set:
(source: NI Software Manual, "Working with Patterns and Clips")

| Mode | Shortcut | What it does |
|------|----------|--------------|
| **Select** (arrow, default) | — | create (double-click bg), delete (double-click note), single/multi select, move, resize, duplicate (Alt+drag) |
| **Draw** (pencil) | `E` | create on click, create a *series* by click-drag, delete on click, delete series by drag |
| **Split** (scissors) | `X` | white line previews split position (snaps to grid; Ctrl/Cmd = free); click splits one or all selected notes |
| **Join** (plus) | `Y` | join a note with the next note on the same row; fills the gap |
| **Mute** (cross) | `Z` | click toggles per-note mute (greyed out, won't trigger) |

Select-mode actions in full (ibid.):
- **Create:** double-click background. **Delete:** double-click note / right-click → Delete.
- **Select:** click (single); `Shift`+click (add/remove); drag a **selection frame**
  in the background (marquee); click background to deselect; `Ctrl/Cmd+A` selects all.
- **Move horizontally (time):** drag — snaps to **Step Grid**; `Ctrl/Cmd`+drag = free.
- **Move vertically:** Group view → moves to another Sound; Keyboard view → transposes.
- **Duplicate:** `Alt`+drag.
- **Resize (length):** drag left/right border (snaps to Step Grid; `Ctrl/Cmd` = free).
- **Multi-edit rule:** the clicked note quantizes normally; all other selected notes
  move/resize by the *same amount*, preserving relative offsets and lengths
  (minimum one step). This relative-preservation rule is worth copying exactly.

**Other DAWs converge on the same primitives:**
- Ableton Live 12: Draw Mode (`B`) click-drag to add; clicking an existing note in
  Draw Mode deletes it; drag edges to resize; click-drag = marquee; `Shift`+click
  adds/removes; `Shift`+click a piano key selects the whole key-track; Note Stretch
  markers scale a selection proportionally in time.
  <https://www.ableton.com/en/live-manual/12/editing-midi/>
- FL Studio tools: **Draw** (`P`), **Paint** (`B`, paints lines of notes), **Paint
  drum-sequencer** (`N`), **Delete** (`D`), **Mute** (`T`), **Select** (`E`),
  **Slice** (`C`, drag vertically to cut), **Zoom-to-selection** (`Z`), **Play
  selected** (`Y`), and a **Playback/Scrub** tool.
  <https://www.image-line.com/fl-studio-learning/fl-studio-online-manual/html/pianoroll.htm>
- Cubase Draw tool: click = note of Length-Quantize length; click-drag = longer note;
  **while inserting** drag up/down = velocity, `Alt`+drag up/down = pitch, drag
  left/right = length, `Shift`+drag = time position. Hold `Alt` to temporarily switch
  from Object Selection to Draw.
  <https://www.thedigitalaudiomanual.com/blog/key-editor-operations-v2-in-cubase-12>
- LMMS: **Pencil** to place, **Select**, **Knife** to split at click, **Glue**
  (`Shift+G`) merges adjacent same-pitch notes.
  <https://deepwiki.com/LMMS/lmms/4.1-piano-roll-and-automation-editor>

---

## 2. Velocity editing (per-note + lanes)

- **Maschine:** velocity is shown *in the note itself* as **transparency** ("the
  softer the hit, the more transparent the event"); there's no dedicated velocity
  lane in the software piano roll — velocity is edited via the controller's
  **Step/Quick Edit** (works for both Step and Piano-Roll mode) or by re-recording.
  Maschine's separate **Control Lane** is for **modulation/MIDI-CC**, not velocity.
  (NI Software Manual, "Working with Patterns and Clips.")
- **Ableton:** a dedicated **Velocity Editor** lane under the notes (saturation also
  shows velocity on the note); numeric value in the lane header; **Velocity
  Deviation** sets a per-note random range; Draw-mode can draw velocity ramps
  (`Alt`/`Cmd` for straight lines/crescendos); a separate **Release Velocity Editor**
  lane exists. <https://www.ableton.com/en/live-manual/12/editing-midi/>
- **FL Studio:** an **Event Editor** panel below the grid renders each note's value
  as a vertical line with a circle on top; a **Target control** selector switches the
  lane between velocity, pan, pitch, cutoff (MODX), resonance (MODY), etc.
- **Cubase:** a **Velocity lane** at the base of the window; controller lanes for CCs.

**Design rule for becky:** support BOTH affordances — note-intrinsic velocity (color/
opacity/height on the note) *and* a toggleable bottom **velocity lane** with drag +
ramp drawing. Add a velocity-*range* (deviation) field per note for the
corroborate-then-vary aesthetic later.

---

## 3. Length, quantize, swing/groove

- **Length/duration:** edited by dragging a note's left/right border in every product
  (Maschine, Ableton, FL, Cubase). Cubase additionally has a **Length Quantize**
  pop-up that sets the default drawn length and constrains lengths to a multiple of
  that value; a **Scale Length / Scale Legato** slider stretches selected notes.
  (Cubase docs, above.)
- **Quantize (Maschine):** right-click → **Quantize** (full: snap to nearest Step Grid
  step) or **Quantize 50%** (move halfway — keeps human feel). Quantization
  auto-removes accidental double-notes. **Input Quantize** modes: None / Record /
  Play+Rec (Preferences → General → Input). (NI Software Manual.)
- **Quantize (Ableton):** `Ctrl/Cmd+U` applies; `Ctrl/Cmd+Shift+U` opens settings;
  the **Quantize MIDI Tool** has an **Amount** for partial quantize.
- **Swing/Groove:** Maschine applies swing at the Group/Pattern level (Swing knob),
  not per-note in the piano roll; FL Studio and becky's own `internal/dawmodel`
  already carry swing math (see existing becky `becky-drum` swing reuse). For becky,
  expose **quantize strength (0–100%)** and **swing %** at minimum.

---

## 4. Snap / grid resolutions

- **Maschine has THREE grids** (copy this three-grid model — it's clean):
  1. **Step Grid** — resolution for creating/moving/quantizing events; togglable; menu
     ranges **1 Bar → 1/128 incl. triplets**, default **1/16**; drawn as grey lines
     (black = bar, grey = beat); lines auto-hide at very fine densities.
  2. **Arrange Grid** — divisions for Pattern/Section *length* (1 Bar, 1/2, 1/4, 1/8,
     1/16, Off, or **Quick** = preset bar counts 1/2/4/8/12/16…).
  3. **Nudge Grid** — invisible secondary grid for nudging events; default **½ step**;
     options Step, Step/2, Step/4, Step/8, Step/16. (NI Software Manual.)
- **Nudge:** `Alt`+←/→ shifts the selection (or whole pattern if nothing selected) by
  one Nudge-Grid division; nudged notes that pass the end wrap to the start. (ibid.)
- **Snap override:** every product lets you hold `Ctrl/Cmd` (or `Alt` in Ableton) to
  bypass snap for free placement. FL Studio's **Global Snap** offers a unified value
  (Line/Step/custom). Ableton can disable the grid entirely.

---

## 5. Scales / key-lock + chord tools + arpeggiator

This is where the references diverge sharply, and it matters for scope.

**Maschine (controller-side engine, not the software piano roll):**
- A **Scale and Chord engine** (added in Maschine 2.2): pick a **scale + root note**
  and the 16 pads stay **in key** ("always in key") — this is key-lock. The selected
  scale's in-key notes get **coloured keys in the piano roll**; out-of-scale keys are
  uncoloured. (NI Software Manual; Synthtopia/CDM coverage of 2.2.)
  <https://www.synthtopia.com/content/2014/11/25/maschine-2-2-update-brings-new-melodic-playing-options/>
- **Chord mode** — one pad plays a whole chord, built from the configured scale/chord
  (Harmonizer / Chord-set style). (maschinetutorials, NI manual.)
- **Arp / Note Repeat** — holding **NOTE REPEAT** with pads in Keyboard/Chord mode
  engages a flexible **Arp engine**; arpeggios follow the held pads + the scale/chord;
  it reads **Polyphonic Aftertouch** per pad for varying velocities.
  <https://www.native-instruments.com/ni-tech-manuals/maschine-mk3-manual/en/playing-on-the-controller>
- **Explicit limits (do NOT replicate as myths):** Maschine has **no Ratchet
  function**; **scale parameters cannot be modulated or automated**; the arp is
  play-time only (no editable arp-step page). Sources: NI community + manual.
  <https://community.native-instruments.com/discussion/5532/maschine-plus-note-repeat>

**FL Studio (best-in-class in-roll harmony tooling — borrow these ideas):**
- **Stamp / Chord tool** (`Alt+C`) — pick a chord type (major/minor/dim/aug/7th/…)
  and stamp the correct notes; an **Automatic chord** mode builds progressions from
  the **Snap-to-scale** setting.
- **Snap to scale** — notes snap to the chosen scale when moved/clicked in.
- **Scale highlighting** + **Scale levels** tool; plus **Arpeggiate**, **Strum**,
  **Flam**, **Riff machine**, **Generate chord progression**, **Quick legato**,
  **Articulate**, **Quick chop**, **Glue**, **Claw machine**, **Limit**, **Flip**,
  **Randomize**, **LFO** in the Tools menu.
  <https://www.image-line.com/fl-studio-learning/fl-studio-online-manual/html/pianoroll.htm>

**Ableton 12:** Scale Mode with **Scale Highlighting**, **Fold to Scale**, **Fit to
Scale**, plus MIDI Tools (next section). Cubase has a **Scale Assistant** for pitch
quantization to a scale. (Ableton & Cubase docs above.)

---

## 6. Step input, ghost/reference notes, note properties (micro-timing / probability / ratchet)

- **Step input:**
  - Maschine: **Step mode** (the rectangle/step view) and **Step/Quick Edit** for
    velocity exist on the controller; both Step and Piano-Roll mode honor step edits.
  - Cubase: **Step Input** places played notes according to current quantize settings.
- **Ghost / reference notes (a Maschine-style must):**
  - FL Studio **Ghost Notes** show note data from *other* channels/piano-rolls — two
    kinds: **within-pattern** (solid blocks) and **out-of-pattern** (open blocks);
    double-right-click switches the active channel. This is exactly the "see the other
    drum/melody lane while editing this one" feature. (FL manual.)
  - Maschine's analogue: in Keyboard view you focus one Sound but the Group context
    is one button away; LMMS shows other-track ghosting too.
- **Note properties / micro-timing / probability / ratchet:**
  - FL Studio: per-note **pitch, velocity, release velocity, pan, MODX (cutoff),
    MODY (resonance), Slide flag, Portamento flag**; double-click a note for a full
    properties dialog. **Slide** notes draw a small triangle (glissando);
    **Portamento** slides pitch from prior note. (FL — native instruments only.)
  - Ableton: per-note **Chance/Probability** (0–100%, the **Chance Editor** lane),
    **Velocity Deviation** (random range), **Probability Groups** ("Play All" /
    "Play One"), **Humanize** (±¼ grid random timing). (Ableton docs.)
  - **Ratchet/note-repeat-per-step:** NOT in Maschine's roll (explicitly absent, see
    §5). It exists conceptually in step sequencers (e.g. Stepic) and in Ableton 12 via
    the **Ornament/Arpeggiate** tools, not as a per-note "ratchet" field. Treat ratchet
    as a *later* nicety, modelled as a per-note "repeat count + rate."
  - **Micro-timing:** Maschine = **Nudge Grid** (sub-step shift). Others = free-move
    with snap off. becky should store an explicit per-note **time offset in ticks**.

---

## 7. MIDI import / export

- Maschine: drag-and-drop **MIDI Dragger** out (export) and "Importing MIDI to
  Patterns" in (import); also an **Audio Dragger** for rendered audio. (NI manual.)
- All DAWs import/export Standard MIDI Files (SMF). **becky already has a pure-Go SMF
  reader/writer in `internal/music` and `becky-compose` emits per-track `.mid`** — the
  piano roll should read/write through that existing path (no new MIDI code).

---

## 8. Scrolling, zoom, keyboard-follow

- Maschine: a **zooming scroll bar** on the bottom (drag horizontally = scroll, drag
  vertically = zoom centered on cursor; handles zoom while pinning one edge;
  double-click = fit whole pattern) and an identical **vertical** zoom bar on the right
  in Keyboard view. A **Follow** button auto-scrolls to the playhead and auto-disables
  when you manually scroll. Clicking the timeline moves the playhead (snapping rules
  differ play-on vs play-off). (NI manual.)
- Ableton/FL: **Fold** (hide empty key-tracks) and **Fold to Scale** are essential for
  keeping a tall chromatic roll readable — high priority for becky. (Ableton docs.)

---

## 9. Copy / paste / duplicate, legato/glue, humanize, multi-clip

- **Copy/paste (Maschine):** `Ctrl/Cmd+X|C|V`; first pasted event snaps to Step Grid,
  the rest keep their relative offset; pasting past the end extends the pattern;
  Group→Keyboard paste only carries the focused Sound's events. (NI manual.)
- **Duplicate:** `Alt`+drag (Maschine/Ableton); FL has duplicate + the Paint tool.
- **Legato:** Ableton **Legato** (extend/shorten each note to the next note's start);
  Cubase **Scale Legato** slider; FL **Quick legato**.
- **Glue/Join:** Maschine **Join** mode; LMMS **Glue** (`Shift+G`); FL **Glue**
  (`Ctrl+G`).
- **Humanize:** Ableton **Humanize** (random ±¼-grid); FL **Randomize**. becky should
  make humanize **seeded/deterministic** (it already does this in `becky-drum`).
- **Multi-clip editing:** Ableton edits up to 8 clips at once with a Focus mode — a
  later nicety, not v1.

---

## 10. OSS implementations to learn from (with licenses)

| Project | Lang / stack | License | What to borrow |
|---|---|---|---|
| **g200kg/webaudio-pianoroll** | JS / Canvas | **Apache-2.0** | Cleanest minimal model: note = `{t, n, g}` (onset-ticks, note-number, length-ticks); 4 edit modes **gridmono/gridpoly/dragmono/dragpoly**; decoupled **timebase / grid / snap** (1 bar = timebase ticks); optional wheel-zoom. Borrow the data model + grid/snap decoupling. <https://github.com/g200kg/webaudio-pianoroll> |
| **ryohey/signal** | TypeScript / React+Canvas | **MIT** | Full browser DAW piano roll: velocity/pitch-bend/CC graph lanes, tempo + time-sig changes, SMF in/out, WAV export. MIT = safe to study/port idioms. <https://github.com/ryohey/signal> |
| **LMMS PianoRoll** | C++ / Qt | **GPL-2.0-or-later** | Mature tool semantics: Pencil/Select/**Knife**(split)/**Glue**/pitch-bend tools; right-click context menu to mark notes/chords/scales. GPL — read for behavior, don't copy code into a non-GPL tool. <https://github.com/LMMS/lmms/blob/master/src/gui/editors/PianoRoll.cpp> |
| **Helio (helio-fm)** | C++ / JUCE | **GPL-3.0** | Hand-drawn automation/volume curves with a pen tool; piano-roll *and* pattern-roll share the volume/automation editor — mirrors our drum-lane/piano-roll unification. GPL — study only. <https://github.com/helio-fm/helio-workstation> |
| **mjhasbach/pixi-piano-roll** | JS / PixiJS (WebGL/Canvas) | **MIT** | Animated rendering reference for a GPU/canvas roll. <https://github.com/mjhasbach/pixi-piano-roll> |
| **Sjhunt93/Piano-Roll-Editor** | C++ / JUCE | (check repo) | Small, readable standalone piano-roll widget. <https://github.com/Sjhunt93/Piano-Roll-Editor> |

**Recommendation:** model the data structure after **webaudio-pianoroll** (`{t,n,g}` +
velocity + flags) and study **Signal (MIT)** for lane/graph-editor idioms; treat
LMMS/Helio (GPL) as behavior references only, never code donors, since becky is not GPL.

---

## 11. (1) MUST-have capability checklist — v1-minimal vs later

### v1-minimal (the editor is unusable without these)
- [ ] **Unified editor, swappable Y-axis**: drum-lane mode (one row per pad/sound) and
      chromatic piano-roll mode share one canvas/code path (Maschine model).
- [ ] **Note CRUD**: draw (click / click-drag series), delete, move (time + pitch),
      resize via edge-drag.
- [ ] **Selection**: single click, `Shift`+click add/remove, marquee drag, select-all,
      click-bg deselect.
- [ ] **Multi-note relative edit**: moving/resizing a selection preserves relative
      offsets and lengths (Maschine rule).
- [ ] **Snap to Step Grid** with resolutions 1/1…1/128 + triplets (default 1/16),
      and a **modifier to bypass snap** for free placement.
- [ ] **Per-note velocity**: visible on the note (opacity/height) AND a toggleable
      bottom **velocity lane** with drag editing.
- [ ] **Quantize**: full + partial (strength %), plus **swing %**.
- [ ] **Copy / cut / paste / duplicate** (`Alt`+drag) with relative-offset paste.
- [ ] **Per-note mute** and **delete** (Del/Backspace).
- [ ] **Scroll + zoom** (H and V), **fit-to-pattern**, and **Follow playhead**.
- [ ] **MIDI in/out** via the existing `internal/music` SMF path.
- [ ] **Scale key-lock + scale highlighting** (color in-scale keys; optional snap-to-scale)
      — Maschine's signature melodic affordance; cheap and high-value.

### Later (high-value, not blocking)
- [ ] **Split / Join (glue)** tools.
- [ ] **Legato / Scale-Length**, **Humanize** (seeded/deterministic).
- [ ] **Chord stamp tool** + **Arpeggiate / Strum / Flam** (FL-style generators) —
      becky already has `becky-drum`/`becky-compose` math to reuse.
- [ ] **Per-note probability/chance** + **velocity deviation (range)** (Ableton model).
- [ ] **Ghost/reference notes** from other lanes/patterns (FL model — solid vs open).
- [ ] **Control/automation lane** for MIDI-CC / modulation (Maschine Control Lane).
- [ ] **Nudge grid** (sub-step micro-timing shift) + per-note time-offset storage.
- [ ] **Note properties** beyond velocity: pan, slide/portamento flags, release velocity.
- [ ] **Fold** (hide empty rows) / **Fold-to-scale**.
- [ ] **Step-input** mode (enter notes at the snap value from a controller/keys).
- [ ] **Ratchet / per-step repeat** (count + rate) — explicitly absent in Maschine;
      model as a per-note field if/when wanted.
- [ ] **Multi-clip editing**, release-velocity lane, MPE — far-future.

---

## 12. (2) Data-model + interaction notes for an immediate-mode / canvas GUI

becky-canvas uses **Gio** (immediate-mode, Direct3D11) and a `dawmodel.DrumGrid` /
`[]Note` model already. The piano roll should extend that, not fork it.

### Data model (extend the existing `internal/dawmodel` / `internal/music` types)
```
Note {
    StartTick   int64      // onset, in ticks (1 bar = PPQ*beatsPerBar)
    LengthTick  int64      // duration in ticks (>=1)  [webaudio-pianoroll: g]
    Pitch       uint8      // 0..127 MIDI note number  [n]  (or SoundSlot in drum mode)
    Velocity    uint8      // 1..127
    // later/optional:
    VelRangeLo, VelRangeHi uint8   // velocity deviation (Ableton)
    Chance      float32    // 0..1 probability (Ableton)
    TimeOffset  int64      // sub-grid micro-timing (Maschine nudge)
    Muted       bool
    Flags       NoteFlags  // slide/portamento/etc (FL) — bitset
    Selected    bool       // transient UI state (keep OUT of the persisted model)
}
Lane / View {
    Mode        {Drum, Chromatic}   // swaps Y-axis meaning
    StepGrid    Fraction            // 1/16 default; 1/1..1/128 + triplets
    NudgeGrid   Fraction            // default StepGrid/2
    SnapOn      bool
    Scale       {Root uint8, Mask [12]bool}  // for key-lock + highlighting
    PPQ         int                 // ticks per quarter (e.g. 480 or 960)
}
```
Keep `Selected`/hover/drag-preview as **transient** state separate from the saved
notes — immediate-mode redraws every frame, so derive visuals from a per-frame
interaction struct, not by mutating the model until commit.

### Coordinate mapping (the core of a canvas roll)
- `x(tick)   = (tick - scrollTick)   * pxPerTick`
- `y(pitch)  = (topPitch - pitch)    * rowHeightPx`   (pitch descends downward)
- Inverse on click: `tick = snap(scrollTick + mouseX/pxPerTick)`,
  `pitch = topPitch - floor(mouseY/rowHeightPx)`.
- `snap(t)` = round to nearest `StepGrid` ticks unless the bypass modifier is held;
  keep **timebase / grid / snap decoupled** (webaudio-pianoroll's clean idea) so zoom
  doesn't change musical resolution.

### Interaction state machine (immediate-mode friendly)
Maintain one `interaction` enum updated each frame from pointer events:
`Idle → (press on empty) Drawing | (press on note body) Moving | (press on note edge,
~6px hit zone) Resizing | (press+drag on empty) Marquee`. On release, **commit** the
preview to the model and push one undo entry. Modifiers checked per-frame: `Ctrl/Cmd`
= bypass snap / free-move; `Alt` = duplicate-drag; `Shift` = add/remove to selection.
Tool mode (Select/Draw/Split/Join/Mute) gates which transition fires — mirror
Maschine's five modes with the same shortcut letters (`E/X/Y/Z`).

### Rendering notes (per frame)
- Note rectangle: fill color = lane/sound color; **opacity or inner-bar height ∝
  velocity** (Maschine transparency / Ableton saturation).
- Edge handles: draw a subtle 4–6px grip at L/R for resize affordance.
- Muted = desaturated/outlined; selected = bright border; out-of-scale (key-lock) =
  dimmed row background.
- Grid: black lines on bars, grey on beats, faint on sub-steps; **hide sub-step lines
  when they'd be <~4px apart** (Maschine's anti-clutter rule).
- **Fold** = filter visible rows to those with notes (or in-scale) to tame the 128-row
  chromatic height.

### Velocity lane
A separate bottom strip sharing the X transform: each note draws a vertical bar at its
`StartTick`; drag the bar top to set velocity; `Alt`/`Cmd`+drag across bars draws a
ramp/line (Ableton). Use the same hit-test → preview → commit loop.

### Learning loop (becky-specific, already in the codebase)
Every manual fix (move/quantize/velocity edit) should emit a correction via
`habits.AppendCorrectionLog` (as `becky-drum`/canvas already do) so becky learns
Jordan's by-eye preferences — the "visual-first + learn from corrections" invariant.

### Determinism
All generators (humanize, strum, arpeggiate, chance-resolution at export) must take a
**fixed seed** (`--seed`, default 42) so the same project renders identically — the
offline/deterministic invariant. Probability is stored per-note; resolve it only at
play/export time from the seed.

---

## 13. Sources
- NI Maschine Software Manual — Working with Patterns and Clips: <https://native-instruments.com/ni-tech-manuals/maschine-software-manual/en/working-with-patterns-and-clips>
- NI Maschine MK3 Manual — Playing on the controller: <https://www.native-instruments.com/ni-tech-manuals/maschine-mk3-manual/en/playing-on-the-controller>
- NI community — Maschine has no ratchet / note-repeat: <https://community.native-instruments.com/discussion/5532/maschine-plus-note-repeat>
- Synthtopia — Maschine 2.2 melodic/scale/chord/arp: <https://www.synthtopia.com/content/2014/11/25/maschine-2-2-update-brings-new-melodic-playing-options/>
- CDM — Maschine 2.2 scales/chords/arp: <https://cdm.link/2014/11/maschine-2-2-proves-pad-playability-matters/>
- maschinetutorials — Arp/Chord/Scale mode: <https://maschinetutorials.com/maschine-2-2-using-the-new-arp-mode-feature/>
- Ableton Live 12 — Editing MIDI: <https://www.ableton.com/en/live-manual/12/editing-midi/>
- Ableton — Note & Velocity Chance FAQ: <https://help.ableton.com/hc/en-us/articles/360019144299-Note-and-Velocity-Chance-FAQ>
- FL Studio — Piano Roll manual: <https://www.image-line.com/fl-studio-learning/fl-studio-online-manual/html/pianoroll.htm>
- Cubase Pro 15 — Key Editor: <https://www.steinberg.help/r/cubase-pro/15.0/en/cubase_nuendo/topics/midi_editors/midi_editors_key_editor_r.html>
- The Digital Audio Manual — Key Editor Operations v2 (Cubase 12): <https://www.thedigitalaudiomanual.com/blog/key-editor-operations-v2-in-cubase-12>
- g200kg/webaudio-pianoroll (Apache-2.0): <https://github.com/g200kg/webaudio-pianoroll>
- ryohey/signal (MIT): <https://github.com/ryohey/signal>
- LMMS PianoRoll.cpp (GPL-2.0+): <https://github.com/LMMS/lmms/blob/master/src/gui/editors/PianoRoll.cpp>
- LMMS Piano Roll (DeepWiki): <https://deepwiki.com/LMMS/lmms/4.1-piano-roll-and-automation-editor>
- helio-fm/helio-workstation (GPL-3.0): <https://github.com/helio-fm/helio-workstation>
- mjhasbach/pixi-piano-roll (MIT): <https://github.com/mjhasbach/pixi-piano-roll>
- Wikipedia — Comparison of MIDI editors and sequencers (licenses): <https://en.wikipedia.org/wiki/Comparison_of_MIDI_editors_and_sequencers>
