# TTS engine research — which voice becky should adopt to read things aloud

**Status: research complete, recommendation made. Authored 2026-06-22 via becky's deep-research
protocol (`STANDARDS-MUSIC-RESEARCH.md` corroborate-then-conclude; ≥2 independent sources before a
claim).** This is the cited evidence behind `SPEC-BECKY-TTS.md`. Re-verify any model id / license
clause / llama.cpp flag past this date before building — the TTS field moves monthly.

> **WHO this is for (read `ACCESSIBILITY.md`):** Jordan is **sighted with impaired vision**. He does
> NOT use a screen reader. He wants becky to **read results aloud in a good, natural voice** so he can
> rest his eyes (forensic summaries, transcript-search answers, "done" notices). Therefore
> **NATURALNESS is criterion #1 — a robotic voice is a regression even if it "adds accessibility."**

## 0. The bottom line (corroborated)

**Adopt Orpheus-3B (Apache-2.0) as becky's primary TTS, served on the llama.cpp `llama-server`
transport becky already runs, with a thin SNAC-decoder Python helper. Keep Chatterbox (MIT, Python/
torch) as the documented fallback/upgrade if Jordan's ear prefers it on his hardware.** Reasoning:

1. **It clears Jordan's quality bar by reputation AND is the cleanest fit for becky's stack.** Orpheus
   is a Llama-3.2-3B fine-tune that emits SNAC audio tokens; it is widely reported as one of the most
   *human-like, emotional* open models — "outperforms existing high-end closed-source alternatives" on
   intonation/emotion in its own materials, and grouped with the top-quality open models (Kokoro, CSM,
   F5) in independent 12-model comparisons. ([canopyai/Orpheus-TTS](https://github.com/canopyai/Orpheus-TTS),
   [inferless 12-model compare](https://www.inferless.com/learn/comparing-different-text-to-speech---tts--models-part-2))
2. **License is clean for commercial use: Apache-2.0.** Most of the *highest-ranked* open-weight
   models on the leaderboards are **non-commercial** (Fish/OpenAudio CC-BY-NC, F5-TTS CC-BY-NC, XTTS-v2
   CPML) — disqualifying for becky. Orpheus (Apache-2.0) and Chatterbox (MIT) are the two strong
   **permissive** options. ([promptquorum license survey](https://www.promptquorum.com/power-local-llm/local-tts-voice-cloning-piper-coqui-xtts),
   [unsloth Orpheus GGUF](https://huggingface.co/unsloth/orpheus-3b-0.1-ft-GGUF))
3. **It drops onto becky's existing infrastructure with near-zero new machinery.** Orpheus ships
   first-class **GGUF** quants and runs on **llama.cpp / `llama-server`** — the *exact* transport
   `internal/avlm` already uses for Gemma-4. Q4_K_M is ~2 GB; it fits the 8 GB RTX 3070 with huge
   headroom, GPU-offloaded. The only new piece is a small SNAC decoder (tokens → 24 kHz WAV), which is
   a tiny dependency, not a torch stack. ([unsloth GGUF sizes](https://huggingface.co/unsloth/orpheus-3b-0.1-ft-GGUF),
   [orpheus-tts-local](https://github.com/isaiahbjork/orpheus-tts-local))
4. **It's deterministic-friendly and has stable preset voices** (`tara, leah, jess, leo, dan, mia, zac,
   zoe`) — no voice-cloning step required, fixed seed + greedy/low-temp decode gives reproducible
   output, which suits becky's invariant. ([Orpheus GitHub](https://github.com/canopyai/Orpheus-TTS))

**Confidence: medium-high on the *fit/license/feasibility*; medium on *"Jordan will love the voice"*
— which no amount of research can settle for him.** Voice preference is subjective and Jordan has
already rejected a leaderboard #1 (Kokoro) as sounding "like ass" (see §2). The spec therefore makes
**hearing it on his hardware the mandatory final gate**, and names a concrete fallback.

---

## 1. The hard constraints (settled by Jordan — not relitigated)

- **Naturalness is #1.** Robotic = unacceptable.
- **Ruled OUT:** **Piper** (deprecated) and **Kokoro** (Jordan: "sounds like ass"). Mentioned below
  only as rejected baselines.
- **No Microsoft TTS — ever** (SAPI / Narrator / built-in Windows voice). Not even as a fallback.
- **Offline-first + deterministic**, local on his Windows PC (**RTX 3070, ~8 GB VRAM**; he already runs
  llama.cpp + Python helpers). A cloud API is acceptable only with a strong explicit case + an
  offline-invariant flag.
- **License:** prefer permissive (MIT/Apache/BSD); flag restrictive (CC-BY-NC, CPML, research-only).
- **Integration:** a thin local helper (Python in `internal/pyhelpers`, or a binary/ONNX/GGUF) a Go
  `becky-tts` CLI calls; **text in → WAV out** (+ optional playback); **degrade-never-crash → fall
  back to PRINTING TEXT, never to Microsoft TTS.**

---

## 2. The leaderboard trap (why "the #1 model" is the wrong question)

The community leaderboards measure *crowd preference on single short utterances*, which does **not**
predict one specific listener's comfort over long forensic readouts. Two corroborating facts make this
concrete:

- **Kokoro-82M reached #1 / top of TTS Arena** in early 2026 — and Jordan still rejected it outright.
  ([texttolab](https://texttolab.com/blog/kokoro-tts-review),
  [TTS Arena V2](https://huggingface.co/spaces/TTS-AGI/TTS-Arena-V2))
- **Chatterbox sits at only ~#52 on TTS Arena V2 (Elo ~1006)** yet Resemble's own blind study had
  **65.3% of listeners prefer it over ElevenLabs**. ([offlinetts arena 2026](https://www.offlinetts.com/blog/tts-arena-leaderboard-2026/),
  [resemble-ai/chatterbox](https://github.com/resemble-ai/chatterbox))

**Conclusion:** use leaderboards/blind-tests to build a *shortlist of credible, permissive, local-
feasible* engines, then let **Jordan's ear pick the winner** on his own hardware. That is exactly how
the spec is structured.

The current top of the open-weight arena (for context, not as the pick — most are non-commercial):
Fish Audio S2 Pro ~#11 (Elo 1128.7), Step Audio EditX ~#16, NVIDIA Magpie 357M ~#26, Kokoro 82M ~#32,
Mistral Voxtral TTS ~#33, Maya1 ~#35, Chatterbox ~#52, XTTS-v2 ~#66, StyleTTS2 ~#67.
([offlinetts arena 2026](https://www.offlinetts.com/blog/tts-arena-leaderboard-2026/))

---

## 3. The contenders, scored against becky's criteria

Criteria: **Naturalness** (the #1), **License**, **Local on 8 GB 3070**, **Reuses becky's llama.cpp
transport (GGUF)?**, **Windows**, **Determinism**, **Maintenance/maturity**.

### Top 5 for becky (permissive + local + credible quality)

| Engine | Params | Naturalness (evidence) | License | 8 GB 3070? | GGUF/llama.cpp fit | Voices | Verdict for becky |
|---|---|---|---|---|---|---|---|
| **Orpheus-3B** (Canopy/Canopy Labs) | 3B (Llama-3.2-3B + SNAC) | High — "human-like… emotion, rhythm"; grouped with top-quality open models in independent compares | **Apache-2.0** | **Yes** — Q4_K_M ~2 GB, big headroom | **Yes — first-class GGUF on `llama-server`** (becky's exact path) + tiny SNAC decoder | 8 stable presets (tara/leah/…); optional zero-shot clone | **PRIMARY** |
| **Chatterbox** (Resemble AI) | 0.5B (Turbo 350M) | Very high — 65.3% beat ElevenLabs in maker blind study; "first local model that stopped sounding synthetic" to reviewers | **MIT** | **Yes** — ~5–7 GB typical; Turbo lighter | **No** — PyTorch/`transformers`, not GGUF; separate helper | Built-ins + zero-shot clone | **FALLBACK** (best if his ear prefers it; heavier dep) |
| **Maya1** (Maya Research) | 3B (Llama-style + SNAC) | High + 20+ emotion tags, natural-language *voice design* | **Apache-2.0** | Official path wants **16 GB**/vLLM; community **GGUF** exists for llama.cpp/CPU | **Partial** — community GGUF, same SNAC-decoder shape as Orpheus | Voice-design prompt (less deterministic) | **Close alt** — pick over Orpheus only if Jordan wants emotion/voice-design and GGUF proves stable |
| **Sesame CSM-1B** | 1B | Very high — blind testers "could not distinguish from human" on short snippets | **Apache-2.0** | **Yes** (~1B) | **No** native GGUF path; PyTorch | Conversational/contextual; weaker as a plain reader | Watch-list — built for dialogue, not one-shot readouts |
| **Dia-1.6B** (Nari Labs) | 1.6B | High, "ultra-realistic dialogue" | **Apache-2.0** | **Yes** | **No** GGUF; PyTorch | Dialogue-oriented | Watch-list — dialogue focus, overkill for becky's readouts |

### Rejected / flagged (with the reason — do not re-propose)

- **Kokoro-82M** — **rejected by Jordan** ("sounds like ass"), despite a #1 arena run. Apache-2.0,
  tiny, English-only, no cloning. Baseline only. ([hexgrad/Kokoro-82M](https://huggingface.co/hexgrad/Kokoro-82M))
- **Piper** — **rejected by Jordan** (deprecated). Baseline only.
- **Microsoft SAPI / Narrator** — **banned by Jordan**. Never, not even as fallback.
- **Fish Speech / OpenAudio S1 / S2 Pro** — top arena open-weight quality, **but CC-BY-NC-SA-4.0
  (non-commercial)** → disqualified for becky as shipped software. ([promptquorum](https://www.promptquorum.com/power-local-llm/local-tts-voice-cloning-piper-coqui-xtts),
  [fishaudio/fish-speech](https://github.com/fishaudio/fish-speech))
- **F5-TTS / E2-TTS** — **CC-BY-NC-4.0 (non-commercial)** → flagged out. ([promptquorum](https://www.promptquorum.com/power-local-llm/local-tts-voice-cloning-piper-coqui-xtts))
- **XTTS-v2 (Coqui)** — **CPML, non-commercial**, and **Coqui is defunct** (no one to sell a license) →
  unmaintained + restrictive. Flagged out. ([promptquorum](https://www.promptquorum.com/power-local-llm/local-tts-voice-cloning-piper-coqui-xtts))
- **StyleTTS2** — capable but mid arena (~#67) and older; no GGUF; not worth the integration over
  Orpheus/Chatterbox.
- **Higgs Audio v2** — **v2 is Apache-2.0** and strong, **but v3 moved to a research/non-commercial
  license** — license drift risk; 3B, heavier path. Watch, don't lead with it. ([boson-ai/higgs-audio](https://github.com/boson-ai/higgs-audio))
- **Kitten-TTS** — 15–80M, **CPU-only, 25 MB**, Apache-class. Impressively tiny but **a small model is
  exactly the "good-enough-not-great" tier Jordan already rejected** (Kokoro is 82M). Note as a
  no-GPU emergency tier only, not the voice he'll listen to. ([KittenML/KittenTTS](https://github.com/KittenML/KittenTTS))
- **IndexTTS-2 / 2.5** (Bilibili) — excellent emotional/duration control, but heavier and check the
  license per release; not a llama.cpp-native fit. Watch. ([IndexTeam/IndexTTS-2](https://huggingface.co/IndexTeam/IndexTTS-2))
- **GPT-SoVITS / MaskGCT / Llasa / Spark-TTS / OuteTTS / Parler-TTS / Zonos** — surveyed; each either
  (a) carries restrictive/uncertain licensing, (b) is clone-first/needs reference audio (extra
  friction for a "just read this aloud" feature), or (c) lacks a clean GGUF/llama.cpp path. None beat
  the Orpheus(Apache+GGUF) / Chatterbox(MIT+quality) pair on becky's specific axes. (OuteTTS is the one
  honorable mention with a GGUF/llama.cpp story — keep on the watch-list as an Orpheus alternative.)

---

## 4. Why Orpheus is the *integration* winner (grounded in becky's code)

becky already drives **llama.cpp `llama-server`** for multimodal (`internal/avlm/server.go`: spawn a
transient server on a free port → wait `/health` → POST → read result, fixed seed, degrade-never-
crash). Orpheus is a **Llama-class GGUF** — it loads on that *same binary and the same spawn/health/
POST dance*. The token model is just another `llama-server` model; becky's existing machinery covers
80% of the work. The genuinely new part is small and well-trodden:

```
text  ──►  llama-server (orpheus GGUF, --temp 0.6 top_p 0.9 rep-penalty 1.1, fixed seed)
                │  emits SNAC audio tokens (custom <custom_token_N> ids)
                ▼
        snac decoder (Python helper, `snac` pkg)  ──►  24 kHz mono PCM  ──►  WAV
```

- **Deps:** the token step is pure llama.cpp (no torch). The decoder step needs the small **`snac`**
  package (+ its tiny model); it runs on the 3070 trivially, and can run on CPU. The reference
  `orpheus-tts-local` project does exactly this against an LM-Studio/llama.cpp server, proving the path.
  ([orpheus-tts-local](https://github.com/isaiahbjork/orpheus-tts-local))
- **VRAM:** Q4_K_M Orpheus ≈ 2 GB; community guidance confirms "8 GB is enough for Q4_K_M" (with the
  rest of the 3070 free). ([orpheus GGUF guides](https://huggingface.co/QuantFactory/orpheus-3b-0.1-ft-GGUF))
- **Latency:** ~200 ms streaming (≈100 ms with input streaming) — fine for short "done"/summary
  readouts. ([canopylabs/orpheus](https://huggingface.co/canopylabs/orpheus-3b-0.1-ft))
- **Output:** 24 kHz mono — exactly what a spoken-readout feature needs.

Chatterbox, by contrast, is **PyTorch/`transformers`** (a separate, heavier helper that does *not*
reuse the llama.cpp transport) — which is why it's the fallback, not the lead, despite arguably edging
naturalness. If Jordan's ear strongly prefers Chatterbox on his box, the spec lets us promote it; the
becky-tts CLI contract is engine-agnostic by design.

---

## 5. What's uncertain / honest caveats

- **The one thing research cannot settle: whether Jordan likes the Orpheus voice.** He rejected a
  leaderboard #1 already. The spec's PROVABLE-HANDOFF makes *hearing it* the final gate and names
  Chatterbox + Maya1 as ready alternates so a "no" isn't a dead end.
- **LLM-TTS stability.** Llama-based TTS (Orpheus, Maya1) can occasionally hallucinate/repeat on long
  or odd input; the literature documents this class of issue and mitigations (low temp, rep-penalty,
  chunking). becky already chunks long transcripts — feed the TTS **sentence/short-paragraph chunks**,
  low temp, fixed seed. ([arXiv:2509.19852](https://arxiv.org/pdf/2509.19852))
- **Determinism is "best-effort."** Fixed seed + greedy-ish decode is reproducible *per build*; a
  llama.cpp/quant change can shift output. Pin the GGUF + flags in `internal/freshness/manifest.json`.
- **Cloud agent cannot hear audio.** Everything voice-quality is Jordan's hardware step (see spec §
  build-split + handoff).

---

## 6. Sources (verified 2026-06-22)

- Orpheus: [github.com/canopyai/Orpheus-TTS](https://github.com/canopyai/Orpheus-TTS) ·
  [huggingface.co/canopylabs/orpheus-3b-0.1-ft](https://huggingface.co/canopylabs/orpheus-3b-0.1-ft) ·
  [unsloth GGUF](https://huggingface.co/unsloth/orpheus-3b-0.1-ft-GGUF) ·
  [QuantFactory GGUF](https://huggingface.co/QuantFactory/orpheus-3b-0.1-ft-GGUF) ·
  [orpheus-tts-local (llama.cpp path)](https://github.com/isaiahbjork/orpheus-tts-local)
- Chatterbox: [github.com/resemble-ai/chatterbox](https://github.com/resemble-ai/chatterbox) ·
  [ResembleAI/chatterbox (HF)](https://huggingface.co/ResembleAI/chatterbox) ·
  [resemble.ai/chatterbox](https://www.resemble.ai/chatterbox/)
- Maya1: [huggingface.co/maya-research/maya1](https://huggingface.co/maya-research/maya1) ·
  [Mungert/maya1-GGUF](https://huggingface.co/Mungert/maya1-GGUF) ·
  [marktechpost overview](https://www.marktechpost.com/2025/11/11/maya1-a-new-open-source-3b-voice-model-for-expressive-text-to-speech-on-a-single-gpu/)
- Leaderboards / arena: [TTS Arena V2](https://huggingface.co/spaces/TTS-AGI/TTS-Arena-V2) ·
  [HF arena-tts blog](https://huggingface.co/blog/arena-tts) ·
  [offlinetts arena 2026 (open-weight ranks)](https://www.offlinetts.com/blog/tts-arena-leaderboard-2026/) ·
  [artificialanalysis.ai TTS leaderboard](https://artificialanalysis.ai/text-to-speech/leaderboard)
- Licenses: [promptquorum local-TTS license survey](https://www.promptquorum.com/power-local-llm/local-tts-voice-cloning-piper-coqui-xtts) ·
  [fishaudio/fish-speech](https://github.com/fishaudio/fish-speech) ·
  [coqui/XTTS-v2](https://huggingface.co/coqui/XTTS-v2)
- Other contenders: [sesame/csm-1b](https://huggingface.co/sesame/csm-1b) ·
  [nari-labs/dia](https://github.com/nari-labs/dia) ·
  [boson-ai/higgs-audio](https://github.com/boson-ai/higgs-audio) ·
  [KittenML/KittenTTS](https://github.com/KittenML/KittenTTS) ·
  [IndexTeam/IndexTTS-2](https://huggingface.co/IndexTeam/IndexTTS-2) ·
  [hexgrad/Kokoro-82M](https://huggingface.co/hexgrad/Kokoro-82M)
- Rejected baselines / general: [inferless 12-model compare](https://www.inferless.com/learn/comparing-different-text-to-speech---tts--models-part-2) ·
  [modal open-source TTS](https://modal.com/blog/open-source-tts)
- In-repo grounding: `becky-go/internal/avlm/server.go` (the llama-server transport reused),
  `internal/config/config.go` (`LlamaServer`, model-path config pattern),
  `internal/pyhelpers/pyhelpers.go` (embedded-helper pattern), `ACCESSIBILITY.md` (who/why),
  `internal/freshness/manifest.json` (where to pin the model).
