# Gemma 4 QAT — should becky upgrade its AVLM? (E4B vs 12B, and what QAT actually buys)

**Status: v1, 2026-06-23.** Question from Jordan: Google shipped Gemma 4 with Quantization-Aware
Training (QAT); upgrade becky's `validate`/`avlm` model before the Shotcut build? Evaluate
`gemma-4-E4B-it-qat-q4_0` vs `gemma-4-12B-it-qat-q4_0`, the trade-offs, and whether the "bigger
model at the same cost" idea generalizes to other models. **Bottom line: yes, move to a QAT build —
and the 12B QAT now *fits* the 8 GB RTX 3070 (~7 GB), so trial it as the new AVLM with E4B-QAT as the
fast fallback. But correct one premise: QAT buys near-bf16 quality at 4-bit MEMORY; it does NOT make a
12B as cheap to *compute* as a 4B (the 12B is still slower per token). The thing that genuinely gives
"more model at less compute" is MoE — and Gemma 4 ships one (26B-A4B) too, but it won't fit 8 GB.**

## What QAT actually is (and the one premise to correct)

- **Post-training quantization (PTQ)** — the normal path — trains in bf16, then rounds weights to
  4-bit afterward. Cheap, but accuracy drops because the model never "knew" it'd be quantized.
- **QAT** simulates the 4-bit rounding *during* the last phase of training, so the weights settle into
  values that survive quantization. Google's claim: **"preserves similar quality to bfloat16 while
  dramatically reducing the memory to load the model,"** and **"QAT results yield even higher overall
  quality compared to standard PTQ baselines."**
- **What it buys:** ~4× smaller weights (bf16→4-bit) at ~bf16 quality, and faster token *generation*
  (decoding is memory-bandwidth-bound, so smaller weights = more tok/s).
- **What it does NOT buy:** lower *compute*. FLOPs/latency still scale with parameter count. A 12B-QAT
  has the same matmul cost as a 12B-bf16 — it's just smaller in memory and bandwidth-lighter. So a 12B
  QAT is still meaningfully **slower per token than E4B**, even though both are 4-bit. The "4B→12B at
  the same compute cost" framing is the part to adjust: it's the same *VRAM budget*, not the same
  *compute*.

## The two repos Jordan linked are bf16, not the thing to run

Both `…-qat-q4_0-unquantized` repos are **half-precision (bf16) weights extracted from the QAT
pipeline, for custom downstream compilation** — ~16 GB for the 12B. You don't run these directly.
For local inference, take the **GGUF QAT** build. And a sharp finding from Unsloth: **a naïve `q4_0`
conversion throws away most of QAT's benefit** — their "dynamic" `UD-Q4_K_XL` recovers it:

| | naïve Q4_0 | Unsloth UD-Q4_K_XL |
|---|---|---|
| Gemma 4 E2B top-1 acc | 89.29% | **98.16%** (KLD 0.051 → 0.0017, ~29×) |
| Gemma 4 26B-A4B top-1 | 70.20% | **85.63%** (+15.4 pts, file 200 MB *smaller*) |

**Practical rule: pull the Unsloth GGUF (`UD-Q4_K_XL`), not a hand-rolled `q4_0`.** Unsloth also notes
precisions *above* `UD-Q4_K_XL` degrade rather than help for these QAT models.

## E4B vs 12B — the numbers

| | **E4B-it QAT** | **12B-it QAT** |
|---|---|---|
| Total / effective params | 8B total, ~4.5B effective (Per-Layer Embeddings) | 11.95B (unified arch, 48 layers) |
| Footprint (Unsloth, RAM+VRAM) | **~5 GB** | **~7 GB** |
| Context | 128K | 256K |
| Modalities | Text + Image + **Audio** | Text + Image + **Audio** |
| MMLU-Pro | 69.4% | **77.2%** |
| GPQA Diamond | 58.6% | **78.8%** |
| LiveCodeBench v6 | 52.0% | **72.0%** |
| Audio (CoVoST) | — | 38.5% |
| MMMU-Pro (vision) | — | 69.1% |

The 12B is **not a marginal bump — it's a tier up** on exactly the axes becky's forensic AVLM leans on:
reasoning (GPQA +20 pts), multi-step "what is happening / who is doing what" inference, and stronger
audio+vision. For a who/what/where corroboration engine, that gap is worth real money.

## Does the 12B fit the 8 GB RTX 3070?

**Borderline-yes, and that's the news.** Before QAT, a 12B at Q4_K_M was ~7–8 GB of weights alone —
no room. QAT's ~7 GB total is what makes it plausible to fully offload (`-ngl 99`) on the 3070. The
risk is the **KV cache + the BF16 mmproj + Windows display overhead** on top:

- weights ~7 GB + mmproj ~0.3–0.5 GB + KV cache at becky's ~16k context (12B, 48 layers ≈ 1–2 GB) +
  ~0.5 GB display ⇒ **realistically 8.5–9.5 GB → over the 8 GB line at full context.**
- Mitigations that keep it on-GPU: trim the AVLM context window (becky samples ~30 frames, not 256K),
  use a quantized KV cache, or accept a couple of CPU-offloaded layers (slower but functional —
  degrade-never-crash already covers this).

**E4B-QAT (~5 GB) is the no-drama fit** with headroom for KV + mmproj. So the honest split is: E4B-QAT
is the safe upgrade; 12B-QAT is the clearly-better-if-it-fits upgrade that needs a local VRAM/tok-s
measurement at becky's real frame count.

## Recommendation for becky

