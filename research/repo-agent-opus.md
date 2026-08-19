# atherion005-byte/agent-opus

Source: https://github.com/atherion005-byte/agent-opus | licence: MIT

Read for `shorts-user-feedback.md`: what step does this run that becky does not?
Written by a free model reading the source; the build/skip judgement is not its call.

---

## What it is
Agent Opus takes a video file or a YouTube/TikTok/Twitter URL, downloads it via yt-dlp, transcribes audio with Faster-Whisper (GPU-accelerated), scores candidate segments for virality using either a local Ollama LLM (Llama 3, Mistral, etc.) or a built-in heuristic, detects and smoothly tracks faces with YOLOv8 + scipy interpolation, renders animated word-level captions in one of three styles (karaoke, bold_box, minimal), optionally applies dynamic zoom on "power moments", and encodes the final clips with MoviePy/FFmpeg at CRF 20. It emits up to 12 ranked MP4 clips plus a ZIP archive and a virality report.

## The pipeline, in order
1. **Download** — `clipping_tool/clipper.py:AgentOpusClipper.download_video()` uses yt-dlp with format selector `bestvideo[height<=1080][ext=mp4]+bestaudio[ext=m4a]/best[height<=1080][ext=mp4]/best`.
2. **Transcribe** — `clipping_tool/clipper.py:AgentOpusClipper.transcribe()` calls Faster-Whisper with `beam_size=5`, `word_timestamps=True`, `vad_filter=True`, `vad_parameters={"min_silence_duration_ms": 400}`. Writes `transcript.json` to `OUTPUT_DIR`.
3. **Highlight detection / virality scoring** — `clipping_tool/clipper.py:AgentOpusClipper.analyze_highlights()`:
   - If Ollama available and model ≠ "none": sends transcript prefix to LLM with a structured prompt (see Prompts section), parses JSON array of `{start, end, title, reason, hook}`, validates duration bounds, then re-scores each candidate with `_virality_details()`.
   - Fallback heuristic: slides a window (default `min(max_dur, max(min_dur, total_dur * 0.4))`) in steps of `max(5.0, window * 0.2)`, scores each with `_virality_details()`, picks non-overlapping top-N.
4. **Face tracking** — `clipping_tool/clipper.py:AgentOpusClipper._compute_face_track()` samples frames at 5 fps, runs YOLOv8 (`classes=[0]` for person), picks largest box, records center-x, smooths with `scipy.ndimage.uniform_filter1d(size=max(1,10))`, returns `interp1d` linear interpolator.
5. **Caption frame generation** — `clipping_tool/clipper.py:_render_caption()` (spaces/app.py) or `AgentOpusClipper._render_caption_*` methods (clipper.py) create per-word PIL frames: yellow highlight for current word, white for others, semi-transparent dark background bar.
6. **Dynamic zoom (optional)** — `clipping_tool/clipper.py:AgentOpusClipper._render_clip()` applies a zoom transform on detected "hook moment" (first power word or high-confidence word in first 25 words) when `enable_zoom=True`.
7. **Composite & encode** — `clipping_tool/clipper.py:_render_clip()` builds `CompositeVideoClip([cropped] + caption_clips)`, writes with `libx264`, `crf=20`, `preset=fast`, 30 fps, 4 threads.
8. **Package** — `clipping_tool/app.py:run_pipeline()` zips all clip MP4s into `agent_opus_clips.zip`, returns report, thumbnails, ZIP, and top clip preview.

## Models, libraries and services

| Component | Stage | Local / Paid API |
|-----------|-------|------------------|
| Faster-Whisper (SYSTRAN) | Transcription | Local (CUDA or CPU) |
| YOLOv8n (Ultralytics) | Face/person detection | Local (auto-downloads `yolov8n.pt`) |
| Ollama (Llama 3, Llama 3.1, Mistral, Gemma2, Phi3) | Virality highlight selection | Local (optional; heuristic fallback if not running) |
| MoviePy | Video compositing & encoding | Local |
| yt-dlp | Video download | Local |
| Gradio | Web UI | Local |
| scipy (interpolate, ndimage) | Face-track smoothing | Local |
| PIL / Pillow | Caption frame rendering | Local |
| CrewAI + LangChain-Ollama | Generative studio (separate pipeline) | Local LLM via Ollama |

## Prompts
**LLM highlight selection prompt** (from `clipping_tool/clipper.py:analyze_highlights()`):
```
You are a viral content expert. Find the {max_clips} best segments for YouTube Shorts/TikTok.
Rules: {min_dur}–{max_dur} seconds each, strong hook, complete thought.
Return ONLY valid JSON array. Each object: "start"(float),"end"(float),"title"(≤6 words),"reason"(why viral),"hook"(opening line).

Transcript:
{ts_text[:6000]}
```
**Generative Studio research/script/storyboard prompts** (from `generative_studio/studio.py`):
- Researcher: `"Gather deep insights and facts about: {prompt}"` → expected output: "A list of 5 key facts with sources."
- Scriptwriter: `"Write a 60-second script based on the research."` → expected output: "A script with dialogue and scene descriptions."
- Storyboarder: `"Create detailed AI image prompts for 5 scenes from the script."` → expected output: "A JSON-like list of 5 image prompts."

