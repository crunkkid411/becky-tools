# SPEC-BECKY-CLIP.md — the forensic transcript-based video compilation editor

> **STATUS: BUILDING (2026-06-18, local agent, branch `local/becky-clip-2026-06-18`).**
> Architecture LOCKED by four evidence-based research passes (see
> `becky-clip-work/research/R-STACK.md`, `R-REUSE.md`, `R-CUT.md`, `R-AI.md` — each
> backed by live spikes + screenshots). This spec is the canonical contract every
> build agent codes against. If this spec and a research doc disagree on a *recipe*,
> the research doc's TESTED command wins; if they disagree on a *contract* (a struct
> field, an API shape, a tool boundary), this spec wins — fix the other and tell Jordan.

---

## 0. TL;DR — what we are building and why

A detective has **500 GB of footage** and recurring needs like *"compile every time the
defendant offered money for the victim's cat 'Penguin'"* or *"find all direct threats to
the host family."* Today that is hours of manual scrubbing + searching `.srt` in a second
window. **becky-clip** replaces that with a lightweight, **AI-first** transcript-based
editor:

1. Point it at a case folder (read-only over 500 GB).
2. **Search** the transcripts — by keyword, semantically, or by *asking the AI* ("find
   every bounty offer for the cat"). Results are timestamped quotes.
3. **Click a quote → it plays instantly** in the preview.
4. **Double-click a quote → that clip appends to the timeline** (at the cursor).
5. Arrange / ripple / trim like a normal editor; set markers/regions.
6. Toggle an **unobtrusive forensic lower-third** (source filename, *running
   original-file timecode*, date, link, person, location) so the detective can verify
   every frame against the original.
7. **Export** a single compilation MP4 (+ EDL + re-based SRT + frame stills).

The user talks to an **"Underlord"-style AI assistant** (right panel) that can drive the
*entire* program, is context-aware of the timeline + files + folder, and **never modifies
originals**. Deterministic-first: anything a single deterministic tool can do, it does;
a small local model makes cheap decisions; the frontier model (Jordan's Claude Max via the
`claude` CLI, or an API key) is reserved for the genuinely hard semantic work.

---

## 1. Architecture — engine core + thin swappable shell

The durable value is a **becky-native Go engine**; the GUI is a **thin client**. This split
is why we can ship something functional today and swap the frontend later if desired.

```
                 ┌─────────────────────────────────────────────┐
   detective ──► │  becky-clip.exe  (WebView2 window)           │
   + AI chat     │   HTML/CSS/JS UI  ◄──http+Bind──►  Go backend │
                 └───────────────┬─────────────────────────────┘
                                 │ shells out (becky way)
        ┌────────────────────────┼───────────────────────────────┐
        ▼                        ▼                                 ▼
   becky-quotes            becky-reel                       internal/assistant
   (search brain)     (deterministic media engine)        (cost-tiered AI router)
   srt+criteria →      EDL → ffmpeg render + burn          deterministic → local → frontier
   regions JSON        + EDL/SRT/frame export              emits the action schema (§8)
                                 │
                                 ▼  ffmpeg / ffprobe (already on PATH)
```

- **Standalone, launchable from becky-canvas.** `becky-clip` is its OWN binary, NOT loaded
  into canvas at launch (answers Jordan's worry). becky-canvas may `exec` it when video
  mode is requested — exactly how canvas already exec's `becky-daw-engine`. Canvas stays
  lightweight.
- **Frontend = WebView2 + Go** (`github.com/jchv/go-webview2`, `CGO_ENABLED=0`, uses the
  system WebView2 runtime already installed — no DLL to ship, ~10 MB binary). The `<video>`
  element gives instant click-to-seek preview for free; the AI drives the page via
  `w.Eval`/`Bind` (propose-then-apply). Evidence + the working spike:
  `becky-clip-work/spikes/webview2/` and `R-STACK.md`. **Reuse that spike as the starting
  point.** Fallback (documented, not today): Gio window + bundled mpv (`R-STACK.md`).
- **C++/Qt is OFF the table today** — no MSVC/Qt6 toolchain on this PC; only mingw gcc.
  Building Qt from source would eat the whole day. (This is why we deviated from Jordan's
  literal "C++" ask — the WebView2 result is native, lightweight, not Electron-bundled, and
  *actually buildable + verifiable today*. Reversible: the engine is frontend-agnostic.)

---

## 2. Tool set + file layout (becky conventions)

All new code lives under `becky-go/`. Each tool: JSON to stdout, diagnostics to stderr,
exit 0/nonzero, offline-by-default, **never modify source files**, degrade-never-crash,
`config.Load()` for tool paths. `cmd/*` imports no sibling `cmd/*` — shared logic in
`internal/`. `build-all-tools.bat` auto-discovers new `cmd/*` (no edit needed). `go build
./...` / `go test ./...` must stay green with NO build tags and NO models/ffmpeg present.

| Path | Role | New? |
|---|---|---|
| `internal/edl/` | The clip-list/EDL data model (§4) + CMX3600 EDL emitter + re-based SRT emitter. Pure Go, no exec. | NEW |
| `internal/reel/` | ffmpeg render: frame-accurate multi-source assemble + lower-third burn + frame→PNG + proxy. | NEW |
| `cmd/reel/` | `becky-reel` CLI over `internal/reel` (EDL JSON in → compilation MP4 / EDL / SRT / frame out). | NEW |
| `internal/quotes/` | Port of the deterministic SRT mapping/expand/emit half (§7) + a pluggable `Selector`. | NEW |
| `cmd/quotes/` | `becky-quotes` CLI (per `SPEC-BECKY-SRT-QUOTES.md`): srt + criteria → `_QUOTES.srt` + JSON. | NEW |
| `internal/footage/` | Read-only case-folder index: videos + transcript sidecars + `*.beckymeta.json`. | NEW |
| `internal/llmlocal/` | Lift `cmd/ask/llama.go`'s llama-server transport into a shared package. | NEW |
| `internal/assistant/` | The cost-tiered AI router + backends (deterministic / local / claude-CLI / API) + action schema. | NEW |
| `cmd/clip/` | `becky-clip` — the WebView2 GUI binary: HTTP media server + UI + wires all of the above. | NEW |

Reused as-is: `internal/sidecar` (SRT parse), `internal/config`, `internal/mediainfo`
(ffprobe), `internal/beckyio` (JSON/Fatalf/Logf), `cmd/search` (semantic retrieval),
`cmd/transcribe` (make an SRT when one is missing).

---

## 3. The detective's end-to-end flow (acceptance scenario)

> *"Compile every time he offered money for the cat."* — given a folder with the 3 source
> videos + their `.srt` sidecars:
> 1. Index the folder → 3 videos, transcripts found.
> 2. Ask the assistant; it runs `find_quotes` with criteria "offers money/reward/bounty for
>    delivering or returning the cat (Penguin)" over the transcripts (Tier-2 frontier on the
>    candidate windows only) → a ranked list of timestamped quotes across the 3 files.
> 3. Detective clicks each quote → preview seeks + plays that exact moment.
> 4. Double-clicks the real ones → clips append to the timeline.
> 5. Toggles the lower-third (filename + original timecode + date) ON.
> 6. Clicks Export → `becky-reel` renders one MP4 + an EDL + a re-based SRT; done in minutes.

Every step must also be doable **manually** (drag-drop a video, scrub, set in/out, ripple),
because the detective is in control — the AI just removes the grunt work.

---

## 4. EDL / clip-list JSON — THE core contract (do not change field names/types)

`internal/edl` owns these. `becky-reel`, the GUI, and the assistant all serialize this exact
shape. A `Reel` is a multi-source compilation (the gap in becky-cut's *single*-source v1
timeline). Times are **seconds (float64)** into each source.

```go
package edl

type Reel struct {
    Version string  `json:"version"` // "1"
    Name    string  `json:"name"`
    Clips   []Clip  `json:"clips"`
    Overlay Overlay `json:"overlay"` // defaults; per-clip Meta fills the values
    Created string  `json:"created,omitempty"`
}

type Clip struct {
    ID     string   `json:"id"`             // stable, e.g. "c1"
    Source string   `json:"source"`         // ABSOLUTE path to the source video (read-only)
    In     float64  `json:"in"`             // seconds into source (frame-accurate at export)
    Out    float64  `json:"out"`            // seconds into source
    Label  string   `json:"label,omitempty"`// e.g. the quote text
    Meta   ClipMeta `json:"meta"`
}

type ClipMeta struct {
    Date      string  `json:"date,omitempty"`       // recording date if known (ISO YYYY-MM-DD)
    Link      string  `json:"link,omitempty"`       // source URL if known
    Person    string  `json:"person,omitempty"`
    Location  string  `json:"location,omitempty"`
    SourceFPS float64 `json:"source_fps,omitempty"` // for the original-timecode burn
}

type Overlay struct {
    Enabled      bool   `json:"enabled"`
    ShowFilename bool   `json:"show_filename"`
    ShowTimecode bool   `json:"show_timecode"` // RUNNING ORIGINAL-FILE timecode (see §5)
    ShowDate     bool   `json:"show_date"`
    ShowLink     bool   `json:"show_link"`
    ShowPerson   bool   `json:"show_person"`
    ShowLocation bool   `json:"show_location"`
    Position     string `json:"position"`      // "bottom" (default) | "top"
}
```

- **Per-video metadata sidecar:** `<video>.beckymeta.json` holds `{date, link, person,
  location, source_fps}`. `internal/footage` reads it; the GUI/AI can write/update it.
  **This sidecar is the ONLY place metadata is stored — the original video is never
  touched.** A missing sidecar → empty meta, not an error.
- `internal/edl` provides: `Load(path)`, `Save(path, Reel)`, `WriteEDL(w, Reel)` (CMX3600 —
  reel-name table + `* FROM CLIP NAME:` comments), `WriteSRT(w, Reel)` (re-based: each
  clip's label/captions re-timed to the COMPILATION timeline, not the source). All
  deterministic + table-tested.

---

## 5. The forensic lower-third (Jordan's explicit requirement)

Unobtrusive but always-on so the detective has provenance on screen at all times. Lines
(each toggleable), bottom by default:

- **Source filename** (from `Clip.Source` basename).
- **Running ORIGINAL-file timecode** — as the compilation plays, this clip shows *its
  source's* timecode ticking from `Clip.In`. **This is the verification anchor.** R-CUT
  proved the correct recipe is ffmpeg `drawtext timecode='<In as HH\:MM\:SS\:FF>' :
  timecode_rate=<source_fps>` **per clip** (NOT melt's `#timecode#`, which shows timeline
  position — wrong number for verifying against the original).
- **Date of recording** (if known), **video link** (if known), optional **person** +
  **location** (from the sidecar).

Preview overlay = HTML/CSS over the `<video>` (cheap, live, toggle). Export overlay = ffmpeg
`drawtext` burn-in (authoritative). Exact tested recipes live in `R-CUT.md` §lower-third —
the reel agent implements from there.

---

## 6. Render / cut engine (decided in R-CUT — read it for exact commands)

- **DEFAULT = raw ffmpeg, one pass:** frame-accurate per-clip extract (input-seek `-ss <in>`
  before `-i`, `-t (out-in)`, **re-encode** — `-c copy` slips to the nearest keyframe, off by
  a whole GOP / seconds; PROVEN in `R-CUT.md`), normalize mixed sources to a common
  fps/resolution/SAR, `concat`, and `drawtext` lower-third — in a single deterministic pass.
- **Codec:** prefer `h264_nvenc` (Jordan's GPU). **CRITICAL:** unlike `becky-export`,
  `becky-reel` MUST fall back to `libx264` when nvenc init fails or on a GPU-less box —
  detect at runtime and degrade with a note, never hard-fail. (Tracked open issue from
  R-CUT.)
- **Frame export:** frame-accurate PNG by timestamp (`-ss <t>` accurate + `-frames:v 1`).
- **Proxy transcode:** for exotic codecs the `<video>` preview can't decode, ffprobe-detect →
  transcode a lightweight H.264 proxy. Preview uses the proxy; export uses the original.
- **auto-editor** stays available as an *optional* frame-accurate cut backend (it is in becky
  already), but it has no overlay (needs a 2nd ffmpeg pass) and Jordan reports becky-cut is
  buggy — so ffmpeg is the primary. **melt / lossless-cut: NOT integrated** (melt timecode is
  wrong for our need; lossless-cut is a GPL Electron app with no headless multi-source+burn).
- **License hygiene:** never bundle kdenlive/shotcut/lossless-cut (GPL). Reimplement
  videogrep's technique — **do NOT copy its code** (anti-law-enforcement license). `autocut`
  (Apache-2.0) is safe to borrow from. NOTICE: ffmpeg attribution.

---

## 7. becky-quotes — the AI quote-finding brain

Implements `SPEC-BECKY-SRT-QUOTES.md` (read it). srt + `--criteria` → an LLM selects the
important passages (NOT grep), recursively expands sentence context, snaps to verbatim cue
timestamps, emits `<video-stem>_QUOTES.srt` + a JSON summary (regions with
start/end/source-cue/text/rationale). Modes: default (LLM selection), `--exact "<phrase>|..."`
(literal, no model), `--select-from-json` (a stronger external model supplies anchors → tool
only expands+emits). Port the deterministic mapping/expand/merge/emit half from the proven
prototype `C:\Users\only1\Documents\Obsidian\llm-wiki-CLANCY-TRIAL\tools\triage\make_quote_srt.py`.

**The `Selector` seam is the integration point with the assistant (§8):** define
`type Selector interface { Select(transcript, criteria) ([]Anchor, error) }`. Implementations:
`ExactSelector` (literal), `JSONSelector` (read `--select-from-json`), `LocalSelector`
(llama-server via `internal/llmlocal`). The GUI's "ask the AI to find quotes" routes the
frontier tier to produce the JSON anchors that feed `--select-from-json` — keeping the tool
deterministic while letting Claude do the hard selection. Invariants: source `.srt` + video
byte-identical before/after (sha256); every emitted timestamp is a real cue boundary.

---

## 8. The Underlord assistant — cost-tiered router + action schema (design in R-AI.md)

**Tiers (route cheap-first; never burn Max-plan tokens on tier-0/1 work):**
- **Tier 0 — deterministic (no model):** regex/keyword command parse ("add clip 3",
  "export", "jump to 12:40") + deterministic retrieval (`becky-search` / literal SRT grep).
  Most turns land here.
- **Tier 1 — small local (llama-server Qwen3-4B, `internal/llmlocal`):** fuzzy NL→action
  parsing; cheap yes/no (e.g. becky-quotes neighbor expansion). Offline default for "needs a
  little intelligence."
- **Tier 2 — frontier (escalation only):** hard semantic retrieval + multi-step plans.
  Backend A = **`claude` CLI** (Jordan's Max plan, verified flags):
  `claude -p --output-format json --model opus --append-system-prompt "<rules>" --max-turns 1`
  with the candidate block piped on **stdin**; reply is `{type,result}`. Backend B = Anthropic
  API (`POST https://api.anthropic.com/v1/messages`, `x-api-key` + `anthropic-version:
  2023-06-01`, body `{model,max_tokens,system,messages}`). Model ids: deep = `opus` /
  `claude-opus-4-8`; mid = `claude-haiku-4-5`-class. **Prefer the CLI aliases** (durable
  across snapshot bumps). Tier 2 is triple-gated: classified-hard AND online-toggle-on AND
  within budget; degrade to Tier 1/0 when unavailable.

**Action schema (default-deny allowlist — the AI's ONLY control surface; no free-form exec).**
The model emits a list of these as JSON (or the line DSL `verb arg --flag`); the Go backend
dispatches each to a deterministic handler:

`search(query,mode)` · `find_quotes(criteria, sources)` · `preview_clip(source,in,out)` ·
`grab_frame(source,t)` · `add_clip(source,in,out,label)` · `remove_clip(id)` ·
`reorder(id,to)` · `set_overlay(field=value)` · `set_marker(t,label)` · `set_label(id,text)` ·
`export(opts)`.

**Propose-then-apply (the "show me, don't do it" overlay Jordan loves):** the assistant
returns a `Proposal{ actions[], preview_text }`; the UI shows the preview; **nothing mutates
until the human approves** (✓). Approved proposals append to `habits.AppendCorrectionLog` so
becky learns Jordan's patterns.

**500 GB context discipline (critical):** the model NEVER ingests the folder. Funnel:
read-only filename+sidecar index (`internal/footage`) → deterministic candidate retrieval
(top-K via becky-search + grep) → map-reduce over token-bounded transcript windows → ONE
final plan call over the *reduced* candidate set. Per-turn context = system prompt + action
catalog + current timeline state + one window of candidates. Scales with surviving
candidates, never folder size.

`internal/assistant` sketch: `Router` + `Backend` interfaces; backends
`deterministic`, `local` (llmlocal), `claudeCLI` (exec), `api`; every model/network boundary
behind `Available()` so `go test ./...` is green offline. cgo-free.

---

## 9. GUI ↔ engine contract (WebView2)

- **Media:** Go backend runs a localhost `net/http` server; `GET /media?path=<abs>` →
  `http.ServeFile` (emits `Accept-Ranges` → range-seekable `<video>`). **Security:** only
  serve paths under the currently-opened case folder (reject traversal); read-only.
- **Control:** `w.Bind("beckyCall", goHandler)` exposes the engine to JS; JS calls
  `beckyCall(verb, argsJSON)` → returns JSON. The AI's proposals are pushed to the page with
  `w.Eval`/`w.Dispatch`. Use the proven patterns in `becky-clip-work/spikes/webview2/`.
- **UI surfaces (mirror `descript-gui.jpg` / `filmora-gui.jpg`):** left = search box +
  results list (click→preview, double-click→add); center-top = `<video>` preview with the
  HTML lower-third overlay; center-bottom = timeline strip of clips (drag-drop, ripple,
  trim, cursor, markers); right = the "Underlord" chat panel (proposal previews + ✓/✗).
  Icon-first, unobtrusive — per Jordan's canvas design north star (colors/shapes > walls of
  text).

---

## 10. Build & verify

```bash
# becky-go/ — must stay green with NO tags, NO models, NO ffmpeg:
go build ./...   && go vet ./...   && go test ./...   && gofmt -l .
```
- `cmd/clip` pulls `github.com/jchv/go-webview2` (added by the GUI agent only; `go mod tidy`).
  Build it explicitly: `go build -o bin/becky-clip.exe ./cmd/clip` (Windows). If WebView2 needs
  a build tag to keep CI/Linux green, gate the window code like canvas does with `-tags gui`
  and provide a `//go:build !gui` headless stub so `go build ./...` stays green.
- After any tool work, run `build-all-tools.bat` (auto-discovers `cmd/quotes`, `cmd/reel`,
  `cmd/clip`). The `.exe` Jordan runs must build — `go test` green is NOT "done."
- **Verify with vision:** launch `becky-clip.exe`, screenshot the window, confirm: a `<video>`
  plays, a quote-click seeks, a double-click adds a clip, the lower-third renders, an export
  produces a real MP4. Screenshot each.

---

## 11. Build plan (waves — disjoint file ownership, parallel where independent)

**Wave 1 (parallel — 3 agents, disjoint dirs, all pure-Go + green offline):**
1. **Media engine** — `internal/edl` (§4 model + EDL + re-based SRT) + `internal/reel` +
   `cmd/reel` (§6 ffmpeg render/burn/frame/proxy, nvenc→libx264 fallback). Reads `R-CUT.md`.
2. **Quotes** — `internal/quotes` + `cmd/quotes` (§7, port the prototype; `Selector` seam;
   `--exact`/`--select-from-json`/`--criteria`). Reads `SPEC-BECKY-SRT-QUOTES.md`.
3. **Assistant + footage** — `internal/footage` + `internal/llmlocal` + `internal/assistant`
   (§8 tiers, action schema, funnel, propose-then-apply). Reads `R-AI.md`.

**Wave 2 (1 agent, integrator, single owner of `cmd/clip`):**
4. **GUI** — `cmd/clip` (§9 WebView2 + HTML/JS UI + HTTP media + `Bind` wiring to reel/quotes/
   assistant/footage). Starts from `becky-clip-work/spikes/webview2/`. Reads `R-STACK.md`.

**Then:** orchestrator builds all `.exe`s, launches the GUI, screenshot-verifies, updates the
handoff (§12 + CLAUDE.md §6), cleans `becky-clip-work/cut-tests/` bulk.

---

## 12. Handoff status (update before ending any session)

- [x] Research locked (R-STACK/R-REUSE/R-CUT/R-AI + spikes + screenshots in `becky-clip-work/`).
- [x] Spec authored (this file).
- [ ] Wave 1 — media engine (`edl`/`reel`).
- [ ] Wave 1 — quotes.
- [ ] Wave 1 — assistant + footage.
- [ ] Wave 2 — `cmd/clip` GUI.
- [ ] `build-all-tools.bat` green + GUI launch screenshot-verified.

---

## 13. Open decisions for Jordan (defaults chosen so work proceeds while he's away)

1. **Frontend = WebView2, not C++/Qt.** Native + lightweight + buildable today; the engine is
   frontend-agnostic so we can add a Gio/mpv shell later. *Veto if you truly want native C++
   (costs a toolchain day).*
2. **Names:** `becky-clip` (GUI), `becky-reel` (renderer), `becky-quotes` (search). Provisional.
3. **Export always re-encodes** (frame accuracy + mixed-source concat require it); `-c copy`
   is offered only for a quick single-source rough pull. OK?
4. **Frontier default backend = `claude` CLI (your Max plan)**, API key only if `ANTHROPIC_API_KEY`
   is set. Online is opt-in + logged.

## 14. Non-goals (today)
- Not a full NLE (no multi-track video compositing, transitions, effects beyond the lower-third).
- Not a transcriber (calls `becky-transcribe` when an `.srt` is missing).
- Not loaded into becky-canvas at launch (separate process, launched on demand).
- Originals are NEVER written. Ever.
