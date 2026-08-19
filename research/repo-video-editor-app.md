# aeronesto/video-editor-app

Source: https://github.com/aeronesto/video-editor-app | licence: not stated

Read for `shorts-user-feedback.md`: what step does this run that becky does not?
Written by a free model reading the source; the build/skip judgement is not its call.

---

## What it is
A browser-based video editor that lets users edit video through a transcript (Descript-style). The frontend is a React app with WaveSurfer.js waveform visualization and word-level transcription display. The backend is a FastAPI server running WhisperX for ASR + forced alignment, exposing `/transcribe/`, `/transcribe-file/`, `/upload`, and `/video/{id}` endpoints. It takes uploaded video files (or blob URLs) and emits word-level timestamped transcription JSON plus silence-region trim suggestions.

## The pipeline, in order
1. **Upload** – `POST /upload` (server/app/routes/upload.py) saves file via storage service (local or GCS) and returns `video_id`.
2. **Transcribe (upload path)** – `POST /transcribe/` (server/app/routes/transcribe.py) receives multipart file, saves to temp, runs WhisperX ASR → alignment, returns `TranscriptionResponse` (text, segments[], words[] with start/end/score).
3. **Transcribe (server-file path)** – `POST /transcribe-file/` (same file) takes `file_path` already on server, same WhisperX pipeline.
4. **Frontend transcription call** – `transcribeBlob()` or `transcribeFile()` (src/services/transcribeService.ts) posts to backend, returns `TranscriptionData`.
5. **Silence detection** – `detectSilencesCore()` (src/utils/silenceDetection.ts) scans transcription segments/words for gaps ≥ threshold (default 0.7s), adds leading/trailing silence if first/last word far from boundaries.
6. **Padding adjustment** – `adjustSilencesWithPadding()` (same file) shrinks each silence by 0.2s padding, drops if < 0.1s remains.
7. **Trim history merge** – `mergeTrimItems()` (imported from utils, not shown but used in VideoEditorContext) merges new silence regions into existing trim history.
8. **Playback & seeking** – WaveSurfer.js regions represent trims; clicking transcript word seeks video via `findWordAtTime()` (VideoEditorContext.tsx).

## Models, libraries and services

| What | Stage | Local / Paid API |
|------|-------|------------------|
| WhisperX (ASR + alignment) | Transcription (server/app/routes/transcribe.py) | Local (Python, GPU/CPU via torch) |
| whisperx.load_model (default `large-v2`) | ASR model load | Local |
| whisperx.load_align_model (default `WAV2VEC2_ASR_LARGE_LV60K_960H`) | Forced alignment | Local |
| WaveSurfer.js + regions plugin | Waveform rendering & trim regions | Local (npm) |
| React / React Router | Frontend framework | Local |
| FastAPI + Uvicorn | Backend API | Local |
| Google Cloud Storage (optional) | Video storage (server/app/services/storage.py) | Paid API (if `STORAGE_TYPE=gcs`) |
| Local filesystem storage | Video storage (default) | Local |

## Prompts
No LLM prompts found in the source files. The pipeline uses WhisperX (ASR + alignment) only; no LLM is invoked for clipping, summarization, or titling.

## How it decides WHAT to clip
File: `src/utils/silenceDetection.ts` → `detectSilencesCore()`  
Logic (verbatim from code):
- Iterate `transcription.segments[i].words[j]`.
- For each adjacent word pair (including cross-segment), compute `gap = nextWord.start - currentWord.end`.
- If `gap >= threshold` (default 0.7s from `silenceThreshold` state in VideoEditorContext), emit silence region `{start: currentWord.end, end: nextWord.start}`.
- Also check leading gap: if `firstWord.start >= threshold`, emit `{start: 0, end: firstWord.start}`.
- Also check trailing gap: if `duration - lastWord.end >= threshold`, emit `{start: lastWord.end, end: duration}`.
- Returns `TrimHistoryItem[]` (id, start, end, color, handleStyle, timestamp).
- No ranking, scoring, or LLM selection—pure heuristic on word-timestamp gaps.

## How it decides framing / cropping
It does not reframe or crop. The editor only marks trim regions (silences or user-drawn) on a single horizontal timeline. No vertical reframe, aspect-ratio change, or speaker-tracking crop logic exists in the provided files.

## Multi-pass or iteration
No multi-pass or self-refinement loops. Transcription runs once per upload. Silence detection runs on demand when user clicks “Detect Silences” (threshold adjustable). Trim history is additive; `mergeTrimItems` (referenced but not shown) presumably unions overlapping regions. No re-transcription, no confidence-based re-alignment, no iterative clip selection.

## Steps here that a transcript-first clipper would MISS
- **Word-level forced alignment** via WhisperX (`whisperx.align`) producing per-word `start`/`end`/`score` – most transcript-first tools only have segment-level timestamps.
- **Silence detection anchored to word boundaries** (not VAD on raw audio) – gaps are computed from aligned word timestamps, so they respect actual speech pauses.
- **Leading/trailing silence detection** relative to first/last word – catches intro/outro silence that pure inter-word gap scanning would miss.
- **Padding shrinkage** (`adjustSilencesWithPadding` with 0.2s pad, 0.1s min) – prevents over-trimming tight breaths.
- **WaveSurfer region sync** – detected silences become draggable WaveSurfer regions instantly playable/seeking.
- **Blob URL transcription path** – `transcribeBlob` fetches `blob:` URL to `File` → multipart upload, enabling in-browser recording/editing without server-side file persistence first.
- **Dual transcription endpoints** – `/transcribe/` (upload) and `/transcribe-file/` (server path) share identical WhisperX pipeline, avoiding code duplication.

## Worth stealing
1. **`src/utils/silenceDetection.ts`** – clean, dependency-free silence-from-aligned-words logic with leading/trailing handling and padding adjustment. MIT-style licence not stated in repo, but file is standalone TypeScript.
2. **`server/app/routes/transcribe.py`** – minimal WhisperX wrapper: model caching (`asr_model`, `align_models` dicts), VAD onset override, background temp-file cleanup, Pydantic response models.
3. **`server/app/services/storage.py`** – abstract `StorageService` with `LocalStorageService` and `GCSStorageService` implementations + factory `get_storage_service()`; swappable via `STORAGE_TYPE` env var.
4. **`src/context/VideoEditorContext.tsx`** – centralised state (video, transcription, trim history, WaveSurfer refs) with `detectSilences` callback that chains `detectSilencesCore` → `adjustSilencesWithPadding` → `mergeTrimItems` in one call.
5. **`transcribeBlob` / `transcribeFile`** (src/services/transcribeService.ts) – unified frontend API handling both blob URLs and server paths, forwarding all WhisperX options as form-data or JSON.
