# TTS engine research — which voice becky should adopt to read things aloud

**Status: corrected to v3, 2026-06-22.** v1 (Orpheus-3B) was article-based and stale. v2
(Qwen3-TTS-1.7B) picked on HF adoption — a safer default, still not real research. v3 does the
actual task: define the right MODEL CLASS, survey the current field within it, and use leaderboards
as a verification step. Conclusion: **NeuTTS Air.**

## The class that matters (the real insight)
Good small TTS models have an **LLM baked into the backbone** → expressive, context-aware,
"intelligent" prosody. Kokoro (82M, no LLM) is light but flat — hence Jordan's rejection. Heavy
LLM-TTS (3B+) sound great but are **too slow to be useful**. The target is the intersection:
**tiny + LLM-backbone + expressive + fast.** Leaderboards verify a shortlist; they don't make it
(arena #1 was Kokoro, which Jordan hates).

## Constraints (from Jordan — settled)
Local/offline on a Windows RTX 3070; expressive + FAST; NOT Microsoft SAPI/Narrator; not Piper
(deprecated) or Kokoro (flat); permissive license; right-sized (3B too slow); becky-integration =
a local binary/helper with degrade-never-crash (absent model → print text, never SAPI).

## Field within the class (live HF + web, 2026-06-22)
| Model | Params | Backbone | License | Local | Notes |
|---|---|---|---|---|---|
| **neuphonic/neutts-air** | **747.9M** | **qwen2 (0.5B-class LLM)** | **Apache-2.0** | **GGUF q4/q8 + NeuCodec** | **CHOSEN.** On-device, real-time, expressive, instant voice clone. English. 874 likes. |
| ResembleAI Chatterbox-Turbo | ~350M | Llama-class | **MIT** | yes | Emotion-exaggeration control; beats ElevenLabs in side-by-sides. Strong alternate. |
| neuphonic/neutts-nano | 228.7M | llama | **other** (restrictive — verify) | GGUF | Ultra-light; license is the catch. |
| Qwen/Qwen3-TTS-12Hz | 0.6B / 1.9B | qwen3 | Apache-2.0 | GGUF + qwen3-tts.cpp | Heavier; multilingual; keep as fallback for a different voice. |
| hexgrad/Kokoro-82M | 82M | none (StyleTTS2) | Apache | ONNX | **Rejected** — light but flat (no LLM). |

## Recommendation
**Primary: NeuTTS Air** — the tiny+intelligent sweet spot: 0.75B Qwen2-LLM backbone (expressive),
Apache-2.0, GGUF (on-device/real-time), instant voice cloning. **Try-by-ear alternate:
Chatterbox-Turbo** (350M, MIT, emotion control). **Max-speed bench option: NeuTTS Nano** (228M;
license:other — verify terms). **Heavier fallback: Qwen3-TTS** (0.6B/1.7B, Apache) for a different
timbre or multilingual. Final judge = Jordan's ears (SPEC §8.2).

## Leaderboard caveat (verification, not selector)
TTS-Arena-V2's top is cloud APIs (Inworld/Hume/Papla/Vocu/ElevenLabs) — off-limits offline. The
open-model arena (`Pendrokar/TTS-Spaces-Arena`) is where small local models like NeuTTS surface;
use it to sanity-check the shortlist. The leaderboard verifies; it does not research.

## Confidence
High on class fit / size / license / offline feasibility (verified on the live hub + field reviews).
Medium on "Jordan likes this exact voice" — hence the mandatory hear-it gate, with named alternates.

## Lesson logged
A model choice = (1) identify the right model CLASS, (2) survey the current field within it on the
live hub, (3) verify against the leaderboard, (4) let the human judge by ear. NOT: read one article,
or grab the most-downloaded name. v1/v2 of this file both skipped step 1.

## Sources
- `hf.co/neuphonic/neutts-air`, `…-q4-gguf`, `…-q8-gguf`; `hf.co/neuphonic/neutts-nano`; `github.com/neuphonic/neutts`
- `hf.co/ResembleAI/chatterbox`; `hf.co/Qwen/Qwen3-TTS-12Hz-1.7B-CustomVoice`
- getstream.io/blog/best-on-device-tts-models; bentoml.com/blog (open-source TTS 2026)
- `hf.co/spaces/TTS-AGI/TTS-Arena-V2`, `hf.co/spaces/Pendrokar/TTS-Spaces-Arena`
