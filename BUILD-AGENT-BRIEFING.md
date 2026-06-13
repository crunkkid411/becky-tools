# becky-go — Build Agent Briefing (shared by every tool-build subagent)

You are a **coding subagent** building ONE becky tool. Read this fully, then read
your assigned task file and the v3 spec. Build a robust, tested tool. Do not stop
until it compiles, runs on real footage, emits valid JSON, and exits 0.

## MANDATORY: Model Verification Protocol (any tool/stage that picks an AI model)
NEVER pick a model from an article, blog, "guide", or your training data — they are stale and
SEO-tainted (a 2026 blog that names "Phi-4-mini", "qwen3-4b", or version-less "gemini flash" is a
RED FLAG, not a source). A model name without a verified-current version + official source is a
BLUNDER and is not allowed. Before naming ANY model:
- **Local (HuggingFace) — REQUIRED, use the HF CLI:** `hf` / `huggingface-cli` to check the
  publisher's OFFICIAL current repos and read the actual model card. e.g. Qwen is on the **3.5 / 3.6
  / 3.7** line now (NOT "qwen3"); confirm a runnable size (e.g. 4B), prefer the **Instruct** variant
  over base for tool/chat work, and pick a GGUF/quant that fits the GPU. **Reuse what's on disk
  first:** check `X:\HuggingFace\models\` and `models\` before downloading — e.g. Qwen3.5-4B GGUF +
  mmproj is already at `X:\HuggingFace\models\unsloth\Qwen3.5-4B-GGUF`, and the Gemma-4 AV model is
  in `models\gemma4\` (consider reusing it before adding anything).
- **OpenRouter (no CLI):** fetch the LIVE models list (OpenRouter models API/page), filter to free,
  sort newest, pick a CURRENT model suited to the task (agentic coding / tool-calling / reasoning),
  and confirm the exact id is live. Do NOT assume DeepSeek-free (their free OpenRouter models are
  1.5+ yrs old). **Default if you cannot verify:** `poolside/laguna-m.1:free` (agentic coding,
  tool-calling + reasoning, 128k ctx, 8k out); fallback `moonshotai/kimi-k2.6:free`.
- **Hosted APIs (Gemini/etc.):** NEVER write a version-less name like "gemini flash" — it resolves
  to a deprecated version and the API call fails. Verify the EXACT current version on the official
  site (e.g. Gemini Flash-Lite 3.1/3.5, not 1.5).
- **Record per choice:** exact model id, official source URL, date checked, Instruct-vs-base
  decision, and why it fits the GPU/task. This applies to EVERY becky tool, not just new-tool/ask.

## Role & non-negotiable definition of done

Run a Ralph-style loop: **build → run on real input → inspect output → fix → repeat**
until ALL of these are true (verify, do not assume):

1. `go build -o bin\becky-<name>.exe .\cmd\<name>` succeeds with no errors.
2. `go vet .\cmd\<name>` is clean.
3. The tool RUNS on the real test asset `X:\AI-2\becky-tools\test.mp4` (45.2s clip,
   has video+audio) — or, for tools that take a JSON input, on real output produced
   by the upstream tool (see your task file's chaining). No synthetic-only testing.
4. stdout is a single valid JSON document (pipe it through a JSON parser to confirm).
5. Exit code is 0 on success; non-zero on error; errors/progress go to **stderr only**.
6. `--verbose` prints progress to stderr; without it stderr stays quiet on success.

If a capability genuinely cannot run in this environment (e.g. a missing model that
can't be obtained), DEGRADE GRACEFULLY: emit valid JSON with a clear
`"skipped"/"reason"` field and exit 0 for the optional part — never crash, never emit
half-JSON. Document the degradation in your final report.

## Build root & module

- Build root: `X:\AI-2\becky-tools\becky-go\` (Go module `becky-go`, go 1.26).
- Your code goes in `cmd\<name>\main.go` (one package main per tool).
- Binaries build to `becky-go\bin\`.
- After your tool builds & passes, append `<name>` to the `set TOOLS=` line in
  `becky-go\build-all-tools.bat` (single-line edit only) and update ONLY your tool's
  row in `X:\AI-2\becky-tools\PROGRESS.md`. Do not reformat or touch other rows/tools.

## Reuse the shared packages — DO NOT reinvent them

Read these before writing anything (they define the house style; match it):

- `becky-go\internal\beckyio\beckyio.go` — `PrintJSON(v)`, `Fatalf(fmt,...)`,
  `Logf(verbose, fmt,...)`. Use these for ALL output. JSON to stdout, diagnostics to
  stderr, exit 1 on fatal.
- `becky-go\internal\config\config.go` — `config.Load()` returns paths for Python,
  models, ffmpeg/ffprobe, auto-editor, codec, device. NEVER hardcode a path that
  config already provides. If you need a new shared path, add a field here (and to
  `merge`/`defaults`) rather than hardcoding in your tool.
- `becky-go\internal\mediainfo\mediainfo.go` — `mediainfo.Probe(ffprobe, path)` →
  duration, fps, hasVideo, hasAudio.
- `becky-go\internal\pyhelpers\pyhelpers.go` — embeds Python glue via `//go:embed`
  and `Materialize(name, content)` writes it to a temp dir at runtime so the .exe is
  self-contained. If your tool needs a Python helper, add the `.py` to
  `internal\pyhelpers\`, add a `//go:embed` var, and materialize+exec it. Heavy
  compute stays in ONNX/sherpa/sentence-transformers; Python is glue only.

