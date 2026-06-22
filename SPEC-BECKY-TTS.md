# SPEC — becky-tts — a natural local voice (Orpheus-3B) so becky can read results aloud

> ## STATUS — design only, NOT built; researched + decided 2026-06-22
> No Go code, no Python helper, no model download exists for this yet. This spec proposes adopting
> **Orpheus-3B (Apache-2.0)** as becky's primary text-to-speech engine, served on the **llama.cpp
> `llama-server`** transport becky already runs (`internal/avlm/server.go`), with a thin **SNAC
> decoder** Python helper, exposed through a new **`becky-tts`** CLI (**text in → WAV out** / optional
> playback). Full cited evidence is in **`research/tts.md`**. Re-verify any model id / license clause /
> llama.cpp flag past this date before building. Nothing ships until Jordan greenlights it and HEARS
> it (open decisions at the end).
>
> **WHO this is for (load-bearing — read `ACCESSIBILITY.md`):** Jordan is **sighted with impaired
> vision**; he does **not** use a screen reader. He wants becky to **read results aloud in a good,
> natural voice** so he can rest his eyes. **NATURALNESS is criterion #1.** A robotic voice is a
> regression even if it "adds accessibility."

---

## 0. TL;DR

- **Engine:** **Orpheus-3B** — a Llama-3.2-3B fine-tune that emits **SNAC** neural-codec audio tokens.
  Reputation for **human-like, emotional** speech; **Apache-2.0** (clean commercial); **first-class
  GGUF** that runs on **the exact `llama-server` path becky already drives for Gemma-4**.
- **Fit:** Q4_K_M ≈ 2 GB → trivially fits the **8 GB RTX 3070**, GPU-offloaded, with the rest free.
  Output **24 kHz mono**, ~200 ms streaming latency. 8 stable preset voices (`tara, leah, jess, leo,
  dan, mia, zac, zoe`) → no cloning needed, deterministic with a fixed seed.
- **Why not the leaderboard #1:** the arenas measure crowd preference on short clips and **do not
  predict Jordan's ear** — he already rejected a #1 (Kokoro). And the top open-weight arena models
  (Fish/OpenAudio, F5-TTS, XTTS-v2) are **non-commercial** → disqualified. The two permissive, local,
  credible options are **Orpheus (Apache, GGUF — lead)** and **Chatterbox (MIT, PyTorch — fallback)**.
- **becky shape:** one new tool, `becky-tts`, file/text-in → WAV-out → exit code. Offline +
  deterministic at inference. **Degrade-never-crash: if the model/binary is absent, PRINT THE TEXT —
  never Microsoft TTS.**
- **The honest gap:** the cloud agent has **no audio device** and **cannot judge the voice**. It can
  scaffold + unit-test the deterministic Go, define the helper contract, and cross-compile. **Whether
  it sounds good is Jordan's hardware step** — made the mandatory final gate in §8.

---

## 1. Verified facts (the re-check baseline) — Orpheus-3B + alternatives

All fetched/corroborated **2026-06-22** (see `research/tts.md` for the ≥2-source backing of each).
Re-verify before building; TTS model lines move monthly.

### 1.1 The chosen engine — Orpheus-3B

