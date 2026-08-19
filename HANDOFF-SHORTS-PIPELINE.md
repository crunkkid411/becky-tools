# HANDOFF — short-form repurposing pipeline: why it would fail, and what it takes

**Status, corrected 2026-08-18 (local) after Jordan's review.** Step 0 is fixed and
proven end-to-end. Step 1 (`becky-moment`) now runs a real, offline content pass on
his own footage. Step 2 (`internal/facetrack`) compiles and unit-tests but has still
**never seen a real face** — it has no detector wired to it (§5.3), so "PROVEN" in the
original status line was true of step 1 and not of step 2. Steps 3-6 are not started.

Read `research/shorts-models.md` first — it is the model research this rests on. This
document is the *engineering* half.

**Jordan's decisions — settled, do not re-litigate:**
- **MediaPipe and OpenCV are IN.** *"we NEED to use them. weve been cutting corners and
  all that does is waste my time, not save time."* **Both are now installed and verified
  on this machine** (§7 Step F): `cv2` 4.13.0 was already there, `mediapipe` 1.0.1 added.
- **Free models only.** One allowlist, no override flag (§6).
- The GitHub/CI failures are **not** cloud's to fix — that is the local agent's lane.

**What his review found, and what it cost.** He said the conclusions were
*"overengeneered already"* and flagged the Zen guard specifically. Auditing that turned
up three bugs of the exact class §2.1 warns about — each produced a file that plays:

| Bug | What Jordan would have seen |
|---|---|
| `--folder` labelled moments with the **wrong source video** (owner tracked, thrown away, recovered by matching timestamps) | reels cut from the wrong footage |
| "Incomplete arc = **VETO**" was a 0.6 multiplier — a 90/99 clip that trails off (0.540) beat a modest one that lands (0.5025) | shorts with no payoff, ranked top |
| `CoverageIn` returned the **span** between sightings, so a face glimpsed 3× in 20s scored 1.000 — same as one in every frame | the wrong person framed |

All three are fixed with regression tests that assert values. The veto's property test
immediately found a fourth hole: an incomplete clip whose signals *also* disagreed hit
the Disputed branch first and escaped the veto entirely.

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

### 3.1 Fix the render-drift bug first, in isolation

`becky-reel render` drifts +1.27 s over 88 cuts. A shorts renderer sits directly on top of it,
and captions are the most visible element of a short. Fix + regression-test the fps quantisation
**before** anything is built on it. This is a small, self-contained, independently verifiable
task and it is the correct first commit.

### 3.2 Build the native dependency properly, or decide not to and write down the cost

`gocv` (Apache-2.0, OpenCV 4.12, Windows-supported) and MediaPipe are the right tools for
stages 4–6. Cloud **cannot** build or test them. Under `STANDARDS-WORKFLOW.md` §7 that makes
them a handoff obligation with a checkboxed work order — **not** grounds to ship a weaker
pure-Go default and call the branch done. If Jordan decides against the native dependency, that
is legitimate, but the accuracy cost gets written into the spec rather than discovered later.

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

| # | Step | State | Needs native dep? |
|---|---|---|---|
| 0 | `becky-reel` fps quantisation | **ALREADY DONE** (§5.1) | No |
| 1 | `becky-moment` — moment selection over existing transcripts | **BUILT + PROVEN** (§5.2) | No |
| 2 | Face **tracking** — persistent track IDs via IoU + ArcFace | **BUILT + TESTED**, one gap (§5.3) | No |
| 3 | **LR-ASD** (ONNX) speaking decision, fused with `becky-diarize` + track identity | not started | ONNX Runtime |
| 4 | Crop path — MediaPipe Pose framing, OpenCV camera-path smoothing | not started | **Yes** |
| 5 | Render + captions through `becky-reel`, cut-snapped via `internal/subs` | not started | No |

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

## 7. Work order for the local agent

Ordered, checkboxed, with the command and the DONE-WHEN for each. Do not mark a box without
pasting evidence (`HANDOFF-TEMPLATE.md` §5).

