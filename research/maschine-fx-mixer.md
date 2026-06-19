# Maschine 2 — Effects, Mixer & Signal Routing (build-target research)

Research brief for a becky drum-machine mixer/FX engine. Goal: capture exactly what NI
Maschine 2 does for **effects, mixer architecture, signal routing, and sidechain**, so we can
clone the high-value parts and explicitly defer the rest. Every claim is cited to a URL.

**Primary source:** the official Native Instruments *MASCHINE Reference Manual* (Effect
Reference is chapter 12; Audio Routing is chapter 8; Using Effects / Send Effects is chapter
11). NI's CDN/Akamai blocks datacenter IPs (403 / "Access Denied"), so the manual text below
was extracted from the NI-authored PDF mirrored at mpcstuff and corroborated against NI's
online tech-manual and NI Support. Page numbers are the manual's own page numbers.

Authoritative sources used:
- NI Reference Manual, "Effect Reference" (online): https://www.native-instruments.com/ni-tech-manuals/maschine-mk3-manual/en/effect-reference
- NI Reference Manual, "Managing Sounds, Groups, and your Project" (signal flow): https://native-instruments.com/ni-tech-manuals/maschine-software-manual/en/managing-sounds,-groups,-and-your-project
- NI Reference Manual, "Controlling your mix" (Mixer/Mix view): https://www.native-instruments.com/ni-tech-manuals/maschine-software-manual/en/controlling-your-mix
- NI Reference Manual PDF (NI-authored, mirrored): https://www.mpcstuff.com/content/MPC_Manuals/NI_MASCHINE_V2.0_MK1_Manual.pdf
- NI Support, "Setting Up MASCHINE 2 for Internal Side-Chaining": https://support.native-instruments.com/hc/en-us/articles/209582449-Setting-Up-MASCHINE-2-for-Internal-Side-Chaining
- NI Maschine "Software Updates" (2.3 added Reverb Room/Hall, Plate Reverb, Cabinet, Analog Distortion): https://www.native-instruments.com/en/products/maschine/production-systems/maschine/software-updates/
- DJ TechTools, "Maschine 2.3 Out Now" (effect additions): https://djtechtools.com/amp/2015/05/18/maschine-2-3-out-now-free-komplete-select-instruments
- NI Reference Manual, "Using Performance Effects" (Perform FX): https://www.native-instruments.com/ni-tech-manuals/maschine-plus-manual/en/using-performance-effects
- ManualsLib mirror of the NI Reference Manual (Beat Delay parameters): https://www.manualslib.com/manual/703081/Native-Instruments-Maschine.html?page=151
- Maschine Tutorials, "Maschine 2.0 audio routing, fx routing, and sidechaining" (aux vs direct routing): https://maschinetutorials.com/maschine-2-0-audio-routing-fx-routing-and-sidechaining/

---

## 1. The full effect catalog

