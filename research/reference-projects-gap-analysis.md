# reference-projects-gap-analysis.md — what 3 reference projects reveal becky-canvas may be missing

Gap analysis, NOT a rewrite. Read 3 local reference projects (REAPER MCP chatbot, MIDI-generation
Claude skill, a C++ DAW) for **ideas becky forgot to consider** — explicitly NOT to adopt their
Python/C++ or stray from what becky already dialed in. Cross-referenced against `dawmodel`,
`ctledit`, `ctlmodel`, `arrange`, `musictheory`, `beatgen`, `drummachine`, `pianoroll`,
`audioengine`, and the existing `GAP-ANALYSIS.md` / `FEATURE-INVENTORY.md` (187-item checklist).

Tags: **already-have** (cite package) / **partial** / **MISSING**. Items already tracked by
GAP-ANALYSIS/FEATURE-INVENTORY are NOT repeated here unless a reference adds a genuinely new angle.

Date: 2026-06-22.

---

## PRIORITIZED ROADMAP — "becky-canvas forgot to consider"

Ordered by impact on Jordan's actual workflow (he works in the canvas, talks to the agent box,
makes beats, arranges). Each is a NEW gap the references surfaced, or a known gap a reference
sharpens.

| # | Gap | Tag | Why it matters | Source |
|---|-----|-----|----------------|--------|
| 1 | **Per-step drum detail in the UI** (velocity/probability/microtiming/ratchet/flam/pitch/pan), and unify `beatgen`↔`drummachine` so the rich data survives | partial | The engine HAS all of it (`beatgen.Step`); the canvas drum panel touches almost none — Jordan's "lacks core fundamentals" complaint is mostly here | all 3 |
| 2 | **AI can READ full DAW state, not just write** — a `Describe`/snapshot the agent box queries before proposing | partial | `ctledit.Describe` + `ctlmodel.Snapshot` exist but are thin; Dawzy's whole design is "get_state → reason → act". Without a rich read, the agent guesses | Dawzy |
| 3 | **AI sets FX parameters by name+value** (`set_fx_param`, `add_fx`) | MISSING | Dawzy's core verb. becky's `ctledit` can route/sidechain but cannot say "set the compressor attack to 5ms" — FX params are declared data, never AI-settable | Dawzy |
| 4 | **`add_send` aux send / FX-return bus** as a first-class model field + AI verb | MISSING | dawbase has `Send{target,gain_db,pre_fader}` on every track; becky has buses+sidechain but NO sends/returns. Reverb/delay routing is impossible | dawbase |
| 5 | **Per-track headroom defaults + lazy plugin chain** ("born at -6 dB, not 0 dB"; plugins are `{name,preset,loaded=false}` intent) | partial | dawbase's signature fix. becky's `mixplan` has role chains but new tracks aren't gain-staged on creation, and there's no lazy-plugin intent model | dawbase |
| 6 | **LEGO "brick" track presets** — `add_track(brick="808")` snaps in a routed, gain-staged, pre-chained track | partial | becky has genre profiles + `add_layer`, but no named single-track recipe ("add a Lead Vocal", "add Hats") that arrives pre-routed+pre-FX | dawbase + midi-skill |
| 7 | **MIDI CC / pitch-bend / expression as data** (mod wheel, sustain, expression over time) | MISSING | Dawzy reads/writes envelopes; no becky model carries CC. Blocks expressive synth/automation. Already noted in GAP-ANALYSIS but references confirm it's table-stakes | Dawzy + midi-skill |
| 8 | **Instrument range + role tables** (GM ranges, soprano/alto/tenor/bass register map) to keep generated parts playable & unmasked | MISSING | midi-skill ships full range/orchestration tables. becky clamps bass register but has no per-instrument range guard or frequency-masking check | midi-skill |
| 9 | **Counterpoint / voice-leading checks** (parallel-5th/8ve avoidance, contrary motion, voice-crossing, no doubled leading-tone) | partial | becky's `musictheory.Evaluate` checks key/velocity/register; midi-skill adds explicit part-writing rules. Would raise multi-line arrangement quality | midi-skill |
| 10 | **Articulation + duration vocabulary** (dotted/triplet durations `d4`/`8t`, staccato/legato, accent) at generation time | partial | becky has `pianoroll.Legato`/`Humanize`; midi-skill models dotted+triplet durations and accent as first-class. becky's step grid is 1/16 only (no triplet) | midi-skill |
| 11 | **Polyrhythm / hemiola / odd meters** (3:2, 4:3, 5/4, 7/8) | MISSING | midi-skill + beatgen-adjacent. becky is 4/4-locked (single global meter, no triplet grid). Limits prog/experimental genres | midi-skill |
| 12 | **"Mix doctor" + "arrange doctor" agent reads** — analyze current mix/arrangement and PROPOSE balance/structure fixes | partial | midi-skill's `/mix` `/arrange` `/jam` commands. becky has `becky-wire`/`stems`/`ref` as CLIs but the canvas agent box doesn't run an analyze-then-suggest loop over the live session | midi-skill + dawbase |
| 13 | **`key`+`scale`+confidence on the session, AI-settable** (`set_key`) + drives snap-to-scale in piano roll | partial | dawbase stores key/scale/confidence on Project; becky has BPM but key lives only inside `arrange`, and `set_key` is not a `ctledit` op. Unwired `musictheory.InScale` (GAP-ANALYSIS) would finally have a source | dawbase + Dawzy |
| 14 | **Per-clip / media-item parameters** (fade in/out len+shape, snap offset, item gain, loop-source, timebase) | partial | Dawzy's `get_state` enumerates 28 media-item params. becky has region fades in `audiotrack` but no per-CLIP fade/gain/loop in the arrangement model | Dawzy |
| 15 | **Generate-and-audition loop** ("generate a beat → it lands in the session → hear it") driven from one agent phrase | partial | Dawzy's `generate_beat` adds AI audio straight to REAPER. becky's `beatgen` + `becky-daw-engine` render is there; the one-phrase "make it and play it in place" round-trip is the missing glue | Dawzy |

