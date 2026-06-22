# SPEC-BECKY-INGEST.md — `becky ingest`: one-command corpus ingest + a human/LLM-readable DIGEST.md

> **BUILT 2026-06-22 (cloud) — the digest formatter + `ingest` op are DONE and cloud-verified via
> `--no-pipeline`; only the REAL-model pipeline run is left for local. See §7 STATUS + the DONE/LEFT-FOR-LOCAL
> split.** Authored 2026-06-22; grounded in the real code (every design claim cites `file:symbol`).

---

## 1. Purpose + the user need (one paragraph)

`becky ingest <folder> --kb kb-final` runs the whole forensic chain over a folder of
clips with ONE command, and then writes **one human-readable `DIGEST.md`** that, per
clip, states the **capture-time**, a plain-language **who / what / where** summary, and
the **unknowns that still need a human**. The need is concrete: a finished pipeline run
leaves 8+ raw JSON sidecars *per clip* (`transcript.json`, `diarized.json`, `events.json`,
`osint-manifest.json`, `ocr.json`, `embed.json`, `identify.json`, `motion.json`,
`validate.json`, `report.json`) — nobody, and no LLM context window, wants to read 8 JSON
files × N clips to understand a case. Today `becky-pipeline` already produces those
sidecars (`cmd/pipeline/main.go`) and `becky-report` already distills *one clip's*
sidecars into a corroborated case report (`cmd/report/main.go`, `internal/report`). What
is missing is the **corpus-level front door**: a single verb that drives the pipeline over
the folder *and* rolls every clip's `report.json` up into one skimmable `DIGEST.md` an
investigator (or a downstream model) reads top-to-bottom. `becky ingest` is that verb. It
invents no new analysis — it **orchestrates** the existing tools and **formats** their
already-corroborated output.

---

## 2. How it orchestrates the existing pipeline (and where DIGEST.md slots)

`becky ingest` is a **new op on the existing `becky` orchestrator** (`cmd/becky/main.go`),
alongside `enroll-wiki` / `index` / `find` / `corroborate`. It is a thin driver in the
exact style of the others: it shells out to existing binaries via the shared plumbing in
`cmd/becky/runner.go` (`commonFlags.binPath`, `runTool`) and **re-implements no analysis**.

Pipeline flow (nothing here is new analysis — every box already exists):

```
becky ingest <folder> --kb kb-final
   │
   ├─(1) shell: becky-pipeline <folder> --out <out> --kb <kb> --steps <set> [--resume]
   │        → runs the forensic chain per clip; writes <out>/<stem>/*.json + <out>/manifest.json
   │          (cmd/pipeline/main.go: processVideo → runStep; canonicalOrder in steps.go)
   │
   ├─(2) read <out>/manifest.json (cmd/pipeline/manifest.go: Manifest, VideoResult, StepResult)
   │        → the authoritative list of clips, per-step status, output dirs
   │
   ├─(3) per clip, build the corroborated report from its sidecars
   │        report.LoadSidecars(transcript, events, identify, motion)  (internal/report/load.go)
   │        report.Build(sidecars, stem)                                (internal/report/build.go)
   │        + read the capture-time / GPS block from <stem>/osint-manifest.json
   │          (the metadata field — exifmeta.Metadata, cmd/osint/osint.go:142)
   │
   └─(4) format ONE DIGEST.md from all per-clip reports + the manifest
            (NEW internal/digest formatter — §3)
            → <out>/DIGEST.md  +  <out>/digest.json (machine manifest, §4)
```

**Relationship to `becky-report` (reuse, do not reinvent):**
- `becky-report` is **per-clip**: it turns one clip's sidecars into one `report.json` /
  `report.md` and is *already a pipeline step* (`stepReport` in `cmd/pipeline/steps.go:30`,
  `reportStepArgs` in `cmd/pipeline/run.go:144`). `becky ingest` does **not** duplicate its
  corroboration logic — it calls `report.Build` (`internal/report/build.go:23`) directly so
  the "≥2 signals → DOCUMENTED" rule lives in exactly one place (`applyCorroboration`,
  `build.go:181`).
