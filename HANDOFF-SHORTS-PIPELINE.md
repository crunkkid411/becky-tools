# HANDOFF — short-form repurposing pipeline: why it would fail, and what it takes

**Status, 2026-08-19 (late). All six stages are built and every §7 item below is closed
or explicitly deferred.** Drop a video or a folder on `Make Shorts.bat`: it transcribes if
needed, picks moments (structure + content + audio + face coverage), decides who is
speaking, preserves the source's own cuts where it has them, reframes to 9:16 and burns
captions.

**The standard this is now measured against is `research/jordan-edit-reverse-engineered.md`
— read it before changing framing or cut timing.** Jordan supplied his own 30-second
vertical short and the long-form it was cut from; because the vertical is a crop of the
master, his decisions were recovered as numbers. Three of them overturned assumptions this
document was built on:

- **He inherits the cuts, he does not choose them.** 11 of 14 aligned cuts land within
  100ms of a cut that ALREADY existed in the long-form; 8 are frame-exact.
- **He removes 10.3% of the time — 150ms per cut.** `becky-cut` alone removed 51%.
- **His cuts land on WORDS** (median 57ms from a word boundary), not on silence.

Read `research/shorts-models.md` first — it is the model research this rests on. This
document is the *engineering* half.

**TEST ON THE RIGHT FOOTAGE.** `X:/AI-2/becky-tools/test-for-clips.mp4` is Jordan's own
content and is what editing work is judged on. **`E:\TakingBack2007` is the CRIMINAL CASE
evidence folder, NOT his content** — shorts were demoed from it once and that was wrong
twice over (wrong footage, wrong style). Style guidance lives in
`hair-jordan-personality-profile.md` and `hair-jordan-content-analysis.md`.

**Jordan's decisions — settled, do not re-litigate:**
- **MediaPipe and OpenCV are IN.** *"we NEED to use them. weve been cutting corners and
  all that does is waste my time, not save time."* Both installed and verified (§7 Step F):
  `cv2` 4.13.0 was already there, `mediapipe` 1.0.1 added. **gocv is dropped** — Jordan's
  call, 2026-08-18: MediaPipe has no Go binding, so stage 4 is a Python helper regardless.
- **Free models only.** One allowlist, no override flag (§6).
- **SELF-ORCHESTRATION IS NOT OPTIONAL.** *"why the FUCK is it trying to make ME use
  becky-transcribe or whatever the fuck it wants before it will work - that is WRONG. drag
  and drop. if no .srt, it needs to FUCKING MAKE ONE."* No tool in this chain may ever stop
  and ask him to run another tool first (§5.4).
- **This is OFFLINE EDITING, not a realtime preview.** *"make it do a SECOND PASS for
  christ's sake - or 100 fucking passes, I don't give a shit... I literally go frame-by-frame
  if I have to."* Anything that trades quality for speed here is the wrong trade (§5.6).
- **Multiple signals, and the transcript is one of them.** *"transcript is CERTAINLY one of
  them. but with more context."* Not a replacement — an addition (§5.7).
- The GitHub/CI failures are **not** cloud's to fix — that is the local agent's lane.

**Bugs found by looking at rendered frames, not at exit codes.** Every one of these shipped
a file that plays, which is §2.1 exactly:

| Bug | What Jordan would have seen |
|---|---|
| `--folder` labelled moments with the **wrong source video** | reels cut from the wrong footage |
| "Incomplete arc = **VETO**" was a 0.6 multiplier — a trailing-off 90/99 clip beat one that lands | shorts with no payoff, ranked top |
| `CoverageIn` returned the **span** between sightings, so 3 glimpses in 20s scored 1.000 | the wrong person framed |
| Crop framed on the **torso centroid**, so leaning over sheared his head off the edge | half a frame of empty wall |
| Crop **scale** came from shoulder width, which collapses when he leans | a close-up of the top of his head |
| A `None` reaching the median filter killed the helper and it **silently fell back to a static centre crop** | quietly worse shorts, `followed=false` ignored |
| The render **capped the camera path at 48 keyframes** and truncated — freezing the crop for the rest of the shot | the tracker appearing to give up |
| The smoother was **causal**, so it could never catch up | the lag he rejected the whole thing over |
| MediaPipe reported a person with **confidence 1.00 inside a glass jar** | a short framed on a lamp |
| Average coverage **hid a 4.2s dead patch** where he was out of shot | seconds of stale crop mid-clip |
| `Make Shorts.bat` only accepted a **folder**, so dragging a clip printed "No folder given" | the launcher simply not working |
| LR-ASD faces were preprocessed **2x zoomed out from its training crop** | confident, wrong speaking scores |

All fixed, each with a regression check. The veto's property test immediately found a
further hole: an incomplete clip whose signals *also* disagreed hit the Disputed branch
first and escaped the veto entirely.

## 1. What Jordan asked for

Repurpose long videos into social shorts. He has tried virtually every AI tool on the market
and found quality consistently subpar. He does not have time to do it by hand.

