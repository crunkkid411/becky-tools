---
name: becky-video-editing-rules
description: "Jordan's rules for editing or clipping video with becky-tools: livestream edits, rough cuts, shorts, timelines, VEGAS markers and regions, renders, captions, ffmpeg drawtext, becky-cut, becky-short, becky-livestream. Read before cutting, clipping, rendering or placing anything on his timeline."
---

# Video editing and clipping rules

Moved verbatim out of the always-loaded CLAUDE.md files on 2026-10-10 so it is read only when this kind
of work comes up. It has the same authority as CLAUDE.md.

## Invariants (moved from becky-tools/CLAUDE.md section 4)

- **"CLIPPING" and "EDITING" ARE DIFFERENT JOBS — use his words correctly.** Jordan, 2026-08-21:
  *"Since this run is focused on editing footage that has already been edited, it's referred to as
  clipping (whereas editing is generally used when discussing raw footage, or when specific edits or
  revisions are requested)."* This is not vocabulary, it changes the defaults. **CLIPPING** takes a
  FINISHED video and INHERITS its cuts — his own vertical short kept 8 of the master's cuts
  frame-exact and removed only ~10% of the running time. Running a silence threshold over an edited
  video and re-cutting it is the wrong job and produces shredded output. **EDITING** is raw footage,
  or a specific revision he asked for. The full canon for the clipping pipeline — every tool, every
  flag, the framing ladder, the critic loop, his measured edit standard, the traps, and which
  research doc already answered which question — is **`SKILL.md`'s `VIDEO CLIPPING` section**. Read
  it before touching `becky-short`/`becky-moment`/`becky-hits`.
- **EDITING IS ITERATIVE. QUALITY IS THE ONLY BUDGET. STOP OPTIMISING FOR SPEED.**
  Jordan, repeatedly, most recently 2026-08-21: "I continue telling you that video editing
  is iterative, even if it takes a long time... I'm a world class video editor and I don't
  care if it takes an hour; if the edits look like shit, I can't use any of this." He is
  the one who watches the output. A render that takes an hour and is usable beats one that
  takes four minutes and is not. So: never list runtime as a "weakness" of an editing
  pipeline, never drop a model or a pass because it is slow, and never reject a model on a
  timing measured under the wrong conditions (Marlin-2B was written off at 22 min/22s — on
  CPU, because the GPU was not visible to that session; that is a fact about the session,
  not the model). Firing the same model up MORE THAN ONCE at different steps is explicitly
  fine: "Even if Gemma4 or other models have to be fired up more than once at different
  steps in the pipeline that is okay!" The ONE thing that is still waste is doing identical
  work twice for the same answer — pay for more passes, not for repeated passes.
- **AN LLM MUST WATCH THE OUTPUT BEFORE IT SHIPS.** Detectors do not understand the video;
  they lock onto posters, doorways and empty sofas and the file plays fine. Jordan: "an LLM
  needs to verify all of that - we're not picking random dumb data points and rendering
  that shit; quickest way to get someone fired. I re-watch a video clip like 10 fucking
  times before I hit render... If gemma4 had just been made to watch the goddamn output
  when it was focused on the pikachu poster it would have said 'oh wait, that isn't
  right'." So any render pipeline ends with a model LOOKING AT THE RENDERED FILE, judged
  against what the clip is about, with the power to send it back — and its rejection must
  NAME what should have been in frame, or it is not actionable
  (`cmd/becky-short/critic.go`, `internal/watch/critique.go`). A deterministic check over
  the output is not this: becky-short's older `--review` looks at the output too and its
  own header says "No model call anywhere in this file" — it counts faces, so it cannot
  notice the thing in frame is a poster.
