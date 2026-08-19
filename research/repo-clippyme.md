# fralapo/clippyme

Source: https://github.com/fralapo/clippyme | licence: MIT

Read for `shorts-user-feedback.md`: what step does this run that becky does not?
Written by a free model reading the source; the build/skip judgement is not its call.

---

## What it is
ClippyMe is a self-hosted pipeline that accepts a YouTube URL or direct video upload, downloads the source, transcribes it, detects viral moments, reframes to 9:16 with active-speaker tracking, post-processes (Ken Burns zoom, EBU R128 loudness, cover frame), optionally applies compose-on-demand edits (colour grade, smart cut, hook overlay, subtitles, logo), and publishes/schedules to TikTok, Instagram Reels, and YouTube Shorts via Zernio. Output is a set of vertical MP4 clips with `moov` atom first for progressive playback.

## The pipeline, in order
1. **Download** – `yt-dlp` (with Deno-based JS runtime for bot-detection bypass, optional cookies) — invoked from the job runner (not visible in files read; referenced in `README.md` and `docker-compose.yml` env `YTDLP_PLAYER_CLIENTS`).
2. **Audio extraction** – Strip to mono 16 kHz FLAC before transcription (`CLIPPYME_TRANSCRIBE_AUDIO_ONLY=true` in `docker-compose.yml`).
3. **Transcribe** – Provider chosen by `TRANSCRIPTION_PROVIDER` (`deepgram` | `elevenlabs` | `whisper`):
   - Deepgram Nova-3 (REST, `DEEPGRAM_API_KEY`, `DEEPGRAM_MODEL=nova-3`, `DEEPGRAM_LANGUAGE=multi`)
   - ElevenLabs Scribe (REST, `ELEVENLABS_API_KEY`, `ELEVENLABS_MODEL=scribe_v1`, optional `ELEVENLABS_AUDIO_ISOLATION`)
   - Local Faster-Whisper (fallback for both; `ENABLE_WHISPER_DIARIZE=1` at build time adds pyannote diarization)
   — Implementation in `clippyme.domain.job_runner` (not visible in files read; wiring in `src/clippyme/api/app.py` via `make_run_job`).
4. **Viral-moment detection** – Google Gemini (`gemini-3.5-flash` default, `GEMINI_API_KEY`, `GEMINI_MODEL`):
   - 5-axis rubric (HOOK_STRENGTH, EMOTIONAL_PAYOFF, QUOTABILITY, SELF_CONTAINED, DENSITY) with 1–100 `viral_score`.
   - Robust JSON parser tolerates malformed output.
   - **No-AI fallback**: TextTiling (ported from ClipsAI) segments transcript into clips when Gemini key missing or call fails.
   - **Edge snapping**: each `[start, end]` snapped to nearest **word** boundary → extended to surrounding **sentence** (asymmetric, ≤60 s, no overlap, abbreviation-safe) → nudged to nearest **waveform silence trough** via `ffmpeg silencedetect` (only toward quiet, no-op if none near).
   — Schemas in `src/clippyme/schemas.py` (`ViralClip`, `ViralClipsResponse`); parser in `clippyme.domain.gemini_parser` (not visible in files read).
5. **Reframe to 9:16** – Active-speaker tracking:
   - YOLOv8 (Ultralytics) person detection + MediaPipe FaceMesh mouth-aspect-ratio (MAR) variance to pick speaker.
   - Smoothed cameraman with adaptive speed/zoom per scene.
   - Hardening: variable-frame-rate normalization, audio `start_time` compensation (YouTube A/V desync), corrupt-frame resilience.
   - **Comfort mode** (default on, `REFRAME_COMFORT=1`): second decode pass; per-scene stationary crop + fixed zoom (anti-nausea). `REFRAME_STATIC_AUTO=on` locks camera per scene (zero pan, zero mid-shot zoom); single subject moving > `REFRAME_MOTION_WIDE_THRESH` (0.12 frame fraction) demoted to static WIDE.
   - Faceless scenes: optional Sobel saliency (`REFRAME_SALIENT_GENERAL`) or weighted-object centroid via existing YOLO (`REFRAME_OBJECT_WEIGHTS`).
   — Service entry `run_reframe` in `clippyme.domain.reframe_service` (imported in `src/clippyme/api/app.py`); tuning via `docker-compose.yml` env vars.
6. **Post-process per clip** – Ken Burns auto-zoom (1.0→1.05×), EBU R128 normalization to −14 LUFS, automatic cover frame selection. Every render uses shared near-visually-lossless libx264 (CRF 18, `CLIPPYME_X264_CRF`, preset `medium`) so stacked re-encodes don’t compound; final mux is stream-copy + `+faststart`.
   — Encode helpers in `clippyme.domain.encode` (`x264_video_args`, `ffmpeg_timeout`); referenced in `src/clippyme/domain/logo.py` and `src/clippyme/domain/hooks.py`.
