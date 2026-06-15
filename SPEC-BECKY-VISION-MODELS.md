# SPEC — becky vision models — adopt Liquid AI LFM2-VL / LFM2.5-VL + a custom-training plan

> ## STATUS — design only, NOT built
> No Go code, no Python helper, no model download exists for this yet. This spec
> proposes (a) adding the **Liquid AI LFM2-VL / LFM2.5-VL** lightweight
> vision-language models as *additional, task-specific* VLM backends alongside
> becky's existing Gemma-4 E4B (AVLM) and PaddleOCR stacks, and (b) a concrete
> **custom-training** plan (Unsloth SFT/LoRA → GGUF) for becky-specific jobs.
> Nothing here changes a shipped tool until Jordan greenlights it (open decisions
> at the end). Re-verify any model id / license clause / llama.cpp flag past its
> cited date before building — the verified-facts block in §1 is the re-check
> baseline.
>
> **Core principle (Jordan's, load-bearing):** *everything human is visual first.*
> becky-ask already carries a vision model; becky's vision is a video+audio
> understanding model. This spec keeps that posture — small vision models go where
> the human's intent is visual, and the heavy audio-visual reasoner (Gemma-4)
> stays for the jobs that genuinely need it.

---

## 0. TL;DR

Liquid AI ships a family of *tiny, edge-class* vision-language models (LFM2-VL /
LFM2.5-VL) at **450M** and **1.6B** parameters, with **first-class GGUF builds and
day-one llama.cpp support** (image-text-to-text, multilingual). becky already runs
llama.cpp with an `--mmproj` multimodal projector for Gemma-4 E4B — so an LFM2-VL
GGUF drops into the **exact same `internal/avlm` llama-server transport** with a
different model + mmproj pair. The win is **right-sizing**:

- A **450M** VLM for ultra-light frame/scene captioning and a fast triage pass —
  fits in well under 1 GB, leaves the 8 GB RTX 3070 free for the rest of the
  pipeline.
- A **1.6B-Extract** variant **purpose-fine-tuned for document/structured
  extraction** (with an official GGUF) for becky-ocr's document-VLM escalation —
  lighter than Gemma-4 and lighter than the deferred PaddleOCR-VL.
- **Gemma-4 E4B stays** the default for heavy multimodal (audio + video) reasoning
  in becky-validate. LFM2-VL is **image-only** — it does not replace the AVLM.

And the headline Jordan is excited about: because LFM2-VL is small, **Unsloth +
LoRA fine-tuning of it is feasible on his own 3070**, exporting straight back to a
GGUF that runs on the same llama.cpp path. That makes a *custom-trained becky
vision model* — tuned on his evidence and his preferences — a real, near-term
option, not a fantasy. §6 is the plan.

This stays inside becky's ethos: **offline + deterministic at inference, degrade
never crash, one model = one explicit local call.** LFM2-VL is *added as a tracked
candidate*, not forced on any existing tool.

---

## 1. Verified facts (the re-check baseline) — Liquid AI LFM2-VL / LFM2.5-VL

All fetched **2026-06-15** via the Hugging Face hub tools + Liquid/Unsloth docs.
Re-verify before building; model lines move.

### 1.1 The model family (Hugging Face, author `LiquidAI`)

| Repo | Params | Format | Downloads | Updated | Notes |
|---|---|---|---|---|---|
| `LiquidAI/LFM2.5-VL-1.6B` | 1.60B | transformers (safetensors) | ~511K | 30 Mar 2026 | flagship edge VLM; `image-text-to-text`; multilingual (en/ja/ko/fr/es/de/ar/zh); base `LFM2.5-1.2B-Base` |
| `LiquidAI/LFM2.5-VL-1.6B-GGUF` | 1.60B | **GGUF (llama.cpp)** | ~568K | 30 Mar 2026 | quantized flagship; `llama.cpp` tag; +it/pt langs |
| `LiquidAI/LFM2.5-VL-450M` | 449M | transformers | ~102K | 8 Apr 2026 | tiny edge VLM; base `LFM2.5-350M` |
| `LiquidAI/LFM2.5-VL-450M-GGUF` | 449M | **GGUF (llama.cpp)** | ~72K | 11 Jun 2026 | tiny GGUF VLM; tags `vlm`/`llama.cpp` |
| `LiquidAI/LFM2.5-VL-1.6B-Extract` | 1.60B | transformers | ~3.1K | 4 Jun 2026 | **fine-tune SPECIALIZED for extraction** (documents / structured data); English; demo "Liquid-Image-to-JSON" |
| `LiquidAI/LFM2.5-VL-1.6B-Extract-GGUF` | 1.60B | **GGUF (llama.cpp)** | ~4.3K | 4 Jun 2026 | GGUF build of the Extract fine-tune → runs on llama.cpp |
| `LiquidAI/LFM2-VL-3B-GGUF` | ~3B | GGUF | ~238K | 30 Mar 2026 | larger sibling (prev-gen LFM2-VL); GGUF/llama.cpp |

- **Architecture:** `lfm2_vl` = the **LFM2.5** language backbone + a **SigLIP2
  NaFlex 400M** vision encoder (per Unsloth's LFM2.5 tutorial). Tech report:
  `arXiv:2511.23404`; the Extract line also cites `arXiv:2502.14786`.
- **Proven LoRA fine-tunes already exist in the wild** (e.g. community PEFT/LoRA
  fine-tunes of LFM2-VL such as a `catmus`/medieval-document tune) — concrete proof
  that LoRA fine-tuning of this family works and lands on the Hub. (Verify the
  specific repo before citing it as a template.)

### 1.2 GGUF + llama.cpp (the integration fact that makes this cheap)

- LFM2-VL GGUF ships **`mmproj-*.gguf`** multimodal-projector files — the **same
  mmproj concept becky already uses for Gemma-4**. Liquid's deployment docs show
  both `llama-mtmd-cli` and `llama-server` invocations:
  ```bash
  # CLI (one-shot)
  llama-mtmd-cli -m LFM2.5-VL-1.6B-Q4_K_M.gguf \
    --mmproj mmproj-LFM2.5-VL-1.6B-Q8_0.gguf \
    --image frame.jpg -p "What is in this image?" -ngl 99
  # server (becky's path)
  llama-server -m LFM2.5-VL-1.6B-Q4_K_M.gguf \
    --mmproj mmproj-LFM2.5-VL-1.6B-Q8_0.gguf \
    -c 4096 --port 8080 -ngl 99
  ```
- **Day-one llama.cpp support**, "under 1 GB of memory" for the small variants,
  with quoted edge speeds (~239 tok/s decode on an AMD CPU; ~82 tok/s on a mobile
  NPU). Supported quants include **Q4_0, Q4_K_M (recommended), Q5_K_M, Q6_K, Q8_0,
  F16**.
- **becky already drives `llama-server` (not `llama-mtmd-cli`)** for multimodal —
  see `internal/avlm/server.go`. LFM2-VL is a model+mmproj swap on that transport
  (caveat: re-check the `-fa off` flash-attention quirk per-model; it's a Gemma-4
  workaround, may differ for LFM2-VL — see §4).

### 1.3 License — **LFM Open License v1.0** (NOT plain Apache-2.0)

- Based on Apache-2.0, **royalty-free**, allows use / modify / **fine-tune** /
  distribute, **including commercial** — but with a **commercial revenue
  threshold**: free commercial use is for entities with **annual revenue under
  USD $10M**. Above $10M you must contact `sales@liquid.ai` for a commercial
  license. Qualified nonprofits / research are exempt from the threshold.
- **For Jordan (independent producer / forensic analyst, well under $10M):
  free commercial + fine-tune + redistribute is permitted.** This is a *go* for
  his use, but it is **not** the Apache-2.0 becky's offline-ethos docs assume —
  flag it explicitly in `becky-freshness` and any release notes (see §7), and
  re-confirm the live clause before shipping a product on top of it.
- **Contrast:** PaddleOCR / PaddleOCR-VL are Apache-2.0 (no revenue clause); Gemma
  is under the Gemma terms. So LFM2-VL is the *one* candidate here with a revenue
  gate — track it.

Sources (verified 2026-06-15): HF repos under `https://hf.co/LiquidAI/…`; Liquid
docs `https://docs.liquid.ai/deployment/on-device/llama-cpp` and
`https://docs.liquid.ai/lfm/getting-started/model-license` /
`https://www.liquid.ai/lfm-license`; Unsloth `https://unsloth.ai/docs/models/tutorials/lfm2.5`;
tech report `https://arxiv.org/abs/2511.23404`.

---

## 2. becky's current vision stack (what we're integrating with)

So the proposal is grounded in real, shipped code:

- **AVLM (audio-visual reasoning):** `becky-validate` / `internal/avlm` runs
  **Gemma-4 E4B-it** (`Q4_K_M` GGUF + **BF16 mmproj**, mandatory) through
  **llama.cpp `llama-server`**. The transport (`internal/avlm/server.go`) spawns a
  transient server on a free port → waits `/health` → POSTs OpenAI-style
  `/v1/chat/completions` with frames as `image_url` parts **and audio as an
  `input_audio` part** → reads `message.content`, with
  `chat_template_kwargs.enable_thinking=false` and **`-fa off`** (the Gemma-4 mmproj
  misbehaves with flash-attention). Pinned in `internal/freshness/manifest.json`
  as `gemma-4-e4b`.
- **OCR:** `becky-ocr` runs **PaddleOCR PP-OCRv6→v5→v4** via `rapidocr`
  (classic det+rec) and has **PaddleOCR-VL** (0.9B document VLM, Apache-2.0)
  *wired but deferred* behind `--engine ppocr-vl`, to be activated by installing
  the model. Pinned as `paddleocr-pipeline`, `rapidocr`, `paddleocr-vl`.
- **Intent (text):** `becky-ask` drives a local **Qwen3.5-4B** GGUF through the
  same llama-server transport for natural-language intent (text-only).
- **Runtime:** llama.cpp + onnxruntime, both local. Offline + deterministic at
  inference (fixed seeds), degrade-never-crash.
- **Freshness:** `internal/freshness/manifest.json` is the single source of truth
  for pinned models and where to watch upstream.

**The seam:** LFM2-VL reuses the **AVLM llama-server transport almost verbatim**.
The only real differences are (a) it's **image-only** (no `input_audio` part), and
(b) its own model/mmproj/`-fa`/quant settings. That is the whole integration cost.

---

## 3. Which becky task uses which model (the decision table)

The principle: **right-size the model to the job, never one-size-fits-all.** "Good
enough + small + free + fine-tunable" beats "biggest" for the visual-first, triage,
and extraction jobs; keep Gemma-4 only where audio+video reasoning actually earns
its weight.

| becky task | Recommended model | Why / "good enough" boundary |
|---|---|---|
| **Document / structured extraction** in `becky-ocr` (forms, IDs, screenshots of chats, receipts, dense doc frames → JSON fields) | **`LFM2.5-VL-1.6B-Extract-GGUF`** | Purpose-fine-tuned for extraction with a ready GGUF; "Image-to-JSON". Lighter than Gemma-4 and than the deferred PaddleOCR-VL, and **structured-output-native**. Sits as a *new* `becky-ocr --engine lfm-extract` option **beside** PP-OCR (which stays the line/char det+rec floor) and `ppocr-vl`. |
| **Ultra-light frame / scene captioning**; **fast triage first-pass** over thousands of frames ("is there a face / screen / weapon / text here? what's the gist?") to decide *which* frames deserve the heavy reasoner | **`LFM2.5-VL-450M-GGUF`** | <1 GB, very fast; recall-first cheap gate. Surfaces candidates for Gemma-4 / human review — never the final word. Classic "cheap detector, expensive namer" split. |
| **Single-image visual Q&A in `becky-ask`** ("what's in this screenshot?", "describe this photo target") where there's no audio and no need for Gemma-class reasoning | **`LFM2.5-VL-1.6B-GGUF`** | Matches Jordan's "becky-ask has a vision model": a small, fast image understander for the chat front-door's visual questions, local-only. |
| **Heavy multimodal reasoning — audio + video together** (`becky-validate` AVLM: who/what/where over a clip with sound) | **KEEP Gemma-4 E4B** | LFM2-VL is **image-only**; it cannot ingest the `input_audio` part. Audio-visual fusion is exactly Gemma-4's job. **Do not** swap this. |
| **Line/character OCR det+rec** (the deterministic text floor) | **KEEP PaddleOCR PP-OCRv6→v4** | A VLM is not a replacement for fast, deterministic text detection+recognition; the Extract VLM escalates *on top* for structured fields, not instead. |

**Where LFM2-VL is NOT good enough (be honest):**
- **No audio.** Anything needing sound stays on Gemma-4. This is the single biggest
  boundary — becky's "vision is a video+audio understanding model" line means the
  *flagship* job keeps the AVLM.
- **Forensic NAMING.** Per `FORENSIC-OUTPUT-PHILOSOPHY.md`, a small VLM's caption is
  a **candidate/detection signal**, never a corroborated conclusion. LFM2-VL feeds
  the triage funnel; it does not assign identities or final findings.
- **Long-context video reasoning / temporal fusion across many frames** — out of
  scope for a 450M–1.6B image model; that's the AVLM's two-stage caption+synthesize
  flow.

Net: LFM2-VL adds a **cheap, fast, fine-tunable layer** (triage + extraction +
chat-image) *under* and *beside* the existing stack, not over it.

---

## 4. llama.cpp integration — the thin helper contract

LFM2-VL reuses `internal/avlm`'s transport. Proposed shape (additive; nothing in
the Gemma-4 path changes):

- **Backend selection.** Add an `lfm-vl` backend option to the AVLM runner (and a
  `becky-ocr --engine lfm-extract` route). A `Runner` is already parameterized by
  `Model`, `MMProj`, `Server`, `NGL` — populate those from config for the LFM2-VL
  GGUF + its `mmproj-*.gguf` instead of Gemma-4's pair. **Reuse `spawnServer` /
  `ensureServer` / `chat` verbatim.**
- **Image-only request.** Send only `text` + `image_url` content parts; **omit the
  `input_audio` part**. (The Gemma-4 path's audio part is simply not appended.)
- **Per-model server flags (re-verify, don't copy blindly):**
  - `enable_thinking=false` is a Gemma-4 chat-template toggle — LFM2-VL may not need
    it; harmless if the template ignores it, but confirm the LFM2.5 chat template.
  - **`-fa off`** is a *Gemma-4 mmproj* workaround. For LFM2-VL, **test
    flash-attention ON first** (it's faster) and only fall back to `off` if the
    projector misbehaves. Record the verified setting in config + this spec.
  - Context `-c`: the 16384 window in `server.go` is sized for ~30 Gemma frames +
    audio. LFM2-VL image-only single-frame triage needs far less (4096 is Liquid's
    documented default); size per use.
  - Quant default: **Q4_K_M** (Liquid's recommended) for the 1.6B variants; the
    450M can afford **Q8_0/F16** and still be tiny.
- **Helper contract (the local agent wires this):**
  - *Input:* a frame path (or a base64 image) + a prompt (caption prompt, or for
    Extract a JSON-schema/field-list prompt).
  - *Output:* assistant text → for Extract, a **single JSON object** of fields
    (becky's JSON-out discipline); for captioning, a short candidate caption string.
  - *Degrade:* model/mmproj/binary absent or server unhealthy → typed **DegradeError**
    + partial result (the AVLM already returns `*DegradeError` on every recoverable
    failure) — **never panic**. Falls back: Extract → PP-OCR/`ppocr-vl`; caption →
    skip/triage-unknown; ask-image → "vision model unavailable" honest note.
- **VRAM / speed on the RTX 3070 (8 GB) — estimate, MEASURE locally:** the 450M
  Q8_0 (<1 GB) and 1.6B Q4_K_M (~1–1.3 GB) both fit fully GPU-offloaded (`-ngl 99`)
  with large headroom — markedly lighter than Gemma-4 E4B. Liquid quotes edge
  throughput in the hundreds of tok/s on CPU/NPU; on a 3070 expect comfortably
  interactive single-frame latency. **These are projections; the local agent must
  benchmark real frames and pin the numbers here.**

---

## 5. becky-freshness — track LFM2-VL as a pinned candidate

Add entries to `internal/freshness/manifest.json` so a newer LFM2-VL build (or an
Extract revision) is **never missed** — that manifest exists precisely because the
PaddleOCR-VL improvement was nearly missed before. *(Note only — the actual JSON
edit is the local agent's job; here is the contract.)* Proposed rows, matching the
existing schema (`id`, `name`, `used_by`, `pinned`, `upstream`, `note`):

```jsonc
{
  "id": "lfm2-vl-extract",
  "name": "LiquidAI LFM2.5-VL-1.6B-Extract (GGUF)",
  "used_by": ["becky-ocr"],
  "pinned": "1.6B-Extract Q4_K_M + mmproj (WIRED as --engine lfm-extract; activate by installing the GGUF)",
  "upstream": {"type": "hf-model", "ref": "LiquidAI/LFM2.5-VL-1.6B-Extract-GGUF"},
  "note": "Document/structured extraction VLM -> Image-to-JSON. LFM Open License v1.0 (free commercial under $10M revenue) -- NOT Apache-2.0; re-confirm clause on upgrade."
},
{
  "id": "lfm2-vl-450m",
  "name": "LiquidAI LFM2.5-VL-450M (GGUF)",
  "used_by": ["becky-validate", "becky-ask"],
  "pinned": "450M Q8_0 + mmproj (candidate: ultra-light triage/caption)",
  "upstream": {"type": "hf-model", "ref": "LiquidAI/LFM2.5-VL-450M-GGUF"},
  "note": "Tiny edge VLM for fast frame triage + image Q&A via llama.cpp. Image-only (no audio); Gemma-4 stays the AVLM. LFM Open License v1.0."
}
```
(Optionally add `LFM2.5-VL-1.6B-GGUF` for the ask-image role if §8-Q2 picks it.)
The `llama-cpp` row's `used_by` should gain the new tools when wired.

---

## 6. Custom training plan — the headline (Unsloth → GGUF, and LoRA adapters)

This is the part Jordan is "absolutely thrilled" about: a **custom-trained becky
vision (or preference) model**, tuned on *his* evidence and *his* choices, that
runs on the same offline llama.cpp path. It is genuinely feasible because LFM2-VL
is small enough to fine-tune on a single 3070.

### 6.a Unsloth SFT/LoRA → GGUF export — the pipeline

Unsloth officially supports **LFM2.5-VL-1.6B** fine-tuning ("fine-tune it locally
with Unsloth", "2× faster, ~50% less VRAM", and it "fits comfortably on a free
Colab T4"). Pipeline:

1. **Load** with `FastVisionModel` (Unsloth) → the LFM2.5-VL base.
2. **Attach LoRA** adapters: documented config **r=16, lora_alpha=16, dropout=0,
   bias="none"**, targeting language + attention + MLP modules
   (`finetune_vision_layers=False` by default — tune the language/attention side;
   flip vision layers on only if the visual domain shifts hard, e.g. unusual frame
   types).
3. **Data format** = multimodal conversations: each example is an image plus a
   `[{"type":"image"}, {"type":"text","text": "<instruction>"}]` user turn and the
   desired assistant answer (caption / JSON fields / label). Standard
   instruction-response shape; deterministic to generate from becky's own outputs.
4. **Train** (SFT with the LoRA), then **export**:
   `model.save_preferred_gguf("becky_lfm_vl", tokenizer, quantization_method="q4_k_m")`
   → a **GGUF that drops straight onto becky's llama.cpp transport** (§4). Unsloth
   can also **merge LoRA into the base and export GGUF** for llama.cpp/Ollama.

**SFT vs LoRA — when:**
- **LoRA (default, start here):** cheap, small adapters, fast iteration, low VRAM —
  ideal on a 3070 and for "learn *this specific* becky job" without touching the
  base. Multiple task LoRAs can coexist as separate adapters.
- **Full SFT:** only if a task needs to move the base broadly (rare for becky's
  narrow jobs) — heavier VRAM/time; usually unnecessary.

**VRAM on the 3070 (8 GB):** the 450M and 1.6B bases are small; with Unsloth's
4-bit + LoRA + gradient checkpointing, **1.6B LoRA fine-tuning is expected to fit
in 8 GB** (the same recipe fits a free T4, which is ~16 GB but slower; the 450M is
trivially comfortable). **Projection — the local agent must confirm** real
batch-size/seq-length that fits, and fall back to the 450M base or a smaller batch
if the 1.6B run OOMs. Offline-friendly: training is a *local, opt-in, one-off*; it
does not touch the offline-at-inference invariant.

### 6.b LoRA adapters — task-specific becky models, served via llama.cpp

Train **small, swappable LoRAs** for distinct becky jobs and **merge → GGUF** for
serving (llama.cpp serves the merged GGUF; for hot-swappable adapters keep separate
merged GGUFs per task, selected by config — simplest + most deterministic). Candidate
adapters:

- **Forensic-frame classifier / captioner LoRA** — tuned on Jordan's footage to caption
  becky-relevant frames in *his* vocabulary (scene type, on-screen text presence,
  people-present, screen-vs-real-world) → a sharper, cheaper triage gate than a
  generic 450M.
- **Evidence-OCR-VL LoRA** — Extract base fine-tuned on the document/screenshot
  *types he actually has* (chat exports, IDs, receipts, specific forms) so
  Image-to-JSON hits *his* fields, beating the generic Extract model on his corpus.
- **"becky preference" model (the visual-first preference loop)** — a model that
  learns *his* creative choices (music/mix/vocal-edit/cut decisions), tying directly
  into the preference-learning loops in **SPEC-BECKY-VOX** and **SPEC-BECKY-DAW-EDITOR**.
  Because "everything human is visual first," the preference signal can be
  **visual** (e.g. the waveform/clip/timeline thumbnail + the chosen edit) — an
  LFM2-VL LoRA that, shown the visual state, predicts the edit *he'd* pick. This is
  the bridge between the vision models here and the editor/vox preference specs.

### 6.c Candidate training tasks + the data each needs

| Task | Base | Adapter | Data needed (from becky itself where possible) |
|---|---|---|---|
| Forensic-frame triage/caption | 450M or 1.6B | LoRA | frames + short labels/captions (bootstrap from Gemma-4 captions, human-corrected) |
| Evidence-OCR → his fields (Image-to-JSON) | 1.6B-Extract | LoRA | his document/screenshot images + target JSON (hand-labeled or PP-OCR-bootstrapped) |
| becky preference (edit/mix choices) | 1.6B-VL | LoRA | (visual state image, chosen action) pairs logged by becky-vox / daw-editor preference loop |

**Data discipline (forensic):** training data may contain case material — keep it
**local, never uploaded**; document provenance; treat any bootstrap labels
(Gemma-4 captions, PP-OCR text) as *weak/auto* and human-review the gold set, per
the corroborate-then-conclude rule. The trained model still emits
candidate-not-conclusion output at inference.

---

## 7. Invariants — how this stays becky-shaped

- **Offline + deterministic at inference.** Inference is local llama.cpp, fixed
  seed, no network. *Training* is an explicit, opt-in, local one-off (or
  user-chosen Colab) — it is a build-time activity, not a runtime network call, so
  the offline-at-inference invariant holds.
- **Degrade, never crash.** Missing GGUF/mmproj/binary or unhealthy server → typed
  `DegradeError` + partial result + honest note; fall back to PP-OCR / Gemma-4 /
  skip. (The AVLM runner already enforces this.)
- **Single-tool principle.** LFM2-VL is added as *backends/engines* on existing
  tools (`becky-ocr --engine lfm-extract`, an AVLM `lfm-vl` backend, an
  ask-image option) — **not** a new mega-tool. No tool's contract changes; each
  stays file/JSON-in → JSON-out → exit code.
- **License caveat tracked.** LFM Open License v1.0 (revenue-gated, not Apache-2.0)
  is flagged in `becky-freshness` and must be re-confirmed on upgrade and before any
  >$10M-revenue commercial release. Fine (free) for Jordan today.
- **Forensic output unchanged.** Small-VLM captions/labels are detection candidates;
  naming/conclusions still require corroboration (`FORENSIC-OUTPUT-PHILOSOPHY.md`).

---

## 8. Cloud ↔ local build split + open decisions

### Build split (per CLAUDE.md §4)
| Cloud / web agent | Local agent (Jordan's Win10 + 3070) |
|---|---|
| This spec; model/license verification | Download the GGUF + mmproj; real inference |
| Go scaffolding: `lfm-vl` AVLM backend + `--engine lfm-extract` route (image-only request, config wiring) | Pin `-fa on/off`, quant, `-c`, `enable_thinking` from real runs; record verified flags |
| Unit tests for the deterministic Go (backend selection, request shape, degrade paths) | VRAM/latency benchmarks on real frames; freshness JSON edit |
| Custom-training plan (this §6) | Run the Unsloth LoRA → GGUF on the 3070; tune; pin numbers |

Every Python/training step the cloud agent can't run is left as a documented stub
with an explicit input→output contract; the local agent only plugs in the model +
real numbers. Per CLAUDE.md, the cloud agent works on a `claude/*` branch and opens
a **draft PR** — no push to `master`, no code written until the decisions below.

### Open decisions for Jordan (go/no-go)
1. **Adopt LFM2-VL at all?** It opts into a *non-Apache* (revenue-gated) license —
   free for you now, but a new license class for becky. OK to track + use?
2. **ask-image model:** for becky-ask's visual Q&A, use `LFM2.5-VL-1.6B-GGUF` (more
   capable) or reuse the `450M` (tinier, already pulled for triage)?
3. **becky-ocr ordering:** make `lfm-extract` the default document-VLM escalation,
   or keep `ppocr-vl` (Apache-2.0) default and `lfm-extract` opt-in? (License vs
   capability trade-off.)
4. **Custom training first target:** which adapter first — forensic-frame triage,
   evidence-OCR-to-his-fields, or the visual preference model (ties to
   SPEC-BECKY-VOX / SPEC-BECKY-DAW-EDITOR)?
5. **Train where:** locally on the 3070 (fully offline, slower) vs a one-off Colab
   T4 (faster, but data leaves the machine — forensic concern)? Default
   recommendation: **local-only** for any case data.

---

## 9. Sources (verified 2026-06-15)

- HF: `https://hf.co/LiquidAI/LFM2.5-VL-1.6B`, `…/LFM2.5-VL-1.6B-GGUF`,
  `…/LFM2.5-VL-450M`, `…/LFM2.5-VL-450M-GGUF`, `…/LFM2.5-VL-1.6B-Extract`,
  `…/LFM2.5-VL-1.6B-Extract-GGUF`, `…/LFM2-VL-3B-GGUF` (downloads/dates/tags above).
- Liquid docs: `https://docs.liquid.ai/deployment/on-device/llama-cpp` (mmproj +
  llama-server/`llama-mtmd-cli` commands, quants),
  `https://docs.liquid.ai/lfm/getting-started/model-license` /
  `https://www.liquid.ai/lfm-license` (LFM Open License v1.0, $10M threshold).
- Unsloth: `https://unsloth.ai/docs/models/tutorials/lfm2.5` (FastVisionModel +
  LoRA r/alpha config, data format, `save_preferred_gguf` q4_k_m export, SigLIP2
  NaFlex encoder, T4-fits / 2×-faster / ~50%-less-VRAM claims).
- Tech report: `https://arxiv.org/abs/2511.23404`.
- In-repo grounding: `becky-go/internal/avlm/server.go` (the llama-server
  transport reused), `internal/freshness/manifest.json` (`gemma-4-e4b`,
  `paddleocr-vl`, `llama-cpp` pins), `SPEC-OCR.md` (PP-OCR + PaddleOCR-VL),
  `SPEC-BECKY-ASK.md` (the local-VLM-in-the-chat posture),
  `SPEC-BECKY-VOX.md` / `SPEC-BECKY-DAW-EDITOR.md` (the preference loop §6.b ties
  into), `FORENSIC-OUTPUT-PHILOSOPHY.md` (candidate-not-conclusion).