The cluster that matters most for Jordan: **#1, #3, #4, #7** — drum-step detail in the UI, AI-settable
FX params, aux sends, and CC/expression. Those are the four "real DAW" capabilities all three
references take for granted and becky's agent box cannot do today.

---

## DRUM MACHINE — the concrete "core fundamentals" gap

Jordan: becky's canvas drum machine "lacks significant core fundamentals." The honest finding: the
**engine already supports most fundamentals**; the **canvas UI exposes almost none of them**, and the
two drum models (`drummachine`, `beatgen`) disagree on what a step carries.

### Engine ALREADY supports (do NOT rebuild — wire to UI)
- Per-step **velocity** (`drummachine.Step.Vel`, `beatgen.Step.Velocity`), **probability**
  (`beatgen.Step.Probability`), **ratchet/retrigger** (`beatgen.Step.Ratchet` + `ExpandStep`),
  **flam** (`beatgen.Step.Flam` + `flam.go`), **pitch** (`beatgen.Step.Pitch`), **pan**
  (`beatgen.Step.Pan`).
- **Swing** (`Pattern.Swing`), **per-lane microtiming** (`beatgen.Lane.TrackDelay`), **density**,
  **euclidean** (`ApplyEuclidean`), **rotate**, **genre presets**, **mutate/remix**.
- **Choke groups** (`Pad.ChokeGroup` + SFZ `off_by`), **velocity layers/round-robin**
  (`sampler.Layer`/`Variant`), **per-pad sample load / kit load-save / tune / reverse / decay**.
- **Multiple patterns, banks, scenes, song-mode chaining** (`drummachine.Song`, `SetSongOrder`).

### Genuinely MISSING fundamentals (UI and/or model)
1. **Per-step detail editor in the canvas** — no UI sets velocity, probability, microtiming,
   ratchet, flam, pitch, or pan on a step. This is THE gap. (engine ✅ / UI ❌)
2. **The `beatgen`↔`drummachine` round-trip DROPS the rich per-step data** (`ToDrumGrid`/
   `PatternFromDrumGrid` flatten to on/off+vel) — so even when set, it's lost. Unify or carry it.
3. **Triplet / finer step resolution** — 1/16 only; no 1/8, 1/32, or triplet grid (no swung/shuffle
   grid beyond the swing scalar).
4. **Live record / step input + note-repeat/roll** — sequencer is click-to-toggle only; no real-time
   playing-in, no held-repeat at a rate (ratchet ≠ note repeat). (Dawzy has a mic-record path.)
5. **Per-pad full ADSR (attack)** in the UI — pad model has decay only; full envelope only via an
   imported `sampler.Sound.AmpEnv` (no attack knob on a pad).
6. **Clear pattern** + dedicated **end-of-phrase Fill** op (only `Busier` density today).
7. **Accent** as a first-class step state (velocity simulates it; no accent toggle).
8. **Per-step pan/pitch is inaudible** — engine render is mono (stereo pan/pitch are Phase-2), so
   even a wired UI wouldn't be heard until the real-time stereo mix engine exists.

Lead with #1+#2 (wire the existing rich step model into a per-step canvas editor and stop dropping it
in the bridge) — that single change closes most of "lacks core fundamentals" without new DSP.

