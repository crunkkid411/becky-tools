# SPEC-BECKY-COMPOSE.md — deterministic, genre-aware multi-track MIDI

> **STATUS: BUILT 2026-06-15 — `becky-compose` (cmd/compose) ships.** Pure-Go,
> offline, deterministic. Ask for a genre, get all the MIDI stems (drums, bass,
> chords, melody, lead, counter, sfx) + a routing manifest you tweak to taste.
> Music theory is math; a genre is a frozen bundle of that math. Profiles are a DB
> so becky "already knows" a genre. Genre DB is meant to grow toward the bands a
> producer actually works with (Jordan's 15+ years of manual genre research,
> encoded once and reused).

---

## 1. What it is

`becky-compose --genre crunkcore [--key F#m] [--bpm 150] [--seed 1] [--out dir]`
emits, into `dir/`:
- one **Standard MIDI File per track**: `drums.mid bass.mid chords.mid melody.mid
  lead.mid counter.mid sfx.mid` — drag any stem into any DAW (or becky-canvas);
- a combined multi-track `song.mid`;
- `project.json` — the arrangement + **routing** manifest (§5).

`becky-compose --list` prints the known genres. **Deterministic:** the same
`(genre, key, bpm, seed)` yields **byte-identical** files (verified by test).

## 2. The load-bearing idea — a genre profile DB

Music theory is math: intervals, chord stacks, Roman-numeral function, groove
offsets. A **genre** is a named bundle of numeric constraints over that math
(tempo window, scales, weighted Roman-numeral progressions, per-track 16-step
grids, registers, swing, humanization). Each genre is researched **once** and
frozen as JSON in `becky-go/internal/music/profiles/<id>.json` (embedded via
`go:embed`). Generation is then a pure function `Generate(profile, key, bpm,
seed) -> Song`. The profile is the knowledge; the generator is deterministic.

**This maps a producer's manual research.** Jordan researches BPM/key/progression/
drum-feel by ear from reference bands. becky-compose turns that into a cached,
reusable profile, so "give me a crabcore song" or "early-metalcore in drop D"
produces a full multi-track starting point in seconds.

## 3. Implementation (BUILT)

`becky-go/internal/music/`:
- `smf.go` — dependency-free Standard MIDI File (type-1) writer (MThd/MTrk, VLQ
  deltas, note/program/tempo/timesig/track-name/end-of-track; byte-stable).
- `theory.go` — scales/modes, `ParseKey`, `ScaleMidi`, diatonic `Triad`, Roman
  degree, register `Clamp`, seeded deterministic PRNG.
- `genre.go` — `Profile` schema + embedded genre DB loader + velocity ladder.
- `compose.go` — `Generate`: resolve key/bpm, weighted progression, per-bar chord+
  energy timeline, then per-track generation (drums from grids + swing + rolls +
  energy-gated density; bass from roots + groove; chords with register voicing;
  seeded stepwise melody anchored to chord tones; lead/counter derived; sfx
  markers). Harmonic-minor V rule (uppercase V => major dominant). Per-track seeded
  streams.
- `project.go` — the `project.json` routing manifest (§5).

`becky-go/cmd/compose/main.go` — the CLI. Pure-Go, no cgo/models/network → green
on Linux+Windows CI. `music_test.go` covers VLQ vectors, key parsing, the V rule,
determinism, and a from-scratch SMF parser asserting a valid noteful arrangement.

## 4. Profile schema (genre JSON)

```
Profile { schemaVersion, id, displayName, sources[],
  tempo{min,max,default}, key{defaultRoot,scales[],defaultScale},
  swing(0.5..0.75), humanize{timingJitter,velHumanize},
  progressions[ {name,weight,roman[]} ],   // roman e.g. "i","bVI","V","V7"
  arrangement[ {name,bars,energy(0..1),tracks[],chaotic?} ],
  tracks{ <name>: TrackSpec } }
TrackSpec { program|null, channel, register[lo,hi],
  patterns{ <voice>:{note,grid[16],vel,rolls[{cell,n,ramp}]} },  // drums
  densityByEnergy, style, octave, glide, voicing, extensions[],
  rhythm, scaleSource, contour, motifBars, density, role, events[],
  activeFromEnergy, vel }
```
Velocity ladder: `ghost40 soft64 normal88 accent104 hard118`. Scales: major, minor
(aeolian), dorian, phrygian, phrygian_dominant, mixolydian, lydian, locrian,
harmonic_minor, pentatonics. PPQ = 480.

## 5. Routing — `project.json` (loads into becky-canvas)

Plugs into `SPEC-BECKY-CANVAS.md`'s deterministic audio DAG. Opens with **sane
routing, no manual wiring**: **808 isolated on `bus.808`**, **kick ducking the
music bus and the 808** as two declared `{from,to}` sidechain edges (one
declaration, not 100 clicks), channels pre-assigned per GM, `deterministic:true`.
Doubles as a human-readable load manifest for any other DAW.

## 6. Genre DB — current + growing

**Shipped:** `crunkcore`, `digicore`, `hyperpop`, and the 2000s scene-rock family
**`metalcore`** (Underoath *They're Only Chasing Safety*, early Bring Me the
Horizon, Emery, Kids in the Way, early Asking Alexandria, early Motionless in
White) and **`crabcore`** (Attack Attack! self-titled, Abandon All Ships, I Set My
Friends On Fire, the electronicore/synth-breakdown sound). Each profile records its
reference bands/sources in `sources[]` for provenance.

**Adding a genre (research once, cache forever)** — aligned with the ACE-Step
`music-theory-engine` research-first process:
1. Normalize name → `id`; reuse `profiles/<id>.json` if present.
2. Gather + **log** cited sources (reference bands/albums, BPM, key, drum feel).
3. Extract tempo window; key/scale; 2–4 progressions (Roman numerals, validated
   against harmonic function); drum/808 16-step grids; swing; register/program per
   track; humanize; arrangement (metalcore/crabcore add a `breakdown` section).
4. Write `profiles/<id>.json`, then generate. (The only explicit, logged, cached
   online step; the generator never touches the network.)

A becky agent/subagent does the research; the output is a plain JSON file added by
PR, so the DB grows toward the bands Jordan actually works with without touching
the engine.

## 7. Next (cloud agent to extend)
- More of Jordan's working-bands map (post-hardcore, easycore, trancecore,
  swancore, deathcore, pop-punk, …) — each a JSON profile.
- Richer voice-leading (full nearest-inversion argmin), motif reuse, per-genre
  rhythm/contour generators; breakdown/chug rhythm generator for metalcore family.
- becky-canvas loads `project.json` as a DAW project (piano-roll/mixer edit the
  stems in place); pitch-bend glide bytes for 808 glides.
