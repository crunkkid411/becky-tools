# msnodderly/vcut

Source: https://github.com/msnodderly/vcut | licence: MIT

Read for `shorts-user-feedback.md`: what step does this run that becky does not?
Written by a free model reading the source; the build/skip judgement is not its call.

---

## What it is
vcut is a text-based video editor that transcribes video to a timestamped transcript using faster-whisper, lets the user edit that transcript (deleting or commenting lines to cut content), then renders the edited video by extracting and concatenating the kept segments via ffmpeg. It takes a video file as input and emits an edited video file; the transcript is the editable intermediate representation.

## The pipeline, in order
1. **Audio extraction** — `src/vcut/transcribe.py:extract_audio()` uses ffmpeg to decode the input video to a 16 kHz mono WAV file in a temporary directory.
2. **Transcription** — `src/vcut/transcribe.py:transcribe()` loads a faster-whisper model (default `distil-large-v3`, int8 compute), runs `model.transcribe()` with `word_timestamps=True`, and returns raw whisper segments.
3. **Chunk merging** — `src/vcut/transcribe.py:merge_words_into_chunks()` consumes the word-level timestamps from all segments and groups words into chunks of approximately `chunk_size` seconds (default 3 s), producing a list of dicts with `start`, `end`, `text`.
4. **Transcript serialization** — `src/vcut/transcribe.py:segments_to_text()` formats each chunk as `[HH:MM:SS.mmm -> HH:MM:SS.mmm] | text` and writes to `{video}.txt`.
5. **User edit** — The user (or an agent) edits the transcript file: lines prefixed with `#` or deleted are treated as cuts.
6. **Transcript parsing** — `src/vcut/editor.py:parse_edited_file()` reads the edited file, skips empty/commented lines, parses timestamps with `parse_timestamp()`, validates ordering (start < end, non-decreasing across segments), and merges overlapping/adjacent segments.
7. **Segment extraction** — `src/vcut/render.py:render()` iterates the kept segments; for each it runs ffmpeg either in stream-copy mode (`-ss` before `-i`, `-c copy`) or re-encode mode (`-ss` after `-i`, full decode/encode), writing segment files to a temp directory.
8. **Concatenation** — `render()` writes a concat demuxer list (`file 'seg_XXXX.mp4'`) and runs ffmpeg `-f concat -safe 0 -c copy` to produce the final output video.

## Models, libraries and services

| Component | Used in stage | Local / Paid API |
|-----------|---------------|------------------|
| faster-whisper (WhisperModel) | Transcription (step 2) | Local (downloads model weights to `~/.cache/huggingface/`) |
| ffmpeg | Audio extraction, segment extraction, concatenation (steps 1, 7, 8) | Local binary on `$PATH` |
| rich | Progress bars, console output (CLI, transcribe, render) | Local Python package |
| Python stdlib (subprocess, tempfile, pathlib, re, shlex, os) | Throughout | Local |

## Prompts
No LLM prompts are present in the source. The `AGENT_HELP` string in `cli.py` is a usage guide for AI agents, not a prompt sent to an LLM.

## How it decides WHAT to clip
It does not decide. The user (or an external agent) decides by editing the transcript file: any line that is deleted or begins with `#` is excluded; all other lines are kept. The selection logic is entirely external. The only internal logic is validation in `parse_edited_file()` (src/vcut/editor.py) which enforces chronological order and merges overlapping/adjacent segments.

## How it decides framing / cropping
It does not reframe or crop. The output video retains the original resolution and aspect ratio. Segment extraction uses `-c copy` (stream copy) or full re-encode without any video filters for cropping/scaling.

## Multi-pass or iteration
No multi-pass or iterative refinement exists. Transcription runs once. Rendering runs once per `vcut render` invocation. The `vcut edit` command opens an editor once, then renders once on exit. There is no re-checking of output, no confidence-based re-transcription, no iterative boundary adjustment.

## Steps here that a transcript-first clipper would MISS
- **Word-level timestamp merging into fixed-duration chunks** — `merge_words_into_chunks()` groups raw whisper words into ~3 s segments (configurable via `--chunk-size`), producing cleaner editable boundaries than raw whisper segments.
- **Overlapping/adjacent segment merging on parse** — `parse_edited_file()` automatically merges segments where `start < previous_end`, preventing jitter/glitches at cut points.
- **Stream-copy vs re-encode toggle** — Default stream copy seeks to keyframes (fast, imprecise); `--reencode` does frame-accurate cuts via decode/encode.
- **Concat demuxer for lossless joining** — Uses ffmpeg `-f concat -c copy` to join extracted segments without re-encoding the join.
- **Agent-oriented CLI help** — `AGENT_HELP` in `cli.py` documents editing patterns (contiguous clip, supercut, removal) and exact command templates for programmatic use.

## Worth stealing
1. **`merge_words_into_chunks()`** (src/vcut/transcribe.py) — Clean, dependency-free function that turns word-level whisper output into fixed-duration editable chunks. MIT licence.
2. **`parse_edited_file()`** (src/vcut/editor.py) — Robust transcript parser with timestamp validation, chronological enforcement, and automatic overlap merging. MIT licence.
3. **Render pipeline** (src/vcut/render.py) — Two-mode extraction (stream copy / re-encode) + concat demuxer is a minimal, correct pattern for transcript-driven cutting. MIT licence.
4. **`AGENT_HELP` string** (src/vcut/cli.py) — A well-structured prompt/instruction block that teaches an LLM how to drive the tool for three editing patterns (contiguous clip, supercut, removal). MIT licence.
5. **Chunk-size parameter** — Exposing `--chunk-size` at transcribe time lets downstream search/clip tools trade boundary granularity for transcript readability. MIT licence.
