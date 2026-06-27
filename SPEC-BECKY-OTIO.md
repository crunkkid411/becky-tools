# SPEC-BECKY-OTIO.md — becky's editor-agnostic timeline export (`becky-otio`)

> **STATUS: COMPLETE — ALL FORMATS BUILT + PROVEN (Phase 1 cloud 2026-06-26; Phase 2
> + render-proof local 2026-06-27).** Every `--format` the CLI advertises is now
> implemented and value-tested in `internal/otio` + `cmd/becky-otio`:
> `otio` (OpenTimelineIO JSON), `fcpxml` (flat FCPXML 1.10), `mlt` (`.kdenlive`, via
> the proven `internal/kdenlive` emitter), `edl` (CMX3600, reuses `edl.WriteEDL`),
> `vegas-list` (`.review.txt`), `all`, plus the optional `--via-otio-cli` escape hatch
> (shells `otioconvert` for AAF/ALE; degrades silently when the OTIO python pkg is
> absent). `go build/vet/test ./...` + gofmt green; `becky-otio --selftest` exits 0
> with 12 per-format value assertions (clip counts, exact frame numbers, exact rational
> time strings, exact Vegas-list line). `build-all-tools.bat` produces `becky-otio.exe`.
> Pure Go, offline, deterministic, **no model boundary** (the lone exec is the optional
> otioconvert). **This closes the hand-conversion gap**: the Reel becky-clip already
> saves now converts to a reviewable timeline with one command — no human plumbing.
>
> **Render-proven (local, 2026-06-27):** a real 2-cut Reel → `--format all` produced
> all five files; the `.kdenlive` rendered headless through **melt** (kdenlive's own
> engine) at exit 0 to **exactly 210 frames = 7.0s** (3s + 4s cuts, frame-exact — not
> the 2-frame "unknown length" collapse), which deterministically closes the kdenlive
> round-trip. What's LEFT is the human eyeball gate only (§9): confirm DaVinci Resolve /
> VEGAS Pro 18 import + play the files (all three editors are installed on the PC), and
> optionally add a one-click button in becky-clip.
>
> Architecture decided by the 4-angle research pass summarized in §1 (DaVinci Resolve
> scripting, VEGAS .NET history, web-timeline feasibility, interchange formats).

---

## 0. TL;DR — what we are building and why

becky's forensic tools already find *moments* — "every segment where the cat is close
to the camera," "every bounty offer for Penguin" — and `becky-clip` already models them
as a `Reel` (an ordered clip-list; see `internal/edl`). **The missing piece is getting
those moments into a SNAPPY editor that Jordan can scrub, and that the AI can also
drive — without marrying becky to any one NLE.**

`becky-otio` is the decoupler. It takes a becky `Reel` and emits **standard timeline
interchange files** that snappy editors open natively:

```
   becky Reel (internal/edl)
            │
            ▼
       becky-otio  ──►  .otio        (OpenTimelineIO — DaVinci Resolve + kdenlive 25.04+ open this NATIVELY)
                   ──►  .fcpxml      (universal fallback — Resolve / Final Cut; Premiere via plugin)   [Phase 2]
                   ──►  .edl         (CMX3600 — dumb last resort; reuse existing edl.WriteEDL)
                   ──►  review.txt   (the "review list" the VEGAS Pro 18 C# script ingests — see /vegas)
```

The win: **becky stays the forensic brain and emits a standard file; the editor becomes
a swappable front-end.** The "which NLE?" question — which has ping-ponged for weeks —
stops being load-bearing. Jordan reviews the cat-tooth candidates in Resolve, kdenlive,
or VEGAS *today*, and we change hosts later by changing one output flag, not the pipeline.

---

## 1. The research this rests on (so it isn't re-litigated)

Four parallel research agents (2026-06-26) established the facts below. Full cited
summaries are in this session's history; the load-bearing conclusions:

- **No single format imports natively into every editor.** The correct strategy is
  **OTIO as the in-code generation hub**, then write the right file per target:
  | Format | DaVinci Resolve | kdenlive | VEGAS Pro 18 | Shotcut | Premiere | Final Cut |
  |---|---|---|---|---|---|---|
  | **OTIO (.otio)** | ✅ native | ✅ native (25.04+) | ❌ | ❌ | ❌ | ❌ |
  | **FCPXML** | ✅ native | ⚠️ via import | ❌ (no import) | ⚠️ | ⚠️ plugin | ✅ native |
  | **EDL (CMX3600)** | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ |
  | **MLT XML** | ❌ | ✅ (.kdenlive) | ❌ | ✅ (.mlt) | ❌ | ❌ |