| Fact | Value | Source |
|---|---|---|
| Architecture | Llama-3.2-3B-Instruct fine-tune → emits **SNAC** audio tokens (decoded to waveform) | github.com/canopyai/Orpheus-TTS |
| License | **Apache-2.0** (commercial OK) | unsloth GGUF card; canopylabs HF |
| Format | **GGUF** (1-bit … 16-bit); **Q4_K_M ≈ 2 GB** | huggingface.co/unsloth/orpheus-3b-0.1-ft-GGUF |
| Runtime | **llama.cpp / `llama-server`** (becky's existing transport) + `snac` decoder | github.com/isaiahbjork/orpheus-tts-local |
| VRAM (3070, 8 GB) | Q4_K_M fits with large headroom (community: "8 GB is enough for Q4_K_M") | QuantFactory GGUF card |
| Output | **24 kHz mono**; ~200 ms streaming latency | canopylabs/orpheus-3b-0.1-ft |
| Preset voices | `tara, leah, jess, leo, dan, mia, zac, zoe` (English) | github.com/canopyai/Orpheus-TTS |
| Decode params | temp 0.6, top_p 0.9, **repetition_penalty 1.1** (tune for stability) | orpheus GGUF guides |

### 1.2 The fallback — Chatterbox (Resemble AI)

- **MIT** license; 0.5B (Turbo 350M). Strong naturalness (maker blind study: 65.3% preferred over
  ElevenLabs). **PyTorch/`transformers`, NOT GGUF** → a *separate* helper, does not reuse llama-server.
  ~5–7 GB VRAM typical (Turbo lighter, runs on a 3060). Source: github.com/resemble-ai/chatterbox.
- **Role:** if Jordan's ear prefers it on his hardware, promote it; the `becky-tts` CLI contract is
  engine-agnostic so this is a config/helper swap, not a redesign.

### 1.3 The close Apache alternative — Maya1 (Maya Research)

- 3B Llama-style + SNAC (same shape as Orpheus); **Apache-2.0**; 20+ emotion tags + natural-language
  **voice design** (`<description="40-year-old, low-pitch, warm">`). Official path wants **16 GB/vLLM**;
  **community GGUF** exists (Mungert/maya1-GGUF) for llama.cpp/CPU. **24 kHz mono.** Pick over Orpheus
  only if Jordan wants emotion/voice-design AND the community GGUF proves stable on his box.

### 1.4 Explicitly rejected (do NOT propose — settled)

- **Kokoro-82M** — Jordan: "sounds like ass" (even though it hit arena #1). Baseline only.
- **Piper** — deprecated (Jordan). Baseline only.
- **Microsoft SAPI / Narrator** — **banned. Never. Not even as fallback.** (becky's fallback is
  printing text.)
- **Fish/OpenAudio (CC-BY-NC), F5-TTS (CC-BY-NC), XTTS-v2 (CPML, Coqui defunct)** — non-commercial /
  unmaintained → flagged out for shipped software.
- **Higgs Audio** — v2 Apache but **v3 went research/non-commercial** (license drift risk); watch only.

---

## 2. becky's current model stack (what we integrate with)

Grounded in real shipped code so the proposal is cheap:

- **AVLM (audio-visual):** `becky-validate` / `internal/avlm` runs Gemma-4 E4B through **llama.cpp
  `llama-server`**. The transport (`internal/avlm/server.go`) — confirmed by reading it —
  `spawnServer` allocates a free localhost port → `waitForHealth` polls `GET /health` → `chat` POSTs
  an OpenAI-style `/v1/chat/completions` (fixed seed, temperature) → reads the assistant text → **every
  recoverable failure returns a typed `*DegradeError`** (never panics). `ensureServer` reuses an
  already-running server when `Runner.ServerURL` is set.
- **Config:** `internal/config/config.go` holds `LlamaServer` (path to `llama-server.exe`) and a
  per-model path pattern (`GemmaModel`, `ParakeetModelDir`, …) merged from `~/.becky/config.json`.
- **Python helpers:** `internal/pyhelpers/pyhelpers.go` embeds small `.py` glue scripts and
  `Materialize`s them to a temp dir at runtime, so the compiled `.exe` is self-contained.
- **Freshness:** `internal/freshness/manifest.json` is the single source of truth for pinned models.

**The seam:** Orpheus is **a Llama-class GGUF**, so the *token half* reuses `spawnServer` / `ensureServer`
/ `waitForHealth` / `chat` **almost verbatim** (different model path, text-only request, no `--mmproj`).
The only genuinely new machinery is the **SNAC decoder helper** (tokens → 24 kHz WAV) and playback.
That is the whole integration cost.

---

## 3. The `becky-tts` tool — CLI shape

One new tool, single-purpose (single-tool principle): **turn text into spoken audio.** File/text-in →
WAV-out → exit code. No tool's existing contract changes.

```
becky-tts say "Done. Found 12 matches for penguin." [--voice tara] [--out done.wav] [--play]
becky-tts say --in summary.txt --out summary.wav
becky-tts say --in report.md --play           # render to a temp WAV and play it, no file kept
becky-tts voices                              # list available preset voices (plain text)
becky-tts --selftest [--out tts-selftest.wav] # render a fixed line deterministically (cloud proof)
```

Flags:
- `--voice <name>` — preset voice (default from config, e.g. `tara`). `voices` lists them.
- `--in <file>` / positional text — input text (UTF-8). `--in -` reads stdin.
- `--out <path>` — write a 24 kHz mono WAV. Default: `<input-dir>/<stem>.tts.wav`, or a temp file when
  only `--play` is given (becky's "write next to the source" protocol; temp when ephemeral).
- `--play` — play the rendered WAV through the default device after writing (best-effort; see §4).
- `--seed <int>` — default fixed (e.g. 42) for deterministic output.
- `--temp` / `--top-p` / `--rep-penalty` — decode knobs (defaults 0.6 / 0.9 / 1.1); rarely touched.
- `--engine <orpheus|chatterbox|maya1>` — engine selector (default `orpheus`); honest degrade if the
  selected engine's model/helper is absent.
- `--json` — emit a machine-readable result (`{out, voice, engine, seconds, sample_rate, degraded,
  note}`) for other tools (becky-ask, becky-clip "read this aloud", becky-report "speak the summary").

**Output discipline:** stdout for a spoken-readout tool is the WAV path + a one-line status (or the
`--json` object); the *audio* is the deliverable. `becky-tts voices` prints a short plain list Jordan
can read (per `ACCESSIBILITY.md` — concise, readable, colorized if it grows).

**Library form:** the engine lives in `internal/tts` (a `Speaker` interface + an `Orpheus` impl) so
other tools call it directly (`tts.Speak(text, opts) (wavPath, error)`) without shelling out — the same
pattern as `internal/avlm`.

---

## 4. The local-helper contract (text → WAV)

Two stages, mirroring `research/tts.md` §4. The Go side owns orchestration; a thin Python helper owns
only the SNAC decode (and, for Chatterbox, the whole torch call).

### 4.1 Stage A — token generation (Go, reuses the llama-server transport)

- Resolve `cfg.LlamaServer` + the Orpheus GGUF path (new `cfg.TTSModel`). Reuse `spawnServer` /
  `ensureServer` / `waitForHealth`. **Text-only** request (no `--mmproj`, no image/audio parts).
- Prompt format: Orpheus expects the voice-tagged prompt convention (`<voice>: <text>` style, per the
  Orpheus repo — **confirm the exact special-token framing at build time against the model card**).
- Decode: `--temp 0.6 --top-p 0.9` + repetition penalty `1.1`, **fixed `--seed`**, request the model's
  audio-token stream. Long input is **chunked into sentences/short paragraphs** (becky already chunks
  transcripts) to avoid LLM-TTS drift on long text (`research/tts.md` §5).
- Output of Stage A: the raw SNAC token id sequence (parsed from the completion's `<custom_token_N>`
  ids).

### 4.2 Stage B — SNAC decode (thin Python helper, embedded)

`internal/pyhelpers/tts_snac.py` (embedded like the others), contract:

- **Args:** `--rate 24000 --out <wav>` (model id / SNAC variant configurable).
- **stdin:** JSON `{"tokens": [<int>, ...], "seed": 42}` (the SNAC token ids from Stage A).
- **stdout:** JSON `{"out": "<wav path>", "frames": <n>, "sample_rate": 24000}` on success; on any
  recoverable failure a JSON `{"error": "..."}` and a non-zero exit (Go maps to `*DegradeError`).
- **Deps:** the small **`snac`** package + its decoder model (NOT a full torch-TTS stack); runs on the
  3070 or CPU. Pinned in config (`cfg.TTSPython`, `cfg.SNACModel`).
- **Determinism:** SNAC decode is deterministic for fixed tokens; seed is carried for completeness.

### 4.3 Playback (`--play`, best-effort, NEVER Microsoft TTS)

- Play the rendered WAV through the default device with an existing local tool: prefer becky's own
  audio path (`becky-daw-engine` WAV playback already exists) or `ffplay` from the configured `ffmpeg`
  install. **`--play` only plays an already-rendered WAV** — it is NOT a TTS engine, so there is no
  path by which a missing model leads to a system voice. If no player resolves, write the WAV and print
  its path with a note; do not crash.

### 4.4 Degrade-never-crash (the hard rule)

Every recoverable failure (no `llama-server`, no GGUF, unhealthy server, no `snac`, no Python, decode
error) → typed `*DegradeError` + **PRINT THE INPUT TEXT to stdout** so Jordan still gets the content,
plus a one-line honest note (`tts unavailable: <reason>; printing text instead`). **Never** fall back
to SAPI/Narrator/any system voice. `--json` sets `degraded:true` + `note`.

---

## 5. Deterministic vs model — the split (becky's "math not tokens" posture)

- **Deterministic → Go, no surprises:** CLI parsing, text chunking, prompt assembly, the
  spawn/health/POST transport, token parsing, WAV muxing of decoded PCM, output-path derivation,
  the degrade ladder, `--json` shaping, `voices` listing.
- **Model step → the inherent non-determinism, pinned down:** the Orpheus token generation is a model
  call — made **as reproducible as a local TTS allows** via fixed seed + low temp + greedy-ish decode +
  pinned GGUF/flags. Honest caveat (`research/tts.md` §5): a quant/llama.cpp upgrade can shift the
  waveform; pin the model in freshness and re-verify on upgrade. SNAC decode itself is deterministic.

---

## 6. Config + freshness wiring (contract; local agent makes the JSON/Go edits)

New `Config` fields (in `internal/config/config.go`, same `firstExisting`/`resolve` pattern):

```go
TTSEngine  string // "orpheus" (default) | "chatterbox" | "maya1"
TTSModel   string // Orpheus GGUF path, e.g. X:\AI-2\becky-tools\models\tts\orpheus-3b-0.1-ft-Q4_K_M.gguf
TTSVoice   string // default preset voice, e.g. "tara"
TTSPython  string // interpreter with the `snac` package (may equal an existing helper python)
SNACModel  string // SNAC decoder model id/dir (e.g. hubertsiuzdak/snac_24khz)
// (llama-server is already cfg.LlamaServer)
```

Freshness rows (proposed, matching the existing schema in `internal/freshness/manifest.json`):

```jsonc
{
  "id": "orpheus-tts",
  "name": "Canopy Orpheus-3B (GGUF)",
  "used_by": ["becky-tts"],
  "pinned": "orpheus-3b-0.1-ft Q4_K_M (served via llama-server) + snac_24khz decoder",
  "upstream": {"type": "hf-model", "ref": "unsloth/orpheus-3b-0.1-ft-GGUF"},
  "note": "Primary local TTS. Apache-2.0. Llama-class GGUF on becky's llama-server transport + SNAC decode -> 24kHz WAV. Re-verify quant/flags on upgrade."
},
{
  "id": "chatterbox-tts",
  "name": "Resemble AI Chatterbox (fallback)",
  "used_by": ["becky-tts"],
  "pinned": "candidate fallback (MIT, PyTorch -- NOT GGUF); promote if Jordan's ear prefers it",
  "upstream": {"type": "hf-model", "ref": "ResembleAI/chatterbox"},
  "note": "MIT. Separate torch helper; does not reuse llama-server. Strong naturalness in maker blind tests."
}
```

---

## 7. Invariants — how this stays becky-shaped

- **Offline + deterministic at inference.** Local llama.cpp + local SNAC decode; no network. Fixed
  seed; pinned model. (Cloud TTS APIs are explicitly NOT used — they'd break the offline invariant and
  Jordan's data-locality posture for forensic content.)
- **Degrade, never crash.** Any missing piece → `*DegradeError` + **print the text**; never a system
  voice. (`internal/avlm` already models this exact discipline.)
- **Single-tool principle.** `becky-tts` does ONE thing (text → speech). Other tools *call* it
  (becky-ask reads an answer, becky-report speaks the conclusion, becky-clip says "export done") — they
  do not absorb it.
- **License tracked.** Orpheus Apache-2.0 (clean); Chatterbox MIT (clean). The rejected non-commercial
  engines stay rejected; re-confirm Orpheus's license on upgrade in `becky-freshness`.
- **Accessibility, correctly.** This serves Jordan's real need (a good voice to rest his eyes), does
  NOT strip his colored TUIs, and NEVER uses Microsoft TTS (`ACCESSIBILITY.md`).

---

## 8. Cloud ↔ local build split + PROVABLE HANDOFF

### Build split (per CLAUDE.md §4) — be honest about the audio boundary

| Cloud / web agent (no audio device, no GPU, no model weights) | Local agent (Jordan's Win10 + RTX 3070) |
|---|---|
| This spec; model/license verification (`research/tts.md`) | Download the Orpheus GGUF + SNAC model; real inference |
| `internal/tts` (`Speaker` iface, `Orpheus` impl, chunking, prompt assembly, token parse, WAV mux) | Pin `--temp/--top-p/--rep-penalty/--seed`, voice token framing from real runs |
| `cmd/tts` CLI + `--selftest` + `--json`; reuse of the avlm transport | **HEAR it** — confirm the voice clears Jordan's bar; benchmark VRAM/latency |
| Unit tests for the deterministic Go (arg parse, chunking, path derivation, degrade ladder, WAV header on a synthetic token stream) | Wire the embedded `tts_snac.py` to the real `snac` package; freshness JSON edit |
| Cross-compile / `go build ./...` green | `build-all-tools.bat`; the hardware Definition-of-Done below |

The cloud agent works on a `claude/becky-tts-*` branch and opens a **draft PR** — no push to `master`,
no code until Jordan greenlights §9. Every Python/model step the cloud can't run is a documented stub
with an explicit input→output contract (§4.2); the local agent only plugs in the model + real numbers.

### 8.1 The one-command OFFLINE proof the cloud CAN run (no audio device needed)

The cloud cannot hear audio, but it CAN prove the *deterministic pipeline shape* end-to-end without a
GPU/model by exercising the WAV-muxing path on a **synthetic, fixed token stream** through the real
decoder-helper contract (a deterministic stub decoder when `snac` is absent), then measuring the
output:

```bash
# Cloud-runnable proof (no GPU, no model weights, no speakers):
becky-tts --selftest --out tts-selftest.wav --json
# then MEASURE it (becky's "it compiles is not proof" rule):
ffprobe -v error -show_entries stream=codec_name,sample_rate,channels -of default=nk=1 tts-selftest.wav
# EXPECT: pcm_s16le  24000  1     (a real 24 kHz mono WAV of the fixed selftest token stream)
```

This proves: arg parse → chunking → token-stream handling → decode-helper contract → WAV mux →
measurable file. It does **NOT** prove the voice sounds good — that is impossible without ears and is
Jordan's gate below. (When the real Orpheus GGUF + `snac` are installed locally, the *same* command
renders real speech; the selftest's synthetic stream is the cloud-verifiable stand-in.)

### 8.2 Ordered, checkboxed LOCAL work order (drive to completion; paste evidence into CLAUDE.md §6)

- [ ] **0. Green base.** `cd becky-go && go build ./... && go vet ./... && go test ./...` pass; `gofmt -l .` clean.
- [ ] **1. Cloud proof reproduces.** Run §8.1 `--selftest`; confirm `ffprobe` shows `pcm_s16le 24000 1`.
- [ ] **2. Install the model.** Download `orpheus-3b-0.1-ft-Q4_K_M.gguf` (unsloth) to
      `models\tts\`; `pip install snac` for `cfg.TTSPython`; set `~/.becky/config.json`
      (`TTSModel`, `TTSPython`, `SNACModel`, `TTSVoice`). Confirm `cfg.LlamaServer` resolves.
- [ ] **3. Render real speech to a file.** `becky-tts say "becky is online and ready." --out hello.wav`
      → `ffprobe` confirms 24 kHz mono, non-trivial duration; `ffmpeg volumedetect` shows audible
      (mean above the −80 dB floor). Paste the numbers.
- [ ] **4. HEAR it (the gate research can't pass).** `becky-tts say "..." --play` — Jordan listens and
      confirms the voice clears his bar (NOT robotic). If no: try another preset voice, then
      `--engine chatterbox` / `--engine maya1`. **This is the Definition-of-Done.**
- [ ] **5. Degrade proof.** Temporarily point `TTSModel` at a missing path → confirm `becky-tts` PRINTS
      the text + an honest note and **does not** invoke any system voice (and exits cleanly).
- [ ] **6. VRAM/latency.** Note Q4_K_M VRAM on the 3070 and short-utterance latency; pin in
      `freshness/manifest.json`.
- [ ] **7. Build the .exe.** `build-all-tools.bat` (auto-discovers `cmd/tts`); confirm `becky-tts.exe`.
- [ ] **8. Wire one caller.** Make becky-ask (or becky-report) offer "read this aloud" via `internal/tts`.

A canvas/feature parallel: **"it compiles" is not "done" — `becky-tts` is done only when ▶ makes a
voice Jordan likes come out of the speakers.**

---

## 9. Open decisions for Jordan (go/no-go — short, readable list)

1. **Adopt Orpheus-3B as the primary voice?** (Apache-2.0, GGUF on becky's existing stack, 8 preset
   voices, fits the 3070.) Yes / try-something-else.
2. **Which preset voice is the default** once you hear them — `tara, leah, jess, leo, dan, mia, zac,
   zoe`? (Pick after Step 4; we'll set `TTSVoice`.)
3. **Fallback engine if Orpheus's voice isn't right for you:** **Chatterbox** (MIT, possibly more
   natural, heavier torch dep) or **Maya1** (Apache, emotion/voice-design, community GGUF)? Default
   plan: try Chatterbox first.
4. **Quality vs size:** stay on **Q4_K_M (~2 GB, fast)**, or spend VRAM on **Q6_K/Q8_0** for a possible
   quality bump (still fits 8 GB)? Default: start Q4_K_M, A/B against Q8_0 by ear.
5. **Where should becky speak by default** — manual `--play` only, or auto-read short "done"/summary
   notices in becky-ask/becky-report (with a quiet/no-voice toggle)? Default: opt-in `--play`, plus a
   `--speak` option in callers.

---

## 10. Sources

Full cited evidence (≥2 sources per claim) is in **`research/tts.md` §6**. Key anchors: Orpheus
[GitHub](https://github.com/canopyai/Orpheus-TTS) / [unsloth GGUF](https://huggingface.co/unsloth/orpheus-3b-0.1-ft-GGUF) /
[orpheus-tts-local llama.cpp path](https://github.com/isaiahbjork/orpheus-tts-local); Chatterbox
[GitHub](https://github.com/resemble-ai/chatterbox); Maya1 [HF](https://huggingface.co/maya-research/maya1);
licenses [promptquorum survey](https://www.promptquorum.com/power-local-llm/local-tts-voice-cloning-piper-coqui-xtts);
arena context [offlinetts 2026](https://www.offlinetts.com/blog/tts-arena-leaderboard-2026/). In-repo
grounding: `internal/avlm/server.go`, `internal/config/config.go`, `internal/pyhelpers/pyhelpers.go`,
`internal/freshness/manifest.json`, `ACCESSIBILITY.md`.
