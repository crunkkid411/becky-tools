# Anil-matcha/AI-Youtube-Shorts-Generator

Source: https://github.com/Anil-matcha/AI-Youtube-Shorts-Generator | licence: not stated

Read for `shorts-user-feedback.md`: what step does this run that becky does not?
Written by a free model reading the source; the build/skip judgement is not its call.

---

## What it is
A CLI tool and Python library that takes a YouTube URL (or local video file in local mode) and produces N vertical short-form clips (default 9:16, 720p source) ranked by a virality scoring framework. It emits rendered MP4s (hosted URLs in API mode, local file paths in local mode) plus a JSON payload containing the full transcript, all candidate highlights with scores/hooks/reasons, and the final selected clips.

## The pipeline, in order
1. **Download source video** — `shorts_generator/downloader.py:download_youtube()` (API mode) calls MuAPI `/youtube-download`; `shorts_generator/local/downloader.py:download_youtube_local()` (local mode) uses `yt-dlp` with resolution selector, caches by YouTube ID in `LOCAL_OUTPUT_DIR`.
2. **Transcribe** — `shorts_generator/transcriber.py:transcribe()` (API) posts the hosted video URL to MuAPI `/openai-whisper` with `response_format=verbose_json`; `shorts_generator/local/transcriber.py:transcribe_local()` (local) runs `faster-whisper` (model/device from env) and caches `.srt` next to the source.
3. **Detect content type & density** — `shorts_generator/highlights.py:detect_content_type()` sends first ~25 segments (≤3000 chars) to LLM with `CONTENT_TYPE_PROMPT`; returns `{content_type, density}`.
4. **Chunk long videos** — `shorts_generator/highlights.py:chunk_transcript()` splits transcripts > `LONG_VIDEO_THRESHOLD` (1800s) into `CHUNK_SIZE_SECONDS` (1200s) windows with `CHUNK_OVERLAP_SECONDS` (60s) overlap; each chunk gets `_offset` for later time correction.
5. **Highlight ranking (LLM)** — `shorts_generator/highlights.py:call_highlight_api()` builds a system prompt from `HIGHLIGHT_SYSTEM_PROMPT` + `VIRALITY_CRITERIA` + content info, asks for ≥ `min_clips` (≈2× user target, capped at 8) highlights per chunk; retries up to `MAX_HIGHLIGHT_API_ATTEMPTS` (3) on parse failure. LLM backend is pluggable: `call_muapi_llm()` (API mode, uses MuAPI `gpt-5-mini`) or `call_local_llm()` (local mode, OpenAI `gpt-4o-mini` or Gemini `gemini-2.5-flash`).
6. **Sanitize & offset-correct** — `_sanitize_highlights()` clamps times to chunk duration, coerces score 0–100; chunk results have `_offset` added back.
7. **Dedupe overlapping highlights** — `dedupe_highlights()` sorts by score descending, drops any highlight that overlaps >50% of its own duration with a higher-scoring kept highlight.
8. **Select top-N** — `pipeline.py:_run_api()` / `_run_local()` take top `num_clips` by score.
9. **Vertical crop / render** — API: `shorts_generator/clipper.py:crop_highlights()` calls MuAPI `/autocrop` per highlight with `start_time`, `end_time`, `aspect_ratio`. Local: `shorts_generator/local/clipper.py:crop_highlights_local()` runs ffmpeg subclip (`libx264`, CRF 20, AAC 128k) then OpenCV Haar-cascade face tracking with 0.15 smoothing to slide a vertical crop window, muxes audio back.

## Models, libraries and services

| Component | Stage | Local / Paid API |
|-----------|-------|------------------|
| MuAPI `/youtube-download` | Download (API mode) | Paid API (MuAPI key) |
| `yt-dlp` | Download (local mode) | Local (Python pkg) |
| MuAPI `/openai-whisper` (server-side `whisper-1`) | Transcription (API mode) | Paid API (MuAPI key) |
| `faster-whisper` (models: tiny/base/small/medium/large-v3) | Transcription (local mode) | Local (CPU/CUDA) |
| MuAPI `gpt-5-mini` | Highlight ranking (API mode) | Paid API (MuAPI key) |
| OpenAI `gpt-4o-mini` (default) | Highlight ranking (local mode, `LLM_PROVIDER=openai`) | Paid API (OpenAI key) |
| Google Gemini `gemini-2.5-flash` (default) | Highlight ranking (local mode, `LLM_PROVIDER=gemini`) | Paid API (Gemini key) |
| MuAPI `/autocrop` | Vertical crop (API mode) | Paid API (MuAPI key) |
| `ffmpeg` (libx264, AAC) | Subclip + mux (local mode) | Local (system binary) |
| OpenCV `haarcascade_frontalface_default.xml` | Face tracking for crop (local mode) | Local (bundled model) |
| `python-dotenv` | Config loading | Local (Python pkg) |
| `requests` | HTTP client for MuAPI | Local (Python pkg) |