- `DIGEST.md` is **corpus-level**: it is a roll-up of every clip's `report.Report` into one
  document, *plus two things `report.Report` doesn't carry today*: (a) **capture-time +
  location** (those live in the osint metadata block, `exifmeta.Metadata` — not in the four
  sidecars `report.LoadSidecars` reads), and (b) an explicit **"Unknowns"** section per clip
  (driven from `report.ReviewItems` + `identifyOutput.Unidentified`, `internal/report/load.go:135`).
- `becky-report`'s own markdown (`internal/report/markdown.go:Markdown`) is **not** reused
  verbatim for the digest: it is per-clip, uses GitHub tables and emoji icons (`signalTable`,
  the `| Time | Type | … |` timeline, `✅`/`⚫`), which is the wrong shape for a skimmable,
  accessibility-clean corpus digest (see §3 + §5). The digest gets its **own linear
  formatter**; it reuses the report **data** (`report.Report`), not its rendering.

So the build order is: **(prefer) ask the pipeline to run `report` as a step** so each
`<stem>/report.json` already exists, then `becky ingest` just loads each `report.json` +
each `osint-manifest.json` and formats the digest. If a clip's `report.json` is missing
(report step absent/failed), `becky ingest` falls back to building the report itself in-
process via `report.Build` over whatever sidecars exist (§5). Either way the corroboration
math is `internal/report`'s, never re-coded.

---

## 3. The exact DIGEST.md layout

Design goals, in priority order: **skimmable, linear, plain-language, honest about
unknowns.** Per `FORENSIC-OUTPUT-PHILOSOPHY.md`: DOCUMENTED facts stated plainly by name;
CANDIDATE/unknown items flagged with their basis, never hedged into mush, never asserted.
Per `ACCESSIBILITY.md` (Jordan is low-vision, **sighted** — concise, high-contrast-friendly,
no screen-reader assumptions but also no meaning-by-color): **no GitHub tables, no emoji-
as-data, no ascii-art** — headings + short labelled lines a person scans and a model parses.

The file has a top **case summary**, then one **section per clip**, then a **corpus
unknowns roll-up**.

```markdown
# Case Digest — <folder name>

Generated: 2026-06-22T14:03:11Z
Clips: 12 ingested · 11 fully processed · 1 partial
Knowledge base: kb-final
People concluded across the corpus: John Clancy, Shelby Reed
Earliest capture: 2025-08-14 19:32 (-05:00) · Latest: 2025-09-02 11:07 (-05:00)

---

## 1. reddit-livestream-2025-08-14.mp4

When (capture-time): 2025-08-14 19:32:07 (-05:00)  [source: quicktime — trusted]
Duration: 14:22
Where: GPS 41.8781, -87.6298 (lat/long in file)   |   no on-screen address text found
Device: Samsung Galaxy S25 Ultra (SM-S938U1)

Who:
- John Clancy — DOCUMENTED (voice + face, confidence 0.88). Appears 0:13–2:41, 9:05–11:20.
- Shelby Reed — DOCUMENTED (voice + face + location, confidence 0.91). Appears 1:50–6:30.

What (key moments):
- 0:13 — John taps her hip, she turns it into hand-holding, he pulls her in. [DOCUMENTED, events]
- 1:02 — speech: "I want the penguin." [DOCUMENTED, transcript]
- 9:48 — sub-second motion burst (score 0.82), not visible at 1 fps — REVIEW. [CANDIDATE, motion]

Unknowns / needs a human:
- An unidentified man (candidate: Mark, voice 0.71) at 4:10 — single signal, not concluded.
- becky-validate ran but found 0 observations on this clip — re-check the 9:48 window by eye.

Sidecars: pipeline-out/reddit-livestream-2025-08-14/  (report.json, transcript.json, …)

---

## 2. <next clip>.mp4
…(same shape)…

---

## Corpus unknowns (everything still needing a human)

- reddit-livestream-2025-08-14.mp4 @ 4:10 — unidentified man, candidate "Mark" (voice 0.71).
- backyard-2025-08-29.mov @ 0:00 — capture-time fell back to file mtime (UNTRUSTED) — date unverified.
- (3 clips skipped identify: no --kb match — listed under each clip above.)

## Notes
- 1 clip is PARTIAL: garden-clip.mkv — becky-diarize failed (see manifest.json).
- becky-validate was not run (no GPU model available); motion bursts are localized but undescribed.
```

