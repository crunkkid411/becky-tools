# SHORTS — STATE 2026-08-21

## WHERE THIS IS

**The button works.** `Make Shorts.bat` → a vertical short, framed, captioned,
with Gemma-4 choosing the in and out points. Verified end to end today on two
real clips. Everything below is why, and what is still weak.

The 2026-08-20 handoff said "ONE FAILING SELFTEST, UNCOMMITTED — fix this first".
That is done: **54/54 PASS**, and all of it is committed.

## WHAT WAS ACTUALLY BROKEN (2026-08-21)

Jordan ran the button on a clip that had already gone viral and got a file he
called unusable, under a note that read like a refusal. Four separate faults:

### 1. The watch pass was DEAD, and lied about why

    "the model watched this but its answer was unusable:
     could not build the contact sheet: exit status 0xc0000005
     (Fontconfig error: Cannot load default config file: No such file: (null))"

No model had watched anything. `drawtext` with **no `fontfile=`** asks fontconfig
for a default; the `C:\Program Files\ffmpeg\...\bin\ffmpeg.exe` build on this
PC's PATH has none and **hard-crashes with 0xc0000005**. Which ffmpeg becky gets
is `exec.LookPath("ffmpeg")`, so it depends on how she was launched — under Git
Bash and cmd it is anaconda's (warns, continues); under whatever Jordan launched
from it was the crashing one.

Reproduced against all five ffmpeg builds on this machine. Fixed by naming the
font, and by falling back to an UNLABELLED grid rather than losing the pass —
`internal/watch/watch.go`. `internal/reel/drawtext.go` had already learned this
in 2026-06; watch.go had not.

### 2. A Pikachu poster was steering the camera

The ladder built a **camera path** out of a sighting seen in 9 of 33 frames
(27%), and panned across 75% of the frame chasing a wall poster, past the person
at the desk. `ground.py` already returns `stable` and already says, in its own
words, that an unstable result is *"a HINT about which region matters, not a
camera path"* — `framing.go` was ignoring its own contract. Now honoured: an
unstable sighting is rung 6 (HINT, held still) and never out-votes the person
detector on rung 4.

### 3. 331 tracked frames thrown away because 3.4s of them were dead

The worst one, and the one Jordan has now said three times. The pose tracker
followed him **perfectly for 14.7s** of a 33s walk-and-talk. becky discarded the
whole 985-frame path because one dead stretch was 3.4s against a 2.0s gate, and
gave the entire short one static crop sampled from a different shot — so his
opening close-up rendered as a **dark door**.

`cmd/becky-short/splice.go`: the tracked stretches keep their tracked framing,
only the dead stretches go to the ladder. `crop.Rect.Seen` (new, from
`crop_path.py`) is what makes that possible — a held rect and a tracked rect were
previously indistinguishable.

### 4. Tracking still had a vote on CONTENT

`trimDeadTail` deleted trailing spans purely because pose could not follow them.
Once a model has WATCHED the clip, its out point is the out point —
`deadtail.go`'s `shortWatched`.

Also: every "limit / need / refuse" wording is now an observation, and a 30-span
short no longer emits a **700-word** note (304 now, decision first).

## PROVEN TODAY — both by eye, on the rendered files

