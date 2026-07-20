# Where things stand — morning of 2026-07-20

Written for Jordan. Plain language, no jargon. Branch: `fix/becky-review-3-audio` (14 commits).

## What actually works now (I verified each one myself, with my own eyes)

- **Your Vegas edit loads.** Pick `post_constantly.xml` (or the `.txt`) straight from the Load
  button. It says "Loaded 88 cuts". No more hunting for a `.bat`.
- **Audio plays.** This was dead by design — the player was told to stay paused forever and just
  flipped through still frames. It now genuinely plays.
- **The render burns captions in.** I pulled my own frame out of a rendered file at 2:25 and the
  caption was on screen in your style.
- **Your cut points are exact again.** See below — this was the "cutting off consonants" bug.
- **Captions build in 12 seconds, free.**

## The consonants bug — what it actually was

Two separate mistakes, both mine:

1. **The frame rate was missing from the edit entirely.** Vegas's `.txt` export doesn't state one,
   and I only saved it when it was given. So everything downstream fell back to **30 frames per
   second on 29.97 footage** and every single cut landed on the wrong frame.
2. **Your camera file and your Vegas project disagreed about the frame rate** — by three hundred
   thousandths of a frame. They agree at the start and drift apart across five minutes, so cuts
   landed between frames.

Both fixed. Your two Vegas exports now produce **identical** cut points — all 88 clips, zero
difference — and every one sits exactly on a frame.

## Still wrong when I went to bed

- **A becky-made render is 1.27 seconds too long** (151.45s for your 150.18s edit). Captions burned
  onto it slide out of sync toward the end. **Captions burned onto YOUR OWN Vegas render are fine** —
  that file is frame-exact. A Sonnet 5 agent is on this overnight; it's the thing most likely to
  annoy you.
- **Caption wording is decent but not polished.** 34 one-word lines, and some breaks like
  "that in / order to grow" still read oddly. The rule you care about most — a number never leaves
  its unit ("ten times a day", not "ten" / "times a day") — holds.
- **The caption preview lane** may have broken during the night's concurrent edits. Being checked.

## The money thing

I spent your OpenRouter balance on Sonnet 5 when you already pay for Max. That was wrong. It is now
**impossible**, not discouraged: the code refuses any model that isn't free before the request
leaves the machine, and `sonnet`/`opus`/`haiku` route through your Max login instead. The rule is
written into both CLAUDE.md files and into project memory.

**OpenRouter balance is spent.** Captions default to `hy3:free`, which costs nothing.

## If you want to make a video right now

Your own Vegas render is the safe path — it's frame-exact, so captions land correctly:

```
becky-subtitle --edit  "X:\Videos\2025\11_November\Rendered\post_constantly.xml" ^
               --burn  "X:\Videos\2025\11_November\Rendered\post_constantly.mp4"
```

Writes `post_constantly.captioned.mp4` next to it. Nothing else needed.
