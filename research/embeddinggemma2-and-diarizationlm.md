# EmbeddingGemma 2 for qmd, and DiarizationLM for speakers (2026-10-10, local)

Jordan asked (2026-10-10): switch qmd (and anything else on EmbeddingGemma) to EmbeddingGemma 2,
GGUF preferred, note when Google recommends pairing with Gemma 4, and add Google's
DiarizationLM-Gemma-4-E4B to becky if it covers something the speaker pass lacks.

## EmbeddingGemma 2 - measured, NOT switched

- Model: `google/embeddinggemma-2` (Apache-2.0, 768 dims, Matryoshka down to 128, 8192-token input,
  text + picture + sound in one space). GGUF: `ggml-org/embeddinggemma-2-GGUF` Q8_0 (310 MB). Same
  task prompts as v1 (`task: search result | query: ` / `title: ... | text: `).
- Gemma 4 note from Google's guide: "When paired with Gemma 4 in an on-device RAG pipeline, both
  models share the same text tokenizer and audio encoder architecture, reducing the total memory
  footprint." That saving needs one runtime sharing the weights (Google's on-device stack); with
  llama.cpp they are two separate files, so nothing is shared here.
- Users on this PC: qmd (CLI, MCP, becky's `internal/qmd`) and the dormant `X:\AI-2\moltbot`.
  becky-embed/becky-search stay on Qwen3-Embedding-4B (decided earlier: higher retrieval).
- qmd's bundled engine (node-llama-cpp 3.18.1 = llama.cpp b8390) cannot load it ("unknown model
  architecture gemma-embedding2", support merged in llama.cpp 2026-10-06). Built a Vulkan engine
  from llama.cpp b11541 in `C:\nq` (node-llama-cpp 3.22.1 + a 12-line shim for the removed
  `common_batch_add`). Checked against Google's ONNX build: cosine 0.9999-1.0000 on 6 texts, so the
  GGUF is faithful.
- **Result on Jordan's own notes** (231 becky docs/research files, 1381 chunks, 20 questions with a
  known right file, qmd's prompt format):

  | Model | right file 1st | in top 5 | MRR | embed time |
  |---|---|---|---|---|
  | EmbeddingGemma 300M (current) | 13/20 | 17/20 | 0.743 | 70 s |
  | EmbeddingGemma 2 (270M text) | 12/20 | 14/20 | 0.664 | 117 s |

  Same order with `title: none` documents (0.616 vs 0.569). So for qmd's text search v2 is slightly
  worse and slower; qmd stays on v1. v2's real gain is searching pictures/sound by meaning, which
  qmd (markdown only) cannot use. Revisit if becky ever needs "find the frame/sound that matches
  this sentence".

## DiarizationLM - built as becky-diarfix

- Model: `DiarizationLM-Gemma-4-E4B-v1` Q4_K_M GGUF (5.3 GB). Google's repo now 404s; the
  `diarizers-community` mirror has the same card and files (`scripts/get-diarizationlm.ps1`,
  pinned revision + sha256). Paper numbers (word speaker error): Fisher 5.32 -> 2.99, Callhome
  7.74 -> 4.92, ICSI/AMI ~1 point.
- Strength we lacked: Nemotron decides speakers from SOUND only. DiarizationLM reads the WORDS and
  fixes turn edges ("...doing today? | I am" split one word late) and short backchannels.
- How becky uses it: `becky-transcribe --diarize` -> becky-diarfix on the words within 5 of a
  speaker change only (the unsure ones); everything else keeps Nemotron's label; it can only move a
  word between speakers that already exist; a reply that rewrites more than 10% of the words is
  ignored. Port of the official transcript-preserving speaker transfer.
- Measured on the 9-min 3-person "Fast Food Test" video: 4 prompts, all trusted, 1300 unsure words,
  42 moved, +2m52s (about a third of the video's length; total run 4m40s). Hand check of 27 moves:
  most clearly right (a sentence's first word - "I", "What", "Um," - moved to its own speaker), one
  clearly wrong (189 s: the answer "Yes, it is." merged into the asker), ~8 unclear from text alone.