7. **Compose-on-demand (download-time editing)** – Tabbed modal applies to one/all/staged clips:
   - Colour grade preset (warm_cinematic / cool_crisp / neutral_punch / vivid_pop)
   - Smart Cut: filler-word + silence removal via auto-editor v3 timeline + audio polish; manual transcript trim; conversational AI trim
   - Hook text overlay (Pillow + emoji, Instagram-Stories-style banner/colours/outline/font; default bannerless white Anton with thin black outline)
   - Subtitles: 6 ASS karaoke presets or classic SRT with live preview
   - Brand logo watermark (PNG, position/scale/opacity/margin)
   — `compose_layers` in `clippyme.domain.compose` (imported in `src/clippyme/api/app.py`); hook rendering in `src/clippyme/domain/hooks.py`; logo in `src/clippyme/domain/logo.py`.
8. **Publish / schedule** – Zernio multi-platform API; SmartScheduler picks Italian-prime-time slots, avoids same-day collisions, spreads one clip/day under daily caps. Residual 429 surfaced verbatim.
   — `publish_clip_flow` in `clippyme.domain.publish_service` (imported in `src/clippyme/api/app.py`).

## Models, libraries and services

| Component | Stage | Local / Paid API |
|---|---|---|
| yt-dlp (+ Deno JS runtime) | Download | Local |
| Deepgram Nova-3 | Transcription (default) | Paid API |
| ElevenLabs Scribe (+ optional Voice Isolator) | Transcription (alt) | Paid API |
| Faster-Whisper (CTranslate2) | Transcription (fallback) | Local |
| pyannote.audio (speaker diarization) | Transcription (opt-in, `ENABLE_WHISPER_DIARIZE=1`) | Local (needs HF token) |
| Google Gemini (`gemini-3.5-flash` + fallbacks) | Viral-moment detection | Paid API |
| TextTiling (ClipsAI port) | Viral fallback (no key / failure) | Local |
| YOLOv8 (Ultralytics) | Person detection (reframe) | Local |
| MediaPipe FaceMesh | MAR speaking detection (reframe) | Local |
| PySceneDetect | Scene boundaries (reframe comfort) | Local |
| ffmpeg | All video/audio processing, silence detect, mux | Local |
| auto-editor (Nim binary) | Smart Cut timeline | Local (auto-updated) |
| Pillow | Hook/logo image rendering | Local |
| EBU R128 (ffmpeg `loudnorm`) | Audio normalization | Local |
| Zernio | Publishing / scheduling | Paid API |

## Prompts
No LLM prompt text is visible in the provided files. The Gemini viral-detection prompt is not included in `src/clippyme/schemas.py` (only the response schema `ViralClip`/`ViralClipsResponse`) nor in any other supplied file.

## How it decides WHAT to clip
**File:** `README.md` (lines 115–130) + `src/clippyme/schemas.py` (validation bounds).

- **Primary**: Gemini returns a list of `ViralClip` objects each with `start`, `end` (float seconds), `viral_score` (1–100), `viral_reason` (≥20 chars), plus platform descriptions/titles/hook text. Duration validated to 10–75 s (wider than 15–60 s target so Smart Cut can rescue near-misses).
- **Fallback (no Gemini key or call fails)**: Lexical **TextTiling** (ported from ClipsAI) topic-segments the transcript into several clips — heuristic, not viral-ranked, but offline and free.
- **Edge cleaning (applies to both paths)**:
  1. Snap to nearest **word** boundary in transcript.
  2. Extend to surrounding **sentence** (start back to sentence onset, end forward to sentence-final word); asymmetric, clamped ≤60 s, no overlap with neighbour, guards against false ends (abbreviations, decimals, acronyms), no-ops on unpunctuated transcripts.
  3. **Waveform pass**: nudge each edge to nearest audio **silence trough** via `ffmpeg silencedetect` (moves only toward quiet; no-op if no silence near edge). Controlled by `CLIPPYME_SILENCE_SNAP=1` (default).

## How it decides framing / cropping
**File:** `README.md` (lines 130–135), `docker-compose.yml` (REFRAME_* env vars), `src/clippyme/domain/logo.py` (geometry helpers).

