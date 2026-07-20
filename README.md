# becky-tools

Offline, deterministic CLI tools for analyzing video and audio files. Each tool does ONE thing,
takes a file/JSON in, writes JSON to stdout, exits with a code. No network, no LLM between steps
unless a tool explicitly calls a **local** model. Go binaries with heavy ML compute pushed into
ONNX / sherpa-onnx / InsightFace / llama.cpp via thin embedded-Python helpers.

**Primary use case:** forensic investigation of video footage — WHO is in it, WHAT is said
(with timestamps), WHAT happens on screen, and WHERE. But the tools are general-purpose: the
same `becky-transcribe` that processes evidence footage will transcribe a podcast, a lecture,
or a music video. The same `becky-search` hybrid search works over any indexed transcript corpus.

> **New instance:** read `FORENSIC-OUTPUT-PHILOSOPHY.md` and the "Non-obvious decisions" section
> below BEFORE touching anything. Most hard-won lessons look like simple code but each was a
> real bug. Re-deriving them costs hours.

## Quick start

See **`SKILL.md`** for the full usage guide (human and agent). Short version:

```bat
REM Add becky-go\bin\ to PATH (or call by full path)
becky enroll-wiki --wiki "path\to\wiki" --kb kb-final
becky profile "Person Name" --kb kb-final --corpus "video-or-folder"
becky find "search term" --db forensic.db
becky "this is Shelby" clip.mp4     REM teach a new person from a clip
```

Each tool is also callable standalone: `becky-transcribe video.mp4 --format srt`

## Build

```bat
cd becky-go
build-all-tools.bat    REM builds every cmd\<tool> -> bin\becky-<tool>.exe (+ bin\becky.exe)
```

