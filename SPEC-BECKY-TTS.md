# SPEC — becky-tts — a natural local voice (Qwen3-TTS-1.7B) so becky can read results aloud

> **Status: design-only, awaiting Jordan's go/no-go (§9). No code yet.**
>
> **Correction note (2026-06-22):** the first draft of this spec recommended Orpheus-3B
> off stale articles. That was shallow — a re-check of the LIVE Hugging Face field (see
> `research/tts.md`) shows the 2024-era names (Orpheus/XTTS/Maya) are no longer the local
> front-runners, 3B is heavier than needed, and **Qwen3-TTS** (which the first pass missed
> entirely) is the right pick: Apache-2.0, ~half the size, GGUF-ready, massively adopted.
> This spec is rebuilt around it.

## 0. TL;DR

Give becky a real spoken voice so it can READ a result aloud (a forensic summary, a
transcript answer, a "done" notice) — letting Jordan rest his eyes. The voice must be
**local/offline, good-quality, and NOT Microsoft SAPI/Narrator**. A new `becky-tts` tool
(text → WAV, optional playback) backed by **Qwen3-TTS-12Hz-1.7B-CustomVoice**. Degrade-
never-crash: if the model/runtime is absent, becky PRINTS the text — it never falls back
to a Microsoft voice. Final quality gate = **Jordan hears it on his hardware** (§8.2).

## 1. Verified facts (live re-check, 2026-06-22) — Qwen3-TTS + alternatives

All confirmed against the live HF hub this session (not an article):

### 1.1 The chosen engine — Qwen3-TTS-12Hz-1.7B-CustomVoice
- `Qwen/Qwen3-TTS-12Hz-1.7B-CustomVoice` — **license Apache-2.0** (clean commercial),
  **~1.9B params** (arch `qwen3_tts`), **7.6M downloads**, official demo space
  `hf.co/spaces/Qwen/Qwen3-TTS`. arXiv:2601.15621.
- **Half the weight of the rejected Orpheus-3B**, and there is a still-lighter
  `Qwen3-TTS-12Hz-0.6B-CustomVoice` if we want featherweight on the 3070.
- **Runs local with GGUF already published** (community): `cstr/qwen3-tts-1.7b-customvoice-GGUF`,
  `cstr/qwen3-tts-0.6b-customvoice-GGUF`, `Serveurperso/Qwen3-TTS-GGUF`, plus the matching
  **12Hz audio codec/detokenizer** GGUF (`cstr/qwen3-tts-tokenizer-12hz-GGUF`) and a dedicated
  **`qwen3-tts.cpp`** runtime (llama.cpp-style C++). So it fits becky's "local binary becky
  shells out to" pattern, like the existing llama-server.
- CustomVoice = preset + custom/cloned voices from a short sample; a sibling `VoiceDesign`
  variant takes a plain-English voice description. For becky we want a stable preset/sample.

Why it wins on becky's axes: **offline-capable (GGUF), permissive (Apache), right-sized
(1.7B/0.6B vs 3B), and the most-adopted current open TTS** — adoption is corroboration that
it actually works for people, which a leaderboard rank does not give us (see §1.3).

### 1.2 Current LOCAL alternates (the real 2026 field, if Jordan dislikes Qwen's voice)
- `microsoft/VibeVoice-1.5B` — **MIT**, 1.5B, podcast/expressive. (Open research model — this
  is NOT the banned Microsoft SAPI/Narrator; different thing entirely. Flagged for optics only.)
- `openbmb/VoxCPM2` — diffusion TTS, voice-cloning/design, very popular.
- `Supertone/supertonic-3` — **ONNX, on-device** (license openrail); the easiest pure-runtime
  integration if we want to avoid a Python/torch stack.
- `ResembleAI/chatterbox` — **MIT**, beats ElevenLabs in the maker's blind test; PyTorch (heavier
  helper, no first-class GGUF).

### 1.3 Why the TTS-Arena-V2 leaderboard is NOT the selector
The current arena top (Inworld TTS MAX/Preliminary, Hume Octave, Papla, Vocu, ElevenLabs
Turbo/Flash) is almost entirely **cloud/proprietary APIs** — disqualified by becky's offline
rule. And the leaderboard is an unreliable proxy for Jordan anyway: Kokoro hit arena #1 and he
rejected it ("sounds like ass"). So we select on **local + permissive + right-sized + adopted**,
then let **Jordan's ears make the final call** (§8.2), with named alternates so a "no" isn't a
dead end.

