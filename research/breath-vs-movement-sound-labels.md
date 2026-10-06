# Breath or movement? Naming the sound in a wordless gap (2026-10-06, local)

RESEARCH + TEST + PROPOSAL, then BUILT the same day after Jordan's yes (markers only; see "Built" at
the end).

## The question

Jordan, 2026-10-06: *"does silero VAD identify exactly what the non-speech sounds are? If not, then we
need a dedicated VAD for that second VAD pass. Ten VAD is an option I've explored and might be installed
locally somewhere already, if that doesn't do it, then please research and propose a current VAD model
for this speciffic task. NVIDIA, Qwen and lots of others have cutting edge VAD type models"* and
*"because for example, fast movement in my chair does not = breath, and that does change the nature of
the edit"*.

## Answer: no VAD can name a sound

A VAD answers one question, "is someone talking?", frame by frame. Checked on each model's own card:

| VAD | What it outputs |
|---|---|
| Silero VAD (becky-cut's post-pass) | a speech probability per frame |
| TEN VAD (installed: anaconda base `ten_vad` 1.0.6.8; Jordan used it in `X:\AI-2\AI_VIDEO_Editor\storytime_edit_reverse_engineer_2026-01-27`) | "binary speech indicators (0 for non-speech signal, 1 for speech signal)" |
| NVIDIA Frame-VAD Multilingual MarbleNet v2.0 | "a speech probability for each 20 millisecond frame"; trained so coughing, laughter and breathing count as non-speech |

Naming the sound is a different job: **sound event detection** (SED). It labels each short frame from
the AudioSet Strong list (447 classes), which includes Breathing, Gasp, Sigh, Sniff, Pant, Snort, Rustle,
Creak, Squeak, Rub, Scrape, Thump/thud, Tap, Clicking, Generic impact sounds, Surface contact,
Crumpling/crinkling, Hands, Typing and Male speech.

## The pick: PretrainedSED `BEATs_strong_1`

- Code: github `fschmid56/PretrainedSED` (MIT, ICASSP 2025 paper "Effective Pre-Training of Audio
  Transformers for Sound Event Detection"). BEATs itself is Microsoft's (`microsoft/unilm`, MIT). Fine
  for a monetized channel.
- Best student in the paper: 46.5 PSDS1 on AudioSet Strong (ATST-F 45.8, M2D 46.3, ASiT 46.2).
- One label set every 40 ms; runs in 10 s chunks of 16 kHz mono (250 frames each).
- **Measured on this PC:** 15:00 of audio in 4.3 s on the RTX 3070, peak 0.46 GB of graphics memory,
  347 MB checkpoint. Runs from anaconda base (torch 2.5.1+cu121, librosa, soundfile) plus `einops`.
- Trap: the plain "Speech" class outputs 0.00 everywhere on his voice. Use "Male speech, man speaking"
  (0.55 inside transcript words vs 0.15 outside).
- Probe: `research/breath-sed-probe.py` (clone PretrainedSED next to it).

## Test on Jordan's footage

`X:\Videos\2026\09_sept\27-livestream` (the becky-livestream test stream). 114 loud wordless gaps: at
least 0.3 s between transcript words, inside becky-cut's kept pieces, louder than becky-cut's -43 dB
threshold. 102 s in total.

"Picture moving" = frame difference (72x128 gray, 10 fps) averaging at least 2x his median while talking,
or peaking at 3.5x or more.

| The sound labeler hears | picture still | picture moving | seconds |
|---|---|---|---|
| breath (>= 0.3, movement sounds < 0.15) | 24 | 8 | 25 |
| breath + a movement sound | 10 | 8 | 36 |
| a movement sound only (>= 0.15) | 7 | 6 | 17 |
| neither | 45 | 6 | 23 |

- **25 of 96 gaps are really his voice.** More than half the inner frames are "Male speech" (after
  trimming 0.12 s at each edge): the transcript's word times are off there. These must never be cut.
- **Checked by eye in frame strips:**
  - 13:36 and 0:16: a breath sound DURING a big movement (sitting up fast with his hands through his
    hair; lunging at the camera). Audio alone calls both a breath. This is Jordan's chair point exactly.
  - 4:15: he is out of frame, then comes back with a drink (movement sound 0.24, breath 0.05).
  - 14:20: hand gestures (impact and surface-contact sounds).
  - 14:07: a clean breath while still.
- **`breath.go`'s loudness-only "Breath example" markers were wrong.** These markers are in the three
  `27-livestream-*.veg` projects. Only 14:07 (Claude's project, marker 3 of 4) is a clean breath. The
  rest:
  - 3:21: his voice (100% voice frames, mouth moving).
  - 12:13: looking at his notes, then reaching for water.
  - 13:36: a big movement.
  - 14:04: not a breath (40% voice frames).
  - 14:20: hand gestures.

## Ruled out (from the model cards, this session)

- **NVIDIA Audio Flamingo Next** (and its captioner and think versions) and **NVIDIA audio-visual
  Flamingo**: "NVIDIA OneWay Noncommercial" license (research only), and 7B models.
- **shlv/AudioJev** (a Qwen2.5-Omni-3B fine-tune): Qwen Research License (no commercial use), about
  18.8 GB in FP32.
- **Qwen3-Omni-30B-A3B-Captioner**: 30B, far beyond 8 GB of graphics memory.
- **CED** (mispeech, Apache-2.0, ONNX): strong, but one label per clip, with no frame timing.
- **MiDashengLM-0.6B**: a captioner, and it needs a llama.cpp fork.
- **PANNs CNN14 and AST**: older, and one label per clip.
- **Gemma-4 E4B as a tie-breaker**: not needed. When the two signals disagree, the gap stays
  (corroborate, then conclude).

## Proposal (Jordan said yes on 2026-10-06 - see "Built" below)

1. A separate pass after becky-cut and BeckyCut that never touches either of them.
2. A gap counts as a breath only when BOTH signals agree:
   - the sound labeler hears a breath, with no movement sound and no voice;
   - the picture is still.
3. A movement is never treated as a breath, his voice is never touched, and anything else is left alone.
4. The first run places markers only, replacing `breath.go`'s loudness markers. Nothing is cut until
   Jordan judges those markers.
5. How it would be built:
   - a thin pyhelper returns per-gap labels as JSON;
   - the motion check runs through ffmpeg in Go;
   - the pass itself stays in `cmd/livestream/breath.go`.

## Built (2026-10-06, the same day: Jordan said "yes")

Built as proposed, as becky-livestream's breath check (`cmd/livestream/breath.go`, `motion.go`,
`internal/pyhelpers/sound_labels.py`). Markers only - nothing is cut.

- **The sound labeler** runs once per edit over every candidate gap (anaconda Python, CUDA, about
  10 s including the model load). It never downloads: the checkpoint must already be in
  `models\sed\PretrainedSED\resources`. Per gap, with 0.1 s trimmed off each end (word edges carry
  speech): the strongest breathing score (Breathing, Pant), the strongest OTHER sound and its name,
  and the share of 40 ms frames with a voice above 0.5.
- **"Other" leaves out** the room (Background noise, Mechanisms, hum, the room classes) and
  breathing by another name (Gasp, Sigh, Sniff, Snort, Wheeze, Snoring). The clean breath at 14:07
  also scored Sigh 0.15, and a sigh is still breathing; a cough, a laugh or a bump is not, and it
  stays "other".
- **The picture**: ffmpeg, 10 frames a second at 72x128 gray, mean pixel change from the frame
  before, judged against his median while talking; cached as `becky-edit\<video>.motion.json`.
- **The verdict, in order**: a voice in more than 20% of the gap = voice; mean movement 1.5x his
  talking or more, or a peak of 2.5x or more = movement; another sound at 0.15 or more = other
  sound; breathing at 0.30 or more = breath; anything else = unclear. The bounds lean to "not a
  breath", because a missed breath only leaves a breath in.
- **Calibration by eye (20 gaps in frame strips)**: the first draft's movement bounds (2.0x / 3.5x)
  passed 0:18 as still while he turned away, so they were tightened to 1.5x / 2.5x. Hand on the
  mouth at 8:46 now counts as movement, which is safe. One known miss: a slow hand-in-hair at 8:16
  scores as still, so it would be marked a breath (it is in none of the three edits). Whole-frame
  change catches fast movement; a person-pose signal would be the upgrade if slow movements matter.
- **No loudness test.** The loudness gate was the old proxy, and it hid two real breaths (13:18 and
  14:10, peaks around -44 dB against becky-cut's -43 dB threshold). Every wordless gap of 0.35 s or
  more inside one kept piece is checked; a silent gap ends "unclear" and is left alone.
- **If a signal cannot be measured**, no breath region is placed and the report says why. It never
  falls back to loudness.

Result on the three 27-livestream projects (`--breaths-only`; the final version ran twice on each
project, with the same result both times). Each edit had only 7 pauses long enough to check, because
BeckyCut takes the rest out.

| Project | Old "Breath example" markers | Were breaths | New "Breath check" regions |
|---|---|---|---|
| Gemma-4 | 5 | 0 (3 movement, 2 voice) | 1 (13:18) |
| Qwen3.5 | 5 | 0 (the same 5 gaps) | 1 (13:18) |
| Claude | 4 | 1 (14:07; plus 2 movement, 1 voice) | 3 (13:18, 14:07, 14:10) |

13:18 and 14:10 are new: both were quieter than becky-cut's threshold, so the loudness rule never
saw them.

Every new region was read back from VEGAS and mapped to the stream time of its breath. The
projects were saved, and backups of the 2026-10-05 versions are in
`X:\Videos\2026\09_sept\27-livestream\becky-edit\veg-backup-2026-10-06\`.
