# BECKY-CLIP-HANDOFF.md — read this BEFORE you change becky-clip

> **Purpose:** the single file to point a fresh agent (or future-Jordan) at before iterating on
> becky-clip. `SPEC-BECKY-CLIP.md` says *what it is*; THIS file says *what will bite you* — the
> mistakes already made, the non-obvious logic, and the dead ends already ruled out, so we stop
> re-making solved mistakes. When you learn a new non-obvious thing, ADD it here.
> Last updated 2026-06-18 (initial build + first round of Jordan's fixes).

---

## 0. What it is, in one breath
A forensic, transcript-based, AI-first video **compilation** editor. Detective opens a 500GB case
folder → searches transcripts (keyword, or asks "becky") → clicks a quote to preview that exact
moment → double-clicks to drop the clip on a timeline → burns an unobtrusive forensic lower-third
→ exports ONE compilation MP4 (+ EDL + re-based SRT + frame stills). Originals are NEVER modified.

## 1. How to build / run it (do this, not something clever)
- **Jordan's way:** double-click `Build Becky Clip.bat` (repo root). Builds the gui exe, makes a
  Desktop "Becky Clip" icon, opens it.
- **Agent's way (the REAL exe — note the build tag + cgo off):**
  `cd becky-go && CGO_ENABLED=0 go build -tags gui -o bin/becky-clip.exe ./cmd/clip`
- `build-all-tools.bat` also produces it, but ONLY because we special-cased `cmd/clip` to build
  with `-tags gui` (like canvas). Its generic loop builds the **headless stub** — if you ever
  remove that special case, the suite will overwrite the real window with a stub. (See §3.7.)
- `go build ./...` / `go test ./...` (NO tags) MUST stay green — the window code is build-tagged
  off by default (see §3.6). Run `go test ./cmd/clip/... ./internal/reel/... ./internal/quotes/...
  ./internal/assistant/... ./internal/footage/... ./internal/edl/... ./internal/llmlocal/...`.

## 2. The file map (where things live — don't re-derive)
**Engine (pure Go, deterministic, cross-platform, offline-testable):**
- `internal/edl/` — the multi-source clip-list/EDL model. `Reel{Version,Name,Clips,Overlay,Created}`,
  `Clip{ID,Source,In,Out,Label,Meta}`, `ClipMeta{Date,Link,Person,Location,SourceFPS}`,
  `Overlay{Enabled,Show*,Position}`. `Load/Save`, `WriteEDL` (CMX3600), `WriteSRT` (re-based to the
  COMPILATION timeline), `SecondsToTimecode(sec,fps)`. **This struct is THE contract** — the GUI,
  reel, and assistant all serialize it. Don't rename fields.
- `internal/reel/` + `cmd/reel/` — ffmpeg render. `reel.Render(edl.Reel, Options) (Result, error)`,
  `GrabFrame(src,t,outPNG)`, `Proxy(src,outDir)`. `args.go` builds the ffmpeg argv; `drawtext.go`
  is the lower-third; `escape.go` is Windows filtergraph quoting; `resolveOptions` does the
  first-clip auto-dimension (§3.9).
- `internal/quotes/` + `cmd/quotes/` — the AI quote-finder. `Selector` interface +
  `ExactSelector` / `JSONSelector` / `LocalSelector`. Emits a verbatim-timestamped `_QUOTES.srt`.
- `internal/footage/` — read-only case-folder index. `Index(folder)`, `GrepTranscripts(idx,terms)`,
  `LoadMeta/SaveMeta` (the `<video>.beckymeta.json` sidecar — the ONLY place metadata is written).
- `internal/llmlocal/` — shared llama-server transport (lifted from `cmd/ask/llama.go`).
- `internal/assistant/` — "becky" the router. `Router.Handle(ctx,utt,Context,[]footage.Candidate)
  (Proposal,error)`, `Router.Apply(id)`/`Reject(id)`. `Action{Verb,Args}`, `Proposal{...}`,
  11 verbs, `Backend` interface (`Available() error`).
**Shell (Windows-only, gated):**
- `cmd/clip/` — `app.go` (state + handlers), `bridge.go` (the `beckyCall(verb,argsJSON)` dispatch —
  default-deny allowlist), `server.go` (localhost http + `/media` ServeFile), `export.go`,
  `window_gui.go` (`//go:build gui && windows` — the WebView2 window), `main.go`
  (`//go:build !gui || !windows` — headless stub), `folderpicker_windows.go` / `_other.go`,
  `assets/{index.html,app.css,app.js}` (embedded via `go:embed`).

## 3. GOTCHAS & MISTAKES ALREADY MADE — do not repeat these
1. **`.bat`/`.ps1` launchers MUST be ASCII-only + the `.bat` must `pause`.** A double-clicked
   `.bat` runs Windows PowerShell **5.1**, which reads a BOM-less `.ps1` as the system ANSI
   codepage; one em-dash/smart-quote makes the WHOLE script fail to PARSE → window flashes shut,
   no error. This bit BOTH `Build Becky Clip.bat` and the cloud's `Build Becky Drum.bat`.
   Parse-check before shipping: `powershell -Command "$e=$null;[void][System.Management.Automation.Language.Parser]::ParseFile('x.ps1',[ref]$null,[ref]$e);$e"`. (Also in CLAUDE.md §3.)
2. **Frontend is WebView2, NOT C++/Qt — on purpose.** There is no MSVC/Qt6 toolchain on this PC
   (only mingw gcc); building Qt would eat a whole day. WebView2 (`github.com/jchv/go-webview2`,
   pure-Go, no cgo, uses the installed system runtime) is native+lightweight and built+verified
   the same day. The ENGINE is frontend-agnostic, so a Gio/mpv shell can be added later without
   touching it. Don't "upgrade" to Qt unless Jordan explicitly wants to pay the toolchain cost.
3. **ffmpeg `-c copy` is NOT frame-accurate.** It cuts at the nearest keyframe (off by a whole
   GOP / seconds — proven on camera in `becky-clip-work/research/R-CUT.md`). Forensic cuts
   RE-ENCODE, with input-seek `-ss <in>` placed **before** `-i`, plus `-t (out-in)`. (Jordan
   raised this himself; he's right.) Don't switch the export to `-c copy` for "speed".
4. **The `-t` (duration) flag must come BEFORE `-i`, not after.** After `-i` it's an *output*
   duration option and truncates the whole concat filtergraph to just the last clip (the first
   smoke render produced only 1 of 2 clips because of this). It's documented in `reel/args.go`.
5. **melt is NOT used for the lower-third — don't re-add it.** R-REUSE first recommended MLT
   `melt` (it's installed at `C:\Program Files\kdenlive\bin\melt.exe`), but a live test showed its
   `dynamictext #timecode#` renders the *compiled-timeline* position, NOT the original-file
   timecode a detective must verify against. ffmpeg `drawtext timecode=<src-in>:timecode_rate=
   <src-fps>` gives the correct original timecode. lossless-cut is a GPL Electron app — idea only,
   never bundle. (Full reasoning: R-CUT.md / R-REUSE.md.)
6. **`becky-reel` MUST allow `libx264`** (unlike `becky-export`, which forbids it). nvenc isn't
   available on a GPU-less box / can fail to init; reel tries `h264_nvenc` then falls back to
   `libx264` with a note. Don't copy becky-export's libx264 ban into reel.
7. **`build-all-tools.bat` special-cases `cmd/clip` to `-tags gui`** (right after canvas). Without
   it, the generic loop builds the headless stub and OVERWRITES the real window exe. If you add a
   new gui tool, add the same special case.
8. **The window code is gated `//go:build gui && windows`; a `//go:build !gui || !windows` stub
   keeps `go build ./...` green** on CI/Linux. `go-webview2` only imports inside the tagged file.
   Never import it from a non-tagged file or CI/Linux breaks.
9. **Auto-dimensions:** when `Options.Width/Height/FPS` are 0, `reel` probes the FIRST clip
   (ffprobe) and normalizes everything to it; explicit `--width/--height/--fps` still override;
   falls back to 1280x720/30 only when the first clip can't be probed. IMPORTANT: the lower-third
   `timecode_rate` always uses each clip's **own source fps** (the verification anchor), which is
   independent of the matched output fps. Tests fake the probe — don't make them need ffprobe.
10. **The native folder picker is a `powershell -STA` FolderBrowserDialog exec** (cgo-free,
    `folderpicker_windows.go`). It's a modal dialog, so it can't be unit-click-tested — the wiring
    is unit-tested, the actual pop must be eyeballed. If Jordan says "Open folder does nothing,"
    this is the first suspect (STA threading / focus / exec).
11. **Originals are sacred.** Metadata lives ONLY in `<video>.beckymeta.json` sidecars; `becky-quotes`
    sha256-guards the source srt+video. Never write to a source video. Ever.
12. **Tests must stay green OFFLINE** (no ffmpeg, no models, no network, no claude). Every media/
    model/network boundary is behind a probe/seam with a fake in tests. Do NOT add a test that
    shells ffmpeg or calls a model — CI has neither.
13. **`gofmt -l .` lists many files on Windows — that's cosmetic CRLF, not real.** Files are stored
    LF in git (autocrlf), so Linux CI gofmt is green. Check your OWN new files with a scoped
    `gofmt -l <dir>`; don't try to "fix" the whole-repo list.

## 4. The "becky" assistant — wired vs not (the honest state + the next big job)
- **Tier 0 (deterministic, no model)** runs in the GUI today: keyword command parse + `footage`
  grep / `becky-search`. This is what works now.
- **Tier 1 (local GGUF via `internal/llmlocal`)** and **Tier 2 (frontier)** are BUILT and the
  `claude` CLI path is unit-verified, but they are **NOT yet driven end-to-end from the "ask becky"
  button.** THE NEXT MAJOR FEATURE: wire `Router.Handle` into the GUI chat so "find every time he
  offered money for the cat" → classify Tier-2 → `claude`/local produces JSON anchors → feed
  `becky-quotes --select-from-json` → show the found quotes as clickable results. The plumbing
  exists; it just needs the GUI button → router → quotes wiring + the propose-then-apply preview.
- **Verified `claude` CLI invocation** (use the aliases, model IDs drift):
  `claude -p --output-format json --model opus --append-system-prompt "<rules>" --max-turns 1`
  with the candidate block on **STDIN**; reply is the `{type,result}` envelope. NOTE:
  `--system-prompt-file` does NOT exist in this build — use `--append-system-prompt`.
- **500GB rule:** the model NEVER ingests the folder. `internal/assistant/funnel.go` does
  index → candidate-retrieve → token-bounded windows → ONE plan over the reduced set. Don't shove
  transcripts into a prompt.

## 5. Data contracts (the shapes everything agrees on)
- **EDL/Reel JSON** — `SPEC-BECKY-CLIP.md` §4 (the `Reel`/`Clip`/`ClipMeta`/`Overlay` struct).
- **`<video>.beckymeta.json`** — `{date (ISO YYYY-MM-DD), link, person, location, source_fps}`.
- **Action schema** — 11 verbs, propose-then-apply envelope — SPEC §8.
- **GUI bridge** — JS calls `beckyCall(verb, argsJSON)` (default-deny allowlist in `bridge.go`);
  media via `GET /media?path=` (folder-scoped, range-seekable). SPEC §9.

## 6. Where the evidence + reasoning lives (don't redo the research)
`becky-clip-work/research/` — `R-STACK.md` (WebView2 vs Gio decision + spikes), `R-REUSE.md`
(kdenlive/shotcut/melt/videogrep/lossless-cut verdicts + licenses), `R-CUT.md` (frame-accuracy
proof + tested ffmpeg recipes), `R-AI.md` (the router design + claude flags). Screenshots:
`shot-loop.png` (full loop on a demo case), `verify-launch.png` (fresh launch + becky rename).

## 7. P1 backlog (not blocking; in rough priority)
1. Wire "ask becky" plain-English search end-to-end (§4) — the headline AI feature.
2. Confirm the native folder picker pops for Jordan (§3.10); add a fallback if STA misbehaves.
3. Timeline polish: ripple/trim handles, drag-reorder finesse, markers/regions UX.
4. In-window OS file drag-drop (Gio had the same gap; WebView2 may need a small shim).
5. Exotic-codec preview proxies surfaced in the UI (engine supports `reel.Proxy` already).
6. Clean scratch: `becky-clip-work/{cut-tests,*-smoke}` (~13MB throwaway clips/screens; a delete-
   guard hook blocked auto-cleanup — safe to `rm -rf` by hand).

## 8. How to iterate safely (the loop)
1. Branch off master (`local/becky-clip-*`); never commit directly to master (a hook enforces it).
2. Make the change; keep `go build ./...` + offline tests green; `gofmt` your new files.
3. Build the real exe (`-tags gui`, `CGO_ENABLED=0`) and **actually launch it + screenshot** — the
   `.bat` bug existed because nobody ran it. Verify the specific thing you changed, by eye.
4. Parse-check any touched `.ps1` under 5.1 (§3.1).
5. FF-merge to master + push (local owns master; the cloud uses `claude/*` branches).
6. Update THIS file with any new gotcha, and `SPEC-BECKY-CLIP.md` §12 status.
