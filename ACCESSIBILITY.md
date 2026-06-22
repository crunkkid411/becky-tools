# ACCESSIBILITY.md — how becky must fit Jordan's vision

**Jordan is SIGHTED but has impaired vision.** He reads the screen directly. He does
**not** use a screen reader and does not want one. This file exists because an agent
once assumed the opposite, stripped his colors, and bolted on Microsoft's TTS — all
wrong. Get these facts right.

## The facts (do not re-guess these)

1. **He reads the terminal himself.** Plain, sighted reading — with limits on how much
   he can comfortably read at once. So: lead with the answer, keep it tight, no walls of
   text. Concise > exhaustive.
2. **High-contrast CUSTOM COLORS are an accessibility AID, not a barrier.** becky-ask's
   bubbletea TUI with its custom palette (the neon-green / pink / amber scheme in
   `cmd/ask/styles.go`) is *easier* for him to read. **Keep colored TUIs. Do NOT strip
   color, and do NOT replace a colored TUI with plain monochrome text "for accessibility."**
   That makes things worse for him, not better.
3. **No screen reader.** Don't linearize/flatten output for an assistive reader he
   doesn't use. Tables/columns should be readable, but the fix is good layout + color,
   not stripping formatting.
4. **No Microsoft TTS — ever.** SAPI / Narrator / the built-in Windows voice are
   explicitly rejected.
5. **He DOES want a real, good-quality TTS** as an output channel (becky reads results
   aloud so he can rest his eyes). This is a genuine, long-standing request. **The engine
   choice is a model choice and MUST go through becky's deep-research protocol** (see
   `SPEC-DEEP-RESEARCH.md` / `STANDARDS-MUSIC-RESEARCH.md`). Known dead ends already ruled
   out by Jordan: **Piper (deprecated)** and **Kokoro (quality insufficient — "sounds like
   ass")**. The researched recommendation + spec lives in `SPEC-BECKY-TTS.md`.
6. **Asking him to decide:** a short, concise option list he can read is fine (he reads
   the terminal). Don't dump a giant menu; keep choices few and plainly worded.

## Why this matters

Audio/voice is a channel he values, and his reading has limits — but the answer is
**high-contrast visual + a good spoken option**, not pretending he's blind. A feature
that removes his color cues or speaks in a voice he hates is a regression, even if it
"adds accessibility." Match the real human, not an assumed one.
