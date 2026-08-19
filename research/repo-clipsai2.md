# JerielCodes/clipsai2

Source: https://github.com/JerielCodes/clipsai2 | licence: not stated

Read for `shorts-user-feedback.md`: what step does this run that becky does not?
Written by a free model reading the source; the build/skip judgement is not its call.

---

## What it is
Clips AI is a video processing pipeline that takes a YouTube URL or local video file, downloads/extracts audio, transcribes it, finds candidate clips using text-based heuristics, scores them with an LLM for viral potential, exports the top clips as MP4 files, and optionally scans YouTube Shorts for viral reposts of the same content. It emits a set of short vertical clips (MP4) with metadata (scores, timestamps, transcripts) and a viral scan report.

## The pipeline, in order
1. **Download video** – `clipsai.utils.video.download_youtube_video` (YouTube) or direct file copy (local) — `main.py:180`, `app.py:78`, `gradio_app.py:68`
2. **Extract audio** – `clipsai.utils.audio.extract_audio` (ffmpeg) — `main.py:187`, `app.py:85`
3. **Transcribe** – `clipsai.transcribe.transcriber.Transcriber.transcribe` (Whisper) — `main.py:192`, `app.py:92`
4. **Find candidate clips** – `clipsai.clips.clipfinder.ClipFinder.find_clips` — `main.py:197`, `app.py:99`
5. **AI viral scoring** – `clipsai.ai_viral_scorer.AIViralScorer.score_clip` — `main.py:202`, `app.py:106`
6. **Sort & select top-N** – in-pipeline sort by `ai_score` descending, take top 5 (mobile) or top 10 (desktop) — `app.py:113`, `gradio_app.py:77`
7. **Export clips** – `clipsai.utils.video.export_clip` (ffmpeg copy codec) — `main.py:210`, `app.py:118`
8. **Viral scanner (optional)** – `clipsai.viral_scanner.scan_youtube_shorts` — `main.py:220`, `BOT_INTEGRATION_README.md`

## Models, libraries and services

| Component | Stage | Local / Paid API |
|-----------|-------|------------------|
| Whisper (via `whisper` package) | Transcription | Local |
| yt-dlp | Video download / metadata | Local |
| ffmpeg | Audio extract / clip export | Local |
| Selenium + ChromeDriver | YouTube Shorts bot search | Local (requires Chrome) |
| YouTube Data API v3 | Viral scanner fallback search | Paid API (quota) |
| OpenAI GPT (via `openai` package) | AI viral scoring | Paid API |
| DeepFace (Facenet512) | Facial emotion scoring (optional) | Local |
| sentence-transformers (all-MiniLM-L6-v2) | Semantic similarity in viral scanner | Local |
| rapidfuzz | Fuzzy title matching in viral scanner | Local |

## Prompts
**File:** `clipsai/ai_viral_scorer.py` (not provided in file list, but referenced in imports) — **not visible in the files read**

No LLM prompt text appears in any of the supplied files.

## How it decides WHAT to clip
**File:** `clipsai/clips/clipfinder.py` (not provided) — **not visible in the files read**

The pipeline calls `ClipFinder.find_clips(transcription)` and then scores each candidate with `AIViralScorer.score_clip(clip_text, context=video_title)`. The top-N by `ai_score` are exported. No thresholds or heuristics are visible in the provided files.

## How it decides framing / cropping
**File:** `clipsai/utils/video.py` function `export_clip` — **not visible in the files read**

The provided files only call `export_clip(video_path, start_time, end_time, output_path, overwrite_existing=True)`. No reframing, vertical crop, or face-tracking logic is present in the visible code.

## Multi-pass or iteration
**No multi-pass or iteration detected.** Each stage runs once sequentially. The viral scanner can be run independently (`run_viral_scanner_only` in `main.py`), but the main pipeline does not re-check or refine its own output.

## Steps here that a transcript-first clipper would MISS
- **Selenium+yt-dlp YouTube Shorts bot** that clicks the Shorts tab, scrolls, and extracts view/like counts from the live UI (`selenium_bot/yt_search_bot.py`, `clipsai/youtube_shorts_bot.py`)
- **Fallback chain**: bot → YouTube Data API → HTML scraping for view counts (`BOT_INTEGRATION_README.md`)
- **Facial emotion scoring** (DeepFace Facenet512) on exported clip thumbnails (`main_broken.py:process_clip_facial`, `test_deepface.py`)
- **Visual fingerprinting** (pHash + face embeddings) for cross-platform duplicate detection (`clipsai/professional_matcher.py` referenced in tests)
- **Pause/Resume/Cancel hotkeys** (P/R/C/V) with persistent `resume_state.json` (`main.py:wait_if_paused`, `start_pause_listener`)
- **Pushover notifications** on start/complete/error (`clipsai.utils.pushover.send_pushover_notification`)
- **Sleep prevention** during long runs (`clipsai.utils.system.prevent_sleep`)

## Worth stealing
1. **`selenium_bot/yt_search_bot.py`** – robust YouTube Shorts metadata scraper that handles dynamic UI, with yt-dlp fallback for missing fields. (No licence visible)
2. **`clipsai/viral_scanner.py`** – multi-method search (bot, API, yt-dlp) with quota-aware fallback and semantic/fuzzy/audio fingerprint matching. (No licence visible)
3. **`main.py` resume system** – step-grained `resume_state.json` with dependency graph (`can_resume_from_step`) and 30-second timed prompt. (No licence visible)
4. **`clipsai/utils/pause.py`** – global hotkey listener (keyboard) for pause/resume/cancel/status without blocking the pipeline. (No licence visible)
5. **`clipsai/face_emotion.py` + `professional_matcher.py`** – DeepFace Facenet512 embeddings + pHash visual fingerprint for cross-platform clip matching. (No licence visible)