Maschine ships **"more than 20 different Effect Plug-ins that can be quickly applied to Sounds,
Groups and the Master, all as insert effects"** (Reference Manual, ch.12, p.500 — PDF mirror:
https://www.mpcstuff.com/content/MPC_Manuals/NI_MASCHINE_V2.0_MK1_Manual.pdf). The manual
groups them into six categories: **Dynamics, Filtering, Modulation, Spatial & Reverb, Delays,
Distortion** (p.500, same source). A separate **Performance FX (Perform FX)** set exists for
the hardware Smart Strip / live performance (NI "Using Performance Effects":
https://www.native-instruments.com/ni-tech-manuals/maschine-plus-manual/en/using-performance-effects).

> History note: the **Reverb Room, Reverb Hall, Plate Reverb, Cabinet, and Analog Distortion
> (Saturator) modes** were added in **Maschine 2.3 (May 2015)**, which is why an older PDF lists
> a shorter reverb/distortion set. Source: https://djtechtools.com/amp/2015/05/18/maschine-2-3-out-now-free-komplete-select-instruments
> and NI software-updates page: https://www.native-instruments.com/en/products/maschine/production-systems/maschine/software-updates/

All per-effect parameters below are transcribed from the NI Reference Manual Effect Reference
chapter (ch.12, pp.501–548) via the mirrored PDF:
https://www.mpcstuff.com/content/MPC_Manuals/NI_MASCHINE_V2.0_MK1_Manual.pdf
(cross-checked with https://www.native-instruments.com/ni-tech-manuals/maschine-mk3-manual/en/effect-reference).

### 1a. Dynamics (ch.12.1, pp.501–514)

| Effect | What it does | Key parameters |
|---|---|---|
| **Compressor** | Classic compressor; "fatten up your drums or control signals with a very wide dynamic range." Two modes: **Classic** (cleaner/precise) and **Feedback** (vintage feel). **Has a Side-Chain Input page.** Plug-in Strip adds input/output level meters + a **GR (gain-reduction) meter**. | MODE: Mode (Classic/Feedback). DEPTH: Threshold, Knee, Amount (= ratio). TIME: Attack, Release. OUTPUT: Gain (make-up). |
| **Gate** | Cuts any signal below threshold; "rhythmically chop the signal and make it stutter or sound staccato." **Has a Side-Chain Input page.** | DEPTH: Threshold. TIME: Attack, Hold, Release. OUTPUT: Mix. |
| **Transient Master** | Emphasize/attenuate transients by reshaping attack & sustain envelopes — **no threshold**, affects all of the signal. | DEPTH: Input Gain, Attack, Sustain, Limit (hard output limiter on/off). |
| **Limiter** | Keeps level below 0 dB (anti-clip) and can raise perceived loudness by lowering threshold. "Recommended to place in a Master Plug-in slot." Introduces small latency. **Has a Side-Chain Input page.** | DEPTH: Threshold. (Plug-in Strip adds an input level meter.) |
| **Maximizer** | Reduces dynamics to make the whole sound louder (loudness-focused vs the Limiter). **Has a Side-Chain Input page.** | DEPTH: Amount, Curve (knee), Turbo (applies algorithm twice). |

(Compressor pp.501–504; Gate pp.504–507; Transient Master pp.507–509; Limiter pp.509–512;
Maximizer pp.512–514. Source: PDF mirror above.)

### 1b. Filtering (ch.12.2, pp.515–520)

| Effect | What it does | Key parameters |
|---|---|---|
| **EQ** | 4-band EQ to cut/boost selective frequencies (also usable as DJ cut/boost). Parameters spread over two pages. | LOW: Freq (20 Hz–8 kHz), Gain. LOW-MID: Freq (40 Hz–16 kHz), Gain, Width. HIGH-MID: Freq (40 Hz–16 kHz), Gain, Width. HIGH: Freq (50 Hz–20 kHz), Gain. OUTPUT: Gain (overall). |
| **Filter** | Modulatable multimode filter; emulate synth filters, create filter sweeps. **Has a Side-Chain Input page.** | TYPE: Mode (LP / BP / HP / Notch). FREQ: Cutoff, Resonance (not in Notch). MOD: Amount, Source (LFO / LFO Sync / Envelope) → Speed, LFO Shape, Phase (LFO); or Decay, Smooth, Shape (Envelope). |

(EQ pp.515–517; Filter pp.517–520. Source: PDF mirror above; EQ band ranges quoted verbatim.)

### 1c. Modulation (ch.12.3, pp.520–527)

| Effect | What it does | Key parameters |
|---|---|---|
| **Chorus** | Thickens signals / adds stereo by detuning a split copy. | MOD: Rate, Amount. OUTPUT: Mix. |
| **Flanger** | Classic flanger w/ LFO + envelope mod & feedback; tempo-syncable. | MAIN: Frequency, Feedback, Invert. MOD: Amount, Source (LFO / LFO Sync / Envelope), Speed, Shape, Stereo. OUTPUT: Mix. |
| **FM** | FM-synthesis modulation of the signal; adds gritty texture. | FREQ: Rate, Split (crossover to highs). DEPTH: Contour, Amount. |
| **Freq Shifter** | Shifts frequencies by a set amount (pitch-shifter-like at highs, chorus-like at lows). | FREQ: Coarse, Fine. OUTPUT: Feedback, Stereo, Invert, Mix. |
| **Phaser** | Classic phaser w/ LFO + envelope mod. | MAIN: Frequency, Feedback, 8Pole (more intense). MOD: Amount, Source (LFO / LFO Sync / Envelope), Speed, Shape, Stereo. OUTPUT: Mix. |

(Chorus pp.520–521; Flanger pp.522–523; FM pp.523–524; Freq Shifter pp.524–525; Phaser
pp.526–527. Source: PDF mirror above.)

### 1d. Spatial & Reverb (ch.12.4, pp.528–534)

| Effect | What it does | Key parameters |
|---|---|---|
| **Ice** | Cold/metallic reverb with a bank of self-oscillating filters. | ROOM: Color, Ice, Size. OUTPUT: Mix. |
| **Metaverb** | Synthetic-sounding reverb, good for melodic content. | ROOM: Size. EQ: Low, High. POSITION: Pan (dry). OUTPUT: Mix. |
| **Reflex** | Resonating reverb; tight rooms at moderate settings, metallic textures at extremes. | ROOM: Color, Smooth, Size. OUTPUT: Mix. |
| **Reverb** | The general-purpose, natural reverb; great on drums. | ROOM: Room (General/Bright/Guitar/Shatter), Size. EQ: Low, High. POSITION: Pan (dry), Stereo. OUTPUT: Freeze (mute dry + trap tail), Mix. |
| **Reverb Room / Reverb Hall** | Algorithmic room and hall modes (added in 2.3). | (Room/Hall variants of the Reverb family.) |
| **Plate Reverb** | Emulates a plate reverb; vintage metallic; great on vocals & snares (added 2.3). | MAIN: Pre Delay, Decay. EQ: Low Shelf, High Damp. OUTPUT: Mix. |

(Ice pp.528–529; Metaverb pp.529–530; Reflex pp.530–531; Reverb pp.532–533; Plate Reverb
pp.533–534. Reverb Room/Hall + Plate added in 2.3:
https://djtechtools.com/amp/2015/05/18/maschine-2-3-out-now-free-komplete-select-instruments.)

### 1e. Delays (ch.12.5, pp.535–542)

| Effect | What it does | Key parameters |
|---|---|---|
| **Beat Delay** | Tempo-synced delay; rhythmic. Two pages. | DELAY: Time (note values, ½–16 units), Offset, Feedback, Crossover (rhythmic pan of feedback), Color, Split. OUTPUT: Stereo (−100%..+100%), Mix. UNIT page: Unit (note unit for Time/Offset). |
| **Grain Delay** | Granular cloud delay; ambient textures. Two pages. | GRAIN: Pitch, Size, Jitter, Reverse. CLOUD: Space, Density, Mod, Mix. OUTPUT: Stereo (0–100%). |
| **Grain Stretch** | Granular time/pitch manipulation of incoming audio (buffers 32×1/16). | MASTER: On. TIME: Stretch (50% = half speed), Loop. PITCH: Pitch, Link, Size. OUTPUT: Mix. |
| **Resochord** | Bank of 6 individually-tuned comb filters; prints harmonic content onto input (best on non-melodic/drums). | PITCH: Mode (Chord/String); Spread (String); Style + Chord (Chord); Tune. COLOR: Brightness, Feedback, Decay. OUTPUT: Mix. |

(Beat Delay pp.535–537 — also corroborated at
https://www.manualslib.com/manual/703081/Native-Instruments-Maschine.html?page=151; Grain
Delay pp.537–539; Grain Stretch pp.539–540; Resochord pp.541–542. Source: PDF mirror above.)

### 1f. Distortion (ch.12.6, pp.543–548)

| Effect | What it does | Key parameters |
|---|---|---|
| **Distortion** | Overdrive/fuzz with feedback + modulation, like a guitar stomp-box. | MAIN: Drive, Color, Feedback, Tone, Tone Mod. OUTPUT: Gate (kills feedback loops), Release, Mix. |
| **Lofi** | Bit-depth + sample-rate reduction; vintage→harsh digital. | RESAMPLE: SR (44.1 kHz → 99.5 Hz). BITCRUSH: Bits, Smooth, Stereo. OUTPUT: Mix. |
| **Saturator** | Flexible saturation, 3 modes. **Classic:** Mode, Input, Contour, Drive. **Tape:** Mode, Input, Contour (HF roll-off freq), Drive (LF boost/cut). **Tube:** Mode, Charge (LF-dependent negative feedback), Overload (LF boost), Drive, EQ: Bypass/Bass/Treble, OUTPUT: Gain. | (see cell) |
| **Cabinet / Analog Distortion** | Guitar/bass cabinet emulation + warm mid-enhancing tube-amp overdrive (added 2.3). | (Cabinet model + Analog mode of the Saturator/distortion family.) |

(Distortion pp.543–544; Lofi pp.544–545; Saturator pp.545–548. Cabinet + Analog added in 2.3:
https://djtechtools.com/amp/2015/05/18/maschine-2-3-out-now-free-komplete-select-instruments.)

### 1g. Performance FX (Perform FX — Smart Strip / live)

A distinct set designed for live, one-finger performance via the controller's Smart Strip.
Source: NI "Using Performance Effects"
(https://www.native-instruments.com/ni-tech-manuals/maschine-plus-manual/en/using-performance-effects)
and NI Maschine sound-details/software pages.

| Perform FX | What it does |
|---|---|
| **Filter** | Analog-modeled LP/BP/HP filter with saturation; resonance can self-oscillate. |
| **Flanger** | Comb-filter that ranges from flanger/phaser to creative-delay territory. |
| **Burst Echo** | Warm, characterful dub-style echo; also extreme sound design. |
| **Reso Echo** | Psychedelic echo that tightens into a punchy resonator. |
| **Ring** | Bank of ring modulators (bell-like) + a plate reverb to ring out picked notes. |
| **Stutter** | Beat-mangling glitch/fill effect for drum patterns. |
| **Tremolo** | Tremolo/vibrato for on-the-fly expression and movement. |
| **Scratcher** | Turntable motion / "brake" + scratch effect, vinyl-style. |

(Descriptions: NI sound-details + the Perform FX manual page above.)

---

## 2. The exact signal-flow model (Sound → Group → Master)

Three fixed tiers. Verbatim from the Reference Manual (corroborated across the online
"Managing Sounds, Groups, and your Project" page and ch.8 of the PDF):

> "The channels of the **16 Sounds in a Group are mixed together and sent to the Group
> channel**, where their sum will be processed by the Group's Plug-ins, if any. Similarly,
> the channels of **all Groups in your Project are mixed together and sent to the Master
> channel**, where their sum will be processed by the Master's Plug-ins, if any."
> — https://native-instruments.com/ni-tech-manuals/maschine-software-manual/en/managing-sounds,-groups,-and-your-project

```
            [Sound 1]──insert FX chain──┐
 GROUP A    [Sound 2]──insert FX chain──┤
 (16 slots) [ ...   ]                   ├──► GROUP A channel ──Group insert FX──┐
            [Sound 16]─insert FX chain──┘                                       │
                                                                                │
 GROUP B  ... 16 Sounds ... ──► GROUP B channel ──Group insert FX──────────────┤
   ...                                                                          │
 GROUP H  ... 16 Sounds ... ──► GROUP H channel ──Group insert FX──────────────┤
                                                                                ▼
                                              MASTER channel ──Master insert FX──► audio outputs (Ext. 1–16)
                                                                                └─► (Cue bus is a parallel pre-listen path)
```

Key structural facts (Reference Manual ch.8, "Audio Routing in MASCHINE", PDF pp.336, 345;
mirror: https://www.mpcstuff.com/content/MPC_Manuals/NI_MASCHINE_V2.0_MK1_Manual.pdf):

- **8 Groups (A–H per bank), 16 Sounds per Group.** Each Sound, each Group, and the Master is
  a **channel** with its own Plug-in chain, Level, Pan, and output routing (p.336).
- **Plug-ins are INSERT effects in a serial chain.** Processing order is **top-to-bottom** in
  the Plug-in List (Arrange view) and **left-to-right** in the Plug-in Strip (Mix view).
  Source: https://www.native-instruments.com/ni-tech-manuals/maschine-software-manual/en/controlling-your-mix
- **Default routing:** every Sound → its parent Group → Master → audio outputs (p.336). This is
  overridable: a Sound's `Dest.` can be None, Master, parent Group, **any other Group or any
  other Sound acting as a bussing point**, or one of 16 external stereo outs (Ext. 1–16). A
  Group's `Dest.` can be None, Master, another Group/Sound bussing point, or Ext. 1–16 (p.337).
- **Master output** goes only to Ext. 1–16; its Level == the Header Master Volume slider
  (p.346).
- **Plug-in slots are bypassable** ("mute" a slot — audio passes straight to the next slot),
  and Plug-ins can be **moved/copied across slots, Sounds, Groups, and levels** (ch.6.1.5–6.1.7,
  pp.208–212). This is how you reorder an FX chain.

### Mixer (Mix view) channel strip
Source: https://www.native-instruments.com/ni-tech-manuals/maschine-software-manual/en/controlling-your-mix
and PDF ch.8.

- In **Mix view**, Sounds, Groups, and the Master are shown as **channel strips**. Each strip
  has: a **level fader**, a **balance/pan control above the fader**, a **headphone/Cue button
  below the fader**, an **output level meter** (with the `Dest.` selector under it), and
  Mute/Solo. The **Master/Cue share one strip at the far right** (PDF pp.340, 347–349).
- Toggle rows on the strip via the Mixer's left-edge buttons: **IO** (input/output + MIDI),
  **AUX** (the two aux sends), and the **Plug-in Strip** (mini FX UIs, left-to-right order).
- **Mute & Solo** exist at Sound and Group level; an optional **Audio Mute** (Sounds only) also
  mutes audio tails, not just events (PDF p.337).
- **Level/Pan shortcuts:** the little knob pair on each Sound/Group in the lists (left = level,
  right = pan) duplicates the channel Level/Pan (PDF p.338).

---

## 3. How sidechain is wired

Maschine's sidechain is **internal-only routing into specific effects' secondary input** — no
external hardware sidechain bus. Sources: NI Support
(https://support.native-instruments.com/hc/en-us/articles/209582449-Setting-Up-MASCHINE-2-for-Internal-Side-Chaining)
and the Reference Manual ch.12 Side-Chain Input pages + ch.6.1.6 "Using Side-Chain" (PDF
pp.210, 503).

**Which effects accept a sidechain input** (each has a dedicated **Side-Chain Input page**,
"if this effect is used in a Sound or a Group"): **Compressor, Gate, Limiter, Maximizer, and
Filter.** (PDF pp.503, 506, 510, 513, 519.) NI Support confirms the same five internal effects,
plus AU/VST plug-ins that expose a sidechain input
(https://support.native-instruments.com/hc/en-us/articles/209582449-Setting-Up-MASCHINE-2-for-Internal-Side-Chaining).
Note the **Master cannot host a sidechained instance** — the Side-Chain page only appears on a
Sound or Group.

**The Side-Chain Input page parameters** (identical across all five effects — PDF p.503):

- **INPUT → Source** — selects the sidechain signal. Options: **None** (default, disabled),
  **the output of any other Sound, or any other Group.** Labeled `[Group name]: [Sound name]`
  (e.g. `Drums: Kick`) or `[Group letter+number]:S[Sound number]` (e.g. `A1:S4`).
- **INPUT → Gain** — input level of the sidechain signal feeding the detector.
- **FILTER → Filter** — enable a band filter on the sidechain (key-listen / frequency-selective
  triggering).
- **FILTER → Center Freq** — center frequency of that filter.
- **FILTER → Width** — bandwidth of that filter.

**Setup procedure** (the classic kick-ducks-bass; NI Support article above + PDF ch.11.1.3):
1. Add a **Compressor** (or Gate/Limiter/Maximizer/Filter) onto the **target** Sound/Group
   (e.g. the bass) via the `+` in the Plug-in List.
2. Open the effect's **Side-Chain Input** page/tab.
3. Set **Source** to the trigger channel (e.g. `Drums: Kick`).
4. Tune Threshold/Amount/Attack/Release for the pump; optionally enable the sidechain Filter to
   key off a frequency band. The Plug-in-Strip **GR meter** shows the gain reduction pumping in
   time with the trigger (PDF p.504).

> Important distinction for our engine: the sidechain `Source` is the **other channel's output**
> tapped as a control/detector signal — it does **not** re-route that channel's audio. This is
> separate from aux sends (§4).

---

## 4. Buses, sends/aux, and metering

**Inserts are first-class; "sends" are emulated, not a true aux-bus architecture.** Maschine has
no dedicated aux-return tracks — instead:

- **Insert FX:** the normal model — drop effects into a channel's serial Plug-in chain at Sound,
  Group, or Master level (ch.12 intro, PDF p.500: "all as insert effects").
- **Send effects are built by convention** (ch.11.3, PDF pp.490–496): load an effect into the
  **first Plug-in slot of an otherwise-empty Sound or Group**; Maschine then auto-configures that
  channel's input to **receive signal from other channels**, turning it into a **bussing point**
  / send-return. You feed it via each source channel's **two auxiliary outputs (Aux 1 / Aux 2)**.
- **Aux outputs** live on the Output properties **Aux page** of every Sound and Group (PDF p.342):
  - **Dest.** — same target list as the main output (None default; a Group, a bussing-point Sound,
    or Ext. 1–16).
  - **Level** — send amount.
  - **Order** — **Pre** or **Post** (default Post = after the channel's main Level/Pan).
  - **The Master has no Aux page** (PDF p.341) — you cannot send the Master to a send effect.
- A community/tutorial gloss-of-distinction worth noting for our naming: an **aux send mixes the
  wet effect *in addition to* the dry signal, whereas routing a channel's main `Dest.` directly
  to an effect-Sound sends it *entirely through* that effect** (i.e. direct = 100% wet bus). Source:
  https://maschinetutorials.com/maschine-2-0-audio-routing-fx-routing-and-sidechaining/
- **Send-effect rules** (PDF p.496): can't send Master to a send; can't feed a send's output to
  itself or a Group to its own Sound; **can** chain sends and reuse one reverb across many
  channels to save CPU.
- **Cue bus:** a parallel pre-listen bus (headphones). Enabling **Cue** on a channel routes its
  main output to the Cue bus (and mutes its Aux 1/2) — used for monitoring, not mixing (PDF
  pp.337, 346).

**Metering** (PDF pp.340, 504, 512; mix page above): each channel strip has an **output level
meter**; the **Compressor** strip adds **input + output meters and a gain-reduction (GR) meter**;
the **Gate/Limiter** strips add an **input meter** alongside the Threshold fader so you can see
which parts cross threshold.

**Macro Controls / modulation of FX params** (PDF ch.8.2–8.3, pp.356–360): almost any FX
parameter **driven by a knob or button** is automatable (via recorded **Modulation**, host/MIDI
**Automation**, or assigned to **Macro Controls** as parameter aliases). Selector-type params
(modes, filter type) are generally **not** automatable. Relevant if becky wants knob-automation
of compressor threshold / filter cutoff etc.

---

## 5. v1-minimal vs later split for a becky drum machine

Design principle: clone the **deterministic, high-impact, math-cheap** parts first; defer the
exotic granular/modeled effects. Mirror Maschine's load-bearing structure — **a fixed
Sound→Group→Master bus tree with serial insert FX and an internal sidechain tap** — because that
is what makes mixes predictable and "obvious which thing broke."

### v1 — minimal (build first)
Routing / mixer:
- **Sound → Group → Master** three-tier sum, fixed (matches Maschine; cheap; deterministic).
- Per-channel **Gain (Level) + Pan + Mute + Solo**, and an **output level meter** per channel.
- **Serial insert-FX chain** per channel (top-to-bottom / left-to-right order, slot bypass,
  reorder) — this single abstraction covers everything below.

Effects (the cheap, high-impact set Jordan named):
- **Gain/Trim** (per channel — free).
- **Compressor** with Threshold / Ratio (Amount) / Attack / Release / Knee / Make-up Gain
  **+ internal Sidechain Input** (Source = any other Sound/Group, Gain, optional band Filter
  with Center Freq + Width) **+ a GR meter.** This is the centerpiece: kick→bass ducking.
- **EQ** — start with a 3–4 band parametric (Low/Low-Mid/High-Mid/High: Freq + Gain, Width on
  the mids). Maschine's exact band ranges (20 Hz–8 kHz / 40 Hz–16 kHz / 50 Hz–20 kHz) are a
  good copy target.
- **Delay** — tempo-synced **Beat Delay** style (Time in note values, Feedback, Color/tone,
  Stereo, Mix). Cheap and rhythmically essential for drums.
- **Reverb** — one general algorithmic reverb (Size, Low/High EQ, Mix; Pre-Delay/Decay). One
  shared reverb usable as a **send** (see below) to save CPU.

Send/aux (minimal):
- **One Aux send per channel → a return bus** (Level + Pre/Post) so a single reverb/delay can be
  shared — mirrors Maschine's "send effect" convention with far less ceremony than 16 bussing
  Sounds.

### Later (defer — explicitly out of v1)
- **Limiter / Maximizer / Transient Master** (Master-bus loudness; nice but not core to a beat).
- **Gate** as an FX (Mute/velocity already covers most drum-gating needs).
- **Modulation FX** (Chorus, Flanger, Phaser, FM, Freq Shifter) — LFO/envelope infra needed.
- **Distortion family** (Distortion, Lofi/bitcrush, Saturator Tape/Tube/Analog, Cabinet) — Lofi
  bitcrush is the cheapest of these and a reasonable early-extra; the modeled tube/tape/cabinet
  algos are real DSP work.
- **Granular / exotic** (Grain Delay, Grain Stretch, Resochord, Ice, Reflex, Metaverb) — high
  DSP cost, niche payoff.
- **Performance FX** (Stutter, Scratcher, Tremolo, Ring, Burst/Reso Echo) — these are a
  live-performance/Smart-Strip layer; only relevant once becky-canvas has a live-gesture surface.
- **Full multi-output routing** (16 external stereo outs, arbitrary Sound-as-bussing-point
  graphs), **Cue/pre-listen bus**, and **2× aux per channel with arbitrary destinations** —
  start with the single fixed tree + one return.
- **Parameter automation / Macro Controls / modulation lanes** for FX params — valuable for
  becky's "learn my moves" loop, but layer it after the static graph works.

### Why this split
The compressor-with-sidechain + a clean Sound→Group→Master bus tree + EQ/delay/reverb is the
80/20 of a drum-machine mixer: it's what produces "pumping" modern drums and a balanced bus,
it's all closed-form deterministic DSP (no models, fixed output), and each effect is an
independent insert so a failure is localized — consistent with becky's single-tool, degrade-
never-crash invariants.
