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

### 8.1 One-command OFFLINE proof — BUILT + RUN by cloud (2026-06-22)
```
becky-tts --selftest --out /tmp/selftest.wav
ffprobe -v error -show_entries stream=codec_name,sample_rate,channels -of csv=p=0 /tmp/selftest.wav
# EXPECT (local, with ffprobe): pcm_s16le,24000,1
```
**Cloud has NO ffprobe and NO audio device**, so cloud proved the same thing TWO other ways
(equivalent, and the canonical one for this repo's CI which is also ffprobe-less):
1. A Go test parses the `--selftest` WAV header and asserts RIFF/WAVE + `fmt ` (PCM, mono, 16-bit,
   24000 Hz) + a non-empty `data` chunk (`internal/tts.ValidateWAV`, exercised by
   `internal/tts/synth_test.go` and `cmd/tts/main_test.go`). This is the ffprobe-free equivalent.
2. A real run + raw header dump (paste from cloud, `go run ./cmd/tts --selftest --out /tmp/tts_selftest.wav`):
   ```
   Saved: /tmp/tts_selftest.wav (28844 bytes, 24000 Hz)
   0000000 52 49 46 46 a4 70 00 00 57 41 56 45 66 6d 74 20  >RIFF.p..WAVEfmt <
   0000016 10 00 00 00 01 00 01 00 c0 5d 00 00 80 bb 00 00  >.........]......<
   0000032 02 00 10 00 64 61 74 61 80 70 00 00 00 00 04 00  >....data.p......<
   #            ^PCM ^mono ^24000Hz(0x5dc0)        ^16-bit ^data chunk, 0x7080 payload bytes
   ```
The neural-voice quality and actual SOUND are unverifiable in the cloud — those are local-only (8.2).

### 8.2 Work order — cloud's half DONE; local's half is the checklist below
**CLOUD DID (this branch, deterministic half — only `cmd/tts/` + `internal/tts/` touched):**
- [x] `internal/tts`: `Synth` interface + `Synthesize(text, opts)`; real `ggufSynth` resolving
      `BECKY_TTS_BIN`+`BECKY_TTS_MODEL` (override → env → becky default `…\models\tts\` → PATH, mirrors
      `internal/reaperbrain`); pure-Go PCM16-mono WAV writer (`WriteWAVPCM16`) + validator (`ValidateWAV`);
      deterministic `SelfTest` fixture; typed `DegradeError` (NEVER a Microsoft voice). `NeuTTSArgs`
      documents the exact argv the local runtime must honour.
- [x] `cmd/tts` (`becky-tts`): full CLI — `"<text>"` / `--in` / `--out` / `--play` / `--voice` /
      `--selftest` / `--seed` / `--model` / `--bin` / `--json`; flags work before OR after the text;
      `--out` mandatory unless `--play`/`--selftest`; refuses a non-`.wav` ext AND refuses to overwrite an
      existing non-WAV file; degrade = print the text to stdout + plain reason to stderr + non-zero exit;
      `--play` best-effort (PowerShell SoundPlayer / afplay / aplay-paplay-ffplay) and keeps the WAV on failure.
- [x] `go build ./cmd/tts/... ./internal/tts/...` exit 0; `go vet` clean; `gofmt -l cmd/tts/ internal/tts/`
      empty; `go test ./cmd/tts/... ./internal/tts/...` green (value-asserting: header bytes, selftest
      validity+determinism, degrade-prints-text-when-bin/model-absent, non-WAV-ext refusal, overwrite
      guard, helper-contract, resolver precedence). `GOOS=windows go build ./cmd/tts/` exit 0.
- [x] `--selftest` RUN offline; header dump pasted in §8.1. Cloud CANNOT hear it — stated plainly.

**LOCAL TO DO (needs the GPU/audio/disk cloud has none of — drive in order, paste evidence into CLAUDE.md §6):**
- [ ] 1. `cd becky-go && go build ./... && go test ./... && gofmt -l .` green on Windows; `build-all-tools.bat`
        (auto-discovers `cmd/tts`) builds `becky-tts.exe`.
- [ ] 2. Fetch the NeuTTS Air GGUF backend (Path A) — recommended layout under `X:\AI-2\becky-tools\models\tts\`:
        - LM GGUF: `huggingface-cli download neuphonic/neutts-air-q4-gguf --local-dir X:\AI-2\becky-tools\models\tts`
          (or `neutts-air-q8-gguf` for higher quality). Name the file so it contains `neutts`/`air`/`q4` (the
          resolver's scorer prefers those and disqualifies `codec`/`mmproj`/`vocoder`).
        - NeuCodec decoder: `huggingface-cli download neuphonic/neucodec --local-dir X:\AI-2\becky-tools\models\tts\neucodec`.
- [ ] 3. Install the NeuTTS on-device runtime that becky-tts shells out to (the `--bin`). It MUST accept the
        argv `internal/tts.NeuTTSArgs` emits: `--model <gguf> --text <text> --out <wav> --voice <name|sample> --seed <n> [--rate <hz>]`
        and write a PCM WAV to `--out`. From neuphonic's `neutts` repo (`github.com/neuphonic/neutts`):
        `pip install neutts` (or clone + `pip install -e .`), then provide a thin CLI wrapper exposing those
        flags (or adapt the repo's inference script into one) and point `BECKY_TTS_BIN` at it. If you ship the
        Python wrapper as `neutts-air.exe`/`neutts.exe` on PATH it is auto-found; otherwise set the env.
- [ ] 4. Point becky at it (PowerShell), e.g.:
        `setx BECKY_TTS_BIN "X:\AI-2\becky-tools\models\tts\neutts-air.exe"`
        `setx BECKY_TTS_MODEL "X:\AI-2\becky-tools\models\tts\neutts-air-q4.gguf"`
        (or pass `--bin` / `--model` per call). Until these resolve, becky-tts honestly degrades to printed text.
- [ ] 5. `becky-tts --selftest --out s.wav` then `ffprobe -v error -show_entries stream=codec_name,sample_rate,channels -of csv=p=0 s.wav`
        → expect `pcm_s16le,24000,1` (offline plumbing proven on hardware; no model needed for this step).
- [ ] 6. `becky-tts "becky here, the transcript is ready" --out hi.wav` then ffprobe `hi.wav` → confirm a real,
        non-empty WAV from the MODEL (rate will be the NeuTTS native rate, not 24000).
- [ ] 7. `becky-tts "becky here, the transcript is ready" --play` → **HEAR it.** Judge quality + speed.
        If the voice is off, try `neutts-air-q8`, then Chatterbox-Turbo (MIT, ~350M), then NeuTTS Nano (§1.2).
- [ ] 8. Report to Jordan: which model/voice/quant, did it sound good + fast, any degrade notes — and let
        Jordan HEAR it (the final gate, §0).

**Config/freshness wiring (local, §6 — not in cloud's `cmd/tts`+`internal/tts` lane):** add `BECKY_TTS_BIN`/
`BECKY_TTS_MODEL` resolution to `internal/config` and add NeuTTS Air + the chosen quant + NeuCodec to
`internal/freshness/manifest.json`. becky-tts works without these (env/flag/default resolution), so this is a
convenience pass, not a blocker.

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
