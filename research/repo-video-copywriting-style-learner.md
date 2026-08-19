# redpanda12138/video-copywriting-style-learner

Source: https://github.com/redpanda12138/video-copywriting-style-learner | licence: MIT

Read for `shorts-user-feedback.md`: what step does this run that becky does not?
Written by a free model reading the source; the build/skip judgement is not its call.

---

## What it is
A Codex/Claude Code agent skill that ingests video links (Xiaohongshu, Bilibili, Xiaoyuzhou), local media, or transcripts to extract linguistic evidence, incrementally builds per-author "Writing DNA" style profiles, and then generates original copy for new videos using the learned style rules. It does not perform shot-by-shot video editing; it focuses on language patterns, syntax rhythm, vocabulary tone, and reusable writing rules.

## The pipeline, in order
1. **Content acquisition** – Delegated to external `agent-reach` tooling (OpenCLI for Xiaohongshu/Bilibili, `ffmpeg` + Groq Whisper for Xiaoyuzhou). Not implemented in this repo.
2. **Evidence classification** – Labels each source as official subtitles, auto-transcription, OCR, user transcript, or semantic summary; records evidence strength and completeness. (Described in README usage flow step 2; implementation not visible in files read)
3. **Immutable source record creation** – Stores each ingestion as a deduplicated source file keyed by URL + media hash under `sources/<source-id>.json`. (README data structure; implementation not visible in files read)
4. **Linguistic feature extraction** – Derives language features (style, syntax, rhythm, vocabulary, stance) from the evidence. (README step 4; implementation not visible in files read)
5. **Profile aggregation** – Rebuilds the author profile (`profile.json`) from all historical sources after each new ingestion. (README step 5; implementation not visible in files read)
6. **Readiness check** – `scripts/style_store.py readiness` verifies ≥20 complete reliable text sources, ≥80% metadata coverage, ≥3 content categories. (README 深度蒸馏 section)
7. **Distillation (L1–L6)** – `scripts/style_store.py distill` produces versioned Writing DNA artifacts (`Writing-DNA_*.md`, `语言DNA_*.md`, `文案结构模板_*.md`, `选题与认知框架_*.md`, `视听风格指南_*.md`) plus a manifest. (README 数据结构 & 深度蒸馏)
8. **New video fact extraction** – Identifies people, scenes, actions, objects, on-screen text, chronology, explicit selling points; marks uncertainties. (README 完整流程二 step 2.1–2.2; implementation not visible in files read)
9. **Style constraint mapping** – Reads `profile.json` and latest `Writing-DNA_*.md`; converts stable rules into narrative perspective, sentence length, rhythm, emotion curve, language actions. (README step 2.3–2.4; implementation not visible in files read)
10. **Copy generation** – Produces original copy for the new video using mapped constraints. (README step 2.5; implementation not visible in files read)
11. **Originality check** – Mechanical contiguous-text overlap detection against historical reliable corpus. (README step 2.5 & 隐私与原创性; implementation not visible in files read)
12. **Draft persistence** – Saves candidate to `drafts/候选文案_YYYYMMDD_HHmm.md` with `_02`, `_03` suffixes on collision. (README step 2.6)

## Models, libraries and services

| Component | Stage | Local / Paid API |
|-----------|-------|------------------|
| `agent-reach` (OpenCLI, bili-cli) | Content acquisition (1) | Local CLI tools (require Chrome extension + logged-in sessions) |
| `ffmpeg` | Audio extraction for Xiaoyuzhou (1) | Local binary |
| Groq Whisper API | Speech-to-text for Xiaoyuzhou / no-subtitle media (1) | Paid API (key via `agent-reach configure groq-key`) |
| Python 3.9+ stdlib | All runtime scripts (install, style_store) | Local |
| LLM (unspecified model) | Feature extraction, distillation, copy generation (4, 7, 10) | Not visible in files read – README says "执行 L1-L6 深度蒸馏" and "生成原创文案" but no model name or provider appears in the provided files |

## Prompts
No LLM prompt text is present in the provided files. The `agents/openai.yaml` only declares a default_prompt string ("Import these videos, assess readiness, and distill a versioned Writing DNA.") but does not contain the actual prompts used for extraction, distillation, or generation.

## How it decides WHAT to clip
Not applicable – this tool does not select or clip video segments. It learns writing style from full transcripts and generates new copy for a new video's facts.

## How it decides framing / cropping
Not applicable – no video reframing or cropping is performed.

## Multi-pass or iteration
- **Profile rebuild** runs after every new source ingestion (README: "每导入一个链接都会新增来源记录并重新聚合").
- **Distillation** can be run repeatedly as more samples arrive; each run emits a new timestamped version under `distillations/YYYYMMDD_HHmm/`.
- **Draft revision loop** – user can request rewrites ("保留事实…把前 3 秒钩子加强…修改后重新检查原创性"), triggering re-generation + originality re-check. (README 完整流程二 step 4)
- No automated self-critique or multi-pass refinement inside a single generation call is visible in the files read.

## Steps here that a transcript-first clipper would MISS
- Evidence-type grading (official subtitles > auto-transcription > OCR > summary) with explicit tagging per source.
- URL + media-hash deduplication before profile aggregation.
- Per-feature confidence lifecycle: provisional → stable → evolving → single-sample, updated incrementally.
- Hard readiness gates (≥20 sources, ≥80% metadata coverage, ≥3 categories) before formal distillation.
- Versioned, immutable distillation snapshots (Writing DNA + 4 companion docs + manifest) rather than overwriting a single profile.
- Mechanical contiguous-text overlap check against historical reliable corpus for originality verification.
- Draft versioning with timestamp + collision suffix (`_02`, `_03`) – never overwrites.
- Strict separation: new video facts never enter the learning corpus unless explicitly requested; generated copy is never used as training data.

## Worth stealing
1. **`scripts/style_store.py` CLI interface** – `readiness` and `distill` (with `--allow-provisional`) subcommands provide a clean contract for "is this profile ready?" and "produce versioned artifacts". (MIT licence per repo root)
2. **Immutable source-record schema** – `sources/<source-id>.json` keyed by URL + media hash, with evidence-type metadata. (README data structure)
3. **Distillation output bundle** – Five coordinated Markdown files + JSON manifest per version (`distillations/YYYYMMDD_HHmm/`). (README data structure)
4. **Draft persistence convention** – `drafts/候选文案_YYYYMMDD_HHmm[_NN].md` with explicit non-overwrite semantics. (README step 2.6)
5. **Readiness thresholds** – Concrete numbers (20 sources, 80% metadata, 3 categories) codified in CLI rather than hidden in prompts. (README 深度蒸馏)
6. **Evidence hierarchy** – Official subtitles > auto-transcription > OCR > user transcript > semantic summary, recorded per source. (README 功能 & 使用示例)