Requires Go 1.26+. Add `becky-go\bin\` to PATH to call tools by name.
The friendly entry point is **`becky.exe`** (the orchestrator).

## Tool catalog (24+)

| Group | Tools | Purpose |
|---|---|---|
| Media | `transcribe` (Parakeet ASR), `cut` (auto-editor+VAD jumpcuts), `vad`, `diarize` (who-speaks-when), `subtitle` (**cut-snapped burn-in captions**) | turn raw A/V into timed text + speaker spans |
| Identity/forensic | `identify` (voice+face+location → corroborated names), `events` (scene/phone/multi-face), `osint` (provenance frames + EXIF/GPS metadata), `validate` (Gemma-4 AV description), `framematch` (same-room exhibit), `motion` (sub-second movement localizer) | who/what/where, evidence-grade |
| Index/report | `embed` (Qwen3 vectors), `search` (hybrid FTS5+vec+OCR RRF), `ocr` (text off frames), `consolidate`, `review` (LLM annotate), `export` | searchable corpus + reports |
| Orchestration | `becky` (plain-language op runner), `enroll` (wiki→KB + `becky "this is X" <clip>`), `cluster` (recurring-unknown "Person A"), `ask` (TUI front-door, saves output next to source) | drive the toolset; build/grow the KB |
| Utility/meta | `web2md`, `deslop`, `debt-scan`, `eval` (recall harness), `pipeline` (chains the above), `new-tool` (AI-assisted tool scaffolding) | |
| Music / DAW | `compose` (genre→MIDI stems), `hum`, `vox`, `mix`, `drum`, `wire`, `reaper` (**AI-first DAW: authors REAPER `.rpp` sessions + drives REAPER, which hosts all his VSTs**) | becky as the AI brain over a real DAW |

## Captions that do not flash (`becky-subtitle`)

Captions timed off a raw transcript drift against the edit, so at every cut one blinks on or off
for a few frames — jarring enough that Jordan captioned by hand instead. `becky-subtitle` snaps
each caption to the cut it lives in and closes every gap between them (the rules ported from the
pre-Go `cli-cut` `build_master_srt`, which was left behind when `becky-cut` was ported).

One call does the whole job — it imports the edit, transcribes the source if needed, chunks on the
speaker's pacing, and burns the result in:

```bat
becky-subtitle --edit post_constantly.xml --burn post_constantly.mp4
```

One-click: **`Caption This Edit.bat`** (drag a Vegas edit onto it).

Two things worth knowing:

- **The pause threshold is derived from the transcript, not hardcoded.** `cli-cut`'s 0.120s constant
  assumed an ASR with tight word boundaries. Parakeet quantises to 0.08s and leaves ~49% of words
  with `end == start`, so its ordinary connected-speech gap is 0.16-0.24s — above the constant, which
  breaks after nearly every word (measured: 421 captions from 631 words). `subs.AutoGapSeconds` takes
  the 90th percentile of the transcript's own gaps instead (0.32s here → 180 captions, 3-4 words each).
- **Captions are NOT lowercased by default**, unlike `cli-cut`. Jordan's published captions keep
  sentence case and punctuation ("Their label doesn't want you..."). `--lower` restores the old look.

## Importing an edit you already cut (`becky-otio --import`)

Export was one-way by design (`SPEC-BECKY-OTIO.md` non-goals). It no longer is: Jordan cuts in Vegas
Pro, and those cuts have to reach becky without being redone by hand.

```bat
becky-otio --import post_constantly.xml    REM or the Vegas .txt
```

Reads a Vegas **EDL TXT** (`.txt`, milliseconds, absolute paths per event) or a **Final Cut Pro 7
XML** (`.xml`, source frames at the sequence rate) and writes a becky reel — which every surface
already understands, so an imported Vegas edit opens in Becky Review with **Load Reel** and captions
with `becky-subtitle`. Verified on a real 88-cut edit: both files import to 88 clips and agree to
**0.31 ms** (1/100th of a frame).

Traps handled, both real: FCP7 declares a media path once and refers to it by id afterwards — in a
Vegas export that declaration sits in the **bin**, so paths are collected document-wide before the
sequence is read (resolving only from the sequence imports zero clips); and the bin's own `<clipitem>`
spans the whole file, so it must not be imported as a 5-minute event.

## AI-first DAW (REAPER)

The functional AI-first DAW is **becky driving REAPER** (already installed, fully scriptable
via ReaScript/Lua, plain-text `.rpp`, hosts every VST). `becky-reaper` deterministically
authors a real REAPER session from `dawmodel.Arrangement` (tracks, Cubase-style bus folders,
MIDI, render config) — proven: REAPER rendered an audible 24-bit/48k WAV and a generated
17-track session opens with the full bus tree. One-click: **`Open Becky DAW.bat`**. Full
detail: `SPEC-BECKY-REAPER.md`.

> **Local LLM = llama.cpp, ALWAYS (NOT Ollama).** Every becky local-model path uses
> llama.cpp's `llama-server` (OpenAI-compatible `/v1/chat/completions`), per `internal/llmlocal`.
> The in-REAPER "REAPER Chat" extension expects a server on `http://localhost:11435/v1/chat/completions`.
> **`becky-reaper brain --start`** boots that server for you (resolves a chat GGUF + `llama-server`
> and binds them to :11435); **`Start Becky REAPER Brain.bat`** is the one-click launcher, and
> **`Open Becky DAW.bat`** auto-starts the brain if the port isn't already serving. Do **not** use Ollama.

## Becky Canvas (the central hub)

**`becky-canvas` is the one window Jordan opens** — a native Gio app (no browser; `GUI-RULES.md`).
Its left dock has the canvas modes (draw / piano / drum / video / record) plus, below a divider, a
**HUB** of launch buttons that open the real standalone tools in their own windows, so there's no
hunting through folders:

| Hub button | Opens |
|---|---|
| **Drum Machine** | `becky-drummachine` — the 16-pad Maschine-class groovebox (real kit loading + sample browser) |
| **REAPER DAW** | `becky-reaper open` — authors a Cubase-style session and opens it in REAPER |
| **Clip** | `becky-clip` — forensic transcript video editor |
| **NLE** | `becky-nle` — AI video editor |
| **Ask** | `becky-ask` — the natural-language chat front-door |

Open it with **`Open Becky Canvas.bat`** or the Desktop **"Becky Canvas"** shortcut. Drop a
becky-compose `project.json` (or a `.mid`) on it and the **in-window panels** work on the real
session over ONE `dawmodel.Arrangement` spine: a **piano roll** (add/move/resize/velocity), a
**drum machine** (lanes×steps), and a **mixer** (fader/pan/mute/solo/route) — all editing the
arrangement by hand via the immutable dawmodel verbs, with `internal/ctledit` as the deterministic
AI-edit applier behind the agent box. The remaining convergence (audio/vocal panel, VST rack,
drum bar-paging, the model→batch half of select→ask→transform) is tracked in `CANVAS-BLUEPRINT.md`.
All GUI windows build as `-tags gui -ldflags "-H windowsgui"` (no console flash) via
`build-all-tools.bat`.