### 1.4 Explicitly rejected (settled — do NOT re-propose)
- **Microsoft SAPI / Narrator** — Jordan's hard no. Not even a fallback.
- **Piper** — deprecated.
- **Kokoro** — quality insufficient for Jordan (rejected by ear despite arena rank).
- **Orpheus-3B / XTTS-v2 / Maya** — superseded; 3B is heavier than needed, XTTS is non-commercial
  (CPML) and its maker (Coqui) is defunct.

## 2. becky's current model stack (what we integrate with)
- becky already drives local GGUF models via a spawned server + HTTP (`internal/avlm/server.go`:
  spawn → `/health` → POST → `DegradeError`) and via embedded Python helpers
  (`internal/pyhelpers/`, e.g. `transcribe_parakeet.py`) called from a `cmd/*` tool.
- becky-tts reuses ONE of these two patterns (see §4): a local TTS binary (qwen3-tts.cpp/GGUF)
  OR a Python helper (transformers). Either way the Go CLI is the deterministic front; the model
  is the only "AI in the loop", and absence degrades to printed text.
- Model + runtime get tracked in `internal/freshness/manifest.json` (§6) so upgrades are visible.

## 3. The `becky-tts` tool — CLI shape
```
becky-tts "<text>" --out speech.wav             # synth text -> WAV (explicit --out)
becky-tts --in answer.txt --out speech.wav       # synth a file
becky-tts "<text>" --play                        # synth to a temp WAV and play it (best-effort)
becky-tts "<text>" --voice <name|sample.wav>     # preset name or a short reference sample
becky-tts --selftest --out s.wav                 # offline proof path (no model needed) - see §8.1
  flags: --seed N (default 42, deterministic), --rate (read from helper, not hardcoded),
         --model <path>, --bin <path> (override resolution), --json (machine status)
```
- `--out` is mandatory unless `--play`. becky-tts NEVER overwrites a non-WAV file (sidecar rule).
- Other becky tools (e.g. `becky-ask`, `becky-report`) call `becky-tts` as a sibling binary to
  speak a short summary — keeping the single-tool principle (no TTS baked into them).

## 4. The local-helper contract (text → WAV)
Two viable backends; **§9 asks Jordan to pick**. The Go `becky-tts` is identical either way —
it builds argv, runs the helper, and validates the returned WAV.

### 4.1 Path A (preferred, offline-first) — qwen3-tts.cpp + GGUF
- becky-tts shells out to the `qwen3-tts` C++ binary with the 1.7B (or 0.6B) CustomVoice GGUF
  + the 12Hz codec GGUF, which together emit a WAV directly (the codec is the audio detokenizer,
  the role SNAC plays for other models). No torch stack.
- Resolution mirrors `internal/reaperbrain`/`internal/config`: `BECKY_TTS_BIN` / `BECKY_TTS_MODEL`
  -> becky default model dir -> PATH. Missing -> DegradeError (§4.4).

### 4.2 Path B (official, heavier) — Python helper (transformers)
- `internal/pyhelpers/tts_qwen3.py` loads the official safetensors via `transformers` and writes a
  WAV — same pattern as `transcribe_parakeet.py`. Robust + officially supported, but pulls torch.

### 4.3 Playback (`--play`, best-effort, NEVER Microsoft TTS)
- Play the rendered WAV via becky's existing audio path / a system player. If playback fails,
  becky still wrote the WAV and says where — it does NOT substitute any other voice.

### 4.4 Degrade-never-crash (the hard rule)
- Model/binary/codec absent, or synth fails -> return a typed `DegradeError`, PRINT the text to
  stdout so the human still gets the content, exit non-zero with a plain reason. **Never** SAPI.

## 5. Deterministic vs model — the split (becky's "math not tokens" posture)
- Deterministic (Go, cloud-testable): CLI parse, file/sidecar safety, `--seed` plumbing, argv
  build, helper-process management, WAV validation (header/`ffprobe`), degrade path, `--selftest`
  WAV assembly from a fixed PCM fixture.
- Model (local hardware only): the actual neural synthesis + the audio codec decode. This is the
  single AI step; everything around it is deterministic and testable without a GPU.

## 6. Config + freshness wiring (contract; local agent makes the JSON/Go edits)
- Add Qwen3-TTS (1.7B + 0.6B) + the 12Hz codec + the chosen runtime to
  `internal/freshness/manifest.json` so `becky-freshness` reports upstream movement.
