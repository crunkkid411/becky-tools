# Breath or movement? Naming the sound in a wordless gap (2026-10-06, local)

RESEARCH + TEST + PROPOSAL. Nothing is built into becky-tools. The breath pass waits for Jordan's yes.

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

## Proposal (awaiting Jordan's yes)

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
