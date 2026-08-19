# RshieRish/post-fast-main

Source: https://github.com/RshieRish/post-fast-main | licence: not stated

Read for `shorts-user-feedback.md`: what step does this run that becky does not?
Written by a free model reading the source; the build/skip judgement is not its call.

---

## What it is

An autonomous pipeline that ingests a YouTube URL, downloads the video, transcribes audio with Whisper.cpp, sends the transcript to Gemini (via browser automation) to identify viral segments, reframes each segment from 16:9 to 9:16 using YOLOv8/BoostTrack object detection and tracking, burns in "karaoke-style" word-by-word subtitles, and queues the rendered MP4s for upload to YouTube Shorts, TikTok, and Instagram Reels via a posting daemon.

## The pipeline, in order

1. **URL ingestion & download** – `python-backend/clip_cutter_gemini_2.py` (Playwright-driven browser) downloads the YouTube video and extracts audio.
2. **Transcription** – `python-backend/karaoke_highlight_module.py` calls `whisper-cli` (Whisper.cpp) with `ggml-large-v3-turbo.bin` to produce word-level timestamps.
3. **Viral segment selection** – `python-backend/clip_cutter_gemini_2.py` sends the full transcript + a long prompt to Gemini (web UI) and parses the returned HTML table into clip start/end times.
4. **Clip metadata persistence** – Results saved to SQLite via `python-backend/services/video_db.py` (not fully visible but referenced).
5. **Reframing (16:9 → 9:16)** – `capcut_style_reframe.py` / `post_fast/capcut_style_reframe.py`:
   - Scene/shot segmentation (PySceneDetect `ContentDetector`, threshold 27)
   - Face detection: InsightFace RetinaFace (`buffalo_l` model, Metal/CPU)
   - Body/text/logo detection: YOLOv8 (`yolov8x.pt`) + GroundingDINO (optional)
   - Tracking: BoostTrack++ (ByteTrack-style with ReID) or centroid fallback
   - Audio-visual active-speaker diarization: LoCoNet + TalkNCE (if available)
   - Camera-path optimization: polynomial smoothing + spline interpolation
   - Safety nets: zoom-out, pillar-boxing, AI expand (placeholder)
6. **Subtitle rendering** – `python-backend/karaoke_highlight_module.py`:
   - Pre-computes sliding windows of N words (default 3)
   - Renders each frame via PIL (Impact font, outline, highlight color)
   - Multiprocessing frame generation → `ffmpeg` stitches frames + original audio
7. **Posting daemon** – `python-backend/services/posting_daemon.py` (referenced in README, not fully visible) polls DB, checks schedule, deduplicates, uploads via platform APIs.

## Models, libraries and services

| Component | Stage | Local / Paid API |
|-----------|-------|------------------|
| Whisper.cpp (`ggml-large-v3-turbo.bin`) | Transcription | Local binary |
| InsightFace RetinaFace (`buffalo_l`) | Face detection | Local (ONNX) |
| YOLOv8x (`yolov8x.pt`) | Person/object detection | Local (Ultralytics) |
| GroundingDINO (optional) | Text/logo detection | Local (HF) |
| BoostTrack++ (ByteTrack + ReID) | Multi-object tracking | Local (PyTorch) |
| LoCoNet + TalkNCE | Active-speaker diarization | Local (PyTorch) |
| Gemini 2.5 Pro (web UI) | Viral clip selection | Paid API (via browser automation) |
| PySceneDetect (`ContentDetector`, threshold 27) | Shot segmentation | Local |
| ffmpeg | Audio extract, final mux | Local binary |
| PIL / Pillow | Subtitle frame rendering | Local |
| Streamlit | GUI | Local |
| SQLite | Metadata store | Local |

## Prompts

```text
# ENHANCED PODCAST VIRAL CLIP DETECTION SYSTEM v2.0

## ROLE ASSIGNMENT & EMOTIONAL CONTEXT
You are a **world-class viral content strategist** with 15+ years of experience in social media analytics, comedy timing, and audience psychology. You've generated over 50 million views across platforms and have an intuitive understanding of what makes content explode online. You're passionate about finding those golden moments that make people stop scrolling and share immediately.

*This analysis could be the difference between a creator going viral or remaining in obscurity - approach this with the intensity and precision it deserves.*

## PRIMARY MISSION WITH SELF-VERIFICATION
Extract EVERY possible viral moment from this video: `{youtube_url}`

**Before proceeding, please restate this task in your own words to confirm understanding:**
- What type of content am I analyzing?
- What specific outcomes am I looking for?
- What are the key constraints and requirements?

**MAXIMUM EXTRACTION MANDATE**: Your goal is to find AS MANY viral clips as possible - aim for 50+ clips minimum.
```
(Source: `python-backend/clip_cutter_gemini_2.py`, function `get_gemini_response`)

