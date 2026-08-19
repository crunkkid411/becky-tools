# JeremySNR/clip-forge

Source: https://github.com/JeremySNR/clip-forge | licence: MIT

Read for `shorts-user-feedback.md`: what step does this run that becky does not?
Written by a free model reading the source; the build/skip judgement is not its call.

---

## What it is
ClipForge is a desktop Electron application (macOS, Windows, Linux) that takes a long-form video file or URL (YouTube, Vimeo, TikTok, Twitch, or any yt-dlp source) and produces vertical short-form clips (9:16, 1:1, 16:9) with burned-in animated captions, auto zoom, speaker-aware reframing, and optional B-roll inserts. It emits rendered MP4 files (H.264/AAC, loudness-normalised to -14 LUFS) plus AI-generated social captions for TikTok/Reels/Shorts.

## The pipeline, in order
1. **Import / source acquisition** — `src/main/pipeline/ytdlp.ts` (URL download via yt-dlp with browser cookie support) or local file selection; `src/main/ipc.ts` handles `project:create` / `project:createFromUrl`.
2. **Probe & audio extraction** — `src/main/pipeline/ffmpeg.ts` (`probeVideo`, chunked audio extraction for transcription).
3. **Transcription** — `src/main/pipeline/transcribe.ts` (chunked Whisper transcription with word-level timestamps, checkpointed to avoid re-paying).
4. **Viral moment detection (LLM pass 1)** — `src/main/pipeline/highlights.ts` (`findHighlights`): LLM picks self-contained hook/build/payoff micro-stories from transcript.
5. **Ending review (LLM pass 2)** — `src/main/pipeline/highlights.ts` (`reviewEndings`): second LLM pass extends each clip to the beat that completes the thought.
6. **Virality scoring** — `src/main/pipeline/highlights.ts` (`scoreClips`): two-pass score (0–99) — text rubric (Berger & Milkman 2012) + measured vocal energy (`src/main/pipeline/energy.ts`) + vision pass (Kayal et al. 2025) on sampled frames.
7. **Active speaker detection (on-device)** — `src/main/pipeline/asd.ts` (LR-ASD ONNX model) + `src/main/pipeline/facetracks.ts` (UltraFace detection + IOU tracking + interpolation) + `src/main/pipeline/detect.ts` (face detection + scene cuts).
8. **Focus track building** — `src/main/pipeline/faces.ts` (`buildFocusTrack`): merges speaker diarisation, face tracks, and scene cuts into per-clip `FocusKeyframe[]` with `cut` flags.
9. **B-roll keyword tagging (LLM)** — `src/main/pipeline/broll.ts` (`tagBrollKeywords`): LLM tags words/phrases for image search.
10. **Image search** — `src/main/pipeline/imagesearch.ts` (Wikipedia + Openverse, no API keys).
11. **Caption generation** — `src/main/pipeline/captions.ts` (`buildAss`): ASS karaoke subtitle generation with 12 built-in styles + custom fonts (`src/main/fonts.ts`).
12. **Render / export** — `src/main/pipeline/render.ts` (`renderClip`): cuts, reframes (auto or manual), auto zoom (scene-aware punch-ins, jump zooms, slow creep), tighten cuts (removes pauses/filler), watermark/logo, burns captions, NVENC GPU encoding with CPU fallback (`src/main/pipeline/encoders.ts`).
13. **Social caption generation (optional)** — `src/main/pipeline/socialCaption.ts` (`generateSocialCaption`): LLM writes TikTok/Reels/Shorts caption.
14. **WorkVivo posting (optional)** — `src/main/pipeline/workvivoCaption.ts` + `src/main/pipeline/workvivo.ts`: renders clip, compresses to fit WorkVivo upload cap, posts via Customer API.

## Models, libraries and services

| Component | Stage | Local / Paid API |
|-----------|-------|------------------|
| Whisper (`whisper-1` default) | Transcription | Paid API (OpenAI) |
| GPT-5.4-mini (default) / user-selectable | Highlight detection, ending review, virality scoring (text), B-roll tagging, social caption, WorkVivo caption | Paid API (OpenAI) |
| GPT-4o / GPT-4o-mini (vision) | Virality scoring (vision pass on sampled frames) | Paid API (OpenAI) |
| UltraFace (ONNX) | Face detection | Local (bundled) |
| LR-ASD (ONNX, exported from MIT-licensed LR-ASD repo) | Audio-visual active speaker detection | Local (bundled) |
| MFCC (`src/main/pipeline/mfcc.ts`) | Audio features for ASD | Local (pure TS) |
| yt-dlp (binary) | URL download | Local (bundled/managed) |
| FFmpeg (bundled + optional NVENC GPU build) | Probe, audio extraction, render, encode | Local |
| libass (via FFmpeg) | Caption burn-in | Local |
| Wikipedia / Openverse | B-roll image search | Free public APIs (no key) |
| electron-updater / GitHub Releases | Auto-update | Free service |
| safeStorage (Electron) | API key / token encryption | Local OS keyring |