**Per-clip section contract (the load-bearing layout):**

| Line | Source field | Behaviour when missing |
|---|---|---|
| `## N. <stem>.<ext>` | `VideoResult.Input` / `report.Report.Source` | always present |
| `When (capture-time):` | osint `metadata.capture_time_local` + `utc_offset` + `capture_time_source` (`exifmeta.Metadata`, `exiftool.go:setCaptureTimes`) | `unknown — no capture tag` |
| `Duration:` | `report.Report.Duration` (`build.go:clipDuration`) → `formatTime` | omit line if 0 |
| `Where:` | osint `metadata.gps` + any OCR'd address text | `no location signal` |
| `Device:` | osint `metadata.device_name` / make+model | omit line if absent |
| `Who:` | `report.Report.Entities` (DOCUMENTED first, then CANDIDATE), `Appearances` → spans | `nobody identified` |
| `What (key moments):` | `report.Report.Conclusions` + high-signal `Timeline` moments | `no notable moments` |
| `Unknowns / needs a human:` | `report.Report.ReviewItems` + identify `Unidentified` + per-step degrade notes | `none flagged` |
| `Sidecars:` | the clip's `<out>/<stem>/` dir + the file names present | always (the audit trail) |

**Forensic-output rules baked into the formatter** (these are assertions the unit tests
check, §7): the `capture_time_source` is **always shown** and the word **UNTRUSTED** is
emitted verbatim when the source is `mtime(untrusted)` (`exifmeta.SourceMTime`,
`exifmeta.go:32`) — a copied/synced file's mtime must never read as a capture time; a
DOCUMENTED person is written **by name with no hedging**; a CANDIDATE is written with its
single-signal basis and confidence and the words "not concluded" / "REVIEW"; sub-second
motion bursts carry the "not visible at 1 fps" note from `build.go:271`. The "Unknowns"
section is **never empty-by-omission** — if there are no unknowns it says `none flagged`,
so a reader can trust that absence means "nothing pending," not "the tool forgot."

---

## 4. CLI flags + behaviour + the JSON manifest

```
becky ingest <folder> [--kb <dir>] [--out <dir>] [--steps <set>]
                      [--bin <dir>] [--resume] [--force]
                      [--digest <path>] [--no-pipeline] [--verbose] [--json]
```

| Flag | Meaning | Default |
|---|---|---|
| `<folder>` (positional) | the corpus folder of clips to ingest | required |
| `--kb <dir>` | knowledge base for the `identify` step | "" (identify skipped) |
| `--out <dir>` | pipeline output root (passed straight to `becky-pipeline --out`) | `pipeline-out` |
| `--steps <set>` | step set passed to `becky-pipeline --steps`; `report` is force-appended if absent so each clip has a `report.json` | the ingest default set, below |
| `--bin <dir>` | where `becky-*.exe` live (shared `commonFlags`, `runner.go:binPath`) | next to the running binary |
| `--resume` | skip clips/steps already done (forwarded to `becky-pipeline`; the pipeline is already resumable/idempotent, `cmd/pipeline/main.go:82`) | off |
| `--force` | re-run every step (forwarded) | off |
| `--digest <path>` | where to write DIGEST.md | `<out>/DIGEST.md` |
| `--no-pipeline` | **skip running the pipeline**; only (re)build the digest from existing `<out>/` sidecars — fast, offline, no models needed (the cloud-runnable / re-format path, §6) | off |
| `--verbose` | progress on stderr (`headline` / `beckyio.Logf`) | off |
| `--json` | suppress the plain-English headline; still writes digest.json (`commonFlags.jsonOut`, `runner.go:64`) | off |

