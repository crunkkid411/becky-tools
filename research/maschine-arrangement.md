# Maschine 2 — Arrangement Model & Performance Features (Research / Build Target)

Research brief for becky's groovebox. Every claim is cited inline with a URL. Where the
official NI tech-manual / NI PDF pages refused automated fetches (HTTP 403), the same
content is cited via the search-surfaced manual excerpts and reputable mirrors
(Manualzz, ManualsLib, NI Support, ADSR Sounds, MaschineTutorials, MusicRadar). Sources
are listed at the bottom; inline cites use short tags that map to that list.

> Scope: ARRANGEMENT model (Pattern → Scene → Section → Song) + the two workflows (Ideas
> view vs Arranger view) + the four PAD INPUT MODES + the PERFORMANCE feature set
> (mute/solo, choke/link groups, Lock snapshots + Morph, Perform-FX, Note Repeat, tempo/tap).
> This is a *capability* spec to clone the model, NOT a UI clone.

---

## 0. TL;DR mental model

A Maschine **Project** is built bottom-up:

```
Project
 ├─ Groups (A–H banks of Sounds; a Group ~ one instrument/drum kit, 16 Sounds on 16 pads)
 ├─ Patterns        (per-Group step/real-time sequences; a Group has a Pattern bank)
 ├─ Scenes          (a Scene = a set of Patterns, one per Group, played together — a "section idea")
 ├─ Sections        (a slot on the Arranger Timeline that REFERENCES a Scene + has its own length)
 └─ Song / Arrangement (the ordered Sections on the Timeline = the finished track)
```

The single most important structural fact: **a Scene and the Section(s) that reference it are
"one and the same" content.** Edit a Scene's Patterns and *every* Section pointing at that
Scene changes too — Sections are references/instances, not copies, unless you explicitly make
one "unique." [NI-ARR][NI-VIDEO]

There are **two views of the same data**: the **Ideas view** (non-linear, loop-based
clip-launch grid of Patterns × Scenes — for experimenting) and the **Arranger / Song view**
(linear Timeline of Sections — for committing to an arrangement). They show the same Scenes;
they are two lenses on one project. [NI-ARR][NI-VIDEO]

---

## 1. The precise data model

### 1.1 Group (context, not in becky's v1 but needed to read the rest)
- A Project holds up to **8 Groups (A–H)**; each Group has **16 Sounds** mapped to the 16 pads
  in the default pad mode. A Group is the unit you'd think of as "a drum kit" or "an
  instrument." [NI-PADMODES][NI-OVERVIEW]
- becky's current `Machine` ≈ **one Group** (one 16-pad Kit + one Pattern bank). Multi-Group is
  a later extension (see §6).

### 1.2 Pattern
A Pattern is a sequence (step-programmed or real-time recorded) for **one Group**. Fields/behaviors:

| Field | Behavior | Cite |
|---|---|---|
| Name / id | Patterns live in a per-Group **Pattern bank** you switch between live. | [NI-IDEAS][NI-PADMODES] |
| **Length** | Adjustable; HW presets are **2 / 4 / 8 / 16 bars**, and free length via the LENGTH knob; the **Pattern Grid** mode defines the increments by which length can change. | [PATLEN][GRIDMODES] |
| **Step Grid resolution** | 16 settings from **1 whole bar down to 1/128**, plus **triplet** variants for all but the smallest — sets the step-sequencer cell size. | [PATLEN][GRIDMODES] |
| **Time signature** | Per-Pattern via the recording settings (METRONOME TIME knob), e.g. 4/4, 3/4, 6/8. | [PATLEN] |
| Events / notes | On/off + velocity + pitch + micro-timing; Note Repeat & 16-Velocities feed expressive events. | [NI-PERF][PADMODES2] |

Key nuance for the arrangement engine: **a Pattern shorter than the Section it sits in is
automatically repeated to fill the Section (last repeat may be truncated).** This is the
"polymeter / phasing" lever producers exploit. [NI-ARR][PHASING]

### 1.3 Scene
A Scene is a **horizontal combination of Patterns — one Pattern per Group — that play
together.** It represents one *idea / part* of the song (intro, verse, chorus, break…).
[NI-ARR][NI-VIDEO]