---

## PER-PROJECT FINDINGS

### 1. Dawzy-chatbot (REAPER MCP chatbot) — the AI↔DAW operation schema

Architecture: an LLM that **dynamically generates Lua ReaScript** and can call any of the **711
ReaScript API functions**. The MCP server itself only ships two live tools (`set_fx_param`,
`generate_beat`; `add_fx` is commented out), but the real "operation schema" is what `get_state.lua`
READS and what the demo manipulates. That read-model is the takeaway.

**Operations Dawzy exposes / demonstrates, cross-referenced to becky's `ctledit`:**

| Dawzy operation | becky `ctledit`? | Tag |
|-----------------|------------------|-----|
| Get full project state (play state, position, bpm, time-sig, repeat, cursor context) | thin `Describe` | **partial** |
| Per-track: name, instrument, FULL FX chain w/ every param + formatted value + range | `Describe` lists tracks/strips/FX-chips only | **partial** |
| `set_fx_param(track, fx, param, value)` (dB-aware) | — | **MISSING** |
| `add_fx(track, fx_name)` / open FX browser | — (FX are manifest data) | **MISSING** |
| Insert MIDI notes into a take at PPQ positions | `OpAddNotes` ✅ | already-have |
| Add a track + name it | `OpAddTrack` ✅ (name = ID) | already-have / partial rename |
| Duplicate a track + pitch up an octave + blend at 20% | transpose ✅; duplicate-track ❌; "blend %" = gain ✅ | **partial** |
| Shift track up 1 octave via ReaPitch FX param | `OpTranspose` (notes) ✅; as an FX ❌ | already-have (notes) |
| Set item attack/decay (envelope/FX param) | — | **MISSING** |
| Read/write **track envelopes** + envelope points (CC/automation, shapes: linear/bezier/hold…) | — | **MISSING** |
| Read **media-item params** (28: fades in/out len+shape+dir, snap offset, item vol, loop-src, timebase, take, color, lane Y/H) | region fades in `audiotrack` only | **partial** |
| Tempo/time-sig **markers** over time (tempo map) | single global BPM | **MISSING** (also in GAP-ANALYSIS) |
| Play / record (mic) / transport state | render-and-play ▶; mic→WAV record | **partial** |
| `generate_beat(description)` → AI audio dropped INTO the session | `beatgen`+engine render exist; one-phrase in-place add is the glue | **partial** |

**Net for becky:** the high-value missing verbs are **`set_fx_param`** and **`add_fx`** (FX
parameters by name+value), a **richer AI read of session state** (Dawzy's whole loop is read→reason→
act), **envelope/automation** read-write, and **per-item fade/gain** params. Dawzy's "generate the
Lua, run any of 711 API calls" is more than becky needs — becky's closed `ctledit` enum is the right
call — but the enum is missing the FX-param and send verbs the references treat as basic.

### 2. midi-agent-skill (MIDI-generation Claude skill) — music concepts

Models a deliberately simple composition (`{title, bpm, tracks:[{instrument, notes:[{pitch,
duration}]}]}`) but ships **rich music-theory resources** that reveal concepts becky's
`arrange`/`musictheory`/`beatgen` don't model:

