# SPEC-BECKY-NLE.md — the real video NLE: adopt Shotcut, add a Becky layer

> **STATUS: SPEC (2026-06-23). Design-only — NOT built. To be built FIRST (Jordan's
> priority: most immediately useful for everyday work).** Grounded in the research:
> `research/daw-nle-strategy-feasibility.md`, `research/bookmarks-video-nle-crawl.md`,
> and the becky-clip lessons (`SPEC-BECKY-CLIP.md` §1-9, `BECKY-CLIP-HANDOFF.md`).
> Pinned direction: **don't build an NLE from scratch — adopt a mature one and add the
> becky layer.** Reverses the from-scratch instinct that produced a "fancy .jpg."

---

## 0. TL;DR — what we are building and why

becky-clip had the **right idea** (point at a case folder → search the `.srt` transcripts →
click a quote → import the clip) but was **abandoned because it had no real video editing**
(no multi-track, trim/ripple, transitions, titles, audio mixing — it was a transcript
browser with an export button). The fix is not to build those — it's to **adopt a mature
open-source NLE that already has them, and add becky's forensic/quote/AI layer inside it.**

**Host = Shotcut** (mltframework/shotcut): C++ / **Qt6 + QML**, GPL-3, **MLT + FFmpeg**
engine — *the exact engine becky already writes* (`internal/kdenlive` emits MLT XML, `melt`
renders). Most active mainstream FOSS NLE (v26.x, 2026), best Windows build, and its **QML
UI** makes adding a "Becky" dock the cheapest of any NLE. (Runner-up: kdenlive — also MLT,
also becky-writable — heavier KF6 Windows build; keep as the documented fallback.)

**What becky adds inside Shotcut (the value):**
1. A **Becky dock** (QML panel) — point at a case folder → it ingests + indexes videos and
   transcripts (reusing `internal/footage`) and shows a **transcript/quote search**.