| Field | Behavior | Cite |
|---|---|---|
| Name | e.g. "Intro", "Verse", "Chorus". | [NI-ARR] |
| Pattern refs | One referenced Pattern **per Group** (in the Ideas grid: a column of pad/clip cells). | [NI-IDEAS][NI-VIDEO] |
| Scene Tempo (opt) | A Scene can carry a **custom tempo** (right-click → Scene Tempo) overriding the project tempo while it plays. | [SCENEMGMT] |
| (in Ideas view) loop range | The currently-played Scene loops; the Scene loop range is adjustable. | [SCENELOOP] |

Scenes are created/selected in the **Ideas view Scene grid**; you can **create, duplicate,
delete, and clear** Scenes. Duplicating a Scene by default shares its Patterns (reference) —
there is an explicit option to **duplicate with independent (new) Patterns**. [NI-IDEAS][DUPINDEP][SCENECREATE]

### 1.4 Section (the Timeline element — the piece becky lacks)
A Section is a **slot on the Arranger Timeline that references a Scene** and gives it a
position + a length. Sections are how Scenes become a linear song. [NI-ARR][NI-VIDEO]

| Field | Behavior | Cite |
|---|---|---|
| Position | Where the Section sits on the Timeline (Arrange Grid quantizes moves). | [NI-ARR][SECMGMT] |
| **Scene ref** | The Scene this Section plays — assigned via right-click slot → **Append** → pick Scene. | [SECLEN][SECMGMT] |
| **Length** | Independent of the referenced Scene's Pattern length; drag in the Timeline or set via controller. Can be **shorter or longer** than the contained Pattern; if longer, the Pattern **auto-repeats** to fill. | [SECLEN][NI-ARR] |
| Link vs Unique | Multiple Sections referencing one Scene are **linked** (edit one → all change). **"Make unique"** clones the Scene+Patterns into a fresh, independently-editable Section in place. | [NI-ARR][SECUNIQUE] |
| Loop range | A separate **Loop range** in the Timeline (start/end/position) set in **Arrange Grid** increments controls what loops during playback. | [SECLEN] |

**Section management ops** (all via Timeline right-click / controller): **Append** a Scene,
**Duplicate** (copies content to the next column, shifts the rest right), **Remove** (delete
slot), **Insert**, reorder by drag, and **Make Unique**. [SECMGMT][NI-ARR][SECUNIQUE]

### 1.5 Song / Arrangement
The **ordered set of Sections on the Timeline** = the song. There is no separate "song list"
object distinct from the Timeline of Sections; the Arranger Timeline *is* the song.
"Repeat count" in becky's current `Song.Entries[].Repeat` maps to **Maschine repeating a Scene
by either making the Section longer (Pattern auto-repeats) or placing multiple Sections** —
Maschine has no single "play N times" integer field; repetition is expressed as Section length
or duplicate Sections. [NI-ARR][SECLEN]

---

## 2. Ideas view vs Arranger view (the two workflows)

Both views **edit the same project data** — they are two presentations, not two copies. [NI-ARR][NI-VIDEO]

### Ideas view (non-linear / clip-launch grid)
- Purpose: **experiment with ideas free of a timeline.** You build Patterns per Group and
  combine them into Scenes. [NI-ARR][NI-IDEAS]
- Layout: a **grid** — **Pattern grid** (Patterns for the focused Group) and a **Scene grid**
  (the Scenes). Launching a Scene loops it; this is the live "jam / clip-launch" surface
  (think Ableton Session view). [NI-IDEAS][NI-VIDEO]
- You create/select/duplicate/delete **Patterns** and **Scenes** here. [NI-IDEAS][SCENECREATE]

### Arranger / Song view (linear timeline)
- Purpose: **sequence Scenes into the final arrangement** by creating **Sections** on the
  Timeline and assigning a Scene to each. [NI-ARR][NI-VIDEO]
- Layout: a horizontal **Timeline** of Section slots, each showing its Scene, with a movable
  **Loop range**, playhead, and per-Section length handles. [NI-ARR][SECLEN]

