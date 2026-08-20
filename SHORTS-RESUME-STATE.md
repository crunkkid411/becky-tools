# SHORTS WORK — RESUME STATE

Written so any session can pick this up with no input from Jordan. Overwrite as things land.
Last updated 2026-08-20 ~08:00.

## IT RUNS. Start here.

**`Make Shorts.bat`** in the repo root. Drag a video or a folder onto it. It runs
becky-moment -> becky-hits -> becky-short and drops vertical shorts in a `shorts`
folder beside the footage. Verified end to end on 2026-08-20 against Jordan's own
560-second long-form: **2 shorts in about 7 minutes**, framed on people, his cuts
kept, his caption look burned in.

`--caption-style` now DEFAULTS to `jordan`, so the button produces his look with
no flags. It used to default to the plain `cli-cut` look, which is why the thing
he was shown did not look like his.

**`WATCH THESE/`** in the repo root holds tonight's output and the side-by-side.

## The test that matters — use these two files, nothing else

    master  2024-08-30_We_Tried_the_ULTIMATE_Fast_Food_Test_BLINDFOLD_Tasting_[unswA5Jv7fI].mp4
    his cut 2024-08-30_spitters_are_quitters..._[7409071570410327339].mp4

His short is **master 22.11s -> 54.07s**. The apples-to-apples run is

    becky-short -video <master> -start 22.11 -end 54.07 -out <x>.mp4

then measure the render against his. A previous session showed him a comparison
built from a DIFFERENT video and he spotted it instantly. Do not do that again.
Canon for framing and cut timing stays `research/jordan-edit-reverse-engineered.md`.

## Where becky stands against his own edit, measured

| | Jordan | becky (start of 2026-08-19) | becky now |
|---|---|---|---|
| caption cap height | 57px | — (wrong font entirely) | **58px** |
| caption block bottom | 513px up | — | **510px** |
| caption outline | 11px | — | 10px |
| face height | 23.6% | 24.0% | **24.4%** |
| face centre Y | 29.7% | 36.0% | 33.4% |
| real cuts found | (inherits) | 17 of 24 | **20 of 24** |
| existing cuts in window | ~22 | 17 | **21** |
| time removed | 3.29s = 10.3% | 2.434s = 7.6% | **3.034s = 9.5%** |
| tighten per boundary | 0.15–0.18s | 0.143s | **0.152s** |
| coverage | — | 0.605 | **0.673** |
| no face in frame | 11.5% | 20.3% | 17.2% |

## Landed 2026-08-19 → 20 (10 commits, all on master, all measured on that footage)

| Commit | What |
|---|---|
| `b8a7b5f` | **The caption look**, measured off his own render. |
| `27e3d24` | **becky could not zoom at all** — and the knob for it was disconnected. |
| `28561a9` | **A `[` in a filename fed the model the wrong video, or none.** |
| `26ba612` | **The AV defaults could never finish**, so they returned nothing. |
| `895597d` | `internal/focal` — where to point when there is no face. |
| `9db36f8` | `--focal-point` wired, measured, and **off**: it is a coin toss. |
| `51390f2` | **"Pace with the content" was a cherry-pick** — struck. |
| `d2d7c95` | **A busy stretch swallowed its own cuts** — recall 0.708 → 0.833. |
| `76575df` | **Marlin-2B actually run** — it answers RULE 4. |
| `0ee6559` | **The review pass failed every multi-person short** by construction. |
| `851abc8` | **becky was deleting his words** on raw footage — 9 of 26 rendered. |
| `2796a45` | **His caption look is the DEFAULT**, so the button produces it. |
| `d0f2117` | **A short must not end on nothing** — 7.7s of blur trimmed. |

### Captions — done, and it matches
Font settled by rendering every heavy sans installed here at his measured cap height and scoring
glyph IoU against his actual pixels: **Montserrat ExtraBold 0.803** … and **ProximaNova-Semibold
0.609, which is what it shipped**. A SEMIBOLD where his is an EXTRABOLD — the difference he saw on
sight.

Two traps only a render could reveal. ASS `Fontsize` is not the em square (libass sizes by
ascent+descent; this face renders **cap = Fontsize/2**, so 80 gave a 40px cap against his 57 — the
right number is **114**). `MarginV` is not the gap under the text (libass keeps the descender even
on all-caps, 11px here, so 499 rendered 523 — the right number is **487**).

**Correction to the record:** the "cyan vs yellow per-word" idea was wrong. Cyan is his per-word
emphasis inside a white block; a WHOLE block goes yellow for a directive. The mixed-case yellow
captions in his short are not his — they are the long-form's own burned-in captions showing through
the crop.

### Framing — the crop could not zoom
All 128 crop rects came back **w=606 h=1080 y=0**: a pure horizontal pan, never a zoom, and with
height pinned to the source height `--eye-line` had nothing to move. Two causes — `--shoulder-frac`
was **inert** whenever a head was visible (the expression cancelled it), and `--min-crop-frac 0.34`
made a punch-in **arithmetically impossible** on 16:9 (the floor sat above the full-height width).
New `--head-frac` default **0.212**, his measured median. He does punch in: 446×792 recovered at his
t=3.0s by SIFT+RANSAC and confirmed by rendering the rect back out.

**This also answers the old open question about close-up 16:9 without asking him.** His edit is
**full-bleed** — no padding, no blurred background. When he wants tighter he punches IN.

