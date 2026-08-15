# HANDOFF — short-form repurposing pipeline: why it would fail, and what it takes

**Status: STEPS 0-2 BUILT + PROVEN (2026-08-15, cloud). Steps 3-6 need the native CV
dependency Jordan approved.** Read `research/shorts-models.md` first — it is the model
research this rests on. This document is the *engineering* half: an honest prediction of how
this build fails if started the usual way, the conditions that have to hold for it not to,
and (§5) the current build state with the exact work order for what remains.

**Jordan's decisions, 2026-08-15 — these are settled, do not re-litigate:**
- **MediaPipe and OpenCV are IN.** *"we NEED to use them. weve been cutting corners and all
  that does is waste my time, not save time."* The §2.3 pattern — shipping the pure-Go
  degrade path because cloud couldn't compile the real thing — is explicitly over.
- **OpenCode Zen is the LLM judge**, using Jordan's API key. See §6 for the spending guards
  that ship with it (they are not optional, and one of them is load-bearing).
- The GitHub/CI failures are **not** cloud's to fix — that is the local agent's lane.

---

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

**Real CLI run cloud RAN** on a synthetic interview `.srt`: 8 cues → 17 candidates → top pick
`00:00:06.500 → 00:00:27.000` (hook "The thing nobody tells you about running a studio is the
cashflow", build, payoff "I nearly went under twice before I figured that out"), correctly
labelled `candidate` with the note *"the content pass did not run"* because no API key was set.
The degrade path is proven, not asserted.

**Seam test** (`cmd/becky-moment/main_test.go`): it **parses `cmd/becky-hits/main.go`** and
asserts every JSON key becky-moment emits is one becky-hits actually reads. Rename a field
there and this fails here. This is the §3.4 precondition made real, and it is the exact class
of bug that produced `becky-validate --variant` — a flag that did not exist, passed by a tool
whose unit tests were all green.

### 5.3 Step 2 — `internal/facetrack` BUILT, with one honest gap

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

## 6. The LLM judge — OpenCode Zen, and the guards that ship with it

Jordan authorised his OpenCode Zen key for the content pass. `cmd/becky-moment/zen.go`
implements it against Zen's OpenAI-compatible endpoint (`https://opencode.ai/zen/v1`), at
`temperature: 0` so the same transcript yields the same verdicts.

Two properties of this provider make an unguarded client genuinely dangerous, so three guards
ship with it — all tested, all refusing **before** a request is built:

1. **Zen resells `claude-opus-5` / `claude-sonnet-5` / `claude-haiku-4-5` per token.** Jordan
   pays for Claude Max — those are already bought. Routing them through a metered gateway is
   paying twice, which is the exact 2026-07-19 mistake `CLAUDE.md` records. **Hard-blocked, and
   `--allow-paid` does not unblock them.**
2. **Zen auto-reloads $20 when the balance drops below $5.** This is materially worse than
   OpenRouter, where a runaway loop *stopped itself* when the balance hit zero and every call
   402'd. Here it would keep charging indefinitely. So any metered model needs an explicit
   per-run `--allow-paid`.
3. **An unrecognised model id is treated as PAID**, so a typo costs a refusal, not money.

**Unverified, and it needs one local run:** Zen has no OpenAI-style `/v1/models` discovery
endpoint, so the exact free-tier ids could not be confirmed from code. `defaultZenModel` is set
to `deepseek-v4-flash-free` from the docs page. If that id is wrong the tool degrades with the
endpoint's own error message — it does **not** silently fall back to something metered.

---

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

### Step D — turn on the content pass (verifies the Zen model id)
```
setx BECKY_ZEN_API_KEY "<Jordan's key>"
bin\becky-moment.exe --folder <folder> --top 10
```
- [ ] DONE WHEN: moments come back as `conclusion` / `disputed` rather than all `candidate`.
- [ ] **If it errors on the model id**, list the current free ids at
      https://opencode.ai/docs/zen/ and update `defaultZenModel` in `cmd/becky-moment/zen.go`.
      Do **not** work around it by passing `--allow-paid`.

### Step E — the facetrack gap (model boundary; §5.3)
- [ ] Extend `internal/pyhelpers/face_embed.py` to emit **all** faces per frame, not just the
      most prominent one. Suggested: an additive `--all-faces` flag so every existing caller
      (`becky-identify`, `becky-enroll`, `becky-cluster`) is byte-identical without it.
- [ ] Add the matching multi-face path in `internal/faceembed` and feed
      `facetrack.Build` from it.
- [ ] DONE WHEN: a real clip with two visible people yields exactly 2 tracks whose
      `CoverageIn` over a window where both are present is > 0.8 each.

### Step F — the native CV dependency (Jordan approved this; §3.2)
- [ ] Install `gocv` + OpenCV 4.12 on the Windows box and confirm `go build -tags gocv ./...`.
- [ ] Install MediaPipe Tasks (Python) and confirm Pose Landmarker runs on one frame.
- [ ] Report back which of the two is more painful — that decides whether step 4's crop path
      goes through gocv (Go, in-process) or a MediaPipe pyhelper (Python, like the others).
- [ ] **Do not proceed to steps 3-6 by approximating either in pure Go.** That is §2.3.

### 7.1 Not this branch's work

CI is red on `master` and has been since 2026-07-24 (30 consecutive runs; `go vet` is
`skipped` on every one because it runs after `go test` in the same job). Two failures:
`cmd/clip TestAudioLevels_ThreadsFpsAndParses` (environmental — shells to `auto-editor`, absent
on runners, no skip guard) and `internal/assistant TestHandleTier2Funnel` (**a real regression**
— `frontier plan actions = [], want add_clip(s)`). Per Jordan, this is local's lane, not
cloud's. Flagged here so it is not mistaken for something this branch introduced.

---

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