### How they relate (the load-bearing rule)
> "The content in these two areas are actually one-in-the-same… if you make a change to a Scene
> it will affect all other instances of that Scene automatically. If you place a Scene in three
> different Sections… and change the Patterns assigned to that Scene, the other two instances
> also play the newly-assigned Patterns." [NI-VIDEO][NI-ARR]

So: **Scene = the reusable musical idea; Section = a placement/instance of that idea on the
timeline with its own length.** Editing flows through the Scene to every Section unless a
Section is made unique. This is exactly the becky distinction to add: a `Scene` of pattern refs
+ a `Section` that *points at* a scene index and owns its own `LengthBars`/`LoopRange`.

---

## 3. The four PAD INPUT MODES (the pad-mode enum)

The 16 pads are remapped wholesale by the active pad input mode. The four modes (plus two
modifiers) are: [NI-PADMODES][CHEATSHEET][PADMODES2]

| Mode | What the 16 pads do | Cite |
|---|---|---|
| **Group / Pad mode** (default) | Each pad triggers a **different Sound** of the focused Group — i.e. play the whole kit; pad N → Sound N. Best for drum kits / loop banks. | [NI-PADMODES] |
| **Keyboard mode** | All 16 pads play the **same Sound at 16 different pitches** (chromatic, or constrained to a selected **scale/root**) — play melodies/basslines on one Sound. | [NI-PADMODES] |
| **Chord mode** | Pads trigger **chords** (each pad = a chord, built from scale/harmonizer settings) rather than single notes. | [NI-PADMODES][NI-PERF] |
| **Step mode** | The controller becomes a **step sequencer**: the 16 pads = 16 steps of the current Pattern (toggle steps on/off) for the focused Sound. | [NI-PADMODES][STEPSEQ] |

Modifiers available **in all four modes**: **Fixed Velocity** (every hit at one velocity), and
**16 Velocities** (all 16 pads play the same pitch at 16 ascending velocities — for fills /
dynamic rolls). [NI-PADMODES]

**becky enum implication** — a `PadInputMode` enum `{Group, Keyboard, Chord, Step}` that is a
*view/mapping* over the same underlying Sounds+Pattern, not a data-model change. Group/Step act
on the focused Sound's step lane; Keyboard/Chord generate pitched note events for one Sound.

---

## 4. Performance features

### 4.1 Mute / Solo
- **Mute** silences, **Solo** isolates, at **Sound** *and* **Group** level: hold MUTE/SOLO and
  press a pad (Sound) or a Group button (Group). Used to "create effective breaks on the fly."
  [NI-PERF]
- becky already models per-pad `Mute`/`Solo` + `AudiblePads()` (solo-wins). Group-level
  mute/solo arrives with multi-Group.

### 4.2 Choke groups vs Link groups (two pad-relationship systems)
- **Choke group**: pads in the same choke group **cut each other off** — triggering one stops
  the others (classic open-hat → closed-hat). becky already implements this
  (`ChokeGroup` + `ResolveChokes`, last-trigger-wins). [CHOKELINK][NI-PERF]
- **Link group**: pads in a link group **trigger together** — a master-slave relationship so
  one pad tap fires several Sounds simultaneously (e.g. kick + cymbal always hit together).
  becky does **not** have this yet. [CHOKELINK]

### 4.3 Lock snapshots + Morph (the marquee performance system)
- **Lock**: press LOCK to **snapshot all modulable parameters** of the Project; tweak freely
  during a performance; press the lit LOCK again to **recall** the snapshot's original values.
  [LOCK]
- **Extended Lock** (SHIFT+LOCK): store/recall **up to 64 snapshots** across banks on the pads;
  update, organize, clear them; an overview screen of all snapshots. [LOCK]
- **Morph**: when switching between two snapshots, Maschine can **morph (interpolate) between
  them** over a set sync/timing, adding movement — set morph on/off + morph time in Extended
  Lock or the on-screen overlay. [LOCK]
- Use cases: big modulations, A/B comparing mixes, live snapshot switching. [LOCK]
- becky has **none** of this — it's a Phase-2/3 feature (needs a parameter-modulation layer
  first). Note for the data model: a "Lock snapshot" is a named freeze of all automatable
  params; "Morph" is a timed interpolation between two snapshots.

