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

## DELIVER THE DOCUMENT. NEVER POINT AT A PATH. (2026-08-26)

Violated badly on 2026-08-26. A list Jordan urgently needed was written to
`X:\Videos\...\23_hj-fbi-recap\QUOTES.md` and he was told where to find it. His reply:
*"you're asking me to navigate to a subfolder and open a .md file - which explicitly violates so
many fucking rules... fucking open the markdown file for me or else your job is not done yet."*

He is right, and it is not a small thing: he has impaired vision, reading is physically costly,
and "go to this folder, find this file, open it" is several barriers stacked on someone who asked
for a list. **A deliverable that a human still has to go and find is not delivered.**

The rule:

- **If you produce a document for Jordan, OPEN IT ON HIS SCREEN.** Then verify with a screenshot
  that it actually rendered - do not assume the launch worked.
- **If it is short, put it in the chat instead.** A list of 20 things is a chat message, not a file.
- Only mention a path as a footnote *after* the thing is already open or already in the message.

**Machine fact, do not relearn it:** there is **NO default handler registered for `.md`** on this
PC, so `Start-Process file.md` fails silently. MarkText is installed at
`C:\Users\only1\AppData\Local\Programs\MarkText\MarkText.exe` - launch it explicitly:

```powershell
Start-Process 'C:\Users\only1\AppData\Local\Programs\MarkText\MarkText.exe' -ArgumentList '"<file.md>"'
```

Same principle for any artifact: a `.veg` gets opened in VEGAS, a render gets opened, a report gets
opened or pasted. The end of the job is the human LOOKING at the result, not a path in a sentence.

## Interaction

If you have a question about video footage, **PULL IT UP** in the becky review gui
If you have a question that can be VISUALIZED, **CREATE THE VISUAL**

Eventually we'll have a conversational realtime ai to discuss things with Jordan, for now just be mindful and creative in your approach