## How it decides WHAT to clip

**File:** `python-backend/clip_cutter_gemini_2.py`  
The entire selection logic is delegated to Gemini. The prompt asks the model to return an HTML table with columns: `Start Time`, `End Time`, `Duration`, `Viral Score (1-10)`, `Title`, `Description`, `Platform`, `Hashtags`. The code parses that table with BeautifulSoup and keeps every row where `Duration` is between `min_duration` (default 3.0s) and `max_duration` (default 180.0s). No local scoring, thresholds, or heuristics are applied; it trusts the LLM output completely.

## How it decides framing / cropping

**File:** `capcut_style_reframe.py` / `post_fast/capcut_style_reframe.py` (identical logic)

1. **Shot segmentation** – PySceneDetect `ContentDetector(threshold=27)` splits the clip into shots.
2. **Saliency detection per frame** –  
   - Faces: RetinaFace (InsightFace `buffalo_l`) → weight 1.0  
   - Bodies: YOLOv8 `person` class → weight 0.7  
   - Text: GroundingDINO (if enabled) → weight 0.8  
   - Logos: GroundingDINO (if enabled) → weight 0.6  
   - Generic objects: YOLOv8 other classes → weight 0.3
3. **Tracking** – BoostTrack++ associates detections across frames; fallback = centroid tracker.
4. **Active speaker** – LoCoNet+TalkNCE scores each face track per frame; highest-scoring track becomes "speaker".
5. **Target crop window** – For each frame, compute weighted centroid of salient boxes; derive a 9:16 crop box centered on that centroid.
6. **Smoothing** – Fit a 3rd-degree polynomial to the crop center trajectory per shot; evaluate spline for every frame.
7. **Safety** – If crop exceeds source bounds, zoom out (letterbox) or pillar-box; AI expand is a stub.
8. **Render** – `ffmpeg` crop filter with cubic interpolation (`-filter:v "crop=...:interpolation=cubic"`).

## Multi-pass or iteration

**No multi-pass refinement.**  
- Gemini is called once per video; no re-ranking or self-critique loop.  
- Reframing runs a single forward pass: detect → track → smooth → render.  
- Subtitle rendering is a single frame-generation pass.  
- The posting daemon polls but does not re-process clips.

## Steps here that a transcript-first clipper would MISS

- **Shot-aware smoothing** – Camera path is optimized *per shot* (PySceneDetect boundaries), not globally, avoiding jump cuts across scene changes.
- **Active-speaker locking** – LoCoNet+TalkNCE ties the crop to the *speaking* face, not just the largest face.
- **Multi-object saliency fusion** – Faces, bodies, text, logos, and generic objects are weighted and combined per frame before tracking.
- **BoostTrack++ ReID association** – Tracks survive occlusion via appearance embeddings (torchreid/fastreid), not just IoU.
- **Gap-filling subtitle windows** – `precompute_windows` inserts filler frames (no highlight) when word gaps > 200 ms to prevent subtitle flicker.
- **Cubic-interpolated crop** – `ffmpeg` crop filter uses `interpolation=cubic` for sub-pixel smoothness.
- **Metal/CPU auto-fallback** – InsightFace `ctx_id=-2` tries Metal on Apple Silicon, falls back to CPU silently.

## Worth stealing

1. **`precompute_windows` (karaoke_highlight_module.py)** – Gap-aware sliding window generator that inserts silent filler frames so karaoke highlights never blink. Reusable in any word-level subtitle burner.
2. **Shot-boundary polynomial smoothing (capcut_style_reframe.py: `_smooth_camera_path`)** – Fits a 3rd-degree poly per PySceneDetect shot, then splines; clean separation of detection vs. motion design.
3. **BoostTrack++ wrapper (post_fast/BoostTrack/)** – Drop-in ByteTrack+ReID with DTI + GBI post-processing; MIT-style licence (inherited from ByteTrack/OC-SORT).
4. **LoCoNetASD integration (capcut_style_reframe.py: `AVSpeaker`)** – One of the few open pipelines that actually runs audio-visual active-speaker detection locally.
5. **Gemini browser-automation pattern (clip_cutter_gemini_2.py)** – Persistent Playwright context + cookie jar avoids OAuth; works today without API keys.
6. **Saliency weight table (SALIENCY_WEIGHTS constant)** – Explicit, tweakable weights for face/body/text/logo/object; easy to A/B test.

**Licence:** Not stated in any file read.