2. **Single-click a quote timestamp → the Shotcut preview seeks there and plays.**
3. **Double-click a quote → that clip is appended to the Shotcut timeline** at the playhead.
4. The **AI assistant** (becky's cost-tiered router) that can drive the editor + find quotes
   ("compile every bounty offer for the cat") — propose-preview-apply, never auto-mutates.
5. The **forensic non-negotiables**: originals are NEVER written; an optional lower-third
   with the running ORIGINAL-file timecode for provenance.

Everyday basic editing (the thing becky-clip lacked) comes **for free from Shotcut**:
multi-track timeline, trim/ripple/roll/slip, transitions, titles, audio, hundreds of
filters, proxy editing, GPU export. becky does not reimplement any of it.

---

## 1. Architecture — Shotcut host + thin Becky layer over the EXISTING Go engine

```
            ┌──────────────────────────────────────────────────────────┐
  Jordan ─► │  Becky NLE = forked Shotcut (Qt6/QML/MLT) — the editor    │
            │  ┌───────────────┐   ┌───────────────────────────────┐    │
            │  │ Shotcut native │   │  BECKY DOCK (new QML panel)    │    │
            │  │ timeline/prev/ │◄──┤  folder ingest · quote search │    │
            │  │ filters/export │   │  · AI chat · propose/preview  │    │
            │  └───────┬────────┘   └──────────────┬────────────────┘    │
            └──────────│───────── one window ──────│────────────────────┘
                       │ MLT (open/append/seek)    │ NDJSON over stdio / local socket
                       ▼                           ▼
                 MLT + FFmpeg            becky-nle-bridge (Go, NEW thin process)
                 (Shotcut's engine)      wraps the EXISTING becky engine:
                                         footage · quotes · edl · reel · assistant
                                                       │ shells out (the becky way)
                                                       ▼
                                         becky CLIs (becky-transcribe, becky-search,
                                         becky-quotes, becky-reel, …) = runtime "tools"
```

**Design rules (load-bearing):**
- **The brain stays Go; the dock is thin.** All heavy logic (folder index, transcript
  search, quote-finding, AI routing, render) lives in becky-go and is reused **as-is** — see
  the reuse map in §2. The QML dock is a UI that talks to a small **`becky-nle-bridge`** Go
  process over the existing NDJSON **seam** (`internal/seam`) or a localhost socket. This
  mirrors becky's "engine in Go, swappable shell" philosophy (SPEC-BECKY-CLIP §1) and means
  the dock never duplicates engine logic.
- **Shotcut is driven, not reimplemented.** The dock asks Shotcut to seek/play the preview
  and to append a clip to the timeline via Shotcut's own QML/C++ API (MLT producers). becky
  supplies the *what* (source path + in/out + label); Shotcut does the *how*.
- **Originals are read-only, always.** The folder is opened read-only; metadata lives in
  `<video>.beckymeta.json` sidecars (existing `internal/footage` contract); export writes to
  a `render/` subfolder of the case folder (the becky-clip rule).

---

## 2. Reuse map — becky already has the engine (do NOT rebuild it)

| becky package (EXISTING) | What it gives the NLE | Change |
|---|---|---|
| `internal/footage` | Read-only case-folder index: videos + `.srt` sidecars + `.beckymeta.json`; YouTube-`[id]` pairing; transcript-only quotes. | reuse |
| `internal/quotes` + `cmd/quotes` | The SRT quote brain: `--criteria` (AI-select), `--exact`, `--select-from-json`; verbatim-timestamped `_QUOTES.srt`; sha256 source guard. | reuse |
| `internal/edl` | The `Reel`/`Clip`/`ClipMeta`/`Overlay` model + CMX3600 EDL + re-based SRT. | reuse (also export to MLT) |
| `internal/reel` + `cmd/reel` | Headless ffmpeg render + forensic lower-third (running ORIGINAL-file timecode) + frame/proxy; nvenc→libx264 fallback. | reuse (becky's own quick-render path) |
| `internal/kdenlive` + `cmd/becky-nle` | Writes a real **MLT** project from a clip list; renders via `melt`. **Shotcut reads MLT** → this is how becky hands a built compilation to Shotcut. | reuse + extend (Shotcut-flavored MLT) |
| `internal/assistant` + `internal/llmlocal` | Cost-tiered AI router (deterministic→local Qwen→`claude` CLI/API), 11-verb propose-preview-apply action schema, 500 GB retrieval funnel. | reuse + add NLE verbs |
| `internal/sidecar`, `internal/mediainfo`, `cmd/transcribe`, `cmd/search` | SRT parse, ffprobe, make-an-SRT-when-missing, semantic retrieval. | reuse |

**NEW code (small, all under `becky-go/`):**
- `cmd/becky-nle-bridge/` — the thin Go process the QML dock talks to (NDJSON/socket);
  dispatches the action schema (§4) to the reused packages above. Pure Go, offline-green.
- The **Shotcut fork** (a separate C++/Qt repo checkout, NOT under `becky-go/`): the new
  **Becky dock** QML/C++ + the wiring to call the bridge and drive the timeline/preview.

---

## 3. The features Jordan named — exact contracts

**Folder → search → preview → import (the becky-clip workflow, now inside a real editor):**
1. **Open case folder** → bridge runs `footage.Index` (read-only) → the dock lists videos +
   shows transcript coverage. No original is touched.
2. **Search** the transcripts in the dock: keyword (literal SRT grep), semantic
   (`cmd/search`), or **ask the AI** ("find every threat to the host family"). Results are
   timestamped **quotes** (text + source path + cue start/end), incl. transcript-only quotes.
3. **SINGLE-CLICK a quote timestamp →** the dock sends `preview_clip(source, in, out)` to the
   bridge, which tells Shotcut to **open that source in the preview and seek+play from `in`**
   (Shotcut MLT producer + `position`/`play`). This is the "click a timestamp, it plays"
   requirement — verified by the preview actually moving + playing audio.
4. **DOUBLE-CLICK a quote →** `add_clip(source, in, out, label)` → Shotcut **appends that clip
   to the active timeline track at the playhead** (trimmed to in/out). Then Jordan edits it
   like any clip (the basics Shotcut already provides).
5. Optional **forensic lower-third** toggle (filename + running original-file timecode +
   date/person/location from the sidecar) — preview via overlay, export via `internal/reel`
   burn (the becky-clip §5 recipe).

**Acceptance scenario (must work end-to-end):** open `E:/TakingBack2007` → search "penguin"
→ single-click the first quote (preview seeks to 19:32 and plays) → double-click 3 quotes
(they land on the timeline) → trim/reorder in Shotcut → export → a real compilation MP4 in
`E:/TakingBack2007/render/` with audio. (becky-clip already proved every *engine* step on
this exact folder; the NLE wires them to a real editor.)

---

## 4. Extensibility — runtime tools/workflows WITHOUT recompiling (Jordan's question)

**Short answer: YES — and that's the whole point of the Becky layer.** Recompiling the
Shotcut fork is needed ONLY for deep native-UI changes. Everything Jordan means by "build
plugins / tools / workflows like the Claude Code agent" is designed to extend **at runtime**,
four ways:

1. **becky CLIs ARE the plugin system.** The in-app agent's action schema includes a
   `run_tool(name, args)` verb (default-deny allowlist). Drop a NEW `becky-go/cmd/<tool>`
   `.exe` into the bin → the agent can call it immediately. **No host recompile** — a new
   becky tool is a new capability the moment it builds. This is becky's existing pattern
   (`build-all-tools.bat` auto-discovers `cmd/*`).
2. **An embedded PTY / agent panel (Jordan's idea).** A terminal dock running `claude`
   (Claude Code) with **full app context** — the bridge writes the live session state
   (open folder, timeline, selection) to a file/socket the in-terminal agent reads, and the
   agent drives the NLE through the SAME action schema (§4). New *workflows* = new prompts /
   becky skills, authored and run live, **no recompile**. (This is what ACE-Step-DAW
   attempted poorly; we do it cleanly via the documented command bus.)
3. **Saved workflow macros.** A workflow is just a named JSON list of action-schema verbs
   (`search → find_quotes → add_clip×N → set_overlay → export`). Jordan (or the agent) saves
   one; replays it on any folder. Data, not code → **no recompile**.
4. **MLT/frei0r runtime effect plugins.** Shotcut/MLT load video/audio effects (frei0r,
   LADSPA, MLT services) from `.dll`/`.so` at startup — so new *effects* extend without
   rebuilding Shotcut. (Deep new *native UI* still needs a Shotcut rebuild — the one honest
   exception.)

**The action schema (the agent's + the dock's ONLY control surface — extends §8 of
SPEC-BECKY-CLIP):** `open_folder` · `search` · `find_quotes` · `preview_clip` ·
`add_clip` · `trim_clip` · `move_clip` · `add_track` · `set_marker` · `set_overlay` ·
`grab_frame` · `run_tool(name,args)` · `export`. Every verb dispatches to a deterministic
Go handler in the bridge; **nothing mutates the timeline until the human approves** (the
propose-preview-apply overlay becky already uses).

---

## 5. Build plan (Phase 0 is a SPIKE — verify before committing, per the no-churn rule)

**Phase 0 — SPIKE (local, ~1-2 days, go/no-go BEFORE any becky code):**
- [ ] Clone Shotcut; **build it on Jordan's Windows PC** (Qt6 + MLT + FFmpeg; follow
      Shotcut's documented Windows build / CI recipe). Prove it launches + edits a clip.
- [ ] Add a **minimal "Becky" dock** (one QML panel) that lists files from a chosen folder
      and, on click, tells the preview to open + play one of them. This proves the two hard
      unknowns: (a) Shotcut builds here, (b) a custom dock can drive the preview/timeline.
- [ ] **Go/no-go:** if the build is a swamp or the dock can't drive the preview → fall back
      to **kdenlive** (same MLT engine, also becky-writable) and re-spike. Report honestly.

**Phase 1 — the bridge (local, pure-Go, green offline):**
- [ ] `cmd/becky-nle-bridge` — NDJSON/socket server dispatching the §4 action schema to the
      reused `footage`/`quotes`/`edl`/`reel`/`assistant` packages. Value-asserting tests:
      each verb returns the expected JSON for a fixture folder; `run_tool` allowlist enforced.

**Phase 2 — wire the Becky dock to the bridge (local):**
- [ ] Folder ingest + transcript/quote search in the dock (reads the bridge).
- [ ] **Single-click → preview seek+play**; **double-click → append clip to timeline.**
      Verify by actually moving the preview + landing a clip (screenshot + the acceptance
      scenario on a real folder).
- [ ] Forensic lower-third toggle (preview overlay; export via `internal/reel`).

**Phase 3 — the AI + extensibility:**
- [ ] AI chat in the dock (becky's cost-tiered router; propose-preview-apply); `find_quotes`
      drives the editor.
- [ ] `run_tool` (becky CLIs as runtime tools) + the saved-workflow macros.
- [ ] (Stretch) the embedded PTY/Claude-Code panel with live app-state context.

**Verification standard (the becky bar):** "compiles" is NOT done. A phase is done when the
window opens, the named interaction *works on a real case folder*, and originals are
provably untouched (sha256). Screenshot/clip every claim. `go build/vet/test ./...` +
`gofmt` green for the bridge; the Shotcut fork builds on Windows.

---

## 6. Open decisions for Jordan

1. **Host = Shotcut** (vs kdenlive). Shotcut for the lighter Windows build + QML dock ease;
   kdenlive is the fallback. Both reuse becky's MLT writing. *Veto if you prefer kdenlive.*
2. **Name** of the forked editor (provisional "Becky NLE"). The existing headless
   `cmd/becky-nle` (MLT writer) stays the engine; the GUI fork needs its own name.
3. **Fork vs upstream-dock.** Plan = a maintained Shotcut fork with the Becky dock. We track
   upstream Shotcut releases and rebase the dock (small, isolated). OK?
4. **GPL-3:** the fork is GPL-3 (Shotcut is). becky-go stays its own license; the bridge talks
   over a process boundary (no GPL linkage into becky-go). Fine for personal use.

## 7. Non-goals
- Not reimplementing editing primitives Shotcut already has (timeline/trim/transitions/titles).
- Not loading the NLE into becky-canvas (separate app, launched on demand — the canvas/DAW is
  a sibling, spec'd in `SPEC-BECKY-DAW.md`).
- Originals are NEVER written. Export goes to `<case-folder>/render/` only.
