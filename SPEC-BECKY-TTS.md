# SPEC — becky-tts — a tiny, intelligent local voice (NeuTTS Air) so becky can read results aloud

> **Status: decisions LOCKED by Jordan 2026-06-22 (§9) — ready for the build swarm. No code yet.**
> Build to: NeuTTS Air, GGUF (Path A), a universal standalone `becky-tts` (never auto-speaks),
> stock preset voice for v1.
>
> **Research note (corrected twice, 2026-06-22).** v1 picked Orpheus-3B off a stale article.
> v2 swapped to Qwen3-TTS-1.7B off HF adoption — still not real research, just a safer default.
> v3 (this) does the actual work Jordan asked for: identify the right MODEL CLASS — **tiny +
> LLM-backbone (expressive) + fast** — survey the current field within it, and use the
> leaderboard as a VERIFICATION step, not the search. Conclusion: **NeuTTS Air.**

## 0. TL;DR

Give becky a spoken voice to read a result aloud (a forensic summary, a transcript answer, a
"done" notice) so Jordan can rest his eyes. The voice must be **local/offline, expressive, FAST,
and NOT Microsoft SAPI/Narrator**. A new `becky-tts` tool (text → WAV, optional playback) backed
by **NeuTTS Air** — a ~0.75B Qwen2-LLM-backbone on-device TTS (Apache-2.0, GGUF). Tiny enough to
be instant, smart enough not to sound flat. Degrade-never-crash: model absent → becky PRINTS the
text, never a Microsoft voice. Final quality gate = **Jordan hears it** (§8.2).

## 1. The model class + the verified field (live HF + web re-check, 2026-06-22)