**Default `--steps`:** the deterministic sweep `becky-pipeline` runs by default
(`transcribe,metadata,diarize,events,osint,ocr` — `cmd/pipeline/steps.go:64`) **plus**
`identify` when `--kb` is given, **plus** `report`. `embed`/`validate` stay opt-in (they
need the embedding server / GPU model). `becky ingest` appends `report` to whatever the
user passes so the digest's preferred input always exists.

**Behaviour:**
1. Validate `<folder>` exists and is a directory; error out cleanly otherwise (mirror
   `cmd/pipeline/main.go:65`).
2. Unless `--no-pipeline`: run `becky-pipeline` once over the folder via `runTool`
   (`runner.go:148`), forwarding `--out/--kb/--steps/--bin/--resume/--force/--verbose`.
   becky-pipeline already writes `<out>/manifest.json` and exits 0 even with partial
   clips, so a single failed step never aborts ingest.
3. Read `<out>/manifest.json` into the `Manifest` shape (`cmd/pipeline/manifest.go:8`) —
   this is the authoritative clip list (not a re-scan of the folder), so ingest reports
   exactly what the pipeline processed.
4. For each `VideoResult`: load `report.json` if present (`<out_dir>/report.json`); else
   `report.Build` in-process from the sidecars that exist (`report.LoadSidecars`). Read
   `<out_dir>/osint-manifest.json` for capture-time/GPS/device.
5. Format and write `<out>/DIGEST.md` and `<out>/digest.json`.
6. Print a one-line headline to stderr ("Digest: 12 clips, 11 ok, 1 partial → <out>/DIGEST.md")
   and the `digest.json` to stdout (the structured result, like every other `becky` op).

**`digest.json` (the machine manifest — same data as DIGEST.md, for chaining):**

```json
{
  "tool": "becky-ingest v1.0.0",
  "folder": "/cases/reddit",
  "generated_at": "2026-06-22T14:03:11Z",
  "out_root": "/cases/reddit/pipeline-out",
  "kb": "kb-final",
  "clips": [
    {
      "stem": "reddit-livestream-2025-08-14",
      "input": "/cases/reddit/reddit-livestream-2025-08-14.mp4",
      "status": "ok",
      "capture_time_local": "2025-08-14T19:32:07-05:00",
      "capture_time_source": "quicktime",
      "capture_trusted": true,
      "duration": 862.0,
      "gps": {"latitude": 41.8781, "longitude": -87.6298},
      "device": "Samsung Galaxy S25 Ultra",
      "concluded_people": ["John Clancy", "Shelby Reed"],
      "candidate_people": ["Mark"],
      "documented_count": 5,
      "review_count": 2,
      "sidecar_dir": "/cases/reddit/pipeline-out/reddit-livestream-2025-08-14",
      "notes": ["validate produced 0 observations"]
    }
  ],
  "corpus": {
    "clips_total": 12, "clips_ok": 11, "clips_partial": 1,
    "people_concluded": ["John Clancy", "Shelby Reed"],
    "earliest_capture": "2025-08-14T19:32:07-05:00",
    "latest_capture": "2025-09-02T11:07:44-05:00",
    "unverified_dates": ["backyard-2025-08-29"]
  },
  "degraded": false,
  "notes": ["becky-validate not run (no GPU model available)"]
}
```

All slices initialised to `[]` (never `null`), RFC3339 timestamps — matching the house
conventions in `cmd/pipeline/manifest.go` and `internal/report/types.go`.

---

## 5. Deterministic · offline · degrade-never-crash

- **Deterministic.** The digest formatter is pure data→text: the same `<out>/` sidecars
  always yield byte-identical `DIGEST.md` (clips ordered as `manifest.json` lists them,
  which is the pipeline's sorted order; entities/findings already sorted deterministically
  in `internal/report/build.go:sortFindings`/`sortSpans`). `generated_at` is the one
  non-deterministic field (a timestamp) — isolate it so tests can pin it (pass a clock, as
  `report.Build` uses `time.Now().UTC()` once at `build.go:26`).
