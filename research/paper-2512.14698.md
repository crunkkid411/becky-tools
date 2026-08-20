# paper-2512.14698

Source: https://arxiv.org/abs/2512.14698

Read for `shorts-user-feedback.md` (UPDATE section): what does this offer becky?
Written by a free model reading the source; the adopt/skip judgement is not its call.

---

## What it is
TimeLens is a systematic investigation into building multimodal large language models (MLLMs) for video temporal grounding (VTG): given a video and a text query, localize the described event as a temporal segment (start, end). The work addresses two dimensions: data quality and algorithmic design. It produces TimeLens-Bench (manually re-annotated Charades-STA, ActivityNet Captions, QVHighlights), TimeLens-100K (automatically re-annotated training corpus), and TimeLens models (7B and 8B) that achieve state-of-the-art open-source VTG performance and surpass GPT-5 and Gemini-2.5-Flash on the refined benchmarks.

## The method, in order
1. **Benchmark diagnosis & manual refinement** – Define strict criteria (query clarity/specificity, event existence, query uniqueness, no information leakage, annotation precision, exhaustiveness). Annotators diagnose-then-refine each video-query pair from Charades-STA, ActivityNet Captions, QVHighlights with cross-validation; yield **TimeLens-Bench** (Charades-TimeLens, ActivityNet-TimeLens, QVHighlights-TimeLens).  
2. **Automated training-data re-annotation** – Sample videos from existing VTG corpora (CosMo-Cap, InternVid-VTime, DiDeMo, QuerYD, HiREST, etc.), use **Gemini-2.5-Pro** with a prompt (Fig. 10) to identify distinct events, generate queries and timestamps, and verify quality; yield **TimeLens-100K** (~20K videos, ~100K annotations).  
3. **Timestamp encoding selection** – Compare position-embedding (MRoPE), visual overlay, non-interleaved textual prefix, and **interleaved textual prefix** (raw timestamps like “10.2s” tokenized and prepended to each frame’s tokens). Interleaved textual prefix with raw timestamps wins (Table 2).  
4. **Training paradigm: thinking-free RLVR** – Use GRPO with reward `r(y) = IoU(Ŝ, S*)` on the answer only (no thinking tokens, no format reward). Outperforms SFT and thinking-based RLVR in both performance and efficiency (Table 3).  
5. **RLVR recipes** – (a) **Early stopping** when temporal IoU reward and within-group reward std plateau (~310 steps / ~2.5K examples for Qwen2.5-VL). (b) **Difficulty-based sampling**: offline inference on training set, compute difficulty `d_i = 1 - IoU(Ŝ_i, S_i*)`, Gaussian sampling with `μ=0.05, σ=0.2`, weights `w_i = g(d_i;μ,σ²)/p̂(d_i)`.  
6. **Model training** – **TimeLens-7B**: Qwen2.5-VL-7B base → thinking-free RLVR on TimeLens-100K. **TimeLens-8B**: Qwen3-VL-8B base → small SFT to “revert” to base state → thinking-free RLVR on TimeLens-100K.

## Numbers the source reports
| Metric | Value | Benchmark / Condition |
|---|---|---|
| TimeLens-7B mIoU | 48.8 / 46.2 / 56.0 | Charades-TimeLens / ActivityNet-TimeLens / QVHighlights-TimeLens (Table 1) |
| TimeLens-8B mIoU | 55.2 / 53.2 / 65.5 | Charades-TimeLens / ActivityNet-TimeLens / QVHighlights-TimeLens (Table 1) |
| TimeLens-7B vs Qwen2.5-VL-7B ΔmIoU | +9.5 / +14.8 / +24.4 | Same three benchmarks (Table 1) |
| TimeLens-8B vs Qwen3-VL-8B ΔmIoU | +6.9 / +6.4 / +6.1 | Same three benchmarks (Table 1) |
| Interleaved textual prefix (raw ts) mIoU | 48.3 / 43.1 / 56.7 | Charades / ActivityNet / QVHighlights (Table 2) |
| Thinking-free RLVR mIoU | 48.3 / 43.1 / 56.7 | Same three benchmarks (Table 3) |
| SFT (100K) mIoU | 48.6 / 39.7 / 49.0 | Same three benchmarks (Table 3) |
| Thinking-based RLVR mIoU | 42.7 / 41.2 / 57.8 | Same three benchmarks (Table 3) |
| Training time (1.0×) | ≈ 4h10m | 8×H20 GPUs (Table 3 caption) |
| RLVR steps (Qwen2.5-VL) | ~310 steps (~2.5K examples) | Until reward plateaus (Appendix C.2) |
| Frame sampling (final models) | 2 FPS | min_tokens=64, total_tokens=14336 (Appendix C.2) |
| Max resolution (110s video) | ~320×320 | Final model config (Appendix C.2) |
| TimeLens-100K size | ~20K videos, ~100K annotations | Appendix H |
| TimeLens-Bench total | 4,279 videos, 9,404 annotations | Table 4 |
| Charades-STA error rate | 20.6% query uniqueness, 34.9% annotation accuracy | Section 3.3 |
| VUE-TR IoU (TimeLens-7B) | 45.1 | Table 10 |
| Video-MME All (TimeLens-7B) | 65.7 | Table 11 |

