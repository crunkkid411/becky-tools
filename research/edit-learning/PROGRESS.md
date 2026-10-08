# Learning Jordan's edit decisions: night run progress (started 2026-10-07 ~21:00 PDT)

Pick-up file. Newest entries at the bottom. Jordan's full requests (verbatim) are saved in the
session scratchpad as `jordan-2026-10-07-night.md`; the binding ones are copied here.

## What Jordan asked for (short)

- Spend the $5/month on whichever System One decision model fits each task (Perplexity Decider
  by default). DONE.
- Update llama-server (Whoretana is unused, so no conflict). DONE.
- Build a database of HIS edit decisions, extracted systematically from years of edited video.
  "Above all else, make sure the original files are not altered in any way - the Vegas project
  files can be READ for information, but they also should not be altered."
- A decision model is ONE data point; becky's point is several signals corroborating each other,
  with confidence scores.
- Livestreams: download live chat with yt-dlp (never touch his yt-dlp .conf), work out the chat
  delay, and use eyes/posture (leaning in, eyes on screen) as "reading chat" evidence.
- Deep-dive Reka's clipping platform; zooms and censoring must work in VEGAS; what else can't we
  do in VEGAS?
- Mine Albert Olgaard's skills, the Jev video repos (hermes-research-agent, HyperEdit, ...),
  theAIsearch "AI is reverse engineering everything" video. What are they doing that we
  overlook, and why? Use a decision model to triage.
- Consider EmbeddingGemma 2 and Google's 4B Gemma DiarizationLM. Remember research better
  (sqlite database / modern RAG).
- Free models for grunt work: Space Bunny Free (OpenCode Zen), ling-3.1-flash (OpenRouter free);
  Haiku 5.5 when free limits bite.
- Document progress and push to GitHub after milestones.

## Answer keys found on this PC (read-only)

| Project | What it is | Raw footage | Render |
|---|---|---|---|
| `X:\Videos\2025\8_August\4-human-brain-robot\human-brain-robot.veg` | Talking-head cut, 720x1280: 71 kept pieces from SNOW-2/5/6_converted.mp4 | `hj-footage\` (present) | not identified (`explaining-ai.mp4` predates the .veg) |
| `X:\Videos\2025\8_August\cool-little-tripod-product-video\cool-little-tripod.veg` | Product video | IMG_21xx.MOV, SNOW-11/12/13 (present) | `Rendered\cool-little-tripod-WITH-CAPTIONS.MP4` (65 s) |
| 91 `.veg` files under `X:\Videos` | years of edits | varies | varies |

## Log

- **21:05 Decision client:** `internal/systemone` `Hosted` now accepts any OpenRouter model whose output is
  "decisions" (refuses chat models), default `perplexity/pplx-decider-v1.1-27b`; $5/month cap
  unchanged. Live: Perplexity and Jev both pick the right take. Pushed 532c788.
- **21:06 llama.cpp updated:** b9551 -> b11487 (CUDA 12.4) in `C:\llama.cpp\build\bin`; old build kept in
  `C:\llama.cpp\build\bin-b9551-backup`. Same Gemma behaviour on text and image on both builds
  (Gemma "thinks" on image prompts in both). New build serves `/v1/systemone` for local decision models.
- **21:10 `vegas/BeckyDumpProject.cs`:** headless, read-only export of a whole VEGAS project to JSON
  (events, source offsets = the cuts, pan/crop keyframes = zooms, effects + parameter values,
  envelopes, Titles & Text, markers, regions). Always run on a COPY in `X:\AI-2\edit-learning-work\`
  (outside the repo). Originals' SHA-256 checked before and after: unchanged.
  - human-brain-robot: works (2 tracks, 71 video events; track FX: LUT, Beauty Box, AutoLooks,
    Color Corrector, Color Curves; audio: Noise Gate, EQ, Compressor).
  - cool-little-tripod: VEGAS crashes while opening it headless (even with the old verify script),
    so it is the project, not the exporter. Not solved yet.
- **21:15 Reka research done** (scratchpad `reka-report.md`): Reka Clip runs on their private Reka Flash, not
  the local Reka Edge; their repos call a paid API that returns rendered video only, so nothing
  lands on a timeline. VEGAS can do zooms (Pan/Crop keyframes), censoring (Pixelate/Blur FX +
  presets; Jordan's own 2021 `CENSOR.js`), bleeps/ducking (volume envelopes). Bridge reads pan/crop
  but writes none of these yet. Proposed: 3 small writer scripts (pan/crop, FX+preset on a range,
  volume/speed envelopes).
- **In progress:** transcribing SNOW-2/5/6 to label every raw sentence keep/cut against the .veg.
  Space Bunny is reading Albert's skills and hermes/HyperEdit (OpenCode blocks paths outside its
  folder, so it works on copies).
