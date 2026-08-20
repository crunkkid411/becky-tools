# Reverse-engineering Jordan's own vertical edit — the standard, measured

Two files, one derived from the other:

- **The edit** — `2024-08-30_spitters_are_quitters…[7409071570410327339].mp4`, 1080×1920, 30fps,
  915 frames, 30.5s. Jordan's own vertical short.
- **The master** — `2024-08-30_We_Tried_the_ULTIMATE_Fast_Food_Test_BLINDFOLD_Tasting_[unswA5Jv7fI].mp4`,
  1920×1080, 29.97fps, 560s. The finished long-form it was clipped from, starting ~21s in.

His brief: *"That video is my editing preference for what you're building… there are easily like 100
decisions made in that 30 second clip - try to reverse engineer EVERYTHING."*

Because the vertical is a crop of the master, the decisions are **recoverable as numbers**, not
guessed. Method: SIFT features + RANSAC similarity transform between every vertical frame and the
master frames, giving the exact crop rectangle per frame; InsightFace on all 915 vertical frames for
what the viewer actually sees; becky-transcribe for word-level timing; frame-difference cut
detection on both. Everything below is measured. Where a measurement is unreliable it says so.

Cast: **Allison** (pink ombré hair, blindfolded), **Jordan** (green hair + paper crown,
blindfolded), **Shelby** (platinum blonde, not blindfolded — she runs the game).

---

## THREE FINDINGS THAT CHANGE WHAT WE BUILD

### 1. He did not choose the cuts. He inherited them.

Cut detection on both files, then aligning each vertical shot back to the master:

| vertical shot | starts at master time | nearest existing master cut | delta |
|---|---|---|---|
| 5 | 30.05s | 30.05s | **0 ms** |
| 6 | 31.95s | 31.95s | **0 ms** |
| 7 | 33.39s | 33.42s | 30 ms |
| 10 | 36.59s | 36.59s | **0 ms** |
| 11 | 37.29s | 37.29s | **0 ms** |
| 12 | 38.09s | 38.02s | 70 ms |
| 14 | 39.66s | 39.66s | **0 ms** |
| 15 | 42.36s | 42.36s | **0 ms** |
| 19 | 48.10s | 48.17s | 70 ms |
| 21 | 52.20s | 52.20s | **0 ms** |
| 23 | 54.07s | 54.04s | 30 ms |

**11 of 14 confidently-aligned vertical cuts land within 100ms of a cut that ALREADY EXISTS in the
long-form; 8 are frame-exact.** The master already carries ~24 cuts in this 30-second span — it is
itself a fast, heavily-cut edit.

**So the repurposing job is not "decide where to cut". The cuts arrive with the footage.** The job
is: detect the existing shot boundaries, keep them, and choose the vertical framing *within each
one*.

`becky-short` currently does the opposite — it takes a continuous window and runs `becky-cut` to
invent new cuts. On already-edited footage that is wrong twice over: it re-cuts an edit that was
already made, and it does so with a silence threshold tuned for raw footage.

### 2. He removes 10% of the time, not half of it

Two high-confidence anchors (115 and 248 RANSAC inliers):

    vertical  0.00s = master 22.11s
    vertical 28.67s = master 54.07s

    master span 31.96s rendered as 28.67s  ->  3.29s removed = 10.3%, across 22 cuts = 150ms per cut

**150 milliseconds per cut.** That is tightening — clipping the breath between beats — not silence
removal. For comparison, `becky-cut --dry-run` on `test-for-clips.mp4` removed **51%** of a window.
On this kind of material that would be a demolition.

Most of the 3.29s comes out in the first six seconds; from shot 5 onward the edit runs close to
real time.

### 3. His cuts land on WORDS, not on silence

Word-level transcript of the vertical (80 words), against the 22 cuts:

    median distance from a cut to the nearest word boundary:  57 ms  (1.7 frames)
    cuts within 2 frames (67ms) of a word boundary:           14 / 22
    cuts landing INSIDE a spoken word:                         9 / 22