## Prompts
**Highlights detection (first LLM pass)** — from `src/main/pipeline/highlights.ts`:
```
You are an expert short-form video editor. Your job is to find the most viral-worthy, self-contained moments in a long-form transcript.

A "moment" is a continuous span of speech that tells a complete micro-story: it has a hook, a build, and a payoff. It does NOT trail off mid-setup, cut off a punchline, or leave the viewer hanging.

Return a JSON array of objects, each with:
- "startWord": index of the first word in the transcript.words array (inclusive)
- "endWord": index of the last word (inclusive)
- "title": a punchy, click-worthy title for the clip (max 60 chars)
- "reason": one sentence explaining why this moment works as a standalone clip

Rules:
- Each moment must be 30–90 seconds of speech (use word timestamps to estimate).
- Moments must not overlap.
- Prefer moments with high emotional arousal, surprise, practical value, or narrative tension.
- If the transcript is a conversation, prefer exchanges between speakers over monologues.
- Do not return more than 15 moments.
- If the transcript is too short or has no viral moments, return [].

Transcript (word array with .text, .start, .end, .speaker):
{{TRANSCRIPT_JSON}}
```

**Ending review (second LLM pass)** — from `src/main/pipeline/highlights.ts`:
```
You are reviewing the ENDING of a short-form clip. The clip currently ends at the word indexed {{END_WORD}} (text: "{{END_WORD_TEXT}}", timestamp {{END_TIME}}s).

Here are the next 50 words of the transcript:
{{NEXT_WORDS_JSON}}

Does the current ending feel complete — does it land the payoff, finish the joke, close the thought? Or does it cut off mid-sentence, mid-setup, or right before a punchline?

If the ending is already complete, return {"extend": false}.
If extending by a few seconds would complete the thought, return {"extend": true, "newEndWord": <index>, "reason": "..."}.
Only extend if the additional words genuinely complete the thought. Do not extend just to add filler.
```

**Virality scoring (text rubric)** — from `src/main/pipeline/highlights.ts`:
```
Score this clip 0–99 on viral potential using the Berger & Milkman (2012) framework:
- High arousal (awe, excitement, anger, anxiety, humor): +0 to +30
- Practical value (actionable, saves money/time, teaches): +0 to +20
- Social currency (makes sharer look smart, insider, funny): +0 to +20
- Narrative tension / curiosity gap (hook → build → payoff): +0 to +15
- Emotional resonance (relatable, identity-affirming): +0 to +15

Penalties:
- Trails off / incomplete: -20
- Generic / no clear point: -15
- Audio quality issues (from transcript hints): -10
- Single speaker monologue with no variation: -10

Return JSON: {"score": <int>, "breakdown": {"arousal": <int>, "practical": <int>, "social": <int>, "narrative": <int>, "emotional": <int>, "penalties": <int>}, "reason": "one sentence"}
```

**Virality scoring (vision pass)** — from `src/main/pipeline/highlights.ts`:
```
You are scoring the VISUAL scroll-stopping potential of a short-form clip. You will see 8 evenly-spaced frames from the clip.

Rate 0–99 on how likely a viewer is to STOP SCROLLING based purely on visuals. Consider:
- Faces with strong expressions (surprise, laughter, intensity)
- Visible action, movement, gestures
- Text on screen, overlays, captions already present
- Scene changes, cuts, visual variety
- Production quality (lighting, framing)
- Unusual or striking imagery

Penalties: static talking head, blank screen, low light, watermarks from other platforms.

Return JSON: {"score": <int>, "reason": "one sentence"}
```

**B-roll keyword tagging** — from `src/main/pipeline/broll.ts`:
```
You are tagging a transcript for B-roll image inserts. For each word or short phrase that would benefit from a visual illustration (proper nouns, concrete objects, places, people, concepts), output a tag.

Return JSON array of objects: {"wordIndex": <index in transcript.words>, "query": "search query for image", "durationSec": <how long to show the image, 2-5>}.

Only tag words where an image would genuinely add value — not filler words, not abstract concepts with no clear visual. Max 15 tags per clip.

Transcript words (with .text, .start, .end, .speaker):
{{TRANSCRIPT_JSON}}
```

