# shreesha345/AI-short-creator

Source: https://github.com/shreesha345/AI-short-creator | licence: MIT

Read for `shorts-user-feedback.md`: what step does this run that becky does not?
Written by a free model reading the source; the build/skip judgement is not its call.

---

## What it is
A Python/TypeScript pipeline that downloads a video (not shown in files), transcribes it (assumes pre-existing `raw_video/subtitles.srt`), uses GPT-3.5-turbo-16k to select 30–58 second "viral" segments, cuts those segments, reframes each to 9:16 by tracking the dominant face with MTCNN, then renders burned-in captions and a waveform via a Remotion composition. Output: vertical MP4s in `output/best_video_*.mp4` and a Remotion build in `caption/public/`.

## The pipeline, in order
1. **video_downloader.py** – invoked by `main.py` via `os.system`; not provided in the file set.
2. **transcript_analysis.py** – reads `raw_video/subtitles.srt`, sends full transcript to OpenAI ChatCompletion (`gpt-3.5-turbo-16k`) with a system + user prompt, writes `main_part.json` containing `combined_response` array of `{start_time, end_time, description, duration}`.
3. **video_cutter.py** – loads `main_part.json`, cuts `raw_video/video.mp4` into `Clips/video_1.mp4`, `video_2.mp4`, … using `moviepy.VideoFileClip.subclip`.
4. **face.py** – for each clip in `Clips/`, runs MTCNN face detection every 7 frames at 0.25× resolution, interpolates/ predicts face centre, smoothly pans/crops to 9:16 (1080×1920), limits to 58 s, writes `output/best_video_N.mp4`.
5. **last_edit.py** – reads `output/best_video_2.mp4` duration via OpenCV, patches `caption/src/Root.tsx` replacing `durationInSeconds: 51` with actual duration.
6. **process.py** – for each `output/best_video_*.mp4`: extracts audio to `caption/public/audio.mp3`, moves video to `caption/public/video.mp4`, runs `stable-ts` CLI to produce `subtitles.srt`, moves SRT to `caption/public/`, runs `npm run build` inside `caption/` (Remotion render).

## Models, libraries and services

| Component | Stage | Local / Paid API |
|-----------|-------|------------------|
| OpenAI GPT-3.5-turbo-16k | transcript_analysis.py (clip selection) | Paid API |
| MTCNN (via `mtcnn` pkg) | face.py (face detection) | Local (TensorFlow/Keras) |
| stable-ts (CLI) | process.py (forced-alignment transcription) | Local (whisper.cpp / faster-whisper) |
| moviepy | video_cutter.py, face.py, process.py (cutting, audio) | Local |
| OpenCV (cv2) | face.py, last_edit.py (frame I/O, video props) | Local |
| Remotion + React | caption/ (caption + waveform render) | Local (Node) |
| parse-srt | caption/src/Subtitles.tsx (SRT parsing) | Local |
| pydub | process.py (audio handling) | Local |

## Prompts
```text
This is a transcript of a video/podcast. Please identify the most viral sections from this part of the video, make sure they are more than 30 seconds in duration, Make sure you provide extremely accurate timestamps, respond only in this format [{"start_time": 0.0, "end_time": 55.26, "description": "main description", "duration": 55.26}, {"start_time": 57.0, "end_time": 107.96, "description": "second main description", "duration": 50.96}, {"start_time": 137.0, "end_time": 187.78, "description": "third main description", "duration": 50.78}], I just want JSON as a Response (nothing else)  
 Here is the Transcription:
{subtitle_content}
```
System message:
```text
You are a ViralGPT helpful assistant. You are a master at reading YouTube transcripts and identifying the most interesting parts and viral content from the podcasts making sure that it is more than 30 second and less then 58 second
```
(Source: `transcript_analysis.py`, function `analyze_transcript`)

## How it decides WHAT to clip
Entirely delegated to the LLM. The prompt asks for “most viral sections … more than 30 seconds … less than 58 second” and demands exact timestamps. No programmatic scoring, thresholding, or re-ranking exists in the codebase. The model’s raw JSON is written to `main_part.json` and used verbatim by `video_cutter.py`. File: `transcript_analysis.py`.

## How it decides framing / cropping
`face.py` implements a custom smooth pan-and-crop:
- Detects faces with MTCNN every 7 frames at 25 % resolution.
- Uses the first detected face (`result[0]`).
- Interpolates box between detection frames.
- Predicts next position with `PREDICTION_WEIGHT = 0.5`.
- Moves crop window toward face centre at `MOVEMENT_SPEED = 0.06` of the offset per frame.
- Applies exponential smoothing (`SMOOTHING_FACTOR = 0.3`).
- If no face for > `QUICK_MOVE_THRESHOLD = 10` frames, snaps instantly to new detection.
- Output resolution fixed to 9:16 (width = height × 9/16).
No speaker diarisation, no multi-face handling, no silence-based cut alignment. File: `face.py`, function `process_video`.

## Multi-pass or iteration
None. Each stage runs exactly once in linear sequence. The LLM is called once with the full transcript; no self-critique, no re-try on parse failure, no refinement of face tracks. `main.py` simply executes the six scripts sequentially.

## Steps here that a transcript-first clipper would MISS
- **Visual re-framing to 9:16** via continuous face tracking with interpolation, prediction, and smoothed camera motion (`face.py`).
- **Hard 58-second cap** applied after cropping (`face.py`: `output_video.subclip(0, min(58, …))`).
- **Audio preservation** from original clip during crop (`face.py` re-attaches original audio subclip).
- **Remotion-based caption burn-in** with per-word uppercase rendering (`caption/src/Word.tsx` shows first 3 words upper-cased) and waveform overlay (configured in `Root.tsx`).
- **stable-ts forced alignment** run *after* clipping to generate fresh SRT for the short clip (`process.py`).
- **Duration patching** of the Remotion composition by reading the actual rendered video length (`last_edit.py`).

## Worth stealing
1. **`face.py` smooth pan logic** – the combination of interpolation + prediction + exponential smoothing + quick-move snap is a compact, dependency-light way to keep a face centred in vertical crop. Constants are exposed at top of file for tuning.
2. **`transcript_analysis.py` prompt structure** – the system + user message split and the explicit JSON schema in the prompt are a clean pattern for “give me timestamps only” tasks.
3. **`last_edit.py` duration injection** – trivial but practical: measure final clip length with OpenCV and patch the Remotion `durationInSeconds` prop before render.
4. **MIT licence** – permissive; all code can be copied/adapted.

(If nothing else is needed, the face-tracking constants and the prompt template are the highest-leverage takeaways.)
