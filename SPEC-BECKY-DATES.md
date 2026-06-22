# SPEC-BECKY-DATES.md — forensic "when was this captured?" date triangulation

> **SPEC — NOT BUILT, AWAITING JORDAN'S APPROVAL.**
> Research + design only. No Go code has been written, no new binary exists, nothing in
> `becky-go/` has been changed. Jordan approves before any build starts.
>
> Authored 2026-06-22. This spec is grounded in code that already exists in the repo —
> every interface below names a real file:symbol it builds on, and invents no new external
> dependency. The hard work (EXIF/QuickTime extraction, OCR, ffprobe) is already done; this
> tool is the **triangulation + verdict** layer over it.

---

## 1. Purpose + user need

**`becky dates <folder>` answers one forensic question per clip: "when was this captured?" —
and shows the BASIS for the answer (which signals agreed).** It is the dating counterpart to
`becky-identify` (who) and the planned `becky location` (where).

The need is concrete. A 500 GB corpus of evidence footage is only useful if each clip can be
placed on a timeline. But a single date field is a trap:

- The filesystem **mtime is an evidence-integrity landmine** — it is rewritten by copy, sync,
  and cloud download (README "Non-obvious decisions": *"file mtime is an evidence-integrity
  landmine and is only ever a fallback marked `mtime(untrusted)`"*). A clip pulled off a phone
  and synced to a drive may show a 2026 mtime for 2024 footage.
- The container `creation_time` (EXIF/QuickTime) is the **trustworthy** capture time when
  present — but re-encoded, screen-recorded, or stripped clips lose it.
- A **burned-in on-screen timestamp** (a stream overlay, a dashcam/CCTV date stamp, a phone
  camera date overlay) is often the *only* surviving date on a re-encoded clip — and it is
  legible to OCR, which `becky-ocr` already reads and tags `candidate_timestamp`.

No one of these is reliable alone, but they are **independent**. `becky dates` does exactly what
`FORENSIC-OUTPUT-PHILOSOPHY.md` (TOP PRINCIPLE) demands: it combines multiple independent
signals, scores confidence, and reaches a **confident, corroborated verdict** — then states it
plainly, with the corroborating points attached. A lone weak signal stays a candidate / unknown;
≥2 independent signals agreeing → a stated verdict. **Date triangulation maps perfectly onto
corroborate-then-conclude** — this tool is that rule applied to time.

It is a NEW standalone tool, `becky-dates`, plus a `becky dates` orchestrator op (mirroring
`becky find` / `becky profile`). It reads only — it never modifies a source file.

---

## 2. The independent date signals + how they triangulate

A "signal" is one independent observation of when the clip was captured, each with a value, a
source label, and a base trust weight. The tool gathers all available signals per clip, then
corroborates them into one verdict.

### Signal A — Container capture time (EXIF / QuickTime / ffprobe) — STRONGEST

Already extracted, evidence-grade, in `internal/exifmeta`:
- `exifmeta.Extractor.Extract(path)` (`internal/exifmeta/exifmeta.go:127`) returns
  `Metadata.CaptureTimeLocal` / `CaptureTimeUTC` / `UTCOffset` with `CaptureTimeSource` set to
  one of `exif` / `quicktime` / `ffprobe` / `mtime(untrusted)`
  (`internal/exifmeta/exifmeta.go:28-33`).
- exiftool is preferred (vendor tags), with an ffprobe fallback
  (`resolveFFprobeCaptureTime`, `internal/exifmeta/ffprobe.go:130`) that reads the container
  `creation_time` (UTC ISO8601) and applies the Samsung/Android `utc_offset` tag to recover
  device-local time.

`becky-dates` consumes this directly. **Crucially: a `CaptureTimeSource` of `mtime(untrusted)`
is NOT signal A** — it is signal B (below). The tool reads `CaptureTimeSource` to decide which
bucket the time belongs in. This is the single most important correctness rule in the tool.

Trust weight: `exif`/`quicktime` = **strong** (a real capture tag); `ffprobe` `creation_time`
= **strong** (container mux time, normally the capture instant); both count as one independent
signal each only when they disagree (see §2.5).

### Signal B — Filesystem date (mtime + filename date tokens)

Two sub-signals, both WEAK and explicitly labelled untrusted:

- **mtime** — `exifmeta.Metadata.FileMTime` (`internal/exifmeta/exifmeta.go:68`, always
  populated, always `file_mtime_untrusted`). Never the verdict on its own; useful only as a
  *bound* ("the file existed by then") and as corroboration when it happens to agree with a
  stronger signal.
- **filename date token** — a date parsed out of the basename, e.g. `20250704_181431.mp4`,
  `2025-07-04 19.14.31.mov`, `IMG_20240301_..`, `VID_20240301..`, `Screen Recording 2025-07-04
  at ...`. These device/app naming conventions are deterministic and offline. Parse the basename
  via `pathx.Base` (`internal/pathx/pathx.go:20`) — **never `filepath.Base`** — because corpus
  paths may be Windows `C:\...` paths even on Linux/CI (README/CLAUDE invariant). A filename
  token is a date a human or device wrote at capture/save time; it is **mid-trust** — stronger
  than mtime (it usually survives copy), weaker than a container tag (it is editable text).

### Signal C — Burned-in on-screen timestamp (OCR) — OPTIONAL, needs the OCR model

A date/clock burned into the pixels. `becky-ocr` already finds these: `categorize.go`
(`cmd/ocr/categorize.go:38`, `timestampRe` + `isTimestamp`) tags such a line
`candidate_timestamp`. `becky-dates` reads an existing `ocr.json` (the `FrameResult.Lines` /
`LowConfidenceLines` with `category == "candidate_timestamp"`, `cmd/ocr/ocr.go:72-90`), parses
the date text, and treats it as an independent signal. Because OCR needs PP-OCRv5 on Jordan's
hardware, this signal is **optional** — the tool runs fully without it and notes its absence
(see §5). A high-confidence burned-in date that agrees with the container time is the strongest
possible corroboration (two fully independent provenance paths agreeing).

Trust weight: scaled by OCR confidence — a `lines` read (≥ min-confidence) is **strong**; a
`low_confidence_lines` read is **weak** (candidate only).

### Optional context signals (corroboration only, never the verdict source)

- **GPS-derived date is NOT a thing** — GPS gives place, not time; out of scope here (that's
  `becky location`). But `exifmeta.GPS` presence is noted as provenance context only.
- **Sidecar `.info.json`** — yt-dlp's `upload_date` (when present next to the clip) is a
  publish date, not a capture date; recorded as a labelled context note, never the verdict.

### 2.5 Triangulation: corroborate, then conclude

Each gathered signal yields a normalized **calendar date** (day precision is the verdict unit;
the clock time is carried for display but two signals "agree" if they fall on the same local
calendar day, with a `--tolerance` window for near-midnight/timezone slop, default ±1 day).

The corroboration engine (deterministic, no model):

1. **Collect** every available signal → `(date, source, trust, raw_value)`.
2. **Cluster** signals whose dates agree within `--tolerance`.
3. **Score** each cluster: `agree_count` = number of *independent* sources in it (mtime and a
   filename token from the same file are independent; two ffprobe reads of one tag are not).
4. **Conclude** — mirrors `FORENSIC-OUTPUT-PHILOSOPHY.md`:
   - **≥2 independent signals agree** → `verdict = that date`, `status = DOCUMENTED`, state it
     plainly. (e.g. container `quicktime` 2025-07-04 + burned-in OCR 2025-07-04 → "Captured
     2025-07-04.")
   - **1 strong signal, no contradiction** (a lone `exif`/`quicktime` tag) → `verdict = that
     date`, `status = DOCUMENTED` but flagged `single_signal: true` (a real capture tag is
     trustworthy even alone; FORENSIC-PHILOSOPHY allows a single strong signal to conclude).
   - **only weak signals** (mtime only, or a lone filename token, or a lone low-confidence OCR
     read) → `verdict = that date`, `status = CANDIDATE` (best guess, surfaced for review).
   - **no signal at all** → `status = UNKNOWN`, no verdict.
   - **signals CONFLICT** (≥2 clusters with comparable trust, none dominant) → `status =
     CONFLICT`; verdict = the highest-trust cluster's date but `conflicts` lists the dissenting
     signals plainly ("container says 2024-03-01, filename says 2025-07-04 — REVIEW"). A
     conflict is a loud finding, not a hidden one.

This is the whole point of the tool: it does the sorting so the human reviews confident,
corroborated dates and a short list of genuine conflicts — not a pile of raw fields.

---

## 3. CLI contract, JSON schema, and the human line

### CLI

```
becky-dates <folder>                          # date every clip in a folder
becky-dates <video.mp4> [<video2> ...]        # or specific files

Options:
  --ocr <ocr.json>      becky-ocr output to mine for burned-in candidate_timestamp lines
                        (per-clip auto-discovery also tried: <stem>/ocr.json next to source)
  --recursive           recurse into subfolders when given a folder
  --tolerance <days>    calendar-day slop for "signals agree" (default 1)
  --exiftool <path>     override exiftool binary (else auto-detect, internal/exifmeta)
  --ffprobe <path>      override ffprobe binary (default from ~/.becky/config.json)
  --min-ocr-conf 0.80   OCR timestamp read at/above this = strong signal; below = weak/candidate
  --output <file>       write JSON here instead of stdout
  --json                JSON only, suppress the per-clip human line on stderr
  --verbose             progress on stderr
```

Conventions (every becky tool): JSON to stdout via `beckyio.PrintJSON`, the human line(s) to
stderr, exit 0 on success / nonzero only on a usage/fatal error, stderr otherwise silent without
`--verbose` (`internal/beckyio`). Reads only; never modifies a source video.

### JSON output schema (proposed; synthetic values)

```json
{
  "tool": "becky-dates v1.0.0",
  "folder": "E:/TakingBack2007",
  "clips_dated": 3,
  "results": [
    {
      "source_file": "E:/TakingBack2007/20250704_181431.mp4",
      "source_base": "20250704_181431.mp4",
      "verdict_date": "2025-07-04",
      "verdict_time_local": "2025-07-04T18:14:31-05:00",
      "status": "DOCUMENTED",
      "confidence": 0.95,
      "basis": "container capture tag (quicktime) and the filename date token agree on 2025-07-04",
      "single_signal": false,
      "signals": [
        {"source": "quicktime", "trust": "strong", "date": "2025-07-04",
         "value": "2025-07-04T18:14:31-05:00", "agrees_with_verdict": true},
        {"source": "filename",  "trust": "medium", "date": "2025-07-04",
         "value": "20250704_181431", "agrees_with_verdict": true},
        {"source": "mtime(untrusted)", "trust": "weak", "date": "2026-01-12",
         "value": "2026-01-12T09:01:00Z", "agrees_with_verdict": false},
        {"source": "ocr_burned_in", "trust": "strong", "date": "2025-07-04",
         "value": "07/04/2025 6:14 PM", "ocr_confidence": 0.97, "frame_timestamp": 0.0,
         "agrees_with_verdict": true}
      ],
      "conflicts": [],
      "notes": [
        "file mtime (2026-01-12) is UNTRUSTED (rewritten by copy/sync) and disagrees with the verdict — expected, not a conflict"
      ]
    },
    {
      "source_file": "E:/TakingBack2007/clip_reencoded.mp4",
      "source_base": "clip_reencoded.mp4",
      "verdict_date": "2024-03-01",
      "verdict_time_local": "",
      "status": "CONFLICT",
      "confidence": 0.45,
      "basis": "container says 2024-03-01 (ffprobe creation_time) but the burned-in on-screen date reads 2025-07-04 — REVIEW",
      "single_signal": false,
      "signals": [
        {"source": "ffprobe", "trust": "strong", "date": "2024-03-01",
         "value": "2024-03-01T00:00:00Z", "agrees_with_verdict": true},
        {"source": "ocr_burned_in", "trust": "strong", "date": "2025-07-04",
         "value": "2025-07-04", "ocr_confidence": 0.93, "agrees_with_verdict": false}
      ],
      "conflicts": [
        {"a": "ffprobe", "a_date": "2024-03-01", "b": "ocr_burned_in", "b_date": "2025-07-04",
         "note": "container mux time vs burned-in overlay disagree by >1 day; a re-mux can reset creation_time"}
      ],
      "notes": ["no exiftool capture tag; relied on ffprobe creation_time"]
    },
    {
      "source_file": "E:/TakingBack2007/screen_grab.mkv",
      "source_base": "screen_grab.mkv",
      "verdict_date": "",
      "verdict_time_local": "",
      "status": "UNKNOWN",
      "confidence": 0.0,
      "basis": "no trustworthy date signal: no capture tag, no filename date token, OCR not run",
      "single_signal": false,
      "signals": [
        {"source": "mtime(untrusted)", "trust": "weak", "date": "2026-02-02",
         "value": "2026-02-02T11:00:00Z", "agrees_with_verdict": false}
      ],
      "conflicts": [],
      "notes": ["only the untrusted file mtime is available; run becky-ocr for a burned-in date, or treat as undated"]
    }
  ],
  "skipped": [
    {"source_file": "E:/TakingBack2007/notes.txt", "reason": "not a media file"}
  ],
  "notes": {
    "ocr": "not supplied; burned-in on-screen timestamps were not consulted (run becky-ocr and pass --ocr)"
  }
}
```

Field rules:
- `status` ∈ `DOCUMENTED | CANDIDATE | CONFLICT | UNKNOWN` (maps to the philosophy tags;
  `DOCUMENTED` is stated plainly, the rest are flagged with their basis).
- `signals[].source` ∈ `exif | quicktime | ffprobe | filename | mtime(untrusted) | ocr_burned_in`.
- `signals[].trust` ∈ `strong | medium | weak`.
- `verdict_date` is `YYYY-MM-DD` (empty only for UNKNOWN). `verdict_time_local` is the
  best-available wall-clock from the strongest agreeing signal (empty when day-only).
- `mtime(untrusted)` ALWAYS appears as a signal (so the reviewer sees it) but is `weak` and
  never carries a verdict alone.

### The concise human line (per clip, to stderr; ACCESSIBILITY.md — tight, lead with the answer)

```
20250704_181431.mp4  ->  2025-07-04  [DOCUMENTED, conf 0.95]  (container tag + filename agree)
clip_reencoded.mp4   ->  2024-03-01  [CONFLICT]  container 2024-03-01 vs burned-in 2025-07-04 — REVIEW
screen_grab.mkv      ->  (undated)   [UNKNOWN]   only file mtime available; run becky-ocr
```

One line per clip: the basename, the verdict, the status, and the one-phrase basis. Lead with
the answer, no wall of text (ACCESSIBILITY.md: concise > exhaustive; keep it tight). Per
`FORENSIC-OUTPUT-PHILOSOPHY.md` §6 the bracketed tag carries known-vs-candidate at a glance.

---

## 4. Deterministic / offline / degrade-never-crash

- **Deterministic:** given the same files (+ same `ocr.json`), the same verdict every run. Date
  parsing and clustering are pure functions; no clock-dependent behavior except reading file
  mtime (a property of the file, not the run).
- **Offline:** no network. exiftool/ffprobe are local; OCR is consumed from an existing file.
- **Degrade-never-crash** (mirrors `exifmeta.Extract`'s own fallback chain,
  `internal/exifmeta/exifmeta.go:127-159`):
  - no exiftool → ffprobe path (already built into `exifmeta`).
  - no container capture tag → fall back to filename token + mtime, status downgrades to
    CANDIDATE/UNKNOWN with a note; never errors.
  - no `--ocr` / no `ocr.json` → signal C simply absent, top-level note says so; exit 0.
  - an unparseable date string → that signal is dropped with a note, others still triangulate.
  - a non-media or unreadable file → added to `skipped` with a reason, not fatal; the run
    continues over the rest of the folder.
  - **the worst case (only mtime) still returns a result** — UNKNOWN with the mtime shown and
    the remedy noted — never a crash and never a confident wrong date from mtime alone.

---

## 5. Cloud-vs-local split

The tool is built so the **whole date-triangulation core is cloud-buildable and unit-testable**,
and only the burned-in-timestamp pixel read needs Jordan's hardware.

| Cloud (pure-Go, deterministic, testable here)                                   | Local (Jordan's PC)                                  |
|---------------------------------------------------------------------------------|------------------------------------------------------|
| Filename date-token parser (all device/app conventions) — pure string/time      | Run `becky-ocr` (PP-OCRv5) to PRODUCE `ocr.json`     |
| mtime reading + the untrusted labelling                                         | Sound/visual check on real footage with overlays     |
| The `exifmeta`-backed container-time signal (already built + tested)            | `build-all-tools.bat` (auto-discovers `cmd/dates`)   |
| The corroboration/cluster/verdict engine (the heart of the tool)                |                                                      |
| Reading + parsing an existing `ocr.json` for `candidate_timestamp` lines        |                                                      |
| All JSON schema, the human line, degrade paths, full unit-test suite            |                                                      |

**Design the OCR signal as optional through a clean seam.** `becky-dates` does NOT call the OCR
model — it consumes the **already-produced** `becky-ocr` JSON (`--ocr <file>`, or auto-discovered
`<stem>/ocr.json`). Concretely the OCR-signal source is an interface:

```
// TimestampSource yields burned-in date/clock candidates for a clip. The default
// impl reads a becky-ocr ocr.json file (pure-Go, cloud-testable). It can be left
// unset → signal C is simply absent (degrade, exit 0).
type TimestampSource interface {
    BurnedInDates(sourceFile string) []OCRDateCandidate // {text, confidence, frameTimestamp}
}
```

This is the same pattern the repo already uses (becky-ocr itself defers the model to a Python
helper subprocess; becky-scout defers the online step behind a `PlaylistSource` interface). The
cloud agent builds + tests the file-reading impl with fixture `ocr.json`; nothing about
triangulation depends on the model running.

---

## 6. Build plan (checkboxed) + unit tests

Package layout: `becky-go/cmd/dates/` (`main.go`, `dates.go`, `signals.go`, `triangulate.go`,
`filename.go`) + `becky-go/internal/dateguess/` for the reusable parsing/clustering core (so a
future `becky ingest` DIGEST.md step can reuse it). New tool = a new `cmd/<tool>` only;
`build-all-tools.bat` auto-discovers it (no edit needed). Orchestrator op `becky dates` added to
`cmd/becky` (`main.go:67` switch + a `runDates` in a new `dates.go`, mirroring `runFind`).

- [ ] **Scaffold** `cmd/dates/main.go`: flag parsing, folder/file expansion (`pathx.Base` for
      basenames), media-file filter (extension allow-list: mp4/mov/mkv/m4v/avi/webm/…), output
      via `beckyio.PrintJSON`, the per-clip stderr human line.
- [ ] **Signal A** (`signals.go`): call `exifmeta.NewExtractor(...).Extract(path)`; map
      `CaptureTimeSource` → the right signal/trust (`exif`/`quicktime`/`ffprobe` = strong;
      `mtime(untrusted)` → route to Signal B, NOT A).
- [ ] **Signal B mtime** (`signals.go`): read `Metadata.FileMTime` as a weak signal, always
      emitted.
- [ ] **Signal B filename** (`filename.go`, `internal/dateguess`): deterministic basename
      date-token parser covering `YYYYMMDD[_HHMMSS]`, `YYYY-MM-DD`, `IMG_/VID_YYYYMMDD`,
      `Screen Recording YYYY-MM-DD at ...`, `YYYY.MM.DD`, and unix-epoch-in-name guards;
      returns `(date, ok)`; rejects implausible dates (year < 1990 or > now+1).
- [ ] **Signal C** (`signals.go`): `TimestampSource` interface + an `ocr.json`-reading impl that
      pulls `candidate_timestamp` lines (and any `low_confidence_lines` with that category),
      parses the date text (reuse the `cmd/ocr/categorize.go` timestamp grammar), scaled by OCR
      confidence against `--min-ocr-conf`.
- [ ] **Triangulation engine** (`triangulate.go`, `internal/dateguess`): normalize each signal
      to a calendar date, cluster within `--tolerance`, count INDEPENDENT sources, apply the
      §2.5 status rules → `verdict_date`, `status`, `confidence`, `basis`, `conflicts`.
- [ ] **Output assembly** + degrade notes + `skipped` handling.
- [ ] **Orchestrator op** `becky dates` in `cmd/becky`.
- [ ] **`go build ./... && go vet ./... && go test ./... && gofmt -l .`** all green (cloud
      gates 1-4); `build-all-tools.bat` is local's completion step.

### Unit tests — assert the VERDICT from fixture metadata (assert values, not truthiness)

Per `STANDARDS-ENGINEERING.md`: tests assert concrete values, and every conflict case is a
regression fixture. The engine takes a `[]Signal` and emits a verdict, so triangulation is
tested with no files/model at all:

- [ ] `filename.go`: table-driven — `20250704_181431.mp4` → 2025-07-04; `IMG_20240301_...`
      → 2024-03-01; `2025-07-04 19.14.31.mov` → 2025-07-04;
      `Screen Recording 2025-07-04 at 9.01 AM.mov` → 2025-07-04; `random_name.mp4` → not-ok;
      `clip_99999999.mp4` → rejected (implausible).
- [ ] **Two-signal agreement → DOCUMENTED**: container `quicktime` 2025-07-04 + filename
      2025-07-04 → `verdict_date=="2025-07-04"`, `status=="DOCUMENTED"`, `single_signal==false`.
- [ ] **Lone strong tag → DOCUMENTED single_signal**: only `exif` 2024-03-01 →
      `status=="DOCUMENTED"`, `single_signal==true`.
- [ ] **Weak-only → CANDIDATE**: only a filename token (no tag) → `status=="CANDIDATE"`.
- [ ] **mtime-only → UNKNOWN**: only `mtime(untrusted)` present → `status=="UNKNOWN"`,
      `verdict_date==""`, mtime still listed in `signals`.
- [ ] **CONFLICT case (the load-bearing one)**: `ffprobe` 2024-03-01 vs strong `ocr_burned_in`
      2025-07-04 → `status=="CONFLICT"`, `conflicts` non-empty naming both sources/dates,
      verdict = the higher-trust cluster (assert which one, and assert the basis string mentions
      both dates).
- [ ] **mtime disagreement is NOT a conflict**: strong container date + a much-later
      `mtime(untrusted)` → `status=="DOCUMENTED"`, `conflicts` empty, a note explains mtime is
      untrusted. (Guards against treating sync-rewritten mtime as evidence of conflict.)
- [ ] **tolerance window**: container 2025-07-04 + OCR 2025-07-05 with `--tolerance 1` →
      agree (DOCUMENTED); with `--tolerance 0` → CONFLICT.
- [ ] **OCR-source seam**: a fixture `ocr.json` with a `candidate_timestamp` line is parsed into
      Signal C; a missing file → Signal C absent + top-level note, no error.
- [ ] **degrade**: an unparseable `creation_time` and a non-media file each handled without a
      panic; the run produces a result/`skipped` entry respectively.
- [ ] **determinism**: same inputs → byte-identical JSON across two runs.

---

## 7. Open Decisions for Jordan

1. **Verdict precision: day vs exact time.** The verdict unit is the calendar **day** (clips
   rarely need sub-day forensic dating, and day-level is where signals reliably agree). The
   exact clock time is carried in `verdict_time_local` when a precise signal supplies it. OK, or
   do you ever need the verdict itself to be a precise timestamp?
2. **Default `--tolerance` = 1 day.** Allows timezone/near-midnight slop between a UTC container
   tag and a local burned-in overlay. Looser (2-3 days) catches more agreements but weakens
   conflict detection. Keep 1?
3. **Auto-run OCR vs consume existing `ocr.json`?** This spec keeps `becky-dates` model-free and
   consumes an existing `becky-ocr` output (clean cloud/local split). Alternative: a `--run-ocr`
   convenience that shells out to `becky-ocr.exe` first. Add that convenience flag, or keep the
   tool strictly a consumer?
4. **Filename token conventions to cover.** Listed the common phone/screen-recorder patterns.
   Are there case-specific naming conventions in your corpus (a camera/app that names files a
   particular way) I should add to the parser?
5. **`.info.json` `upload_date`.** Treated as a labelled *context note only* (publish ≠ capture).
   Confirm it should never influence the verdict — only inform a reviewer.
6. **Index integration (later).** Should the verdict write a `clip_date` row into `forensic.db`
   so the corpus becomes filterable by date range (like `ocr_text` made it filterable by text)?
   Out of scope for v1, flagged for a follow-up if useful.