**Social caption generation** — from `src/main/pipeline/socialCaption.ts`:
```
Write a scroll-stopping caption for TikTok / Reels / Shorts for this clip.

Clip title: {{TITLE}}
Clip transcript (first 500 chars): {{TRANSCRIPT_EXCERPT}}
Virality score: {{SCORE}}/99
Clip duration: {{DURATION}}s

Format:
1. Hook-first line (max 125 chars, no hashtags) — the line that stops the scroll.
2. One engagement driver (question, poll, "comment X if…", "save for later").
3. 3–5 niche hashtags (no generic #fyp #viral).

Tone: native to the platform, not corporate. No emojis in the hook line. One emoji max in the engagement line.
Return JSON: {"caption": "full caption text"}
```

**WorkVivo caption generation** — from `src/main/pipeline/workvivoCaption.ts`:
```
Write an internal-comms style post for WorkVivo (enterprise social network) for this clip.

Brand voice:
- Brand name: {{BRAND_NAME}}
- Tone: {{TONE}}
- Style: {{STYLE}}
- Avoid: {{AVOID}}

Clip title: {{TITLE}}
Clip transcript (first 800 chars): {{TRANSCRIPT_EXCERPT}}
Clip duration: {{DURATION}}s

Format: Professional but human. 2–3 short paragraphs. Include 2–3 relevant internal hashtags (e.g. #Engineering #ProductLaunch). No emojis unless the brand voice explicitly allows them.
Return JSON: {"caption": "full caption text"}
```

## How it decides WHAT to clip
File: `src/main/pipeline/highlights.ts` (`findHighlights` → `reviewEndings` → `scoreClips`).

1. **LLM proposes candidates** — The first prompt asks the model to return up to 15 non-overlapping spans (30–90 s each) that form complete micro-stories (hook/build/payoff). Selection is entirely LLM-driven; no heuristic pre-filter.
2. **Ending review** — Each candidate's end word is sent to a second LLM call with the next 50 words; the model decides whether to extend to complete the thought (`extend: true` + new `endWord`).
3. **Two-pass scoring** — Each reviewed clip gets:
   - **Text score** (0–99): rubric based on Berger & Milkman (2012) — arousal, practical value, social currency, narrative tension, emotional resonance, with penalties for trailing off, generic content, audio issues, monologue.
   - **Vocal energy** (`src/main/pipeline/energy.ts`): per-segment RMS energy normalised to 0–1, averaged over the clip, weighted into final score.
   - **Vision score** (0–99): 8 evenly-spaced frames sent to GPT-4o-mini vision prompt (scroll-stopping potential: faces/expressions, action, text, scene changes, production quality).
   - **Final score** = weighted combination (weights not visible in files read; code shows `scoreClips` combines them but exact formula not in provided snippets).
4. **Ranking** — Clips sorted by final score descending; top N returned to UI.

Thresholds / constants visible: 30–90 s duration, max 15 candidates, 50-word lookahead for ending review, 8 frames for vision pass.

## How it decides framing / cropping
File: `src/main/pipeline/faces.ts` (`buildFocusTrack`) + `src/shared/focusTrack.ts` + `src/main/pipeline/render.ts` (`faceCentreCropLeft`).

1. **Face detection** — UltraFace ONNX runs on sampled frames (`src/main/pipeline/detect.ts`).
2. **Face tracking** — IOU-based association across frames with linear interpolation for gaps (`src/main/pipeline/facetracks.ts`).
3. **Active speaker detection** — LR-ASD ONNX model takes face crops + MFCC audio windows (`src/main/pipeline/asd.ts` + `src/main/pipeline/mfcc.ts`) to classify each tracked face as speaking/not per frame.
4. **Scene cut detection** — `src/main/pipeline/detect.ts` detects hard cuts.
5. **Focus track construction** — `buildFocusTrack` merges:
   - Speaker diarisation from transcript (Whisper segments have `.speaker`).
   - Face tracks with speaking probability.
   - Scene cuts.
   Produces `FocusKeyframe[]`: `{ t: number, x: number, cut: boolean }` where `x` is normalised face centre (0–1).
6. **Crop logic** — At render time (`render.ts`), for each output frame:
   - `focusAt(track, t)` samples the track (eased pan for within-shot moves, hard snap on `cut=true` or shift > 0.3).
   - `faceCentreCropLeft(faceX, sourceW, sourceH, aspectW, aspectH)` computes left edge of 9:16 (or 1:1/16:9) crop window centred on that face.
   - If no face/speaker, falls back to centre crop.
7. **Manual override** — User can set `clip.edit.framing = 'manual'` and adjust `clip.edit.focusX` (crop slider 0–1) in the editor; this bypasses the auto track.