## Prompts
```
CONTENT_TYPE_PROMPT = """Analyze this video transcript sample and classify the content type.
Choose one: podcast, interview, tutorial, lecture, commentary, debate, vlog, other.
Also estimate content density: low (mostly filler/chit-chat), medium, or high (dense info/stories).
Respond with JSON only: {"content_type": "...", "density": "..."}"""
```

```
VIRALITY_CRITERIA = """
Virality signals to prioritize (ranked by impact):
1. HOOK MOMENTS — statements that create immediate curiosity ("The secret is...", "Nobody talks about...", "I was completely wrong about...")
2. EMOTIONAL PEAKS — genuine surprise, laughter, anger, vulnerability, excitement; raw unscripted reactions
3. OPINION BOMBS — strong, polarizing or counter-intuitive statements that trigger agree/disagree
4. REVELATION MOMENTS — surprising facts, stats, or confessions that reframe how the viewer thinks
5. CONFLICT/TENSION — disagreement, pushback, or a problem being confronted head-on
6. QUOTABLE ONE-LINERS — a sentence that works as a standalone quote card
7. STORY PEAKS — the climax or twist of an anecdote; the payoff moment
8. PRACTICAL VALUE — a concrete tip, hack, or insight the viewer can immediately apply
"""
```

```
HIGHLIGHT_SYSTEM_PROMPT = """You are an elite short-form video editor who has studied thousands of viral clips on TikTok, Instagram Reels, and YouTube Shorts. You know exactly what makes viewers stop scrolling, watch to the end, and share.

{virality_criteria}

Content type: {content_type} | Density: {density}

Your task: identify the most viral-worthy highlights from the transcript.

Rules:
- Every highlight must open with a strong HOOK — a line that grabs attention within the first 3 seconds
- Duration sweet spot: 45-90 seconds. Go shorter (20-44s) only for a perfect standalone one-liner. Go longer (91-180s) only when a story arc needs full context to land
- Never cut mid-sentence or mid-thought — each clip must feel complete and self-contained
- Clips must not overlap significantly with each other
- Score 0-100 on viral potential (not general quality)
- {num_clips_instruction}
- For each highlight, identify the single best "hook_sentence" — the opening line that would make someone stop scrolling
- Explain in one sentence why this clip is viral ("virality_reason")

Respond ONLY with valid JSON (no markdown, no explanation):
{{"highlights":[{{"title":"string","start_time":float,"end_time":float,"score":int,"hook_sentence":"string","virality_reason":"string"}}]}}"""
```

## How it decides WHAT to clip
File: `shorts_generator/highlights.py`

- **Content-aware prompt tuning**: An LLM classifies the transcript sample into one of 8 content types (`podcast`, `interview`, `tutorial`, `lecture`, `commentary`, `debate`, `vlog`, `other`) and density (`low`/`medium`/`high`). These values are injected into the system prompt.
- **Virality framework**: The `VIRALITY_CRITERIA` string (8 ranked signals) is embedded verbatim in the system prompt. The LLM is instructed to score 0–100 on "viral potential (not general quality)".
- **Chunking for long videos**: Videos ≥ 1800s are split into 1200s chunks with 60s overlap. Each chunk is processed independently with `is_chunk=True`, asking for at least `min_clips = min(max(num_clips*2, 5), max(2, duration/90), 8)` highlights. Offsets are added back after.
- **Dedupe by overlap**: After collecting all chunk highlights, `dedupe_highlights()` sorts by score descending and discards any highlight where `overlap_duration > 0.5 * highlight_duration` with a higher-scoring kept highlight.
- **Top-N selection**: Pipeline takes the first `num_clips` from the deduped, score-sorted list.
- **No secondary re-ranking or verification pass** — the LLM's score is trusted as final.

