# Music Research Standard — how becky learns a genre before composing

**Status: MANDATORY.** Pinned from `CLAUDE.md`. Read before composing in an unfamiliar genre or
adding a genre profile. This is the discipline a real musician uses — *"composers don't memorize
all theory; they research what they need for each piece"* — captured from the ACE-Step-DAW
`music-theory-engine` / `compose` skills and re-expressed as becky's standing procedure. The
*rules* it produces are deterministic (they live in `internal/arrange` + `internal/musictheory`);
this file governs how the genre KNOWLEDGE that feeds them is gathered, so becky stops guessing.

The five-phase loop: **RESEARCH → ANALYZE → EXTRACT → COMPOSE → EVALUATE.** Compose is the
deterministic engine (`internal/arrange`); this doc is RESEARCH/EXTRACT/EVALUATE.

---

## 1. The 5 elements to extract for any genre (the output of research)

A genre is "researched" when becky can fill all five — this is exactly the shape of an
`internal/music/profiles/<genre>.json`:

1. **Key/Scale** — the most common tonality (e.g. minor pentatonic for blues, Dorian for lo-fi).
2. **Chord Language** — the chord types/progressions that define it (7ths/9ths for jazz; power
   chords for punk).
3. **Rhythmic Feel** — straight 8ths? swing? syncopation? the drum backbone + typical BPM.
4. **Texture** — sparse or dense, which instruments, which register.
5. **Form** — section lengths and the energy curve (Intro / Loop A / Loop B / Outro, in bars).

## 2. The search-query templates (run these, don't freestyle)

For a genre `{g}`: `"{g}" chord progressions analysis` · `"{g}" song structure common patterns` ·
`"{g}" rhythm patterns drum programming` · `"{g}" bass line techniques` · `"{g}" typical BPM tempo`
· `"{g}" scales modes used`. Aim sources at **Hooktheory, Chordify, Ultimate Guitar** and genre
breakdowns.

**Named references are GOLD.** If Jordan names a song or artist, that is the highest-value signal —
he is telling you exactly what he wants. Research it directly: `"{song}" chord progression key BPM`,
`"{song}" music analysis breakdown`. A named reference outranks generic genre research.

## 3. Research-when-you-don't-know matrix

| You don't know… | Do this |
|---|---|
| the genre's typical chords | search `"{g}" common chord progressions` |
| the right scale/mode | search `"{g}" scales modes used` |
| the drum pattern | search `"{g}" drum pattern programming` |
| the BPM range | search `"{g}" typical BPM tempo` |
| a specific song's harmony | search `"{song}" chords key BPM analysis` |
| a voicing | **don't search — reason from intervals** (`musictheory.VoiceFromIntervals`) |

The one thing worth memorizing (no search): **Middle C = c4 = MIDI 60; an octave = 12 semitones.**

## 4. EXTRACT — distill to 2–4 principles

Reduce the research to **2–4 governing principles** (default 3: one harmonic, one rhythmic, one
textural). Fewer than 2 is too vague; more than 4 is overconstrained and mechanical. Simple genres
(punk, ambient) → 2; complex (jazz, prog) → 4. This is the contract handed to the arranger.

Worked examples (note the corroboration with `ARRANGEMENT-RULES.md`):
- **Lo-fi hip-hop:** jazz 7th/9th + Dorian color; laid-back drums behind the beat + ghost notes,
  70–90 BPM; sparse pentatonic melody with space.
- **EDM drop:** four-on-the-floor + offbeat hats, 128 BPM; minor, often 2 chords, heavy bass;
  stripped breakdown → full drop.

## 5. The pipeline (how research becomes a deterministic profile)

```
becky-research "{genre}"   ──►  distill to the 5 elements + 2–4 principles  ──►  internal/music/profiles/{genre}.json
   (ONLINE: the query           (the MODEL/fuzzy step — local model or             (DETERMINISTIC data the
    templates above,             Claude; the ONLY non-deterministic part)            Go arranger consumes)
    fetch + verify)
```

This is how becky grows past its 13 hand-authored genres **on demand, from research** — not by an
agent re-deriving theory in chat (the failure this whole standard exists to kill). The profile, once
written, is permanent deterministic data.

## 6. Deterministic vs. model — the split (becky's "math not tokens" invariant)

- **Deterministic → Go, no tokens:** transposition, chord-function classification, progression/
  melody/rhythm analysis, voicing-from-intervals, the universal constraints, the evaluation
  checklist, the build order, and consuming a profile. Most of a genre's "rules" are math — becky
  must NOT spend tokens on them. (`internal/musictheory`, `internal/arrange`.)
- **Model/network → the 5%:** the genre RESEARCH itself (reading reference analyses online) and
  distilling fuzzy prose into the 5 elements / 2–4 principles. Plus the two subjective evaluation
  questions ("would a human recognize the genre?", "does it groove?") which are flagged, never
  auto-passed.

## 7. EVALUATE — becky checks its OWN output before shipping a beat

`musictheory.Evaluate(arr)` machine-checks the deterministic half of the checklist: key-consistency
across all parts, never-flat velocity, rests/space present, bass on chord roots at strong beats,
chord-tones on strong beats. This is "corroborate, then CONCLUDE" applied to becky's own music —
a generated beat that fails these is flagged, not shipped. The two subjective checks stay
human/model-flagged.
