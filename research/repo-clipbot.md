# SeanSpon/clipbot

Source: https://github.com/SeanSpon/clipbot | licence: not stated

Read for `shorts-user-feedback.md`: what step does this run that becky does not?
Written by a free model reading the source; the build/skip judgement is not its call.

---

## What it is
ClipBot is an AI-powered video editing pipeline that takes a source video (uploaded file or cloud URL), transcribes it, uses an LLM "Director" to identify viral-worthy segments and plan camera cuts, then renders vertical short-form clips (9:16 default) with karaoke-style captions. It emits rendered MP4 clips with thumbnails, plus a shot list and transcript.

## The pipeline, in order
1. **Download video** – `api/routers/process.py:_run_pipeline()` calls `services.storage.storage.download_from_url()` if a `video_url` is provided and no local file exists.
2. **Transcribe** – `ai.transcription.transcribe()` (WhisperX / OpenAI Whisper) with progress callbacks; stores word-level timestamps and speaker diarization in project `transcript` field. Implemented in `api/routers/process.py` step 2.
3. **AI Director analysis** – `director.master.MasterDirector.direct()` receives the transcript, project ID, and preferences; returns a `ShotList` and list of `Clip` objects with `start_time`, `end_time`, `virality_score`, `tags`, and per-clip `shot_list`. Implemented in `api/routers/process.py` step 3.
4. **Persist clips** – `database.create_clip()` writes each clip to SQLite; SSE `clip_found` events emitted for frontend. `api/routers/process.py` after director returns.
5. **Render clips** – `services.renderer.renderer.render_clips()` cuts the source video per clip timestamps, burns captions (with filler-word removal option), normalizes audio, applies aspect ratio (9:16, 1:1, 4:5, 16:9), and writes MP4s to `exports/{project_id}/`. `api/routers/process.py` step 4 and `api/routers/render.py:start_render()`.
6. **Generate thumbnails** – Renderer produces a `.jpg` thumbnail per clip (checked in `api/routers/render.py:list_exports()`).

## Models, libraries and services

| Component | Stage | Local / Paid API |
|-----------|-------|------------------|
| WhisperX / OpenAI Whisper (`ai.transcription.transcribe`) | Transcription | Local (WhisperX) or Paid (OpenAI Whisper API) – provider selectable |
| Anthropic Claude (`ai.providers.get_provider("anthropic")`) | AI Director | Paid API (requires `ANTHROPIC_API_KEY`) |
| OpenAI GPT (`ai.providers.get_provider("openai")`) | AI Director (alternative) | Paid API (requires `OPENAI_API_KEY`) |
| FFmpeg (via `services.renderer`) | Rendering | Local binary |
| SQLite (`database.py`, `api/models/clip.py`) | Persistence | Local file (`clipbot.db`) |
| Vercel Blob (`@vercel/blob/client`) | Upload/Download | Paid service |
| Prisma + Neon (`@prisma/client`, `@prisma/adapter-neon`) | ORM / DB (frontend) | Paid (Neon serverless Postgres) |
| FastAPI + Uvicorn | API server | Local |
| Next.js + React + Framer Motion | Frontend | Local |

## Prompts
No LLM prompt strings are visible in the provided files. The director logic resides in `director/master.py` which was not included.

## How it decides WHAT to clip
Not visible in the files read. The selection/ranking logic lives in `director.master.MasterDirector.direct()` which is not provided. The pipeline passes the full transcript and preferences to the director and receives back `Clip` objects with `virality_score` (0–100), `tags`, and `start_time`/`end_time`. No thresholds or heuristics are exposed in the given code.

## How it decides framing / cropping
Not visible in the files read. The renderer (`services.renderer.renderer.render_clips`) accepts `aspect_ratio` (default "9:16") and produces vertical clips, but the actual reframing/cropping logic (face tracking, active speaker detection, etc.) is inside the renderer module which was not provided.

## Multi-pass or iteration
No multi-pass or self-refinement loops are visible. The pipeline runs linearly: download → transcribe → direct → render. The director is called once per project; the renderer processes each clip once. Job manager (`services.job_manager.job_manager`) tracks progress but does not re-run stages.

## Steps here that a transcript-first clipper would MISS
- **Multi-camera labeling at upload** – Frontend `CameraSetup` component lets users assign a label (e.g., "wide", "closeup") to each uploaded file before processing; labels flow into the director via `shot_list`.
- **Style reference upload** – `StyleReferenceInput` accepts a reference video + user prompt; stored as `style_reference` on the project and presumably consumed by the director (not visible in provided files).
- **Per-clip shot list from director** – Each `Clip` carries its own `shot_list` (camera angles, cut timings) produced by the LLM director, not just a single time range.
- **Filler-word removal at render time** – `remove_fillers` flag passed to renderer strips filler words from burned captions (WhisperX word timestamps enable this).
- **Audio normalization to social-media loudness** – `normalize_audio` flag in render step.
- **Real-time SSE progress across all stages** – Granular events: `downloading`, `transcribing`, `directing`, `render.progress`, `clip_found`, `render.complete`.
- **Aspect-ratio variants from one shot list** – Same `shot_list` can be re-rendered at 9:16, 1:1, 4:5, 16:9 via the render endpoint.

## Worth stealing
1. **`api/routers/process.py:_run_pipeline()`** – Clean linear orchestration with percentage-range mapping for each stage (2% download, 5–30% transcribe, 30–60% direct, 60–100% render) and unified progress callback (`_progress`) that fans out to both SSE broadcaster and job manager.
2. **`api/routers/render.py:start_render()`** – Decoupled render endpoint that can be called independently after directing; accepts `caption_style`, `aspect_ratio`, `remove_fillers`, `normalize_audio` as query params.
3. **`app/src/lib/sse.ts`** – Typed SSE event union (`SSEEvent`) with discriminated `type` field; retry logic (3×, 2 s delay) and clean `close()` pattern.
4. **`api/models/clip.py:ClipCreate`** – Pydantic model enforcing `start_time < end_time`, `virality_score` 0–100, and optional `shot_list` dict per clip.
5. **Frontend upload flow (`app/src/app/page.tsx`)** – Three-stage wizard (drop → style reference → camera labeling) with parallel uploads via `Promise.all` and immediate director trigger.
6. **Licence** – Not stated in any provided file.