### 4.4 Perform-FX
- **Perform FX**: enable PERFORM and use the Smart Strip to control a performance effect for the
  selected Group; SHIFT+PERFORM to pick/load a Perform FX. Live, gestural, per-Group FX. [NI-PERF]
- becky: not present; belongs with the audio/FX engine (Phase-2 audio).

### 4.5 Note Repeat
- **Note Repeat** plays the selected Sound/note **repeatedly at a chosen rate while a pad is
  held**; with it on, pads become **velocity- AND pressure-sensitive** for expressive rolls /
  dynamic basslines. A core beat-programming + performance tool. [NI-PERF]
- becky: not present; it's an input/playback behavior (generates repeated events), modelable as
  a rate + gate over a held pad.

### 4.6 Tempo / Tap
- Project **Tempo (BPM)**; **TAP** button — tap repeatedly to set the tempo. Per-Scene **Scene
  Tempo** override available via right-click. [SCENEMGMT][PADMODES2]
- becky already has `Machine.Tempo`. Add: **Tap-tempo** helper (deterministic average of tap
  intervals) and optional **per-Scene tempo** field.

---

## 5. Mapping to becky's existing `internal/drummachine`

becky's current model (`drummachine.go`) already mirrors a **single Group**:

| becky type | Maschine concept | Status |
|---|---|---|
| `Machine` | ~ one Group + project (tempo, kit, bank) | present |
| `Kit` / `Pad` (16) | a Group's 16 Sounds + per-Sound voice settings | present |
| `Pattern` (`Lanes[pad][step]`, `Steps` 16/32/64, `Swing`) | a Pattern (step seq) | present |
| `Bank` (≤16 Patterns) | the Pattern bank | present |
| `Scene{PatternIndex}` | a Scene — **but single-pattern, not one-per-Group** | partial |
| `Song{Entries[]{SceneIndex,Repeat}}` | the arrangement — **a flat scene list with repeat count, not a Section Timeline** | partial / divergent |
| `ChokeGroup` + `ResolveChokes` | choke groups | present |
| `Mute`/`Solo` + `AudiblePads` | Sound-level mute/solo | present |

### What's MISSING vs the Maschine model
1. **Section** type — the Timeline placement object (own length + loop range + Scene ref +
   link/unique). becky's `Song` conflates "scene order" with "arrangement"; Maschine separates
   Scene (idea) from Section (placement). This is the single biggest gap.
2. **Ideas-vs-Arranger duality** — the "one-and-the-same, edit-propagates" reference semantics
   (Sections reference Scenes; "make unique" to fork).
3. **Pattern Length in bars + time signature** — becky has `Steps` (16/32/64) only; no explicit
   `LengthBars`/`TimeSig`, no Pattern-auto-repeat-to-fill-Section.
4. **Pad input modes** — no `Group/Keyboard/Chord/Step` enum; becky is implicitly Group/Step.
5. **Multi-Group Scenes** — a Scene should hold one pattern ref *per Group*, not a single index.
6. **Link groups** (trigger-together) — only choke (cut-off) exists.
7. **Lock snapshots + Morph**, **Perform-FX**, **Note Repeat**, **Tap-tempo** — all absent.

---

## 6. v1-minimal vs later split (for becky)

### v1-minimal (pure-Go, deterministic, NO audio/GPU — buildable & unit-testable now)
These are data-model + logic changes that fit becky's existing immutable/deterministic style:

1. **Add `Section`** and make `Song`/`Arrangement` a Timeline of Sections:
   ```go
   type Section struct {
       Name        string
       SceneIndex  int      // reference (not a copy) — edits to the Scene propagate
       LengthBars  int      // own length; may exceed the Scene's pattern length
       Unique      bool     // false = linked; "make unique" forks Scene+Patterns
   }
   type Arrangement struct { Sections []Section; LoopStartBar, LoopEndBar int }
   ```
   Keep `Song.Entries{SceneIndex,Repeat}` as a back-compat alias, but treat `Repeat` as
   shorthand for "Section length = Repeat × Scene length" so it maps onto Maschine semantics.
