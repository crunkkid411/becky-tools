# paper-2509.10761

Source: https://arxiv.org/abs/2509.10761

Read for `shorts-user-feedback.md` (UPDATE section): what does this offer becky?
Written by a free model reading the source; the adopt/skip judgement is not its call.

---

## What it is
EditDuet is a multi-agent system that automates video non-linear editing by producing a B-roll timeline over a fixed A-roll (interview/voiceover). It takes a user request, an A-roll video, and a raw video collection as input; two LLM agents (Editor and Critic) iterate over an NLE timeline using editing tools until the Critic calls RENDER, outputting a final edited video.

## The method, in order
1. **NLE Environment** (Sec. 3.1): Encapsulates A-roll 𝒜, video collection 𝒱, and timeline τ. Exposes four observations: A-roll transcription (o_𝒜), video collection summary (o_𝒱), visual search results (o_search), and current timeline τ.
2. **Video collection preprocessing**: Hierarchical segmentation via TW-FINCH clustering (Sarfraz et al. 2021); clusters <1 s discarded. Each cluster annotated with start/duration, description (Llava-NeXt, 16 frames/cluster), shot type (MobileNet V3 on MovieShots), camera motion (LSTM on SSD features, Movie Shot Classification dataset). Collection summarized in a paragraph by Llama3.1-70B-Instruct.
3. **Editor Agent** (Sec. 3.2): Llama3.1-8B-Instruct + structured generation. Functions: `search_collection` (CLIP-ViT-B-32 similarity, top 5, deduplicate if longer clip ≥90% similarity of shorter), `add_to_timeline`, `remove_from_timeline`, `switch_clip_positions`, `move_clip`, `DONE`. Acts until `DONE` given Critic feedback.
4. **Critic Agent** (Sec. 3.3): Llama3.1-8B-Instruct + structured generation. Actions: `give_feedback` (string) or `RENDER`. Receives timeline observation, history, user request.
5. **In-Context Learning of Multi-Agent Communication** (Sec. 3.4): Two-stage self-supervised exploration.
   - Stage 1 (Editor): Editor Explorer → Editor Labeler (generates feedback text) → Editor Scorer (1–5 on alignment/efficiency) → Self-Reflecting Editor (refines if score 4). Keep only score-5 pairs; repeat until 5 demos.
   - Stage 2 (Critic): Critic Explorer (with Editor using Stage 1 demos) → Critic Labeler (generates user request) → Critic Scorer (1–5 on satisfaction). Keep only score-5; repeat until 5 demos.
6. **Automatic NLE Evaluation** (Sec. 3.5): VLM judge 𝒥 (GPT-4o) prompted with keyframe grids (midpoint of each sub-clip + duration) for two timelines; outputs preferred timeline. PreferenceRate = fraction of episodes method M₁ preferred over M₂.
7. **Judge–human correlation** (Sec. 3.5.1): 35 participants, 10 pairs each. Judge–human agreement 80.6%; human–human agreement 78.7%. PABAK 0.61 (judge–human) vs 0.57 (human–human).

## Numbers the source reports

| Metric | Value | Benchmark / Dataset |
|--------|-------|---------------------|
| Failure rate (EditDuet) | 8.2% | 5 EditStock documentaries (1458 min raw → 21.5 min final) |
| Failure rate (Editor Critic) | 19.5% | Same |
| Failure rate (Editor Only) | 23.8% | Same |
| Failure rate (BAGEL) | 14.3% | Same |
| Failure rate (VisProg) | 34.8% | Same |
| Failure rate (T2V) | 0.0% | Same |
| Time coverage (EditDuet) | 89.8% | Same |
| Time coverage (Editor Critic) | 82.7% | Same |
| Time coverage (Editor Only) | 68.5% | Same |
| Time coverage (BAGEL) | 73.5% | Same |
| Time coverage (VisProg) | 44.8% | Same |
| Time coverage (T2V) | 92.6% | Same |
| Repetitions per sequence (EditDuet) | 0.174 | Same (≥80% overlap, n>2 counts as n−1) |
| Repetitions (Editor Critic) | 0.257 | Same |
| Repetitions (Editor Only) | 0.217 | Same |
| Repetitions (BAGEL) | 0.214 | Same |
| Repetitions (VisProg) | 0.783 | Same |
| Repetitions (T2V) | 2.696 | Same |
| Human preference (EditDuet) | N/A (baseline) | User study, 35 people |
| Human preference (BAGEL) | 18.2% | Same |
| Human preference (T2V) | 13.1% | Same |
| Auto preference (EditDuet) | N/A (baseline) | GPT-4o judge |
| Auto preference (BAGEL) | 11.5% | Same |
| Auto preference (T2V) | 22.2% | Same |
| Judge–human agreement | 80.6% | 350 pairwise comparisons |
| Human–human agreement | 78.7% | Same |
| PABAK (judge–human) | 0.61 | Same |
| PABAK (human–human) | 0.57 | Same |
| Professional editor preferred over EditDuet | 75.1% | Automatic preference eval vs EditStock final cuts |
| Exploration time | ~82 minutes | Not stated on what hardware |
| EditDuet inference time | ~2.6 minutes | 8 H100s, single B-roll sequence |
| T2V / VisProg inference | <30 seconds average | Hardware not stated |
| BAGEL inference | ~1.5 minutes | Hardware not stated |
| High-scoring Editor demos length | 17 steps average | — |
| High-scoring Critic demos length | 4 steps average | — |
| B-roll sequence max length | 1.25 minutes | — |
| Max sub-clips per B-roll | 27 | — |
| Search results per query | 5 | — |
| CLIP deduplication threshold | 90% | — |