## Multi-pass or iteration
- **Transcription** is checkpointed per chunk; re-running skips completed chunks (not a refinement pass, just resumption).
- **Highlight detection** runs two distinct LLM passes (candidate generation → ending review) — this is a designed two-pass, not an iterative refinement loop.
- **Virality scoring** runs two independent passes (text + vision) then combines — no iteration.
- **Ending review** does not feed back into candidate generation.
- **Render** is single-pass; no re-encode based on quality check.
- **No stage re-checks its own output or loops to improve.** The pipeline is strictly feed-forward.

## Steps here that a transcript-first clipper would MISS
- **Speaker-aware reframing via audio-visual ASD** — on-device LR-ASD + UltraFace tracks *which face is speaking* (lip sync), not just who is on screen; crop switches like a camera cut between speakers.
- **Scene-cut-aware focus snaps** — `FocusKeyframe.cut` flag forces hard crop switch at detected shot boundaries; within-shot drift uses eased pan (0.6 s smoothstep) to avoid "camera shake" from micro-movements.
- **Auto zoom planner** — `src/shared/zoomPlanner.ts` (referenced in `render.ts`) generates three zoom layers: scene-aware punch-ins on high-energy lines, jump zooms covering cuts, slow creep on static stretches; all remapped when tighten-cuts shortens timeline.
- **Tighten cuts with full remap** — pauses/filler words removed via transcript word timestamps; captions, B-roll, zoom keyframes, and face track all time-warped to the shortened timeline (`src/shared/tighten.ts`).
- **B-roll from LLM-tagged keywords + keyless image search** — LLM tags concrete nouns/phrases → Wikipedia/Openverse image fetch → inserted at word timestamp with Ken Burns pan.
- **Loudness normalisation to -14 LUFS** — FFmpeg `loudnorm` filter applied at render, with gentle audio tail fade.
- **NVENC GPU encoding with automatic CPU fallback** — `src/main/pipeline/encoders.ts` probes NVENC, downloads GPU-enabled FFmpeg if missing, falls back to libx264.
- **Live preview = export preview** — shared planning code in `src/shared/` (caption layout, tighten, zoom, focus track) ensures what the user sees in the React editor is exactly what `renderClip` burns.
- **Encrypted API key storage** — Electron `safeStorage` (OS keyring) with `plain:` fallback for headless Linux.
- **Project-level write lock** — `src/main/projects.ts` `withProjectLock` serialises all `project.json` mutations (clip edits, caption generation, rename) to prevent lost updates.

## Worth stealing
1. **`src/shared/focusTrack.ts`** — pure, testable focus-track sampling (eased pan vs hard snap on cuts, shift threshold, pan duration capping). Used identically by live preview (CSS `object-position`) and export (FFmpeg crop expression). MIT licence.
2. **`src/main/pipeline/highlights.ts` ending-review prompt** — second LLM pass that only extends when the thought is incomplete; avoids the common "clip cuts off mid-punchline" problem. Prompt is concise and effective.
3. **`src/main/pipeline/asd.ts` + `facetracks.ts` + `detect.ts`** — complete on-device audio-visual active speaker pipeline (UltraFace + LR-ASD ONNX + MFCC) with scene-cut detection. No cloud dependency, MIT-compatible (LR-ASD weights exported from MIT repo).
4. **`src/main/pipeline/encoders.ts` NVENC detection & GPU FFmpeg download** — probes hardware, fetches prebuilt GPU-enabled FFmpeg binary per platform, verifies it works, falls back cleanly.
5. **`src/main/projects.ts` `withProjectLock`** — simple per-project mutex using a `Map<string, Promise>` that queues read-modify-write cycles; prevents lost updates from concurrent IPC handlers without a heavy DB.
6. **`src/main/fonts.ts` `parseFontFamily`** — sfnt `name` table parser (TTF/OTF) that extracts typographic family (nameID 16) with Windows/Unicode priority over Mac; enables correct `@font-face` matching for libass without trusting filenames.
7. **`src/main/exportPath.ts` `uniqueOutputPath`** — trivial but correct: appends ` (2)`, ` (3)`… to avoid silently overwriting exports with same title.
8. **`src/main/updates.ts` `detachedRelaunchEnv`** — strips `ELECTRON_RENDERER_URL` before relaunching a source checkout so the new process loads the built `index.html` instead of the dead dev server.
9. **`src/main/pipeline/transcribe.ts` chunked checkpointing** — splits long audio into chunks, writes partial transcript JSON per chunk, resumes on retry; avoids re-transcribing (and re-paying) on failure.
10. **`src/shared/tighten.ts` + `zoomPlanner.ts` (referenced)** — time-warp remapping of *all* timeline layers (captions, B-roll, zoom, focus track) when filler removal shortens the clip. Not fully visible in provided files but referenced in `render.ts` and `shared/` index.

Licence: MIT (repo root `LICENSE`, confirmed in README).
