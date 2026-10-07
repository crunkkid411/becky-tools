# Visual moments: context-based cuts (2026-10-07, local)

**Status:** BUILT as `becky-go/cmd/livestream/moments.go` (replaces `expressions.go`), measured on
the 27-livestream (`X:\Videos\2026\09_sept\27-livestream`, stream
`2026-09-26_SOME_of_my_videos_are_back_[17VOWCqJows].mp4`, Claude project). Dry run in progress at
the time of writing - see "Open" at the end.

## The problem (Jordan, 2026-10-07)

> "the overall context of the cuts are still not understood - the extreme facial expressions were
> just one example of that... focusing on that issue DID resolve it... but it left other visual cues
> ignored, and that's the ROOT issue we need to fix."

> "that 'hi' followed by a hand wave should have been enough motion to trigger a bunch of small
> vision models to determine what is going on specifically, and then have gemma or qwen decide to
> leave it or cut it"

> "vision models meaning like the falcon perception and media pipe and all that data stuff"

His examples (stream times), all checked in frame strips:

| Line | What he does | Frames | What the edit did |
|---|---|---|---|
| "I'm livestreaming" | big excited face | 3:10.6-3:11.5 | restored by v2's face check |
| "Some of my videos got restored" | big excited face | 3:15.8-3:16.7 | restored by v2's face check |
| "but not all of them" | drops his head in defeat (face hidden) | 3:17.6-3:18.6 | becky-cut cut 3:18.03-3:18.7 - mid-gesture |
| "...still allowed to livestream" | thumbs up | 3:22.2-3:23.0 | the section ended 3:22.27 - cut |
| "Hair Jordan, hi" | both hands up, awkward wave | 14:56.0-14:56.8 | becky-cut cut 14:56.27-14:56.9 - the wave |

He also approved cutting the checked breaths: "the breaths identified are spot on - definitely
those should all be removed". Built as `cutBreaths` in `breath.go`.

## What each small model sees (per 0.1 s frame, `picture_signals.py`)

New fields (cache bumped to `<video>.picture-v3.json`): MediaPipe Gesture Recognizer
(`models\mediapipe\gesture_recognizer.task`, was on disk and unused), all Face Landmarker
expression scores (brow, eyes, smile, frown, pucker, funnel, puff), head pitch/yaw from the face
mesh matrix, pose nose + elbows.

| Moment | Signals |
|---|---|
| Thumbs up 202.2-203.0 | Thumb_Up 0.51 -> 0.72 on 7 frames; motion 0.2-0.3 |
| Head drop 197.7-198.5 | face mesh LOST him for 9 frames while pose still tracks the body (insightface still "finds" a face in the hair); pitch -12 -> -22 before it; motion 0.22 |
| Wave 896.2-896.8 | motion 0.53 / 0.46 / 0.48 shoulder widths a frame (talking stays under 0.2); Open_Palm 0.57 |
| Excited faces | jawOpen 0.79-0.81 held; insightface mouth 0.25-0.32 |

