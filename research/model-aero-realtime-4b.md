# model-aero-realtime-4b

Source: https://huggingface.co/kcz358/aero-realtime-4B

Read for `shorts-user-feedback.md` (UPDATE section): what does this offer becky?
Written by a free model reading the source; the adopt/skip judgement is not its call.

---

## What it is
Aero Realtime is a 4B-parameter native proactive audio-video language model that processes audio (80 ms slots), video (1 fps frames), and text in a single aligned autoregressive stream, jointly predicting the next lexical token or a learned silence token at each step. Input is timestamp-ordered audio/video chunks; output is a per-interval stream of lexical and silence predictions.

## The method, in order
1. **Audio encoder** (`aero_realtime_audio_encoder`): 32-layer transformer, 1280 hidden size, 20 heads, 128 mel bins, processes ~80 ms audio slots (8 audio tokens per slot per `audio_length_per_tok`).
2. **Vision encoder** (`qwen3_vl_vision`): 24-layer ViT, 1024 hidden size, 16 heads, patch size 16, temporal patch size 2, spatial merge size 2, outputs 2560-d tokens.
3. **Projector** (gelu): projects vision/audio tokens to language model hidden size (2560).
4. **Language model backbone** (`qwen3_vl_text`): 36-layer Qwen3-VL decoder, 2560 hidden size, 32 attention heads, 8 KV heads (GQA), RoPE with mrope_section [24,20,20], max position embeddings 262144.
5. **Unified autoregressive head**: predicts either next lexical token or special tokens (`rt_speak_token_index` 151674 for speech, `rt_pad_token_index` 151673 for silence/pad, `rt_start_token_index` 151672, `rt_end_token_index` 151675) on a single causal clock.
6. **vLLM-Omni deployment** (`aero_realtime.yaml`): serves the model with chunked audio/video input over WebSocket (online) or batched offline demo (`examples/offline/demo.py`).

## Numbers the source reports
| Metric | Value | Benchmark / Dataset |
|--------|-------|---------------------|
| Model parameters | 4B (checkpoint name) | — |
| Audio encoder layers | 32 | — |
| Audio encoder hidden size | 1280 | — |
| Audio encoder attention heads | 20 | — |
| Audio mel bins | 128 | — |
| Audio tokens per slot | 8 (`audio_length_per_tok`) | — |
| Vision encoder layers | 24 | — |
| Vision encoder hidden size | 1024 | — |
| Vision encoder heads | 16 | — |
| Vision patch size | 16 | — |
| Vision temporal patch size | 2 | — |
| Vision spatial merge size | 2 | — |
| LLM layers | 36 | — |
| LLM hidden size | 2560 | — |
| LLM attention heads | 32 | — |
| LLM KV heads | 8 (GQA) | — |
| LLM max position embeddings | 262144 | — |
| Vocab size (text) | 151936 | — |
| Vocab size (audio encoder) | 131072 | — |
| Dtype | bfloat16 | — |
| License | MIT | — |

No benchmark scores (e.g., OVOBench, VideoMME, etc.) are reported in the provided text.

## Prompts, losses or decision rules
```
not stated in the source
```
The README shows only CLI invocation examples; no system prompt, scoring rule, loss formula, or silence/lexical threshold is quoted.

## Hardware and runtime cost
- **Model weight file**: `model.safetensors` (size not stated).
- **Precision**: bfloat16 throughout (config.json).
- **Deployment**: vLLM-Omni with custom YAML (`vllm_omni/deploy/aero_realtime.yaml`).
- **Multi-GPU**: supports `--tensor-parallel-size N`.
- **B200 optimization**: `--mm-encoder-attn-backend TRITON_ATTN` mentioned for B200.
- **Offline demo**: processes a bundled 60-second video at 1 fps video + 80 ms audio chunks; writes per-interval JSONL.
- **VRAM / latency / throughput**: not stated in the source.
- **8 GB RTX 3070 feasibility**: not stated. A 4B bfloat16 model alone needs ~8 GB for weights; plus KV cache, audio/video encoders, and vLLM overhead will exceed 8 GB. The source does not claim single-8GB-GPU operation.

## What becky does NOT do that this does
- Single unified autoregressive stream that **jointly decides when to speak (lexical token) and when to stay silent (learned silence token)** at 80 ms resolution.
- **Native realtime / streaming inference** via WebSocket with chunked audio/video input and per-slot output.
- **End-to-end audio-token generation** (the model emits audio tokens / silence tokens directly, not just text).
- **Aligned audio-video-text clock**: audio slots fused with preceding output token on one causal clock.
- **vLLM-Omni serving stack** for multimodal realtime streaming (online server + client).
- **Training pipeline** (LMMs-Engine two-stage configs) and **eval harness** (LMMs-Eval shard runner) for this architecture.

## Worth adopting
- **No** — nothing here is directly adoptable into becky’s offline, single-8GB-GPU pipeline.
  - The model is designed for **realtime streaming** (WebSocket, 80 ms slots, silence tokens), not offline batch scoring.
  - It **requires vLLM-Omni** and a custom deploy config; no standalone `transformers.generate()` example is given.
  - **VRAM footprint** (4B bfloat16 LLM + 32-layer audio encoder + 24-layer ViT + KV cache) almost certainly exceeds 8 GB; the source never demonstrates single-GPU 8 GB operation.
  - No benchmark numbers, no prompt templates, no silence/lexical thresholds to reuse.
  - License is MIT (permissive), but the engineering mismatch is fundamental.

**Bottom line**: Aero Realtime solves a different problem (low-latency proactive AV conversation) with a different deployment model (streaming server, multi-GPU ready). It does not provide a drop-in component for becky’s offline ranking/reframing/captioning workflow.