Reference implementations to copy patterns from (positional-arg parsing, exec of
external tools with captured stderr, temp-file handling, JSON shape):
- `becky-go\cmd\transcribe\main.go` (Python+sherpa helper pattern)
- `becky-go\cmd\cut\main.go` (auto-editor + ffmpeg + VAD post-pass, XML parsing)

## Hard rules (apply to every tool — violating these fails the task)

1. **JSON in / JSON out.** File path(s) on argv; JSON to stdout; errors to stderr;
   exit codes. No interactive prompts, no TUI, no web UI.
2. **h264_nvenc, NEVER libx264.** Any video encode uses `cfg.Codec` (h264_nvenc).
3. **No LLM between pipeline steps.** Deterministic only. (Exception: becky-review IS
   the LLM step — that's its whole job; all other tools stay LLM-free.)
4. **Do not modify source videos.** `test.mp4` is read-only test input (safe to read).
5. **No Python for hot paths** — Python is only thin glue around ONNX/sherpa/ST.
6. **Don't change Jordan's locked settings** (auto-editor margin `0.04s,0.25s`, etc.).
7. Match the existing code's comment density and naming; keep files < 800 lines,
   functions < 50 lines where reasonable; handle errors explicitly with wrapped context.

## Verified environment (checked 2026-06-06 — trust these, no need to re-discover)

Interpreters and what they have:
- **kevs venv** = `X:\AI-2\kevs-obsidian-ingestion-engine\.venv\Scripts\python.exe`
  (this is what `config.Load().Python` returns). HAS: sentence_transformers, torch
  (CPU), transformers, sherpa_onnx 1.13.2 (incl. `OfflineSpeakerDiarization`,
  `SpeakerEmbeddingExtractor`), numpy. MISSING: onnxruntime, soundfile, librosa.
- **anaconda** = `C:\ProgramData\anaconda3\python.exe`. HAS everything kevs has PLUS
  soundfile, librosa. MISSING: onnxruntime. Use this if you need soundfile/librosa.

External tools:
- go: `C:\Program Files\Go\bin\go.exe`
- ffmpeg/ffprobe: `C:\ProgramData\anaconda3\Library\bin\` (via `cfg.FFmpeg`/`cfg.FFprobe`)
- auto-editor: `C:\Users\only1\bin\auto-editor.exe` (via `cfg.AutoEditor`)
- claude CLI: `claude` (claude.ps1 on PATH) — for becky-review claude-code backend
- llama-server / llama-cli: `C:\llama.cpp\build\bin\`
- sqlite3 CLI: `C:\ProgramData\anaconda3\Library\bin\sqlite3.exe`

Models on disk (no download needed unless noted):
- Silero VAD: `X:\AI-2\becky-tools\models\silero_vad.onnx` (via `cfg.SileroVADModel`)
- Parakeet ASR: `...kevs...\models\asr\sherpa-onnx-nemo-parakeet-tdt-0.6b-v3-int8`
- pyannote-seg-3.0: `...kevs...\models\diarization\sherpa-onnx-pyannote-segmentation-3-0\model.onnx` (and `model.int8.onnx`)
- CAM++ speaker embedding (192-dim): `...kevs...\models\diarization\3dspeaker_speech_campplus_sv_en_voxceleb_16k.onnx`
- Qwen3-Embedding-0.6B (1024-dim, real weights cached): HF cache under
  `...kevs...\models\embeddings\` — load via sentence-transformers id `Qwen/Qwen3-Embedding-0.6B` with `cache_folder` pointed there.
- sqlite-vec extension: `...kevs...\models\sqlite-vec\vec0.dll`
- ArcFace face model: NOT present. Face matching is the only piece needing a download;
  make face OPTIONAL/graceful if you can't obtain it.

(`...kevs...` = `X:\AI-2\kevs-obsidian-ingestion-engine`)

## Proven recipes (copy these, don't rediscover)

- **Diarization (high-level, works in kevs venv, no onnxruntime needed):**
  `sherpa_onnx.OfflineSpeakerDiarization` with
  `OfflineSpeakerDiarizationConfig(segmentation=...pyannote model.onnx...,
  embedding=...CAM++ onnx..., clustering=FastClusteringConfig(num_clusters=-1, threshold=0.5))`.
  sherpa reads the wav itself; feed it 16k mono PCM wav (extract via ffmpeg first).
  Auto-detect speaker count with `num_clusters=-1` + a threshold. A lower-level
  reference also exists at `...kevs...\models\diarization\sherpa-onnx-pyannote-segmentation-3-0\speaker-diarization-onnx.py` (needs anaconda python for soundfile/librosa) — prefer the high-level API.
- **Voice embedding / identification (CAM++, dim 192):** see proven
  `...kevs...\scripts\enroll_voice.py` — `sherpa_onnx.SpeakerEmbeddingExtractorConfig(model=CAM++, provider="cpu")`,
  read wav, `accept_waveform` → `compute()` → 192-float embedding; cosine-compare.
- **Text embeddings (Qwen3, dim 1024):** see proven `...kevs...\scripts\embed_text.py` —
  `SentenceTransformer("Qwen/Qwen3-Embedding-0.6B", cache_folder=...models/embeddings...)`,
  `model.encode(texts, normalize_embeddings=True)`. Works in kevs venv.

## Audio extraction (standard 16k mono wav for any sherpa/VAD step)

```
ffmpeg -y -i <input> -vn -ac 1 -ar 16000 -acodec pcm_s16le -loglevel error out.wav
```

## A hook you will hit: the "Fact-Forcing Gate"

A PreToolUse hook may BLOCK your first `Write` of a new file and ask you to state 4
facts. When blocked, reply with these, then retry the same Write:
1. **Callers** — who/what will run this file (e.g. "becky-events runs becky-osint via
   the documented chain; invoked from CLI").
2. **No-dup** — confirm you checked existing code and are not duplicating an existing
   tool/helper (name what you checked).
3. **Data shape** — the input you read and the JSON you emit (1 line each).
4. **Verbatim instruction** — quote the line from your task file that requires this file.

## When you finish

Report back: what you built, the exact build command, the exact test command(s) you
ran against real data, a snippet of the real JSON output, any graceful degradations,
and confirmation that build-all-tools.bat + your PROGRESS.md row were updated.