The target chain is six stages:

    transcribe → find the moment → verify it → find the subject →
    decide who is speaking → reframe to 9:16 + render

becky is strong at stages 1–3 and has **nothing** at 4–6 (see `research/shorts-models.md` §2).

---

## 2. Why I think this would fail — and it is not the models

Jordan's own words: *"becky-tools is a hacked together shit show that mostly doesn't work.
Likely not due to model choices, however, robust workflows have to be robust at every stage."*

That is the correct diagnosis, and the repo's own history proves it. Every one of these is
recorded in `STATE-OF-MASTER.md` / `HANDOFF-LOG.md`:

| What happened | What kind of failure |
|---|---|
| `becky-resolve`, `becky-presence`, `becky-case` all **compiled and passed unit tests** but were broken at runtime on a real file | seam |
| Two of them passed `becky-validate --variant <x>` — **a flag that does not exist** — so the Gemma escalation ladder silently never fired | seam |
| `becky-identify` was invoked **without its required `--kb`**, so naming always degraded to nothing | seam |
| `becky-case` — "the one dumb call" — **ran nothing at all** on a bare `--file` | seam |
| `build-all-tools.bat` contained literal `0x08` bytes, so `becky-review-engine.exe` **silently never rebuilt** | seam |
| `becky-reel render` drifts **+1.27 s over 88 cuts** (30.0 fps against 29.97 sources, no per-clip frame quantisation) and nothing catches it | seam |
| `CLAUDE.md`, on a failed forensic task: *"the tools worked, the agent's chaining didn't"* | seam |

**Not one of these is a model failure.** Every one is a seam failure — a wrong flag, a missing
argument, an unchecked assumption about what the previous stage returned. The models did their
jobs. The wiring between them was wrong, and the test suites stayed green throughout because
they tested **units, not seams**.

### 2.1 Why a shorts pipeline is the worst possible place for this

The forensic tools mostly fail **loudly** — a wrong name, a missing hit — and Jordan notices.
A shorts pipeline fails **silently and plausibly**. Every one of these produces a file that
plays:

- ASD picks the wrong face → a clip of the wrong person talking. Renders fine.
- Crop path smoothing is over-damped → the subject drifts out of frame. Renders fine.
- Transcript timing is off by 300 ms → captions desync. Renders fine.
- The moment LLM picks a clip that trails off mid-setup → a short with no payoff. Renders fine.
- `becky-reel`'s existing fps drift → captions ~38 frames out by the end. **Renders fine.**

Jordan will not get an error. He will get 40 shorts that are subtly bad, and no way to tell
which stage did it. That is *exactly* the "quality is always subpar" experience he has already
had with every commercial tool — and becky would reproduce it faithfully unless each stage can
say what it did and how sure it is.

### 2.2 The compounding-error arithmetic

Six stages at 90% each is ~53% end to end. At 95% each it is ~74%. There is no model on the
shortlist that fixes this; only per-stage verification and honest refusal do. becky already has
the doctrine for this (`FORENSIC-OUTPUT-PHILOSOPHY.md`, corroborate-then-conclude). It has
simply never been applied to a *rendering* pipeline, only to reporting ones.

### 2.3 The vision-layer root cause

`research/shorts-models.md` §6 documents this in becky's own source comments: the vision layer
is largely the **degrade path** of a stack that was never fully built, because the heavy CV
option was repeatedly rejected as *"cgo + native OpenCV cannot be built or tested [by cloud]"*.

Stages 4–6 are precisely the stages that need that dependency. **If this build follows the same
pattern, it will produce another pure-Go approximation that compiles, passes tests, and reframes
badly.** That is the single most likely failure mode of this project, and it is a process
failure, not a technical one.

---

## 3. What it would take — the preconditions

These are conditions, not steps. If they are not agreed, do not start.

### 3.1 Fix the render-drift bug first, in isolation — DONE (§5.1)

`becky-reel render` drifts +1.27 s over 88 cuts. A shorts renderer sits directly on top of it,
and captions are the most visible element of a short. Fix + regression-test the fps quantisation
**before** anything is built on it. This is a small, self-contained, independently verifiable
task and it is the correct first commit.

### 3.2 Build the native dependency properly — SETTLED 2026-08-18

**Resolved, and the answer cost nothing.** OpenCV (`cv2` 4.13.0) was **already installed** in
the interpreter becky uses for face work — a year of routing around "we cannot have OpenCV" was
one `import cv2` away. MediaPipe 1.0.1 installed in minutes beside it. **gocv is dropped** on
Jordan's call: MediaPipe has no Go binding, so stage 4 is a Python helper regardless, and
routing OpenCV through cgo would split one per-frame stage across two languages for no gain.
That matches `CLAUDE.md`'s stated architecture — Go binaries, heavy ML in thin Python helpers.
No accuracy was traded away; see §7 Step F and §5.6.

### 3.3 Every stage declares a confidence and is allowed to refuse