## Non-obvious decisions

**Each item below was a real bug or measured failure — not an opinion.**

**Output: corroborate, then CONCLUDE (don't hedge).** `FORENSIC-OUTPUT-PHILOSOPHY.md` governs
every human-facing finding. A lone weak signal → "unknown"/candidate; **≥2 independent signals
agreeing → state the conclusion plainly** (`identify`'s `fuse.go` emits one `corroborated` entry:
"Shelby, conf 0.94 (voice 0.80 + face 0.68)"). A flood of maybes a human must sort = tool failure.

**Recall is for DETECTION, not NAMING.** Every face/voice is surfaced (recall), but a NAME is only
attached above a confidence bar. `identify --face-threshold` default is **0.55, deliberately not
lower**: cross-person faces hit ~0.50, so chasing recall with a low bar produces false names. A
detected-but-unnamed person is reported as "unknown," never force-matched to the nearest enrollee.

**Voice threshold: use measured distribution, not the flag default.** CAM++ same-person voice
similarity runs **0.76–0.91**; different persons ~0.03. The `--voice-threshold 0.45` default was
too permissive — a 0.73 match (below the 0.76 same-person floor) still produced a confident but
wrong name on a real corpus. The default has been calibrated to 0.45 but watch for false
positives when your enrolled KB is small (3 people → any male voice that isn't a strong John
match lands on the next-nearest male). When in doubt, pass `--voice-threshold 0.75`.

**Face rotation.** Portrait phone video is stored landscape + a 90° display-rotation flag.
ffmpeg's implicit autorotate is unreliable (varies by build/hwaccel/decode path), so faces get
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
(`visual` field) so a human can check what the model actually saw.

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
~0.03), so `--voice-threshold 0.45` is safe in clean conditions. Face is the weak modality. The
deployed CAM++ model outputs **512-d** (old docs said 192 — wrong).

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
`mtime(untrusted)`.

