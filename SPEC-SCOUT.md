# SPEC-SCOUT.md — `becky-scout`: turn a YouTube playlist into becky improvements

> **STATUS: BUILT (cloud, 2026-06-16) — deterministic core + tests + CLI; the
> yt-dlp fetch and the optional local-model assessor are stubbed behind
> interfaces for the local agent to wire.** Sibling of `becky-radar`: radar reads
> what Jordan flags in *Chrome*; scout reads what he collects in a *YouTube
> playlist*. Both cross-reference the same `becky-freshness` manifest so a flagged
> idea becomes becky action instead of staying in Jordan's head.

---

## 1. The problem (first principles)

Jordan discovers things on his phone. Some of that discovery is **YouTube** —
talks, model walkthroughs, "I built X with Y" videos — and he saves them to a
playlist as a "watch / becky should look at this" queue. Nothing in becky reads
that queue, so the same failure mode as the PP-OCRv6 miss applies: a good idea in
a video he watched is invisible to becky until he re-types it from memory.

The cure is a tool that **ingests a playlist, reads the text becky CAN process
offline for each video (title, channel, description, tags, captions), and asks
one question per video: does this name something becky should adopt, upgrade, or
build?** — then cross-references becky's own manifest and capabilities and
**concludes**, instead of handing Jordan a wall of links.

## 2. The forensic discipline (why it isn't just a keyword grep)

becky's rule (FORENSIC-OUTPUT-PHILOSOPHY.md) is **corroborate, then CONCLUDE.** A
single weak signal is a *candidate*; ≥2 independent signals agreeing is a stated
*conclusion*; a flood of maybes a human must hand-sort is a tool failure. Scout
applies it with three **independent** signals per video:

1. **dep-match** — the video names a model/library in becky's freshness manifest
   (e.g. "PaddleOCR", "InsightFace", "Whisper"). The *improve* case: an upgrade
   candidate for an existing tool. (Same manifest cross-reference becky-radar uses.)
2. **capability** — the video sits in a becky capability domain by keyword (OCR,
   ASR, diarization, embeddings, VLM, agents, music/DAW…). It relates to a tool.
3. **assessor** — an OPTIONAL local model independently calls the video relevant
   to becky. Never the sole basis for a conclusion; it only corroborates.

Buckets:
- **relevant** (`score ≥ 2`) — corroborated → stated conclusion (improve/extend).
- **candidate** (`score == 1`) — review.
- **useful** (no becky signal, but a personal-interest hit) — see below.
- **skipped** (`score == 0`, no interest) — off-topic; **counted, not enumerated**.

`improve` = matches a tracked dependency (upgrade an existing tool); `extend` =
becky-domain but nothing tracked yet (a new tool/model to build).

### 2a. The "useful to you" lane (Jordan, 2026-06-16)

Jordan: *"even if something doesn't exactly qualify as a becky-tool it could still
be useful to me."* So scout asks a **second, lower-stakes question** per video,
independent of the becky lens: does it land in one of Jordan's interest areas
(`internal/scout/interests.go` — AI agents/assistants, local & open-source AI, AI
for music/audio, AI for video/images, document/notes/knowledge tools,
productivity/automation, AI how-to)? A non-becky video with ≥1 interest hit is
surfaced as **useful** — a *suggestion*, not a forensic conclusion (the becky lane
keeps its ≥2-signal rigor). Items are labelled by interest area and ranked by how
many areas they touch, so a curated "ai useful" playlist becomes an organized,
deduped, becky-cross-referenced shortlist rather than a wall of links.

## 3. What it does (pipeline)

```
becky-scout <playlist-url-or-id> [--json] [--catalog]
becky-scout --from-json <file> [--json]      # assess a pre-fetched playlist offline

1. RESOLVE the playlist → [{id,url,title,channel,description,tags,transcript,position}]
   (PlaylistSource: the one online step, via yt-dlp — wired by the local agent;
    OR --from-json reads a pre-fetched dump with no network — the offline path).
2. For each video, BUILD a lower-cased haystack from every offline-readable field.
3. SIGNAL 1 — cross-reference the becky-freshness manifest (tracked model named?).
4. SIGNAL 2 — match becky's capability catalog (in a becky domain?).
5. SIGNAL 3 — (optional) ask the local-model Assessor for an independent verdict.
6. CORROBORATE → relevant / candidate; ALSO check Jordan's interests → useful;
   classify improve vs extend; the rest are skipped (counted).
7. RENDER a deterministic, stably-sorted report (plain-language or --json).
```