## Prompts, losses or decision rules
```
# GRPO loss (Eq. 1)
ℒ_GRPO = -𝔼_{(v,q)~𝒟} 𝔼_{y^(i)~π_θ} [A^(i) log π_θ(y^(i)|v,q)]
A^(i) = r(y^(i)) - (1/G) Σ_j r(y^(j))          (Eq. 2)

# Thinking-based RLVR response & reward (Eqs. 3-4)
y = [y_thinking, y_answer]
r(y) = r_acc(y_answer) + r_format(y)
r_acc(y_answer) = IoU(Ŝ, S*)

# Thinking-free RLVR response & reward (Eqs. 5-6)
y = y_answer
r(y) = r_acc(y) = IoU(Ŝ, S*)

# Difficulty estimate (Eq. 7)
d_i = 1 - IoU(Ŝ_i, S_i*)

# Gaussian sampling weight (Eqs. 8-9)
g(d;μ,σ²) = (1/√(2πσ²)) exp(-(d-μ)²/(2σ²))
w_i = g(d_i;μ,σ²) / p̂(d_i)   with μ=0.05, σ=0.2

# Early stopping rule (Section 5.3, Fig. 6)
Stop when temporal IoU reward and within-group reward std plateau.
```

## Hardware and runtime cost
- **Training**: 8×H20 GPUs; 1.0× ≈ 4h10m (Table 3 caption). RLVR on Qwen2.5-VL takes ~310 steps (~2.5K examples).  
- **Model sizes**: 3B, 7B, 8B, 235B-A22B (Table 6).  
- **Inference config (final models)**: 2 FPS sampling, min_tokens=64, total_tokens=14336 → ~320×320 max resolution for 110s video.  
- **8GB RTX 3070 feasibility**: Not stated in the source. The paper only reports training on 8×H20 and evaluation via API (GPT, Gemini) or same high-res config on unspecified hardware. A 7B/8B MLLM at this token budget almost certainly exceeds 8GB VRAM for inference, let alone training. No quantization or offloading strategies are discussed.

## What becky does NOT do that this does
- **Video temporal grounding**: Localize a natural-language query to a precise (start, end) segment in a long video.  
- **Rigorous benchmark curation**: Manual diagnose-then-refine pipeline with strict criteria (uniqueness, existence, clarity, accuracy, no leakage) and cross-validation.  
- **Automated high-quality training-set creation**: LLM-based (Gemini-2.5-Pro) re-annotation of diverse VTG corpora into TimeLens-100K with verification step.  
- **Interleaved textual timestamp encoding**: Raw timestamps tokenized and inserted before each frame’s visual tokens, bypassing MRoPE.  
- **Thinking-free RLVR for VTG**: Pure GRPO with IoU reward on the answer only, no chain-of-thought, no format reward.  
- **Early stopping on reward plateau**: Explicit stopping criterion for single-task RLVR.  
- **Difficulty-aware Gaussian sampling**: Offline difficulty estimation via current model’s IoU, then importance-weighted sampling toward target difficulty mean.  
- **Unified evaluation suite**: TimeLens-Bench spanning three domains with consistent metrics (R1@0.3/0.5/0.7, mIoU) and external validation on VUE-TR.

## Worth adopting
- **Interleaved textual timestamp encoding (raw timestamps)** – Simple, no RoPE surgery, best empirical performance (Table 2). Could replace any current timestamp handling in becky’s VLM pass if becky ever adds per-frame time conditioning.  
- **Thinking-free RLVR paradigm** – If becky ever fine-tunes a VLM for a grounding-like task with verifiable reward (IoU, detection AP, etc.), this paradigm is simpler and more efficient than SFT or thinking-based RL.  
- **Early stopping on reward plateau** – Practical recipe for any single-task RL fine-tuning; saves compute and avoids degradation.  
- **Difficulty-based Gaussian sampling** – Drop-in replacement for uniform sampling in any RL fine-tuning loop where offline difficulty estimation is cheap.  
- **TimeLens-Bench as VTG evaluation standard** – If becky adds a VTG capability, this is the only benchmark the source shows to be reliable (legacy benchmarks misrank models).  
- **TimeLens-100K as VTG training data** – If becky trains a VTG model, this dataset is independently constructed and validated (Table 5).  
- **License**: CC BY 4.0 (stated at top of source).  

**No** – If becky’s scope remains ASR, face tracking, active-speaker detection, shot detection, silence/energy signals, moment ranking, reframing, and burned-in captions, none of the above VTG-specific components (task, data, encoding, RL paradigm) are directly applicable. The only potentially transferable items are the general RL recipes (early stopping, difficulty sampling), but becky currently does no RL fine-tuning.