Nine cuts land mid-word — and even those sit 7–107ms from a boundary, which is inside Parakeet's own
timing error. Six cuts land in genuine pauses (250–690ms out); those are the breath points.

**The rule is: cut ON the beat of speech, not in the gaps between speech.** becky currently derives
cut points from silence, which is the opposite instinct. Snapping to silence (shipped earlier
tonight) is right for a clip's OUTER edges and wrong for the cuts inside it.

---

## THE FRAMING CONSTANTS, MEASURED

From InsightFace on all 915 frames of the finished edit — what the viewer actually sees, independent
of what the master was doing.

| Measure | p10 | p25 | **median** | p75 | p90 |
|---|---|---|---|---|---|
| Face HEIGHT, % of frame height | 17.0 | 20.7 | **24.3** | 29.0 | 32.1 |
| Face CENTRE X, % of frame width | 40.2 | 45.6 | **49.6** | 54.0 | 60.5 |
| Face CENTRE Y, % of frame height | 24.7 | 26.9 | **29.9** | 35.5 | 40.1 |

- **87%** of frames put the main face horizontally inside the middle 30% of the frame.
- **90%** put the face centre in the **upper 40%** of the frame.
- **8.3% of frames (76) contain NO FACE AT ALL**, and that is deliberate (see shot 19).
- Faces per frame: mean 1.21. **656 frames show exactly one face**; single-subject framing is the
  default, two-shots are the exception, and only the final shot holds all three.

### A hard limit I hit while trying to apply these — read before "fixing" the framing

I measured becky's own render of `test-for-clips.mp4` with the identical tool and metric and got
face height **39.7%** against his 24.3%, face centre Y **39.3%** against his 29.9%. That looks like
becky framing far too tight and too low. **It is not, and the difference is the footage.**

A 9:16 crop of a 1920x1080 frame is at most **608x1080 — the full source height**. On
`test-for-clips.mp4` his face is already **37.8% of the SOURCE height**, so:

- it can never be smaller than that in the output, whatever the crop does, and
- a full-height crop has **no spare source above or below**, so it cannot be moved up or down at all.

Changing `--shoulder-frac` from 0.46 to 0.30 and re-rendering moved face height 39.7% -> 40.7% and
centre Y 39.3% -> 38.2%. Essentially nothing, because the crop was already maximal. **becky's
framing on that clip is constrained, not wrong**, and I reverted `--shoulder-frac` rather than keep
a change with no evidence behind it.

His reference edit gets its framing freedom from the shot itself: it is a wide table scene with
three people at a distance, so most of his crops are *narrower* than full height and he has room to
place a face at 30%. `--eye-line` is still worth correcting — it is the right number whenever there
IS vertical freedom — but head size is a property of where the camera was, not of our cropper.

**The open question for Jordan**, and it is a taste call only he can make: on close-up 16:9 footage
where the subject already fills the frame, do you want (a) the full-height crop we do now, subject
filling the frame, or (b) a padded/blurred background with the subject placed at your 30% line? His
own edit is full-bleed, but it never had to solve this case.

### What this says about becky's current defaults

`crop_path.py` ships `--eye-line 0.38` — eyes 38% down the frame. Derived from the measurements
above, Jordan's eyes sit at roughly **27%**.

> **becky frames the subject about a tenth of a frame too low WHENEVER it has the room.** The bottom
> half of his frame is reserved for the caption block, the hands and the food. `--eye-line` changed
> 0.38 -> **0.27**. Head height ≈ 24% is a useful target to aim at but cannot be forced — see the
> hard limit above.

### He frames himself wider than he frames everyone else

Median face height by subject: **Shelby 32%**, **Allison 27%**, **Jordan 20%**.

His own shots are the loose ones — because his shots are the ones carrying gesture (shot 15 hands up
on "PUT THE BURGER DOWN", shot 17 hands miming the plate swap). **When the gesture is the point, he
frames wide enough to hold the hands.**

### The camera is locked far more often than it moves