`--catalog` prints becky's capability map AND Jordan's interest map (what scout
treats as "a becky area" / "useful to you") so he can see — and the local agent
can tune — exactly what it looks for.

`--from-json <file>` assesses a pre-fetched playlist offline. The file is a JSON
array of videos (or a `{videos:[...]}` object) — the shape a yt-dlp dump or a
simple `ytInitialData` scrape produces. This is the manual escape hatch (like
becky-radar's `urls-file`) and is how the tool was demoed on Jordan's real
"ai useful" playlist from the cloud (titles only, no yt-dlp): **15 becky
candidates, 28 useful-to-you, 57 off-topic** of 100 videos. (Titles alone don't
reach the ≥2 becky bar; descriptions+captions via yt-dlp will corroborate more.)

## 4. Output contract (synthetic values)

```json
{
  "tool": "becky-scout v1.0.0",
  "playlist": "Watch later — AI stuff",
  "playlist_id": "PLabc",
  "assessed": 12,
  "relevant": [
    {
      "id": "abc123", "url": "https://youtu.be/abc123",
      "title": "PP-OCRv6 walkthrough — best OCR for documents",
      "position": 3,
      "score": 2,
      "signals": ["dep-match", "capability"],
      "kind": "improve",
      "becky_tools": ["becky-ocr"],
      "dep_matches": [
        {"dependency_id": "paddleocr-pipeline", "name": "PaddleOCR PP-OCRv6",
         "used_by": ["becky-ocr"], "becky_pinned": "auto PP-OCRv6 -> v5 -> v4"}
      ],
      "ideas": ["a newer OCR pipeline could improve becky-ocr accuracy on hard frames."],
      "verdict": "RELEVANT — corroborated: names a model/tool becky tracks AND sits in a becky capability area. Likely an UPGRADE for becky-ocr."
    }
  ],
  "candidates": [
    {"id": "def456", "title": "Semantic search with embeddings", "score": 1,
     "signals": ["capability"], "kind": "extend", "becky_tools": ["becky-embed","becky-search"],
     "verdict": "CANDIDATE — touches a becky capability area; one signal only, review whether there's something to build."}
  ],
  "skipped": 9,
  "model": "deterministic floor (no model wired)",
  "degraded": false
}
```

Discipline: a viewed video is one signal → **candidate**; a video that names a
tracked dependency AND lands in a becky domain (or is independently confirmed by
the model) is **corroborated** → stated plainly as an upgrade/new-tool candidate.
It never changes anything; it surfaces, ranks, and concludes.

## 4a. The autonomous build gate (`--propose`) — Jordan, 2026-06-23

Jordan: *"the local model should be used for judgement… if Qwen thinks something
is genuinely useful it can propose a build spec and see if Gemma‑4 or Claude agree;
if yes, build the spec."* He does NOT want to hand-review findings. So `--propose`
runs a fully autonomous gate that is just becky's corroborate-then-conclude applied
to MODEL judgment:

```
for each surfaced video (relevant + candidate + useful):
  1. PROPOSE  — local Qwen pitches a concrete becky tool (strict-JSON intake:
                worth_building? slug, capability, input/output, kind, why).
                Most videos → worth_building=false (no flood).
  2. JUDGE    — Gemma‑4 (an INDEPENDENT model) votes agree/disagree on the pitch.
  3. CONCLUDE — APPROVED only when the proposer pitched AND ≥1 judge agreed
                (≥2 independent models concur). Held back otherwise.
  4. EMIT     — each approved proposal is written as a becky-new-tool intake
                (`scout-proposals/<slug>.intake.json`). With --build, scout hands
                it straight to `becky-new-tool --intake-file … --yes --offline`,
                which runs the EXISTING staged build factory.
```

scout owns only the *decision*; the actual building is becky-new-tool's job
(reusing its model stages, cost gates, and S4 Claude spec author — so "Claude
agrees" happens naturally downstream). Default is emit-only (write intakes);
`--build` is the explicit, money-spending opt-in. With no local models present it
degrades to a one-line note. Interfaces `Proposer`/`Judge` live in
`internal/scout/propose.go` (deterministic fakes + tests); the real Qwen/Gemma
backends are `cmd/scout/model.go` (llama-server, temp 0, the becky-ask transport).

## 5. Build split (cloud ↔ local)

| Cloud / web agent (DONE)                                       | Local agent (Jordan's PC — remaining)               |
|----------------------------------------------------------------|-----------------------------------------------------|
| This spec; the capability catalog + interests; corroboration   | `pip install yt-dlp`; run `build-all-tools.bat`     |
| Go orchestrator (`cmd/scout/`), JSON + plain-text contract     | Run live on the real playlist (`--deep`)            |
| `internal/scout` core + boundary interfaces + fakes + tests    | (Optional) wire `Assessor` to a local llama.cpp model|
| **Real `ytdlpSource`** (`cmd/scout/ytdlp.go`) — flat + `--deep`, tested live | Schedule `scout-watch.ps1` (or click it)  |
| `--from-json`, `--new-only`/`--state`, `scout-watch.ps1`       | Tune `catalog.go`/`interests.go` keywords on results |
| **Autonomous gate** (`internal/scout/propose.go` + `cmd/scout/model.go`) — Qwen propose + Gemma judge + intake emit; deterministic core tested | Run `--propose` with the local models present; decide whether the weekly watch runs `--build` (auto-build) or emit-only |

The yt-dlp `PlaylistSource` is **built and verified live** from the cloud (flat
mode returned all 100 videos of Jordan's playlist; `--deep` per-video enrichment
works but is rate-limited from a datacenter IP — on Jordan's home PC, with
`--cookies-from-browser chrome`, it runs clean). The only genuinely local steps
are installing yt-dlp, building the `.exe`, and the optional model assessor.

**`Assessor` contract** (optional): `Assess(video, catalog) → {relevant, becky_tools,
ideas, kind, why, confidence}`. With no Assessor wired, scout runs the
deterministic floor (signals 1+2) only and never crashes.

This opts out of becky's offline invariant exactly like `becky-research` /
`becky-radar` / `becky-palantir`: ONE explicit, logged network step (the playlist
fetch), a deterministic OUTPUT, and degrade-never-crash everywhere else.

## 6. Why this is the systemic fix (with freshness + radar)

- `becky-freshness` — "is what becky pins stale upstream?"
- `becky-radar` — "what did Jordan flag in Chrome that becky should act on?"
- `becky-scout` (this) — "what's in the videos Jordan saved that becky should act on?"

All three cross-reference the same manifest. Run scout as standard practice on the
"ai useful" playlist; relevant findings drop straight into the new-tool / upgrade
pipeline (`SPEC-BECKY-NEW-TOOL.md`).

**Running it regularly (the "watch for new entries" ask).** `--new-only --state
<file>` remembers which videos were already assessed and reports only newly-added
ones, so a repeat run is a short "what's new" digest, not a re-read of all 100.
`scout-watch.ps1` (repo root) wraps this for Jordan: double-click it to check now,
or run it once with `-Register` to have Windows Task Scheduler run it weekly and
save the new-finds digest to `scout-latest.txt`. (It can also be folded into the
existing "Get Becky Updates" button so the playlist digest rides along with the
freshness/radar digest — one button, nothing new for Jordan to remember.)

## 7. Open decisions for Jordan
1. **The playlist — ANSWERED (2026-06-16):** Jordan's "ai useful" playlist,
   `https://youtube.com/playlist?list=PLLnp7PR3IvheB68zHrkkKu2ih1uNdV8pT`. He also
   set the scope: surface things **useful to him personally**, not only becky-tool
   matches → the "useful to you" lane (§2a) + the personal interests catalog.
2. **Captions cost — ANSWERED:** default is one fast `--flat-playlist -J` call
   (titles); `--deep` adds per-video descriptions/tags (one request each). Full
   caption (VTT) download is a further opt-in left for later — descriptions+tags
   already corroborate most findings. (`scout-watch.ps1` uses `--deep`.)
3. **Model judgment — ANSWERED (2026-06-23):** built as the `--propose` gate
   (§4a): Qwen proposes, Gemma‑4 must agree, approved proposals become
   becky-new-tool intakes. (This replaced the simpler "3rd signal" assessor idea.)
4. **Auto-build vs emit — OPEN (default = emit):** `--propose` writes intakes;
   `--propose --build` actually runs the factory (spends compute / Claude budget).
   Should the weekly `scout-watch.ps1` run `--build` (fully hands-off: new useful
   video → built tool while Jordan sleeps), or emit-only so he can glance at the
   approved intakes first? Currently emit-only; flip `-Build` on the watch to go
   fully autonomous.
5. **Judge model:** Gemma‑4 is the independent judge today. Add Claude Code as a
   second/third judge (becky-new-tool's S4 already uses Claude downstream), or is
   Qwen+Gemma agreement enough to gate?
6. **Catalog tuning:** the built-in keyword catalog is conservative. Any becky
   area you want weighted more aggressively (e.g. local-LLM inference tricks)?
```
