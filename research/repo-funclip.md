# modelscope/FunClip

Source: https://github.com/modelscope/FunClip | licence: MIT

Read for `shorts-user-feedback.md`: what step does this run that becky does not?
Written by a free model reading the source; the build/skip judgement is not its call.

---

## What it is
FunClip is a locally deployed video clipping tool that takes video or audio files, runs speech recognition with timestamp prediction (and optional speaker diarization), and lets users select text segments or speaker IDs to extract corresponding video/audio clips. It emits clipped media files plus full and segment-level SRT subtitles, with optional LLM-assisted highlight selection.

## The pipeline, in order
1. **Model initialization** – `create_asr_model()` in `funclip/launch.py` loads the chosen ASR backbone (Paraformer, Fun-ASR-Nano, or SenseVoice) plus VAD, punctuation, and speaker models via `funasr.AutoModel`.
2. **Audio extraction & recognition** – `VideoClipper.video_recog()` / `recog()` in `funclip/videoclipper.py` extracts 16 kHz mono audio, runs `funasr_model.generate()` with `sentence_timestamp=True` and optional `return_spk_res=True`, then normalises results via `_normalize_recognition_result()`.
3. **SRT generation** – `generate_srt()` in `funclip/utils/subtitle_utils.py` converts sentence-level timestamps to SRT format.
4. **User selection** – Gradio UI (in `launch.py`) presents recognition text and SRT; user copies desired text or enters speaker IDs (`spk0`, `spk1`, …).
5. **Text-to-timestamp matching (manual mode)** – `proc()` in `funclip/utils/trans_utils.py` does case-insensitive substring search on normalised recognition text to find character indices, then maps to timestamps.
6. **Speaker-based timestamp lookup (speaker mode)** – `proc_spk()` in `funclip/utils/trans_utils.py` filters `sd_sentences` by speaker ID and returns their timestamp spans.
7. **Clipping** – `VideoClipper.clip()` / `video_clip()` in `funclip/videoclipper.py` slices the audio array or `moviepy.VideoFileClip` at the collected timestamps, applies per-segment start/end offsets (`start_ost`, `end_ost`), concatenates segments, and optionally burns subtitles via `moviepy.TextClip`.
8. **LLM inference (optional)** – `llm_inference()` in `funclip/launch.py` routes to one of several provider modules (`qwen_api`, `openai_api`, `litellm_api`, `g4f_openai_api`, `twelvelabs_api`) with system + user prompts + full SRT.
9. **AI Clip (optional)** – `AI_clip()` / `AI_clip_subti()` in `funclip/launch.py` parses LLM output with `extract_timestamps()` (expects `N. [HH:MM:SS,mmm-HH:MM:SS,mmm] text` format) and reuses the clipping logic with those timestamps.

## Models, libraries and services

| Component | Stage | Local / Paid API |
|-----------|-------|------------------|
| `funasr.AutoModel` (Paraformer-Large / SeACo-Paraformer) | ASR, timestamp prediction | Local (ModelScope/HF hub) |
| `funasr.AutoModel` (Fun-ASR-Nano-2512) | Multilingual ASR | Local (HF hub) |
| `funasr.AutoModel` (SenseVoiceSmall) | Multilingual ASR + emotion + audio events | Local (ModelScope hub) |
| FSMN VAD (`damo/speech_fsmn_vad_zh-cn-16k-common-pytorch`) | Voice activity detection | Local |
| Punctuation (`damo/punc_ct-transformer_zh-cn-common-vocab272727-pytorch`) | Punctuation prediction | Local |
| CAM++ / campplus (`damo/speech_campplus_sv_zh-cn_16k-common`) | Speaker embedding / diarization | Local |
| Gradio 4 (`gradio`, `starlette<1.0`) | Web UI | Local |
| MoviePy (`moviepy`, `imageio-ffmpeg`) | Video/audio slicing, subtitle burning | Local |
| Librosa | Audio resampling to 16 kHz | Local |
| OpenAI Python SDK (`openai`) | GPT / Moonshot / DeepSeek / AtlasCloud / MiniMax calls | Paid API (per provider) |
| DashScope SDK (`dashscope`) | Qwen series calls | Paid API (Alibaba Bailian) |
| LiteLLM (`litellm>=1.83.0`) | Unified interface for 100+ LLM APIs | Local wrapper, calls paid APIs |
| G4F (`g4f`) | Free GPT proxy (unstable) | Local, scrapes free endpoints |
| TwelveLabs SDK (`twelvelabs`) | Pegasus video understanding | Paid API (free tier available) |

## Prompts
**`funclip/llm/demo_prompt.py`** (verbatim):
```
你是一个视频srt字幕剪辑工具，输入视频的srt字幕之后根据如下要求剪辑对应的片段并输出每个段落的开始与结束时间，
剪辑出以下片段中最有意义的、尽可能连续的部分，按如下格式输出：1. [开始时间-结束时间] 文本，
原始srt字幕如下：
0
00:00:00,50 --> 00:00:02,10
读万卷书行万里路，
...
```