### Step A — confirm the deterministic layer on this machine
```
cd becky-go && go build ./... && go vet ./... && go test ./internal/moment/ ./internal/facetrack/ ./cmd/becky-moment/
```
- [ ] DONE WHEN: all three packages report `ok`. Cloud's result: all pass, 162 packages green
      overall (the 2 pre-existing failures in `cmd/clip` and `internal/assistant` are unrelated
      and predate this branch — see §7.1).

### Step B — build the real binary
```
cd becky-go && build-all-tools.bat
```
- [ ] DONE WHEN: `bin\becky-moment.exe` exists (auto-discovered, no script edit needed).
      Then `bin\becky-moment.exe --selftest` prints **13/13 PASS**.

### Step C — run it on Jordan's real footage
```
bin\becky-moment.exe --folder <a folder with .srt sidecars> --top 10 --judge=false
```
- [ ] DONE WHEN: real moments come back with sane windows. Every one should read
      `"confidence": "candidate"` and carry the "content pass did not run" note.
- [ ] Then: `becky-hits --hits moments.json --folder <same folder>` builds a Reel that opens in
      Becky Review. **This is the seam that matters** — confirm the clips land at the right
      timecodes on real video, not just that the JSON parses.

### Step D — the content pass (now LOCAL, no key needed) — **DONE 2026-08-18**
```
binecky-moment.exe --folder <folder> --top 5 --verbose
```
- [x] DONE: runs Gemma-4 E4B via `internal/llmlocal`, spawning llama-server on a
      free port. On two real transcripts from `E:\TakingBack2007` it returned five
      moments, **all `conclusion`** (both signals agreeing) with content reasons —
      not the all-`candidate` degrade. `judge_model` reads
      `local:gemma-4-E4B-it-qat-UD-Q4_K_XL.gguf`.
- [ ] OPTIONAL, only if Jordan wants the cloud judge: `setx BECKY_ZEN_API_KEY "<key>"`
      then `--judge-backend=zen`. Nothing depends on this. **His key is not set on
      this machine** — that is why local is the default, not a fallback.

### Step E — the facetrack gap (model boundary; §5.3)
- [ ] Extend `internal/pyhelpers/face_embed.py` to emit **all** faces per frame, not just the
      most prominent one. Suggested: an additive `--all-faces` flag so every existing caller
      (`becky-identify`, `becky-enroll`, `becky-cluster`) is byte-identical without it.
- [ ] The Go side needs it too — `internal/faceembed`'s `Face` struct has singular
      `Vector`/`BBox`/`DetScore` fields, so there is physically no room for a second
      face. Add the multi-face path and feed `facetrack.Build` from it.
- [ ] DONE WHEN: a real clip with two visible people yields exactly 2 tracks whose
      `CoverageIn(t0, t1, samplePeriod)` over a window where both are present is
      > 0.8 each. **Note the third argument** — coverage is now density, not the
      span between the outermost sightings (see §5.3).

### Step F — the native CV dependency — **DONE 2026-08-18, and the bake-off was unnecessary**

The original Step F said: *"Report back which of the two is more painful — that
decides whether step 4's crop path goes through gocv (Go, in-process) or a
MediaPipe pyhelper (Python, like the others)."* That choice should not have been
offered, for three measured reasons:

- [x] **OpenCV was already installed and working.** `cv2` **4.13.0** imports today
      in the exact interpreter becky already uses for face work (anaconda +
      `PYTHONPATH=X:\PythonUserBase\Lib\site-packages`, which is `FacePyLib` in
      `internal/config`). Alongside it: `insightface` 1.0.1, `onnxruntime` 1.26.0.
      No native build, no cgo, no `-tags gocv` needed to reach OpenCV.
- [x] **MediaPipe is now installed there too** — `mediapipe` **1.0.1**, with
      `PoseLandmarker` and `FaceLandmarker` confirmed importable from
      `mediapipe.tasks.python.vision`.