- Add `BECKY_TTS_BIN` / `BECKY_TTS_MODEL` resolution to `internal/config` (mirror the existing
  model/binary resolvers). becky-owned default model dir consistent with the rest of the stack.

## 7. Invariants — how this stays becky-shaped
- Offline + deterministic front; one explicit local model call; degrade-never-crash.
- Single-tool principle: `becky-tts` does ONE thing (text->speech); other tools call it.
- ACCESSIBILITY.md: this voice is an OUTPUT convenience so Jordan can rest his eyes — it does
  NOT replace the high-contrast visual UI, and the rejected-voices list (§1.4) is load-bearing.

## 8. Cloud <-> local build split + PROVABLE HANDOFF

### Build split (be honest about the audio boundary)
- **Cloud can build + test**: the whole deterministic Go layer (§5), the helper *contract* + a
  faked helper, `--selftest`, and `GOOS=windows` cross-compile. Cloud has NO audio device, so it
  **cannot** judge how the voice sounds. It will not claim it does.
- **Local (Jordan's PC) only**: install the runtime + GGUF (or the Python helper), run real synth,
  and HEAR it. That is the final gate.

### 8.1 The one-command OFFLINE proof the cloud CAN run (no audio device needed)
```
# Cloud-runnable proof (no GPU, no model weights, no speakers):
becky-tts --selftest --out /tmp/selftest.wav
# then MEASURE it (becky's "it compiles is not proof" rule):
ffprobe -v error -show_entries stream=codec_name,sample_rate,channels -of csv=p=0 /tmp/selftest.wav
# EXPECT: pcm_s16le,<rate>,1   (a real mono WAV assembled from a fixed PCM fixture - proves the
#         text->WAV plumbing + WAV writer + validation without invoking the model)
```

### 8.2 Ordered, checkboxed LOCAL work order (drive to completion; paste evidence into CLAUDE.md §6)
- [ ] `go build ./... && go test ./... && gofmt -l .` green; `build-all-tools.bat` builds `becky-tts.exe`.
- [ ] Install the chosen backend: Path A — fetch `cstr/qwen3-tts-1.7b-customvoice-GGUF` + the 12Hz
      codec GGUF and the `qwen3-tts` binary; or Path B — `pip` the transformers helper deps.
- [ ] `becky-tts --selftest --out s.wav` -> ffprobe shows a real WAV (offline plumbing proven).
- [ ] `becky-tts "becky here, the transcript is ready" --out hi.wav` -> ffprobe confirms a real WAV.
- [ ] `becky-tts "..." --play` -> **HEAR it.** Judge quality. If bad, try the 0.6B, then a §1.2 alternate.
- [ ] Report to Jordan: which model/voice, did it sound good, any degrade notes.

## 9. Open decisions for Jordan (go/no-go — short)
1. **Model size:** Qwen3-TTS **1.7B** (recommended) or the lighter **0.6B**? (1.7B unless speed/VRAM bites.)
2. **Backend:** Path A **qwen3-tts.cpp + GGUF** (offline-first, recommended) or Path B **Python/transformers** (official, heavier)?
3. **Voice:** a built-in preset, or clone from a short reference sample you provide?
4. **Fallback if you dislike Qwen's voice by ear:** VibeVoice-1.5B (MIT) / VoxCPM2 / supertonic-3 / Chatterbox — pick a 2nd to try.
5. **Where becky speaks first:** `becky-ask` answers and `becky-report`/forensic summaries (recommended), or a standalone `becky-tts` only for now?

## 10. Sources (live HF re-check, 2026-06-22)
- `hf.co/Qwen/Qwen3-TTS-12Hz-1.7B-CustomVoice` (Apache-2.0, 1.9B, 7.6M downloads, arXiv:2601.15621)
- GGUF: `hf.co/cstr/qwen3-tts-1.7b-customvoice-GGUF`, `hf.co/cstr/qwen3-tts-0.6b-customvoice-GGUF`,
  `hf.co/Serveurperso/Qwen3-TTS-GGUF`; codec `hf.co/cstr/qwen3-tts-tokenizer-12hz-GGUF`
- Alternates: `hf.co/microsoft/VibeVoice-1.5B` (MIT), `hf.co/openbmb/VoxCPM2`,
  `hf.co/Supertone/supertonic-3` (ONNX/on-device), `hf.co/ResembleAI/chatterbox` (MIT)
- Leaderboard context: `hf.co/spaces/TTS-AGI/TTS-Arena-V2` (top is cloud APIs — see §1.3)
