# playlist-intake: the "ai-useful" playlist, read locally (2026-09-25)

**Bottom line:** `becky-intake` now reads Jordan's playlist with local models only. Laya (a System
One model) picks how to read each video, code vetoes risky choices, and a small embedding model says
which of his pain points the video most likely speaks to. Two test videos took 81 s total. The
downloaded audio lives in `TEMP\<video id>` and code deletes it every time; no model decides what
gets deleted.

## Files here

| File | What it is |
|---|---|
| `pains.json` | Jordan's pain points, one line each with where it came from. Edit freely; becky-intake matches against it. |
| `seen.json` | Video ids already turned into notes (never redone). Machine state, not committed. |
| `TEMP\` | Audio downloads during a run. Emptied by code; also swept at start-up. Not committed. |
| `qwen-skill\system-one-intake\` | The skill Qwen3.8-omni-flash (paid, one-time) extracted from GitHub Trending Weekly #50. |

## Qwen (paid, cloud) vs becky-intake (local)

| | Qwen omni-skill-creator | becky-intake |
|---|---|---|
| Watches the video frames | Yes | No. Audio + description + linked repos only |
| Cost | Paid per call | Free |
| Coverage on #50 (15 min) | 0-620 s (23 of 35 repos). Whole-video call hit a buffer overflow; the last chunk timed out twice | All 35 repos, from their GitHub pages |
| Time | Several minutes per chunk, plus the failures | About 1 minute per video on the links route |
| Output | A reusable skill (SKILL.md) | One Obsidian note per video: route, why you probably saved it, repos ranked by pain match, steps if it is a tutorial |
| Decides how to read each video | No, same pipeline for everything | Yes: links route or speech route |

What Qwen did better: it sees the screen. For on-screen-only videos (music plus text, like
"Introducing Aside") becky-intake currently has nothing to read and says so in the note. Adding
on-screen text reading (becky-ocr or a local vision model on a few frames) is the next gap.

## Why Laya, and the models over 8 GB (write-up, nothing downloaded)

| Model | Size | Why not (now) |
|---|---|---|
| Laya (receptron/laya-onnx) | 1.7 GB ONNX, runs on CPU | **Chosen.** Open Jev-compatible, 0 VRAM, ~0.5 s per call. |
| Bespoke Nimble 9B (open Jev on Qwen3.5-9B) | ~18 GB unquantized | Over the 8 GB rule. Scores 90% agreement with Jev vs Laya's lower, but a quantized GGUF would still need ~5-6 GB of the GPU that Whoretana shares. Worth it only if Laya's routing proves wrong in practice. |
| fast-browser-use (Qwen3.5-9B, MLX) | 9B, Apple-only MLX | Mac only and too big. The idea (pick only from controls that exist) is adopted instead. |
| reflex, jeff | small | Alternative open System One models; not tested. Laya already passed its selftest here. |

## Ideas from the video, adopted

- **Model decides, code computes** (shapeshift): code measures length/links/repos; Laya only judges.
- **Decision layer vs policy layer** (JevRouter): Laya's answer passes through a code guard
  (`guardRoute`): "links" needs 3+ repos AND 70%+ confidence, otherwise transcribe.
- **Legal option set** (arc-cua, fast-browser-use): repos come from the description, never invented.

## Measured limits (so nobody re-tests them)

- Laya on "is this repo useful?" said yes to nearly everything (AUC 0.45-0.59, three designs).
  Embedding similarity to pains.json: AUC 0.64 on the 35 hand-labelled repos. Neither is a verdict;
  the note calls the match "a pointer to what to look at first".
- Embeddings: Qwen3-Embedding-0.6B Q8_0 GGUF on llama.cpp, CPU: 4.5 s for 50 texts. The old
  in-process sentence-transformers path took 466 s for the same work, same AUC.
- Laya's ONNX fails on DirectML (Reshape node), so it runs on CPU.

## Next step (proposed, not built)

pains.json was written by hand from CLAUDE.md files and one chat. The goal Jordan described is a
local miner (LFM2.5 or Gemma, with guardrails) that refreshes it from his chat logs and computer-use
logs, so "why you saved it" gets personal. That needs his go-ahead on which logs it may read.