Pitch sign on this camera: looking down makes it MORE negative (the helper's doc says so).
Wrists are NOT above the shoulders in the wave (chest height) - the old "hands up" rule would miss it.

**The trigger** (`momentSignals`): motion 0.3+, any gesture 0.5+, face mesh lost while pose has
him, jawOpen 0.6 / smile 0.5 / brow 0.5 / eyes 0.4 / pucker 0.6, both face models' held mouth.
2+ active frames or any gesture = Gemma watches. On the 27-livestream: 41 stretches cut next to his
words (45 s in total), 21 trigger. Most "something" is hair fixing, chat reading, drinking - which is
exactly why a small model only TRIGGERS and Gemma names the action.

## Gemma: E4B cannot do this, 12B can

Probe (`cmd/_momentprobe`, scratch) on the same windows, both on the processor:

| Question | E4B | 12B |
|---|---|---|
| head drop 197.0-199.0, per frame | "Head level, neutral expression" x8 | "Head: down" 197.5-198.5, level before/after |
| "can you see his eyes at 198.2?" (face hidden by hair) | "Yes" (but "bright green hair, black t-shirt" correct - it sees the picture) | - |
| excited face 190.4-191.6 | (called "sitting still" in the run) | "Wide-eyed surprise, mouth open" |
| wave 895.8-897.2 | - | "hands raised and open" 896.3, "moving back down" 896.5 |

Time: ~1.5 min for 8 frames on the processor, E4B and 12B alike. So moments always use
**Gemma-4 12B on the processor** (`momentRunner`); E4B is only the fallback when the 12B files are
missing (the report says so).

## How Gemma is asked (each lesson cost a run)

1. **"Keep or cut?" asked directly:** E4B saw both excited faces ("he is not talking and has a big
   facial expression") and cut both - it treats silence as a reason to cut. Same lesson as the
   content calls (small models: ask the parts, Go applies the rule, 33% -> 96%).
2. **One label per stretch:** lost the thumbs up - he reaches for his water right after it and
   "object" won the stretch. Now Gemma lists each separate action, in order.
3. **Label alone:** 12B called hands going to his hair before "yes, update" "raising both hands near
   his head" = gesture, and the stretch went back. Now each action also says `fits_line` (goes with
   what he says just before / after) and both must hold.
4. **Times as strings:** Gemma writes `"185.2s"`; the `seconds` type reads numbers, strings and
   "s" suffixes. An action it cannot place takes the whole stretch back only when it is the ONLY
   action.
5. **First describe each frame** (head, face, hands), then the JSON line - the answer is parsed from
   the last `{"actions"...}` object.

Rule in Go (`judgeActions`): reaction / gesture / acting AND fits_line -> put back that action's
span, padded 0.1 s, on the frame grid, inside the stretch, slivers of 2 frames absorbed, under 4
frames dropped. Grooming / looking away / object / still stay cut. Marker: "Kept for the picture -
<what> (Gemma, next to "<words>"; small models: <signals>)".

## Memory

Claude Code stopped the dry run twice "because the system is running low on memory": Gemma 12B on
the processor with the default 16384 context reserves several GB of cache on top of ~7 GB of
weights. `avlm.Runner.CtxSize` (new, 0 = 16384) is 8192 for moments, and a window is capped at 20
frames (`momentMaxFrames`; long pauses lower the rate). Close VEGAS before a run if memory is tight.

## Results so far

Dry run 5 (12B, per-action labels, before `fits_line`), stream times:

| Stretch | Gemma | Result |
|---|---|---|
| before 3:05.2-3:06.9 | gesture (hands near head), looking away, grooming | PUT BACK - **wrong** (hair fixing) -> fixed by fits_line |
| pause 3:10.6-3:11.5 | reaction: surprised face, mouth open | PUT BACK - right |
| pause 3:15.8-3:16.7 | reaction: holding mouth open, surprised | PUT BACK - right |
| pause 3:18.0-3:18.7 | acting: dropping his head | PUT BACK - right |
| after 3:22.27-3:24.27 | gesture: thumbs up; then object: water bottle | PUT BACK 3:22.27-3:22.9 only - right |
| after 3:45.8-3:48.3 | grooming, then drinking | cut stays - right |
| pause 11:19.3-11:19.9 | gesture: right hand toward the camera | PUT BACK - not yet checked by eye |
| before 12:57.9-12:59.9 | looking at paper, then pointing up | PUT BACK 12:59.8-12:59.93 - not yet checked |
| pause 13:05.6-13:08.7 | still / moving the notepad | cut stays |

The run was stopped for memory before the wave (14:56). Dry run 6 (fits_line + 8192 context) was
started 2026-10-07 and is the one to check.

## Model types considered

- **Facial-emotion classifiers** (Hugging Face, searched 2026-10-07): the field is 2024 hobby
  fine-tunes (`*/vit-Facial-Expression-Recognition`, ~1k downloads); MediaPipe's expression scores
  + Gemma 12B already cover it. Not installed.
- **Reka Edge** (on disk): its strength is WHERE (grounded boxes); per-frame grounding of a small
  target is not trustworthy (`research/reka-edge-vs-gemma4.md`). Not needed for "what is he doing".
- **Falcon-Perception** stays in the breath check (held object, size); not used for moments.
- **Qwen VL:** the local Qwen3.5-4B is the content-call model; no local Qwen VL video model was
  tested here. Gemma-4 12B does the job.

## Open

- Check dry run 6's calls by eye (frame strips), especially 11:19.3, 12:59.8 and the wave at 14:56.
- Then the real run into VEGAS (Gemma's answers are cached, so it is fast) and Jordan's review.
