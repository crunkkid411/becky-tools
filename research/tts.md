# TTS engine research — which voice becky should adopt to read things aloud

**Status: corrected 2026-06-22.** The first pass leaned on stale articles and recommended
Orpheus-3B (a 2024-era pick). This version is grounded in a LIVE Hugging Face hub re-check
done the same day, after Jordan pointed out (with the current TTS-Arena-V2 leaderboard) that
the original shortlist no longer reflects the field, 3B is heavy for TTS now, and Qwen's TTS
(missed entirely) is the obvious local candidate.

## Constraints (from Jordan — settled)
- Local / offline, runs on a Windows RTX 3070 (~8 GB). Quality matters (he listens).
- NOT Microsoft SAPI/Narrator (hard no). Piper deprecated. Kokoro rejected by ear.
- Prefer permissive license (Apache/MIT) and a RIGHT-SIZED model — 3B is heavier than needed.
- Integration must fit becky: a local binary becky shells out to (GGUF/llama-style) or a Python
  helper (`internal/pyhelpers/`), with degrade-never-crash (absent model -> print text, never SAPI).

## Method
Live HF hub queries this session (not an article): trending `text-to-speech` models, a `Qwen3-TTS
GGUF` search, and repo detail lookups for the front-runners. Corroborate-then-conclude on
license + size + adoption + offline feasibility. Voice naturalness is judged by Jordan's ears
(final gate), not a leaderboard — see "Leaderboard caveat".

## Current local TTS field (HF trending, 2026-06-22) — the real list
| Model | Params | License | Local path | Adoption | Notes |
|---|---|---|---|---|---|
| **Qwen/Qwen3-TTS-12Hz-1.7B-CustomVoice** | ~1.9B | **Apache-2.0** | **GGUF + qwen3-tts.cpp**, or transformers | **7.6M dl, 1.6k likes** | **CHOSEN.** Right-sized, permissive, GGUF-ready, most-adopted. 0.6B variant exists. |
| microsoft/VibeVoice-1.5B | 1.5B | MIT | transformers | 239k dl, 2.4k likes | Open model (NOT SAPI); podcast/expressive. Optics only. |
| openbmb/VoxCPM2 | — | — | voxcpm | 499k dl, 1.4k likes | Diffusion, voice clone/design. |
| Supertone/supertonic-3 | — | openrail | **ONNX, on-device** | 96k dl, 846 likes | Easiest pure-runtime integration (no torch). |
| ResembleAI/chatterbox | — | MIT | PyTorch | 2.1M dl, 1.6k likes | Beats ElevenLabs in maker blind test; no first-class GGUF. |
| FunAudioLLM/Fun-CosyVoice3-0.5B | 0.5B | Apache | onnx/safetensors | 27k dl | Tiny alternative. |
| hexgrad/Kokoro-82M | 82M | Apache | ONNX | 16.6M dl | **Rejected by Jordan (quality).** |

## Recommendation
**Primary: Qwen3-TTS-12Hz-1.7B-CustomVoice (Apache-2.0).** Best fit on becky's axes — offline
(community GGUF: `cstr/qwen3-tts-1.7b-customvoice-GGUF`, `Serveurperso/Qwen3-TTS-GGUF`; codec
`cstr/qwen3-tts-tokenizer-12hz-GGUF`; runtime `qwen3-tts.cpp`), permissive, ~half Orpheus's size,
and by far the most-adopted current open TTS. A **0.6B** CustomVoice exists if speed/VRAM bites.
**Try-next if Jordan dislikes the voice by ear:** VibeVoice-1.5B (MIT), VoxCPM2, supertonic-3
(ONNX), Chatterbox (MIT).

## Leaderboard caveat (why the arena rank doesn't decide this)
TTS-Arena-V2's top (Inworld TTS MAX/Preliminary, Hume Octave, Papla, Vocu, ElevenLabs) is almost
all **cloud/proprietary APIs** — disqualified by the offline rule. And the arena is a poor proxy
for Jordan: Kokoro was arena #1 and he rejected it. So selection = local + permissive + right-sized
+ adopted; the final judge is Jordan hearing it (SPEC-BECKY-TTS.md §8.2).

## Confidence
High on fit/license/size/offline-feasibility (verified on the live hub). Medium on "Jordan will
like this specific voice" — unknowable without his ears, hence the mandatory hear-it gate.

## Honesty note
The earlier Orpheus-3B recommendation in this file's first version was shallow (article-based,
stale, oversized, missed Qwen). It is superseded. Lesson logged in CLAUDE.md: a model choice goes
through a LIVE re-check of the current field, not a single article.
