# Footage library — the editor features of VideoDB / Director / buttercut / kevs, mapped to becky (2026-10-10)

Jordan, 2026-10-10: *"once a video has been analyzed, there is no reason we should have to re-analyze it
— it needs a persistent file ... and we ALSO need a db we can query ... All the projects I mentioned
(videoDB, director, kevs-obsidian, buttercut) are useful because the FEATURES themselves are features
that other video editors actually use ... assume those projects outline the needs of a human video
editor."*

Sources looked at: `research/director-videodb-mining.md` (2026-06-23 study of Director + videodb-cookbook),
`C:\Users\only1\Documents\AI_Local\buttercut` (library.yaml + per-video "visual transcript" JSON),
`X:\AI-2\kevs-obsidian-ingestion-engine` (a "Forensic Video Intelligence" shell: smoke test only, the
frames/identity/search modules were never written), `C:\Users\only1\Documents\AI_Local\maw` (Mnemoria
web-crawl text store — text only, not video). No Qwen-CLI VideoDB rebuild exists on this PC (Qwen CLI's
chat history only shows maw/Mnemoria).

## Analyze once, keep it

`becky-pipeline <video-or-folder>` is the one call. Each step writes its own readable JSON into
`<out>/<video>/` and is skipped next time (`transcript.json`, `diarized.json`, `events.json`,
`ocr.json`, `mediasearch.json`, `shots.json`, ...). The searchable parts go into a database:
transcripts/OCR into the pipeline's `forensic.db`; pictures, sound and the shot log into the video
database (config `video_db`, `X:\AI-2\becky-tools\video-db\becky-video.db`) — or both into one file
with `--db`.

## Feature map

| Editor feature (where it comes from) | becky | Status |
|---|---|---|
| Transcript with word times (all four) | becky-transcribe | have |
| Who speaks when (Director, kevs) | becky-transcribe --diarize + becky-diarfix | have |
| Written shot log / "visual transcript" (buttercut, Director scene index) | **becky-shots** | NEW 2026-10-10 |
| Find a moment by what is SEEN or HEARD (Director multimodal search, VideoDB scene index) | **becky-mediasearch** | NEW 2026-10-10 |
| Find footage that looks/sounds like an example | becky-mediasearch --like | NEW 2026-10-10 |
| Find by spoken words | becky-search, qmd (transcripts) | have |
| Known people by face/voice (kevs, VideoDB face index) | becky-identify | have |
| On-screen text (Director, VideoDB) | becky-ocr | have |
| Scene / location changes, motion bursts | becky-events, becky-motion | have |
| Search hits compiled into one preview video (Director `compile()`) | becky-mediasearch --reel -> becky-reel | NEW 2026-10-10 |
| Hits / selects as a timeline for an editor (buttercut XML, VideoDB timeline) | becky-mediasearch --reel -> becky-otio | NEW 2026-10-10 |
| Rough cut from a folder of takes (buttercut roughcut) | becky-roughcut (into Vegas) | have |
| Captions (Director subtitle agent) | becky-captions, becky-subtitle | have |
| Exact frame at a moment (Director `frame`) | mediasearch hits carry the exact best frame time | partial: no still is saved |
| Library summary + "the man in the blue shirt is Andrew" notes (buttercut library.yaml) | `people.txt` beside the videos (becky-shots `--people`) + becky-identify names in shots and said lines | PARTLY (no library summary yet) |
| One question across said + seen + heard at once | becky-search + becky-mediasearch are separate calls | GAP |

## Not taken (and why)

- VideoDB / Director's hosted cloud API, LLM-written Python executed on the PC, and the ~15 cloud
  GenAI agents (dubbing, voice clone, image/video generation): paid or online, and not deterministic.
  Same reasoning as `research/director-videodb-mining.md` "does NOT translate".
- buttercut's "Claude reads frames and hand-edits the JSON" loop: becky compiles the same job into
  becky-shots (Gemma 4 locally, fixed prompt, fixed seed).