- **Offline (the formatter half).** `--no-pipeline` builds the digest with **zero** model,
  network, ffmpeg, or GPU calls — pure file-read + format. This is the cloud-runnable proof
  path (§6) and the everyday "re-format after I read it" path. The pipeline half (step 2)
  is exactly as online/heavy as `becky-pipeline` already is — ingest adds nothing.
- **Degrade-never-crash, at three layers:**
  1. **Missing pipeline step / sidecar** → that clip's section simply omits the line and,
     where it matters, says so (`no location signal`, `capture-time: unknown`). `report.Build`
     already degrades over missing sidecars (`report.LoadSidecars` leaves fields nil,
     `internal/report/load.go:21`; `Build` sets `Degraded=true` when nothing useful exists,
     `build.go:48`). A degraded clip is rendered as a stub section + a note, never skipped
     silently and never a panic.
  2. **Missing `report.json` for a clip** → fall back to in-process `report.Build` over
     whatever sidecars are on disk.
  3. **Missing `manifest.json`** (e.g. `--no-pipeline` against a dir that was never run) →
     fall back to scanning `<out>/*/` subdirs for sidecars, emit a note, exit 0 with a
     partial digest. `<folder>` itself missing → clean usage error (non-zero), the one
     genuine fatal.
- **Read-only / no source mutation.** Like the rest of the suite, ingest only reads the
  source clips and writes under `<out>/`; it never modifies a source video (the pipeline's
  invariant, `cmd/pipeline/main.go:22`).
- **Exit codes:** 0 on success (incl. partial clips + degraded sections); non-zero only on
  a usage error (`<folder>` missing) or a write failure for `DIGEST.md`. A degraded *digest*
  (no useful data anywhere in the corpus) sets `digest.json.degraded=true` and is noted in
  DIGEST.md but still exits 0 (the operator gets the empty-but-honest digest).

---

## 6. Cloud-vs-local split

The model boundary cuts cleanly between the **pipeline** (heavy, local) and the **digest
formatter** (deterministic, cloud-buildable).

