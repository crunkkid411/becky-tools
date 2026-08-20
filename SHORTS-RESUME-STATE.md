# SHORTS WORK — RESUME STATE

Written so any session can pick this up with no input from Jordan. Overwrite as things land.
Last updated 2026-08-19 ~21:45.

## The reference standard — read this before touching framing or cut timing

**`research/jordan-edit-reverse-engineered.md`.** Jordan's own 30-second vertical short plus the
long-form it came from. The vertical is a crop of the master, so his decisions are recoverable as
numbers. Three findings that changed the build: he INHERITED the cuts (11 of 14 within 100ms of an
existing master cut, 8 frame-exact); he removes 10.3% of the time, not half; his cuts land on WORDS
(median 57ms from a word boundary), not on silence.

## The test that matters — use these two files, nothing else

    master  2024-08-30_We_Tried_the_ULTIMATE_Fast_Food_Test_BLINDFOLD_Tasting_[unswA5Jv7fI].mp4
    his cut 2024-08-30_spitters_are_quitters..._[7409071570410327339].mp4

His short is **master 22.11s -> 54.07s**. So the apples-to-apples run is

    becky-short -video <master> -start 22.11 -end 54.07 -caption-style jordan -out <x>.mp4

and then measure the render against his. A previous session showed him a comparison built from a
DIFFERENT video and he spotted it immediately. Do not do that again.

## Landed 2026-08-19 evening (5 commits, all on master, all verified on that footage)

| Commit | What |
|---|---|
| `b8a7b5f` | **The caption look, measured off his own render.** |
| `27e3d24` | **becky could not zoom at all** — and the knob for it was not connected. |
| `28561a9` | **A `[` in a filename fed the model the wrong video, or none.** |
| `26ba612` | **The shipped AV defaults could never finish**, so they returned nothing. |
| `895597d` | `internal/focal` — where to point when there is no face. |

### Captions — done, and it matches

Font settled by rendering every heavy sans installed here at his measured cap height and scoring
glyph IoU against his actual pixels: **Montserrat ExtraBold 0.803**, Gotham Black 0.801, Segoe UI
Black 0.796, Arial Black 0.783, and **ProximaNova-Semibold 0.609 — what it used to ship**. A
SEMIBOLD where his is an EXTRABOLD; that is the difference he saw on sight.

Two things only a render could reveal: ASS `Fontsize` is not the em square (libass sizes by
ascent+descent, so this face renders cap = Fontsize/2 — 80 gave a 40px cap against his 57, the
right number is **114**), and `MarginV` is not the gap under the text (libass keeps the descender
even on all-caps, 11px here, so 499 rendered 523 — the right number is **487**).

Line breaking now belongs to libass (WrapStyle 3 in a 66%-wide column) instead of a fixed
3-words-per-line rule that put THREE lines on screen where he never uses more than two.

**Measured on the render, his vs becky: cap 57 / 57, text bottom 512 / 511.**

**Correction to the record:** the "cyan vs yellow per-word" hypothesis was WRONG. Measured across
his clip, cyan is his per-word emphasis inside a white block, and a WHOLE block goes yellow for a
directive ("FRENCH FRY FIRST", "PUT THE"). The mixed-case yellow captions visible in his short are
not his at all — they are the long-form's own burned-in captions showing through the crop.

### Framing — the crop could not zoom

All 128 crop rects over the window came back **w=606 h=1080 y=0**. A pure horizontal pan, never a
zoom, and with height pinned to the source height there was no vertical freedom either, so
`--eye-line` had nothing to move. Two causes:

1. `--shoulder-frac` was **inert** whenever a head was visible — the expression cancelled it out, so
   0.30/0.46/0.58/0.70 all returned the same crop. This is why the earlier "0.46 -> 0.30 moved it
   one percentage point" result was written off as "it is the footage".
2. `--min-crop-frac 0.34` made a punch-in **arithmetically impossible** on any 16:9 source: the
   floor (652.8px) sat above the full-height 9:16 width (607.5px).

