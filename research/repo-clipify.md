# louisedesadeleer/clipify

Source: https://github.com/louisedesadeleer/clipify | licence: MIT

Read for `shorts-user-feedback.md`: what step does this run that becky does not?
Written by a free model reading the source; the build/skip judgement is not its call.

---

## What it is
A Claude Code skill that takes a local video file (talking-head / two-person dialogue) and produces 9:16 (or 16:9 / 1:1) social clips with burned-in word-by-word captions. It transcribes with local Whisper, proposes 3–5 candidate segments via an LLM prompt, lets the user pick one, then reframes 16:9 → 9:16 by hard-cut panning between two speaker ROIs detected via ffmpeg frame-differencing motion energy, and renders opus/karaoke/minimal ASS subtitles.

## The pipeline, in order
1. **Transcribe** – `whisper` (CLI) produces `WHISPER.json` with word-level timestamps. (Invoked from the skill prompt, not a script in this repo.)
2. **Propose clips** – The skill prompt (in `SKILL.md`, not provided) asks an LLM to scan the transcript and return 3–5 candidate segments with titles/timestamps. (Not visible in the files read.)
3. **User selects clip & aspect ratio** – Interactive prompts in the skill. (Not visible in the files read.)
4. **Extract sub-clip audio for alignment** – ffmpeg extracts the chosen segment as 8 kHz mono PCM. (Not visible in the files read.)
5. **Align sub-clip to source** – `scripts/audio_align.py` cross-correlates the sub-clip PCM against a window of source PCM to get an absolute start offset in the original video.
6. **If 9:16 from 16:9 with two faces – build speaker timeline**  
   a. ffmpeg `signalstats` on two fixed ROIs (left/right mouth+chin rectangles) → two motion-energy text files.  
   b. `scripts/analyze.py` reads both, normalises, smooths (window=15 frames), decides speaker per frame with 1.15× margin, merges segments shorter than `MIN_DUR` (default 1.0 s), collapses adjacent same-speaker segments → JSON array of `{start, end, speaker}`.
7. **Build pan expression** – `scripts/build_pan.py` reads the segments JSON and two x-coordinates (`LEFT_X`, `RIGHT_X`), emits an ffmpeg `crop` x-expression with nested `if(lt(t,end),x,…)` hard cuts.
8. **Generate ASS subtitles** – `scripts/build_ass.py` reads `WHISPER.json`, chunks words (3/4/6 per style), writes opus/karaoke/minimal ASS with per-word highlight (opus/karaoke) or whole-chunk (minimal).
9. **Final render** – ffmpeg command (assembled in the skill) crops with the pan expression (or center-crop for 1:1), burns the ASS, encodes with VideoToolbox H.264. Output lands in `<source-dir>/clipify_out/`.

## Models, libraries and services

| Component | Stage | Local / Paid API |
|-----------|-------|------------------|
| openai-whisper (CLI) | Transcription | Local |
| ffmpeg (libx264, videotoolbox, signalstats, crop, ass) | Audio extract, ROI motion, pan crop, subtitle burn, encode | Local |
| numpy (FFT) | `audio_align.py` cross-correlation | Local |
| Python stdlib (re, json, sys) | `analyze.py`, `build_pan.py`, `build_ass.py`, `audio_align.py` | Local |
| LLM (Claude Code) | Clip proposal from transcript | Paid API (via Claude Code) |

## Prompts
The LLM prompt that proposes clips lives in `SKILL.md`, which was **not provided in the files read**. No other prompts are present in the supplied scripts.

## How it decides WHAT to clip
**Not visible in the files read.** The selection/ranking logic is inside the skill prompt (`SKILL.md`) that asks an LLM to scan the Whisper transcript and return 3–5 candidates. No thresholds, scores, or heuristics are implemented in the provided Python scripts.

## How it decides framing / cropping
**File: `scripts/analyze.py` + `scripts/build_pan.py`**  
- Two fixed ROIs (left/right mouth+chin rectangles) are sampled on one frame (manual step in the skill).  
- ffmpeg `signalstats` emits per-frame YAVG (luma average) for each ROI → motion energy via frame differencing.  
- `analyze.py` normalises each trace by its mean, smooths with a 15-frame moving average, then assigns speaker per frame: current speaker holds unless the other ROI exceeds it by **1.15×** (`MARGIN`).  
- Segments < **1.0 s** (`MIN_DUR`) are merged into the previous segment; adjacent same-speaker segments are collapsed.  
- `build_pan.py` converts the segment list into a nested `if(lt(t,end),LEFT_X|RIGHT_X,…)` expression for ffmpeg `crop=1080:1920:x='EXPR':y=0` (hard cuts, no interpolation).  
- If the user chooses split-screen or 16:9/1:1, the pan step is skipped (center crop or no crop).

## Multi-pass or iteration
**No.** Each script runs once, linearly. The skill does not re-check its own output, refine the speaker timeline, or re-rank clips. The only “iteration” is the human-in-the-loop selection of which proposed clip to render.

## Steps here that a transcript-first clipper would MISS
- **Motion-energy speaker diarisation without a face detector** – uses ffmpeg `signalstats` on two hand-picked ROIs, normalised + smoothed + 1.15× margin hysteresis (`analyze.py`).
- **Hard-cut pan expression** – nested `if(lt(t,end),x,…)` generated by `build_pan.py` lets ffmpeg crop-follow the speaker with zero interpolation cost.
- **FFT cross-correlation alignment** – `audio_align.py` finds the absolute source offset of a user-chosen sub-clip from 8 kHz mono PCM, so the final render uses exact source timestamps.
- **Opus-style ASS with per-word active highlight** – `build_ass.py` chunks words (3/4/6), emits one `Dialogue` line per word with `{\c&H0000FFFF&}` highlight on the active word (opus preset).
- **Single-frame ROI calibration** – the skill asks the user to “eyeball each face’s mouth+chin area as a rectangle on one sample frame”; no ML face detection runs at all.

## Worth stealing
1. **`scripts/analyze.py`** – 70-line speaker timeline from two ROI motion traces: normalise → smooth (WIN=15) → 1.15× margin hysteresis → merge <1 s → collapse. Zero dependencies beyond stdlib.
2. **`scripts/build_pan.py`** – 20-line generator of ffmpeg `crop` x-expression with hard cuts from a segment list; trivial to adapt for N speakers.
3. **`scripts/audio_align.py`** – FFT cross-correlation (int16 PCM @ 8 kHz) to lock a sub-clip to its source; useful any time you need to map a trimmed segment back to the master timeline.
4. **`scripts/build_ass.py`** – Clean opus/karaoke/minimal ASS presets with per-word highlight via inline `{\c}` tags; chunk sizes (3/4/6) and colours are constants at the top.
5. **ROI motion-energy diarisation concept** – No face model, no OpenCV, just ffmpeg `signalstats` on two rectangles. Works for static-camera two-person talks.
6. **Licence** – MIT (see `LICENSE` in repo root).
