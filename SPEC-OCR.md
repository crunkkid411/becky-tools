# SPEC-OCR.md — Frame OCR for forensic video

> **BUILT 2026-06-08 — `becky-ocr` (cmd/ocr) ships.** The Phase-1 engine is
> **PaddleOCR PP-OCRv5 via ONNX Runtime, through the `rapidocr` package** (RapidOCR
> = the maintained, Apache-2.0 ONNX port of the PaddleOCR PP-OCR pipeline:
> det + angle-cls + rec). This is the exact "PP-OCRv5 via ONNX, reusing becky's
> onnxruntime stack" the spec below recommends — RapidOCR is how PP-OCRv5 is consumed
> on ONNX Runtime in practice (PaddleOCR itself does not ship pre-exported ONNX in a
> turnkey form). Verified working offline on real CLANCY frames (livestream chat +
> document screenshots) at 0.96–1.00 recognition confidence. See "Implementation
> status" at the bottom.
>
> Originally authored 2026-06-07 as research + design. Every external tool below was
> re-verified against current availability — versions, license, offline capability,
> and Windows + RTX 3070 Laptop (~8 GB VRAM, CUDA compute 8.6) support — with sources
> cited inline.

---

## 1. Goal (why this is the highest-ROI addition)

Read the **on-screen TEXT** in the frames `becky-osint` / `becky-events` already export:
signage, **license plates**, storefront names, mail/envelopes, documents, phone &
computer screens, livestream chat burned into the video, and burned-in timestamps.

This is where the corpus's **addresses and names actually live**. Per
`TEST-FEEDBACK.md` recommendation #4: "Add frame OCR ... this is where addresses and
names actually live. Neither arm ran OCR ... The high-value clips in 500 GB won't be
dog-walks — they'll be the ones with signage, plates, storefronts, screens, mail,
documents." A GPS-less clip cannot yield an address — but a storefront or an envelope
in-frame can. OCR is the direct path from "thin b-roll" to actionable location/identity.

This is a **recall-first DETECTION** feature whose OUTPUT obeys
`FORENSIC-OUTPUT-PHILOSOPHY.md` (TOP PRINCIPLE, 2026-06-08): cast wide at detection,
but **corroborate and CONCLUDE** at output. OCR reads each line with a confidence
score and frame provenance; high-confidence reads are **asserted** (in `lines`),
genuinely shaky reads are flagged (in `low_confidence_lines`) — never hidden. A lone
OCR line is one weak signal and stays a candidate; a high-confidence read that other
passes corroborate (fixtures, listing, metadata timeline) becomes a stated conclusion
("this is 2601 Chatham Cir"), reached in the orchestrator/search layer. The human
reviews confident, corroborated findings — they do not sort raw candidates.

---

## 2. Current OFFLINE OCR landscape — verified 2026-06-07

I evaluated three classes and verified each is current, offline-capable, and runs on
Windows + the RTX 3070 (compute 8.6).

### 2a. PaddleOCR — classic detect+recognize pipeline (PP-OCRv5) — **PRIMARY RECOMMENDATION**

- **What it is:** The detection + recognition pipeline (text-box detector → angle
  classifier → CRNN-style recognizer). NOT a VLM — deterministic, fast, line-level.
