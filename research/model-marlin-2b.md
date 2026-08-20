# model-marlin-2b

Source: https://huggingface.co/NemoStation/Marlin-2B

Read for `shorts-user-feedback.md` (UPDATE section): what does this offer becky?
Written by a free model reading the source; the adopt/skip judgement is not its call.

---

## What it is
Marlin-2B is a video-language model (VLM) built on Qwen3.5-2B, tagged for video-captioning and temporal-grounding. It accepts video + text and emits text. The README is inaccessible (HTTP 401), so the exact problem formulation, input format, and output schema are **not stated in the source**.

## The method, in order
**not stated in the source** — the README.md could not be fetched. The repository contains `modeling_marlin.py` (custom architecture code) and a processor/preprocessor, but no pipeline stages are documented in the available metadata.

## Numbers the source reports
| Metric | Value | Benchmark / Dataset |
|---|---|---|
| — | not stated in the source | — |

## Prompts, losses or decision rules
```
not stated in the source
```

## Hardware and runtime cost
- **Parameters**: ~2B (base Qwen3.5-2B) — **not stated in the source** whether adapters or extra vision parameters increase this.
- **VRAM**: **not stated in the source**. A 2B LLM in 4-bit quantization fits in ~2–3 GB; the vision encoder + projector + KV cache will add overhead. No official quantization, offload, or latency numbers are provided.
- **Throughput / latency**: **not stated in the source**.
- **Co-residency with becky’s single 8GB vision model**: Unknown. If Marlin-2B runs quantized (4-bit) it *might* fit alongside another small model, but no memory profile is published. Treat as **not verified**.

## What becky does NOT do that this does
- **Video-captioning / temporal-grounding as a single VLM forward pass** — becky currently uses a separate VLM (Gemma-4B/12B) for vision-language understanding; Marlin-2B is positioned for the same role but at 2B scale.
- **Native temporal grounding** (tags list “temporal-grounding”) — becky does moment ranking via separate signals; Marlin may unify this.
- **Custom architecture (`modeling_marlin.py`)** — implies a non-standard vision encoder / projector / LLM fusion not used in becky today.

## Worth adopting
1. **No — insufficient evidence to adopt.**  
   - The README is unavailable, so we cannot verify: input frame sampling strategy, max video length, temporal grounding output format, quantization support, or inference speed on 8GB GPU.  
   - Without those, we cannot determine if it would *replace* the Gemma-4B/12B VLM pass or *add* a new capability (e.g., unified temporal grounding).  
   - License is Apache-2.0 (per hub metadata), so legally permissive if technical fit is proven later.

**Action**: Fetch the README (or the arxiv papers: 2501.00513, 2407.00634, 2512.14698) and benchmark a 4-bit Marlin-2B on a representative 9:16 clip before any pipeline integration decision.

---

## ORCHESTRATOR NOTE (2026-08-19) - the repo is GATED, not broken

The free model could not read the card because the repo returns 401. Re-checked with
Jordan's own authenticated Hugging Face session and it is not a transient error:

    Access to model NemoStation/Marlin-2B is restricted and you are not in the
    authorized list. Visit https://huggingface.co/NemoStation/Marlin-2B to ask
    for access.

So there is nothing to evaluate and no way to evaluate it. Nobody can read the card,
the config, or the weights until someone with Jordan's account clicks "request access"
on that page and the author approves.

**This is the one item in this research batch that needs Jordan, and it is one click.**
Everything technical about it stays unknown until then - including whether a 2B video
model with native temporal grounding would be a better fit for the 8GB budget than
Gemma-4 12B, which is the actual reason it is interesting.