### The AV model path — two bugs that made it useless on his own files
`filepath.Glob` parses its whole argument as a pattern and `[` opens a character class. Frames are
cached in a directory named after the clip, and his files are yt-dlp output with the video id in
brackets — so every AV call on them got **zero frames**, or, when a sibling directory matched the
character class, **another clip's frames**. And the defaults could not finish: 30 frames at ~14s
each against a 240s timeout. Frames now downscale to 896 (Gemma-4's own tile size) and the count is
budgeted against the deadline.

### Cut detection — the one that moved the headline number
becky removed 7.6% where he removes 10.3%, and it was not the tightening amount: becky found only
17 of 24 real cuts, so there were fewer boundaries to tighten at. `cutTimes` advanced its run
marker on every over-threshold frame, so once the difference signal stayed high it could not emit
again — a busy stretch with several quick cuts collapsed into one. Candidates are now **local
maxima**, gated by a measured **prominence** (a cut is an isolated spike; motion is a ramp):

    phantoms, raw single-take footage : 2.24  2.42  2.79  2.89
    real cuts, the edited master      : 4.01  8.59  11.71 … 144.50

## WHAT IS NOT DONE — honestly

1. **Framing still loses the subject on about a third of spans.** On the two
   shorts the button picked itself, `--review` measured face coverage 0.80 and
   0.74, and 3-4 of 8-9 spans fell back to a static crop. The pose tracker is
   what gives up; an independent InsightFace pass finds faces in more of the same
   frames, so there is headroom here that has not been taken.
2. **Nothing joins WHAT to WHERE.** `--focal-point` is built and OFF because
   motion alone is one signal and it measured two spans better, two worse.
   **Marlin-2B is the missing second signal** and it works — but Marlin gives a
   TIME and focal gives an X, so together they corroborate a MOMENT, not a
   position. The position still has to come from focal.
3. **Marlin and becky-speaking are both blocked on the GPU, not capability.**
4. **The whole-block yellow caption** for a directive. Real in his edit — but
   only 2 instances in the reference clip, and fitting a rule to 2 samples is the
   mistake this session made twice and caught twice. Needs more of his shorts.
5. Face centre Y is 33.4% against his 29.7%. On a close subject a 9:16 crop of
   16:9 is already the full source height, so part of that gap is structural.
6. Both button-picked shorts still END mid-sentence. `becky-moment --extend`
   exists to finish a thought and did not. Worth a look: the moment END is chosen
   before the jumpcut pass runs, so the last complete sentence can be trimmed
   away afterwards.

## THE GPU IS NOT VISIBLE TO THIS SESSION — check this first

    Win32_VideoController -> "Intel(R) UHD Graphics" only
    nvidia-smi            -> "failed because you do not have sufficient permissions"
    torch (cu128)         -> cuda.is_available() False, device_count 0, but nvcuda.dll loads
    llama.cpp             -> "ggml_cuda_init: failed to initialize CUDA: no CUDA-capable
                              device is detected", Available devices: (none)
    Win32_PnPEntity       -> NVIDIA Virtual Audio + Platform Controllers present, NO GPU

llama.cpp failing the same way is what rules out a torch/wheel problem: the card is not visible
to ANYTHING. It is disabled or off the bus. A reboot, or re-enabling it in Device Manager, is
Jordan's one-click fix and it very likely turns Marlin from 22 minutes into seconds.

The RTX 3070 is not reachable. That is why Gemma-4 measured **~14s per frame** and why Marlin took
22 minutes on a 22-second clip. Both are seconds-per-clip work on the card. **Before concluding
that any model is too slow, re-check this.**

## Marlin-2B — gate opened, tested, and it is the right tool

`research/model-marlin-2b-TESTED.md`. Apache-2.0, 2B, ~4GB at bf16. Two calls:

    caption(video)           -> Scene paragraph + <start - end> timestamped events
    find(video, event='...')  -> a parsed (start, end) tuple

Checked against the frames on his snake clip: *"a person reacts with a shocked facial expression"*
→ **60.5–61.5s, correct** — the hand-to-head payoff right after "FAKE SNAKE PRANK!". That is the
harder half of RULE 4 (*"on Robby's face 1–3 frames BEFORE he realises"*) and becky could not do it
at all. *"the snake starts to move"* → 43.2–44.2s, which is the reveal rather than the motion onset:
right object, right region, wrong verb.

Reproduce with `scripts/marlin_probe.py` and the `.venv-marlin` venv (untracked — multi-GB torch;
`.gitignore` is hook-protected so it was left alone, and it will show in `git status`).

## Two things struck from the plan this session, both cherry-picks

- **"Pace with the content."** His 22 shot durations have no accelerate-then-hold arc, and the same
  window of the MASTER has the same shape. His shots are uniformly 0.18s shorter — that is the
  tightening, and it is the entire difference. becky's pacing model already IS his.
- **A coverage gate for focal aiming**, fitted to 4 spans on one clip and removed again.

**The method that caught both: measure the whole distribution, and measure the SOURCE too.** A
number from an edit means nothing until you know what the footage handed him for free.

## Gate status

    go build ./...          clean
    go vet ./...            clean
    go test ./...           green except THREE pre-existing failures, none ours:
                              cmd/tts             TestRun_DegradesWhenNoModel
                              internal/assistant  TestHandleTier2Funnel
                              cmd/daw             TestRun_ask_exitCodes/recognized_edit_ok (hangs;
                                                  confirmed by stashing our changes and re-running)
    build-all-tools.bat     ran clean, vision-smoke-gate PASS
    becky-short --selftest  53/53
    becky-moment --selftest 15/15
    ground.py --selftest    13/13
    crop_path.py --selftest 15/15
    internal/focal          7/7
    internal/shotcut        all green, precision 0.909 recall 0.833
