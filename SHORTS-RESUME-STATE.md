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

## In flight

- **Agent — `becky-short --review`**: the self-review pass (Jordan's rule 5). Deterministic first:
  re-measure the RENDERED file for subject-in-frame, caption/audio alignment, completed ending.
  None of the 21 reference projects does this.

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
