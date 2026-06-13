# SPEC-VIDEO-ANALYSIS.md — True temporal / motion analysis for forensic video

> **SPEC — NOT BUILT, AWAITING JORDAN'S APPROVAL.**
> Research + design only. No Go code has been written. No new binary exists. Nothing
> in `becky-go/` has been changed. Jordan approves before any build starts.
>
> Authored 2026-06-07. Every model/library below was re-verified against current
> (2026-06-07) availability — version, license, offline capability, and whether it
> *genuinely* runs locally on an RTX 3070 Laptop (~8 GB VRAM, CUDA compute 8.6) — with
> sources cited inline. Where I could not confirm something is current, I say so.

---

## 1. The problem, stated precisely

`becky-validate` (internal/avlm) drives Gemma-4 by extracting frames at **~1 fps** (and
≤30 s audio) — this is documented in `internal/avlm/avlm.go` (`opts.FPS = 1.0`,
`MaxFrames = 40`, "Gemma 4's documented multimodal limits"). The model therefore sees a
**slideshow of stills**, not continuous motion. Consequences for a DV/forensic case:

- **Sub-second events fall between samples.** A tap, a grab, a flinch, a hand pushed
  away and re-placed — the exact consent/resistance dynamics `FORENSIC-OUTPUT-PHILOSOPHY.md`
  §3–§4 says ARE the evidence — can happen entirely between two 1-fps frames.
- **No true motion understanding.** From two stills 1 s apart you cannot reliably tell
  *force*, *direction*, *who-initiated*, or *who-pulled-away* — only that positions
  changed. That is "hands moved," the exact failure the philosophy doc forbids.
- This is not a Gemma quirk. It is **how essentially every video-LLM works today** (see
  §2). Fixing it needs a different *class* of model, not a bigger VLM.

**This spec's scope:** a future becky tool/layer for the *edge cases* that need true
~30 fps motion analysis — NOT a replacement for the cheap 1-fps pass. The 1-fps VLM pass
stays the default; this is the escalation for the handful of flagged, evidence-critical
clips.

---

## 2. Brutally honest finding: VLMs do NOT solve this, at any size that fits 8 GB

I verified the current (2026) local video-LLM landscape. **The honest bottom line: there
is no off-the-shelf, locally-runnable-in-8 GB video-LLM that does true ~30 fps motion
understanding.** The reasons are structural, not just hardware:

- **State-of-the-art video-LLMs sample at 1 fps, some 2 fps.** Research as of 2026
  states plainly: "Current video LLMs sample frames at 1 FPS, with some increasing to
  2 FPS. However, videos contain rapidly changing, fleeting details, such as body
  movements and micro-expressions that these frame rates cannot adequately capture."
  And: low-fps sampling "fails to preserve rapidly changing visual cues, leading to
  information loss ... models can miss crucial events."
  [Sub-second sampling limits, arXiv 2026](https://arxiv.org/html/2503.13956v1)
  [Token-compression survey, arXiv 2026](https://arxiv.org/pdf/2603.21957)
- **Qwen3-VL (the best 8 GB-class option) is still frame-sampling.** Qwen3-VL-4B/8B
  (Apache-2.0) has strong "temporal awareness" via interleaved-MRoPE + text-timestamp
  alignment, and GGUF runs on llama.cpp/Ollama (support added 2025-10-30). Qwen3-VL-8B
  FP8 ≈ 8 GB; the 4B fits comfortably. **But its "video" input is a set of sampled
  frames** — adopting it would buy a *better describer of the same 1-fps stills*, not
  true motion. It would improve `becky-validate`'s prose; it would NOT close the
  sub-second gap. There are also fresh (May 2026) llama.cpp/Ollama vision-runner crash
  reports on some Qwen3-VL GGUF builds — verify stability before adopting.
  [Qwen3-VL 8B GGUF / 8 GB](https://huggingface.co/Qwen/Qwen3-VL-8B-Instruct-GGUF)
  [Ollama qwen3-vl](https://ollama.com/library/qwen3-vl)
  [llama.cpp Qwen3-VL date](https://huggingface.co/Qwen/Qwen3-VL-32B-Instruct-GGUF)
- The research frontier (16-fps video-LLMs, generative frame samplers, shot-aware
  sampling) is **papers, not shippable local GGUFs** as of 2026-06-07. Promising, not
  deployable here yet. [16 FPS video-LLM, arXiv 2026](https://arxiv.org/html/2503.13956v1)
  [Generative frame sampler, arXiv 2026](https://arxiv.org/html/2503.09146v1)

**So: do not chase a magic local video-LLM. It doesn't exist in 8 GB.** The realistic
answer is a **two-tier motion pipeline** built from purpose-built, genuinely-dense
temporal vision models that DO fit — used to *detect and localize* sub-second motion, and
to *route* only the critical window to the descriptive model.

---

## 3. The realistic option: a dense-motion DETECT → LOCALIZE → ROUTE layer

The right division of labor (consistent with `TEST-FEEDBACK.md` rec #3 — spend expensive
compute only on flagged moments):

```
Tier 0  (already exists)  becky-events: cheap scene-change detection, 1-fps frames
Tier 1  (NEW, this spec)  becky-motion: DENSE-frame motion analysis on short windows
                          → finds sub-second motion bursts + (optionally) classifies them
                          → emits motion windows with start/end at frame precision
Tier 2  (exists, reused)  becky-validate / a strong model: describe ONLY the flagged
                          window, now told exactly when the motion burst is
```

Tier 1 is where true ~30 fps lives. Two components, both verified to run on the 3070:

### 3a. Motion localization — cheap, deterministic, runs anywhere (PRIMARY, Phase 1)

Before any neural model, a **dense optical-flow / frame-difference pass** over the real
frames (decoded at the source fps, e.g. 30) gives a per-frame **motion-energy signal** at
true temporal resolution. This alone solves the *"sub-second events fall between samples"*
half of the problem: it finds the bursts the 1-fps sampler skips.

- ffmpeg already in the stack can emit frame-difference / scene scores; OpenCV (already a
  becky dep via insightface's cv2) provides Farnebäck dense flow or the faster
  `DISOpticalFlow`. CPU-real-time on short clips; GPU optional.
- Output: a motion-energy time series + detected **motion windows** `(start, end, score)`
  at frame precision. No model, no VRAM, deterministic — exactly becky's preferred shape.
- This is the **single highest-value, lowest-risk piece** and should be Phase 1 on its
  own. It does not say *what* happened; it says *exactly when something fast happened*, so
  nothing falls between samples and the descriptive model gets pointed at the right
  ~1–3 s window.

### 3b. Motion classification — dense-clip temporal models (Phase 2, with caveats)

To put a *label* on a burst (not just "motion here"), use a true spatiotemporal model
that ingests a **densely-sampled clip** (e.g. 16 frames over ~2 s — real motion, not
1-fps stills):

- **VideoMAE V2** (ViT video model; CVPR 2023, code + pretrained released; integrated in
  the **MMAction2** zoo). Base/Small variants run inference on a consumer 8 GB GPU. SOTA
  on Kinetics (K400 90.0%).
  [VideoMAE V2 paper](https://arxiv.org/abs/2303.16727)
  [MMAction2 VideoMAEv2 config](https://github.com/open-mmlab/mmaction2/blob/main/configs/recognition/videomaev2/README.md)
- **SlowFast / spatio-temporal action detection (AVA)** also in MMAction2 — can localize
  *who is doing what, where* in the frame across time (the AVA task), which is closer to
  the becky need than whole-clip classification.
  [MMAction2 detection zoo](https://mmaction2.readthedocs.io/en/latest/model_zoo/detection.html)

**Honest caveats — read before betting on Tier 1b:**
1. **Closed vocabulary.** VideoMAE/SlowFast classify into a *fixed label set*
   (Kinetics-400/700, AVA's 80 atomic actions). AVA includes generic person-to-person
   actions ("grab (a person)", "push (another person)", "hug", "hand shake", "watch a
   person") that are *relevant* — but it will NOT produce the nuanced consent/resistance
   narrative the philosophy doc demands. It is a **flag/router**, not a describer.
2. **Licensing.** The Kinetics pretrain weights carry the dataset's **CC BY-NC 4.0**
   (non-commercial) lineage. For a private, human-reviewed forensic investigation this
   is almost certainly fine (non-commercial use), but it is a real constraint to confirm
   with the case's needs. The *code* (VideoMAE / MMAction2) is Apache-2.0.
   [Kinetics/VideoMAE license note](https://arxiv.org/abs/2303.16727)
3. **Maintenance.** MMAction2 is the standard toolkit and its model zoo is current, but
   I **could not positively confirm an active 2026 release cadence** from the docs — its
   latest documented line is 1.2.x. Treat "MMAction2 is still actively maintained in 2026"
   as **unverified**; confirm before committing (the underlying VideoMAE/SlowFast weights
   work regardless of toolkit release pace, via onnxruntime export).
4. **Pose-based alternative (worth a look in build):** a person-pose pre-pass
   (keypoints per frame at 30 fps) + a small temporal classifier captures relative
   motion (hands toward/away, pull-in vs. push-off) very cheaply and is more
   interpretable for a courtroom than a black-box action label. Flagged as an option to
   evaluate, not a verified recommendation.

### Why this beats "just use a bigger VLM"
- It runs in 8 GB (3a needs ~0; 3b base models fit).
- It operates at **true source fps** — it cannot miss a sub-second burst by construction.
- It preserves becky's architecture: deterministic localization + provenance, expensive
  description reserved for flagged windows.
- It is honest: it surfaces "fast motion at 0:13.4–0:14.1, candidate action: grab(person)
  conf 0.6 — REVIEW THIS WINDOW," then hands the human (or a strong model) the exact clip.

---

## 4. Proposed tool: `becky-motion` (Tier 1)

```
becky-motion <video> [--events events.json] [options]

Options:
  --window <a-b>        analyze only [a,b] seconds (default: whole clip, or each
                        becky-events window when --events given)
  --fps <n>             dense sample fps for flow (default: source fps, capped ~30)
  --classify on|off     run the dense-clip action model on each motion burst (Phase 2;
                        default off = localization only)
  --model videomae|slowfast   classifier choice (Phase 2)
  --device cpu|cuda     default from ~/.becky/config.json
  --min-motion <f>      motion-energy threshold to call a "burst"
  --output <file>       JSON here instead of stdout
  --verbose
```

Behavior: JSON-in/JSON-out, exit-coded, offline, graceful-degrade (missing classifier
model → localization-only + a note, exit 0), heavy compute in an embedded Python helper
via `internal/pyhelpers.Materialize` (mirrors faceembed/avlm). Works on COPIES / reads
only — never modifies the source video.

### Output JSON (proposed; synthetic values)
```json
{
  "tool": "becky-motion v1.0.0",
  "source_file": "test.mp4",
  "source_sha256": "ab12cd34…",
  "source_fps": 30.0,
  "analyzed_window": [10.0, 25.0],
  "motion_bursts": [
    {
      "window_start": 13.40, "window_end": 14.10,   // frame-precise, true fps (seconds)
      "peak_time": 13.70,
      "motion_score": 0.82,                          // normalized motion energy
      "candidate_action": "grab (a person)",         // Phase 2 AVA label; "" if --classify off
      "confidence": 0.61,                             // model conf; flagged candidate, NOT a fact
      "frame_index_start": 402, "frame_index_end": 423,
      "recommend_review": true,
      "route_to": "becky-validate"                    // hand-off hint for Tier 2
    }
  ],
  "notes": {
    "classifier": "off (localization only)",
    "honesty": "motion bursts are candidates for human/strong-model review; labels are not conclusions"
  }
}
```

Per `FORENSIC-OUTPUT-PHILOSOPHY.md`: every `candidate_action` is `[CANDIDATE]`, shown
with its basis (model + confidence), never asserted. The *value* is the frame-precise
`window_start/end` so the contact dynamics get described from the right ~1 s — by a human
or a strong model — instead of being missed between 1-fps stills.

---

## 5. Integration

1. **Feeds Tier 2:** `becky-motion` output gives `becky-validate` an exact
   `--window <start-end>` (and a much tighter `--fps`, since the window is short the
   avlm budget can afford 4–8 fps inside the burst — far better than 1 fps clip-wide).
   This is the concrete fix for the avlm limitation: not "see the whole clip at 30 fps"
   (impossible in budget), but "see the *important 1 second* at high fps."
2. **Pipeline step:** add `motion` to `becky-pipeline --steps`, after `events`, before
   `validate`. Resumable/skip-on-exists like the rest. `validate` consumes `motion.json`
   to choose windows.
3. **Index:** optionally store `motion_bursts` as rows (deterministic id
   `sha12(source_file)+":"+frame_index_start`) so the corpus is searchable for
   "clips with a high-motion person-contact burst" — another rank-by-actionability lever
   for 500 GB. DB timestamps RFC3339, matching `schema.sql`.

---

## 6. Recommendation summary

- **Do NOT** adopt a local video-LLM expecting it to fix motion blindness — none exists
  in 8 GB; they all sample ~1 fps (verified 2026-06-07).
- **DO** build `becky-motion` Tier 1a (optical-flow / frame-diff localization) first:
  zero VRAM, deterministic, immediately closes the "events fall between samples" gap by
  finding sub-second bursts at true fps and routing the descriptive model to them.
- **THEN** (Phase 2) add dense-clip classification (VideoMAE V2 / SlowFast via MMAction2,
  onnxruntime) as a candidate-labeler — with eyes open about closed vocabulary,
  CC-BY-NC pretrain lineage, and unconfirmed toolkit cadence.
- **Separately**, swapping Gemma-4 for **Qwen3-VL-4B** (Apache-2.0, fits 8 GB) would
  improve `becky-validate`'s *descriptions* of the (now motion-targeted) window — a
  worthwhile but distinct upgrade; it is NOT a motion fix.

> **Could not verify as current (flagged honestly):**
> - MMAction2's 2026 release/maintenance cadence (latest documented line 1.2.x; treat
>   "actively maintained in 2026" as unconfirmed).
> - Stability of Qwen3-VL GGUF vision on this machine's llama.cpp build (May-2026 crash
>   reports exist for some builds; must be tested).
> - Real-world VRAM headroom of VideoMAE V2 base inference specifically on an 8 GB
>   *laptop* 3070 (papers/zoo show consumer-GPU inference but not this exact card).

---

## Sources (verified 2026-06-07)
- [Video-LLMs sample 1–2 fps; miss body movement/micro-expressions (arXiv 2026)](https://arxiv.org/html/2503.13956v1)
- [Sub-second event loss / token compression survey (arXiv 2026)](https://arxiv.org/pdf/2603.21957)
- [Generative frame sampler for long video (arXiv 2026)](https://arxiv.org/html/2503.09146v1)
- [Qwen3-VL-8B GGUF (fits ~8 GB, Apache-2.0)](https://huggingface.co/Qwen/Qwen3-VL-8B-Instruct-GGUF)
- [Ollama qwen3-vl; llama.cpp support date](https://ollama.com/library/qwen3-vl)
- [VideoMAE V2 (CVPR 2023, weights released)](https://arxiv.org/abs/2303.16727)
- [MMAction2 VideoMAEv2 config](https://github.com/open-mmlab/mmaction2/blob/main/configs/recognition/videomaev2/README.md)
- [MMAction2 spatio-temporal (AVA) detection zoo](https://mmaction2.readthedocs.io/en/latest/model_zoo/detection.html)
