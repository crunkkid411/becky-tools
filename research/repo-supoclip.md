# FujiwaraChoki/supoclip

Source: https://github.com/FujiwaraChoki/supoclip | licence: AGPL-3.0

Read for `shorts-user-feedback.md`: what step does this run that becky does not?
Written by a free model reading the source; the build/skip judgement is not its call.

---

## What it is
SupoClip takes a long-form video (YouTube URL or direct upload) and emits multiple short, vertical (9:16) clips with burned-in word-synced captions, optional emoji annotations, and virality scores. The pipeline runs asynchronously via an ARQ/Redis worker; results are stored in PostgreSQL and served through a FastAPI backend with a Next.js frontend.

## The pipeline, in order
1. **Ingest & source detection** – `backend/src/main.py:start_task()` decides `source.type` (`youtube` vs `upload`), fetches YouTube title via `get_youtube_video_title()`, persists `Source` and `Task` rows.
2. **Video acquisition** – YouTube: `download_youtube_video()` (from `youtube_utils`); Upload: `_resolve_uploaded_video_path()`.
3. **Transcription** – `get_video_transcript(video_path)` (from `video_utils`) calls AssemblyAI and returns a transcript with ~10-character line equalization.
4. **AI segment selection & virality scoring** – `get_most_relevant_parts_by_transcript(transcript, include_broll)` (from `ai`) returns `most_relevant_segments` (each with `start_time`, `end_time`, `text`, `relevance_score`, `reasoning`, and nested `virality` scores: `total_score`, `hook_score`, `engagement_score`, `value_score`, `shareability_score`, `hook_type`, `virality_reasoning`).
5. **Clip rendering** – `create_clips_with_transitions(video_path, segments_json, output_dir, font_family, font_size, font_color, caption_template)` (from `video_utils`) burns captions, applies transitions, writes MP4s.
6. **Persistence** – Each clip becomes a `GeneratedClip` row with timestamps, scores, and file path; `Task.generated_clips_ids` updated.
7. **Optional post-processing** – `clip_editor.py` exposes `trim_clip_file`, `split_clip_file`, `merge_clip_files`, `overlay_custom_captions`, `export_with_preset` for later manual edits (not used in the automatic `/start` flow).

## Models, libraries and services

| Component | Stage | Local / Paid API |
|-----------|-------|------------------|
| AssemblyAI | Transcription (step 3) | Paid API (`ASSEMBLY_AI_API_KEY`) |
| LLM (Google Gemini / OpenAI / Anthropic / Ollama) | Segment selection & virality scoring (step 4) | Paid API or local (`LLM`, `GOOGLE_API_KEY`, `OPENAI_API_KEY`, `ANTHROPIC_API_KEY`, `OLLAMA_BASE_URL`) |
| yt-dlp / Apify actor | YouTube download (step 2) | Local (yt-dlp) or Paid API (`APIFY_API_TOKEN`) |
| FFmpeg | All video rendering (steps 5, 6, clip_editor) | Local binary |
| PostgreSQL + asyncpg | Persistence | Local |
| Redis + ARQ | Job queue / worker | Local |
| Better Auth | Authentication | Local (self-hosted) |

## Prompts
No LLM prompt strings are visible in the provided files. The call `get_most_relevant_parts_by_transcript` is imported from `ai` but that module is not included.

## How it decides WHAT to clip
**File:** `backend/src/main.py` (line 138) calls `get_most_relevant_parts_by_transcript(transcript, include_broll)`. The function lives in `backend/src/ai.py` (not provided). The returned object contains `most_relevant_segments` with fields `relevance_score`, `reasoning`, and a nested `virality` object (`total_score`, `hook_score`, `engagement_score`, `value_score`, `shareability_score`, `hook_type`, `virality_reasoning`). No thresholds or heuristics are visible in the given source; selection logic is entirely inside the missing `ai.py`.

## How it decides framing / cropping
**File:** `backend/src/clip_editor.py:export_with_preset()` implements the only visible framing logic. It forces 9:16 (1080×1920) via:
```python
scale_filter = (
    f"scale={preset.width}:{preset.height}:"
    "force_original_aspect_ratio=decrease:flags=lanczos,"
    f"pad={preset.width}:{preset.height}:(ow-iw)/2:(oh-ih)/2,"
    "setsar=1"
)
```
No face detection, active-speaker tracking, or dynamic panning (`vertical_pan`, `vertical_split` are accepted as `output_format` values in the MCP server but no implementation is visible in the provided files). The automatic `/start` flow uses `create_clips_with_transitions` from `video_utils` (not provided), so its cropping behaviour is **not visible in the files read**.

## Multi-pass or iteration
Nothing in the provided code re-runs a stage, re-checks its own output, or refines clips. The `/start` endpoint executes steps 1–6 once per task. The worker (`worker_main.py`) simply dequeues jobs; no retry-with-different-parameters or quality-loop is present.

## Steps here that a transcript-first clipper would MISS
- **YouTube metadata & download** – `get_youtube_video_title()` + `download_youtube_video()` (or Apify fallback) before any transcript exists.
- **AssemblyAI transcription with 10-char line equalization** – `get_video_transcript()` returns transcript *and* equalized lines (used later for caption timing).
- **Virality-scored segment objects** – AI returns structured `virality` sub-object per segment (hook/engagement/value/shareability scores + `hook_type`).
- **Transition-aware clip rendering** – `create_clips_with_transitions()` applies named transitions between clips (transition files live in `backend/transitions/`).
- **Emoji + power-word caption annotation** – `emoji_captions.annotate_caption_words()` adds deterministic emojis (rate-limited: ≤8/clip, min 3-word gap, 8-word repeat gap) and highlights power words / numbers.
- **Caption template system** – `caption_templates.get_template()` supplies font, animation, color presets (`default`, `hormozi`, `mrbeast`, etc.) used at burn-in time.
- **Clip cleanup knobs** – `clip_cleanup.py` exposes `cut_long_pauses` (pause threshold 250–3000 ms, default 900 ms) and `remove_filler_words` with a built-in list (`um`, `uh`, `you know`, …) plus user-supplied words.
- **Export presets per platform** – `export_with_preset()` encodes to TikTok/Reels/Shorts bitrate/resolution profiles (CRF 18, slow preset, 10–12 Mbps video, 192k audio).

## Worth stealing
1. **`backend/src/emoji_captions.py`** – Deterministic, rate-limited emoji injection + power-word emphasis for burned captions. No external dependency, ~150 lines. (AGPL-3.0)
2. **`backend/src/clip_cleanup.py`** – Normalisation helpers for pause-threshold (clamped 250–3000 ms) and filler-word lists with de-duplication. (AGPL-3.0)
3. **`backend/src/clip_editor.py:export_with_preset()`** – Single-function, platform-targeted FFmpeg scale/pad/encode chain (TikTok/Reels/Shorts). (AGPL-3.0)
4. **Virality score schema** – The nested `virality` object (`total_score`, `hook_score`, `engagement_score`, `value_score`, `shareability_score`, `hook_type`, `virality_reasoning`) is a clean contract for downstream ranking. (Seen in `main.py` response construction)
5. **Transition directory convention** – `backend/transitions/` holds named `.mp4` transitions; `create_clips_with_transitions` (in `video_utils`) consumes them. (AGPL-3.0)

**Licence:** AGPL-3.0 (see `README.md` and `LICENSE`).
