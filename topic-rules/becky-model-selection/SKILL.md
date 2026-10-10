---
name: becky-model-selection
description: "How to choose an AI model for becky-tools or Whoretana: research the model class, check the live Hugging Face hub and the model card, verify with a leaderboard, let Jordan judge. Use when picking a TTS, ASR, vision or LLM model."
---

# Choosing a model

Moved verbatim out of the always-loaded CLAUDE.md files on 2026-10-10 so it is read only when this kind
of work comes up. It has the same authority as CLAUDE.md.

- **Model choice = research a CLASS, then verify — never one article or the top download.** Pick the
  right model FAMILY first (e.g. TTS: tiny + LLM-backbone + fast; Kokoro is light-but-flat, 3B is
  too slow), survey the CURRENT field live (HF hub + the model's real card: params/license/GGUF), use
  a leaderboard only to VERIFY the shortlist, and end on the human's judgement (Jordan HEARS the TTS).
  The TTS pick was botched twice (stale-article Orpheus-3B, then most-downloaded Qwen) before this
  method produced NeuTTS Air — don't repeat the shortcut. Canon: `SPEC-BECKY-TTS.md` / `research/tts.md`.
