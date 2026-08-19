# modelscope/FunASR

Source: https://github.com/modelscope/FunASR | licence: MIT

Read for `shorts-user-feedback.md`: what step does this run that becky does not?
Written by a free model reading the source; the build/skip judgement is not its call.

---

## What it is
FunASR is an industrial speech recognition toolkit that takes audio files (WAV, PCM, etc.) and emits structured transcripts with timestamps, punctuation, speaker labels, and optional emotion/audio-event tags. It composes ASR, VAD, punctuation, and speaker diarization models into a single `AutoModel` pipeline or OpenAI-compatible API server.

## The pipeline, in order
1. **VAD (Voice Activity Detection)** — `funasr/bin/_server_app.py:_process_vllm()` calls `vad_model.generate()` (fsmn-vad or silero-vad) to split audio into speech segments with `[start_ms, end_ms]`.
2. **Segment extraction** — Audio slices are cut from the original waveform using VAD timestamps (`_process_vllm` lines 280–290; `benchmark_vllm.py:vad_segment()`).
3. **ASR inference** — Each segment is passed to the ASR model:
   - **vLLM path** (Fun-ASR-Nano, GLM-ASR-Nano): `funasr/models/fun_asr_nano/inference_vllm.py:FunASRNanoVLLM.generate()` or `funasr/models/glm_asr/inference_vllm.py:GLMASRVLLMEngine.generate()`.
   - **PyTorch fallback**: `funasr/auto/auto_model_vllm.py:_load_fallback()` → `AutoModel.generate()`.
   - **Streaming**: `funasr/models/paraformer/...` chunked encoder/decoder with cache (see README streaming example).
4. **Punctuation (optional)** — `ct-punc` model applied via `AutoModel` when `punc_model="ct-punc"` (README usage example).
5. **Speaker diarization (optional)** — `funasr/bin/_server_app.py:attach_speaker_labels()` runs CAM++ embeddings on each segment, clusters with `ClusterBackend(merge_thr=0.78)`, merges/smooths, then assigns `SPK{n}` to ASR segments by temporal overlap (`funasr/utils/speaker_utils.py:distribute_spk()`).
6. **Post-processing** — `funasr/utils/postprocess_utils.rich_transcription_postprocess()` removes special tokens (`<|zh|>`, `<|emo|>`, etc.) and applies inverse text normalization (ITN) if enabled.

## Models, libraries and services

| Component | Stage | Local / Paid API | Notes |
|-----------|-------|------------------|-------|
| fsmn-vad | VAD | Local (0.4M params) | Default VAD; also supports Silero VAD via `funasr[silero]` |
| Fun-ASR-Nano-2512 | ASR (flagship) | Local (800M, GPU) | LLM-based (Qwen2.5-0.5B); accelerated via vLLM |
| Fun-ASR-MLT-Nano-2512 | ASR (31 lang) | Local (800M, GPU) | Same architecture, different checkpoint |
| SenseVoiceSmall | ASR + emotion + events | Local (234M, CPU/GPU) | 5 languages; outputs `<|emo|>`, `<|event|>` tags |
| Paraformer-zh | ASR + timestamps | Local (220M) | Non-autoregressive; CIF predictor |
| Paraformer-zh-streaming | Streaming ASR | Local (220M) | Chunked encoder/decoder with cache |
| ct-punc | Punctuation | Local (290M) | CT-Transformer |
| cam++ | Speaker embedding | Local (7.2M) | Used with `ClusterBackend` for diarization |
| emotion2vec+large | Emotion recognition | Local (300M) | Separate model, not in main pipeline |
| vLLM | LLM decoding acceleration | Local | `FunASRNanoVLLM`, `GLMASRVLLMEngine`, `AutoModelVLLM` |
| PyTorch / torchaudio | Core inference | Local | All non-vLLM paths |
| librosa / soundfile | Audio I/O & resample | Local | `prepare_audio_for_inference()` |
| kaldialign | CER computation | Local | Benchmark only (`benchmark_vllm.py`) |
| transformers | LLM/tokenizer loading | Local | Qwen, Vicuna, GLM, etc. |
| fastapi / uvicorn | API server | Local | `funasr-server` OpenAI-compatible endpoint |
| modelscope / huggingface_hub | Model download | Remote (free) | `snapshot_download` in `auto_model_vllm.py` |

