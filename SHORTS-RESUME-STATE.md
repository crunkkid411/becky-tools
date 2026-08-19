# SHORTS WORK — RESUME STATE

Written so any session can pick this up with no input from Jordan. Overwrite as things land.
Last updated 2026-08-19 ~10:10.

## The reference standard — read this before touching framing or cut timing

**`research/jordan-edit-reverse-engineered.md`.** Jordan handed over his own 30-second vertical
short plus the long-form it was cut from and said it is the standard. Because the vertical is a
crop of the master, his decisions were recovered as numbers (SIFT+RANSAC per frame, InsightFace over
all 915 frames, word-level transcript, cut detection on both). The three findings that changed the
build:

1. **He inherited the cuts, he did not choose them.** 11 of 14 aligned cuts land within 100ms of a
   cut that already exists in the long-form; 8 are frame-exact.
2. **He removes 10.3% of the time — 150ms per cut.** `becky-cut` alone removed 51% of a window.
3. **His cuts land on WORDS (median 57ms from a word boundary), not on silence.**

Plus the craft measurement: in shot 1 the master zooms in while his crop widens 668→1160px, holding
Allison at a flat 20.9→20.2% of frame height. **He cancels the master's camera move to keep his
subject locked.**

## Landed on master (all verified on real footage)

| Commit | What |
|---|---|
| `4f62d0d` | Overlap suppression. `--top 10` was ten re-cuts of ONE 68s stretch; now four regions across the whole video. |
| `a5985d5` | Captions burned in — and only this clip's words (110 → 18; the rest were from elsewhere in the video). |
| `ed33917` | The 21-repo research + build/skip decisions. |
| `387df0f` | Clip edges snap to real silence. 21/91 candidates moved. |
| `17a0699` | The judge names the opening line; a verbatim quote from mid-clip holds the clip as DISPUTED. |
| `7374be2` | `becky-speaking` — who is talking. 12/12 selftest, reproduces on real footage. |
| `078c6bb` | Jumpcuts inside the short (raw-footage path). |
| `bd8c200` | A relative `--out` was rendering into a temp dir and deleting it. |
| `6a79d0b` | Face coverage into moment ranking — a window where he is turned away drops #4 → #10. |
| `97a568a` | **The reverse-engineering of his own edit.** |
| `7f3590d` | `--eye-line` 0.38 → **0.27**, measured off his edit. Includes a retraction (see below). |
| `0facffe` | **Detect existing cuts, preserve them, reset the crop per shot.** |
| `5e67f7b` | One untrackable span was throwing away eighteen good ones. |

Measured end state on the already-edited case (BLINDFOLD master, 21.7–52.0s):
**19 spans, 18 of 19 existing cuts preserved, 2.734s tightened = 9.0%** against Jordan's own 10.3%
and becky-cut-alone's 51%. 45 captions, coverage 0.952, 6 spans honestly degraded to a static crop.

## Round 2 — the review pass, and the four bugs it found

`becky-short --review` (`c6d8d26`) re-measures the RENDERED file instead of trusting the plan:
an independent FACE pass (the render tracks with MediaPipe *pose*, so a disagreement is real
signal), a fresh transcription matched against the burned .srt, and `internal/moment`'s own payoff
check on the ending. **It earned its place immediately.**

| Commit | What it caught / did |
|---|---|
| `26ff2c4` | **The coverage number described only the spans that worked.** Claimed 0.952; an independent face pass said 0.18. A degraded span returned `Sampled=0, Found=0` so it vanished from both sides of the fraction. Honest: **0.579**. |
| `e39f51b` | **56 phantom cuts in a single-take file**, some 200ms apart. Brightness difference cannot tell a cut from fast motion. Added a greyscale-histogram check (false positives 0.935-0.969 vs real cuts 0.753-0.830). precision 0.833 -> **0.944**, raw footage 56 -> **0** cuts. |
| `4859d47` | **The same footage was classified differently depending on the window.** Whole file: 0 cuts. Window [102.4,138.72]: 2 cuts 133ms apart on one continuous shot. The threshold is data-derived, so a short static window lowers it. Cuts are now detected once per source and cached. |
| `795605e` | **Jumpcut captions showed words the jumpcut had removed.** 18.86s of cut dialogue was rescued back in and stacked onto the surviving spans - 16 cues in the first 3.3s of a 7.3s clip, one with ZERO duration. Caption checks: 0 of 4 passing -> **3 of 4**. |
| `a82d068` | Loudness -24.0 -> -19.2 LUFS, true peak -0.5 -> -1.5 dBFS. Does not reach -14 because the source is already peak-limited at -0.53 dBTP; stated rather than chased. |
| `571b3c3` | `ground.py` - ask WHERE a thing is when there is no face. **Honest result: per-frame grounding of a small target is NOT trustworthy** (2 of 4 frames, one on empty counter, 0.47 median jump), so it reports `stable:false` and calls itself a region hint. |
| `6ffa324` | The launcher told Jordan to make a transcript becky already makes. |
| `99ec909` | One of the 22 research notes was 4,774 words of a model's chain-of-thought. Validator now rejects repeated headings and first-person reasoning. |

**Every one of these rendered a file that played and exited 0.** That is the whole argument for the
review pass, and for reading output instead of code.

## In flight

Nothing. All agents have landed.

## A retraction worth not repeating