- **Active-speaker tracking**: YOLOv8 person boxes + MediaPipe FaceMesh MAR variance per face → pick speaker per frame.
- **Smoothed cameraman**: adapts speed and zoom per scene; two-speed EMA by default, optional 1€ filter (`REFRAME_SMOOTHER=euro`).
- **Comfort mode (default ON)**: second decode pass; per-scene **stationary crop** + **fixed zoom** (anti-nausea). `REFRAME_STATIC_AUTO=on` locks camera per scene (zero pan, zero mid-shot zoom). Single subject moving > `REFRAME_MOTION_WIDE_THRESH` (0.12 of frame width) → demoted to static WIDE crop; 2+ faces → WIDE.
- **Lost-subject handling**: hold `REFRAME_LOST_HOLD` frames (default 90), then drift to center at `REFRAME_LOST_DRIFT` (0.05).
- **Faceless (GENERAL) scenes**: default letterbox; opt-in Sobel saliency (`REFRAME_SALIENT_GENERAL`) or weighted-object centroid via existing YOLO (`REFRAME_OBJECT_WEIGHTS`, e.g. `dog:3,car:2`).
- **FrameShift object mode**: class weights `face:1,person:0.8,default:0.5` (override via `REFRAME_FRAMESHIFT_WEIGHTS`).
- **Variable-frame-rate normalization**, audio `start_time` compensation (YouTube A/V desync), corrupt-frame resilience — all no-ops on clean sources.

## Multi-pass or iteration
- **Clip-edge refinement is three-pass**: word snap → sentence extend → waveform silence nudge (each a distinct pass over the transcript/audio).
- **Reframe comfort mode** does a **second full decode pass** to compute per-scene stationary crops and fixed zoom levels.
- **Compose-on-demand** stacks multiple encode passes (reframe → zoom → grade → subtitles → smart-cut → hook → logo) but all share the **same CRF 18 / preset medium** so quality doesn’t compound; final mux is stream-copy.
- **No iterative re-ranking or self-correction loops** for viral selection or reframing — each stage runs once per job.

## Steps here that a transcript-first clipper would MISS
- **Waveform silence snapping** after transcript boundaries: cuts never clip a word’s attack/release because edges are nudged to the nearest `ffmpeg silencedetect` trough (only toward quiet).
- **Active-speaker selection via MAR (mouth aspect ratio variance)** from MediaPipe FaceMesh, not just face presence or audio diarization.
- **Comfort-mode reframing**: per-scene stationary crop + fixed zoom (second decode pass) to eliminate seasickness from continuous tracking.
- **Ken Burns auto-zoom** (1.0→1.05×) applied uniformly to every rendered clip.
- **EBU R128 loudness normalization** to −14 LUFS via `ffmpeg loudnorm` on every output.
- **Smart Cut** using auto-editor v3 timeline (filler-word + silence removal + audio polish) as a compose layer, not a pre-processing step.
- **Compose-on-demand editing** at download time: colour grade, hook overlay, subtitles, logo applied in a single stacked encode with shared CRF 18, so the user can toggle layers per clip without re-rendering the reframe.
- **Job journal** (`data/jobs_journal.json`) persists every status transition; on restart queued jobs are re-enqueued, interrupted ones failed or restored from disk artifacts.
- **SmartScheduler** for publishing: Italian-prime-time slot picking, same-day collision avoidance, one-clip-per-day spread under platform daily caps, verbatim 429 surfacing.

## Worth stealing
1. **Three-stage edge snapping** (word → sentence → waveform silence) — `README.md` lines 118–128; controlled by `CLIPPYME_SILENCE_SNAP`. Guarantees clean cuts even with imperfect ASR punctuation.
2. **Comfort-mode reframing** (per-scene stationary crop + fixed zoom, second decode pass) — `docker-compose.yml` `REFRAME_COMFORT=1`, `REFRAME_STATIC_AUTO`, `REFRAME_MOTION_WIDE_THRESH=0.12`. Research-backed anti-nausea; falls back to single-pass tracker when disabled.
3. **Shared CRF 18 / preset medium across all encode passes** — `CLIPPYME_X264_CRF=18`, `CLIPPYME_X264_PRESET=medium` in `docker-compose.yml`; used in `clippyme.domain.encode.x264_video_args()` and referenced by `logo.py`, `hooks.py`. Prevents generational quality loss from stacked re-encodes.
4. **TextTiling fallback for viral detection** — `README.md` line 122; zero-API, offline, produces multiple clips instead of one long dump.
5. **Compose-on-demand at download time** — `clippyme.domain.compose.compose_layers`; layers (grade, smartcut, hook, subtitles, logo) toggled per clip / copied to all / staged, single stacked encode with stream-copy final mux.
6. **Job journal with crash recovery** — `clippyme.domain.job_journal.JOURNAL_FILENAME`, `make_journal_writer`, `recover_jobs` in `src/clippyme/api/app.py` lifespan. Survives container restarts without losing queue state.
7. **Audio-only FLAC upload for transcription** — `CLIPPYME_TRANSCRIBE_AUDIO_ONLY=true`; strips video to mono 16 kHz FLAC (few MB) before sending to cloud ASR, saving bandwidth/cost.
8. **MIT licence** — `README.md` badge and repo root (not shown but declared).

**Licence**: MIT (per `README.md` badge and repository description).