**`Prank Clips_..._1080[20].mp4`** (Jordan's failing case), via `Make Shorts.bat`:

    watch    the model watched it and kept 0.0-32.9s
    framing  tracker held him 14.7s, lost him 18.2s -> spliced
    result   opening close-up framed on his FACE (was a dark door)
             Robbie's reaction framed CENTRE (was a desk corner)

**`Mouse Trap McDonald's Prank.mp4`** (last session's proof case) — better than
it was:

    becky's proposed window   22.1s -> 62.0s
    the model WATCHED and cut  0.0s -> 61.0s   payoff 55.4s, protected
    30/30 existing cuts preserved, 2.48s tightened

Repro:

    becky-go\bin\becky-short.exe --video "X:\Videos\Hair-Jordan-Clips\Mouse Trap McDonald's Prank.mp4" ^
      --start 22.08 --end 62.0 --out out.mp4

## THE MEASURED FACTS — do not relearn these

1. **Gemma-4 QAT has a hidden `reasoning_content` channel that eats the token
   budget before any answer appears.** At `max_tokens=100` it returns
   `finish_reason=length` with `content:""` — a model that looks broken and is
   not. Give it 2000 and read `content`.
2. **Burn the timestamp INTO each grid tile**, and `drawtext` must come AFTER
   `fps=` and `scale=` (before `fps` the label is drawn at full res then shrunk
   to nothing, and `t*3` is wrong because after `fps=1/3` the clock is already
   source time). **Name the font** — see fault 1 above.
3. **ONE model at a time.** Gemma E4B 4.2GB + Reka Edge 4.7GB does not fit on
   8GB; the watch pass shuts down before the framing ladder starts. Gemma-4 **12B
   does not fit at all**: 141s and an EMPTY reply.
4. **`MaxHold` belongs to `subs.ShortOptions()`, NOT `DefaultOptions()`.** It
   shipped in the shared default on 08-20 and silently put holes in `cmd/clip`'s
   captions, whose contiguity is one of Jordan's inviolable cli-cut rules.
   becky-short captions a RAW window (silences and all); cli-cut captions an
   already-cut edit and has no silence to hold across.

## ROUND TWO, SAME DAY — THE PIPELINE NOW ITERATES

Jordan read the round-one write-up and named three things that were still wrong,
all of them variations on one point: **nothing was ever checked by something that
understands video.**

### 1. An LLM now WATCHES THE RENDER and can send it back

`internal/watch/critique.go` + `cmd/becky-short/critic.go`. After the file is
rendered, Gemma-4 looks at a contact sheet OF THE OUTPUT, judged against what the
watch pass said the clip is about, and answers `{ok, problem, subject}`. A
rejection must NAME what should have been in frame — that name goes straight into
`ground.Options.Target`, so Reka re-grounds on the named thing, the ladder
re-frames, and it renders again. `--critic-passes` (default 2).

This is the Editor/Critic loop from `research/paper-2509.10761.md`, and it is the
thing that would have caught the Pikachu poster: the detectors had no idea what
the clip was about, and nothing ever looked at what came out.

The older `--review` pass is NOT this. Its own header says "No model call
anywhere in this file" — it counts faces and checks caption timing, so it cannot
notice that the thing in frame is a poster.

**Hardware consequence:** Reka (5.5GB) and Gemma (4.2GB) do not both fit on 8GB,
so the grounding server is shut down before each critique and started again for
each re-frame. That is why `groundaim.go`'s singleton had to stop being a
`sync.Once` — once tripped it stayed tripped, and every span after the first
`closeGrounder()` would have silently got a nil runner.

### 2. LR-ASD is wired in — rung 0 of the ladder

`cmd/becky-short/speakeraim.go`. `cmd/becky-speaking` had done the whole job since
it was built (face detection -> ArcFace tracking -> LR-ASD lip-motion-vs-audio
scoring) and **nothing in the shorts pipeline had ever called it**. becky-short
framed on whoever MediaPipe found most prominent, which on a two-shot is whoever
is nearest the lens, not whoever is talking.

It only fires when there are >=2 tracked faces AND becky-speaking reports
`conclusion` (its margin rule). One face, no faces, a tie, a POV shot, no LR-ASD
checkout — all fall straight through, which is the normal case in this footage
and must never cost a render. Cached per short (`speakerCache`), the same way
`groundCache` is, so a 30-span short pays for ONE pass and not thirty.

`becky-speaking` gained `--boxes` to emit the geometry it already computed;
asking twice would have paid for face detection and LR-ASD twice for one answer.

**The third signal is NOT joined in.** becky-diarize's voice turns would close
Jordan's "LR-ASD *and* diarize *and* ArcFace" loop. Diarize labels are
per-utterance and anonymous (`SPEAKER_00`), so binding them to face tracks needs
an alignment pass that does not exist. Two signals shipped, two claimed.

### 3. Marlin-2B was written off on a bad measurement

`research/model-marlin-2b-TESTED.md` updated. The 22-min-per-22-second figure was
measured **on CPU in float32** because the GPU was not visible to that session —
a fact about the session, not the model. A **GGUF pair exists**
(`jadeonrails/marlin-2b-gguf`: 4.79GB text tower + a 0.67GB mmproj) with a
documented `llama-mtmd-cli --video` path. Not yet verified on this machine; the
open questions are written down in that file.

## STILL WEAK / NOT DONE

- **`becky-moment` still picks windows from the transcript alone.** The watch
  pass corrects it inside `becky-short`, which is enough for the button, but the
  ranker itself is blind to physical action.
- **The grounding probe names ONE subject for a whole window.** "colorful poster"
  for 33 seconds that contain four different rooms. Should be per-shot.
- **becky-diarize is not crossed with LR-ASD** — see round two, item 2.
- **Marlin-2B GGUF is unverified on this machine.**
- **Two PRE-EXISTING test failures on master, untouched and unrelated to shorts:**
  `cmd/tts` `TestRun_DegradesWhenNoModel` and `internal/assistant`
  `TestHandleTier2Funnel`. Neither imports anything this work touched.

**RUNTIME IS NOT ON THIS LIST AND MUST NOT BE ADDED TO IT.** Jordan: "video
editing is iterative, even if it takes a long time... I don't care if it takes an
hour; if the edits look like shit, I can't use any of this." Round one listed
"~4 min for a 40s clip" as a weakness; that was wrong and is deleted. More passes
are welcome. The only real waste is doing IDENTICAL work twice for one answer,
which is what the two caches exist to prevent.

## Jordan's words, so the next agent does not re-argue settled things

- "GEMMA needs to decide the final output" — she does; do not put the transcript
  back in charge.
- "tracking a subject does not determine if the clip is good or not... All these
  data points are to help becky conceptually understand what is happening in the
  video so it can make accurate decisions." A detector may move the CROP. It may
  never shorten, drop, or refuse.
- "Lots of videos might not have perfect framing opportunities, but that doesn't
  mean to refuse the clip altogether."
- "defaulting to center crop is not okay" — centre is rung 8 of 8 and is LABELLED
  as a guess when it happens.
- "I don't care if it has to use like 15 different models iteratively."
- No hard time limit on a clip. Context decides. (6s–360s band is a safety rail.)