1. **Move the AVLM (`validate`/`avlm`, `internal/avlm/server.go`) to a QAT GGUF regardless of size** —
   it's near-bf16 quality at the same-or-smaller footprint as today's Q4_K_M, a free win.
2. **Trial `gemma-4-12B-it-qat` (Unsloth `UD-Q4_K_XL`) as the new default AVLM.** It's a genuine tier
   up for forensic reasoning + audio, and QAT is what finally lets a 12B fit the 3070. Keep the
   existing flags discipline: `llama-server` (not `llama-mtmd-cli`, which crashes on Gemma-4),
   `-fa off`, BF16 mmproj, `enable_thinking=false`.
3. **Keep `gemma-4-E4B-it-qat` wired as the fast fallback** (the existing AVLM swap mechanism already
   supports per-model settings). If the 12B's tok/s or VRAM at becky's context is unworkable on the
   3070, E4B-QAT is still an upgrade over the current non-QAT E4B.
4. **Local gate before adoption (Jordan + the GPU — cloud can't measure this):** on a real clip,
   compare 12B-QAT vs E4B-QAT for (a) peak VRAM at the real frame count, (b) tok/s, (c) whether the
   who/what/where description is actually *better*, not just bigger. This is the same "research the
   class, then let the human verify" rule that governs every becky model pick.
5. **One thing the 12B will NOT fix:** the measured frame-sampling / averaging limits (sub-second
   touches falling between ~1 fps samples; many-frames-in-one-request scene averaging). README already
   records "a bigger model does NOT fix this" — that's `motion`'s job. The 12B improves *reasoning and
   audio quality*, not temporal resolution. Don't expect it to close the sub-second gap.

## Is "bigger model at the same cost" unique to Gemma? (the alternatives Jordan asked about)

QAT itself is now industry-standard-ish, and there are *two distinct* levers people conflate:

**A. Quality-preserving low-bit (memory lever — same family as Gemma QAT):**
- **OpenAI gpt-oss** ships **native MXFP4** weights (4-bit trained-in, similar spirit to QAT).
- **NVIDIA NVFP4 / Nemotron "QAD" (Quantization-Aware *Distillation*, 2026)** — recovers 4-bit
  accuracy via distillation; the current research frontier for sub-4-bit.
- **Microsoft BitNet b1.58** — ternary (~1.58-bit) weights trained from scratch; extreme memory wins,
  but must be pretrained that way (not a drop-in for an existing model).
- **Qwen / Llama** mostly publish **AWQ/GPTQ** official quants — those are PTQ, *not* QAT, so they
  carry the usual small accuracy tax Gemma's QAT avoids.
- Takeaway: Gemma 4's official, per-size, llama.cpp-ready QAT lineup is **unusually clean and
  complete** for a local-first shop. It's a real reason to prefer Gemma here over rolling your own
  GPTQ of another model.

**B. More capability per FLOP (the actual "bigger model, less compute" lever = sparsity/MoE):**
- This is what Jordan is intuiting, and it's **not QAT — it's Mixture-of-Experts.** Gemma 4 ships
  **26B-A4B**: 26B total parameters but **only ~4B active per token**, so it *computes* like a 4B while
  *reasoning* closer to its 26B class. That's the genuine "12B-ish quality at 4B compute" trick.
- The catch for becky: A4B still needs **all** experts resident — **~15 GB**, so it **won't fit the
  8 GB 3070** without heavy CPU offload (slow). Worth a line in the watch-list, not a build target
  until Jordan's VRAM grows.
- E4B itself already uses a sparsity-ish trick: **Per-Layer Embeddings** make it ~4.5B *effective* out
  of 8B total, the embeddings offloadable — which is why its footprint behaves like a 4B.

**Net:** the memory lever (QAT) and the compute lever (MoE) are separate, and Gemma 4 gives becky both.
For the 3070, **dense 12B-QAT** (fit-permitting) or **E4B-QAT** is the play *now*; **26B-A4B** is the
"if Jordan upgrades the GPU" upgrade path.

## Confidence
High on the QAT mechanism, the repos being bf16-for-compilation, the use-Unsloth-GGUF finding, the
benchmark gap, and the footprint figures (all from Google's release + Unsloth + the live HF cards).
Medium on "12B fits 8 GB at becky's real context" — that's a local measurement, not a spec number, and
is the one gate Jordan must close on the 3070 before committing.

## Sources
- `hf.co/google/gemma-4-E4B-it-qat-q4_0-unquantized`, `…/gemma-4-12B-it-qat-q4_0-unquantized`,
  `…/gemma-4-31B-it-qat-q4_0-unquantized`
- Google blog: *Gemma 4 with quantization-aware training*
  (`blog.google/innovation-and-ai/technology/developers-tools/quantization-aware-training-gemma-4/`)
- Unsloth docs: *Gemma 4 QAT* (`unsloth.ai/docs/models/gemma-4/qat`) — footprints + UD-Q4_K_XL vs naïve
- `ai.google.dev/gemma/docs/core`; Lushbinary / n1n.ai self-hosting write-ups
- QAT-vs-PTQ + MoE/MXFP4/NVFP4/BitNet landscape: NVIDIA NVFP4-QAD report (arXiv 2601.20088),
  PT²-LLM, SignRoundV2, BitNet b1.58 papers
- becky internal: `README.md` (Gemma-4 AVLM two-stage, "bigger model doesn't fix sub-second"),
  `SPEC-BECKY-VISION-MODELS.md` (E4B kept for audio; 8 GB 3070; `-fa off`, BF16 mmproj),
  `internal/avlm/server.go`