## Prompts, losses or decision rules
```
Prompts: "We provide prompts for instantiating all agents described in this section in the supplementary material." (Sec. 3)
No prompts, losses, or decision thresholds are quoted in the main text.
Critic Scorer: scores user-request/timeline pair 1–5 "based on how well the timeline satisfies the request." Only score 5 kept.
Editor Scorer: scores observation-action history + feedback 1–5 "based on alignment and efficiency." Score ≤3 discarded; score 4 → Self-Reflecting Editor refinement; only score 5 kept.
Critic actions: give_feedback(string) or RENDER.
Editor DONE: "signals that the feedback received has been satisfied, shifting control back to the Critic."
Time coverage: TC(d, d̂) = min(d, d̂) / max(d, d̂) where d = ground-truth duration, d̂ = system output duration.
Repetition: pair of sub-clips in same output with ≥80% overlap; if clip appears n>2 times, counted as n−1 repetitions.
```

## Hardware and runtime cost
- **Models**: Editor & Critic – Llama3.1-8B-Instruct; Collection summary – Llama3.1-70B-Instruct; Segment captions – Llava-NeXt; Shot type – MobileNet V3; Camera motion – LSTM on SSD; Search – CLIP-ViT-B-32; Judge – GPT-4o (closed-source).
- **VRAM**: not stated in the source.
- **Exploration**: ~82 minutes (hardware not stated).
- **Inference**: EditDuet ~2.6 minutes on **8 H100s** per B-roll sequence; T2V/VisProg <30 s; BAGEL ~1.5 min (hardware for baselines not stated).
- **Feasibility on becky’s 8 GB RTX 3070**: **Cannot run alongside or instead of a single 8 GB-budget local vision model.** The paper’s inference uses 8 H100s (multi-GPU, >80 GB VRAM total) and relies on Llama3.1-70B and GPT-4o, which exceed 8 GB. Even the 8B models would require quantization and offloading; the multi-agent loop, exploration, and VLM judge are not designed for single-GPU 8 GB deployment.

## What becky does NOT do that this does
- Multi-agent iterative refinement loop (Editor ↔ Critic) with natural-language feedback until `RENDER`.
- Self-supervised, test-time exploration to synthesize in-context demonstrations for *both* agents (four auxiliary agents for Editor, three for Critic).
- Hierarchical video segmentation via TW-FINCH clustering with per-cluster metadata (description, shot type, camera motion).
- Shot-type classification (5 classes) via MobileNet V3 on MovieShots.
- Camera-motion classification (8 classes) via LSTM on SSD features.
- Video-collection summarization via Llama3.1-70B-Instruct.
- CLIP-based visual search with 90%-similarity deduplication.
- Structured generation for guaranteed-valid function calling (search, add, remove, swap, move, DONE).
- Automatic NLE evaluation via VLM judge (GPT-4o) using keyframe grids, with demonstrated 80.6% agreement with human preference.
- Explicit optimization for time-constraint satisfaction (coverage 89.8%) and repetition minimization (0.174/sequence).
- Handling shooting ratios ~100× (1458 min raw → 21.5 min final) in the evaluation.

## Worth adopting
- **No – not adoptable in current form.** The system requires 8 H100s for inference, closed-source GPT-4o for evaluation, and Llama3.1-70B for summarization; all exceed becky’s 8 GB single-GPU budget. The CC BY-NC-ND 4.0 license (non-commercial, no derivatives) further blocks integration into a reusable pipeline.
- **Ideas worth re-implementing locally** (would replace/add to becky):
  - *Editor–Critic iterative loop* → replace single-pass moment ranking; adds multi-turn refinement.
  - *Self-supervised exploration for ICL demos* → replace hand-crafted prompts for Gemma-4B; adds automated prompt optimization.
  - *TW-FINCH hierarchical segmentation* → replace/augment shot-cut detection; adds sub-clip granularity.
  - *Shot-type & camera-motion classifiers* → add to reframing logic; replaces heuristic rules.
  - *CLIP search with 90% deduplication* → augment silence/energy signals for B-roll retrieval.
  - *Keyframe-grid VLM judge* (if a small open VLM fits) → replace human QA for moment ranking.
- **Licence**: CC BY-NC-ND 4.0 (stated in source).