Non-negotiable, and the thing that separates this from the tools that disappointed him. Each
stage emits its result **plus** a confidence and the basis for it. A stage below threshold
returns "I could not do this" and the pipeline **skips that clip** rather than rendering
something plausible. Thirty good shorts and ten honest refusals beats forty shorts where six
are quietly wrong — because the second case costs Jordan a full manual review of all forty,
which is the time he does not have.

### 3.4 Seam tests, not unit tests

This is the direct antidote to §2. For every tool-to-tool boundary:

- A test that runs **the real adjacent binary** and asserts on its **actual output**, not a mock.
- Every CLI flag one tool passes to another asserted to **exist** in the receiving tool. The
  `--variant` bug was a string that no test ever checked against the real flag set.
- Every **required** flag of a called tool asserted present at the call site. That is the
  `--kb` bug.
- Assert **values, not truthiness** (`STANDARDS-ENGINEERING.md` already mandates this — it was
  not followed).

### 3.5 One human gate, early, on real footage

Per `CANVAS-NORTH-STAR.md`'s Definition-of-Done: "it compiles" is not done. The gate is Jordan
watching **one** 9:16 short cut from his own footage and saying whether the framing looks
edited or auto-generated. That judgement cannot be automated and it must happen before stages
5–6 are tuned, not after.

---

## 4. Build order (each step independently valuable)

Ordered so that every step ships something usable even if the next never happens — the direct
counter to becky's "half-built stack" pattern.

| # | Step | State | Where |
|---|---|---|---|
| 0 | `becky-reel` fps quantisation | **DONE** | §5.1 |
| 1 | `becky-moment` — moment selection, now self-transcribing | **BUILT + RUN on his footage** | §5.2, §5.4 |
| 2 | Face **tracking** — persistent track IDs via IoU + ArcFace | **BUILT**, detector now emits all faces, Go wiring open | §5.3 |
| 3 | **LR-ASD** speaking decision | **MODEL WORKS**, not wired into a command | §5.5 |
| 4 | Crop path — MediaPipe Pose + OpenCV, zero-lag | **BUILT + VERIFIED on his footage** | §5.6 |
| 5 | Render 9:16 (`becky-short`) | **BUILT + VERIFIED**, captions not burned in yet | §5.6 |
| 6 | Audio signals as a third ranking signal | **BUILT**, does not reorder yet | §5.7 |

**Note the shape:** step 1 alone — moment selection over transcripts becky already produces,
with no reframing at all — already gives Jordan a ranked list of clip windows he can cut in
Vegas in a fraction of the time. That is real value from the stage becky is *already*
strongest at, and it did not depend on any of the hard parts landing.

---

## 5. Current state — what is built, with evidence

### 5.1 Step 0 was already done (STATE-OF-MASTER is stale)

The +1.27 s / 88-cut drift is **already fixed on master** and I did not rebuild it.
`internal/reel/args.go` carries `framesFor` (quantises each clip to whole output frames),
a per-clip `trim=end_frame=N` in the filter graph, `segmentDur`/`segmentReadPad`, and
microsecond-precision `formatSeconds`. The doc comments cite the measured
"+1.27s / 38 frames over an 88-clip reel" as the bug they fix.

Verified: `TestFramesFor_QuantizesToWholeFrames` and
`TestBuildFilterComplex_TrimsEachSegmentToExactFrameCount` pass, and
`TestRender_FrameCountMatchesReelExactly` renders through real ffmpeg and counts frames
(it SKIPs without ffmpeg, correctly).

**Action for local:** update `STATE-OF-MASTER.md`, which still lists this as an open bug.

### 5.2 Step 1 — `becky-moment` BUILT

New: `internal/moment` (core) + `cmd/becky-moment` (CLI) + `cmd/becky-moment/zen.go` (judge).

The design point that matters, and the reason this is not another auto-clipper:

- **`internal/moment` decides STRUCTURE deterministically** — where a thought starts, whether
  it completes, whether the opening dangles mid-setup, pace, length fit. No model. Same input,
  same output.
- **A model decides CONTENT** (is this actually interesting?) through `JudgeFunc`.
- **`Rank` corroborates the two.** Agree → `conclusion`. Disagree → `disputed`, **held at the
  lower signal, never averaged**. One signal only → `candidate`, never presented as a pick.

Two details worth knowing before changing it:

1. **The pause threshold is DERIVED, not constant** (`AutoThoughtGap`, p75 of the transcript's
   own inter-cue gaps). This is the Parakeet lesson from 2026-07-19 applied at cue scale: 49%
   of Parakeet's words carry `end == start`, so a constant tuned on another ASR shatters a
   Parakeet transcript. There is a regression test asserting the threshold *tracks the data*
   rather than behaving like a constant.
2. **An LLM verdict of "incomplete arc" is a VETO, not a score.** A 90/99 clip that trails off
   ranks below a modest one that lands. That is the whole "clips that trail off mid-setup"
   problem, and it is enforced in `Rank`, not left to the prompt.