## Prompts
```text
Transcribe speech to text.
```
```text
USER: 
INSTRUCTION: Transcribe speech to text.
INPUT: 
```
*Source: `funasr/models/llm_asr/model.py:237` and `247` — the default prompt and its USER/INSTRUCTION/INPUT wrapper fed to the LLM.*

## How it decides WHAT to clip
**It does not clip.** FunASR is an ASR toolkit, not a video clipper. The only segmentation is **VAD-driven**: `fsmn-vad` (or Silero) emits `[start_ms, end_ms]` speech segments; each segment is transcribed independently. No content-based selection, ranking, or highlight detection exists in the provided files.

## How it decides framing / cropping
**Not applicable** — audio-only toolkit. No video framing or cropping logic present.

## Multi-pass or iteration
**No iterative refinement of ASR output.** The pipeline is single-pass per segment:
- VAD runs once → segments fixed.
- ASR runs once per segment (vLLM or PyTorch).
- Speaker diarization runs once on the same segments after ASR.
- No re-scoring, no hypothesis re-ranking, no second-pass decoding visible in the code.
- *Benchmark script* (`benchmark_vllm.py`) runs PyTorch **and** vLLM for comparison, but that is offline evaluation, not production multi-pass.

## Steps here that a transcript-first clipper would MISS
- **VAD-first segmentation** — cuts are made on acoustic silence boundaries (`fsmn-vad` 0.4M model) before any transcript exists, not on punctuation or semantic boundaries.
- **Speaker-aware segment grouping** — CAM++ embeddings + agglomerative clustering (`merge_thr=0.78`) with temporal smoothing (`smooth(mindur=1s)`) and overlap resolution (`funasr/utils/speaker_utils.py:postprocess()`).
- **Streaming chunked inference with cache** — `paraformer-zh-streaming` uses `chunk_size=[0,10,5]` (600 ms) and maintains encoder/decoder cache across chunks (README example).
- **Hotword biasing** — `model.generate(hotword="关键词 20")` passed to ASR decoder (README usage).
- **Emotion / audio-event tags** — SenseVoiceSmall emits `<|HAPPY|>`, `<|laughter|>`, etc., preserved in `sentence_info` (README SenseVoice example).
- **Character-level timestamp alignment** — `rich_transcription_postprocess` + `sentence_info` provides per-character timestamps (README output example).
- **GGUF/llama.cpp edge binary** — single-file CPU inference with built-in VAD (`llama-funasr-sensevoice`), no Python runtime (README Deploy section).

## Worth stealing
1. **`funasr/bin/_server_app.py:create_app()`** — clean composition: lazy-load VAD, ASR (vLLM with AutoModel fallback), speaker diarization; OpenAI-compatible `/v1/audio/transcriptions` with `verbose_json` segments + speaker labels. **MIT licence**.
2. **`funasr/auto/auto_model_vllm.py:AutoModelVLLM`** — generic vLLM wrapper that auto-detects LLM-based ASR models (`FunASRNano`, `LLMASR`, `GLMASR`), extracts LLM weights from `model.pt` (`prepare_vllm_weights()`), loads audio encoder/adaptor in PyTorch, delegates decoding to vLLM. **MIT**.
3. **`funasr/utils/speaker_utils.py:sv_chunk()` + `postprocess()` + `distribute_spk()`** — complete speaker diarization pipeline: fixed 1.5s/0.75s sliding windows → CAM++ embeddings → clustering → overlap resolution → temporal smoothing → segment-level speaker assignment by max overlap. **MIT**.
4. **`funasr/models/llm_asr/adaptor.py:Transformer` adaptor** — downsample-by-k + linear projection + optional Transformer layers (configurable `n_layer`, `attention_heads`) to map audio encoder dim → LLM dim. Reusable for any encoder+LLM fusion. **MIT**.
5. **`benchmark_vllm.py`** — end-to-end benchmark harness: VAD pre-segment → write segment WAVs → run PyTorch & vLLM → concat by file → CER via `kaldialign` → RTFx reporting. **MIT**.
6. **`funasr/utils/postprocess_utils.rich_transcription_postprocess`** (referenced in README) — strips SenseVoice special tokens (`<|zh|>`, `<|emo|>`, `<|event|>`, ITN tags) in one pass. **MIT**.
