# becky-go

Offline, deterministic CLI tools for forensic video analysis (and general media work).
Each tool does ONE thing, takes a file/JSON in, writes JSON to stdout, exits with a code.
No network, no LLM between steps unless a tool explicitly calls a LOCAL model. Go binaries
with heavy ML compute pushed into ONNX / sherpa-onnx / InsightFace / llama.cpp via thin
embedded-Python helpers.

> **New Claude instance: read `../FORENSIC-OUTPUT-PHILOSOPHY.md` and the "Non-obvious decisions"
> section below BEFORE touching anything.** Most of the hard-won lessons are landmines that look
> like simple code but were each a real bug. Re-deriving them costs hours.

## Build / run
```bat
cd X:\AI-2\becky-tools\becky-go
build-all-tools.bat            REM builds every cmd\<tool> -> bin\becky-<tool>.exe (+ bin\becky.exe)
```
Go 1.26+. Add `bin\` to PATH to call tools by name. Each tool: `becky-<tool> --help`.
The friendly entry point is **`becky.exe`** (the orchestrator) — see `../SKILL.md`.

## Tool catalog (24)
| Group | Tools | Purpose |
|---|---|---|
| Media | `transcribe` (Parakeet ASR), `cut` (auto-editor+VAD jumpcuts), `vad`, `diarize` (who-speaks-when) | turn raw A/V into timed text + speaker spans |
| Identity/forensic | `identify` (voice+face+location → corroborated names), `events` (scene/phone/multi-face), `osint` (provenance frames + EXIF/GPS metadata), `validate` (Gemma-4 AV description), `framematch` (same-room exhibit), `motion` (sub-second movement localizer) | who/what/where, evidence-grade |
| Index/report | `embed` (Qwen3 vectors), `search` (hybrid FTS5+vec+OCR RRF), `ocr` (text off frames), `consolidate`, `review` (LLM annotate), `export` | searchable corpus + reports |
| Orchestration | `becky` (plain-language op runner), `enroll` (wiki→KB + `becky "this is X" <clip>`), `cluster` (recurring-unknown "Person A") | drive the toolset; build/grow the KB |
| Utility/meta | `web2md`, `deslop`, `debt-scan`, `eval` (recall harness), `pipeline` (chains the above) | |

## Non-obvious decisions (THE important part — each was a real bug or measured failure)

**Output: corroborate, then CONCLUDE (don't hedge).** `../FORENSIC-OUTPUT-PHILOSOPHY.md` governs
every human-facing finding. A lone weak signal → "unknown"/candidate; **≥2 independent signals
agreeing → state the conclusion plainly** (`identify`'s `fuse.go` emits one `corroborated` entry:
"Shelby, conf 0.94 (voice 0.80 + face 0.68)"). The earlier "never conclude / candidate-only"
framing was wrong — a flood of maybes a human must sort = tool failure.

**Recall is for DETECTION, not NAMING.** Every face/voice is surfaced (recall), but a NAME is only
attached above a confidence bar. `identify --face-threshold` default is **0.55, deliberately not
lower**: cross-person faces hit ~0.50, so chasing recall with a low bar produces false names. A
detected-but-unnamed person is reported as "unknown," never force-matched to the nearest enrollee.

**Face rotation.** Portrait phone video is stored landscape + a 90° display-rotation flag.
ffmpeg's *implicit* autorotate is unreliable (varies by build/hwaccel/decode path), so faces get
fed to SCRFD sideways and detection silently returns nothing — dropping enrolled people
corpus-wide. Fix: `osintexport.DisplayRotation` + explicit `transpose` under `-noautorotate` BEFORE
detection (`ExtractFrameRotated`). Also: sample frames densely (1s), not every 3s — a face can
flash by between sparse samples.

**Unicode paths (cv2).** `cv2.imread` returns None on non-ASCII paths on Windows → a name with "é"
(e.g. "née Kerberg") was silently unenrollable. `internal/pyhelpers/face_embed.py` reads via
`np.fromfile` + `cv2.imdecode`. **Any new cv2 image read MUST use this**, never `cv2.imread`.

**Gemma-4 AV (`validate`) is TWO-STAGE, and it's Gemma both stages.** Given many frames in one
request, Gemma-4-E4B averages them into one scene and drops subtle/occluded contact (measured:
zero contact recall). So: caption EACH frame in its own single-image request (pixel-grounded),
THEN synthesize the captions into the timeline — and the synthesis may add NOTHING not in a
caption. Drive it via **llama-server** (NOT `llama-mtmd-cli`, which crashes on Gemma-4),
`enable_thinking=false`, `-fa off`, context 16384. Per-frame captions are preserved in the output
(`visual` field) so a human can check what the model actually saw. Output uses PLAIN words
(butt/hips/thigh) — never clinical jargon ("iliac crest"), which is unreadable AND was still
missing the act.

**Gemma sees STILLS, not video.** It samples ~1 frame/sec; sub-second events (a quick touch) fall
between samples. `motion` (optical flow at true source fps, pure Go, zero VRAM) localizes the
burst and hands `validate` the exact 1-second window. No local video-LLM does true ~30fps in 8GB
VRAM (verified 2026-06) — a bigger model does NOT fix this; cheap motion math + targeted slow-model
is the answer.

**Diarization (the #1 blunder source).** Phantom-speaker fix = VAD speech-gating (strip
music/SFX/intro before clustering) + clustering threshold **0.7** + auto-mode `--min-speaker-frac`
**0.15** (drops spurious cross-talk clusters). Single-speaker clip → 1 speaker. `identify`'s
internal diarizer passes the same values so the two agree.

**Voice >> face reliability.** CAM++ voice margin is huge (same-person ~0.76–0.91 vs different
~0.03), so `--voice-threshold 0.45` is safe. Face is the weak modality. The deployed CAM++ model
outputs **512-d** (old docs said 192 — wrong).

**Search = hybrid, no cgo.** sqlite-vec (`vec0.dll`) + FTS5, fused with RRF in `cmd/search`.
Vectors are stored as JSON-array TEXT and driven through the `sqlite3.exe` CLI (no cgo). Embeddings
= Qwen3-Embedding-4B via a resident `llama-server --embedding --pooling last`, MRL-truncated to
1024 + L2-normalized; an `embed_meta` model-tag guard prevents silently mixing vector spaces.
**OCR text (`ocr_text`) is fused into the same `becky find`** — on-screen text and speech are one
search.

**Reuse YouTube sidecars; never trust mtime.** Most corpus videos are yt-dlp'd with `.srt`/`.vtt`/
`.info.json`/`.live_chat.json`. `pipeline`/`index` reuse the subtitle as the first-pass transcript
(`transcript_source: youtube-srt`, Parakeet skipped; `--force-transcribe` for verbatim) and ingest
the metadata. **Capture time comes from EXIF/QuickTime** (`osint` metadata), with `capture_time_source`
labeled — file mtime is an evidence-integrity landmine and is only ever a fallback marked
`mtime(untrusted)`. **No clip in this corpus has GPS** → location comes from OCR + framematch, not
coordinates.

**Enrollment: the wiki is the entity source.** `becky enroll-wiki` crawls the case wiki (each
person's `.md` references their videos) and auto-builds the KB — zero manual clip-making. Confirm a
new person from any clip with natural language: `becky "this is Shelby" <clip>` (no "enroll" word).
KB layout: `voice-prints/<name>/*.wav`, `face-prints/<name>/*.jpg`, `entities/<name>.json`,
`enrollment-registry.json`. `cluster` groups recurring UNKNOWNS as "Person A appears in N clips"
before anyone names them.

**The Fact-Forcing Gate.** A hook (outside this repo) blocks new-file writes/edits/destructive
commands until 4 facts are stated (who calls it / not a duplicate / data shape / verbatim user
instruction). That's why edits here are preceded by a facts preamble — it's intentional, not noise.

## Architecture
- `cmd/<tool>/main.go` → `bin/becky-<tool>.exe`. cmd packages never import each other.
- `internal/`: `beckyio` (stdout/stderr/exit), `config` (`~/.becky/config.json` + auto-detect),
  `mediainfo` (ffprobe), `pyhelpers` (`//go:embed`'d Python, materialized to temp + run as a
  subprocess — heavy compute is in ONNX/sherpa/insightface/llama.cpp, not Go), `osintexport`
  (frames + perceptual hash + provenance + rotation), `faceembed` (InsightFace buffalo_l runner,
  shared by identify+events), `exifmeta` (EXIF/QuickTime), `beckydb` (sqlite schema, KNN, FTS, RRF,
  `ocr_text`, cluster tables), `sidecar` (srt/vtt/json3/info.json/live_chat), `avlm` (Gemma-4
  llama-server transport + two-stage captioning).
- **PYTHONPATH gotcha:** insightface/onnxruntime live in `X:\PythonUserBase\Lib\site-packages`
  (a `--target` dir not on the default path); helpers export PYTHONPATH to find them.

## Extensibility (how to add tools / use across domains)
ONE toolbox, FLEXIBLE tools. A tool serves multiple domains via FLAGS/subcommands, not duplication
— e.g. one `becky-cut` does forensic silence-removal AND tiktok jumpcuts via flags; you do NOT need
two. Shared logic lives in `internal/` so nothing is copy-pasted. Each tool is also usable
STANDALONE (`becky-ocr <img>` works on its own). Adding a tool = a new `cmd/<tool>` + an entry in
`build-all-tools.bat` + (if it needs data) a self-contained `internal/beckydb/<tool>.go` table.
100+ unrelated tools in one module is fine. See `../SPEC-BECKY-NEW-TOOL.md` (the deterministic
tool-build pipeline) and `../SPEC-BECKY-ASK.md` (the natural-language front-door).

## Governing docs (read these, not just code)
`../FORENSIC-OUTPUT-PHILOSOPHY.md` (output rules) · `../SKILL.md` (usage) · `../BUILD-AGENT-BRIEFING.md`
(build conventions/guardrails) · `../AUTORESEARCH-SPEC.md` (eval-driven tuning) · `../TEST-FEEDBACK.md`
(the cross-AI test loop) · `../SPEC-*.md` (proposed tools, awaiting approval).

## Conventions (every tool)
JSON to stdout, diagnostics to stderr, exit 0 on success / nonzero on error, stderr silent without
`--verbose`. `h264_nvenc` never `libx264`. Never modify source videos (work on copies/temp).
Degrade gracefully (missing model/dep → a note + exit 0, never a crash or a fake result).
`--margin 0.04s,0.25s` is the locked auto-editor setting.
