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
- **skipped** (`score == 0`) — off-topic; **counted, not enumerated** (no flood).

`improve` = matches a tracked dependency (upgrade an existing tool); `extend` =
becky-domain but nothing tracked yet (a new tool/model to build).

## 3. What it does (pipeline)

```
becky-scout <playlist-url-or-id> [--json] [--catalog]

1. RESOLVE the playlist → [{id,url,title,channel,description,tags,transcript,position}]
   (PlaylistSource: the one online step, via yt-dlp — wired by the local agent).
2. For each video, BUILD a lower-cased haystack from every offline-readable field.
3. SIGNAL 1 — cross-reference the becky-freshness manifest (tracked model named?).
4. SIGNAL 2 — match becky's capability catalog (in a becky domain?).
5. SIGNAL 3 — (optional) ask the local-model Assessor for an independent verdict.
6. CORROBORATE → relevant / candidate / skipped; classify improve vs extend.
7. RENDER a deterministic, stably-sorted report (plain-language or --json).
```

`--catalog` prints becky's capability map (what scout treats as "a becky area")
so Jordan can see — and the local agent can tune — exactly what it looks for.

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

## 5. Build split (cloud ↔ local)

| Cloud / web agent (DONE)                                   | Local agent (Jordan's PC — to wire)                 |
|------------------------------------------------------------|-----------------------------------------------------|
| This spec; the capability catalog; corroboration rules     | Real `PlaylistSource` via **yt-dlp** (see recipe)   |
| Go orchestrator (`cmd/scout/`), JSON + plain-text contract | Run against a real playlist; tune catalog keywords  |
| `internal/scout` core + the two boundary interfaces        | Optional `Assessor` via a local llama.cpp text model|
| Deterministic fakes + unit tests (corroboration/sort/degrade) | (Qwen3/Gemma, `--temp 0`) for the 3rd signal     |
| Freshness-manifest cross-reference (reuses `internal/freshness`) | Confirm captions parse cleanly to plain transcript |

**`PlaylistSource` contract** (`internal/scout/scout.go`): `Playlist(ref) → {id,
title, url, videos:[{id,url,title,channel,description,tags,transcript,position}]}`.
yt-dlp recipe: `yt-dlp --flat-playlist -J <ref>` for entries, then per video
`yt-dlp -J --write-auto-subs --sub-format vtt --skip-download <url>` for
description/tags/channel + auto-captions (VTT → plain text).

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
"watch later / becky" playlist; relevant findings drop straight into the
new-tool / upgrade pipeline (`SPEC-BECKY-NEW-TOOL.md`).

## 7. Open decisions for Jordan
1. **The playlist:** which playlist URL is the "becky, look at this" queue? (Or
   should scout accept several and dedupe across them?)
2. **Captions cost:** fetching auto-captions per video is the slow part. Default
   to titles+descriptions only and fetch captions on `--deep`, or always fetch?
3. **Model assessor:** wire the optional 3rd signal now (Qwen3-4B already used by
   the canvas), or ship the deterministic floor first and add the model later?
4. **Catalog tuning:** the built-in keyword catalog is conservative. Any becky
   area you want weighted more aggressively (e.g. local-LLM inference tricks)?
```