- **Current version:** PaddleOCR **v3.6.0, released 2026-05-28** (prior: v3.5.0
  2026-04-21; v3.4.0 2026-01-29). Actively maintained.
  [GitHub releases](https://github.com/PaddlePaddle/PaddleOCR/releases)
- **Model:** PP-OCRv5, +13% end-to-end accuracy over v4; recognition head ~2M params;
  100+/109 languages incl. English. Mobile (~2.4 GB peak) and Server (up to ~13.5 GB
  peak on high-res) tiers.
  [PP-OCRv5 docs](https://www.paddleocr.ai/latest/en/version3.x/algorithm/PP-OCRv5/PP-OCRv5.html)
- **License:** **Apache 2.0** (toolkit AND PP-OCRv5 weights). Commercial-safe.
  [PaddleOCR repo](https://github.com/PaddlePaddle/PaddleOCR)
- **Offline:** Yes — weights download once, then fully air-gapped.
- **Windows + GPU:** Yes (Windows/Linux/macOS; NVIDIA GPU supported).
- **Inference engine — KEY for becky:** Runs on **Paddle Inference OR ONNX Runtime**
  (confirmed for PP-OCRv5 models, since v3.2.0). The ONNX-Runtime path is what becky
  should use — it matches the existing `onnxruntime` plumbing already in the repo
  (insightface, sherpa-onnx). **No PaddlePaddle framework dependency** if we export
  the det+rec models to ONNX, avoiding a heavy new runtime. *Verify-in-build:* confirm
  the v3.6.0 ONNX export recipe for det + cls + rec separately.

**Why primary:** Apache 2.0 (clean license), tiny + fast (mobile tier runs even on CPU,
seconds/frame), ONNX-Runtime backend reuses becky's existing stack, deterministic
(no LLM hallucination), best-in-class on the *line OCR* job that signage/plates/screens
actually are. This is the workhorse for the 500 GB sweep.

### 2b. PaddleOCR-VL — 0.9B document-parsing VLM — **SECONDARY (selective escalation)**

- **What it is:** A 0.9B vision-language OCR model for document parsing (layout +
  reading order + tables), the newer SOTA-for-size direction.
- **Current:** PaddleOCR-VL-1.6 shipped in **v3.6.0 (2026-05-28)**, **96.33 on
  OmniDocBench v1.6** (PaddleOCR-VL-1.5 scored 92.6 and reportedly beats GPT-4o-class
  on doc parsing). [Towards AI deep dive](https://pub.towardsai.net/paddleocr-vl-1-5-a-deep-dive-into-the-0-9b-model-that-outperforms-gpt-4o-on-document-parsing-c93bac97ac1f)
- **License:** **Apache 2.0.** [StableLearn guide](https://stable-learn.com/en/paddleocr-vl-introduction/)
- **VRAM:** ~3–4 GB practical with optimization (1–2 GB quantized); **but 40+ GB
  unoptimized** if you forget the flags. Fits the 3070 *with care*.
- **GPU req:** NVIDIA compute capability **≥ 8.0** recommended (RTX 30/40/50) — the
  3070 Laptop at **8.6 qualifies**. Officially supported on vLLM.
  [HF model card](https://huggingface.co/PaddlePaddle/PaddleOCR-VL)
- **Offline:** Yes (weights local). **ONNX path is immature** — it expects
  PaddlePaddle / vLLM, a heavier runtime than the classic pipeline.

**Why secondary:** Reserve for the *hard* frames the classic pipeline flags as
text-dense-but-low-confidence (a photographed document, a dense mail/letter, a
cluttered screen with structure). Running a 0.9B VLM over all 500 GB is wasteful and
slower; running it on the ~handful of document frames the cheap pass flags is the right
division of labor (mirrors TEST-FEEDBACK rec #3: spend the expensive model only on
flagged clips). **Phase 2**, not Phase 1.

### 2c. License plates — specialist note

`fast-plate-ocr` (MIT, ONNX Runtime, CPU+GPU on Windows, latest **v1.1.0 2026-03-14**,
~0.5 ms/plate on a 3090) is a *recognition-only* model — it expects an already-cropped
plate and "must be used after a plate object detector."
[fast-plate-ocr repo](https://github.com/ankandrew/fast-plate-ocr)

**Recommendation:** Do **not** add a plate-specific pipeline in v1. PP-OCRv5's general
detector+recognizer already reads plate text in-scene without a separate cropper, and a
plate without a detector is extra moving parts. Revisit only if plate recall proves
insufficient in testing — then add `fast-plate-ocr` behind the same interface as a
plate-confidence booster, fed crops from PP-OCRv5's boxes.

### 2d. Tesseract — verified, but NOT recommended as primary

Tesseract **5.5.2 (2025-12-26)**, still actively maintained (lead: Stefan Weil),
Apache-2.0, fully offline, CPU.
[Tesseract releases](https://github.com/tesseract-ocr/tesseract/releases)
It is reliable on **clean, axis-aligned, high-contrast document scans** but materially
worse than PP-OCRv5 on **scene text** — rotated signage, low-light storefronts,
perspective-skewed plates, screen glare — which is exactly the becky frame profile.
Keep it in mind only as a zero-GPU fallback or a cross-check ensemble member.

### Verdict
**PP-OCRv5 via ONNX Runtime as the primary engine (Phase 1); PaddleOCR-VL-1.6 as a
selective escalation for document/structured frames (Phase 2).** Both Apache 2.0, both
offline, both run on the 3070.

> **Could not fully verify:** PaddleOCR v3.6.0 release notes mention "official SDKs for
> Python, Go, and TypeScript." One source listed Go; the repo README fetch listed only
> C++/C#/Java/JS. **Do not assume a usable Go SDK exists** — design below treats OCR as
> a Python helper subprocess (the proven becky pattern), and an official Go binding, if
> real, becomes a nice optimization later. Confirm before relying on it.

---

## 3. Build decision: feature of `becky-osint` vs. new `becky-ocr` tool

**Recommendation: a NEW standalone tool, `becky-ocr`, that consumes frames — NOT a flag
bolted onto `becky-osint`.**

Rationale, grounded in the existing architecture and SKILL.md's one-tool-per-job design:
- `becky-osint`'s single, clean job is provenance-grade frame *export* (frame +
  SHA-256 + perceptual hash + sidecar). TEST-FEEDBACK explicitly praises this as "the
  strongest piece" — do not regress it by coupling a heavy Python OCR runtime into it.
- OCR is **pixel interpretation** — the repo's known soft spot (face detect, validate).
  Isolating it in its own tool keeps `becky-osint` deterministic/lightweight and lets
  OCR degrade gracefully (missing model → JSON note, exit 0), exactly like `becky-identify`'s
  face path and `becky-validate`'s avlm path already do.
- It composes: `becky-ocr` reads the `becky-osint` **manifest** (or any folder of
  frames/sidecars) and emits its own JSON, so it slots into `becky-pipeline` as a new
  `ocr` step after `osint` without touching the others.

So the chain becomes:
```
becky-events <video> ...                  → events.json
becky-osint  <video> --events events.json → osint manifest + frames + sidecars
becky-ocr    --manifest osint-manifest.json [--frames-dir <dir>]  → ocr.json   ← NEW
becky-embed / index ingests ocr.json text rows                    ← search integration
```

### Why frames, not the video again
`becky-osint` already exported the exact, hash-stamped frames with provenance. `becky-ocr`
runs on **those files** (chain of custody preserved: the OCR'd image is the same bytes
already SHA'd), never re-decoding the source video. Optional `--frames-dir` lets it OCR
any folder of images (e.g. dense-sample mode, or a one-off frame set).

### CRITICAL dependency: the rotation fix (shared with F1)
`TEST-FEEDBACK.md` F1 (CRITICAL): becky-exported frames are **sideways** because the
container display-rotation matrix isn't applied before sampling, so most portrait phone
video is 90°-rotated. **OCR on rotated frames fails as badly as face detect does.**
- `becky-ocr` MUST consume **rotation-corrected** frames. The cleanest fix is for the
  shared frame-export primitive (`internal/osintexport.ExtractFrame`) to apply the
  display rotation once, fixing F1 for face, OCR, and validate together.
- As a belt-and-suspenders fallback, `becky-ocr` should try the frame at 0/90/180/270°
  and keep the orientation with the highest mean recognition confidence (PP-OCRv5 has an
  angle classifier, but full-frame 90° rotation is beyond per-box angle correction).
- This is called out as a hard prerequisite, same as it is for clustering (SPEC 3).

---

## 4. Interface (CLI contract)

```
becky-ocr --manifest <osint-manifest.json> [options]
becky-ocr --frames-dir <dir> [options]

Options:
  --manifest <file>     becky-osint manifest JSON (preferred; carries provenance)
  --frames-dir <dir>    OR a directory of frame images (jpg/png) to OCR directly
  --engine ppocr|ppocr-vl|tesseract   default: ppocr (PP-OCRv5 via ONNX Runtime)
  --lang en             recognizer language (default en; PP-OCRv5 = 100+ langs)
  --min-confidence 0.5  drop boxes below this rec-confidence into a low_conf list
  --device cpu|cuda     default from ~/.becky/config.json (cpu)
  --max-frames N        cap frames OCR'd per run (corpus-scale guard)
  --output <file>       write JSON here instead of stdout
  --verbose             progress on stderr
```

Behavior:
- JSON-in/JSON-out, exit-coded, offline — identical contract style to every becky tool.
- **Graceful degrade:** missing PaddleOCR deps/models → `{"skipped": true, "reason": ...}`
  per-frame and a top-level note, **exit 0** (mirrors `face_embed.py` / avlm `DegradeError`).
- Heavy compute in an embedded Python helper (`ocr_paddle.py`) run via the existing
  `internal/pyhelpers.Materialize` pattern under the right `PYTHONPATH`; Go orchestrates
  + parses JSON, exactly like `internal/faceembed`.
- New config fields (additive, same style as `FaceModelRoot` etc.):
  `OCRPython`, `OCRPyLib`, `OCRModelRoot`, `OCREngine`.

### Output JSON contract (proposed; synthetic values)
```json
{
  "tool": "becky-ocr v1.0.0",
  "engine": "ppocr-v5-onnx",
  "source_manifest": "osint-export/osint-manifest.json",
  "frames_ocrd": 8,
  "results": [
    {
      "frame_path": "osint-export/scene_3s_frame90.jpg",
      "source_file": "20250704_181431.mp4",
      "source_sha256": "ab12cd34…",        // carried from the osint sidecar (provenance)
      "timestamp": 3.0,                      // seconds, from the osint sidecar
      "frame_index": 90,
      "rotation_applied": 90,                // orientation used (F1 transparency)
      "lines": [
        {"text": "2601 CHATHAM CIR", "confidence": 0.93,
         "bbox": [12, 40, 318, 96], "category": "candidate_address"},
        {"text": "BRAXTON'S BBQ", "confidence": 0.88,
         "bbox": [22, 110, 290, 164], "category": "candidate_business"}
      ],
      "low_confidence_lines": [
        {"text": "TX 7K?2", "confidence": 0.34, "bbox": [200, 300, 268, 332],
         "category": "candidate_plate"}
      ],
      "full_text": "2601 CHATHAM CIR\nBRAXTON'S BBQ"
    }
  ],
  "skipped": [],
  "notes": {}
}
```

### Light, honest categorization (NOT extraction-as-fact)
Tag lines with a cheap, regex/heuristic `category` to make the index searchable, while
staying candidate-not-conclusion per FORENSIC-OUTPUT-PHILOSOPHY:
- `candidate_address` — number + street suffix (St/Ave/Cir/Dr/Rd/Ln/Blvd…)
- `candidate_plate` — short alnum block matching US plate shapes (esp. TX patterns)
- `candidate_business` / `candidate_signage` — title-case multiword on signage boxes
- `candidate_timestamp` — burned-in date/time patterns (useful vs. F3 mtime distrust)
- `text` — everything else
These are **hints for ranking/search**, plainly labeled "candidate" — at the DETECTION
layer. They are NOT the final output.

Per `FORENSIC-OUTPUT-PHILOSOPHY.md` (TOP PRINCIPLE, 2026-06-08): becky-tools
**corroborate, then CONCLUDE** — they do not dump a pile of "maybes" for a human to
sort. A single OCR line is one weak signal, so on its own it stays a candidate. But
when an OCR read is **high-confidence AND corroborated** by other data points (fixtures
matching the same place, a vlog, a listing, a second frame, the metadata timeline), the
tool **ASSERTS the conclusion plainly** — "this is 2601 Chatham Cir" — with the
confidence score, the frame provenance, and the corroborating points attached. The
human reviews confident, corroborated findings; they do **not** do the tool's sorting.
So: don't conclude from one thin OCR line, but DO conclude — confidently, with the
address/name — when the read is high-confidence and the data points agree. (Concretely,
becky-ocr emits each read with its confidence and frame provenance, asserting
high-confidence reads in `lines` and flagging only genuinely shaky ones in
`low_confidence_lines`; corroboration across passes happens in the orchestrator/search
layer, which is where the confident conclusion is reached and stated.)

---

## 5. How results feed the index / search

becky already has the right substrate. The `beckydb` schema is FTS5 + sqlite-vec, and
`becky-search` does hybrid (BM25 keyword + dense KNN, fused with RRF). OCR text is a
**perfect fit for the keyword half** — the schema comment literally says dense KNN
"blurs exact tokens (names, dates, plates, addresses), so we also keep a literal index."
Addresses/plates/names are exact tokens → FTS5 is where they belong.

**Recommended integration (additive, no migration risk):**

1. **New table `ocr_text`** (one row per OCR'd line, keyed deterministically like the
   others: `sha12(source_file)+":"+frame_index+":"+line_ordinal`), columns:
   `ocr_id, source_file, source_sha256, frame_path, timestamp, frame_index, text,
   confidence, category, bbox_json, created_at` (created_at = RFC3339, matching the
   existing `segments.created_at` / `media_meta.ingested_at` convention in `schema.sql`).
   Plus an FTS5 mirror `ocr_text_fts(ocr_id UNINDEXED, text, tokenize='porter unicode61')`
   — same pattern as `segments_fts`. Index `source_file`, `category`.

2. **Writer:** either a small `--ingest-ocr` mode on `becky-consolidate`/`becky-embed`,
   or `becky-ocr --db <forensic.db>` writes directly (preferred: keeps the OCR tool
   self-contained, mirrors how `becky-embed` owns its writes). Idempotent via the
   deterministic `ocr_id` (INSERT OR REPLACE + DELETE/INSERT for FTS, like segments).

3. **Search:** extend `becky-search` to also query `ocr_text_fts` and fuse OCR hits into
   the same RRF ranking, tagged `source: ocr` with the frame_path + timestamp so a hit
   on "Chatham" returns *the frame to look at*. `becky find "2601 Chatham"` then surfaces
   the storefront/mail frame directly. (Dense embedding of OCR text is optional and lower
   value — addresses/plates want literal match, not semantic blur.)

4. **Pipeline step:** add `ocr` to `becky-pipeline --steps`, running after `osint`,
   writing `<videostem>/ocr.json` and (if `--db`) the `ocr_text` rows — resumable/skip-on-
   exists like every other step.

5. **Triage payoff (the 500 GB win):** an `ocr_text` table makes the corpus
   *rank-by-actionability* (TEST-FEEDBACK closing point): "show me every clip with a
   `candidate_address`," "every frame with TX-plate-shaped text," "every storefront name."
   That is how 500 GB of mostly-b-roll becomes triagible.

---

## 6. Performance / scale notes (honest)

- PP-OCRv5 **mobile** runs in well under a second/frame on CPU; with CUDA on the 3070,
  faster still. becky-osint exports only a handful of scene-change frames per clip
  (not every frame), so OCR cost is small relative to transcription/validation.
- **Warm-model server** matters at corpus scale: like F6 (insightface reloads) and the
  resident llama-server, the OCR helper should load the ONNX models **once per batch /
  resident process**, not per frame. Spec the helper to accept a batch of frame paths in
  one invocation (it already does, by design) and consider a persistent mode for the full
  500 GB sweep.
- PaddleOCR-VL escalation is the only GPU-heavy part; gate it (Phase 2) to flagged
  document frames only — never the whole corpus.

---

## 7. Open items to confirm before building
1. **Rotation fix (F1) lands first or in tandem** — OCR on sideways frames is worthless.
2. **PP-OCRv5 → ONNX export recipe** for v3.6.0 (det + cls + rec), to avoid pulling in
   the full PaddlePaddle runtime; confirm `onnxruntime` (CPU + CUDA) on this machine.
3. Whether the rumored **official PaddleOCR Go SDK** is real/usable (would let us skip
   the Python helper) — **do not assume; default to the Python-helper design.**
4. Exact `onnxruntime-gpu` ↔ CUDA/cuDNN version compatibility on the 3070 (becky already
   runs onnxruntime for insightface/sherpa — reuse that known-good combo).
5. TX-specific plate/address regex set for the `category` heuristics (cheap, high value).

---

## Sources (verified 2026-06-07)
- [PaddleOCR releases (v3.6.0, 2026-05-28)](https://github.com/PaddlePaddle/PaddleOCR/releases)
- [PaddleOCR repo — Apache 2.0, ONNX Runtime backend](https://github.com/PaddlePaddle/PaddleOCR)
- [PP-OCRv5 docs — sizes, VRAM, GPU/Windows](https://www.paddleocr.ai/latest/en/version3.x/algorithm/PP-OCRv5/PP-OCRv5.html)
- [PaddleOCR-VL HF model card — compute ≥8.0, vLLM](https://huggingface.co/PaddlePaddle/PaddleOCR-VL)
- [PaddleOCR-VL-1.5 deep dive — 92.6 OmniDocBench, Apache 2.0](https://pub.towardsai.net/paddleocr-vl-1-5-a-deep-dive-into-the-0-9b-model-that-outperforms-gpt-4o-on-document-parsing-c93bac97ac1f)
- [PaddleOCR-VL VRAM/optimization guide](https://stable-learn.com/en/paddleocr-vl-introduction/)
- [fast-plate-ocr (MIT, ONNX, v1.1.0 2026-03-14)](https://github.com/ankandrew/fast-plate-ocr)
- [Tesseract releases (5.5.2, 2025-12-26)](https://github.com/tesseract-ocr/tesseract/releases)
- [RapidOCR — Apache-2.0 ONNX-Runtime port of PaddleOCR PP-OCR (det+cls+rec)](https://github.com/RapidAI/RapidOCR)
- [rapidocr on PyPI (the v3.x package becky-ocr uses)](https://pypi.org/project/rapidocr/)

---

## Implementation status (BUILT 2026-06-08)

**Tool:** `becky-ocr` — `becky-go/cmd/ocr/` (main.go, ocr.go, categorize.go, ocr_paddle.py).
Storage: `becky-go/internal/beckydb/ocr.go` (self-contained `ocr_text` table + FTS5
mirror `ocr_text_fts`, additive — does not touch the canonical schema).

**Verified OCR engine:** PaddleOCR **PP-OCRv5** (det `ch_PP-OCRv5_det_mobile` + EN rec
`en_PP-OCRv5_rec_mobile`) on **ONNX Runtime 1.26.0 (CPU)**, via the **`rapidocr` 3.8.1**
package (Apache-2.0). Deps (`rapidocr`, `onnxruntime`, `opencv` 4.13) live in the SAME
`--target` site-packages dir the face stack uses (`config.FacePyLib`), run under
`config.FacePython` (anaconda) with `PYTHONPATH` set — exactly the `internal/faceembed`
pattern. RapidOCR **bundles PP-OCRv4 ONNX models** (fully offline, no download); the
PP-OCRv5 weights (~12 MB det+rec) download once from ModelScope then run air-gapped.
If v5 can't be fetched, the helper falls back to bundled v4 and labels the output
`ppocr-v4-onnx`. CUDA was not wired (this onnxruntime install is CPU-only); PP-OCRv5
mobile runs in well under a second/frame on CPU, which is fine for becky's scene-change
frame counts. GPU is a drop-in later (install `onnxruntime-gpu`).

**Real-frame verification:** OCR'd real CLANCY frames. A YouTube-livestream chat frame
read `@Kadeofspades ya cat is gonna be mine, imma steal it` at **0.99**,
`Message deleted by @AshStoleYourToast` at **1.00**; a police-report frame read
`GREENWOOD POLICE DEPARTMENT` and `Greenwood IN 46143` at **1.00**. `becky find`
(FTS5/BM25 over `ocr_text`) returns the exact frame for queries like `"steal cat"` and
`"Greenwood police"`. On thin b-roll (test.mp4: a person indoors, no signage) PP-OCRv5
correctly returns ~nothing — confirming the spec's thesis that actionable text lives in
signage/screens/documents, not dog-walk b-roll.

**Honest limits:** (1) Natural-scene hard text (faint face tattoos, skewed signage,
plates) is unreliable — those reads land in `low_confidence_lines`, as intended.
(2) The detector sometimes splits one physical sign across boxes (e.g. "186" / "Surina"
/ "Way" as separate lines), so the single-line `candidate_address` regex can miss a
split address; multi-box line assembly is a Phase-2 refinement. (3) PaddleOCR-VL
(Phase 2, the document-VLM escalation) is NOT built — v1 is the classic det+rec pipeline
only. (4) Per-frame `--try-rotations` (0/90/180/270, keep best-confidence) is the
sideways-frame fallback; the durable F1 fix still belongs in `osintexport.ExtractFrame`.

**Interface delta from the design above:** `--lang`, `--device`, and the `ppocr-vl`
engine option were not implemented in v1 (English-only via PP-OCRv5 EN rec; CPU). The
shipped flags are `--manifest | --frames-dir`, `--engine ppocr|ppocr-v4`,
`--min-confidence`, `--try-rotations`, `--max-frames`, `--db`, `--output`, `--verbose`.