- **OTIO covers our two best snappy-editor candidates natively** (Resolve, kdenlive),
  which is why it's the primary output.
- **VEGAS Pro 18 cannot import OTIO *or* FCPXML** (its only interchange imports are
  export-only AAF / Final Cut **7** XML). So the VEGAS path is **not** a file becky-otio
  emits for import — it's the **C# script** in `/vegas/BeckyReviewTimeline.cs` that
  builds the timeline through VEGAS's scripting API, fed by the plain **review list**
  that becky-otio writes. (VEGAS also never left legacy .NET Framework 4.8, even at
  v22 — so "upgrade VEGAS to modernize scripting" is a non-starter; the C# script path
  is the same on 18 and 22.)
- **DaVinci Resolve is the strongest AI-drivable host** (official Python/Lua API:
  `ImportMedia → CreateEmptyTimeline → AppendToTimeline([{mediaPoolItem, startFrame,
  endFrame}])` — the start/end frames ARE the in/out), **but external scripting requires
  Resolve *Studio*** (~$295 one-time; the free version scripts only from its internal
  console). becky-otio's `.otio` output works with free Resolve via manual File ▸ Import;
  full AI-driven assembly needs Studio. (That driver is a *separate* future tool,
  `becky-resolve-build`; out of scope here. becky-otio just emits the `.otio`.)
- **"Snappier web editor" is mostly a myth — proxies are the real lever.** Don't build a
  web NLE for this. See `HANDOFF-PROXY-SNAPPINESS.md`.

---

## 2. Scope — what `becky-otio` IS and IS NOT

**IS:** a deterministic converter, `Reel JSON → interchange file(s)`. One tool, one job
(single-tool principle). Pure Go, offline, no ffmpeg, no models, no network.