**The class that matters (Jordan's insight):** the good small TTS models have an **LLM baked in**
→ expressive, context-aware, "intelligent" prosody. Kokoro (82M, no LLM) is light but flat — that's
why he rejected it. The heavy LLM-TTS (3B+) sound great but are **too slow to be useful**. The
target is the intersection: **tiny + LLM-backbone + expressive.** Leaderboards verify a shortlist;
they don't make it (the arena #1 was Kokoro, which he hates).

### 1.1 Chosen engine — NeuTTS Air (`neuphonic/neutts-air`)
- **747.9M params, architecture `qwen2`** (a 0.5B-class LLM backbone → the "intelligent" part),
  **Apache-2.0**, 874 likes / 175K downloads, updated Feb 2026.
- **On-device, real-time, GGUF shipped**: `neuphonic/neutts-air-q4-gguf`, `-q8-gguf`. Pairs the
  LM (GGUF, llama.cpp-class) with the **NeuCodec** decoder → WAV. No torch needed for the LM path.
- Positioned as "the first on-device super-realistic TTS with instant voice cloning… natural,
  expressive, emotionally rich." English-only (fine for Jordan). GitHub: `neuphonic/neutts`.
- Fits becky's "local binary becky shells out to" pattern exactly, and the size means it's fast.

### 1.2 Alternates in the same class (try by ear if Air's voice doesn't land)
- **Chatterbox-Turbo (Resemble AI)** — **350M, MIT**, first open model with emotion-exaggeration
  control, benchmarked favorably vs ElevenLabs. Even smaller; strong second pick.
- **NeuTTS Nano (`neuphonic/neutts-nano`)** — **228.7M, llama backbone**, ultra-light, GGUF — but
  **license:other** (more restrictive than Air's Apache; verify terms before shipping).
- **Qwen3-TTS-12Hz (0.6B / 1.7B, Apache, GGUF)** — heavier (1.7B ~1.9B), solid, multilingual; keep
  as a fallback if a different voice/timbre or non-English is wanted. The 0.6B is the lighter option.
- **IndexTTS-2 (`IndexTeam/IndexTTS-2`)** — evaluated at Jordan's request and REJECTED for becky's
  profile despite being excellent: SOTA WER/speaker-similarity, emotion + duration control (Qwen3
  emotion module). But it is the **heavy class** (~5.9 GB multi-component PyTorch), has **no GGUF**,
  and its license is Apache-2.0 **encumbered by bilibili's Model Use License Agreement** (usage
  restrictions). Fails the tiny + fast + GGUF + clean-license bar. Record only as a "max-quality,
  size-no-object" option if priorities ever flip.

### 1.3 Rejected (settled — do NOT re-propose)
- **Microsoft SAPI / Narrator** — hard no, not even a fallback.
- **Kokoro** — light but no LLM → flat; rejected by Jordan's ear despite arena rank.
- **Piper** — deprecated.
- **Orpheus-3B / XTTS / Maya / any 3B+** — too slow to be useful; XTTS non-commercial + maker defunct.

### 1.4 Leaderboard as verification (not the selector)
TTS-Arena-V2's top (Inworld, Hume, Papla, Vocu, ElevenLabs) is almost all **cloud APIs** —
off-limits offline. The open-model arenas (e.g. `Pendrokar/TTS-Spaces-Arena`) are where small local
models like NeuTTS show up; use them to sanity-check the shortlist, then let Jordan's ears decide.

## 2. becky's current model stack (what we integrate with)
- becky drives local GGUF models via a spawned server + HTTP (`internal/avlm/server.go`: spawn →
  `/health` → POST → `DegradeError`) and embedded Python helpers (`internal/pyhelpers/`, e.g.
  `transcribe_parakeet.py`). becky-tts reuses ONE of these (see §4); the Go CLI is the deterministic
  front, the model is the only AI step, absence degrades to printed text.
- Model + runtime tracked in `internal/freshness/manifest.json` (§6).

## 3. The `becky-tts` tool — CLI shape
```
becky-tts "<text>" --out speech.wav             # synth text -> WAV (explicit --out)
becky-tts --in answer.txt --out speech.wav       # synth a file
becky-tts "<text>" --play                        # synth to a temp WAV and play it (best-effort)
becky-tts "<text>" --voice <name|sample.wav>     # preset, or a short reference sample (Air clones it)
becky-tts --selftest --out s.wav                 # offline proof path, no model needed (§8.1)
  flags: --seed N (default 42, deterministic), --rate (read from helper, not hardcoded),
         --model <path>, --bin <path> (override resolution), --json (machine status)
```
- `--out` mandatory unless `--play`. Never overwrites a non-WAV file (sidecar rule).
- **becky-tts is a UNIVERSAL standalone tool, never auto-invoked.** Other tools (`becky-ask`,
  `becky-report`) MAY call it as a sibling to speak something, but ONLY on an explicit user opt-in
  (e.g. a `--speak` flag). Nothing speaks by default — becky-ask stays its normal colored TUI unless
  the user asks for voice. (Jordan's call: a tool you reach for, not a thing that always talks.)

## 4. The local-helper contract (text → WAV) — two backends; §9 picks one
The Go `becky-tts` is identical either way: build argv, run the helper, validate the WAV.

### 4.1 Path A (preferred, offline-first) — NeuTTS Air GGUF + NeuCodec
- becky-tts shells out to the NeuTTS on-device runtime (`neuphonic/neutts` + the `-q4/-q8-gguf` LM
  and the NeuCodec decoder), emitting a WAV. LM is GGUF (llama.cpp-class), so no torch for the LM.
- Resolution mirrors `internal/reaperbrain`/`internal/config`: `BECKY_TTS_BIN` / `BECKY_TTS_MODEL`
  → becky default model dir → PATH. Missing → DegradeError (§4.4).

### 4.2 Path B (reference, heavier) — NeuTTS Python package
- `internal/pyhelpers/tts_neutts.py` using neuphonic's `neutts` package + safetensors → WAV (same
  pattern as `transcribe_parakeet.py`). Robust; pulls a Python/torch stack.

### 4.3 Playback (`--play`, best-effort, NEVER Microsoft TTS)
- Play the WAV via becky's audio path / a system player. On failure, becky still wrote the WAV and
  says where — it does NOT substitute any other voice.

### 4.4 Degrade-never-crash (hard rule)
- Model/binary/codec absent or synth fails → typed `DegradeError`, PRINT the text so the human still
  gets the content, exit non-zero with a plain reason. **Never** SAPI.

## 5. Deterministic vs model — the split
- Deterministic (Go, cloud-testable): CLI parse, file/sidecar safety, `--seed`, argv build, helper
  process mgmt, WAV validation (header/`ffprobe`), degrade path, `--selftest` WAV assembly from a
  fixed PCM fixture.
- Model (local only): the neural synthesis + NeuCodec decode. The single AI step; all else testable
  without a GPU.

## 6. Config + freshness wiring (contract; local agent makes the JSON/Go edits)
- Add NeuTTS Air (+ chosen GGUF quant) and NeuCodec to `internal/freshness/manifest.json`.
- Add `BECKY_TTS_BIN` / `BECKY_TTS_MODEL` resolution to `internal/config` (mirror existing resolvers).

## 7. Invariants
- Offline + deterministic front; one explicit local model call; degrade-never-crash.
- Single-tool principle: `becky-tts` does ONE thing; other tools call it.
- ACCESSIBILITY.md: the voice is an OUTPUT convenience to rest Jordan's eyes — it does NOT replace
  the high-contrast visual UI; the rejected-voices list (§1.3) is load-bearing.

## 8. Cloud ↔ local build split + PROVABLE HANDOFF
### Build split (honest about the audio boundary)
- **Cloud builds + tests**: the whole deterministic Go layer (§5), the helper contract + a faked
  helper, `--selftest`, `GOOS=windows` cross-compile. Cloud has NO audio device → it CANNOT judge
  the voice and will not claim to.
- **Local only**: install the NeuTTS runtime + GGUF (or the Python helper), run real synth, HEAR it.

### 8.1 One-command OFFLINE proof the cloud CAN run
```
becky-tts --selftest --out /tmp/selftest.wav
ffprobe -v error -show_entries stream=codec_name,sample_rate,channels -of csv=p=0 /tmp/selftest.wav
# EXPECT: pcm_s16le,<rate>,1  (a real mono WAV from a fixed PCM fixture — proves the text->WAV
#         plumbing + WAV writer + validation WITHOUT invoking the model)
```

### 8.2 Ordered, checkboxed LOCAL work order (paste evidence into CLAUDE.md §6)
- [ ] `go build ./... && go test ./... && gofmt -l .` green; `build-all-tools.bat` builds `becky-tts.exe`.
- [ ] Install backend: Path A — fetch `neuphonic/neutts-air-q4-gguf` (or q8) + NeuCodec + the `neutts`
      runtime; or Path B — `pip install` neuphonic's `neutts` package.
- [ ] `becky-tts --selftest --out s.wav` → ffprobe shows a real WAV (offline plumbing proven).
- [ ] `becky-tts "becky here, the transcript is ready" --out hi.wav` → ffprobe confirms a real WAV.
- [ ] `becky-tts "..." --play` → **HEAR it.** Judge quality. If off, try Chatterbox-Turbo, then NeuTTS Nano.
- [ ] Report to Jordan: which model/voice, did it sound good + fast, any degrade notes.

## 9. Decisions — LOCKED by Jordan 2026-06-22 (build to these)
1. **Engine: NeuTTS Air** (0.75B, Apache) — default. ✔ Locked. (Chatterbox-Turbo / Nano stay as
   by-ear alternates only; IndexTTS-2 rejected — §1.2.)
2. **Backend: Path A — GGUF** (`neutts-air-q4`/`q8` + NeuCodec). ✔ Locked ("GGUF preferred if possible").
3. **Scope: a UNIVERSAL standalone `becky-tts` tool, NOT auto-wired into becky-ask.** ✔ Locked —
   becky-ask must NOT always speak; voice is opt-in per call.
4. **Voice: the stock NeuTTS Air preset for now.** ✔ Locked ("neutts default for now"). Custom-voice
   cloning is a later add, not v1.
5. Remaining nicety (not blocking): which other tools grow an opt-in `--speak` later — defer until the
   tool exists and Jordan has heard the voice.

## 10. Sources (live HF + web re-check, 2026-06-22)
- `hf.co/neuphonic/neutts-air` (Apache-2.0, 747.9M, qwen2 backbone, GGUF) + `…-q4-gguf` / `…-q8-gguf`;
  `hf.co/neuphonic/neutts-nano` (228.7M, llama, license:other); GitHub `github.com/neuphonic/neutts`
- `hf.co/ResembleAI/chatterbox` (MIT; Chatterbox-Turbo ~350M, emotion control)
- Heavier fallback: `hf.co/Qwen/Qwen3-TTS-12Hz-1.7B-CustomVoice` (Apache) + 0.6B; GGUF `cstr/qwen3-tts-*`
- Field reviews: getstream.io/blog/best-on-device-tts-models; bentoml.com open-source TTS 2026
- Arena context: `hf.co/spaces/TTS-AGI/TTS-Arena-V2` (cloud-dominated) + `hf.co/spaces/Pendrokar/TTS-Spaces-Arena` (open models) — verification, not selector