**`funclip/llm/twelvelabs_api.py`** – `PEGASUS_SYSTEM_PROMPT` (verbatim):
```
You are a video clipping assistant. Watch the video and select up to four
of the most engaging, self-contained highlight segments based on what
actually happens on screen and what is said. Merge consecutive moments
into a single segment. Reply strictly in this format, one segment per line:
1. [start_time-end_time] description
where start_time and end_time are seconds from the start of the video
(e.g. 12.5), the connector is '-', and description is a short summary.
```

## How it decides WHAT to clip
- **Manual text selection** – `proc()` in `funclip/utils/trans_utils.py`: normalises both recognition text and user-supplied text (removes punctuation, spaces Chinese characters, lower-cases ASCII), then uses `str.find()` to locate the substring. The character index is mapped to the timestamp array (one entry per token) to get start/end ms. Multiple `#`-separated queries are supported; each can carry an optional `[offset_b,offset_e]` in ms.
- **Speaker selection** – `proc_spk()` in same file: iterates `sd_sentences` (from `return_spk_res=True`), keeps sentences where `str(d['spk']) == requested_id` and duration > 999 ms, returns their `[start_ms, end_ms]`.
- **LLM-assisted** – `extract_timestamps()` in `funclip/utils/trans_utils.py` regex-parses `N. [HH:MM:SS,mmm-HH:MM:SS,mmm] description` lines from the LLM response into `[[start_ms, end_ms], …]`. The LLM is prompted to output exactly that format (see prompts above).

## How it decides framing / cropping
**No spatial reframing/cropping occurs.** Clipping is purely temporal: `moviepy.VideoFileClip.subclip(start_sec, end_sec)` (or `concatenate_videoclips` for multiple segments). Subtitles are rendered as bottom-centered `TextClip` overlays (font `STHeitiMedium.ttc`, configurable size/colour) via `moviepy.video.tools.subtitles.SubtitlesClip`. The video resolution and aspect ratio are preserved from the source.

## Multi-pass or iteration
**No multi-pass or iterative refinement.** Each stage runs once:
- ASR → single `generate()` call.
- Text matching → single `str.find()` pass per query.
- LLM inference → single request/response.
- Clipping → single slice/concat pass.
There is no re-ranking, self-correction, or validation loop.

## Steps here that a transcript-first clipper would MISS
- **Speaker diarization as a first-class clipping target** – `CAM++` embeddings let users clip “everything speaker spk1 said” without text search.
- **Hotword injection at ASR time** – `hotword` parameter passed to `funasr_model.generate()` biases recognition for domain terms.
- **Character-level timestamps from Paraformer** – enables sub-second alignment for precise clip boundaries (Nano/SenseVoice fall back to sentence-level).
- **Per-segment offset knobs** – `start_ost` / `end_ost` (in 10 ms units) let users pad/trim each matched segment independently.
- **TwelveLabs Pegasus integration** – reasons over raw video (visuals + audio), not just transcript, returning timestamps in the same format the pipeline already consumes.
- **SRT-aware clipping** – `generate_srt_clip()` rebuilds subtitles that exactly match the clipped timeline, with correct indices and time offsets.
- **Provider-agnostic LLM router** – `llm_inference()` dispatches to 5+ backends (OpenAI-compatible, DashScope, LiteLLM, G4F, TwelveLabs) with a shared prompt template.

## Worth stealing
1. **`proc()` / `proc_spk()` text-to-timestamp mapping** (`funclip/utils/trans_utils.py`) – clean, dependency-free substring alignment that works on token-level timestamps.
2. **`PEGASUS_SYSTEM_PROMPT`** (`funclip/llm/twelvelabs_api.py`) – concise instruction that makes a video-understanding model output the exact timestamp format the downstream clipper expects.
3. **`extract_timestamps()`** (`funclip/utils/trans_utils.py`) – robust parser for `N. [HH:MM:SS,mmm-HH:MM:SS,mmm]` lines, handles variable ms precision.
4. **Speaker-diarization clipping path** (`proc_spk` + `video_clip` `dest_spk` arg) – minimal code to turn speaker IDs into clip boundaries.
5. **Offset syntax `[b,e]` embedded in user text** (`videoclipper.py` lines 180-195, 260-275) – lets power users nudge boundaries without UI clutter.
6. **Unified LLM dispatch** (`llm_inference` in `launch.py`) – single function, prefix-based routing, consistent message format across 5 providers.
7. **SRT regeneration for clips** (`generate_srt_clip` in `subtitle_utils.py`) – produces valid, re-indexed subtitles that match the clipped video timeline.
8. **Licence** – MIT (stated in every source file header).