## How it decides WHAT to clip
**File:** `clipping_tool/clipper.py` — `AgentOpusClipper._virality_details()` (heuristic) and `analyze_highlights()` (LLM + heuristic validation).

**Heuristic scoring (0–99):**
- Base 50.
- Speech pace: `wps = words_in_window / duration`; `score += min(int((wps - 1.5) * 6), 12)`.
- Power-word hits: `_POWER_WORDS = _HOOK_WORDS ∪ _EMOTION_WORDS ∪ _VALUE_WORDS` (≈70 words). Each hit adds 4, capped at +20.
- Question starter in first 10 words (`why, how, what, when, who, where, can, could, should, would`): +7.
- Punctuation: `?` +6, `!` +4, any digit `\b\d+\b` +4.
- Whisper confidence: mean `probability` > 0.92 → +6; > 0.85 → +3.
- Personal pronouns (`i, you, we, my, your, our, me, us`): +4.
- Window length: sliding window = `min(max_dur, max(min_dur, total_dur * 0.4))`, step = `max(5.0, window * 0.2)`.
- Non-overlap filter: greedy highest-score-first, skip if overlaps any already-selected segment.

**LLM path:** Sends first 80 segments (≈6000 chars) to Ollama with the prompt above, expects JSON array, then re-scores each returned segment with `_virality_details()` and sorts by that score.

## How it decides framing / cropping
**File:** `clipping_tool/clipper.py:_render_clip()` and `spaces/app.py:_render_clip()`.

- Aspect ratios: `9:16` (target width = `src_h * 9/16`), `1:1` (square = `min(src_w, src_h)`), `16:9` (no crop).
- If aspect ≠ `16:9` and face track available: crop window centered on interpolated face center-x (`cx`), clamped to `[tgt_w/2, src_w - tgt_w/2]`. Vertical center for `1:1` is `src_h//2`; for `9:16` uses full height.
- If no face track or aspect `16:9`: center crop horizontally.
- Output resolution fixed: `9:16 → 1080×1920`, `1:1 → 1080×1080`, `16:9 → 1920×1080`.
- Dynamic zoom (when `enable_zoom=True`): at detected hook moment (first power word or high-confidence word in first 25 words), applies a zoom-in transform (implementation in `clipper.py` `_render_clip` zoom logic, not fully visible in truncated file but referenced in UI and README).

## Multi-pass or iteration
- **No multi-pass refinement** on the clipping pipeline. The heuristic scorer runs a single sliding-window pass; the LLM path makes one call and validates once. Face tracking is a single forward pass over frames at 5 fps. Caption frames are pre-rendered once per word. No re-scoring, no iterative cropping adjustment, no second-pass encoding.

## Steps here that a transcript-first clipper would MISS
- **Face-tracking crop with temporal smoothing** — YOLOv8 per-frame detection at 5 fps → `uniform_filter1d(size=10)` → linear interpolation (`scipy.interpolate.interp1d`) keeps speaker centered during crop to 9:16/1:1.
- **Per-word animated caption frames pre-rendered as ImageClips** — each word gets its own PIL-rendered frame with yellow highlight, composited at exact word timestamp (`w["start"]` relative to clip start).
- **Dynamic zoom on hook moment** — detects first power word / question starter / high-confidence word in first 25 words, triggers zoom effect at that timestamp.
- **Virality scoring fused from acoustic + lexical signals** — combines Whisper confidence, speech rate (wps), power-word density, question hooks, punctuation, pronouns — not just semantic LLM judgment.
- **Platform presets with duration bounds** — TikTok/Shorts/Reels/LinkedIn presets auto-set aspect, min/max duration, clip count (`clipping_tool/clipper.py:PLATFORM_PRESETS`).
- **Batch URL processing** — UI accepts newline-separated URLs, splits `max_clips` across them (`clipping_tool/app.py:run_pipeline`).
- **GPU-accelerated Whisper with VAD** — `faster_whisper` on CUDA `float16`, `vad_filter=True`, `min_silence_duration_ms=400` reduces hallucination.

## Worth stealing
1. **`_virality_details()` heuristic** (`clipping_tool/clipper.py`) — compact, dependency-free scoring function combining pace, lexicon, confidence, pronouns; returns both score and matched keywords for explainability. MIT licensed.
2. **Face-track smoothing pipeline** — `YOLOv8 @ 5 fps → uniform_filter1d → interp1d` (`_compute_face_track()`) — reusable for any speaker-centric reframing.
3. **Per-word caption frame generator** (`_render_caption()` in `spaces/app.py` or `_render_caption_*` in `clipper.py`) — clean PIL → numpy → `ImageClip` pattern with configurable highlight color.
4. **Platform preset table** (`PLATFORM_PRESETS` dict) — single source of truth for aspect/duration/clip-count per platform.
5. **LLM prompt template** — structured, constrained JSON output with explicit schema in prompt; fallback to heuristic on parse failure.
6. **Transcript persistence** — writes `transcript.json` with word-level timestamps for downstream reuse (search, translation, etc.).
7. **License** — MIT (per README and LICENSE file reference).