## How it decides framing / cropping
- **API mode** (`shorts_generator/clipper.py`): Delegates entirely to MuAPI `/autocrop` endpoint. The request sends `video_url`, `start_time`, `end_time`, `aspect_ratio`. No local logic; the service returns a hosted vertical MP4 URL.
- **Local mode** (`shorts_generator/local/clipper.py`):
  1. ffmpeg cuts `[start, end]` to a temporary file (re-encode libx264 CRF 20, AAC 128k).
  2. OpenCV loads Haar cascade `haarcascade_frontalface_default.xml`.
  3. For each frame: detects faces (`scaleFactor=1.1, minNeighbors=5, minSize=(40,40)`), picks largest face, computes center `(cx, cy)`.
  4. Smooths center position with exponential moving average `smoothing=0.15` (chases new face position 15% per frame).
  5. Computes crop window: largest rectangle at target aspect ratio fitting inside source; centers horizontally on smoothed `cx`, vertically on smoothed `cy` (clamped to frame bounds).
  6. Writes silent cropped video, then ffmpeg muxes original audio back.
- **No scene-change detection, no speaker diarization, no active-speaker tracking beyond largest face** — pure face-center smoothing.

## Multi-pass or iteration
- **LLM highlight generation retries**: `call_highlight_api()` retries up to `MAX_HIGHLIGHT_API_ATTEMPTS` (3) *only* when the model returns unparseable JSON or zero valid highlights. On retry it appends a stricter instruction to the prompt.
- **No iterative refinement of selected clips**: Once top-N are chosen, they are cropped once. No re-scoring, no boundary adjustment based on visual content, no silence-aware trimming.
- **No multi-pass transcription**: Whisper runs once (API or local). No VAD re-segmentation or alignment pass.
- **No dedupe re-check after cropping**: Overlap suppression happens on transcript timestamps only; rendered clips could still have visual overlap if timestamps are close but not >50%.

## Steps here that a transcript-first clipper would MISS
- **Content-type + density classification** before highlight prompting — the same virality criteria are applied but the prompt context changes (e.g., "podcast | high density" vs "vlog | low density").
- **Long-video chunking with 60s overlap** — ensures highlights crossing the 20-min boundary aren't lost; offsets are corrected after LLM returns.
- **Score-based overlap dedupe** (>50% of *candidate's* duration) — not a fixed time window, but proportional to each highlight's length.
- **Hook sentence extraction as explicit LLM output field** — not derived post-hoc; the model must name the exact opening line.
- **Local-mode face-smoothed vertical crop** — Haar cascade + 0.15 EMA smoothing, not center-crop or static saliency.
- **Cached local transcription (`.srt`) and download (`source_<id>.mp4`)** — skips Whisper/yt-dlp if cached file is newer than source.
- **Pluggable LLM backend via `llm_fn`** — same prompt logic drives MuAPI `gpt-5-mini`, OpenAI `gpt-4o-mini`, or Gemini `gemini-2.5-flash` without code changes.

## Worth stealing
1. **`shorts_generator/highlights.py:VIRALITY_CRITERIA`** — concise, ranked 8-signal framework that fits in a system prompt; portable to any LLM-based clipper.
2. **`shorts_generator/highlights.py:dedupe_highlights()`** — 15-line overlap suppression using proportional threshold (50% of candidate duration) instead of fixed seconds; works on any timestamped segments.
3. **`shorts_generator/highlights.py:chunk_transcript()` + offset correction** — clean pattern for long-context LLM limits: chunk with overlap, process independently, add `_offset` back.
4. **`shorts_generator/local/clipper.py:_reframe_vertical()`** — dependency-free face tracking (OpenCV Haar + EMA smoothing) that produces a moving crop window; no heavy models, runs on CPU.
5. **`shorts_generator/pipeline.py` mode dispatcher** — single `generate_shorts()` entry point with `mode="api"|"local"` swapping entire backend stacks (download/transcribe/LLM/crop) via pluggable functions.
6. **`shorts_generator/config.py` env-driven config with validation helpers** — `require_api_key()`, `require_openai_key()`, `require_gemini_key()` give clear errors at call site, not import time.
7. **`shorts_generator/transcriber.py:_extract_verbose_payload()`** — defensive parsing of inconsistent API response shapes (dict, list, stringified JSON) to extract `segments` + `duration`.

**Licence**: Not stated in any provided file (README says "MIT licensed" in badge but no LICENSE file visible).
