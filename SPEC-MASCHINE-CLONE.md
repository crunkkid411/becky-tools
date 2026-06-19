# SPEC: NI Maschine 2 capability spec (clone target)

Authoritative, source-cited capability breakdown of Native Instruments **Maschine 2**
(software) plus the **Maschine MK3** and **Maschine+** hardware, written as the build
target for a Maschine-class drum machine / groovebox. Every functional claim is
backed by a URL in the per-section sources and the consolidated list at the end.

How to read this: each capability area opens with a one-line **MUST** (the
contract), then concrete sub-features with the numbers that make them real. Two
prioritized build lists follow (v1 minimum viable, then later).

Scope note on terminology, since the clone's data model must mirror it:
- A **Project** contains up to **8 Groups (A–H)**; each Group holds **16 Sounds**
  (one per pad). A **Sound** is a chain of plug-in slots (Sampler/instrument + FX).
  ([Managing Sounds, Groups, and your Project — NI](https://www.native-instruments.com/ni-tech-manuals/maschine-mk3-manual/en/managing-sounds,-groups,-and-your-project))

---

## 1. Pads & Groups

**MUST:** 16 velocity-sensitive pads play/select the 16 Sounds of the focused Group,
with four selectable pad-input modes and per-pad performance options.

- **16 pads, 8 Groups.** The sixteen velocity-sensitive pads play and select Sounds;
  a Project holds up to 8 Groups (A–H), each with 16 Sounds. Switching the focused
  Group re-maps the 16 pads to that Group's 16 Sounds.
- **Four Pad Input Modes** (the pads' behavior is set by these, and the clone needs
  all four as a mode enum): **Group/Pad** (each pad = a different Sound), **Keyboard**
  (one Sound across all pads chromatically by pitch), **Chord**, and **Step**.
  ([Playing on the controller — NI](https://www.native-instruments.com/ni-tech-manuals/maschine-mk3-manual/en/playing-on-the-controller))
- **Fixed Velocity.** When on, pads play at a constant velocity regardless of hit
  strength — available in all four pad modes; useful so all slices of a chopped loop
  hit at equal volume. Can be set globally or per single pad.
  ([ADSR — Velocity Sensitivity & Fixed Velocity](https://www.adsrsounds.com/maschine-tutorials/maschine-tutorial-velocity-sensitivity-fixed-velocity/),
  [MaschineTutorials — full velocity on a single pad](https://maschinetutorials.com/maschine-quick-tip-how-to-set-fixed-or-full-velocity-on-a-single-pad/))
- **16 Velocities.** All 16 pads trigger the **same note of the focused Sound at 16
  different velocity values** — for programming dynamic fills. Toggled with
  `SHIFT + FIXED VEL`; only available in Group/Pad mode with Pad Mode active.
  ([Playing on the controller — NI](https://www.native-instruments.com/ni-tech-manuals/maschine-mk3-manual/en/playing-on-the-controller))
- **Note Repeat / Arp engine.** A flexible Note Repeat that doubles as a full
  arpeggiator — holds a pad and re-triggers at a selectable rate; recorded straight
  into the Pattern. (See §8 for Arp.)
  ([Playing on the controller — NI](https://www.native-instruments.com/ni-tech-manuals/maschine-mk3-manual/en/playing-on-the-controller))
- **Choke groups.** Pads assigned to the same Choke group cut each other off (classic
  open-hat/closed-hat exclusivity). Set via the Sound's Choke/Link knobs; Base Key is
  set alongside.
  ([Playing on the controller — NI](https://www.native-instruments.com/ni-tech-manuals/maschine-mk3-manual/en/playing-on-the-controller))
- **Link groups.** Sounds in the same Link group trigger together (one hit fires
  several Sounds) — same controls area as Choke.
  ([Playing on the controller — NI](https://www.native-instruments.com/ni-tech-manuals/maschine-mk3-manual/en/playing-on-the-controller))
- **Base Key.** Per-Sound setting for the pitch each Sound triggers at — load-bearing
  for Keyboard/Chord modes and for pitched one-shots.
  ([Playing on the controller — NI](https://www.native-instruments.com/ni-tech-manuals/maschine-mk3-manual/en/playing-on-the-controller))

Build implication: the 16-pad model already in `internal/drummachine` (Pad with
choke/mute/solo/GM note, DefaultKit) covers most of this; add **Link group**, a
**pad-input-mode enum** (Pad/Keyboard/Chord/Step), **Fixed Velocity** (global +
per-pad), and **16-Velocities** as a play/record helper.

---

## 2. Sound / sampler engine

**MUST:** each Sound can play one or more samples (or an instrument plug-in), and the
built-in Sampler can record, slice-to-pads, time-stretch, pitch, layer, and
destructively edit audio entirely offline.

- **Sample playback + recording.** Audio gets in two ways: live recording through the
  audio interface, or importing files; both land in the Sampler for slicing/stretching.
  ([ADSR — Sampling with Maschine 2.0](https://www.adsrsounds.com/maschine-tutorials/sampling-with-maschine/),
  [Sampling and Sample Mapping — NI](https://www.native-instruments.com/ni-tech-manuals/maschine-plus-manual/en/sampling-and-sample-mapping))
- **Slicing — four modes:** **Detect** (transient-based), **Grid** (fixed
  subdivisions), **Split** (equal count), **Manual**. A zero-crossing engine reduces
  pops/clicks at slice points. Slices **apply to a Group** so each slice lands on its
  own pad (pads 1, 2, 3 …), playable as a drum groove or melody.
  ([ADSR — Sampling with Maschine 2.0](https://www.adsrsounds.com/maschine-tutorials/sampling-with-maschine/),
  [MaschineTutorials — sampler slicing/layering overview](https://maschinetutorials.com/maschine-2-0-sampler-slicing-layering-and-editing-overview-with-maschine-studio/))
- **Time-stretch.** A time-stretch algorithm fits slices/samples to project tempo
  without changing pitch; **stretch quality is adjustable** (higher = cleaner, more
  CPU). Per-content modes: **Beats** (drums), **Tonal**, **Texture** (melodic/pads).
  Stretching is rendered to the sample, not real-time per-note.
  ([ADSR — Sampling with Maschine 2.0](https://www.adsrsounds.com/maschine-tutorials/sampling-with-maschine/),
  [MaschineTutorials — sampler slicing/layering overview](https://maschinetutorials.com/maschine-2-0-sampler-slicing-layering-and-editing-overview-with-maschine-studio/))
- **Pitch.** Slices/samples are pitchable; in Keyboard mode the Sampler plays a sample
  chromatically across the pads (multi-sample instrument building).
  ([ADSR — Sampling with Maschine 2.0](https://www.adsrsounds.com/maschine-tutorials/sampling-with-maschine/))
- **Zones / sample map / layering.** The **Zone** section lists samples for quick
  layering and lets you build **multi-sample instruments** by mapping samples across a
  key range (and velocity); easy to layer a slice with any library sound.
  ([MaschineTutorials — sampler zone map section](https://maschinetutorials.com/understanding-the-sampler-zone-map-section-in-maschine-2-0/),
  [Sampling and Sample Mapping — NI](https://www.native-instruments.com/ni-tech-manuals/maschine-plus-manual/en/sampling-and-sample-mapping))
- **Destructive edit functions (Edit tab):** select a sub-region and apply
  **truncate, normalize, reverse, cut, copy, paste, fade-in, fade-out, silence,
  DC-correct, duplicate**, plus time-stretch/normalize on just the selection.
  ([ADSR — Sampling with Maschine 2.0](https://www.adsrsounds.com/maschine-tutorials/sampling-with-maschine/))

Build implication: `internal/dsp` already has WAV decode + DSP. A v1 Sampler needs:
WAV one-shot per pad (have it), **slice-to-pads** (Detect via the existing onset
detector in `internal/dsp`; Grid/Split are pure math), per-pad **pitch/start/end/gain
+ reverse**, and an offline **Beats time-stretch**. Zones/multisample are later.

---

## 3. Sequencing

**MUST:** a 16-step (expandable) grid sequencer with adjustable resolution and full
per-step note properties, plus a piano-roll mode for melodic entry.

- **Step Mode vs Piano-roll.** Step mode lights the 16 pads as steps for the focused
  Sound; piano-roll mode plots notes up/down the keyboard inside the Pattern. Both
  feed the same Pattern data.
  ([ADSR — Step Mode on Studio hardware](https://www.adsrsounds.com/maschine-tutorials/step-mode-on-maschine-studio-hardware/),
  [MaschineTutorials — melodic step sequencing piano roll](https://maschinetutorials.com/maschine-jam-melodic-step-sequencing-using-piano-roll/))
- **Step length / Step-Grid resolution.** The **Step Grid** sets step size and thus
  editing precision; default **1/16**, selectable **1/8, 1/16, 1/32, 1/64, 1/128**
  (e.g. at 1/32 one pad = 1/32 of a bar). A **Triplet** option switches the grid to
  triplet subdivisions.
  ([Grid Modes in MASCHINE 2 — NI Support](https://support.native-instruments.com/hc/en-us/articles/210272725-Grid-Modes-in-MASCHINE-2),
  [MaschineTutorials — step/perform/pattern grid settings](https://maschinetutorials.com/maschine-2-0-understanding-using-the-step-performance-and-pattern-grid-settings/))
- **Nudge Grid (micro-timing).** Independent grid for nudging notes off the step grid
  to swing-shift the groove: **step, step/2, step/4, step/8, step/16** relative to the
  current step grid.
  ([Grid Modes in MASCHINE 2 — NI Support](https://support.native-instruments.com/hc/en-us/articles/210272725-Grid-Modes-in-MASCHINE-2))
- **Per-step properties.** Each step/event carries editable **velocity, pitch, note
  length (in step-grid increments), and timing/position**.
  ([ADSR — Maschine's Step Sequencer](https://www.adsrsounds.com/maschine-tutorials/maschine-hardware-step-sequencer/),
  [MaschineTutorials — Studio step sequencer for melodies](https://maschinetutorials.com/maschine-studio-using-the-step-sequencer-to-create-melodies/))
- **Probability:** Maschine 2 does **not** expose a native per-step probability
  parameter (this is a documented gap vs Elektron/Ableton; do not claim it). Treat
  probability as a clone *enhancement*, not a parity feature. (No NI source documents
  per-step probability — corroborate-then-conclude: absence across the manual and
  tutorial corpus → state it's not a stock feature.)

Build implication: the existing `Pattern` (16/32/64 steps + swing) and the
`drumcmd`/`DrumGrid` transforms cover the step layer; add the **resolution enum**
(1/8…1/128 + triplet), **per-step velocity/length/pitch** beyond on/off, and a
**piano-roll** note view.

---

## 4. Patterns / Scenes / Song

**MUST:** two complementary arrangement surfaces — a non-linear "Ideas" grid for
sketching, and a timeline "Arranger/Song" for committing structure — sharing one
underlying data model.

- **Ideas view (non-linear).** Experiment without a timeline: create **Patterns** per
  Group and combine them into a **Scene** (a column of one Pattern per Group).
  ([Working with the Arranger — NI](https://www.native-instruments.com/ni-tech-manuals/maschine-software-manual/en/working-with-the-arranger),
  [Using Ideas & Arranger Views — NI Support](https://support.native-instruments.com/hc/en-us/articles/360000578389-Using-the-Ideas-and-Arranger-Views-in-MASCHINE-2-VIDEO))
- **Song / Arranger view (timeline).** Assign **Scenes to Sections** on the timeline
  and reorder them to build the song (intro / verse / chorus / break …).
  ([Arranging Your Project — NI](https://www.native-instruments.com/ni-tech-manuals/maschine-plus-manual/en/arranging-your-project),
  [Audeobox — Song Arrangement](https://www.audeobox.com/learn/maschine/maschine-song-arrangement/))
- **Patterns vs Clips.** **Patterns are referenced objects** that exist in both views —
  edit one and every instance updates (single-sourced). **Clips exist only in the Song
  timeline and are unique** — one-off variations freely positioned. The clone's model
  must distinguish a referenced Pattern from a unique Clip.
  ([Working with Patterns and Clips — NI](https://www.native-instruments.com/ni-tech-manuals/maschine-plus-manual/en/working-with-patterns-and-clips),
  [MaschineTutorials — Ideas view](https://maschinetutorials.com/maschine-2-6-5-arranger-update-understanding-ideas-view/))
- **Hierarchy:** Pattern (per Group) → Scene (set of Patterns) → Section (a Scene
  placed on the timeline) → Song.
  ([Audeobox — Song Arrangement](https://www.audeobox.com/learn/maschine/maschine-song-arrangement/))

Build implication: the existing `Bank / Scene / Song` types in `internal/drummachine`
map directly; add the **Pattern-vs-Clip** distinction and a **Section/timeline** that
references Scenes.

---

## 5. Groove / Swing

**MUST:** swing applied at three stacking levels (Master, Group, Sound) with amount +
subdivision controls.

- **Three levels, additive.** Master (global, from the transport/Swing control),
  per-Group, and per-Sound swing — they **stack/add together**.
  ([MaschineTutorials — swing master/group/sound](https://maschinetutorials.com/how-to-apply-swing-to-the-master-group-and-individual-sounds-in-maschine/),
  [ADSR — Secrets of Maschine Swing](https://www.adsrsounds.com/maschine-tutorials/maschine-swing/))
- **Groove parameters per level:** **Swing amount** (primary), **Cycle** (which
  subdivision the swing acts on, e.g. 1/8, 3/16), and **Invert** (push notes early
  instead of late).
  ([MaschineTutorials — swing master/group/sound](https://maschinetutorials.com/how-to-apply-swing-to-the-master-group-and-individual-sounds-in-maschine/))

Build implication: `Pattern` already has a swing field; extend to **Group** and
**Master** swing that sum, and add **Cycle** + **Invert**. The existing
quantize/swing math in `drumcmd` is the engine.

---

## 6. Modulation

**MUST:** any continuous parameter can be automated, drawn per-step or recorded live,
viewed/edited in a per-Sound automation lane.

- **Control Lane.** Modulation/automation is created and edited in the **Control
  Lane** below the Pattern — draw automation curves per parameter.
  ([MASCHINE 2 Manual (2.8) PDF — NI](https://www.native-instruments.com/fileadmin/ni_media/downloads/manuals/maschine_2_8/MASCHINE_2_SW_Manual_English_2_8.pdf))
- **Record knob moves.** Turning a knob during playback records that movement as
  automation (live parameter automation).
  ([Community — Maschine 2 automation examples](https://community.native-instruments.com/discussion/437/maschine-2-automation-in-action-examples))
- **Step-mode modulation.** Per-step parameter values can be entered in Step mode
  (modulation tied to the step grid), so modulation is programmable, not only
  performed.
  ([MASCHINE 2 Manual (2.8) PDF — NI](https://www.native-instruments.com/fileadmin/ni_media/downloads/manuals/maschine_2_8/MASCHINE_2_SW_Manual_English_2_8.pdf))

Build implication: add a **per-parameter modulation track** keyed to ticks (one lane
per automatable param), with both draw (offline) and step-value entry. This is the
"modulation track" object the clone needs alongside the note grid.

---

## 7. FX & routing

**MUST:** insert FX at every level (Sound / Group / Master), send/aux FX, output
routing between Groups, and sidechain/ducking.

- **Insert FX at three levels.** Sound, Group, and Master each have plug-in slots and
  can host an **unlimited number of insert effects**.
  ([Using effects — NI MK3](https://www.native-instruments.com/ni-tech-manuals/maschine-mk3-manual/en/using-effects),
  [MaschineTutorials — insert/bus/master/send routing](https://maschinetutorials.com/insert-bus-master-and-send-fx-routing/))
- **Send / aux FX.** Maschine supports both **send and insert** effects; an aux send
  blends the FX with the dry signal. A **Multi-FX** group preset can be loaded into an
  empty Group slot to act as an FX bus/return.
  ([Using effects — NI MK3](https://www.native-instruments.com/ni-tech-manuals/maschine-mk3-manual/en/using-effects),
  [How to Route Groups to Multi FX — NI Support](https://support.native-instruments.com/hc/en-us/articles/209555869-How-to-Route-Groups-to-Multi-FX-in-MASCHINE),
  [MaschineTutorials — audio/fx routing & sidechain](https://maschinetutorials.com/maschine-2-0-audio-routing-fx-routing-and-sidechaining/))
- **Output routing between Groups.** A Group's output can be routed to another Group's
  input (e.g. Group A → another Group) for shared bus processing, plus cue/aux outs.
  ([MaschineTutorials — audio/fx routing & sidechain](https://maschinetutorials.com/maschine-2-0-audio-routing-fx-routing-and-sidechaining/))
- **Sidechain / ducking.** Sidechain-capable effects (e.g. the Compressor/Gate) expose
  a **sidechain input**; route the kick to duck the bass/pad — works for internal, NI,
  and 3rd-party plug-ins.
  ([ADSR — Maschine Sidechain overview](https://www.adsrsounds.com/maschine-tutorials/maschine-sidechain/),
  [MaschineTutorials — audio/fx routing & sidechain](https://maschinetutorials.com/maschine-2-0-audio-routing-fx-routing-and-sidechaining/))

Build implication: this is exactly what `becky-wire` + `internal/studio` already model
(graph of buses, insert/send FX, sidechain edges). Reuse that graph; the drum machine
emits a `music.Project` with Sound→Group→Master buses and sidechain edges.

---

## 8. Smart Play (scales / chords / arp)

**MUST:** lock pads to a musical scale, trigger full chords from one pad, and
arpeggiate held notes — all recorded into the Pattern.

- **Scales.** Lock the pads to a selectable scale (beyond chromatic) so played notes
  stay in key; added in Maschine **2.2**.
  ([DJ TechTools — Maschine 2.2 update](https://djtechtools.com/2014/11/24/maschine-2-2-update-scales-chords-touch-knobs-and-more/),
  [CDM — Maschine 2.2 playability](https://cdm.link/maschine-2-2-proves-pad-playability-matters/))
- **Chords.** Play a full chord from a single pad; selectable chord types/sets; works
  together with the active scale so chords stay in key.
  ([DJ TechTools — Maschine 2.2 update](https://djtechtools.com/2014/11/24/maschine-2-2-update-scales-chords-touch-knobs-and-more/),
  [ADSR — Maschine 2.2 update tutorial](https://www.adsrsounds.com/maschine-tutorials/maschine-2-2-mk2-mikro-hardware-update/))
- **Arp.** Note Repeat extended into a full arpeggiator: hold notes → Maschine turns
  them into a melodic pattern; combine with Scale/Chord to stay in key; **recorded
  into the Pattern** for post-edit. Direction/type and rate are controllable (the
  shared NI Arp engine offers Up / Down / Up-Down direction, rate, and octave-reach
  controls).
  ([CDM — Maschine 2.2 playability](https://cdm.link/maschine-2-2-proves-pad-playability-matters/),
  [ADSR — Maschine 2.2 update tutorial](https://www.adsrsounds.com/maschine-tutorials/maschine-2-2-mk2-mikro-hardware-update/),
  [Arpeggiator — NI Kontrol S-mk3 (shared engine)](https://www.native-instruments.com/ni-tech-manuals/kontrol-s-mk3-manual/en/arpeggiator))

Build implication: `internal/music` already has theory/scale/genre logic; add a
**scale-lock + chord-trigger + arp** layer over the pad-play path so notes generated
on the pads quantize to a chosen key/scale.

---

## 9. Browser / tagging / kits

**MUST:** an attribute-tagged browser to find Sounds/Groups/Kits/samples/presets fast,
covering both factory and user content.

- **Attribute tagging.** Content is tagged by **type** (Kick, Snare, Pad), **character**
  (Dark, Bright, Aggressive), and **source** (Acoustic, Electronic); filter the browser
  by these tags. NI uses the **NKS** (Native Kontrol Standard) tagging scheme.
  ([NKS FX tags — NI Support](https://support.native-instruments.com/hc/en-us/articles/360000225269-NKS-FX-Updated-Tags-in-the-MASCHINE-KOMPLETE-KONTROL-Browser))
- **Factory vs User libraries.** A single toggle switches the browser between Factory
  and User content.
  ([Managing User Samples — NI Support](https://support.native-instruments.com/hc/en-us/articles/360001013937-Managing-User-Samples-in-MASCHINE-2-8-0-and-KOMPLETE-KONTROL-2-1-0))
- **Kits (Groups) are taggable + savable.** Save a Group as a kit, then tag it in the
  User section (Edit → set tags → save); import 3rd-party samples/presets/libraries
  into the browser.
  ([MaschineTutorials — import user kits/samples](https://maschinetutorials.com/how-to-import-user-kits-samples-and-libraries-in-maschine-2-0/),
  [ADSR — 3rd-party presets & samples](https://www.adsrsounds.com/maschine-tutorials/3rd-party-presets-and-samples-in-maschine-2/))

Build implication: a JSON kit/sample index with **type/character/source** tag facets
and a factory/user split, plus "save Group as kit." Maps onto the clone's `Kit` type.

---

## 10. MIDI & audio I/O, MIDI export

**MUST:** play/record via MIDI in/out, run as plug-in or standalone with multi-out
audio, and export Patterns as MIDI and Scenes/Patterns as audio.

- **MIDI export — two paths:** (1) the **MIDI export** entry in the Pattern file menu,
  and (2) **MIDI drag-and-drop** from the Pattern Editor's MIDI-dragger icon into a
  DAW — or onto another Sound/Group inside Maschine to import the notes.
  ([Maschine 2 — Export MIDI Files (manual mirror)](https://de.mans.io/files/viewer/2526030/287),
  [MaschineTutorials — MIDI Export menu](https://maschinetutorials.com/maschine-quick-tip-the-midi-export-menu/),
  [ADSR — Drag & Drop MIDI](https://www.adsrsounds.com/ni-massive-tutorials/maschine-drag-and-drop/))
- **Audio export / drag.** Set Pattern Drag Mode to **Audio**, render, then drag the
  rendered sequence as audio into a DAW track (and full Audio export of Scenes/Project).
  ([MaschineTutorials — drag & drop audio or MIDI](https://maschinetutorials.com/how-to-drag-and-drop-audio-or-midi-from-maschine/),
  [Exporting your Ideas — NI](https://www.native-instruments.com/en/maschine-mikro-quickstart/exporting-your-ideas/))
- **MIDI in/out + controllers.** Sounds/Groups can be played and driven by external
  MIDI; the hardware is a MIDI controller, and Maschine runs standalone or as a
  plug-in with multi-output audio.
  ([Maschine+ Overview — NI](https://www.native-instruments.com/ni-tech-manuals/maschine-plus-manual/en/maschine--overview))

Build implication: the clone already has a pure-Go **SMF reader/writer** in
`internal/music` and `becky-compose` emits `.mid` + `becky-daw-engine` renders audio —
so **MIDI export and audio render are already solved**; just wire per-Pattern MIDI
export and per-Scene audio render to the new model.

---

## v1 — minimal viable drum machine (smallest set that feels like a real instrument)

Ordered by build priority. The goal: load a kit, program a beat by ear and by eye,
make it groove, and get sound + a file out.

1. **16 pads in one Group, click-to-audition + light-up** (have it in
   `cmd/drummachine`). One-shot WAV per pad with **fixed velocity** toggle.
2. **16-step sequencer with selectable resolution** (1/16 default; 1/8, 1/32, triplet)
   and **per-step velocity** + **mute/solo**. (Extends existing `Pattern`.)
3. **Choke groups** (open/closed hat) — already modeled; keep it.
4. **Swing** at pattern/Group level with an **amount** control (have the field; expose
   it). One stacking level is enough for v1.
5. **Sampler basics:** load a sample, **slice-to-pads** (Detect + Grid/Split), per-pad
   **start/end/pitch/reverse/gain**. Drop a chopped break across the 16 pads.
6. **Patterns + Scenes (Ideas grid):** several Patterns, combine into a Scene, chain
   Scenes to play a loop/arrangement. (Have `Pattern/Scene/Song`.)
7. **Per-pad insert FX + a Master bus + one sidechain** (kick → bass duck) via the
   existing `becky-wire`/`internal/studio` graph.
8. **MIDI export of a Pattern + audio render of a Scene** (already have SMF + render).
9. **A small tagged kit browser** (type/character/source) + **save Group as kit**.
10. **AI box (the clone's differentiator):** plain-English → edits the model live
    (already prototyped in `internal/machinectl`).

This set = a real, playable, file-producing drum machine.

---

## Nice-to-have / later

- **Piano-roll melodic editing** + Keyboard pad mode across pitches.
- **Smart Play:** scale-lock, one-pad chords, arpeggiator (combine to stay in key).
- **Note Repeat** at selectable rates (live re-trigger).
- **Full modulation/Control Lane:** per-parameter automation, record knob moves,
  step-mode modulation values.
- **Three-level stacking swing** (Master + Group + Sound) with **Cycle** + **Invert**.
- **Sampler depth:** time-stretch modes (Beats/Tonal/Texture) with quality setting;
  zones / multi-sample instruments; full destructive edit suite
  (normalize/reverse/truncate/fades/DC-correct).
- **Arranger timeline with Sections + unique Clips** (Pattern-vs-Clip distinction).
- **Link groups**; **16-Velocities** play helper.
- **Per-step probability** (a clone enhancement — *not* Maschine parity).
- **Send/aux returns + Multi-FX bus groups**, inter-Group output routing, multi-out
  audio, plug-in (VST3/CLAP) hosting per Sound.
- **Up to 8 Groups (A–H)** with full Group switching.

---

## 5 most important sources

1. **Playing on the controller — Maschine MK3 Manual (NI, official)** — pad modes,
   fixed/16 velocities, note repeat, choke/link, base key.
   https://www.native-instruments.com/ni-tech-manuals/maschine-mk3-manual/en/playing-on-the-controller
2. **Working with Patterns and Clips — Maschine+ Manual (NI, official)** — Pattern vs
   Clip, Ideas vs Song, Scenes/Sections.
   https://www.native-instruments.com/ni-tech-manuals/maschine-plus-manual/en/working-with-patterns-and-clips
3. **Grid Modes in MASCHINE 2 — NI Support (official)** — Step/Nudge grids and the
   1/8–1/128 + triplet resolution values.
   https://support.native-instruments.com/hc/en-us/articles/210272725-Grid-Modes-in-MASCHINE-2
4. **In-Depth Overview of Sampling with Maschine 2.0 — ADSR** — slice modes
   (Detect/Grid/Split/Manual), time-stretch modes, zones/layering, destructive edits.
   https://www.adsrsounds.com/maschine-tutorials/sampling-with-maschine/
5. **Using effects — Maschine MK3 Manual (NI, official)** — insert/send FX at
   Sound/Group/Master, unlimited inserts, routing & sidechain.
   https://www.native-instruments.com/ni-tech-manuals/maschine-mk3-manual/en/using-effects
