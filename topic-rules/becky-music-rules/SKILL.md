---
name: becky-music-rules
description: "Music generation rules for becky-tools: deterministic arrangement (key, progression, drums, bass, chords, melody), ARRANGEMENT-RULES.md, becky-compose, beats, MIDI, layering stems. Use for any music, composition or drum work."
---

# Music rules

Moved verbatim out of the always-loaded CLAUDE.md files on 2026-10-10 so it is read only when this kind
of work comes up. It has the same authority as CLAUDE.md.

- **Music is deterministic — generate it with math, not tokens.** The arrangement build
  order and the rules that make each layer fit are SETTLED and live in code
  (`internal/arrange`): `key+progression → drums → bass → chords → melody → texture`,
  each layer aware of the stems before it (bass LOCKS to the actual kick, chords/melody
  stay in key, minor-key V is major, velocity is never flat), 8 bars max per chunk.
  "Four-on-the-floor house with my kick" must be instant + token-free, never a model
  call. A model is only for fuzzy plain-English intent, never the musical result. The
  canon is **`ARRANGEMENT-RULES.md`** — read it before any composition/layering work; it
  exists so these rules stop getting re-researched and lost every session.
