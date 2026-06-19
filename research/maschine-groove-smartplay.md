# Maschine 2 — Timing/Groove & Smart Play: Exhaustive Build-Target Research

**Purpose:** Source-cited spec of Maschine 2's swing/groove, quantize, note-repeat/arp,
velocity, and Smart Play (scale/chord) features, to be used as a build target for becky's
DAW/drum tools (becky-drum, becky-daw, becky-compose, becky-canvas). Every factual claim is
tied to a URL. Where a numeric algorithm is stated by a primary source, it is given exactly;
where Maschine's manual is descriptive rather than numeric, the underlying MPC/Roger-Linn
algorithm is cited separately and clearly labelled as the inferred math.

Maschine's Smart Play scale/chord/arp engine is **the same engine** across Maschine software,
Maschine MK3, Maschine+, and the Komplete Kontrol S-Series — NI ships identical scale and
chord tables. The canonical, most complete tables live in the Kontrol S-Series MK3 manual; the
Maschine manuals describe the same parameters in workflow terms. This is noted at each table.

---

## 1. SWING / GROOVE

### 1.1 What "Groove" is in Maschine

Maschine exposes groove as a single property page called **Swing**. Per the official software
manual, *"Swing controls the rhythmic relationship between events in the selected channel
(Sound, Group or Master)… the Swing feature shifts some of the played notes, hereby adding some
'groove' to your Pattern. By shifting some of the events, you can for example give a shuffling,
ternary touch to your Patterns."*
(https://www.native-instruments.com/ni-tech-manuals/maschine-software-manual/en/playing-on-the-controller)

The Groove/Swing page has exactly three parameters (official software manual,
https://native-instruments.com/ni-tech-manuals/maschine-software-manual/en/playing-on-the-controller):

| Parameter | Definition (quoted) |
|-----------|---------------------|
| **Amount** | *"Adjusts the amount of swing, i.e. the amount by which some events are shifted. At 0 % events are not shifted."* |
| **Cycle**  | *"Determines on what musical resolution the groove is applied."* (a note fraction, e.g. 1/2, 1/4, 1/8, 1/16) |
| **Invert** | *"Allows you to invert the groove so that instead of being delayed in the Pattern, events will be triggered ahead of time."* |

Key facts:
- **Amount = 0 % means no shift.** Maschine's Amount is therefore a 0–100 % *intensity*
  control, NOT the MPC "50 %=straight / 66 %=triplet" ratio scale (see §1.4 for the
  distinction — this matters for becky).
- **Cycle** sets *which* subdivision is grooved: with Cycle = 1/2 (half note), the example in
  the manual shows an eighth-note pattern getting a shuffle; smaller Cycle values groove finer
  subdivisions.
- **Invert** pushes events *earlier* instead of later (rushing instead of dragging).

### 1.2 Per-channel swing hierarchy (Sound vs Group vs Master) — ADDITIVE

Groove is set independently on each channel level and the levels **add together** (they do not
override). Official software manual: *"At the Master level, the Groove properties affect all
Sounds of all Groups. The Master's swing is added to the swing of each individual Group and
Sound."* (https://www.native-instruments.com/ni-tech-manuals/maschine-software-manual/en/playing-on-the-controller)

Confirmed by tutorial: *"Remember everything will add, so if you swing the sounds then swing
the group, it will add swing to the swing, same with master."*
(https://maschinetutorials.com/how-to-apply-swing-to-the-master-group-and-individual-sounds-in-maschine/)

So the effective swing for a given Sound's event = `sound.amount + group.amount + master.amount`
(intensities sum). Group adds onto Sound; Master adds onto Group+Sound.
(https://www.native-instruments.com/ni-tech-manuals/maschine-software-manual/en/playing-on-the-controller)

### 1.3 The underlying algorithm (MPC / Roger Linn) — what "shift the off-beats" means

Maschine's manual is descriptive ("shifts some of the played notes… ternary touch"); the
numeric algorithm it implements is the classic Roger-Linn / Akai-MPC swing. Roger Linn's own
description (Attack Magazine interview):

- Swing **delays every second 16th note** (the even-numbered 16ths: 2, 4, 6, 8…) within each
  beat — i.e. the 16th that sits *between* two 8th notes.
  (https://www.attackmagazine.com/features/interview/roger-linn-swing-groove-magic-mpc-timing/)
- The amount is expressed as the **ratio of time between the first and second 16th of each
  pair**:
  - **50 % = equal spacing = NO swing** (straight 16ths).
  - **66 % = perfect triplet swing**: the first 16th gets 2/3 of the pair's time, the delayed
    16th gets 1/3, so the off-16th lands exactly on the 8th-note triplet.
  - Useful musical range is roughly **50 %–75 %**; above that it approaches a dotted-8th +
    16th (very heavy shuffle).
  (https://www.attackmagazine.com/features/interview/roger-linn-swing-groove-magic-mpc-timing/ ;
   https://www.tumblr.com/palsen/182157488304/about-mpc-swing)

**The math (delay form), for a pair spanning one 8th-note of duration `T8` (two 16ths):**
Let `s` = swing ratio in [0.5, 0.75…]. The first 16th starts on the grid at the beat; the
second (off) 16th, normally at `T8/2`, is moved to:

```
offset_position = s * T8          # fraction of the 8th-note window
delay_applied   = (s - 0.5) * T8  # how far the off-16th is pushed late vs straight
```

So at `s = 0.50`, `delay = 0`; at `s = 0.66`, `delay = 0.16·T8` (lands on the triplet);
at `s = 0.75`, `delay = 0.25·T8` (off-16th becomes a dotted-8th+16th feel).
(Derived from the Roger Linn ratio definition above —
https://www.attackmagazine.com/features/interview/roger-linn-swing-groove-magic-mpc-timing/)

### 1.4 Reconciling Maschine's 0–100 % Amount with the MPC 50–75 % ratio

This is the load-bearing subtlety for becky. Maschine's **Amount** knob is **0 % = straight**
(no shift) per the manual quote in §1.1, whereas the **MPC ratio** is **50 % = straight**.
They describe the same physical delay, on different scales:

```
maschine_amount (0..100%) ≈ 2 * (mpc_ratio - 0.5) * 100
# 0% amount   ->  ratio 0.50  (straight)
# 33% amount  ->  ratio ~0.66 (triplet swing)
# 50% amount  ->  ratio 0.75  (heavy shuffle)
```

Community/tutorial summaries sometimes quote the *MPC* scale when describing Maschine ("100% =
no shift, 66% = triplet, 50% = dotted-16th, 0% = flam"), which is the MPC-ratio convention, not
the on-screen Amount knob. becky should pick **one** convention and document it; the MPC ratio
(50 % straight, 66 % triplet) is the more standard cross-tool one.
(https://www.adsrsounds.com/maschine-tutorials/maschine-swing/ summary;
 https://www.attackmagazine.com/features/interview/roger-linn-swing-groove-magic-mpc-timing/)

---

## 2. QUANTIZE

All quantize definitions below are quoted/condensed from the official Maschine software manual,
"Working with Patterns and Clips":
https://native-instruments.com/ni-tech-manuals/maschine-software-manual/en/working-with-patterns-and-clips

### 2.1 Quantize modes

| Mode | Definition (quoted) |
|------|---------------------|
| **Full Quantize** | *"Moves each event directly onto the closest step of the current Step Grid. This allows a perfectly regular rhythm."* |
| **Quantize 50 % (Half)** | *"Moves each event halfway toward the closest step of the current Step Grid. This allows a tighter rhythm while retaining a human feel."* Re-applicable repeatedly to tighten gradually toward the grid. |

Half-quantize is iterative: *"you can repeatedly apply Quantize 50 % until you are happy"* —
each application halves the remaining distance to the grid. So after *n* applications the
residual offset from the grid = `original_offset * 0.5^n`.
(https://www.adsrsounds.com/maschine-tutorials/maschine-quantization/)

**Quantize math (single application):**
```
nearest = round(event_pos / grid) * grid
full:   new_pos = nearest
half:   new_pos = event_pos + 0.5 * (nearest - event_pos)
        = event_pos*0.5 + nearest*0.5
strength s in [0,1]:  new_pos = event_pos + s * (nearest - event_pos)
```
ADSR confirms the strength interpretation: *"When Q-strength is 100 %, the quantizer covers
that entire gap… When Q-strength is 50 %, the quantizer covers half of that gap, bringing it
halfway to 'right on.'"* (https://www.adsrsounds.com/maschine-tutorials/maschine-quantization/)

### 2.2 Swing-quantize / groove

Maschine does not have a separate "swing quantize" command that bakes swing into note
positions; swing is applied **non-destructively** as a Groove property (§1) at playback. To get
a swung *quantize* feel you quantize to the grid then apply Groove Amount. ADSR frames groove as
the way to *"add some natural swing"* after quantizing.
(https://www.adsrsounds.com/maschine-tutorials/use-grid-quantize-groove-maschine/ ;
 https://www.adsrsounds.com/maschine-tutorials/maschine-quantization/)

### 2.3 Input quantization / Quantize-While-Playing

Three modes (official software manual,
https://native-instruments.com/ni-tech-manuals/maschine-software-manual/en/working-with-patterns-and-clips):

| Mode | Behaviour (quoted/condensed) |
|------|------------------------------|
| **None** | Deactivated; events are not quantized on input. |
| **Record** | Quantization applied only when recording from the pads. |
| **Play/Rec** | Applied both during playback and recording. |

Play/Rec live behaviour: *"When recording in Play/Rec mode, events quantize to the nearest
step. During playback, events in the first half of [a] step remain unchanged; those in the
second half quantize to the next step."* (snaps a late-played note forward to the next grid
line in real time).

### 2.4 Double-note removal during quantize

*"Maschine automatically detects and removes these double notes while quantizing"* — when MIDI
keyboard/pad input creates unwanted duplicate notes at the same position+pitch, quantizing
de-dupes them.
(https://native-instruments.com/ni-tech-manuals/maschine-software-manual/en/working-with-patterns-and-clips)

### 2.5 Step Grid resolutions (the quantize/grid values)

Available Step Grid step sizes (official software manual,
https://native-instruments.com/ni-tech-manuals/maschine-software-manual/en/working-with-patterns-and-clips):

```
1 Bar, 1/2, 1/4, 1/8, 1/16 (default), 1/32, 1/64, 1/128
+ triplet variants of each
+ Off (grid disabled)
```
Default = **1/16**. Maschine distinguishes the **Step Grid** (quantize target / step-sequencer
resolution) from the **Pattern Grid** (pattern-length editing) and the **Perform Grid** (when
pattern changes take effect) — all three are independent grid settings.
(https://maschinetutorials.com/maschine-2-0-understanding-using-the-step-performance-and-pattern-grid-settings/)

---

## 3. NOTE REPEAT & ARP (the Arp engine)

History: Maschine 2.2 (Nov 2014) extended **Note Repeat** into a full **Arp** and added the
Scale/Chord engine.
(https://djtechtools.com/2014/11/24/maschine-2-2-update-scales-chords-touch-knobs-and-more/ ;
 https://maschinetutorials.com/maschine-2-2-using-the-new-arp-mode-feature/)

The Arp/Note-Repeat parameter set below is the canonical NI Smart-Play arp, documented most
completely in the Kontrol S-Series MK3 manual (same engine as Maschine):
https://native-instruments.com/ni-tech-manuals/kontrol-s-mk3-manual/en/arpeggiator
Cross-confirmed by the Maschine+ manual:
https://native-instruments.com/ni-tech-manuals/maschine-plus-manual/en/recording-patterns

### 3.1 Mode

- **Note Repeat** — repeats the held note(s) at the Rate.
- **Arp** — *"generates arpeggiator sequences based on chords you play."*

### 3.2 Arp Type (playback order)

`Up, Down, Up & Down, Order Played, Chord`
(both Kontrol S MK3 and Maschine+ manuals)

### 3.3 Rate (full table — same for Note Repeat and Arp)

Range **1/1 down to 1/128**, each with Normal / Dotted / Triplet variants (Maschine+ also lists
**1 Bar** at the top for Note Repeat):

```
Straight:  1/1   1/2   1/4   1/8   1/16   1/32   1/64   1/128
Dotted:    1/1 D 1/2 D 1/4 D 1/8 D 1/16 D 1/32 D 1/64 D 1/128 D     (×1.5 duration)
Triplet:   1/1 T 1/2 T 1/4 T 1/8 T 1/16 T 1/32 T 1/64 T            (×2/3 duration)
(+ "1 BAR" available for Note Repeat rate)
```
- Dotted = 1.5× the base note duration; Triplet = 2/3× the base note duration.
- (Kontrol S MK3: https://native-instruments.com/ni-tech-manuals/kontrol-s-mk3-manual/en/arpeggiator ;
   Maschine+: https://native-instruments.com/ni-tech-manuals/maschine-plus-manual/en/recording-patterns)

### 3.4 Other Arp parameters

| Parameter | Values / meaning |
|-----------|------------------|
| **Octaves** | `1–8` — play within the pressed octave or span up to 8 octaves. |
| **Gate** | `1.0 %–200 %` — note duration vs. silence between notes (>100 % = overlap/legato). |
| **Dynamic** | `1.0 %–200 %` — scales input velocity (from pad pressure / poly aftertouch). |
| **Swing** | `0–100 %` — *"introducing a delay to every second note in a sequence"* (same swing concept as §1). |
| **Sequence** | `Off, 1, 2, 3, 4, 5, 6, 7, 8` — rhythmic/step sequence template applied to the arp (16-step or 12-step depending on Rate); `Off` = regular. |
| **Retrigger** | restarts the sequence cycle after N steps. |
| **Repeat** | number of times each step in the arp sequence is repeated. |
| **Offset** | shifts the sequence start position by N steps. |
| **Inversion** | adds inverted alternations to the cycle. |
| **Range (Min Key / Max Key)** | keyboard range that triggers the arp. |

(Kontrol S MK3 manual:
https://native-instruments.com/ni-tech-manuals/kontrol-s-mk3-manual/en/arpeggiator ;
Maschine+ manual:
https://native-instruments.com/ni-tech-manuals/maschine-plus-manual/en/recording-patterns)

---

## 4. VELOCITY MODES

From the Maschine MK3 manual / NI docs and tutorials:

- **Fixed Velocity** (`FIXED VEL` button): all pads/notes play at one fixed velocity regardless
  of how hard you hit, instead of being pressure-sensitive.
  (https://www.native-instruments.com/ni-tech-manuals/maschine-mk3-manual/en/playing-on-the-controller ;
   https://www.adsrsounds.com/maschine-tutorials/maschine-tutorial-velocity-sensitivity-fixed-velocity/)
- **16 Velocities** (`SHIFT + FIXED VEL`): the 16 pads all play the **same note/pitch** of the
  focused Sound, each at a **different fixed velocity** (graded across the 16 pads). Used for
  drum fills / velocity ramps from one sound; the right display shows each pad's velocity value.
  (https://maschinetutorials.com/maschine-mikro-how-to-enable-16-level-velocity-pad-mode/ ;
   https://www.adsrsounds.com/maschine-tutorials/maschine-tutorial-velocity-sensitivity-fixed-velocity/)

These three pad-input modes (normal pressure-sensitive, Fixed, 16-Velocities) are mutually
exclusive input behaviours.

---

## 5. SMART PLAY — SCALE & CHORD ENGINE

Added in Maschine 2.2; identical engine across Maschine and Kontrol. The full scale/chord
**tables below are quoted from the Kontrol S-Series MK3 manual** (the most complete published
source for the same engine):
https://native-instruments.com/ni-tech-manuals/kontrol-s-mk3-manual/en/scales
Maschine workflow descriptions cross-confirm the parameters:
https://www.native-instruments.com/ni-tech-manuals/maschine-plus-manual/en/recording-patterns ;
https://djtechtools.com/2014/11/24/maschine-2-2-update-scales-chords-touch-knobs-and-more/

### 5.1 Root Note

12 chromatic options: `C, C#, D, D#, E, F, F#, G, G#, A, A#, B`.

### 5.2 Key Mode (how the scale maps to the pads/keys)

Three modes (Kontrol S MK3):
- **Guide** — all chromatic keys present, but out-of-scale notes are visually marked; you can
  still hit them.
- **Mapped** *(default)* — pads/keys are **remapped** so only in-scale notes are playable; every
  pad is an in-scale degree (you cannot play a wrong note). This is Maschine's headline
  "always in key" mode.
- **Easy** — in-scale notes are laid out starting from the root on the first pad, regardless of
  the chosen root, so every scale is fingered the same way.

(https://native-instruments.com/ni-tech-manuals/kontrol-s-mk3-manual/en/scales)

Note on terminology: Maschine's own pad docs phrase Key Mode as **Chromatic** vs the mapped/
guided scale playing — *"when Chord Mode is set to Harmonizer and Scale Type is set to
Chromatic, the scale includes all semitones."* (Chromatic = the no-scale passthrough, i.e. the
first scale "Chrom" in the Main bank below.)
(https://www.manualslib.com/manual/1217134/Native-Instruments-Maschine-Jam.html?page=90)

### 5.3 Scale Banks and Scale Types (FULL LIST — 8 banks × 15)

Quoted from Kontrol S-Series MK3 manual
(https://native-instruments.com/ni-tech-manuals/kontrol-s-mk3-manual/en/scales).
Names are NI's abbreviated display strings.

**Main:** `Chrom, Major, Minor, Harm Min, Maj Pent, Min Pent, Blues, Japanese, Freygish,
Hung Min, Arabic, Altered, WH Tone, H-W Dim, W-H Dim`

**Modes:** `Ionian, Dorian, Phrygian, Lydian, Mixolyd, Aeolian, Locrian, Ion b2, Dor b5,
Har Phry, Phry Maj, Lyd b3, Maj Loc, Min Loc, Sup Loc`

**Jazz:** `Lyd b7, Altered, Diminshd, Mix b13, Mixb9b13, Lyd b7b2, Bebop, Whole Tn, Blues Ma,
Blues Mi, BluesCmb, Lyd #5, Jazz Mi, Half Dim, Augmentd`

**World:** `Hung Min, Hung Maj, Neapoltn, Spanish, Greek, Jewish 1, Jewish 2, Indian 1,
Indian 2, Indian 3, Indian 4, M East 1, M East 2, M East 3, M East 4`

**5-Tone:** `Pent I, Pent II, Pent III, Pent IV, Pent V, Hira, Insen, Kokin, Akebono, Ryukuan,
Abhogi, Bhupkali, Hindolam, Bhupalam, Amrita`

**Modern:** `Octatonc, Acoustic, Augmentd, Tritone, Lead Wh, Enigmatc, Scriabin, Tcherepn,
Mes I, Mes II, Mes III, Mes IV, Mes V, Mes VI, Mes VII`

**Major:** `Natural, Lydian, Mixolyd, Maj Min, Har Maj, Dbl Maj, Nea Maj, Maj Loc, Blues Ma,
Bebop Ma, Hexa 1, Hexa 2, Penta 1, Penta 2, Penta 3`

**Minor:** `Natural, Dorian, Phrygian, Min Maj, Har Min, Dbl Min, Nea Min, Min Loc, Blues Mi,
Bebop Mi, Hexa 1, Hexa 2, Penta 1, Penta 2, Penta 3`

(Maschine 2.2's original set was smaller — Main + Modes-type scales; later NI firmware/software
converged on this full 8-bank set shared with Kontrol. Treat the 8-bank table as the current
superset.)

### 5.4 Chord mode

Two chord systems (Kontrol S MK3 + Maschine+ docs):

**(a) Harmonizer** — builds a chord *on top of the played note, in the current scale* by adding
scale degrees. Harmonizer chord types (intervals as scale steps):
`Octave, 1-3, 1-5, 1-3-5, 1-4-5, 1-3-5-7, 1-4-7`
(diatonic — the actual quality, major/minor/dim, follows from where you are in the scale).

**(b) Chord Set** — preset chord voicings on each pad:
`Maj 1–8` (eight major-family voicings) and `Min 1–8` (eight minor-family voicings).

**Chromatic chord types** (when Scale Type = Chromatic, fixed-interval chords regardless of
key):
`Octave, Perf 4, Perf 5, Major, Minor, Sus 4, Maj 7, Min 7, Dom 7, Dom 9, Min 7 b5, Dim 7,
Aug, Quartal, Trichord`

(https://native-instruments.com/ni-tech-manuals/kontrol-s-mk3-manual/en/scales ;
 https://www.manualslib.com/manual/1217134/Native-Instruments-Maschine-Jam.html?page=91)

### 5.5 How scale/chord constrain the pads/keyboard

- In **Mapped** Key Mode, the 16 pads (or keyboard keys) are remapped so each pad is the *next
  in-scale degree* from the root — out-of-scale notes are simply unreachable, guaranteeing
  in-key playing. (https://native-instruments.com/ni-tech-manuals/kontrol-s-mk3-manual/en/scales)
- **Guide** keeps the chromatic layout but flags out-of-scale notes.
- With **Chord** active, each single pad press fires a whole chord (Harmonizer = scale-aware;
  Chord Set / Chromatic types = fixed shapes).
- Scale + Chord + Arp **stack**: hold one pad → it becomes a chord (Chord) → which is arpeggiated
  (Arp) → all notes forced in-scale (Scale). This stacking is the core of Smart Play.
  (https://djtechtools.com/2014/11/24/maschine-2-2-proves-pad-playability-matters/ ;
   https://cdm.link/2014/11/maschine-2-2-proves-pad-playability-matters/)

---

## 6. BUILD SPLIT FOR BECKY — v1-minimal vs later

becky already has deterministic groove/quantize/swing math in `internal/drumcmd` (quantize,
swing), `internal/music`, and `dawmodel.DrumGrid`. Map this research onto a v1 and a later tier.

### v1-MINIMAL (pure-Go, deterministic, no model/audio — build now)

1. **Swing engine (MPC/Linn).** Implement swing as: delay every even-numbered subdivision by
   `(ratio − 0.5) · windowsize`, ratio in [0.50, 0.75]. Expose BOTH conventions but store one
   canonical (recommend MPC ratio: 50 %=straight, 66 %=triplet). Cite §1.3 formula. becky-drum
   already has swing/quantize math — align it to this exact definition.
2. **Cycle parameter.** Let swing target a chosen subdivision (1/8, 1/16) — §1.1.
3. **Additive 3-level swing** (sound/group/master sum) — §1.2. Cheap and high-value for
   matching Maschine output.
4. **Quantize: full + iterative half + strength `s`.** `new = pos + s·(nearest−pos)`; half =
   0.5, repeatable — §2.1. becky-drum's "tighten to the grid" should expose strength.
5. **Grid value table.** 1Bar,1/2,1/4,1/8,1/16,1/32,1/64,1/128 + triplets + Off, default 1/16 — §2.5.
6. **Double-note de-dup on quantize** — §2.4. Trivial, prevents stacked-note artifacts.
7. **Scale engine (Mapped + Guide).** Ship the **full 8-bank × 15 scale table** (§5.3) as a
   JSON data file with interval sets; implement root-note transpose + Mapped remap so becky can
   force any melody/pattern in-key. This is pure data + interval math — ideal cloud deliverable.
8. **Chord engine.** Harmonizer (scale-aware degree stacks) + the Chromatic fixed-interval chord
   table + Chord Set Maj/Min 1–8 — §5.4. Pure interval math.
9. **Arp as a deterministic note generator.** Type (Up/Down/Up&Down/OrderPlayed/Chord), Rate
   table (§3.3), Octaves 1–8, Gate, Sequence 1–8, Swing, Repeat/Offset/Inversion — §3.3–3.4.
   This is exactly becky-drum's "humanize/fill/variation" territory and is offline-friendly.
10. **Fixed Velocity + 16-Velocities** as input/generation modes — §4. Trivial.

### LATER / Phase-2 (needs audio, GPU, or taste)

- Real-time **non-destructive swing at playback** in the audio engine (v1 can bake offset into
  MIDI deterministically instead).
- **Note Repeat live capture** from a hardware pad / live input (v1 = offline pattern generation).
- **Input quantization / quantize-while-playing** (§2.3) — only meaningful with a live record
  loop; defer until becky-daw-engine has live recording.
- Polyphonic-aftertouch **Dynamic** scaling (§3.4) — needs real expressive input.
- Model-assisted groove ("make it feel like Dilla") layered ON TOP of the deterministic Linn
  core — the 5 %-taste boundary, local-model only.

### becky design notes / gotchas

- **Pick ONE swing convention and document it** — the Maschine-Amount (0 %=straight) vs
  MPC-ratio (50 %=straight) confusion (§1.4) will bite users otherwise.
- The scale/chord tables are **the same NI engine** as Kontrol; you can lift the full table once
  and reuse across becky-compose/daw/drum/canvas. Store as one `scales.json` + `chords.json`.
- Maschine swing is **additive across 3 levels** — becky's per-track + per-bus + master model
  maps cleanly onto Sound/Group/Master.

---

## 7. SOURCES (all consulted)

- Maschine Software Manual — Playing on the controller (Groove/Swing: Amount, Cycle, Invert; additive hierarchy): https://native-instruments.com/ni-tech-manuals/maschine-software-manual/en/playing-on-the-controller
- Maschine Software Manual — Working with Patterns and Clips (Quantize full/50 %, input quantization, double-note removal, Step Grid values): https://native-instruments.com/ni-tech-manuals/maschine-software-manual/en/working-with-patterns-and-clips
- Kontrol S-Series MK3 Manual — Scales (full scale banks/types, Key Mode, Root Note, chord types): https://native-instruments.com/ni-tech-manuals/kontrol-s-mk3-manual/en/scales
- Kontrol S-Series MK3 Manual — Arpeggiator (Type, Rate table, Octaves, Gate, Swing, Sequence, Repeat/Offset/Inversion): https://native-instruments.com/ni-tech-manuals/kontrol-s-mk3-manual/en/arpeggiator
- Maschine+ Manual — Recording Patterns (Note Repeat/Arp rates, Octaves, Gate, Sequence, Dynamic, Fixed Velocity): https://www.native-instruments.com/ni-tech-manuals/maschine-plus-manual/en/recording-patterns
- Maschine MK3 Manual — Playing on the controller (Fixed Velocity, 16 Velocities): https://www.native-instruments.com/ni-tech-manuals/maschine-mk3-manual/en/playing-on-the-controller
- Roger Linn interview — Attack Magazine (swing algorithm: delay 2nd 16th; 50 %=straight, 66 %=triplet): https://www.attackmagazine.com/features/interview/roger-linn-swing-groove-magic-mpc-timing/
- "About MPC Swing" (palsen) — ratio math detail: https://www.tumblr.com/palsen/182157488304/about-mpc-swing
- ADSR — Introduction to Maschine Quantization (Q-strength 100 %/50 % gap math): https://www.adsrsounds.com/maschine-tutorials/maschine-quantization/
- ADSR — Secrets of Maschine Swing: https://www.adsrsounds.com/maschine-tutorials/maschine-swing/
- ADSR — Grid, quantize and groove: https://www.adsrsounds.com/maschine-tutorials/use-grid-quantize-groove-maschine/
- ADSR — Velocity Sensitivity & Fixed Velocity: https://www.adsrsounds.com/maschine-tutorials/maschine-tutorial-velocity-sensitivity-fixed-velocity/
- Maschine Tutorials — Swing master/group/sound is additive: https://maschinetutorials.com/how-to-apply-swing-to-the-master-group-and-individual-sounds-in-maschine/
- Maschine Tutorials — Step/Performance/Pattern grid settings: https://maschinetutorials.com/maschine-2-0-understanding-using-the-step-performance-and-pattern-grid-settings/
- Maschine Tutorials — 16-level velocity pad mode: https://maschinetutorials.com/maschine-mikro-how-to-enable-16-level-velocity-pad-mode/
- Maschine Tutorials — Arp Mode (2.2): https://maschinetutorials.com/maschine-2-2-using-the-new-arp-mode-feature/
- DJ TechTools — Maschine 2.2 Update: Scales, Chords (2.2 history): https://djtechtools.com/2014/11/24/maschine-2-2-update-scales-chords-touch-knobs-and-more/
- CDM — Maschine 2.2 playability (scale+chord+arp stacking): https://cdm.link/2014/11/maschine-2-2-proves-pad-playability-matters/
- Maschine Jam Manual (ManualsLib) — Chord Mode / Chord Type / Chromatic Harmonizer (p.90–91): https://www.manualslib.com/manual/1217134/Native-Instruments-Maschine-Jam.html?page=90