| Cloud agent CAN build + verify (no GPU/models/ffmpeg) | Local agent (Jordan's PC) finishes |
|---|---|
| The new `internal/digest` package: `Build(reports, manifest, meta) Digest` + `Markdown(Digest) string` + `digest.json` encoder. | Run `becky ingest <real folder> --kb kb-final` end-to-end on real footage (needs Parakeet/ffmpeg/InsightFace/GPU via the pipeline). |
| The `becky ingest` op in `cmd/becky` (flag parsing via `runner.go` helpers, the `becky-pipeline` argv, manifest+sidecar loading). | Confirm capture-time/GPS render correctly from a real `osint-manifest.json` (exiftool/ffprobe present locally). |
| The capture-time/GPS extraction *from an existing osint-manifest.json* (it is plain JSON; reuse `exifmeta.Metadata`'s field tags — no probe call). | Sound/eyes check that the DIGEST reads well for a real case; tune thresholds for "key moments." |
| **The one-command offline proof:** `becky ingest <fixture-out> --no-pipeline --digest /tmp/DIGEST.md` over a committed fixture `pipeline-out/` of hand-authored sidecars → assert the DIGEST.md content (byte-diff against a golden file). This exercises the real formatter code path with no hardware. | `build-all-tools.bat` (the `becky` exe is rebuilt; no new `cmd/*` since ingest is a new op on existing `cmd/becky`). |
| All unit tests (§7). | Verify `--resume` over a partially-ingested corpus. |

The cloud→local handoff therefore ships a **provable** artifact (per CLAUDE.md §2/§4 + the
`HANDOFF-TEMPLATE.md` rule): the `--no-pipeline` run over the fixture is the one-command,
no-hardware proof the formatter works, and the checkboxed work order in §7 is the ordered
command list the local agent drives to completion.

---

## 7. Checkboxed build plan + the unit tests

> **STATUS 2026-06-22 (cloud, branch `claude/subagent-deployment-scaling-4hptv9`): BUILT + cloud-verified
> via `--no-pipeline`.** The digest formatter + the `ingest` op + the golden offline proof are DONE; the one
> thing left is running the REAL pipeline (Parakeet/ffmpeg/InsightFace/GPU) on a real folder, which needs
> Jordan's models. Files changed: `cmd/becky/ingest.go` (new), `cmd/becky/ingest_test.go` (new),
> `cmd/becky/testdata/DIGEST.golden.md` (new), `cmd/becky/main.go` (switch + usage + header — the only
> edit to an existing cmd/becky file), `internal/digest/{digest,types,markdown,digest_test}.go` (new pkg).

**Build plan (cloud, in order):**

- [x] **Create `internal/digest`** — the corpus formatter. Consumes `report.Report` + a `CaptureMeta`
      decoded from `osint-manifest.json`'s `metadata` block (mirrors `exifmeta.Metadata` field tags; does
      NOT import exifmeta — no probe call). Types `Digest`/`ClipDigest`/`CorpusSummary`/`CaptureMeta`.
- [x] `digest.Build(clips []ClipInput, info CorpusInfo, clock func() time.Time) Digest` — deterministic;
      clip order = manifest order; corpus people = de-duped union of concluded names; earliest/latest from
      TRUSTED captures only. (Signature note: I bundled per-clip report+capture+status into one `ClipInput`
      rather than the spec's three parallel collections — it keeps the manifest status, report, and capture
      for one clip together and avoids a fragile stem→map join. Same data, cleaner call site.)
- [x] `digest.Markdown(Digest) string` — the §3 linear layout. **No tables, no emoji, no box-drawing**
      (a test asserts the output contains none of them).
- [x] `digest.JSON(Digest) ([]byte, error)` — `[]` not null, RFC3339, trailing newline.
- [x] **Added the `ingest` op to `cmd/becky`**: `runIngest` in new `cmd/becky/ingest.go`, wired into the
      `switch op` (`case "ingest"`) + a usage line + the header comment in `main.go`. Flags parsed via the
      existing `runner.go` helpers (`extractCommon`/`flagValue`/`hasFlag`/`dropFlag`).
- [x] `runIngest`: builds the `becky-pipeline` argv + runs it via `runTool(cf, "pipeline", args)` unless
      `--no-pipeline`; loads `<out>/manifest.json` (falls back to scanning `<out>/*/` when absent); per clip
      loads `report.json` or rebuilds via `report.Build` over the on-disk sidecars; reads
      `osint-manifest.json` for capture; writes `<out>/DIGEST.md` + `<out>/digest.json`; headline + stdout JSON.
- [x] Wire `report` into the forwarded `--steps` (force-appended; `--kb` also appends `identify`; deduped).
- [x] Documented the op in `cmd/becky/main.go`'s header comment + `usage`.
- [x] `go build ./...`, `go vet ./cmd/becky/... ./internal/digest/...`, `go test ./...`, `gofmt -l` on the
      touched dirs — ALL GREEN (gates 1–4). **Gate 5 (`build-all-tools.bat`) is the LOCAL completion step**
      — no new `cmd/*` (ingest is a new op on the existing `cmd/becky`), so the existing auto-discover
      rebuilds `becky.exe` with no script change.

**Unit tests (assert VALUES from fixture sidecars — all PASSING):**

- [x] `internal/digest` content test: DOCUMENTED person named plainly on `Who:`, the literal
      `capture_time_source` + "trusted", the CANDIDATE "not concluded" basis, GPS, device, the
      conclusion line; and a separate test that an `mtime(untrusted)`-only clip emits the literal
      **`UNTRUSTED`** word + lands in `unverified_dates`.
- [x] **Accessibility test:** asserts the Markdown contains NO table-pipe rows, NO box-drawing chars,
      NO emoji-as-meaning (`✅/⚫/❌/...`).
- [x] **Degrade test:** a `Degraded==true` clip renders a stub section + a PARTIAL note, no panic.
- [x] **Empty-unknowns test:** a clip with zero review items → the per-clip `Unknowns` block reads
      `none flagged` (never omitted).
- [x] **Determinism test:** `Build`+`Markdown` over the fixture twice (fixed clock) → byte-identical.
- [x] **Corpus roll-up test:** overlapping people → de-duped union; earliest/latest from trusted only;
      untrusted-date clip → `unverified_dates`.
- [x] **JSON shape test:** empty corpus → `clips: []` (not null), `degraded: true`, exit 0.
- [x] `cmd/becky` `ingestSteps` flag test: `--kb` ⇒ `identify` present; `report` always appended exactly
      once; explicit minimal set keeps only what was asked + report.
- [x] **Golden-file proof:** `TestIngest_NoPipeline_GoldenProof` builds a fixture `pipeline-out/` and runs
      `runIngest(... --no-pipeline ...)`, asserting the DIGEST.md matches `cmd/becky/testdata/DIGEST.golden.md`
      (paths/timestamp normalized) + the load-bearing lines. **This is the §6 one-command offline proof.**

### DONE (cloud) vs LEFT FOR LOCAL

**DONE (cloud, fully verified with no hardware):**
- The whole `internal/digest` package + the `cmd/becky ingest` op + `--no-pipeline`.
- The offline proof, RUN: `go test ./cmd/becky/... -run TestIngest_NoPipeline_GoldenProof` is green, and the
  built binary was driven live — `becky ingest <fixture-out> --no-pipeline --kb kb-final` produced a correct
  2-clip DIGEST.md (trusted capture labelled "trusted"; the mtime-only clip labelled **UNTRUSTED** and listed
  under corpus unknowns + `unverified_dates`; the partial clip noted; DOCUMENTED person named, CANDIDATE
  flagged, sub-second motion routed to REVIEW; linear, no tables/emoji). The formatter is fully cloud-proven.

**LEFT FOR LOCAL (needs Jordan's models — cloud cannot run these):**
- [ ] Run the REAL pipeline end-to-end: `becky ingest <real folder> --kb kb-final` (drives becky-pipeline →
      Parakeet ASR + ffmpeg + InsightFace + the report step), then read the generated `<out>/DIGEST.md`.
- [ ] Confirm capture-time/GPS/device render correctly from a REAL `osint-manifest.json` (exiftool/ffprobe
      present locally) — the cloud only exercised hand-authored fixtures.
- [ ] `build-all-tools.bat` (rebuilds `becky.exe`; no new `cmd/*`).
- [ ] Verify `--resume` over a partially-ingested corpus (forwarded straight to becky-pipeline).
- [ ] Eyes-check that the DIGEST reads well for a real case; tune the "key moment" cap (§8 decision 2) if 5
      is too few/many.

---

## 8. Open Decisions for Jordan

1. **Default step set.** Proposed default is `transcribe,metadata,diarize,events,osint,ocr,
   (+identify if --kb),report`. Should `embed` (corpus search index) be on by default during
   ingest, or stay opt-in (it needs the resident embedding server)? Recommend opt-in.
2. **What counts as a "key moment" in `What:`.** Proposed: all DOCUMENTED conclusions +
   sub-second motion bursts + the N highest-confidence events. Cap N at ~5 per clip so the
   digest stays skimmable, or list all? Recommend cap with a "(+k more in report.json)" line.
3. **Per-clip `report.md` too?** The pipeline can already write each clip's `report.md`
   (`stepReport`). Keep those AND the corpus DIGEST.md (the digest links to them), or have
   ingest suppress per-clip markdown to avoid clutter? Recommend keep both (DIGEST = index,
   report.md = drill-down).
4. **Folder recursion.** `becky-pipeline` scans a folder **non-recursively**
   (`discoverVideos`, `cmd/pipeline/main.go:178`). Should `becky ingest` add `--recursive`
   for nested case folders, or match the pipeline's flat behaviour? Recommend match (flat)
   for v1; add `--recursive` only if a real case needs it.
5. **Capture-time when EXIF is absent.** When the only date is `mtime(untrusted)`, the digest
   labels it UNTRUSTED and lists the clip under `unverified_dates`. Is that the right default,
   or should ingest also invoke the planned `becky dates` triangulation (README "Planned
   features") once it exists, to recover a date from in-frame timestamps? Recommend wire to
   `becky dates` later — out of scope for ingest v1.