- [x] **MediaPipe has no Go binding.** Stage 4 is a Python helper whatever else is
      decided, so routing OpenCV through `gocv` would split one per-frame stage
      across two languages and two processes for no gain. Pose framing and camera-path
      smoothing run on the same frames; they belong in the same helper.
      This also matches `CLAUDE.md`'s stated architecture — "Go binaries with the
      heavy ML pushed into thin embedded-Python helpers".

  **Recommendation for stage 4: one `internal/pyhelpers` script using MediaPipe
  Pose + OpenCV, no gocv.** They are not alternatives — Pose decides *where* the
  crop sits, OpenCV optical flow decides *how it moves*. Jordan's call to confirm.

- [ ] STILL OPEN — `gocv` remains worth it for one thing the research promised and
      no step covers: retiring the degrade paths in `cmd/motion` (ffmpeg frame-diff
      instead of optical flow) and `cmd/framematch/decor.go` (census descriptor
      instead of ORB+RANSAC). Both can now use `cv2` through a helper instead.
- [ ] **Do not proceed to steps 3-6 by approximating either in pure Go.** That is §2.3.

### Step G — the degrade paths research §6 named but the work order forgot
- [ ] `cmd/events/main.go:14` still says *"multi_face — OPTIONAL. No face detector
      ships in this environment, so it is skipped gracefully."* **That comment is
      false and has been for some time** — the same file runs multi_face default-on
      at :117-137 and `cmd/events/multiface.go:18` imports `internal/faceembed`.
      Delete the comment. (research/shorts-models.md §6 quotes this line as
      evidence; the quote is verbatim but stale, and the §6 conclusion leans on it.)
- [ ] `cmd/motion` and `cmd/framematch/decor.go` — see Step F.

### 7.1 Not this branch's work

**Corrected 2026-08-18 by running the suite locally.** `go test ./...` on this
machine: **163 packages green, two failures** —

- `internal/assistant TestHandleTier2Funnel` — a **real regression**
  (`frontier plan actions = [], want add_clip(s)`). Confirmed.
- `cmd/tts TestRun_DegradesWhenNoModel` — *"expected non-zero exit when degrading
  (no model)"*. **The original §7.1 did not mention this one at all.**

And `cmd/clip TestAudioLevels_ThreadsFpsAndParses`, which §7.1 named as one of the
two, **passes here** (0.16s) — it shells to `auto-editor`, which exists on this
machine and not on a CI runner. So the local failure set is `cmd/tts` +
`internal/assistant`, not `cmd/clip` + `internal/assistant`.

Neither is touched by this work. The "30 consecutive red CI runs" claim was not
re-checked: `CLAUDE.md`'s highest-authority rule forbids spending Jordan's plan
polling GitHub.

## 8. Honest summary

The models are not the problem and mostly already exist — becky has the *better* model at three
of the six stages (`research/shorts-models.md` §3). The problem is that becky has a documented
history of building six-stage chains where each stage compiles, passes its unit tests, and
silently hands the next stage something wrong.

A shorts pipeline is the least forgiving possible place for that pattern, because its failures
render successfully. If this is built the way becky has been built so far, it will produce
plausible, subtly-wrong shorts and reproduce exactly the experience that sent Jordan looking at
other people's projects in the first place.

The preconditions in §3 are what change that outcome. They are cheaper than the rework.

**What this session did about that, concretely** — the three preconditions were not just
written down, they were built into steps 1-2:

- **§3.3 (a stage may refuse)** is `Rank`'s three-state confidence. `becky-moment` on a real
  transcript with no API key returns every moment labelled `candidate` and says why, rather
  than presenting a structure-only guess as a pick.
- **§3.4 (seam tests, not unit tests)** is `TestSeam_EveryEmittedKeyIsReadByBeckyHits`, which
  parses the *other tool's source* instead of mocking it.
- **§3.2 (build the dependency properly or write down the cost)** is why `internal/facetrack`
  ships **unconnected** rather than wired to a single-face shim that would have looked finished
  and tracked one person out of two.

Both bugs found this session were found by tests asserting values: the facetrack identity-merge
across a 40-frame gap, and an assertion of mine that was asking the structural layer to make a
content judgement. Neither would have been caught by "it compiles."