**Offline proof cloud RAN** (`becky-moment --selftest`, no model/media/network) — **13/13 PASS**,
including that a dangling "So he said…" opening scores below a clean declarative hook, that a
disagreeing verdict is held rather than averaged, and that both spending guards refuse before a
request is built.

**Real CLI run** — note the original was on a **synthetic** `.srt` written in the same
session, not on Jordan's footage. It has since been run for real: 177 transcripts from
`E:\TakingBack2007` -> 112,625 candidates -> ranked moments with a local content pass
(§6.1). The synthetic run, for the record: 8 cues → 17 candidates → top pick
`00:00:06.500 → 00:00:27.000` (hook "The thing nobody tells you about running a studio is the
cashflow", build, payoff "I nearly went under twice before I figured that out"), correctly
labelled `candidate` with the note *"the content pass did not run"* because no API key was set.
The degrade path is proven, not asserted.

**Seam test** (`cmd/becky-moment/main_test.go`): it **parses `cmd/becky-hits/main.go`** and
asserts every JSON key becky-moment emits is one becky-hits actually reads. Rename a field
there and this fails here. This is the §3.4 precondition made real, and it is the exact class
of bug that produced `becky-validate --variant` — a flag that did not exist, passed by a tool
whose unit tests were all green.

### 5.3 Step 2 — `internal/facetrack` BUILT, unproven on footage, with one honest gap

**It has never seen a real face.** Zero importers in the repo; every test runs against a
synthetic detection generator. Treat it as compiled-and-unit-tested, not proven.

**Bug found and fixed 2026-08-18:** `CoverageIn` returned `(last-first)/(t1-t0)` — the
SPAN between the outermost sightings — while its own doc comment claimed it "reflects
where the face was actually SEEN, not merely that the track spans the window." A face
glimpsed three times across 20 seconds scored **1.000**, identical to one detected in
every frame, and Step E's acceptance gate (`> 0.8`) was built on it, so the gate could
not fail. It now measures density and takes the detector's `samplePeriod` as a third
argument, because a track genuinely cannot tell "sampled once every 10s and present
throughout" from "glimpsed three times" — the timestamps are identical.

Persistent track IDs from per-frame detections: IoU association, **rescued by the ArcFace
embeddings becky already computes** when geometry fails (a fast head turn or brief occlusion),
deterministic tie-breaks throughout, and `CoverageIn(t0,t1)` which answers "was this person on
screen during this window" with a *measure* rather than a boolean.

A real bug surfaced and was fixed during testing: stale tracks were retired **after**
association, so a track unseen for 40 frames could still win the current frame's detection —
silently merging two separate appearances (on real footage: two different people who stood in
the same spot) into one identity. Now retired first. Regression test:
`TestBuild_EndsTheTrackAfterALongGap`.

> **THE GAP — read this before wiring it up.** `internal/faceembed` returns only the **most
> prominent face per image** (`face_embed.py` filters to the largest × highest-scoring). A
> tracker needs **every** face per frame. `internal/facetrack` is therefore written against a
> `[]Detection` input and is **not yet connected to a real detector**. The change is in the
> Python helper, which is the model boundary and so local's lane — see the work order below.
> It is deliberately not faked with a single-face shim.

### 5.4 The chain self-orchestrates — no tool ever asks Jordan to run another tool

**Added 2026-08-19 after he hit it.** `becky-moment` used to stop with *"no transcript
sidecar — run becky-transcribe first"*. becky-tools is **where transcripts come from**, and
for a non-developer "go run another tool" is a dead end, not an instruction. It violated
`CLAUDE.md` §2 outright: one dumb call, becky does the thinking inside it.

`internal/transcribex` now gets a transcript, **making one if it does not exist**, and writes
it beside the video so the cost is paid once per file ever. Verified by dragging
`test-for-clips.mp4` onto the launcher: transcribed (41 cues), 91 candidates, 10 moments,
10 clips, 10 shorts, exit 0.

`Make Shorts.bat` takes a **video OR a folder**. It previously accepted only a folder, so
dragging a clip printed "No folder given" — and its typed-path prompt could never have worked
either, because batch expands `%VAR%` when an if-block is *parsed*, not when it runs. Delayed
expansion throughout.

### 5.5 Step 3 — LR-ASD works; the Go wiring is the gap

`internal/pyhelpers/asd.py` runs **LR-ASD** (Springer IJCV 2025) over face tracks and returns
a per-frame speaking score per track. It answers the licence question `research/shorts-models.md`
left open: **MIT**, 0.84M params, weights in-repo (`models/lrasd`, gitignored). It runs on the
**GPU** — torch CUDA is available here even though onnxruntime is CPU-only, so the research's
"ONNX" note was wrong and did not matter. 15s of video scores in ~6s.

Tracks come IN rather than being found here: a face must be followed through time before it can
be scored, becky owns that deterministically in `internal/facetrack`, and detection is a model
call while association is arithmetic.

**The bug worth knowing about.** LR-ASD resizes the padded face box to 224 and takes the
**centre 112** — the middle half, a 2x centre zoom (`Columbia_test.py:221-223`). Resizing
straight to 112 hands it a face zoomed out 2x from anything it saw in training, and it still
returns confident-looking scores. On a clip where the transcript and the audio energy both say
he talks throughout, it read **54% speaking and decayed to negative**; with the training crop it
reads **87% and stays positive**. Nothing downstream could have detected that.

A wrong diagnosis, kept for the speed: per-frame `cv2` seeking on a 3.5-hour source was blamed
first. Replacing it with one sequential ffmpeg decode changed the scores by 0.015 — so that
theory was wrong — but took the run from **42s to 6s** and decodes once for all tracks.

**`--all-faces` is done** (Step E's first half): `face_embed.py` emits every face per frame,
deterministically ordered, and is byte-identical without the flag. Verified 75/75 frames on real
footage. **Open:** `internal/faceembed`'s `Face` struct is still singular, so nothing feeds
`facetrack.Build` yet, and no `becky-*` command drives the chain.

### 5.6 Steps 4-5 — `becky-short`, and the tracking Jordan rejected once

`cmd/becky-short` + `internal/crop` + `internal/pyhelpers/crop_path.py`. MediaPipe Pose finds
the subject, OpenCV reads frames, the camera path is smoothed, ffmpeg renders 9:16.

**He rejected the first version outright:** *"the clips which follow my face movement are
noticibly lagging... I have an old, depreciated python script from 5 years ago that keeps up
better than this dog shit."* He was right, and the cause was a category error — a **realtime**
tracker built for an **offline** job. Three things each guaranteed lag:

1. **It sampled 8 times a second** on 30fps footage. Nyquist is then 4 Hz; a head turn is faster,
   so real moves were aliased away before any filter saw them. Now **every frame** — verified
   360/360 and 750/750 on his footage.
2. **The smoother was causal.** `held += (target-held)*ease` only sees the past, so its output is
   *mathematically guaranteed* to trail. Tuning cannot fix it; higher gain trades lag for jitter.
   `smooth_zero_phase` now runs the filter forward **and backward** over its own output — the two
   phase shifts cancel exactly. Zero lag, and because the curve is shaped by frames on both sides
   it leans into a move slightly before it happens, which is what a human operator does.
3. **The render threw the path away.** `FilterChain` squeezed the whole clip into **48**
   ffmpeg-expression keyframes, escalated the tolerance to **64px**, then truncated with
   `kept[:48]` — freezing the crop for the rest of the shot. The path now goes to ffmpeg as a
   **sendcmd script**: one command per real change, no cap, no tolerance, no truncation.
   *Trap:* sendcmd's parser treats a Windows drive colon as its own separator, so the script must
   be a **bare filename** with ffmpeg run from its directory.

**What the three references he named actually do** (`Dolly-Zoom-main`, `ofxFaceTracker-master`,
`obs-zoom-and-follow-master`) — all confirm the diagnosis:

- Dolly-Zoom lerps **60% toward the detection every frame** (tau ~36ms), with **split gains**:
  position fast, zoom 2x slower.
- ofxFaceTracker applies **no filter at all** — it re-optimises per frame from last frame's pose.
  Its non-realtime example sets `setIterations(100)` / `setAttempts(4)` purely because it is
  offline. That is the "second pass" by name.
- obs-zoom-and-follow measures error to the **deadzone edge** at **unit gain**, giving zero
  steady-state error on sustained motion, with a large (15%) deadzone for stillness.
- **None of them buys stillness with a low gain.** That was the mistake.

**Framing rules, each learned from a bad frame:**

- **Position from the FACE, scale from the HEAD.** Framing on the shoulder midpoint sheared his
  head off when he leaned over; scaling by shoulder span produced a hair close-up when his
  shoulders collapsed together. Head width barely changes with pose.
- **The face centre must stay in the middle band** (`FACE_BAND` 0.34), not merely be inside the
  frame — "legally in shot" still allowed him pinned against an edge.
- **A frame with no visible face is a MISS**, never a torso guess. That guess aimed the camera at
  empty room and counted as a success.
- **Reject detections below talking-head scale** (`--min-head-frac` 0.045). MediaPipe reported
  visibility **1.00** and presence **1.00** for a "face" in a clear plastic jar spanning 40px of a
  1920-wide frame. No confidence threshold catches that; geometry does.
- **Gate on the longest CONTIGUOUS gap** (`--max-gap` 2.0s), not average coverage. A clip can be
  92% covered and still hold a stale crop for 4 seconds. 2.0s allows a normal glance away while
  still refusing a real absence.
- **A fresh-eyes second pass** re-runs any frame the tracker lost through an IMAGE-mode
  landmarker with no temporal prior. Honest result: on his clip it never fires (750/750 found),
  so it costs nothing and is insurance for harder footage.

Verified by inspecting rendered frames: subject centred, whole head in, eyes on the upper third,
gestures not cropped, holding through his movement. ~40s of tracking+render per 12s clip.

**Most of his own streams are already 1080x1920**, so the job is usually a push-in rather than a
pan; `test-for-clips.mp4` is 1920x1080 and needs the real crop. One code path serves both.

### 5.7 Step 6 — audio as a third signal

`internal/audiosig` + `internal/pyhelpers/audio_signals.py`. Every threshold derives from the
file's own rolling distribution, never a constant. Measured on `test-for-clips.mp4`: 20 loudness
spikes, 67 pitch rises, 50 breath gaps. **55% of spikes fall within 1s of an independent pitch
rise, against 24% for the same count placed at random** — 2.3x over chance is why these are
signal and not noise. The loudest moment in the whole 5 minutes sits inside a 20-second stretch
where the `.srt` has **no text at all**.

`Rank` folds it into the structural prior (0.72/0.28) and names it in the basis. It moves the
ORDER only — audio measures ENERGY, not quality, so it never on its own promotes a moment to a
conclusion.

**Honest result: it does not reorder his top 3 yet.** The structural prior saturates near 0.95
for everything, so it still dominates. The saturation is the next thing to fix.

---

## 6. The LLM judge — LOCAL by default, OpenCode Zen as the option

**Corrected 2026-08-18 (local), after Jordan's review.** He read the original of
this section and said: *"using 3 gates for the opencode zen api seems like massive
overkill; I've already instructed you to use only their free models, typically
they offer about 4 main free models. It should not be so complicated to simply
make sure we only use those 4 models."* He was right, and checking it turned up
more than an over-guarded client.

### 6.1 The judge is a LOCAL model now; Zen is `--judge-backend=zen`

Three reasons, in order of weight:

1. **`CLAUDE.md`'s standing invariant is "offline + deterministic — no network at
   runtime."** Making a cloud API the primary judge breaks becky's own core rule
   for the single decision that most determines whether a short is worth posting.
2. **The key was never set.** `BECKY_ZEN_API_KEY`, `OPENCODE_API_KEY` and
   `OPENCODE_ZEN_API_KEY` are all unset on Jordan's machine, and nothing is in the
   user environment registry. A judge that cannot run is not a second signal.
3. **Without a second signal the tool is not useful.** Measured on his own footage
   (`E:\TakingBack2007`, 177 real transcripts): 112,625 candidates, of which the
   top 4,000 all score between **0.985 and 1.000** on the structural prior. The
   structure score is compressed into the top 1.5% of its range, so `--top 8`
   returns an arbitrary eight of several thousand ties. Structure cannot rank
   *interestingness* — that is exactly the content pass's job.

`cmd/becky-moment/local.go` runs Gemma-4 E4B through `internal/llmlocal` (the
existing shared llama-server transport — `cmd/ask` and `internal/assistant`
already use it), warm-server so the weights load once. Verified on the same
footage: five moments come back as `conclusion` with reasons, scores spread
0.878–0.817 instead of tied at 1.000.

Only the structurally strongest candidates are judged — 4 per requested moment,
floored at 40 and capped at 400. The original passed **every** candidate to the
judge before `--top` truncated, which on a folder like Jordan's is 112,625 windows
to choose ten.

### 6.2 The Zen guard is one allowlist

Zen's free tier, read from its pricing page and **cross-checked against the live
`https://opencode.ai/zen/v1/models` on 2026-08-18** — an endpoint the old code
comment claimed did not exist:

    big-pickle              deepseek-v4-flash-free   mimo-v2.5-free
    hy3-free                laguna-s-2.1-free        nemotron-3-ultra-free
    nemotron-3.5-lightning-free

Seven, not four, and the roster rotates (ids in older write-ups — `north-mini-code`,
`mimo-v2-pro-free`, `minimax-m2.5-free` — are no longer listed). `/v1/models`
carries no pricing field, so free-ness cannot be derived at runtime; the list is
hardcoded and the refusal message prints it.

**One check replaces three, and is stricter than what it replaces:**

- The old `isFreeZenModel` was a `-free`/`:free` **suffix test**. A metered model
  called `turbo-free` sailed through it — and it already needed a hardcoded
  exception for `big-pickle`, i.e. an allowlist of one bolted onto a heuristic
  that did not work. Zen genuinely hosts both `deepseek-v4-flash` (metered) and
  `deepseek-v4-flash-free`, one character apart.
- The separate resold-Anthropic blocklist is **gone and not needed**: `claude-*`
  is refused because it is not on the list.
- Its "belt and braces" duplicate inside `zenOnce` is gone.
- **`--allow-paid` is deleted.** "Free only" is an absolute rule; a flag whose
  only purpose is to break it should not exist.

`deepseek-v4-flash-free` — the id the original guessed at and could not verify —
is real and remains the default for the Zen backend.

## 7. Work order — what is DONE and what is OPEN

Do not mark a box without pasting evidence (`HANDOFF-TEMPLATE.md` §5).

### DONE (verified on Jordan's own footage, 2026-08-18/19)

- [x] **A — deterministic layer.** `go build`/`go vet` clean, gofmt clean, **164 packages green**.
- [x] **B — real binaries.** `build-all-tools.bat` run. `becky-moment --selftest` **13/13**,
      `becky-short --selftest` **28/28**, `audio_signals.py --selftest` **15/15**.
- [x] **C — the whole chain from a dragged file.** `Make Shorts.bat` on `test-for-clips.mp4`:
      auto-transcribed → 91 candidates → 10 moments → 10 clips → 10 shorts, exit 0.
- [x] **D — the content pass, LOCAL.** Gemma-4 E4B via `internal/llmlocal`. Moments return
      `conclusion` with reasons, not the all-`candidate` degrade. No key needed (§6.1).
- [x] **E (first half) — `face_embed.py --all-faces`.** Every face per frame, deterministically
      ordered, byte-identical without the flag. 75/75 frames on real footage.
- [x] **F — MediaPipe + OpenCV.** `cv2` 4.13.0 was already installed; `mediapipe` 1.0.1 added,
      Pose + Face Landmarker verified. `models/mediapipe/pose_landmarker_{heavy,full}.task`
      downloaded (`scripts/get-mediapipe-models.ps1`). **gocv dropped** — Jordan's call.
- [x] **Stage 4-5 — `becky-short`.** Per-frame tracking, zero-lag path, sendcmd render, refusals
      that fire on real absence. Frames inspected (§5.6).
- [x] **LR-ASD model.** MIT, runs on the GPU, training-crop bug found and fixed (§5.5).
- [x] **Audio signals.** Built, measured, folded into `Rank` (§5.7).

### CLOSED since this list was written — with the commit

- [x] **1. Who-is-speaking wired into a command.** `becky-speaking` (`7374be2`): frames →
      `face_embed.py --all-faces` → `facetrack.Build` → `asd.py`. Selftest 12/12. On a real
      two-person clip it yields exactly 2 tracks. **Honest gap:** the "one talker scores
      materially higher" bar did NOT clear on the only two-person clip on this machine —
      a decoy control showed the pipeline discriminates cleanly when the audio carries a
      real lip-synced signal, so it is that clip, not the wiring. Not papered over.
- [x] **2. The "score saturation" — diagnosed differently and fixed** (`4f62d0d`). Measured
      on `test-for-clips.mp4` the prior is NOT saturated (min 0.640, max 0.982, none at the
      ceiling). The real defect was that `--top 10` returned **ten re-cuts of ONE 68-second
      stretch** of a five-minute video. Overlap suppression (proportional to the shorter
      window, 0.5) takes the same top ten across four distinct regions spanning 9.1s-300.1s.
      Widening the score range would have made duplicates look meaningfully ranked.
- [x] **3. Moment picking now knows whether he is on screen** (`6a79d0b`). `internal/facesig`
      + `Candidate.Face`. A window where he is turned away drops from rank #4 to #10, and the
      frames confirm it: hard profile turn, then the back of his head.
- [x] **4. Captions burned in** (`a5985d5`). And a real bug caught doing it: `WordsPerSegment`
      rescues an unclaimed word onto the nearest cut — right for a reel that tiles the source,
      badly wrong for a single excerpt. **110 captions became 18** once the words were sliced
      to the window.
- [x] **5. Framing taste pass — the constants are now MEASURED, not my eye** (`7f3590d`).
      `--eye-line` 0.38 → **0.27** from InsightFace over all 915 frames of his own edit (face
      centre Y median 29.9%, 90% of frames in the upper 40%). **One retraction:** I also read
      becky as framing far too tight (39.7% vs his 24.3%) and was wrong — a 9:16 crop of
      1920x1080 is already full-height and his face is 37.8% of the SOURCE, so there is no
      crop that shrinks it and no spare source to shift into. `--shoulder-frac` was reverted.
- [x] **6. The false comment at `cmd/events/main.go:14` is gone** (`1f88ede`) — it claimed no
      face detector ships here while the same file runs multi_face default-on.

### Built after this list, from the reference edit

- [x] **Cut preservation** (`0facffe`). `internal/shotcut` detects the source's own cuts
      (precision 0.833 / recall 0.833 against a hand-checked list on real footage). When the
      source is already edited, becky-short **preserves those cuts and tightens near them**
      instead of re-cutting with a silence threshold. Measured on the BLINDFOLD master
      21.7-52.0s: **18 of 19 existing cuts preserved, 9.0% removed** against Jordan's own
      10.3% and becky-cut-alone's 51%. The crop path also resets per shot, so framing snaps
      at a cut instead of drifting in.
- [x] **Jumpcuts for RAW footage** (`078c6bb`) — still the right behaviour when no cuts exist.
- [x] **Clip edges snap to real silence** (`387df0f`), 21/91 candidates moved on his footage.
- [x] **The judge names the opening line** (`17a0699`); a verbatim quote from mid-clip holds
      the clip as DISPUTED. Fired for real on his footage.
- [x] **A relative `--out` was rendering into a temp dir and deleting it** (`bd8c200`).
- [x] **One untrackable span was throwing away eighteen good ones** (`5e67f7b`). His own edit
      holds 1.27s on a pointing finger with no face in frame, so a per-span refusal was the
      wrong granularity.

### STILL OPEN

- [ ] **A. `becky-short --review` — re-watch the output before calling it done.** Jordan's
      rule 5, and **not one of the 21 reference projects does it** (`research/shorts-gap-decisions.md`).
      Deterministic first: re-measure the RENDERED file for subject-in-frame, caption/audio
      alignment, and a completed ending. In progress.
- [ ] **B. His caption STYLE.** 2-3 stacked lines, 2-4 words per line, one word coloured per
      block — cyan on the stressed word of a reaction, yellow for a directive or the running
      joke — profanity in a red box, emoji as accents, placement that moves with the content.
      becky ships one flat white style at a fixed margin. **This is his look: offer it, never
      switch it on by default.**
- [ ] **C. Frame the GESTURE, not just the face.** His shot 19 is 1.27s on a pointing finger;
      becky found the same beat and framed the whole machine wide. Reka Edge is verified
      running here (6496 MiB, `Detect:` in 2.3s) and returns grounded boxes —
      `research/reka-edge-vs-gemma4.md`.
- [ ] **D. Zoom as an editorial device.** becky has none. `internal/audiosig` already measures
      the energy that would drive a punch-in. Build ONE, show him, stop.
- [ ] **E. Loudness to -14 LUFS.** One ffmpeg filter in `crop.RenderArgs`.
- [ ] **F. The last degrade paths:** `cmd/motion` frame-diff → optical flow,
      `cmd/framematch/decor.go` census → ORB+RANSAC. Both can use `cv2` now.
- [ ] **G. THE QUESTION FOR JORDAN, which nobody should guess at:** on close-up 16:9 footage
      where the subject already fills the frame, does he want the full-height crop we do now,
      or a padded/blurred background with the subject on his 30% line? His own edit is
      full-bleed but never had to solve that case.

### 7.1 Not this branch's work

`go test ./...` locally: **164 packages green, two failures**, neither touched by this work —

- `internal/assistant TestHandleTier2Funnel` — a **real regression**
  (`frontier plan actions = [], want add_clip(s)`).
- `cmd/tts TestRun_DegradesWhenNoModel` — *"expected non-zero exit when degrading (no model)"*.

`cmd/clip TestAudioLevels_ThreadsFpsAndParses`, which an earlier version of this section named,
**passes here** — it shells to `auto-editor`, which exists on this machine and not on a CI
runner. The "30 consecutive red CI runs" claim was not re-checked: `CLAUDE.md`'s
highest-authority rule forbids spending Jordan's plan polling GitHub.

## 8. Honest summary

The models were never the problem. becky already had the better model at three of the six
stages, and the two genuinely new dependencies (MediaPipe, LR-ASD) both installed and ran on
this machine inside an evening. What actually cost time was exactly what §2 predicted, and the
shape of it is worth keeping:

**Every real defect this session shipped a file that plays.** Not one produced an error, a
crash, or a red test. A crop framed on a lamp, a tracker that trailed the subject, a speaking
detector reading 54% on a clip of continuous speech, moments labelled with the wrong video — all
of them exited 0. The suites stayed green throughout, because they tested units, not output.

**Every one was found by looking at the output.** Rendered frames read for meaning, scores
compared against the transcript and against audio energy, landmark coordinates dumped and
checked against where the subject actually was. Nothing was found by reading code, and three of
my own confident diagnoses were wrong (video-seek drift, a visibility threshold, a stale
embed) — each disproved by measuring rather than by arguing.

**The one structural lesson.** The tracking was rejected because it was a *realtime* design
doing an *offline* job: sampling 8 times a second, smoothing with a filter that cannot see the
future, then decimating the result to 48 keyframes. Every one of those is a defensible choice
for a live preview and an indefensible one for an editor. Jordan's framing was the correct one —
*"this is video EDITING - make it do a SECOND PASS... I literally go frame-by-frame if I have
to"*. The fix was not a better model or a tuned constant; it was tracking every frame, filtering
forward **and** backward so there is no lag at all, and handing ffmpeg the whole path.

**What is genuinely still open** is in §7, and the honest ranking is: the score saturation (2)
makes extra signals decorative until fixed; moment picking is still blind to whether he is on
camera (3); and every framing constant is set to my taste rather than his (5). The pipeline now
runs end to end from a dragged file and refuses what it cannot do honestly — which is the
§3.3 precondition working — but "runs and refuses honestly" is not the same as "produces clips a
professional would post", and only he can close that gap.