Frame-to-frame movement of the face centre inside a shot: **median 3.05px** (at 540×960 — about 6px
at full width), p90 12.8px. **34% of frames move less than 2px: the frame is nailed down.**

Per-shot scale change: 16 of 22 shots change size by less than ±15% end-to-end. Only six have a real
move. **The default is a locked-off frame; a move is an event.**

---

## THE ONE DECISION THAT SHOWS THE MOST CRAFT

Jordan called this out himself, and it measures exactly as he described.

**Shot 1** (0.00–1.47s). In the **master**, the camera is on a wide two-shot — Jordan left, Allison
right — and it **zooms in** across the shot, ending on a much tighter frame that splits the
difference between them.

In the **vertical**, Allison does not move at all:

    vertical frame  0 : crop width 668px of the master, face height 20.9% of frame
    vertical frame 20 : crop width 607px,               face height 20.7%
    vertical frame 43 : crop width 1160px,              face height 20.2%

**The crop width nearly doubles over 1.47 seconds while Allison's size on screen stays flat.** He is
widening the crop at exactly the rate the master zooms in, cancelling the master's camera move so
his chosen subject stays locked. Jordan is cropped out entirely; the master's "split the difference"
framing would have drifted toward him, and the sentence is about Allison.

> **The rule: when the master's camera moves, the vertical crop counteracts it to hold the chosen
> subject.** The subject's size and position on screen are the thing being controlled — not the crop
> rectangle. becky's zero-lag tracker already does this mechanically; what it lacks is the judgement
> about *which* subject to lock onto.

---

## WHO IS ON SCREEN vs WHO IS TALKING

| # | t | dur | on screen | spoken over it |
|---|---|---|---|---|
| 1 | 0.00 | 1.47 | Allison | "Oh, you guys aren't supposed to eat" |
| 2 | 1.47 | 1.43 | Allison | "them. Supposed to" |
| 3 | 2.90 | 1.20 | Jordan | "to feed them to each other. Yeah, yeah." |
| 4 | 4.10 | 1.63 | Allison | "yeah. I'm sorry." |
| 5 | 5.73 | 1.90 | **Jordan** | **"Allison, what the f***?"** |
| 6 | 7.63 | 1.07 | Allison | "I'm really hungry" |
| 7 | 8.70 | 0.93 | Jordan | *(nothing)* |
| 8 | 9.63 | 1.00 | Shelby | — |
| 9 | 10.63 | 1.73 | Allison | "french fry first" |
| 10 | 12.37 | 0.70 | Jordan | "Spit it out." |
| 11 | 13.07 | 0.73 | Shelby | "Okay, hold on, we're" |
| 12 | 13.80 | 1.10 | Allison | "changing the plate. Spit it out." |
| 13 | 14.90 | **0.53** | Allison | *(nothing — caption reads "NO!")* |
| 14 | 15.43 | **2.73** | Allison | "Swallow it. Okay, spitters are quitters, I'm sorry." |
| 15 | 18.17 | 1.43 | Jordan | "Put the burger down." |
| 16 | 19.60 | 0.87 | Allison | "And then we're" |
| 17 | 20.47 | 1.00 | **Jordan's HANDS** | "going to switch the plate." |
| 18 | 21.47 | 1.23 | Allison | "Yes. Allison" |
| 19 | 22.70 | 1.27 | **A POINTING FINGER — no face** | "this time, don't eat" |
| 20 | 23.97 | **2.87** | Allison | "it. That's really hard not to." |
| 21 | 26.83 | 0.83 | Shelby | "Anyways." |
| 22 | 27.67 | 1.00 | Jordan | "Thank you." |
| 23 | 28.67 | 1.83 | Allison + Jordan | "Yeah." |

**Roughly half these shots are NOT on the speaker.** The governing choice is: show whoever the
moment is *about*, or whoever's reaction is the payoff.

