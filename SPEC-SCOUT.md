# SPEC-BECKY-SCOUT — YouTube playlist scout

`becky-scout` reads a YouTube playlist and answers one question per video:
**does it name something becky should adopt, upgrade, or build?**  Jordan
keeps "look at this" videos in YouTube playlists the same way he keeps model
cards in Chrome (see becky-radar). This tool turns that playlist into
structured becky action.

---

## What it does

For each video `becky-scout` gathers the text becky can read offline — title,
channel, description, tags, captions (transcript) — and runs them against
three **independent** signals.  It then follows the becky forensic discipline:
**CORROBORATE, then CONCLUDE** (FORENSIC-OUTPUT-PHILOSOPHY.md):

| Signals that agree | Lane | Label |
|---|---|---|
| ≥ 2 | RELEVANT | Stated conclusion — worth acting on. |
| 1 | CANDIDATE | One signal only — review. |
| 0 + personal-interest hit | USEFUL TO YOU | Not a becky match, but Jordan's interest areas. |
| 0 | (skipped) | Counted, not listed. A flood of maybes is a tool failure. |

### The three independent signals

1. **dep-match** — The video names a model or library in becky's freshness
   manifest (the same cross-reference becky-radar uses for browser history).
   A dep-match signals "IMPROVE" — an upgrade candidate for an existing tool.

2. **capability** — The video sits in a becky capability domain by keyword
   (catalog.go: speech-to-text, speaker diarization, OCR, embeddings, VLM,
   video understanding, local LLMs, agents, OSINT, music generation, audio
   analysis, DAW production).  A capability-only hit means "EXTEND" — a new
   tool or technique to build.

3. **assessor** (optional) — An independent local-model opinion.  The model
   is called once per video and its verdict is a *third* signal, never the
   sole basis for a conclusion.  With no assessor wired the tool degrades to
   the deterministic floor.

---

## CLI

```
becky-scout <playlist-url-or-id> [--json] [--catalog]
becky-scout --from-json <file>  [--json]
```

- `--json` emits a JSON report (arrays: `relevant`, `candidates`, `useful`;
  fields: `dep_matches`, `becky_tools`, `signals`, `score`, `verdict`, …).
- `--catalog` prints becky's capability catalog + Jordan's interest catalog
  (what exactly scout looks for) and exits.
- `--from-json <file>` offline mode: assess a pre-fetched playlist JSON file
  (no yt-dlp).  Accepts a bare `[{"id","title",…},…]` array **or** a
  `{"id","title","url","videos":[…]}` object (the shape yt-dlp emits).

Exit codes: 0 ok, 1 error, 2 usage.

---

## Output (plain-text default)

```
becky-scout — videos that could improve or extend becky-tools
======================================================================
playlist : My AI tools watchlist
assessed : 12 video(s)   |   signal source: deterministic floor (no model wired)

RELEVANT (corroborated — worth acting on)
----------------------------------------------------------------------
- [IMPROVE] PaddleOCR PP-OCRv6 deep dive: best OCR for documents
    url    : https://youtube.com/watch?v=…
    becky  : becky-ocr
    tracks : PaddleOCR PP-OCRv6 (becky pins PP-OCRv6 -> v5, used by becky-ocr)
    verdict: RELEVANT — corroborated: names a model/tool becky tracks AND sits
             in a becky capability area. Likely an UPGRADE for becky-ocr.

CANDIDATES (one signal only — review)
----------------------------------------------------------------------
…

USEFUL TO YOU (not a becky tool, but in your interest areas)
----------------------------------------------------------------------
…

----------------------------------------------------------------------
1 relevant, 2 candidate(s), 3 useful-to-you, 6 off-topic (skipped).
Tell Claude which to act on (e.g. "build a tool for the first relevant one").
```

---

## Code layout

```
cmd/scout/
  main.go          # CLI: flags, unwiredSource, fileSource, printReport
  main_test.go     # fileSource formats + unwiredSource degrade

internal/scout/
  scout.go         # Build(), assessVideo(), Video/Playlist/Item/Report types,
                   # PlaylistSource + Assessor interfaces
  catalog.go       # Capability type, matchCapabilities(), DefaultCatalog()
  interests.go     # Interest type, matchInterests(), DefaultInterests()
  fake.go          # FakePlaylist + FakeAssessor (CI; no network/model)
  scout_test.go    # 10 table-driven unit tests
```

---

## Cloud ↔ local split

**Cloud (done — this branch):** deterministic Go core, CLI scaffold,
`fileSource` offline mode, `unwiredSource` stub with wiring contract, 14 unit
tests, `--catalog` inspector, plain-text and JSON output.

**Local agent — two stubs to wire:**

### 1. `ytdlpSource` — the online playlist fetch

Replace `unwiredSource{}` in `cmd/scout/main.go`:

```go
type ytdlpSource struct{}

func (ytdlpSource) Playlist(ref string) (scout.Playlist, error) {
    // Step 1 — fetch the playlist entry list (no video download):
    //   yt-dlp --flat-playlist -J <ref>
    //   → parse .entries[]; each entry has .id, .title, .url, .playlist_index
    //
    // Step 2 — per video, fetch description/tags/channel + auto-captions:
    //   yt-dlp -J --write-auto-subs --sub-format vtt --skip-download <url>
    //   → .description, .tags, .channel; read the .vtt file → strip timestamps
    //     → plain-text transcript.
    //
    // Return scout.Playlist{ID, Title, URL, Videos} ordered by playlist_index.
    // Errors should describe what yt-dlp said (for the degrade message).
}
// Then in main(): var src scout.PlaylistSource = ytdlpSource{}
```

### 2. `llamaAssessor` — the local-model third signal (optional)

```go
type llamaAssessor struct{ bin, model string }

func (a llamaAssessor) Assess(v scout.Video, catalog []scout.Capability) (scout.Assessment, error) {
    // Build a compact prompt (title+channel+tags+first 500 chars of description,
    // and a list of the catalog domains). Call:
    //   <bin> -m <model> --temp 0 --seed 42 -p <prompt> --json-schema <schema>
    // Parse into scout.Assessment{Relevant, Tools, Ideas, Kind, Why, Confidence}.
    // Return Assessment{Relevant:false} on any error (degrade, never crash).
}
// Pass to scout.Build() as the assessor argument.
```

Recommended model: **Qwen3-4B-Instruct Q4_K_M** (already in becky's model
dir) — small enough that per-video calls are fast, instruction-tuned,
outputs strict JSON.

---

## Open decisions for Jordan

1. **Which playlists to scout?** The tool accepts any public YouTube playlist
   URL or ID. Do you have a "AI stuff to look at" playlist Jordan already
   keeps, or should the readme suggest one?

2. **Model for the assessor signal?** Qwen3-4B is the current suggestion
   (fast, already present). Gemma-3-4B or LFM2.5 1.6B are alternatives. Only
   matters once the local agent wires it; the tool runs fine without it.

3. **Recurring run / integration with becky-radar?** becky-radar already reads
   Jordan's Chrome history. A future step could export that history as a
   playlist-like JSON and pipe it into becky-scout for the same corroboration
   treatment.  Parked — no action needed yet.