**Enrollment: the wiki is the entity source.** `becky enroll-wiki` crawls the project wiki
(each person's `.md` references their videos) and auto-builds the KB — zero manual clip-making.
Confirm a new person from any clip with natural language: `becky "this is <name>" <clip>`.
KB layout: `voice-prints/<name>/*.wav`, `face-prints/<name>/*.jpg`, `entities/<name>.json`,
`enrollment-registry.json`. `cluster` groups recurring UNKNOWNS as "Person A appears in N clips"
before anyone names them.

## Architecture

- `becky-go/cmd/<tool>/main.go` → `bin/becky-<tool>.exe`. cmd packages never import each other.
- `becky-go/internal/`: `beckyio` (stdout/stderr/exit), `config` (`~/.becky/config.json` + auto-detect),
  `mediainfo` (ffprobe), `pyhelpers` (`//go:embed`'d Python, materialized to temp + run as a
  subprocess), `osintexport` (frames + perceptual hash + provenance + rotation), `faceembed`
  (InsightFace buffalo_l runner, shared by identify+events), `exifmeta` (EXIF/QuickTime),
  `beckydb` (sqlite schema, KNN, FTS, RRF, `ocr_text`, cluster tables), `sidecar`
  (srt/vtt/json3/info.json/live_chat), `avlm` (Gemma-4 llama-server transport + two-stage
  captioning), `agentrun` (shell-out to Claude Code for AI-assisted new-tool generation).
- **PYTHONPATH gotcha:** insightface/onnxruntime live in `X:\PythonUserBase\Lib\site-packages`
  (a `--target` dir not on the default path); helpers export PYTHONPATH to find them.

## Extensibility

ONE toolbox, FLEXIBLE tools. A tool serves multiple domains via FLAGS/subcommands, not duplication
— e.g. one `becky-cut` does forensic silence-removal AND TikTok jumpcuts via flags. Shared logic
lives in `internal/` so nothing is copy-pasted. Each tool is also usable STANDALONE.
Adding a tool = a new `cmd/<tool>` + an entry in `build-all-tools.bat`.
See `SPEC-BECKY-NEW-TOOL.md` (the deterministic tool-build pipeline).

## Governing docs

| File | Purpose |
|---|---|
| `SKILL.md` | **Usage guide** — start here to USE the tools |
| `FORENSIC-OUTPUT-PHILOSOPHY.md` | Output rules: when to conclude vs hedge, plain language |
| `BUILD-AGENT-BRIEFING.md` | Build conventions / guardrails for new tools |
| `SPEC-BECKY-NEW-TOOL.md` | The deterministic tool-build pipeline |
| `SPEC-BECKY-ASK.md` | becky-ask (TUI front-door) spec |
| `SPEC-*.md` | Proposed new tools (awaiting implementation) |

## Conventions (every tool)

JSON to stdout, diagnostics to stderr, exit 0 on success / nonzero on error, stderr silent without
`--verbose`. `h264_nvenc` never `libx264`. Never modify source videos (work on copies/temp).
Degrade gracefully (missing model/dep → a note + exit 0, never a crash or a fake result).
`--margin 0.04s,0.25s` is the locked auto-editor setting.

## Roadmap & Known Issues

### Critical

**Voice-ID wrong-person on small KB** (`identify`, `--voice-threshold`)
CAM++ same-person similarity runs 0.76–0.91; a 0.73 match is below that floor but still above the
0.45 default and produced a confident wrong name on a 3-person KB (any male voice that isn't a
strong match lands on the next-nearest male). **Fix pending:** raise voice-name bar to ~0.75,
emit top-2 candidate margin in output so downstream steps can catch weak matches, add
`--cast "Name1,Name2"` plausibility guard to suppress enrollees known to be absent from a corpus.

### High

**Face crops are torso-only on talking-head footage** (`osint`/`identify`)
When SCRFD detects a face, the saved frame is the full scene — the face bbox is not cropped to a
tight artifact. This makes "teach becky this person" unreliable on footage where faces are small
or off-center. **Fix pending:** save a tight face-crop (+margin) as its own artifact, write the
face embedding into `forensic.db`.

**`becky-cluster` is not yet built.** The spec is in `SPEC-PERSON-CLUSTERING.md`. Right now,
recurring-unknown grouping must be done manually. This is the highest-priority missing piece.

**framematch unreliable on portrait talking-head footage**
Whole-frame pHash keys on the subject's body silhouette, not background fixtures, so same-room
pairs get missed and different-room global-color-tone matches get false positives. **Fix pending:**
pre-crop to ceiling band before hashing, and/or keypoint/feature-match static decor.

### Medium

**becky-ask is interactive-only** — no scriptable mode. There's no `--image <f> --question "<q>"`
single-shot CLI. This blocks programmatic use (e.g. overnight cross-checking). **Fix pending:**
add non-interactive `--image --question` mode that prints answer + exits.

**Enrollment UX:** when `identify` leaves someone unnamed, it doesn't tell you how to fix it.
**Pending:** output should include the remedy inline — `"not enrolled — teach me: becky \"this is <name>\" <clip>"`.

### Planned features (next)

- `becky ingest <folder> --kb kb-final` — one-command corpus ingest that also writes
  `DIGEST.md` (per-clip: capture-time, who/what/where summary, unknowns) so an LLM reads a
  digest, not 8 raw JSON files.
- `becky dates <folder>` — EXIF/metadata/in-frame-timestamp triangulation → one-line dating
  verdict + basis per clip.
- `becky location <video...>` — room-fingerprint report (distinct room sets, same/different-dwelling verdict).
- `becky-cluster` — group recurring unknown faces as "Person A" across clips (spec ready).
- `--cast` flag on `identify` — per-corpus expected-cast filter.
- Face-naming "who is this?" loop — local TUI with image + big text/voice input box, zero
  cloud-LLM credits: `cluster` → `becky-ask` → `enroll` the cluster.

### Low

**validate 0-obs edge case** — one 54MB/35s 1920×1080 clip returned 0 observations (exit 0) while
all other clips produced results. Likely a frame-extraction or window/timeout edge case.

**pipeline step-dependency silently skips** — if you omit `events` from `--steps`, then `osint`
and `ocr` skip with `"missing-dependency"` (logged clearly). Just add `events` to your steps list.