I measured becky framing at 39.7% face height against his 24.3% and concluded it frames far too
tight. **Wrong — it is the footage.** A 9:16 crop of 1920×1080 is at most 608×1080, the full source
height, and on `test-for-clips.mp4` his face is already 37.8% of the SOURCE height. There is no crop
that makes it smaller and no spare source to shift up or down. Changing `--shoulder-frac`
0.46 → 0.30 moved it 39.7% → 40.7%: nothing. `--shoulder-frac` was reverted; `--eye-line` was kept
because it is right whenever vertical freedom exists.

## What is left, in the order that matters

1. **Caption style.** His are 2–3 stacked lines, 2–4 words per line, one word coloured per block —
   **cyan** on the stressed word of a reaction, **yellow** for a directive or the running joke —
   profanity in a red box, emoji as accents, and placement that moves with the content rather than a
   fixed MarginV. becky ships one flat white style at a fixed margin. **This is his look: show it to
   him as an option, never switch it on by default.**
2. **Frame the gesture, not just the face.** His shot 19 is 1.27s on a pointing finger with no face
   in frame; becky found the same beat and framed the whole machine wide. Reka Edge's grounded boxes
   (`Detect: pointing hand`) are the tool — verified running here, see below.
3. **Zoom as an editorial device.** becky has none. `clip-forge` plans punch-ins, jump zooms over
   cuts, and slow creep; `internal/audiosig` already measures the energy that would drive them.
   Build ONE, show him, stop.
4. **Pace with the content.** His shots run 0.70/0.73/1.10/0.53 through the argument, then hold
   2.73s on the punchline. Accelerate in, hold the payoff.
5. **Loudness to −14 LUFS** (`crop.RenderArgs`, `-af loudnorm=I=-14:TP=-1.5:LRA=11`). One line.
6. Retire the last degrade paths (HANDOFF §7 item 6): `cmd/motion` frame-diff → optical flow,
   `cmd/framematch/decor.go` census → ORB+RANSAC.

## The open question only Jordan can answer

On close-up 16:9 footage where the subject already fills the frame, does he want the full-height
crop we do now, or a padded/blurred background with the subject on his 30% line? His own edit is
full-bleed but never had to solve that case. **Ask; do not guess.**

## Facts worth not rediscovering

- **cv2/insightface/mediapipe live in `X:\PythonUserBase\Lib\site-packages`**, reached with
  anaconda python + `PYTHONPATH`. They are NOT importable from the default interpreter — this cost
  time. `internal/crop`'s `pythonFor()` resolves it via `cfg.FacePython` + `cfg.FacePyLib`.
- **Reka Edge runs here** — 6496 MiB VRAM, `Detect:` in 2.3s, six 640px frames = 450 prompt tokens.
  Build 9551's `mtmd.dll` already has the yasa2 tensors, no rebuild. `--reasoning off` is not
  optional; parse its boxes with a tolerant regex (the first `<bbox>` is closed by `</answer>`).
- **OpenCode Zen has no key on this machine.** Only `OPENROUTER_API_KEY`. The repo research ran on
  OpenRouter `:free` via `scripts/research-repos.py` (resumable; validates that a note has real
  sections before writing — the first run wrote four files of a model's chain-of-thought).
- **`nvidia/nemotron-3.5-lightning:free` writes its reasoning into `content`** — useless for
  structured output. `nemotron-3-ultra-550b-a55b:free` and `nemotron-3-super-120b-a12b:free` are clean.
- **Do not put `\n` escapes inside a python heredoc in the Bash tool here** — they arrive as real
  newlines and produce unterminated string literals in generated Go.
- **Never chain `git commit` in a compound Bash command** — it trips the master-branch hook. And a
  commit message containing certain flag-like strings can trip the no-verify guard; write the
  message to a file and use `-F`.
- Two test failures on master are PRE-EXISTING and not ours: `cmd/tts TestRun_DegradesWhenNoModel`
  and `internal/assistant TestHandleTier2Funnel`.
- Test footage is `test-for-clips.mp4` (raw) and the BLINDFOLD long-form (already edited) — the two
  cases behave very differently and both must keep working. `E:\TakingBack2007` is the CRIMINAL
  CASE — never for editing work.

## VERIFIED GAPS in `shorts-user-feedback.md` — still open, found 2026-08-19 15:20

Its research half is done (22 repo notes, the iPhone sweep, the Reka evaluation). Two of its
RULES are not:

1. **RULE 4 — "EDIT ACCORDING TO CONTEXT" is NOT implemented.** His worked example is the
   rubber-snake prank: the snake is the focal point on a POV shot with no faces, and *"the framing
   must be on Robby's face 1 - 3 frames BEFORE he realizes what is happening"*. Nothing in the
   chain can do that. The pieces now exist — `ground.py` (grounded boxes, but measured UNSTABLE
   per-frame on small targets), `shotcut`, `audiosig`, the Reka dense-burst finding (six frames =
   450 tokens) — but nothing joins them into "understand the moment, then override the geometric
   rule". **This is the single largest remaining gap and it is the hardest one.**

2. **The 12B vision model is on disk but is NOT the default.** He wrote *"we have
   gemma-4-12B-it-qat-UD-Q4_K_XL.gguf and absolutely should utilize it"*.
   `models/gemma4/gemma-4-12B-it-qat-UD-Q4_K_XL.gguf` + `mmproj-12B-BF16.gguf` are both present,
   and `config.GemmaAVLM()` selects them only when `BECKY_AVLM_VARIANT=12b` is set. Everything in
   the shorts chain therefore runs on **E4B**. Check the 8GB VRAM budget before switching the
   default — 12B + BF16 mmproj is much larger than E4B, and Reka already measured at 6496 MiB, so
   these cannot be co-resident.
