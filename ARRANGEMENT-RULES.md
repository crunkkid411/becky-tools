# Arrangement Rules — becky's deterministic music-theory canon

**Read before any composition / layering / `becky-compose` / `becky-arrange` /
canvas-music work.** These rules are the answer to "which layer do I generate
first, and how does each layer fit the ones before it." They were researched
repeatedly and then lost between sessions because they lived in chat. **They now
live in code** (`internal/arrange`) so the steps can't be forgotten — this file is
the human-readable canon; the Go is the executing version. If the two ever disagree,
fix the code to match this, and say so.

**Source:** ported from the **ACE-Step-DAW** `.claude` skill set — whose three music
skills (`music-theory-engine`, `strudel-maestro`, `compose`) INDEPENDENTLY state the
same build order — plus Jordan's own rules. ACE-Step itself emits Strudel via an LLM;
becky takes only the *deterministic rules that bound the notes* and implements them as
pure Go (MIDI), so it's instant, token-free, and hand-editable.

---

## 1. The build order (load-bearing)

```
key + progression  →  drums  →  bass  →  chords  →  melody  →  texture
```

Each later layer is written **aware of the earlier ones** (the "LEGO" principle):

- **drums** — the groove/rhythmic foundation. Jordan's starting point (he often has a
  loop already).
- **bass** — **LOCKS to the kick** (reads the actual kick onsets, not a template) and
  lands the **chord root on every strong beat**. → `arrange.AddBass`.
- **chords** — one voicing per bar, **same key**; in a **minor key the V is major**
  (raised leading tone) so the dominant resolves. → `arrange.AddChords`.
- **melody** — sits on top: **chord-tones on strong beats**, lots of **rests** (space
  is musical). → `arrange.AddMelody`.
- **texture** — pads/fx/fills in the remaining space (future).

Rule in code: `arrange.NextLayer` / `SuggestNext` — "if drums exist, suggest bass next."

## 2. The 8-bar chunk rule (Jordan)

**Generate at most 8 bars at a time.** Anything longer is slop, because distinct
musical ideas are distinct chunks — e.g. the B-section of verse 2 differs subtly from
the A-section, so they are generated separately, not as one long run. Enforced by
`arrange.MaxChunkBars = 8`.

## 3. Universal constraints (all deterministic, all in code)

- **Stay in key.** Every pitched part uses the established key/scale.
- **Bass register:** MIDI 36–55 (ACE-Step: bass lives 36–71; below 36 is "too low").
- **Velocity is NEVER flat** (ACE-Step's hardest rule). Per-role windows (MIDI):
  drums 38–115, chords/pads 38–64, melody 64–102, bass 56–104. Accent strong beats,
  soften off-beats, small seeded jitter. → `arrange.humanVel`.
- **Leave space** — rests are musical (melody is intentionally sparse).
- **Minor-key dominant:** raise the 7th degree for the V chord (the leading tone).
- **Time signature:** 4/4 default.

## 4. Per-genre progressions (seed data — extend freely)

Defaults live in `internal/arrange/theory.go` (`genreProgressions`), Roman numerals
over the key (the accidental comes from the scale). E.g. default minor = `i ♭VII ♭VI V`;
house = `i VI III VII`; lo-fi = `ii V i vi`; pop-punk = `I V vi IV`. becky already has
13 genre **theory profiles** in `internal/music/profiles/` — converge the two over
time (profiles carry instrumentation/feel; this carries progression).

## 5. What's deterministic vs. what may use a model

- **Deterministic (done / extendable in Go):** the build order, the per-layer fit
  rules, the universal constraints, the progressions, transposition, humanization.
  These run with **no model and no tokens** — "four-on-the-floor house with my kick"
  must never burn tokens.
- **Model-only-if-needed (the fuzzy edge):** turning vague plain-English intent into a
  *choice* (which genre, which mood). The musical **result** stays deterministic. A
  local model / specialized model / Claude is all fine here — but it's the last resort,
  not the default.

## 6. Still to build (same pattern — scaffolding is ready)

- `AddTexture` (pads/fx/fills); richer voice-leading in `AddChords`; cross-genre
  idioms ("emo guitar over a trap beat" — a genre-specific `AddMelody`/lead variant);
  converge `genreProgressions` with `internal/music/profiles`; "favorite samples"
  (one-click assignment of Jordan's kick/snare to the drum layer).
- Wire `arrange` into the canvas agent box so "add bass" / "add chords" work in the
  window (in progress).