2. **Pattern `LengthBars` + `TimeSig`** and the **auto-repeat-to-fill-Section** rule (pure math
   over existing `Steps`). becky's 16/32/64 → 1/2/4 bars; add 8/16-bar via larger `Steps` or a
   bars field.
3. **Multi-Group Scene** (or at least make `Scene` hold `[]PatternRef` so one-per-Group is
   possible) — even if becky stays single-Group in the GUI, model it correctly.
4. **`PadInputMode` enum `{Group, Keyboard, Chord, Step}`** as a mapping helper over the kit +
   focused sound (deterministic note/step generation for Keyboard/Chord; Step = the existing
   toggle path). Scale/root + chord tables are pure data.
5. **Link groups** (`LinkGroup int` on `Pad` + a `ResolveLinks` that expands one trigger into
   all linked pads) — symmetric to the existing choke logic, fully testable.
6. **Tap-tempo helper** (deterministic mean/median of intervals → BPM) + optional per-`Scene`
   `Tempo` override.
7. **"Make Section unique"** operation (deep-copy the Scene + its Patterns, repoint the Section)
   — pure data, deterministic, the fork primitive.

### Later (needs the audio engine / GPU / live HW — Phase-2/3, behind build tags)
- **Lock snapshots + Morph** — requires a parameter-modulation/automation layer to snapshot and
  to interpolate; only meaningful once params are automatable. (Model the snapshot/morph data
  shape in v1 if cheap, but no runtime.)
- **Perform-FX** — needs the real-time FX/audio engine + a control surface (Smart Strip
  analogue).
- **Note Repeat** with velocity/pressure — a live input/playback behavior; the *event
  generation* (rate × gate) is modelable in v1, the pressure-sensitivity is HW-bound.
- **Clip-launch / Ideas live looping UI** + the Arranger Timeline GUI — visual surfaces for
  becky-canvas (the data is v1; the surface is later).
- **Multi-Group mixing / routing** end-to-end (becky-wire already does routing on the DAW graph;
  reconcile there).

---

## Sources

- [NI-ARR] NI tech-manual, "Working with the Arranger" (Maschine software manual) — Ideas vs
  Arranger, Scenes, Sections, one-and-the-same content, Pattern auto-repeat in a longer Section:
  https://www.native-instruments.com/ni-tech-manuals/maschine-software-manual/en/working-with-the-arranger
- [NI-VIDEO] NI Support, "Using the Ideas and Arranger Views in MASCHINE 2 (VIDEO)" — the
  "content is one-in-the-same / edits propagate to all instances" rule, Scenes→Sections→Timeline:
  https://support.native-instruments.com/hc/en-us/articles/360000578389-Using-the-Ideas-and-Arranger-Views-in-MASCHINE-2-VIDEO
- [NI-IDEAS] Maschine 2 Manual (Manualzz mirror), "Using the Ideas View" — Pattern grid, Scene
  grid, creating/selecting Patterns & Scenes:
  https://manualzz.com/doc/o/nl4o0/maschine-2-manual-english-using-ideas-view
- [NI-OVERVIEW] NI tech-manual, "Maschine+ Overview" (Groups A–H, Sounds, pads):
  https://www.native-instruments.com/ni-tech-manuals/maschine-plus-manual/en/maschine--overview
- [NI-PADMODES] NI tech-manual, "Playing on the controller" (Maschine MK3) — Group/Pad,
  Keyboard, Chord, Step modes; Fixed Velocity; 16 Velocities:
  https://www.native-instruments.com/ni-tech-manuals/maschine-mk3-manual/en/playing-on-the-controller
- [NI-PERF] NI tech-manual / Manualzz "First Steps" & Perform features — mute/solo (Sound+Group),
  Note Repeat (held pad, velocity+pressure), Perform FX (PERFORM + Smart Strip), Chord mode:
  https://manualzz.com/doc/o/1cl86/native-instruments-maschine-studio-2.0-v2.8-getting-start...-first-steps
