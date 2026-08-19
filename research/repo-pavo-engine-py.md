# sonnhfit/pavo-engine-py

Source: https://github.com/sonnhfit/pavo-engine-py | licence: not stated

Read for `shorts-user-feedback.md`: what step does this run that becky does not?
Written by a free model reading the source; the build/skip judgement is not its call.

---

## What it is
Pavo Engine is a timeline-driven video renderer that takes a JSON timeline specification (describing tracks, strips, assets, transitions, and output settings) and produces an MP4 file using FFmpeg. It also ships three standalone perception modules—scene detection, object detection, and speech transcription—that are not wired into the main render pipeline in the provided code.

## The pipeline, in order
1. **Load & validate timeline JSON** – `pavo.sequancer.render.read_json_video` reads the file; schema validation is mentioned in README but the validator (`pavo.schema.validate_timeline_json`) is not in the provided files.
2. **Parse strips into internal `Strip` objects** – `pavo.sequancer.render.get_strips_from_json` converts each track’s strips into `Strip` dataclasses (type, timing, effect, transition, trim, etc.).
3. **Auto-compute `n_frames` if missing** – `pavo.sequancer.render._auto_n_frames` walks all strips and takes the maximum `start + length`.
4. **Build `Sequence`** – `pavo.sequancer.render.init_sequence` instantiates `Sequence` (from `pavo.sequancer.seq`, not provided) with strips, dimensions, fps, background.
5. **Render sequence** – `Sequence.render_sequence(workers=…)` (implementation not in provided files) drives FFmpeg to produce the output MP4.
6. **Optional audio ducking** – If `output.audio_ducking: true`, the renderer (code not shown) lowers soundtrack volume during speech segments; requires `openai-whisper`.

Perception modules (scene, object, speech) are importable but **not called** from the render pipeline in any provided file.

## Models, libraries and services

| Component | Stage / Use | Local / Paid API |
|-----------|-------------|------------------|
| FFmpeg (via `ffmpeg-python`) | Final render / video splitting / audio ducking | Local binary required |
| PySceneDetect (`scenedetect[opencv]`) | `SceneDetector.detect()` / `detect_and_split()` | Local |
| Ultralytics YOLO (`yolo26n.pt`, `yolo11s.pt`, etc.) | `ObjectDetector.detect_image()` / `detect_video()` | Local (model weights downloaded on first use) |
| OpenAI Whisper (`openai-whisper`) | `SpeechTranscriber.transcribe()` / audio ducking | Local (model weights downloaded) |
| PyAnnote / Hugging Face (`pyannote.audio`) | Speaker diarization examples (`SpeakerDiarization`) | Local models; premium pipeline needs pyannoteAI API key (paid) |
| `boto3` / `botocore` | S3 asset fetch in `json_render_with_s3_asset` (imported in `main.py`, impl not shown) | AWS service (paid) |
| Pydantic v2 | Timeline schema validation (referenced, validator code not provided) | Local |

## Prompts
No LLM prompts found in the provided source files. The README mentions “natural language prompts” but the codebase only contains a Python DSL (`PavoVideo`) and JSON schema—no prompt templates or LLM calls.

## How it decides WHAT to clip
**It does not decide.** The timeline JSON explicitly lists every strip with `start`, `length`, `video_start_frame`, and optional `trim_start/end` (seconds or frames). Selection is entirely author-driven; no scoring, thresholding, or LLM-based clip selection exists in the provided code.

## How it decides framing / cropping
**It does not reframe.**  
- Image/video strips accept an optional `fit` field (passed to `Strip` but rendering logic not visible) and `filters` array.  
- Subtitle positioning has heuristic defaults in `PavoVideo.addSubtitle` (landscape → bottom center ~8% margin; portrait → y≈55%), but this is DSL-side authoring assistance, not automatic reframing during render.  
- No face/body detection, auto-crop, or safe-zone enforcement runs at render time.

## Multi-pass or iteration
**No multi-pass logic is visible.**  
- Render is a single forward pass: parse JSON → build `Sequence` → `render_sequence()`.  
- Perception modules (`SceneDetector`, `ObjectDetector`, `SpeechTranscriber`) each run once per call; no re-checking or refinement loops.  
- Audio ducking (if enabled) appears to be a single pass over the soundtrack aligned to Whisper segments—no iterative gain adjustment.

## Steps here that a transcript-first clipper would MISS
- **Explicit timeline authoring** – Every cut, overlay, transition, and effect is hand-authored in JSON/DSL; no “find highlights from transcript” step exists.  
- **Frame-accurate trim** – `trim_start_frame` / `trim_end_frame` let you slice source video at exact frame boundaries without re-encoding the source.  
- **Track-based compositing** – Strips live on numbered tracks with z-order; transitions (`fade`, `slide`, `wipe`, `dissolve`) are declared per strip.  
- **Deterministic duration math** – All times are frame counts (`n_frames`, `start`, `length`) derived from `output.fps`; no floating-point drift.  
- **Audio ducking tied to Whisper segments** – If enabled, soundtrack gain drops by `ducking_reduction_db` dB exactly during detected speech spans.  
- **S3 asset ingestion** – `json_render_with_s3_asset` (signature only) suggests remote asset fetching before render.

## Worth stealing
1. **`pavo/sequancer/render.py:get_strips_from_json`** – Clean mapping from a validated JSON schema to typed `Strip` objects with trim-frame→second conversion.  
2. **`_auto_n_frames` fallback** – Derives timeline length from strip extents when `n_frames` is omitted.  
3. **`PavoVideo.addSubtitle` auto-position logic** (in README) – Landscape vs. portrait heuristic with percentage margins; portable to any caption system.  
4. **`trim_start_frame` / `trim_end_frame` spec** – Frame-accurate sub-clipping without floating-point seconds.  
5. **Audio ducking config** – Simple `audio_ducking: true` + `ducking_reduction_db` in output schema; implementation would be a few lines of FFmpeg `volume` filter keyed to Whisper segments.  
6. **MIT License** (per README badge & `setup.py` classifier).
