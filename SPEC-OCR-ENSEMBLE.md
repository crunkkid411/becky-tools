# SPEC — becky OCR ensemble + adversarial corroboration

> ## STATUS — PROPOSAL / ENHANCEMENT to `becky-ocr` (cloud, 2026-06-27). Build pending.
> This is **not a new tool.** It is an enhancement to the existing, BUILT `becky-ocr`
> (`cmd/ocr`, PP-OCRv5/v6 via rapidocr ONNX) and a small companion package. It extends
> `SPEC-OCR.md` — it does **not** replace it; read that first. Routed to the local agent
> via `COLLAB-PROTOCOL.md` (registry row + INBOX-3). No Go code yet; the deterministic
> core is cloud-buildable next (see §9). Re-verify every model id / license / score
> against its source before wiring — model lines move weekly (see §8).

---

## 0. TL;DR

Evolve `becky-ocr` from "one primary engine (PP-OCR) + one optional VLM escalation
(PaddleOCR-VL)" — which `SPEC-OCR.md` already describes — into a **small ensemble of
specialist OCR models behind a deterministic router**, with **adversarial corroboration**
on the reads that matter: a low-confidence or forensically-critical span is only
**concluded** when **≥2 independent engines agree**; otherwise it is surfaced as a
`candidate` showing every engine's read. This is `FORENSIC-OUTPUT-PHILOSOPHY.md`'s
corroborate-then-conclude rule applied to OCR. All models are small, MIT/Apache-or-edge,
and GGUF/ONNX → fully offline, trivial on Jordan's 4 TB SSD.

## 1. Why (becky philosophy → OCR)

- **Corroborate, then conclude.** A single OCR read is one weak signal. PP-OCR's existing
  `lines` / `low_confidence_lines` split is the start; this spec makes the confirmation
  **explicit and adversarial** — a deterministic anchor read, challenged by an independent
  model, concluded only on agreement.
- **Recall is for detection, not naming.** Surface every read (every engine's text stays in
  the record); *assert* a read only when corroborated. Disagreements are shown, never hidden.
- **Small specialist models, each for its strength.** Jordan's explicit ask. Different OCR
  jobs (scene text vs single-page docs vs long PDFs vs structured extraction) have different
  best-in-class small models; the router sends each input to the right one.
- **Offline + deterministic anchor.** PP-OCR (≈5 M params, ONNX, deterministic) is always the
  anchor. VLM challengers are the explicit "AI in the loop" local call. The output FORMAT and
  the corroboration record stay deterministic even when a VLM's text is not bit-reproducible.
- **Degrade, never crash.** Any missing engine → fall back to whatever is present; with only
  PP-OCR installed, `becky-ocr` behaves **exactly as it does today**.

## 2. The specialist roster (verified-facts baseline — re-verify before wiring)