- **already-have:** scales/chords/intervals/cadences, harmonic function, in-key constraint, bass-on-
  root/fifth, genre progressions, humanize, velocity variation — all in `musictheory` + `arrange`
  (becky's are arguably stronger: stem-aware, kick-locked bass, minor-V-major).
- **MISSING — instrument range + orchestration tables:** per-GM-instrument comfortable ranges,
  soprano/alto/tenor/bass register/role map, frequency-masking avoidance, doubling/layering recipes.
  becky clamps only the bass register; nothing guards a lead from going out of an instrument's range
  or two parts masking in the same octave.
- **MISSING / partial — counterpoint & part-writing:** species counterpoint, **parallel-5th/8ve
  avoidance**, **contrary/oblique motion preference**, **no voice-crossing**, **don't double the
  leading tone**, max voice-spacing. becky's `Evaluate` is closer to "is it in key / not flat"; these
  are the next tier of multi-line quality.
- **MISSING — articulation + duration vocabulary:** dotted (`d4`) and **triplet** (`8t`) durations,
  staccato/legato, accent — modeled as first-class. becky's grid is 1/16 only; piano roll has legato
  but no triplet/dotted-aware generation.
- **MISSING — polyrhythm / hemiola / odd meters:** 3:2, 4:3, hemiola, 5/4, 7/8, swing-triplet feel.
  becky is 4/4 + a swing scalar.
- **AI-DAW interaction model (`/mix`, `/arrange`, `/jam`):** analyze the live mix/arrangement, show
  current-vs-proposed as a table, apply on approval. becky has the CLIs (`becky-wire`, `stems`,
  `ref`) but the **canvas agent box doesn't run this analyze→propose-table→apply loop** over the open
  session (it applies edits but doesn't critique the mix/arrangement first). Pairs with becky's
  existing "show me, don't do it" overlay.

becky's generative core is genuinely ahead on stem-awareness; what it lacks is the **playability /
part-writing guardrails** (ranges, masking, counterpoint) and **rhythmic vocabulary** (triplets,
dotted, odd meters) the skill treats as baseline.

### 3. dawbase (C++ DAW) — DAW features/architecture

becky already ported dawbase's key/tempo DSP (`internal/dsp`) and habit-learner (`internal/habits`).
The model + ops + architecture reveal more becky hasn't taken:

- **MISSING — `Send`/aux returns:** every dawbase `Track` carries `vector<Send>` with
  `{target_bus, gain_db, pre_fader}`, and `add_send` is a first-class op. becky has buses + sidechain
  but **no sends/returns and no pre/post-fader** — the #1 routing gap (also in GAP-ANALYSIS, but
  dawbase shows the minimal model to copy).
- **partial — headroom-first defaults:** dawbase tracks are **born gain-staged** per kind (kick -4 dB
  in DRUMS, 808 -5 dB in BASS, pad -12 dB in MUSIC; groups trim; master at -1 dB w/ a lazy limiter).
  becky's `mixplan` has role chains but a new `dawmodel` track isn't auto-gain-staged on creation.
  This is the "stop defaulting everything to 0 dB" fix becky hasn't fully adopted.
- **partial — lazy plugin references:** dawbase `Plugin = {name, preset, kind, loaded=false,
  bypassed}` — records *intent*, instantiates only on arm/play (so projects open instantly without
  scanning 500 plugins, and a plugin can be **bypassed**). becky's FX are manifest data with no
  per-plugin loaded/bypassed/preset intent and no bypass flag (bypass also flagged in GAP-ANALYSIS).
- **partial — `key`+`scale`+`tempo_confidence`/`key_confidence` on the Project:** dawbase stores the
  musical key/scale AND a 0..1 confidence ("0 == we guessed, change me freely"). becky has BPM on the
  arrangement but key lives only inside `arrange`; surfacing key+scale+confidence on `dawmodel` would
  (a) let `set_key` be an AI verb and (b) finally give `musictheory.InScale` / snap-to-scale a source.
- **partial — LEGO `Brick` track presets:** `add_track(brick="808"|"Lead Vocal"|"Pad"|"Hats")` snaps
  in a track already routed, gain-staged, colored, with a lazy plugin chain + MIDI defaults. becky has
  genre profiles and `add_layer` (bass/chords/melody) but no named single-track recipe library.
- **already-have / shared philosophy:** "95% deterministic, 5% taste"; AI emits tool-calls that
  dispatch to the SAME ops a human uses (becky's `ctledit` is exactly this); habits override defaults
  (becky's `habits` ✅); pure-DSP fallbacks (becky ✅). The architectures agree — becky is missing
  specific MODEL FIELDS (sends, lazy/bypass plugins, key+confidence, born-gain-staged), not the
  philosophy.
- **MISSING — track `color`:** dawbase colors every track/bus/group; becky has no track color field
  (also in GAP-ANALYSIS — a cheap visual-first win for Jordan's low-vision, color-coded canvas).

---

## SUMMARY OF GENUINELY-NEW GAPS (not already in GAP-ANALYSIS/FEATURE-INVENTORY)

1. **AI-settable FX parameters** (`set_fx_param`/`add_fx`) — becky's agent can route but not dial a
   compressor; FX params are read-only manifest data. (Dawzy)
2. **A rich AI READ of the live session** before it proposes — `Describe`/`Snapshot` are thin vs
   Dawzy's full get_state (FX chains w/ param values+ranges, envelopes, item params). (Dawzy)
3. **Aux sends / FX-return buses** with pre/post-fader — a concrete minimal model exists in dawbase;
   becky has none. (dawbase)
4. **Instrument range + frequency-masking guards** and **counterpoint/part-writing rules** at
   generation time. (midi-skill)
5. **Born-gain-staged tracks + lazy/bypassable plugin intent + key/scale/confidence on the session
   model**, and **track color** — model fields dawbase has and becky doesn't. (dawbase)

Everything else the references show (transport, multitrack NLE, metering, save-from-GUI, real-time
mix engine, per-step UI) is ALREADY captured in `GAP-ANALYSIS.md` — these five extend it.