- **A DETECTOR IS A SIGNAL, NEVER A VERDICT ON THE FOOTAGE.** Jordan, three times now:
  "tracking a subject does not determine if the clip is good or not... All these data
  points are to help becky conceptually understand what is happening in the video so it
  can make accurate decisions." A pose tracker that cannot follow a person across a room
  is reporting its own limits, not the clip's. So: a failed/partial detection may change
  WHERE THE CROP POINTS and nothing else — it may never shorten a clip, drop a span, or
  refuse a render. Concretely, and each was a real bug: (1) never discard a whole pose
  path because one stretch of it is dead — splice, keep the tracked seconds
  (`cmd/becky-short/splice.go`); (2) once a model has WATCHED the clip, its in/out is the
  in/out — no tracker-driven trim revises it (`deadtail.go`'s `shortWatched`); (3) honour
  `ground.Result.Stable` — an unstable sighting is "a HINT about which region matters, not
  a camera path" in ground.py's own words, and ignoring that panned a short across a
  Pikachu poster instead of the person. Word every such note as an OBSERVATION ("the
  tracker lost him for 3.4s"), never as a refusal ("limit 2.0s") — Jordan reads these.

- **NAME THE FONT in every ffmpeg `drawtext`.** With no `fontfile=`, drawtext asks
  fontconfig for a default, and `C:\Program Files\ffmpeg\...\bin\ffmpeg.exe` — which
  is on this PC's PATH and which `exec.LookPath("ffmpeg")` will pick depending on how
  becky was launched — prints `Fontconfig error: Cannot load default config file: No such
  file: (null)` and then DIES with `0xc0000005`. Measured against all five ffmpeg builds
  on this machine (2026-08-21): anaconda's warns and continues, that one hard-crashes.
  It silently killed the whole Gemma watch pass, which then reported "the model watched
  this but its answer was unusable" about a clip no model had ever seen.
- **A MARKER (OR ANY PLACED ARTIFACT) IS AN ASSERTION, NOT A GUESS. NEVER SHIP A GUESS IN THE
  VISUAL FORM OF A FACT.** 2026-08-26: 56 of 73 quote markers were positioned on Jordan's timeline
  by fuzzy lexical match against his narration. A marker sitting at 00:17:30 *reads* as "this quote
  belongs here" - it does not read as "a text-similarity score put this here". He called them
  **"worse than un-helpful; they genuinely are dishonest in a way that cost me time and brain
  capacity"** and deleted all of them. He is right: the dishonesty is structural, not a matter of
  intent, because the FORM of the output claims more confidence than the METHOD earned. A
  `confidence` field in a sidecar JSON does not fix it - he sees the timeline, not the sidecar.
  **The rule: if you cannot place it with real confidence, do not place it.** Hand over the list
  instead, or park every item in one clearly-labelled block. Distributing guesses across a timeline
  where each one looks authoritative is worse than delivering nothing, because now he has to verify
  all of them. Same applies to regions, chapter marks, auto-generated captions and detected labels.
- **EXTRACT WHAT THE HUMAN WROTE, NOT WHAT YOUR FILTER LIKES.** Same incident: the quote extractor
  silently applied a 12-character minimum, skipped markdown heading lines, and only matched
  straight `"` - dropping 8 of Jordan's own quotes (`"Elkhart"`, `"vote"`, `"wait, what?"`). He had
  literally put them in quotation marks and reasonably expected all of them back. **Filters the
  human did not ask for are data loss.** Take everything, verbatim; if something is malformed
  (this file had 4 lines with an unclosed `"`), REPORT the anomaly, never silently drop it.
- **USE THE SPECIALIST'S TOOL FOR THE MECHANICS; WRITE ONLY THE CALIBRATION.** Jordan, 2026-08-26:
  *"most of the tools we need already exist - generally created by specialists who solve their own
  niche problems, and we just need to wire them up and calibrate accordingly."* He is right, and
  this rule exists because an agent ignored it and shipped a bug the specialist tool had already
  solved. The rough cut was written as a from-scratch detector emitting SECONDS; `auto-editor`
  (already installed, already wrapped by `becky-cut`) emits INTEGER FRAME chunks
  `[start, end, speed]` and therefore cannot produce the off-grid cut points that put random
  one-frame gaps on Jordan's timeline. A year of his field use had never produced that bug.
  **The boundary: the existing tool owns the mechanics (frame grid, chunking, margins, export);
  we own ONLY the thin layer it genuinely lacks.** Concretely here, `becky-cut` already had the
  right SHAPE (measure level -> threshold -> auto-editor cuts -> Silero VAD post-pass) and only
  its ESTIMATOR was wrong: `cmd/cut/level.go` derives the threshold from `mean_volume` and clamps
  it at `minThresholdDB = -50.0`, so on Rode Wireless GO II footage it picks ~-41 dB where ~-62 dB
  is needed - **+20 dB wrong, and unreachable at ANY `--headroom` because of the clamp.** The
  correct fix was ~40 lines (swap in an Otsu threshold, drop the clamp), not a new detector.
  **Two failure mechanisms to watch for in yourself:** (1) a DIAGNOSTIC quietly grows into the
  PRODUCT - you write measurement code to prove a bug exists, then start emitting results from it
  without ever deciding to; (2) you blame the ENGINE for the WRAPPER's error - "becky-cut failed"
  is evidence about becky-cut's calibration, not about auto-editor. And note the converse trap:
  wiring up a tool silently inherits its assumptions (becky-cut inherited "the threshold is
  absolute"), so wire it up, then find the ONE assumption your input violates and put your new
  code exactly there.

## LESSONS (moved from X:\AI-2\CLAUDE.md)

- Never apply a blanket edit-quality fix to Jordan's timeline on your own judgment (e.g. merging becky-cut's 1-3 frame jump cuts): mark ~5 examples on the timeline for him to examine first - he is the editor (apology livestream, 2026-10-05).
- Never label a sound you have not identified: becky-livestream's "Breath example" markers were just loud gaps with no words; a sound labeler plus frame checks showed most were his voice, hand gestures or big movements (27-livestream, 2026-10-06). Name a sound only from a model that labels sounds, and check the picture.
- Check every region becky places the way Jordan USES it - one script ripple-deletes everything inside the regions: edges on the project's frame grid, auto-editor-style margins off the words, no 1-frame slivers. I checked the breath regions' times only; they sat between frames and one clipped the next word (27-livestream, 2026-10-06).
- Never end a kept piece or cut a quiet stretch from the transcript and loudness alone: the 27-livestream edit kept "cheers, water cheers" but cut him off before the toast, and cut an exaggerated face after "Some of my videos got restored". Look at the picture (motion, mediapipe, lrasd) before cutting (2026-10-06).
- Use the small specialist models TOGETHER with Gemma, each for what it measures best, and never drop one as "not needed": Falcon for where and how big an object is (Gemma is poor at that), MediaPipe for face expression scores (jawOpen), raised objects and body, insightface for the face box and mouth. Jordan, 2026-10-06: combining them is proven more accurate than any one alone - I had used MediaPipe only for shoulders and hands.

- Never judge Jordan's edit by my own taste and re-tune the rules mid-run: he is the judge of the work. Build it, mark every call (e.g. each moment put back) on the timeline, and let him decide; I was calling Gemma's put-backs 'wrong' and planning fixes he never asked for (visual moments, 2026-10-07).
- A decision model is ONE signal, never the final say: every unsure call goes to the other models (Gemma, Qwen, picture signals), and an LLM reads the whole kept timeline in context before it ships. I let System One decide alone on the 27-livestream and put "unsure 65%" markers on his timeline (2026-10-08). Never put a bare model score on his timeline: resolve it, or say what each model said.

- To add a model to an established workflow, insert it INTO the proven path first (e.g. --model qwen: lead + targeted Gemma review) and keep everything else the same; a new decision design comes second, side by side. I built --model systemone as a new vote+readers design and Jordan had to re-explain core principles (2026-10-08).

- In a becky stack, a specialist model (System One, MediaPipe, Falcon...) is a DATA POINT handed to the lead LLM, which trusts it unless the context says otherwise and makes the call; Gemma then reviews as usual. Never make a specialist a voter or tie-breaker unless Jordan asks for voting (systemone-stack, 2026-10-08).