**IS NOT:**
- Not a timeline editor or player (that's the host editor).
- Not a renderer (that's `becky-reel`).
- Not an AI driver of any editor (a future `becky-resolve-build` would drive Resolve
  Studio's Python API; explicitly out of scope).
- Not a proxy/transcode tool (that's `HANDOFF-PROXY-SNAPPINESS.md` / `internal/reel`).
- It never modifies source media. It only reads a small Reel JSON and writes text files.

---

## 3. File layout (becky conventions)

| Path | Role | New? |
|---|---|---|
| `internal/otio/` | The OTIO data model + deterministic JSON writer; `Reel → otio.Timeline → JSON`. Pure Go, table-tested. | NEW |
| `internal/otio/fcpxml.go` | Native FCPXML 1.10 writer (Phase 2). | NEW |
| `internal/otio/vegaslist.go` | The `Reel → review.txt` writer (the `/vegas` script's input). | NEW |
| `cmd/becky-otio/` | CLI: Reel JSON in → chosen format(s) out. `--selftest`. | NEW |
| `internal/edl/` | **Reuse** — `Reel`/`Clip` is the input contract; `WriteEDL` is the CMX3600 output. | reuse |
| `internal/kdenlive/` | **Reuse** — already writes MLT/.kdenlive XML if an MLT output is wanted. | reuse |
| `internal/mediainfo/` | **Optional reuse** — probe source fps/duration when a clip's `Meta.SourceFPS` is unset and ffprobe is present (degrade to `DefaultFPS` when absent). | reuse |
| `/vegas/BeckyReviewTimeline.cs` + `/vegas/README.md` | **Already written** (this branch) — the VEGAS Pro 18 path. | done |

`cmd/becky-otio` imports no sibling `cmd/*`. `build-all-tools.bat` auto-discovers it.
`go build ./...` / `go test ./...` stay green with no tags, no ffmpeg, no models.

---

## 4. Input contract — the existing `edl.Reel` (do not redefine)

becky-otio consumes the **frozen** `edl.Reel` shape (`internal/edl/edl.go`, also
SPEC-BECKY-CLIP §4). The fields it reads:

- `Reel.Name`, `Reel.Clips[]`
- `Clip.Source` (ABSOLUTE path, read-only), `Clip.In`, `Clip.Out` (seconds, float64),
  `Clip.Label`
- `Clip.Meta.SourceFPS` (for frame math; fall back to probe → `edl.DefaultFPS` = 30)
- `Clip.FPS(fallback)` and `Clip.Dur()` helpers already exist — use them.

Times are **seconds into each source**. Clips are placed **in order, end-to-end** on the
output timeline (a compilation), exactly as `becky-reel` renders them. No gaps, single
video track + single audio track (matching the forensic-review use case; multi-track is
a non-goal — see §10).

---

## 5. Primary output — OpenTimelineIO `.otio` (Phase 1, MUST-HAVE)

OTIO's native serialization is JSON with `OTIO_SCHEMA` type tags. Emit it **directly in
Go** (struct → `encoding/json`) — do **not** require Python/the OTIO library at runtime.
This keeps becky offline and deterministic. (A `--via-otio-cli` escape hatch for exotic
formats is Phase 2, §7.)

### 5.1 Frame math (the one thing to get right)

OTIO measures time in **frames at a rate**, via `RationalTime{rate, value}`:
- A clip's source in-point → `RationalTime{rate: fps, value: round(In * fps)}`
- A clip's duration → `RationalTime{rate: fps, value: round((Out-In) * fps)}`
- `fps = clip.FPS(probedOrDefault)`. Round to the nearest whole frame with a single
  consistent rule (`math.Round`); never truncate (truncation drifts on long clips).
- `target_url` must be a **file URL**: `file:///C:/Videos/cam1.mp4` (forward slashes,
  drive-letter form on Windows). Provide a `fileURL(absPath)` helper that handles both
  `C:\...` (use `internal/pathx`) and POSIX paths.

### 5.2 Exact JSON to emit

A `Reel` with two clips produces this structure (a single video Track inside the Stack;
add a parallel audio Track only if §5.4 audio is enabled). Field order is illustrative;
`encoding/json` order doesn't matter to readers.

```json
{
  "OTIO_SCHEMA": "Timeline.1",
  "name": "<Reel.Name or 'becky-review'>",
  "global_start_time": null,
  "metadata": { "becky": { "generator": "becky-otio", "version": "1" } },
  "tracks": {
    "OTIO_SCHEMA": "Stack.1",
    "name": "tracks",
    "children": [
      {
        "OTIO_SCHEMA": "Track.1",
        "name": "V1",
        "kind": "Video",
        "children": [
          {
            "OTIO_SCHEMA": "Clip.1",
            "name": "<Clip.Label or basename>",
            "source_range": {
              "OTIO_SCHEMA": "TimeRange.1",
              "start_time": { "OTIO_SCHEMA": "RationalTime.1", "rate": 30.0, "value": 1950.0 },
              "duration":   { "OTIO_SCHEMA": "RationalTime.1", "rate": 30.0, "value": 255.0 }
            },
            "media_reference": {
              "OTIO_SCHEMA": "ExternalReference.1",
              "target_url": "file:///C:/Videos/cam1.mp4",
              "available_range": null
            },
            "metadata": {
              "becky": { "source": "C:\\Videos\\cam1.mp4", "in_sec": 65.0, "out_sec": 73.5 }
            }
          }
        ]
      }
    ]
  }
}
```

- `source_range.start_time` = the **in-point into the source** (this is what makes the
  editor pull the right span). `duration` = clip length. Together they are the trim.
- Putting the original `source`/`in_sec`/`out_sec` in `metadata.becky` is a forensic
  audit aid (round-trippable, human-readable) and costs nothing.
- `available_range: null` is fine; set it (full source duration in frames) only if a
  probe is cheap and present — it helps some importers but is optional.

### 5.3 Schema version strings (use exactly these)

`Timeline.1`, `Stack.1`, `Track.1`, `Clip.1`, `TimeRange.1`, `RationalTime.1`,
`ExternalReference.1`. These are the stable current OTIO core schema versions Resolve and
kdenlive read. Emit them verbatim.

### 5.4 Audio

Default: emit **video only** (one `Track` with `kind:"Video"`). Resolve/kdenlive link the
source's audio on import for most files. Add an optional `--audio` flag that emits a
second parallel `Track` with `kind:"Audio"` containing the same clips (same source_range)
— useful when a host doesn't auto-link audio. Keep it off by default to match the
review-first use case.

### 5.5 Acceptance for the OTIO writer

- A Reel round-trips: emit `.otio`, parse it back with a Go JSON read, and assert the
  clip count, each `target_url`, and each `start_time.value`/`duration.value` match the
  expected frames. (We can't run the OTIO Python lib in CI, so the round-trip is the
  deterministic proof; the real-editor import is the local-agent gate, §9.)

---

## 6. The VEGAS review list — `Reel → review.txt` (Phase 1, MUST-HAVE)

Because VEGAS Pro 18 imports neither OTIO nor FCPXML, becky-otio emits the plain text
**review list** that `/vegas/BeckyReviewTimeline.cs` ingests. Format (see `/vegas/README.md`):

```
# generated by becky-otio
<absolute source path> | <in seconds> | <out seconds> | <label>
```

- One line per clip, in Reel order. `#` comment header allowed.
- In/out as plain decimal seconds (the C# script also accepts colon time, but seconds is
  unambiguous — emit seconds).
- Label = `Clip.Label`, else the source basename. Strip any `|` from the label (it's the
  delimiter) — replace with `/`.
- Writer is ~20 lines of pure Go; table-test it (assert exact lines for a 2-clip Reel).

This is the path that lets Jordan **review immediately in the editor he knows**, today.

---

## 7. Phase 2 outputs (BUILT 2026-06-27 — Premiere/FCP/AAF reach)

These were the "build only on request" items; they now ship (the request was "build to
completion"). Both are value-tested and wired into `--selftest`:

- **Native FCPXML 1.10 writer** (`internal/otio/fcpxml.go`) — DONE. The universal fallback
  Final Cut and Premiere (via the X27 plugin) read, and a Resolve alternative. Emits flat
  v1.10 XML (not the `.fcpxmld` bundle): a `resources` block with one `<format>` per
  distinct frame rate + one `<asset>` (with a `file://` `media-rep`) per distinct source,
  then `library > event > project > sequence > spine` of `<asset-clip>`s. Times are rational
  frame strings — `offset`/`duration` in the SEQUENCE rate, `start` (source in-point) in the
  source's OWN rate (so mixed-fps sources line up: e.g. `start="1950/30s"` beside
  `start="3000/25s"`); the common NTSC rates emit their exact `1001/x000` rationals.
  Honest limits (review fallback, not a finished conform): nominal `1920×1080` canvas (the
  real resolution comes from the media on import) and `hasVideo`+`hasAudio` assumed.
  `--format fcpxml` → `<name>.fcpxml`.
- **`--via-otio-cli`** — DONE (`internal/otio/otiocli.go` + the CLI flag). If the OTIO Python
  package is installed, it shells `otioconvert -i <generated>.otio -o <out>.<ext>` to reach
  AAF / ALE / other adapters (`--via-otio-cli aaf,ale`). Strictly degrade-never-crash:
  detects `otioconvert` on PATH; if absent it keeps the `.otio` and records a warning —
  becky never depends on Python being present.

Also built this pass (it was listed in §8's CLI but unwired): **`mlt`** → `<name>.kdenlive`,
via the proven `internal/kdenlive` emitter — opens natively in kdenlive and renders headless
through melt (render-proven, see the status banner).

---

## 8. CLI

```
becky-otio --reel <reel.json> --format <fmt[,fmt...]> [--out <dir>] [--audio] [--selftest]
```

- `--reel` — path to a Reel JSON (the shape `becky-clip` already saves). `-` = stdin.
- `--format` — one or more of: `otio` (default), `edl`, `vegas-list`, `fcpxml` (Phase 2),
  `mlt` (via `internal/kdenlive`), `all`.
- `--out` — output directory (default: alongside the reel). File names:
  `<reel-name>.otio`, `<reel-name>.edl`, `<reel-name>.review.txt`, `<reel-name>.fcpxml`,
  `<reel-name>.kdenlive`.
- `--audio` — also emit the parallel audio track (OTIO/FCPXML).
- Output: JSON summary to stdout (`{written:[{format,path,clips}], warnings:[...]}`),
  diagnostics to stderr, exit 0 / nonzero. Standard `beckyio` helpers.
- **Degrade, never crash:** a missing source file is a `warning` in the summary, not a
  fatal — the clip is still written to the timeline (the editor will show it offline/red,
  which is correct: the human is told the evidence file moved, rather than the export
  silently dropping it). A clip with `Out <= In` is skipped with a warning.

### 8.1 `--selftest` (the one-command provable-handoff proof)

`becky-otio --selftest` must:
1. Build an in-memory 2-clip Reel (two synthetic source paths, distinct in/out, distinct
   fps via Meta.SourceFPS).
2. Emit OTIO + EDL + vegas-list to a temp dir.
3. Re-read each and assert values: OTIO clip count + each `start_time.value`/`duration`
   in frames; the vegas-list lines; the EDL event count. Print PASS/FAIL per format.
4. Exit 0 only if all pass.

This is the measurable proof the cloud agent can run offline (no editor needed) and paste,
per `STANDARDS-WORKFLOW.md §7`.

---

## 9. Local-agent work order (the import gates only Jordan's machine can close)

Cloud built + proved §8.1 offline (done). Local has now built Phase 2, the exe, and the
deterministic round-trips; only the human eyeball gates on Resolve/VEGAS remain:

- [x] `go build ./... && go vet ./... && go test ./...` green; gofmt clean; `becky-otio
      --selftest` exits 0 with value assertions (cloud, 2026-06-26; now 12 checks covering
      otio/fcpxml/mlt/vegas-list/edl — local, 2026-06-27).
- [x] `build-all-tools.bat` produces `becky-otio.exe` (auto-discovered; "Built 83 tools",
      exit 0 — local, 2026-06-27).
- [x] Produced a real 3-clip Reel + a 2-cut render Reel; `becky-otio --reel <reel.json>
      --format all --via-otio-cli aaf` wrote all five files; every output STRUCTURALLY
      VALIDATED offline (OTIO JSON parse + frame values, FCPXML XML + rational times, MLT
      producers/entries, vegas-list lines, EDL events). via-otio-cli degraded cleanly
      (otioconvert absent). (local, 2026-06-27)
- [x] **kdenlive engine (melt) round-trip — DONE deterministically.** The `.kdenlive`
      rendered headless through `melt.exe` (kdenlive's own engine) at exit 0 to exactly
      210 frames = 7.0s (3s+4s cuts, frame-exact). A project melt renders is one kdenlive
      opens, so this closes the kdenlive gate without a GUI click. (local, 2026-06-27)
- [ ] **DaVinci Resolve (installed):** File ▸ Import ▸ Timeline ▸ pick the `.otio`. Confirm
      the clips land with correct in/out and play. (Human eyeball gate — Jordan.)
- [ ] **kdenlive GUI (optional):** open the `.otio` to confirm the GUI agrees with the melt
      render (engine round-trip already proven above).
- [ ] **VEGAS Pro 18 (installed):** Tools ▸ Scripting ▸ Run Script ▸
      `/vegas/BeckyReviewTimeline.cs` ▸ pick the `.review.txt`. Confirm the clips + named
      regions land and play. (Human eyeball gate — Jordan.)
- [x] Recorded the build + render-proof in `HANDOFF-LOG.md`; the editor round-trip table is
      seeded with the kdenlive/melt result — Resolve/VEGAS rows fill in once Jordan eyeballs.

## 10. Non-goals (today)

- Multi-track / overlays / effects / transitions in the export (review is single V+A;
  the forensic lower-third stays a `becky-reel` render concern, not an interchange one).
- Driving any editor's live API (future `becky-resolve-build` for Resolve Studio).
- Importing timelines back *into* becky (one-way export is the need).
- Proxy generation (separate; `HANDOFF-PROXY-SNAPPINESS.md`).

## 11. Open decisions for Jordan (defaults chosen so work can proceed)

1. **Primary format = OTIO**, because it natively covers the two snappy editors we'd
   actually adopt (Resolve, kdenlive). *Veto only if you've decided VEGAS is the
   permanent host — then the C# script path is primary and OTIO is secondary.*
2. **FCPXML + `--via-otio-cli` are Phase 2** (Premiere/FCP/AAF), built only if you need
   those editors. Default build = OTIO + EDL + vegas-list.
3. **Single V+A track, clips end-to-end** — matches "review these candidates," mirrors
   `becky-reel`. Multi-track is a non-goal until a real need appears.
