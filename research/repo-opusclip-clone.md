# tdollar15/OpusClip-Clone

Source: https://github.com/tdollar15/OpusClip-Clone | licence: MIT

Read for `shorts-user-feedback.md`: what step does this run that becky does not?
Written by a free model reading the source; the build/skip judgement is not its call.

---

## What it is
A Python pipeline that takes a YouTube URL, downloads the video, transcribes its audio with Whisper, asks GPT-4o to pick a single continuous highlight under one minute, crops that segment to 9:16 vertical by tracking the active speaker's face, and writes the final short to `Final.mp4`.

## The pipeline, in order
1. **Download video** – `Components/YoutubeDownloader.download_youtube_video()` (interactive stream selection, merges adaptive video+audio with ffmpeg).
2. **Extract audio** – `Components/Edit.extractAudio()` writes `audio.wav` via MoviePy.
3. **Transcribe** – `Components/Transcription.transcribeAudio()` runs `faster-whisper` base.en model (CUDA if available) and returns list of `[text, start, end]`.
4. **Build transcript string** – `main.py` concatenates segments into `"start - end: text"` format.
5. **Highlight selection** – `Components/LanguageTasks.GetHighlight()` sends transcript to GPT-4o with a system prompt demanding one continuous clip; returns integer `start, stop` seconds.
6. **Cut highlight** – `Components/Edit.crop_video()` uses MoviePy `subclip(start, stop)` → `Out.mp4`.
7. **Vertical crop with speaker tracking** – `Components/FaceCrop.crop_to_vertical()`:
   - Calls `Components/Speaker.detect_faces_and_speakers()` which runs a Caffe face detector + WebRTC VAD per frame, populates global `Frames` list with the active-speaker bounding box per frame.
   - Then re-reads the video, uses Haar cascade as fallback, centers a 9:16 window on the active speaker's face (smoothed: only moves if center shifts >1 px), writes `croped.mp4` (no audio).
8. **Re-attach audio** – `Components/FaceCrop.combine_videos()` loads `Out.mp4` (with audio) and `croped.mp4` (video only), sets audio from the former onto the latter, writes `Final.mp4`.

## Models, libraries and services

| Component | Stage | Local / Paid API |
|-----------|-------|------------------|
| `pytubefix` + `ffmpeg-python` | YouTube download & merge | Local (ffmpeg binary required) |
| `moviepy` | Audio extract, subclip, final mux | Local |
| `faster-whisper` (base.en) | Transcription | Local (CUDA/CPU) |
| OpenAI GPT-4o (`gpt-4o-2024-05-13`) | Highlight selection | Paid API (requires `OPENAI_API` key) |
| OpenCV DNN (Caffe `res10_300x300_ssd_iter_140000_fp16.caffemodel`) | Face detection | Local (model files in `models/`) |
| `webrtcvad` | Voice activity detection | Local |
| OpenCV Haar cascade (`haarcascade_frontalface_default.xml`) | Fallback face detection | Local (bundled with OpenCV) |
| `pydub` | Audio resampling for VAD | Local (needs ffmpeg) |

## Prompts
```text
Baised on the Transcription user provides with start and end, Highilight the main parts in less then 1 min which can be directly converted into a short. highlight it such that its intresting and also keep the time staps for the clip to start and end. only select a continues Part of the video

Follow this Format and return in valid json 
[{
start: "Start time of the clip",
content: "Highlight Text",
end: "End Time for the highlighted clip"
}]
it should be one continues clip as it will then be cut from the video and uploaded as a tiktok video. so only have one start, end and content

Dont say anything else, just return Proper Json. no explanation etc


IF YOU DONT HAVE ONE start AND end WHICH IS FOR THE LENGTH OF THE ENTIRE HIGHLIGHT, THEN 10 KITTENS WILL DIE, I WILL DO JSON['start'] AND IF IT DOESNT WORK THEN...
```
(From `Components/LanguageTasks.py`, variable `system`. The user message is the transcript string concatenated with the same system prompt again.)

## How it decides WHAT to clip
File: `Components/LanguageTasks.py` → `GetHighlight()`.  
The entire transcript (with timestamps) is sent to GPT-4o once. The model is instructed to return a single JSON object with `start`, `end`, `content`. The code parses `start`/`end` as floats, truncates to `int` seconds. If parsing fails or `start == end`, it prompts the user interactively to retry (`input("Error - Get Highlights again (y/n) -> ")`). No internal scoring, thresholds, or multi-candidate ranking exist.

## How it decides framing / cropping
File: `Components/FaceCrop.py` → `crop_to_vertical()` + `Components/Speaker.py` → `detect_faces_and_speakers()`.  
- First pass (`detect_faces_and_speakers`): runs Caffe SSD face detector (confidence > 0.3) + WebRTC VAD (30 ms frames, aggressiveness 2) on every frame. For each detected face it computes `lip_distance = abs((y + 2*h//3) - y1)` (proxy for mouth openness). The face with max `lip_distance` *on frames where VAD says speech is present* is labeled "Active Speaker"; its `[x, y, x1, y1]` is appended to global `Frames` list (one entry per frame).  
- Second pass (`crop_to_vertical`): re-reads video, runs Haar cascade per frame. If Haar finds faces, it picks the one whose center lies inside the previous `Frames[count]` box; otherwise falls back to `Frames[count]`. The 9:16 window (width = `height * 9/16`) is centered on that face's center X. Window position only updates if the new center differs by >1 px from previous frame (jitter suppression). If crop width mismatches, it nudges `x_start`/`x_end` to keep width constant.

## Multi-pass or iteration
- **Two-pass video read for cropping**: `detect_faces_and_speakers()` writes a debug video `DecOut.mp4` and builds `Frames`; then `crop_to_vertical()` reads the source video again to produce the final vertical video.  
- **Highlight retry loop**: `GetHighlight()` can re-call itself once via user prompt, but no automatic re-try or self-critique.  
- No iterative refinement of transcript, highlight, or crop.

## Steps here that a transcript-first clipper would MISS
- **Speaker-aware vertical crop**: Uses combined visual (lip movement heuristic) + audio (VAD) to pick the active speaker per frame, then centers the 9:16 window on that speaker with temporal smoothing (>1 px dead-zone).  
- **Fallback to Haar cascade + previous-frame box** when Haar finds no faces, avoiding center drift.  
- **Interactive stream selection** for YouTube download (user chooses resolution/bitrate).  
- **Adaptive video+audio merge** via ffmpeg when the selected stream is DASH (non-progressive).  
- **Global mutable state (`Frames`, `Fps`)** shared across modules via `global` variables.

## Worth stealing
1. **`Components/Speaker.py`** – The lip-distance + VAD active-speaker heuristic (≈60 lines) is a compact, dependency-light way to get per-frame speaker boxes without a heavy diarization model.  
2. **`Components/FaceCrop.py`** – The "move crop window only if center shifts >1 px" smoothing logic (lines around `if count == 0 or (x_start - (centerX - half_width)) < 1`).  
3. **`Components/LanguageTasks.py`** – The system prompt that forces a single continuous clip with exact timestamps (despite the kitten threat).  
4. **`Components/YoutubeDownloader.py`** – Interactive stream listing with size/type info and automatic DASH merge via `ffmpeg-python`.  
5. **Licence**: MIT (per `README.md`).
