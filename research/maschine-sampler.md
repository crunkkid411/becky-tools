# Maschine 2 SOUND / SAMPLER engine — deep research (build target for `internal/sampler`)

Exhaustive, source-cited breakdown of Native Instruments **Maschine 2's** Sound model
and built-in **Sampler** plug-in, written as the v1/later build contract for becky's
`internal/sampler` Sound/Layer/Variant model specced in `SPEC-BECKY-DRUM.md` §1, and as
the deep companion to `SPEC-MASCHINE-CLONE.md` §2 (which is shallow on the Sampler
plug-in internals — this file fills that gap).

Every functional claim cites a URL. The single most authoritative source is the **official
NI MASCHINE 2 software manual PDF**, from which the exact parameter names, value ranges,
and page numbers below were extracted verbatim (cited as `[NI MASCHINE 2 Manual, p.N]`).
Primary copy used: the NI-hosted PDF
(https://www.native-instruments.com/fileadmin/ni_media/downloads/manuals/maschine_27810/MASCHINE_2_Manual_English_2_7_10.pdf);
the same chapter text is mirrored at
https://www.strumentimusicali.net/manuali/NATIVEINSTRUMENTS_MASCHINEMKII_ENG.pdf and on
ManualsLib (https://www.manualslib.com/manual/1258176/Native-Instruments-Maschine-Studio.html).
NI's online tech-manual ("Sampling and sample mapping", "Working with Plug-ins") and ADSR /
MaschineTutorials corroborate.

> **Scope guard.** This is the *forensic* truth of how Maschine's Sampler works — not a
> wish-list. Where Maschine does NOT have a feature (e.g. per-region round-robin, true
> velocity *crossfade*, a resonant EQ-band sweep) it is stated plainly so the becky model
> isn't built around a false parity claim.

---

## 0. The big picture: a Sound is a 4-slot plug-in chain

A **Project** holds up to 8 **Groups (A–H)**; each Group holds **16 Sounds**, one per pad.
([Managing Sounds, Groups, and your Project — NI](https://www.native-instruments.com/ni-tech-manuals/maschine-mk3-manual/en/managing-sounds,-groups,-and-your-project))

A **Sound is a chain of up to four Module slots** (the "plugin-slot chain" model):
- **Module 1** hosts the *source*: a **Sampler**, an **Input** (bussing point for
  external/internal audio), a **MIDI Out** module, or a **VST/AU/internal instrument
  plug-in**. The Sampler can ONLY live in Module 1. Adding a sample to an empty Sound
  auto-sets Module 1's source to Sampler. `[NI MASCHINE 2 Manual, p.58–60]`
- **Modules 2, 3, 4** host *effects only* (internal Maschine FX or VST/AU FX), in series.
  `[NI MASCHINE 2 Manual, p.58–59]`
- Each Sound also has an **Output (OUT) tab** (Main out + Aux 1/2 sends) that is part of
  the Sound, not a module. `[NI MASCHINE 2 Manual, p.77–78]`

So the data model is: **Sound = { source-module (Sampler config) + insert-FX[0..3] + output/aux routing }.**
([Working with Plug-ins — NI](https://www.native-instruments.com/ni-tech-manuals/maschine-mk3-manual/en/working-with-plug-ins))

The becky equivalent already exists in two pieces: the per-pad sample voice config is the
`internal/sampler` Sound/Layer/Variant model (`SPEC-BECKY-DRUM.md` §1); the *insert-FX +
bus + send + sidechain* graph is `internal/studio` driven by `becky-wire`
(`SPEC-MASCHINE-CLONE.md` §7). This file is about the **source module = the Sampler**.

---

## 1. The Sampler plug-in — six parameter pages (the heart of this research)

The built-in Sampler's controls are organized into **6 pages** `[NI MASCHINE 2 Manual, p.62]`:

1. Voice Settings, Pitchbend, Engine
2. Pitch/Gate + Amplitude Envelope
3. FX + Filter
4. Modulation Envelope + Destination
5. LFO + Destination
6. Velocity Destination + Modwheel Destination

> Important parity note: **these Sampler Parameters are NOT available for VST/AU plug-ins.**
> They are the native Sampler's own engine. `[NI MASCHINE 2 Manual, p.61]`

### Page 1 — Voice Settings & Engine `[NI MASCHINE 2 Manual, p.63–64]`

| Parameter | Function | Values |
|---|---|---|
| **Polyphony** | Voice limit for the Sound. When reached, the **oldest** still-playing note is killed (oldest-note voice stealing — see [Voice Settings/Engine — Maschine Studio Manual, ManualsLib p.310](https://www.manualslib.com/manual/1258176/Native-Instruments-Maschine-Studio.html?page=310)). | Default **8**, min **1**, max **32** (the MK3-era manual lists up to **64**); plus a special **Legato** setting (poly forced to 1 with continuous pitch glide). `[p.63]` |
| **Choke Group** | One of **8** Choke Groups, or **Off**. Sounds sharing a Choke Group cancel each other (open/closed hi-hat; mono-synth voice). | Off, 1–8 `[p.63]` |
| **Glide** | Only active when Polyphony = **Legato**: portamento between consecutive notes/steps. | time `[p.63]` |
| **Pitchbend** | How the Sound reacts to incoming MIDI pitchbend. | range `[p.63]` |
| **Mode** (Engine) | Sampling-engine model. | **Standard** or **Vintage** `[p.63]` |
| **Model** (Engine) | If Mode = Vintage: emulate **MPC60** or **SP1200** sampler character (hip-hop crunch). | MP60 / S1200 `[p.63]` |
| **Filter** (Engine) | If Model = S1200: the SP1200-style era filter bank. | None, Low, Lo-Mid, Hi-Mid, High `[p.63]` |

### Page 2 — Pitch/Gate & Amplitude Envelope `[NI MASCHINE 2 Manual, p.64–67]`

Pitch/Gate controls:
- **Tune** — basic pitch of the sample (right = higher, left = lower). `[p.64]`
- **Start** — sample start point; **modulatable by Velocity** (page 6) and Modwheel. `[p.64]`
- **Reverse** — play the sample backwards (on/off). `[p.64]`

**Amplitude Envelope — three TYPES** (this is the crucial playback-mode selector):
- **Oneshot** — "typical vintage drum machine behavior: the sample is played in its
  entirety from beginning to end with **no envelope**." When Oneshot is on, **all the
  envelope parameters below are unavailable.** This is the drum default. `[p.65]`
- **AHD** — disables Sustain/Release, exposes **Attack, Hold, Decay**. "Fire-and-forget":
  the sound plays for a set time regardless of how long the pad is held. `[p.66]`
- **ADSR** — **Attack, Decay, Sustain, Release**; for longer/sustained samples needing
  dynamic control. Because Maschine pads are pressure/hold-sensitive, ADSR lets a pad
  behave like a held MIDI key. `[p.66]`

Envelope parameter definitions `[p.66–67]`:
- **Attack** — time to reach full volume after trigger.
- **Hold** (AHD only) — time held at max level.
- **Decay** — ADSR: time to fall to Sustain level; AHD: how fast the sound dies down.
  **Modulatable by Velocity** (page 6).
- **Sustain** (ADSR only) — level held after Decay until note-off; also controllable by
  MIDI CC 64.
- **Release** (ADSR only) — fade-out time after note-off.

### Page 3 — FX & Filter `[NI MASCHINE 2 Manual, p.67–68]`

Built-in per-Sound *quick* FX (distinct from the Module-2/3/4 insert FX):
- **Comp** — basic compressor (density).
- **Drive** — saturation amount (**modulation destination** for env/LFO).
- **SR** — sample-rate reduction (lo-fi).
- **Bits** — bit-depth reduction (digital lo-fi).

**Filter** — `Mode` menu selects the type, each exposing different controls `[p.68]`:
- **Off** — no filter.
- **LP2** — 2-pole low-pass: **Cutoff + Resonance**. Cutoff modulatable by Velocity, Mod
  Envelope, LFO, or Modwheel.
- **BP2** — 2-pole band-pass: **Cutoff** (modulatable, as above).
- **HP2** — 2-pole high-pass: **Cutoff + Resonance** (modulatable).
- **EQ** — equalizer: **Frequency, Bandwidth, Gain**.

### Page 4 — Modulation Envelope & Destination `[NI MASCHINE 2 Manual, p.68–69]`

A second envelope whose only job is to *modulate* other parameters. Its shape mirrors the
Amplitude Envelope: **ADSR or AHD** (and **only AHD if Oneshot** is selected on page 2).
Controls: Attack / Hold / Decay / Sustain / Release (same semantics as page 2).
**Destinations (pick targets):** **Pitch** (p.2), **Cutoff** (p.3), **Drive** (p.3),
**Pan** (Output p.1). `[p.69]`

### Page 5 — LFO & Destination `[NI MASCHINE 2 Manual, p.69–70]`

- **Type** — Sine, Tri, Rect, Saw, **Random**.
- **Speed** — in Hz; if **Sync** on, shows musical values.
- **Phase** — initial phase, % .
- **Sync** — lock to project tempo; values become **16/1 … 1/32**.
- **Destination** — up to **four** targets: **Pitch, Cutoff, Drive, Pan** (same four as
  the Mod Envelope). `[p.70]`

### Page 6 — Velocity Destination & Modwheel Destination `[NI MASCHINE 2 Manual, p.70–71]`

**Velocity** (built-in mod source, per destination amount):
- **Start** — velocity shifts sample start (positive = later start on harder hits; the
  classic "snappy snare transient only on hard hits" trick). `[p.71]`
- **Decay** — velocity → amplitude-envelope Decay.
- **Cutoff** — velocity → filter Cutoff (LP/HP/BP).
- **Volume** — velocity → volume (the normal use). `[p.71]`

**Modwheel** (MIDI CC1 destinations): **Start, Cutoff, LFO Depth, Pan.** `[p.71]`

---

## 2. The Sound's Output / Aux tab (routing — belongs to the Sound) `[p.77–78]`

- **Main:** Output (Master, Group, any Input-Sound, External Outputs 1–8, or None) +
  **Level** + **Pan**.
- **Aux 1 / Aux 2:** each a Destination (Master/Group/Input-Sounds/External Outs/None) +
  **Level** — i.e. **two send buses per Sound**.
- **Pre Mix 1 / Pre Mix 2** (page 2): make a send **pre-fader** (fed before Main Level/Pan).
  `[p.79]`

This is the per-Sound half of the FX/routing graph that `internal/studio`/`becky-wire`
already models; the Sampler model only needs to carry **Level, Pan, and two named send
amounts** so the wire-graph can consume them.

---

## 3. Sampling INTO Maschine (record / resample) `[p.204–205]`

The **Record page** (Sampling mode):
- **Source** — **Extern** (audio interface input) or **Intern** (resample from another
  Group or the Master output → this is "resampling"). `[p.205]`
- **Input** — if Extern: **IN 1 L, IN 1 R, IN 1 L+R**; if Intern: any Group or Master. `[p.205]`
- **Mode** — two recording-start modes `[p.205]`:
  - **Detect** — set a **Threshold**; recording starts when input exceeds it (for live
    instrumentalists/vocals). Set Threshold to **OFF** for manual Start/Stop. `[p.206]`
  - **Sync** — recording starts in sync with the sequencer (next bar if already running);
    **Length** = 1, 2, 4, 8, 16 bars, or **Free** (stop manually).
- **Start / Cancel / Delete** — Start arms; Cancel discards an in-progress take; recorded
  takes live in a **recording history** (multiple samples per slot, PREV/NEXT to pick). `[p.205–206]`

> **Honest gap (verified across the corpus):** the MK2/MK3 manuals describe **Threshold**
> and the **recording history** but document **no explicit *pre-roll / pre-record buffer*
> count-in** for the audio recorder (the count-in/metronome is for *note* recording, not
> the sampler). The task brief lists "pre-roll," but Maschine's sampler exposes it only
> indirectly: Threshold+Detect captures the attack, and Sync starts on a bar. Treat an
> explicit pre-roll buffer as a becky *enhancement*, not a Maschine parity feature.
> (Corroborate-then-conclude: absent from the NI Record-page docs and the
> [ADSR sampling tutorial](https://www.adsrsounds.com/maschine-tutorials/sampling-with-maschine/).)

---

## 4. SLICING and how slices map to the 16 pads `[p.209–211]`

The **SLICE tab** modes:
- **Split** — sample cut into **equal** slices; count = **4, 8, 16, or 32**. `[p.209]`
- **Grid** — cut to **musical values**: **4th, 8th, 16th, 32nd** notes; Tempo Auto/Manual + BPM. `[p.209]`
- **Detect** — **transient-based**; **SENS** (Sensitivity) knob — higher = more slices
  (more transients pass), lower = fewer; tune until all musically-significant onsets are
  marked. `[p.210]`
- **Manual** — hand-edit slices in **Edit mode**: per-slice **Start/End**, **Reset**,
  **Add** (adds a slice per the Mode), **Remove**. `[p.210]`

> **Zero-crossing:** the *online* NI tech-manual and tutorials note slice points snap to
> zero crossings to avoid clicks
> ([Sampling and Sample Mapping — NI](https://www.native-instruments.com/ni-tech-manuals/maschine-software-manual/en/sampling-and-sample-mapping),
> [ADSR — Sampling with Maschine 2.0](https://www.adsrsounds.com/maschine-tutorials/sampling-with-maschine/));
> the click-avoidance amplitude envelope (Attack/Decay) on the slice is in `[p.207]`.

**APPLY TO — the slice→pad / slice→note mapping (load-bearing):** `[p.211]`
- **Apply to a GROUP** → each slice is mapped to its **own Sound/pad** (pad 1, 2, 3, …),
  and the **Step Editor opens with one note per slice** → the chopped break is playable as
  a drum groove (slice-to-pads).
- **Apply to a SOUND** → all slices mapped into **one Sound**, **Piano-Roll/Keyboard
  Editor opens with one note per slice** → played chromatically/melodically (slice-to-notes).
- Plain **Apply** creates the trigger notes in the current Sound and switches to Piano
  Roll, looping in time with project tempo.

So a Group has **16 pads**; a 16-slice "Apply to Group" lays slices across pads 1–16 in
order. >16 slices overflow conceptually beyond a single Group's 16 pads (the becky model
caps a Group at 16; document the overflow rule explicitly).

---

## 5. Multi-sample ZONES — key & velocity mapping `[p.211–213]`

The **MAP tab** builds **Zones**: each Zone = one Sample + a note range + a velocity
range. **Zones may overlap**, so one note can trigger several samples (layering) or pick a
different sample by hit strength (velocity switching). `[p.211]`

Per-Zone parameters (the Mapping Editor pages):
- **Page 1 – Note Settings:** **Root** (note that plays the sample at original pitch),
  **Low** (lowest key), **High** (highest key). `[p.212]`
- **Page 2 – Velocity Settings:** **Low** / **High** velocity bounds for the Zone. `[p.212]`
- **Page 3 – Tune / Gain / Pan:** per-Zone **Tune**, **Gain** (level), **Pan**. `[p.213]`

This is exactly a multisample instrument: stack Zones with adjacent key ranges (chromatic
spread) and/or stacked velocity ranges (soft/medium/hard layers).
([Sampling and Sample Mapping — NI](https://www.native-instruments.com/ni-tech-manuals/maschine-software-manual/en/sampling-and-sample-mapping))

> **Honest gap — round-robin:** Maschine's Zone map has **no native per-region
> round-robin / sequential-sample variant** opcode (unlike SFZ `seq_*` / `lorand`/`hirand`).
> Variant cycling is therefore a becky *superset* feature (already in `SPEC-BECKY-DRUM.md`
> §1 via SFZ), NOT Maschine parity. Likewise Maschine velocity zones are **hard
> boundaries**, not crossfaded — true velocity *crossfade* is also a becky/SFZ extension.

---

## 6. EDIT page — destructive sample editing `[p.207–208, p.214–215]`

- **Start / End** of the sample (drag "S"/"E" handles or type values). `[p.214]`
- **Loop** settings: **enable Loop**, **Loop Start / End**, **Crossfade** (blend material
  near loop ends to kill clicks). The loop repeats while the note is held — used to
  sustain a tone. `[p.207, p.214]`
- **Slice/Sample amplitude envelope** (Attack/Decay) to de-click slices. `[p.207]`
- **Destructive functions** (selected RANGE): **Truncate, Normalize, Reverse, Fade In,
  Fade Out, DC Fix, Silence, Cut, Copy, Paste, Duplicate**, plus **Remove sample from
  map** / **Open containing folder**. `[p.208, p.215]`

---

## 7. The 8 Macro controls `[p.94–96]`

**Eight Macro knobs per Group** (the "one screen, no menu-diving" performance/automation
surface — directly relevant to becky's "kill the click-engineer" goal):
- Each Macro maps to **one** destination over that parameter's full range. `[p.94]`
- Targets = **any modulatable parameter of the Group's Modules (1–4) OR of any Sound
  inside the Group** (e.g. a Sound's Tune). `[p.95]`
- Macros are **bipolar, −100% … +100%** (0% centre). `[p.95]`
- Visible to the host (DAW automation) and assignable to **external MIDI CCs**. `[p.94, p.96]`

(Maschine's modulation hierarchy: per-Sound Velocity/Modwheel/Mod-Env/LFO routings
[§1 pages 4–6] are the *internal* modulators; the 8 Macros are the *performance/host* layer
on top. Maschine also has the **Perform-FX / Performance** features on hardware, but the
parameter-control surface specced here is the Macro page.)

---

## 8. Choke groups & mono at the Sound level `[p.63]`

- **Choke Group**: per-Sound, one of **8** or Off; same group = mutual cut-off
  (open/closed hat). Set on Voice Settings page 1. `[p.63]`
- **Mono behaviour** is achieved via **Polyphony = 1** (or **Legato** for mono + glide),
  also page 1. There is no separate "mono" switch — it's the Polyphony value. `[p.63]`
- **Link groups** (Sounds trigger together) exist on the controller's Sound-properties
  area but are **not** a Sampler-page parameter
  ([Playing on the controller — NI](https://www.native-instruments.com/ni-tech-manuals/maschine-mk3-manual/en/playing-on-the-controller));
  treat Link as a Group-level relation, not a Variant field.

**Per-pad pitch/tune** = each Sound's **Tune** (page 2) plus per-Zone **Tune** (map page
3), plus the engine **Pitchbend/Glide** — so a pad's pitch is `Sound.Tune + Zone.Tune (+ semitone via Base/Root key)`.

---

## 9. MUST capability list — v1-minimal vs later

### v1-minimal MUST (smallest set that is a real, Maschine-faithful sampler)
1. **Sound = source + insert-FX[0..3] + Main(Level/Pan)+2 Aux sends** data shape (the
   chain model). `[p.58–59, 77–78]`
2. **Per-pad one-shot Sampler voice**: SamplePath, **Start/End**, **Reverse**, **Tune**,
   **Gain/Level**, **Pan**. `[p.64, 77]`
3. **Amplitude Envelope TYPE = {Oneshot | AHD | ADSR}** with the correct
   exposed-parameter rules (Oneshot = none; AHD = A/H/D; ADSR = A/D/S/R). `[p.65–67]`
4. **Loop** with Start/End + **Crossfade**, plus a **LoopMode** (no-loop vs sustain-loop). `[p.207, 214]`
5. **Polyphony** (1/2/4/8/16/32, default 8) with **oldest-note voice stealing**; **Legato +
   Glide**; **Mono = Polyphony 1**. `[p.63]`
6. **Choke Group** (Off/1–8) with mutual cut-off. `[p.63]`
7. **Slicing**: Split (4/8/16/32), Grid (4th/8th/16th/32nd), **Detect + Sensitivity**,
   Manual; **Apply-to-Group = slice-to-pads** mapping across pads 1–16. `[p.209–211]`
8. **Velocity → Volume** (and the engine should support Velocity→Start/Decay/Cutoff). `[p.71]`
9. **Destructive edits**: Truncate, Normalize, Reverse, Fade In/Out, DC Fix, Silence,
   Cut/Copy/Paste, Duplicate. `[p.208, 215]`
10. **Sampling-in**: record from **Extern input** with **Threshold/Detect** + manual
    Start/Stop, and **Intern resample** from Group/Master. `[p.205]`

### Later (nice-to-have / parity-plus)
- **Multi-sample Zones** (key range Low/High + Root; velocity Low/High; per-Zone Tune/Gain/Pan;
  overlap = layering). `[p.211–213]`
- **Filter** (LP2/BP2/HP2 with Cutoff+Res; EQ Freq/BW/Gain). `[p.68]`
- **Sampler quick-FX**: Comp, Drive, **SR/Bits** lo-fi. `[p.67]`
- **Engine modes**: Vintage **MPC60 / SP1200** emulation + SP-1200 filter bank. `[p.63]`
- **Modulation Envelope** (ADSR/AHD) → Pitch/Cutoff/Drive/Pan. `[p.68–69]`
- **LFO** (Sine/Tri/Rect/Saw/Random, Hz or synced 16/1…1/32, Phase) → up to 4 of
  Pitch/Cutoff/Drive/Pan. `[p.69–70]`
- **Modwheel** → Start/Cutoff/LFO-Depth/Pan. `[p.71]`
- **8 Macro knobs** (bipolar ±100%, any Sound/Group param, host+MIDI-CC). `[p.94–96]`
- **Pitchbend** range; **Sync recording** (1/2/4/8/16 bars / Free). `[p.63, 205]`
- **Aux pre-fader (Pre Mix 1/2)** sends. `[p.79]`

---

## 10. Data-model mapping → becky `internal/sampler` (Sound/Layer/Variant), with gaps

becky's specced model (`SPEC-BECKY-DRUM.md` §1): **Pad → Sound → Layer[] → Variant[]**,
SFZ-aligned. Mapping each Maschine concept onto it:

| Maschine concept | becky field (existing/specced) | Notes / GAP |
|---|---|---|
| Sound = Module-1 source + FX 2–4 | Sound (source) + `internal/studio` FX graph | becky splits source (sampler) from FX graph (`becky-wire`). **OK.** Add a `SourceType` enum {Sampler, Input, MIDIOut, Plugin} to mirror Module-1 roles. **GAP: `internal/sampler` has no SourceType.** |
| Polyphony (1–32/64, default 8) | **GAP** — not in §1 model | Add `Polyphony int` + `VoiceSteal=oldest` (only mode Maschine has) at Sound level. |
| Legato + Glide | **GAP** | Add `Legato bool` + `Glide` (time); Legato ⇒ Polyphony 1. |
| Choke Group (Off/1–8) | `Sound.ChokeGroup` (SFZ `group`), `Chokes`/`off_by`, `ChokeMode` | becky is a SUPERSET (arbitrary off_by). Constrain UI to 8 to match. **OK.** |
| Mono | Polyphony=1 | No separate field needed. **OK.** |
| Tune (per-Sound) | `Variant.Tune` (cents) + `Transpose` (semis) | Maschine Tune is per-**Sound**; becky's is per-**Variant**. Add `Sound.Tune` summed over Variant.Tune. **GAP: no Sound-level tune.** |
| Start / End | `Variant.StartFrame` / `EndFrame` (SFZ offset/end) | **OK.** |
| Reverse | **GAP** | Add `Variant.Reverse bool` (SFZ has no direct opcode → becky-native). |
| Amplitude Env TYPE {Oneshot/AHD/ADSR} | partially: `Variant.LoopMode`/`OneShot` only covers the loop side | **GAP (important).** Add `Sound.AmpEnv { Type: Oneshot|AHD|ADSR; A,H,D,S,R }`. Oneshot ⇒ ignore A/H/D/S/R. |
| Loop + Crossfade | `Variant.LoopMode`,`LoopStart`,`LoopEnd` | **GAP:** add `Crossfade` field (Maschine loop crossfade; SFZ `loop_crossfade`). |
| Filter (LP2/BP2/HP2/EQ) | none in §1 (FX is `internal/studio`) | Decide: model the Sampler's *built-in* filter as a Sound field, or push to FX graph. **GAP — recommend a `Sound.Filter {Type,Cutoff,Res}` on the sampler** since its Cutoff is a mod destination. |
| Sampler quick-FX (Comp/Drive/SR/Bits) | none | **GAP** — `Sound.SamplerFX` or push to FX graph; Drive is a mod destination so keep at least Drive on the sampler. |
| Mod Envelope → {Pitch,Cutoff,Drive,Pan} | none | **GAP** — `Sound.ModEnv` + `Destinations[]` (Phase-2). |
| LFO → {Pitch,Cutoff,Drive,Pan} | none | **GAP** — `Sound.LFO {Type,Speed,Phase,Sync,Dest[≤4]}` (Phase-2). |
| Velocity → {Start,Decay,Cutoff,Volume} | velocity used only for Layer selection in §1 | **GAP** — add a `VelocityMod` amounts struct; v1 needs at least Velocity→Volume. |
| Modwheel → {Start,Cutoff,LFODepth,Pan} | none | **GAP** — Phase-2 `ModwheelMod`. |
| Multi-sample Zone (Root, Low/High note, Low/High vel, Tune/Gain/Pan) | **Layer** (`VelLo/VelHi`, `KeyLo/KeyHi`, root) + **Variant** (Tune/gain/pan) | **CLOSE MATCH** — a Maschine Zone ≈ a becky Layer-with-one-Variant carrying its own key/vel/tune/gain/pan. becky's `KeyLo/KeyHi`+root = Note Low/High+Root; `VelLo/VelHi` = Velocity Low/High. **OK** (becky is a superset). |
| Round-robin / random variants | `Layer.RoundRobin []Variant` (SFZ seq/rand) | **becky SUPERSET** — Maschine has none. Keep, label as enhancement. |
| Velocity crossfade | optional `xfin/xfout` (Phase-2 in §1) | **becky SUPERSET** — Maschine vel zones are hard. |
| Engine Vintage MPC60/SP1200 + filter | none | **GAP (later)** — `Sound.Engine {Mode,Model,Filter}` lo-fi emulation. |
| Pitchbend range | none | **GAP (later)** — `Sound.PitchbendRange`. |
| Slice → pad (Apply to Group) | drum-machine pad mapping (`SPEC-BECKY-DRUM.md` §2/§6) | The slicer must emit **one Variant per slice across pads 1–16** + Step-grid notes. **becky already plans Detect/Grid/Split** (`SPEC-MASCHINE-CLONE.md` §2). **OK** — wire it to produce Sounds. |
| Slice → notes (Apply to Sound) | one Sound, many start/end Variants, piano-roll notes | **GAP** — the slice-to-notes (chromatic) path needs a Keyboard/piano-roll target. |
| Output Main Level/Pan + 2 Aux sends (pre/post) | `internal/studio` bus graph | becky models sends as graph edges; carry `Sound.Level/Pan` + 2 named send amounts + `prefader` flags. **GAP** — add these scalars on the Sound so `becky-wire` can consume. |
| 8 Macro knobs (bipolar ±100%) | none | **GAP (later)** — `Group.Macros[8] { Target(SoundIdx,Param), Min/Max }`; ties into becky's preference-learning. |
| Sampling-in (Extern/Intern, Threshold/Detect, Sync) | none (becky-stems/becky records elsewhere) | **GAP** — a `record`/`resample` action; threshold+detect is pure-Go DSP becky can do; resample-from-bus needs the live engine. |

### Net gaps to add to `internal/sampler` for Maschine fidelity
**v1:** `SourceType`, `Polyphony`+oldest-steal, `Legato`+`Glide`, `Sound.Tune`,
`Variant.Reverse`, **`Sound.AmpEnv{Type,A,H,D,S,R}`**, `Crossfade`, a `VelocityMod`
struct (≥ Velocity→Volume), `Sound.Level/Pan`+2 send amounts. The slicer must emit one
Variant per slice across pads 1–16.
**Later:** `Filter`, sampler quick-FX (Comp/Drive/SR/Bits), `ModEnv`, `LFO`, `Modwheel`,
Vintage engine, Pitchbend, slice-to-notes, the 8 Macros, sampling-in/resample.
becky's existing **round-robin, velocity-crossfade, and arbitrary off_by choke** are
*supersets* of Maschine — keep them, but don't present them as "Maschine parity."

---

## Sources (most load-bearing first)

1. **Official NI MASCHINE 2 software manual (PDF)** — exact Sampler parameter pages,
   recording/slicing/mapping, Macros (pages cited inline as p.58–96, 203–215).
   https://www.native-instruments.com/fileadmin/ni_media/downloads/manuals/maschine_27810/MASCHINE_2_Manual_English_2_7_10.pdf
   (mirror: https://www.strumentimusicali.net/manuali/NATIVEINSTRUMENTS_MASCHINEMKII_ENG.pdf)
2. **Voice Settings / Engine — Maschine Studio Manual, ManualsLib p.310** — polyphony
   1–64, oldest-note voice stealing, Legato/Glide.
   https://www.manualslib.com/manual/1258176/Native-Instruments-Maschine-Studio.html?page=310
3. **Sampling and sample mapping — NI tech-manual (online)** — zones, slice modes,
   zero-crossing, key/velocity ranges.
   https://www.native-instruments.com/ni-tech-manuals/maschine-software-manual/en/sampling-and-sample-mapping
4. **Working with Plug-ins — NI MK3 manual** — Sound = up-to-4 Module chain, source vs FX.
   https://www.native-instruments.com/ni-tech-manuals/maschine-mk3-manual/en/working-with-plug-ins
5. **In-Depth Overview of Sampling with Maschine 2.0 — ADSR** — slice modes, time-stretch,
   layering, destructive edits (tutorial corroboration).
   https://www.adsrsounds.com/maschine-tutorials/sampling-with-maschine/
6. **Modulation Envelopes in Maschine — ADSR** — Mod-Env ADSR/AHD, Oneshot⇒AHD-only.
   https://www.adsrsounds.com/maschine-tutorials/maschine-sampler-modulation-envelopes/
7. **Playing on the controller — NI MK3 manual** — choke/link groups, base key, pad modes.
   https://www.native-instruments.com/ni-tech-manuals/maschine-mk3-manual/en/playing-on-the-controller