He does punch in — recovered 446x792 at his t=3.0s by SIFT+RANSAC and confirmed by rendering the
recovered rect back out. New `--head-frac`, default **0.212** (his measured median), replaces the
dead knob; `--min-crop-frac` 0.23.

    metric          Jordan   before   after
    face height      23.6%    24.0%   23.6%   <- exact
    face centre Y    29.7%    36.0%   35.0%
    punch-ins            -    0/128   35/128

Face size is now exact. The remaining 5 points of vertical is a real structural limit: on a close
subject a 9:16 crop of 16:9 is already the full source height.

**This also answers HANDOFF question 2 without asking him.** His own edit is **full-bleed** — no
padding, no blurred background. When he wants tighter he punches IN.

### The AV model path — two bugs that made it useless on his own files

- `filepath.Glob` parses its WHOLE argument as a pattern, and `[` opens a character class. Frames
  are cached in a directory named after the clip, and Jordan's files are yt-dlp output with the
  video id in brackets — so every AV call on them got **zero frames** (or, when a sibling directory
  matched the character class, **another clip's frames**). Fixed with `pathx.FilesIn` in all four
  places that globbed a literal directory.
- The shipped defaults could not finish: 30 frames at ~14s each against a 240s timeout. Frames are
  now downscaled to 896 (Gemma-4's own tile size, so nothing the model can use is lost) and the
  frame count is budgeted against the deadline.

Measured cold on the 8GB card with E4B QAT: **1920x1080 = 273 tok / 17.1s per frame; 896 = 218 /
13.5s; 640 = 113 / 5.9s**, all three giving the same answer.

### RULE 4 — the VL can do it, and the deterministic half is built

On the rubber-snake clip, a POV shot with **no face in frame**, becky-validate now returns:

    FOCUS  = the coiled yellow garden hose
    CHANGE = 46.0s, when the speaker mentions a snake

That is Rule 4's shape working for the first time. `internal/focal` is the other half — the
horizontal aim, from the spatial part of the frame difference `becky-motion` throws away, refusing
unless the moving region is a region (not a camera move) and the centroid stays put.

## WHAT IS NOT DONE — honestly

1. **`internal/focal` is NOT wired into the crop path.** The insertion point is the static-centre
   fallback: `jumpcuts.go` ~line 487 (the `forceCenter=true` retry) and `main.go`'s `crop.Run` error
   branch. Both branch on `len(cr.Rects) > 0`, so returning a ONE-rect slice aimed by focal works
   through the existing sendcmd/FilterChain machinery with no new render code. Needs
   `crop.StaticAt(srcW, srcH, aspect, xFrac)` — StaticCenter with an X. Do NOT override an explicit
   user `--center`.
2. **Nothing joins the VL's WHAT to focal's WHERE.** Corroborate-then-conclude: the VL naming a
   thing is one signal, focal's stable aim is the second. Only commit when both agree.
3. **Pace with the content** (his shots run 0.70/0.73/1.10/0.53 through the argument then hold 2.73s
   on the punchline) and **zoom as an editorial device** — punch-ins now happen geometrically, but
   nothing chooses them for dramatic reasons.
4. **The whole-block yellow caption** for a directive. Real in his edit, needs a semantic call.
5. `becky-speaking` is built (12/12) but not wired into framing.

## The ONE thing that needs Jordan, and it is one click

**Marlin-2B is a GATED Hugging Face repo.** Verified against his own authenticated session, not
just the free model's 401: *"Access to model NemoStation/Marlin-2B is restricted and you are not in
the authorized list."* Nobody can read the card, config or weights until he clicks request-access on
https://huggingface.co/NemoStation/Marlin-2B. It is interesting because a 2B video model with native
temporal grounding may fit the 8GB budget better than Gemma-4 12B.

## Round-2 research verdicts (`research/shorts-gap-decisions.md`)

EditDuet (arXiv 2509.10761) — SKIP, 8xH100 + GPT-4o judge + CC BY-NC-ND. TimeLens (arXiv
2512.14698) — SKIP the task, but its Table 2 finding is ADOPTED and shipped: interleaved raw-text
timestamps beat a list up front, which is what becky was sending. Aero Realtime 4B — NO, a streaming
assistant model, wrong problem and it does not fit the card. Marlin-2B — BLOCKED, see above.

## Gate status at this checkpoint

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
    crop_path.py --selftest 15/15 (7 of them new)
    internal/focal          7/7
