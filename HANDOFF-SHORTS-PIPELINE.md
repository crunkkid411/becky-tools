# HANDOFF — short-form repurposing pipeline: why it would fail, and what it takes

**Status: RECOMMENDATION ONLY, 2026-08-15 (cloud). Nothing is built.** Read
`research/shorts-models.md` first — it is the model research this rests on. This document is
the *engineering* half: an honest prediction of how this build fails if started the usual way,
and the conditions that have to hold for it not to.

**Read this before writing any code for a shorts pipeline.** It is deliberately a
stop-and-think document, not a work order to start executing. The work order shape is in §5,
and it should not be started until §3 is agreed.

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

## 4. Recommended build order (each step independently valuable)

Ordered so that every step ships something usable even if the next never happens — the direct
counter to becky's "half-built stack" pattern.

| # | Step | Why here | Needs native dep? |
|---|---|---|---|
| 0 | Fix `becky-reel` fps quantisation + regression test | Everything renders through it | No |
| 1 | `becky-moment` — moment selection over existing transcripts (hook/build/payoff rubric on `becky-judge`'s two-stage recall→judge, ending extended to the completing cue) | Uses **only** what becky already has; delivers value with zero new models | No |
| 2 | Face **tracking**: persist track IDs across frames via IoU + the ArcFace embeddings becky already computes | Unblocks 3–5; no new model | No |
| 3 | **LR-ASD** (ONNX) speaking decision, fused with `becky-diarize` voice turns + track identity | The one genuinely new model; three independent signals | ONNX Runtime |
| 4 | Crop path: MediaPipe Pose for body-aware framing, smoothed via OpenCV camera-path smoothing | Makes the crop look edited rather than centred | **Yes** |
| 5 | Render + captions through the fixed `becky-reel`, cut-snapped via `internal/subs` | Existing, proven | No |

Steps 0–2 are cloud-buildable and testable today. Steps 3–4 are the handoff, and must ship as
a checkboxed work order per `HANDOFF-TEMPLATE.md`.

**Note the shape:** step 1 alone — moment selection over transcripts becky already produces,
with no reframing at all — would give Jordan a ranked list of clip windows he could cut manually
in Vegas in a fraction of the time. That is real value from the stage becky is *already*
strongest at, and it does not depend on any of the hard parts landing.

---

## 5. If you are the local agent picking this up

1. **Do not start at step 3.** The new model is the interesting part and the wrong place to
   begin. Start at step 0.
2. **Do not ship a pure-Go approximation of steps 3–4 because the dependency is awkward.** That
   is the documented failure pattern (§2.3). If it is blocked, say so and stop.
3. **Do not mark this branch done with "LEFT FOR LOCAL: nothing"** unless every box is checked
   with pasted evidence. `HANDOFF-TEMPLATE.md` §5.
4. **Claim the work in `COLLAB-PROTOCOL.md`'s registry before building.**
5. When a stage cannot decide, **make it say so**. The instinct to always return an answer is
   what makes every other tool in this space subpar.

---

## 6. Open decisions for Jordan (blocking §4 steps 3–4)

- **Native CV dependency: yes or no?** gocv + MediaPipe unlock stages 4–6 properly; refusing is
  legitimate but caps quality and that cost goes in the spec (§3.2).
- **Refusal threshold.** How many honest skips per batch is acceptable? Drives §3.3's threshold.
- **Where does the moment LLM run?** Local Gemma-4/Qwen3.5 (free, weaker) or Claude via the
  OAuth session (free at point of use, stronger). **Not** a paid endpoint — `CLAUDE.md`'s
  never-spend-money invariant applies, and `cmd/subtitle/openrouter.go`'s `isFreeModel` guard
  must be copied into anything that talks to a provider.
- **Is step 1 alone worth shipping first?** My recommendation: yes.

---

## 7. Honest summary

The models are not the problem and mostly already exist — becky has the *better* model at three
of the six stages (`research/shorts-models.md` §3). The problem is that becky has a documented
history of building six-stage chains where each stage compiles, passes its unit tests, and
silently hands the next stage something wrong.

A shorts pipeline is the least forgiving possible place for that pattern, because its failures
render successfully. If this is built the way becky has been built so far, it will produce
plausible, subtly-wrong shorts and reproduce exactly the experience that sent Jordan looking at
other people's projects in the first place.

The preconditions in §3 are what change that outcome. They are cheaper than the rework.
