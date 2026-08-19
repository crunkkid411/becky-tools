# antiboredom/videogrep

Source: https://github.com/antiboredom/videogrep | licence: NOASSERTION

Read for `shorts-user-feedback.md`: what step does this run that becky does not?
Written by a free model reading the source; the build/skip judgement is not its call.

---

## What it is
Videogrep is a command-line tool that takes video or audio files with matching subtitle tracks (SRT, VTT, JSON, or pocketsphinx transcripts) and produces a "supercut" — a concatenated video or audio file containing only the segments matching a regex search query. It can also transcribe media using Vosk (offline) or pocketsphinx, and export in multiple formats including MP4, MP3, MPV EDL, M3U playlist, and Final Cut Pro XML.

## The pipeline, in order
1. **Argument parsing & dispatch** – `cli.py:main()` parses flags, handles `--ngrams`, `--transcribe` (Vosk), `--sphinx-transcribe` (pocketsphinx), or the main supercut path.
2. **Transcript location** – `videogrep.py:find_transcript()` looks for a subtitle file with the same basename as the media file (extensions `.json`, `.vtt`, `.srt`, `.transcript`).
3. **Transcript parsing** – `videogrep.py:parse_transcript()` routes to `srt.parse()`, `vtt.parse()`, `json.load()`, or `sphinx.parse()`; all return a list of dicts with `start`, `end`, `content`, and optionally `words[]` (word-level timestamps).
4. **Search / segment selection** – `videogrep.py:search()` runs one of three modes:
   - `sentence`: regex on each `line["content"]`
   - `fragment`: consecutive word-level match (requires `words` in transcript)
   - `mash`: for each query word, pick a random occurrence from the word stream
5. **Segment post-processing** – `videogrep.py:pad_and_sync()` applies `--padding` and `--resyncsubs`; `remove_overlaps()` merges abutting/overlapping clips from the same source file.
6. **Output planning** – `plan_video_output()`, `plan_audio_output()`, `plan_no_action()` decide whether to render video, audio, or error (audio→video not supported).
7. **Rendering** – `create_supercut()` or `create_supercut_in_batches()` (batch size 20) uses MoviePy to subclip and concatenate; `export_individual_clips()`, `export_mpv_edl()`, `export_m3u()`, `export_xml()` (FCP XML), `export_vtt()` write sidecar formats.
8. **Optional preview** – `--preview` launches the result in `mpv`.

## Models, libraries and services
| Component | Used in stage | Local / Paid API |
|-----------|---------------|------------------|
| MoviePy (`VideoFileClip`, `AudioFileClip`, `concatenate_videoclips`, `concatenate_audioclips`) | Rendering (steps 7) | Local (Python lib) |
| Vosk (`Model`, `KaldiRecognizer`) | Transcription (`--transcribe`, step 2) | Local (requires model folder download) |
| pocketsphinx (`pocketsphinx_continuous` binary) | Transcription (`--sphinx-transcribe`, step 2) | External binary (local) |
| imageio-ffmpeg (`get_ffmpeg_exe()`) | Vosk audio extraction (step 2) | Local (bundled ffmpeg) |
| beautifulsoup4 | VTT cue parsing (`vtt.py:parse_cued`) | Local (Python lib) |
| ffmpeg (via subprocess) | pocketsphinx WAV conversion (`sphinx.py:convert_to_wav`) | External binary (local) |
| yt-dlp | Example script `auto_youtube.py` only | External binary (local) |

## Prompts
No LLM prompts found in the source. Selection is purely regex / string matching on transcript text.

## How it decides WHAT to clip
File: `videogrep/videogrep.py`, function `search()`.

- **sentence mode** (default): each transcript entry (`line["content"]`) is tested against every regex in `query` list; matching entries become clips with that entry's `start`/`end`.
- **fragment mode**: requires word-level timestamps (`"words"` key). The query is split on whitespace; the word stream is scanned with a sliding window of that length; a clip is emitted when every word in the window matches the corresponding regex fragment.
- **mash mode**: query split on whitespace; for each word, all matching word occurrences are collected, one is chosen at random (`random.shuffle`), and a one-word clip is emitted.
- **Max clips**: `--max-clips` truncates the final segment list after sorting by `start` time (no scoring/ranking beyond temporal order).

## How it decides framing / cropping
No visual reframing or cropping occurs. Clips use the exact `start`/`end` timestamps from the transcript (sentence or word level). `--padding` adds symmetric seconds to both sides; `--resyncsubs` shifts all timestamps by a constant. Overlapping/adjacent clips from the same source file are merged by `remove_overlaps()` and `pad_and_sync()`.

## Multi-pass or iteration
None. The pipeline is single-pass: search → post-process → render. `create_supercut_in_batches` splits concatenation into groups of `BATCH_SIZE=20` for memory management, but does not re-evaluate or refine selections.

## Steps here that a transcript-first clipper would MISS
- **Unified transcript abstraction**: `parse_transcript()` normalises SRT, VTT, Vosk JSON, and pocketsphinx output to the same dict schema (`start`, `end`, `content`, `words[]`).
- **Word-level fragment search**: `search()` with `search_type="fragment"` slides a regex window over the word stream, enabling sub-sentence precision.
- **Mash mode**: `search_type="mash"` randomly samples one occurrence per query word, useful for poetic/randomised supercuts.
- **Overlap merging across same file**: `remove_overlaps()` and `pad_and_sync()` coalesce adjacent clips from the same source, avoiding duplicate frames.
- **Multi-format export**: single call can emit MP4/MP3, MPV EDL, M3U, FCP XML, and WebVTT simultaneously.
- **Batch concatenation**: `create_supercut_in_batches()` avoids MoviePy memory blow-up on long supercuts.
- **Automatic transcription fallback**: `--transcribe` downloads/runs Vosk locally; `--sphinx-transcribe` shells out to pocketsphinx.

## Worth stealing
1. **`videogrep/vtt.py:parse_cued()`** – extracts per-word timestamps from YouTube-style VTT `<hh:mm:ss.mmm>` cue tags.
2. **`videogrep/videogrep.py:search()`** – three search modes in ~120 lines; fragment mode’s sliding-window regex over word stream is compact and reusable.
3. **`videogrep/videogrep.py:pad_and_sync()` + `remove_overlaps()`** – robust timestamp sanitation (padding, resync, cross-clip overlap merge, negative-time clamping).
4. **`videogrep/fcpxml.py`** – complete FCP 7 XML generator (`Clip`, `Sequence`, `compose()`) enabling round-trip to Premiere/Resolve.
5. **`examples/auto_supercut.py`** – n-gram frequency → stopword filter → top-k → random pick → fragment search; a self-contained “auto supercut” recipe.
6. **`BATCH_SIZE = 20` constant** – simple but effective guard against MoviePy memory growth during long concatenations.

Licence: `NOASSERTION` (per repo description); `pyproject.toml` declares `license = "Anti-Capitalist"`.