| Model | becky role | Size | License | Benchmark (as found) | Source |
|---|---|---|---|---|---|
| **PP-OCRv5 / v6** (rapidocr ONNX) | **Anchor** — exact text, scene text, video frames. Deterministic, fast, CPU. | ~5 M (rec head ~2 M) | Apache-2.0 | SOTA *specialized* OCR; OmniDocBench edit-dist ≈0.067 | [PP-OCRv5 paper](https://arxiv.org/pdf/2603.24373), already in `becky-ocr` |
| **PaddleOCR-VL-1.6** | Doc-parse escalation (tables/formulas/layout), single page. **Incumbent.** | 0.9 B | Apache-2.0 | **96.33** OmniDocBench v1.6 | per `SPEC-OCR.md` §2b; [HF](https://huggingface.co/PaddlePaddle/PaddleOCR-VL) |
| **GLM-OCR** | Doc-parse escalation **candidate** — A/B vs PaddleOCR-VL. | 0.9 B | **MIT** | ~**94.62** OmniDocBench v1.5 (top of some boards) | [HF zai-org/GLM-OCR](https://huggingface.co/zai-org/GLM-OCR) |
| **Unlimited-OCR** | **Long-document** specialist — multi-page PDFs / books, one shot, flat KV cache (32 k ctx). **Fills a real gap.** | 3 B / **500 M active** (MoE) | **MIT** | 93.23 v1.5 / **93.92** v1.6 | [HF baidu/Unlimited-OCR](https://huggingface.co/baidu/Unlimited-OCR), [arXiv 2606.23050](https://arxiv.org/html/2606.23050v1) |
| **LFM2.5-VL-1.6B-Extract** | Document → **structured JSON** fields (forms/IDs/receipts). | 1.6 B | Liquid edge (verify) | — (extraction, not raw OCR) | [HF](https://hf.co/LiquidAI/LFM2.5-VL-1.6B-Extract); already wired in `becky-vision` |

**Decisions baked into the table:** PP-OCR stays the anchor (right tool for frames; nothing
beats it on determinism/cost). PaddleOCR-VL-1.6 currently *out-scores* GLM-OCR and Unlimited-OCR
on single-page OmniDocBench, so GLM-OCR is an **A/B candidate, not an automatic swap** (its edge
is the MIT license + strong tables). Unlimited-OCR wins on a **different axis** — long-horizon
documents — that single-page benchmarks don't measure, so it is **additive**. `becky-vision`
already ships the Extract model; this spec just routes to it.

## 3. The router (deterministic — not a model)

A cheap PP-OCR anchor pass runs first on every input; the router then picks the escalation
engine from **explainable rules** (so the choice is reproducible and auditable):

| Input signal | Primary path |
|---|---|
| Video frame / screenshot / short scene text | **PP-OCR only** (anchor is enough) |
| Single image, text-dense **and** low anchor confidence (photographed doc, dense letter, structured screen) | **PaddleOCR-VL-1.6** (or GLM-OCR per the A/B) |
| PDF / TIFF with **>N pages**, or a long single image | **Unlimited-OCR** (long-horizon) |
| "extract these fields" mode (`--extract`) | **LFM2.5-VL-1.6B-Extract** → JSON |

Density/critical estimation is derived from the anchor pass (box count, mean confidence, line
length), **not** an LLM. `N` (page threshold) and the density cutoff are config, defaulted in §10.

## 4. Adversarial corroboration (the heart)

1. **Anchor pass** — PP-OCR always runs → lines, boxes, per-line confidence. Deterministic.
2. **Escalation triggers** — a span is escalated to a second, *independent* engine when **any**:
   - anchor confidence `< T` (default §10), OR
   - the span matches a **forensically-critical** class (configurable): timestamps/dates, license
     plates, IDs/serials, currency/amounts, or a name in the becky KB, OR
   - the router chose a doc/long-doc path for the whole image.
3. **Challenger pass** — run the routed escalation engine on the region.
4. **Decision rule** — normalize both reads (case-fold, collapse whitespace, fold confusables
   `0/O 1/l/I 5/S`), then compare by edit distance ≤ `tol`:
   - **agree** → `corroboration: corroborated`, asserted in `lines`.
   - **disagree** → `corroboration: candidate`, emitted in `candidates[]` with **both** reads and
     per-engine attribution — a human picks. Never silently choose one.
   - **only one engine available**, or both below `T` → `corroboration: unknown` (candidate).
5. **Determinism by record.** PP-OCR is deterministic; VLM challengers run at temperature 0 +
   fixed seed (best-effort, not bit-guaranteed). So the corroboration **record** — every engine,
   its raw read + confidence, model name+version+quant, and the verdict — is logged, making the
   conclusion auditable and reproducible-by-record even when a VLM's bytes are not.

## 5. Output schema (additive — backward compatible)

Keep today's `becky-ocr` JSON (`lines`, `low_confidence_lines`). Add, per line:
`engines: [{name, version, text, conf}]`, `corroboration: corroborated|candidate|unknown`,
`critical: bool`. Add top-level `candidates: [...]` (disagreements, both reads) and
`engine_runs: [{engine, model, version, quant, ms}]` provenance. Consumers that only read
`lines` keep working; the corroboration data is purely additive.

## 6. Degrade-never-crash

- Only PP-OCR present → no escalation, output identical to today.
- Escalation model/weights missing → typed degrade note, anchor read returned, `corroboration:
  unknown` for would-be-escalated spans. Partial result, never a panic.
- A challenger crashes/times out → log, keep the anchor read as a candidate.

## 7. Offline + storage + freshness

Every engine is small and GGUF/ONNX → runs locally via the existing llama.cpp / onnxruntime
stacks (`internal/avlm/server.go` transport for the GGUF VLMs; rapidocr for PP-OCR). Total
footprint is a few GB — nothing on a 4 TB SSD. Each model is registered in
`internal/freshness/manifest.json` so `becky-freshness` tracks it (and §8 extends that).

## 8. Process fix — make a leaderboard sweep MANDATORY

The gap this whole thread surfaced: research picks a model well, but nothing systematically
asks *"has a better model appeared for this capability?"* `becky-freshness` checks for **newer
versions of what we use**, not **better alternatives**. Proposed addition to
`SPEC-BECKY-NEW-TOOL.md`'s research checklist (and a `becky-freshness` extension):

1. For any model-backed capability, **sweep the capability's leaderboard at research time** and
   record it. OCR → **OmniDocBench** + **OCRBench v2**.
2. **Pin which leaderboard + metric matters per capability** in the manifest (OCR's frame-text
   task ≠ PDF-document-parsing — they reward different models).
3. Extend `becky-freshness` from "is our pinned model newer?" to "has something **outscored** our
   pinned model on *our* leaderboard?" (`becky-scout` already does playlist→capability discovery;
   this complements it.)
4. **Always verify the top candidates on becky's own evidence.** Leaderboards disagree and
   saturate — the same models rank #1 on one board and ~#19 on another depending on
   version/metric/filter. The leaderboard narrows the field; becky's own frames pick the winner.

## 9. Build plan (cloud vs local)

**Cloud (deterministic, buildable here next — no models needed):** a self-contained
`internal/ocrfuse` package, kept OUT of `cmd/ocr` so it does not collide with local's active OCR
work — local wires it in. Contents: the router rules; the normalize + edit-distance agreement +
status logic; the forensically-critical span matcher; the additive schema + merge; and a
**fake-engine** harness so the full corroboration pipeline is unit-tested with synthetic reads,
no weights or network. (Mirrors how `internal/samplelib` / `internal/ctlmodel` were delivered.)

**Local (the model boundary):** install/run the real backends — GLM-OCR, Unlimited-OCR, Extract
via llama.cpp; PaddleOCR-VL — download weights, run the §10 A/B on real frames, tune `T` / `tol` /
the page threshold, and wire `internal/ocrfuse` into `cmd/ocr`.

## 10. Open decisions for Jordan

1. **Doc-slot engine:** GLM-OCR (MIT) **vs** PaddleOCR-VL-1.6 (incumbent, higher current score) —
   resolve by A/B on real frames. *Recommend: keep PaddleOCR-VL as default, A/B GLM-OCR.*
2. **Confidence threshold `T`** for escalation (start ~0.85).
3. **Default "forensically-critical" classes** (dates/times, plates, IDs, amounts, KB-names) —
   confirm the set.
4. **Long-doc in v1?** Add Unlimited-OCR now, or defer until a real long-PDF case appears.
5. **Agreement tolerance `tol`** (edit distance for "two reads agree"; start: exact after
   normalization for short critical strings, ≤10 % length for longer lines).
6. **Escalate-only vs run-everything** (cost): default escalate-only (cheap); a `--thorough` flag
   runs all routed engines on the whole image for high-stakes evidence.

## 11. Sources

- PP-OCRv5 (5 M rivals billion-param VLMs): https://arxiv.org/pdf/2603.24373
- PaddleOCR-VL: https://huggingface.co/PaddlePaddle/PaddleOCR-VL · https://arxiv.org/pdf/2510.14528
- GLM-OCR: https://huggingface.co/zai-org/GLM-OCR
- Unlimited-OCR: https://huggingface.co/baidu/Unlimited-OCR · https://arxiv.org/html/2606.23050v1
- LFM2.5-VL-1.6B-Extract: https://hf.co/LiquidAI/LFM2.5-VL-1.6B-Extract
- OmniDocBench (CVPR 2025): https://github.com/opendatalab/OmniDocBench · "saturated?": https://www.llamaindex.ai/blog/omnidocbench-is-saturated-what-s-next-for-ocr-benchmarks
- OCRBench v2: https://arxiv.org/html/2501.00321v2