- **Shot 5 is the clearest case against speaker-following.** The line is "Allison, what the f***?" —
  a speaker-tracker frames Jordan (he says it) and a subject-tracker frames Allison (she's named).
  Jordan frames *himself*, because the outrage on his face is the joke. Then shot 6 cuts to Allison
  for "I'm really hungry" — the defence. Call and response.
- **Shot 13 is 0.53s with no words at all** — a pure reaction cutaway to Allison while someone
  off-screen shouts "NO!".
- **Shot 17 shows only his hands** miming the swap, over the words "going to switch the plate".
- **Shot 19 shows only a pointing finger**, 1.27 seconds, no face in frame — exactly the case Jordan
  described: the person was out of frame, and the gesture carried the joke.

> **Implication for becky:** LR-ASD answers "who is speaking". That is a *useful* signal and a
> *wrong* default. Nine of these 23 shots would be framed incorrectly by a pure active-speaker
> tracker, and two of them contain no face for it to score at all.

---

## PACING — it accelerates into the punchline, then holds

Shot durations, in order:

    1.47 1.43 1.20 1.63 1.90 1.07 0.93 1.00 1.73 0.70 0.73 1.10 0.53
    2.73 1.43 0.87 1.00 1.23 1.27 2.87 0.83 1.00 1.83

    min 0.53s   median 1.20s   max 2.87s

The two fastest runs and the two longest holds are not random:

- **Shots 10–13: 0.70, 0.73, 1.10, 0.53** — the fastest stretch in the clip, and it is exactly the
  "spit it out / hold on / NO!" argument. The cutting speeds up with the conflict.
- **Shot 14: 2.73s** — immediately after, the longest hold so far, on the punchline
  *"spitters are quitters, I'm sorry."*
- **Shot 20: 2.87s** — the longest shot in the clip, on the closing beat.

> **Accelerate into the joke, then hold on the payoff.** A uniform target shot length would destroy
> this. The measurable proxies becky already has: `internal/audiosig` loudness spikes and pitch
> rises mark the argument; the completion signal in `internal/moment` marks the payoff.

---

## CAPTIONS

Read at full resolution from the burned-in frames.

- **Typography:** heavy rounded sans, ALL-CAPS or sentence case depending on the beat, thick black
  outline plus a soft glow. 2–3 lines on screen at once, roughly **2–4 words per line**.
- **One word per block is coloured**, and the colour carries meaning:
  - **cyan** on the stressed word of a *reaction* — "oh you **GUYS**", "I'm **REALLY** hungry",
    "**OKAY** hold on", "**NO!**", "**YES**", "thank **YOU**".
  - **yellow** for a *directive or the running joke* — "**french fry first**", "**spit it out**",
    "put the **BURGER DOWN!**", "we're going to **switch**".
- **Profanity is censored inside a red box** — "ALLISON WHAT THE **F***?**".
- **Emoji are used as accents**, not decoration — a 🍔 sits above "PUT THE BURGER DOWN!".
- **Placement is content-aware, not a fixed margin.** The block sits over the chest in a portrait
  shot and rides higher in the hands shot. It is always placed in a quiet area of the frame.

becky's caption pass ships one fixed style at one fixed `MarginV`, single-line, 22 chars. The line
length is compatible; **the stacking, the per-word colour, and the content-aware placement are not
there.** That is a taste feature and it is his look — it should be shown to him as an option, never
switched on by default.

---

## WHAT TO CHANGE IN becky, IN ORDER

1. **Detect the master's existing cuts and keep them.** If a source is already edited, shot
   boundaries are data, not a decision to re-make. New flag on `becky-short`; when existing cuts are
   found, `becky-cut` should tighten by ~150ms at those boundaries rather than re-cutting.
2. **Stop applying a raw-footage silence threshold to edited footage.** Measured: he removes 10%,
   `becky-cut` removes 51%. Detect which kind of source this is before choosing.
3. **Raise the frame — DONE.** `--eye-line` 0.38 -> **0.27**, measured. It only takes effect when
   the crop is narrower than the source height; on close-up 16:9 footage there is no vertical
   freedom at all (see the hard limit above). Head height cannot be forced the same way.
4. **Lock the frame by default; make a move an event.** 34% of his frames are dead still and 16 of
   22 shots barely change scale. Whatever the tracker does, the output should read as locked unless
   there is a reason.
5. **"Who is speaking" must not be the sole framing input.** Nine of 23 shots here are reaction,
   object or gesture shots. Needs the subject-of-the-sentence signal, and a way to frame a gesture
   with no face — which is what `research/reka-edge-vs-gemma4.md` recommends Reka Edge's grounded
   boxes for (`Detect: pointing hand`).
6. **Pace with the content.** Shorten shots through a conflict, hold the shot after the payoff
   lands.

## Honest limits of this analysis

- Master alignment is confident for 14 of 23 shots (≥60 RANSAC inliers). The rest matched weakly —
  the set is visually repetitive, and several shots come from moments where the master is zoomed in
  further than the vertical, which suggests **he edited the vertical from the raw footage or the
  project file, not from the rendered YouTube video**. The framing measurements above do not depend
  on that alignment; the cut-inheritance and time-removal findings use only high-confidence anchors.
- Who is *speaking* is inferred from the transcript and the burned captions, not from diarisation.
  The "shown vs speaking" split is directionally right; individual rows may be off.
- Caption colour semantics (cyan = reaction stress, yellow = directive) is a reading of ~15 samples.
  It is a hypothesis, not a measurement — worth asking him about rather than implementing.

---

# ADDENDUM 2026-08-20 — "pace with the content" is not a thing he does

An earlier pass recorded, as a to-build item: *"his shots run 0.70/0.73/1.10/0.53
through the argument, then hold 2.73s on the punchline. Accelerate in, hold the
payoff."* That was a cherry-picked subsequence. Measured over the whole clip it
does not survive, and the reason is the finding this document already leads with.

**Every shot duration in his vertical, measured by scene detection:**

    1.47 1.43 1.20 1.63 1.90 1.07 0.93 1.00 1.74 0.70 0.73 1.63
    2.74 1.43 0.87 1.00 1.23 1.27 2.86 0.84 1.00 1.83

    22 shots   median 1.25s   mean 1.39s
    thirds (median): 1.43 / 1.43 / 1.115

The two long holds are at index 12 and 18, mid-clip. **The last shot is 1.83s** —
not a hold at all. There is no accelerate-then-hold arc.

**The same window of the master (22.11–54.07s):**

    0.95 1.43 3.94 1.63 1.91 1.16 1.74 1.73 0.70 0.74 1.63
    2.71 2.33 1.00 1.24 0.83 1.57 1.63 1.24 0.83 1.00

    21 shots   median 1.43s   mean 1.52s
    thirds (median): 1.63 / 1.63 / 1.24

**The two sequences are the same shape.** Flat, flat, slightly shorter in the last
third — in BOTH. The 0.70 and 0.73 that the cherry-pick called "accelerating
through the argument" are 0.70 and 0.74 in the master, at the same position. So
is the 2.7s "hold on the punchline".

His shots are uniformly **0.18s shorter** than the master's, which is the
tightening this document already measured at 150ms per cut. That is the entire
difference.

## What this means for the build

**Strike "pace with the content" as a feature.** It described a decision he does
not make. He inherits the master's shot rhythm and tightens every boundary by
roughly the same amount, which is *exactly* what `becky-short` already does:
`planShotSpans` preserves the existing cuts and `boundaryTighten` trims a small,
even amount at each one. On the reference window becky removes 2.434s across 17
spans = **0.143s per span**, against his measured 0.15–0.18s.

becky's pacing model is already his pacing model. The remaining difference is
that he removes 10.3% of the window and becky removes 7.6% — a tightening
AMOUNT, one constant, not a missing dramatic structure.

## The method note, because this keeps happening

Both of the last two "obvious" framing/pacing features turned out to be
artefacts of looking at a subset: this one, and the `--shoulder-frac` knob that
appeared not to work because it was disconnected. The check that caught both is
the same one — **measure the whole distribution, and measure the SOURCE too.**
A number from an edit means nothing until you know what the footage handed him
for free.