- [CHEATSHEET] NI "MASCHINE 2.0 MK3 Cheatsheet" PDF — PAD INPUT MODES list (1 Group, 2 Keyboard,
  3 Chord, 4 Step):
  https://www.native-instruments.com/fileadmin/ni_media/downloads/manuals/maschine_2.6.11/MASCHINE_2.0_MK3_Cheatsheet_EN_112017.pdf
- [PADMODES2] MusicRadar, "10 mighty Maschine power tips" — pad modes, tap tempo, performance:
  https://www.musicradar.com/how-to/10-mighty-maschine-power-tips
- [STEPSEQ] Maschine 2 Manual (Manualzz mirror), "Recording Patterns with the Step Sequencer":
  https://manualzz.com/doc/o/nedkb/maschine-2-manual-english-recording-patterns-with-the-step-sequencer
- [PATLEN] Maschine 2 Manual (Manualzz mirror), "Recording Patterns in Real Time" / pattern
  length presets (2/4/8/16 bars), GRID resolution (1 bar→1/128 + triplets), time signature:
  https://manualzz.com/doc/o/pcg1q/maschine-2-manual-english-recording-patterns-in-real-time
- [GRIDMODES] NI Support, "Grid Modes in MASCHINE 2" — Pattern Grid vs Step Grid:
  https://support.native-instruments.com/hc/en-us/articles/210272725-Grid-Modes-in-MASCHINE-2
- [PHASING] ADSR Sounds, "Phasing with Pattern Length in Maschine" — shorter Pattern repeats to
  fill a longer Section; polymeter/phasing:
  https://www.adsrsounds.com/maschine-tutorials/pattern-length-maschine/
- [SECLEN] NI / Manualzz "Using Arranger View" — Section length (drag), Pattern auto-repeat,
  Loop range, Append a Scene to a Section:
  https://manualzz.com/doc/o/rbp4w/maschine-2-manual-english-using-arranger-view
- [SECMGMT] NI / Manualzz "Creating an Arrangement" — Duplicate Section (shifts others right),
  Remove, Append, reorder:
  https://manualzz.com/doc/o/1clba/native-instruments-maschine-studio-2.0-v2.8-getting-start...-creating-an-arrangement
- [SECUNIQUE] NI tech-manual / Manualzz — "Make unique" forks a linked Section into new
  Scene+Patterns (same arranger docs as [NI-ARR]/[SECLEN]).
- [SCENECREATE] Manualzz "Creating Scenes" (Maschine Studio Getting Started):
  https://manualzz.com/doc/o/1clay/native-instruments-maschine-studio-2.0-v2.8-getting-start...-creating-scenes
- [SCENEMGMT] NI / Manualzz arranger docs — Scene Tempo (custom per-Scene tempo), Tap tempo
  (TAP button) — same arranger pages as [SECMGMT].
- [DUPINDEP] MaschineTutorials, "Creating independent patterns when duplicating scenes" —
  duplicate-with-new-Patterns vs shared reference:
  https://maschinetutorials.com/creating-independent-patterns-when-duplicating-scenes/
- [SCENELOOP] MaschineTutorials, "How to adjust the scene loop range":
  https://maschinetutorials.com/maschine-2-0-how-to-adjust-the-scene-loop-range-from-the-mk2-hardware-controller/
- [CHOKELINK] ADSR Sounds, "Using Maschine Choke Groups and Link Groups" — choke cuts off, link
  triggers together (master-slave):
  https://www.adsrsounds.com/maschine-tutorials/using-maschine-choke-groups-and-link-groups/
- [LOCK] ManualsLib (Maschine Studio/JAM manual) — Lock snapshot (snapshot all modulable params,
  recall), Extended Lock (up to 64 snapshots, banks), Morph between snapshots (sync/timing):
  https://www.manualslib.com/manual/1258176/Native-Instruments-Maschine-Studio.html?page=282

> Verification note: the canonical `native-instruments.com/ni-tech-manuals/...` and the NI PDF
> manual URLs returned HTTP 403 to automated fetch; their exact wording was captured via the
> search engine's rendered excerpts of those same pages plus the Manualzz/ManualsLib mirrors of
> the official manual, which are quoted above. Page-level deep links should be re-verified by a
> human in a browser before being quoted verbatim in a shipping spec.
