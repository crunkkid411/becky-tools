# 04 - Albertolgaard skills: functionality catalogue (Jordan roadmap)

Source: `C:\Users\only1\Documents\AI_Local\Claude-Extras\Claude-Skills-albertolgaard` (local copy, read-only).
Status: COMPLETE. 38 functionality blocks, one per skill folder (21 in sections A-C, 17 in section D). Section E lists what was not read in full.

Summary of the set so far: 38 skill folders. Most are video/reel/thumbnail/DM/website/lead-gen skills built around Claude/Higgsfield/ElevenLabs/Fish Audio. Note: `youtube-popup-graphic/youtube-clean/` is a byte-identical copy of `youtube-clean/` (SKILL.md, SETUP.md, cutting.md, layout.md, section-agent.md).

Measurability key used below:
- **[DET]** can be measured from a finished video or VEGAS project without AI (timestamps, luma, audio levels, cut counts, gaps).
- **[DET+ASR]** needs a transcript (word times) but otherwise deterministic.
- **[AI]** needs a judgement (is this a retake, is this the hook, is the sentence "telling" vs "showing").
- **[VEGAS]** can be read straight from a VEGAS project (track/event times, text events, music track).

---

## A. Editing / reel / video skills (exhaustive rules)

### famous-youtube-editor (source: famous-youtube-editor/SKILL.md) - 16:9 long-form edit from one talking-head clip
- PURPOSE: turn a raw talking-head clip into a finished 1920x1080 YouTube video that "keeps moving" by changing the shot with what is being said.
- FUNCTIONALITY: (1) probe source; (2) transcribe raw (ElevenLabs Scribe via famous-reel-editor scripts); (3) hand-pick clean take per sentence -> keep-EDL; (4) cut (`cutjoin.py`, patched to 1920x1080 / 30 fps); (5) silence trim (`silence_keep.py`); (6) re-transcribe final cut and check for clipped words; (7) write a **shot plan** `shotplan.json` = list of `{start, end, mode, what}` assigning every sentence one of four modes A/B/C/D; (8) fire Higgsfield B-roll jobs; (9) author/render full-screen "Premiere-style" window cards with hyperframes; (10) ffmpeg compose driven by shotplan (enable='between(t,A,B)' overlays); (11) optional punch-ins (100% vs 115% crop); (12) contact-sheet self-check; (13) retro/self-learning.
- USE CASE FOR JORDAN: a long YouTube video where the cut itself is the shot grammar: face for opinions/jokes, full-screen graphic for explanations, PiP for "look at this", B-roll for concrete actions. Maps directly onto a VEGAS timeline (mode = track/event type).
- DECISION MODEL: LLM free-text (could be System One). The mode decision per sentence is a 4-way pick. System One question: "For this sentence of the transcript, which shot mode fits: A face (telling / opinion / joke / CTA), B full graphic (explaining a concept, list, number, comparison), C cam PiP over screen/graphic (pointing at something), D full B-roll (concrete filmable action)? Give probabilities." Also: "Is this sentence a retake / false start of the next sentence?" (take selection).
- DETERMINISTIC PARTS: cut assembly, silence trimming, re-transcribe + clipped-word check (audio vs transcript end times), A/V drift check (ffprobe duration), punch-in crop math, the ffmpeg compose graph, the >5s-gap check on the shot plan (pure arithmetic on shotplan), mode-duration checks (no mode >10s, no two B-roll back-to-back), "mode changes land on word boundaries" check.
- DATA IT NEEDS: raw talking-head clip (4K), word-level transcript of raw and of final cut, shotplan, B-roll clips, card renders.
- RULES WORTH COPYING (quoted):
  - "**Rhythm rules**: hook = A (first shot is the face, always)." [DET+ASR for hook position = first 2s is cam; AI for "is it a hook"]
  - "**Something NEW must happen on screen every ~5s** - a mode change, a punch-in, a new card, or a staged element popping inside a card all count; scan the shot plan for any gap >5s without an event." [DET: gap list from shotplan / from scene-cut detection + card-element events]
  - "Never stay in one mode >10s." [DET: mode durations]
  - "Never two B-roll windows back-to-back." [DET]
  - "a good long-form rhythm is A -> B -> D -> A -> C -> A ..., returning to full-screen cam regularly so the video stays personal. End (CTA) = A." [DET for ordering, AI for choice; the CTA = last sentence is A: DET+ASR via last sentence]
  - "Mode changes land ON word boundaries (use the transcript times)." [DET+ASR]
  - "Shot changes between words only - use the transcript times, never round numbers." [DET+ASR]
  - "~1 B-roll per 15-20s of runtime (a 75s video wants ~4)." [DET: count]; "3-6s each; never the hook or CTA." [DET+ASR]
  - "Reserve the PiP corner (bottom-right 520x340) as a dead zone in every mode-C card layout." / PiP ~480px wide, bottom-right, `W-w-48:H-h-48` [DET: region check on frame]
  - "If a mode-A stretch exceeds ~8s, alternate punch level at sentence boundaries (100% vs ~115% crop)." [DET: duration of cam-only runs; crop 1/1.15]
  - Card sizing: "Stage elements INSIDE each card on their trigger words ... so a 5s card has 2-3 internal events." [DET for events if VEGAS keyframes are visible; AI for choosing words]
  - Mode B cards: "Design cards as UI PANELS, not bare text" (window chrome) - [AI/visual; a frame classifier could only check "not blank"]
  - "Wide layouts ... don't render a vertical card centered on a 16:9 frame." [DET: bounding box of card vs 1920 width]
  - "NO captions burned on the YouTube video" and "NO background music by default - voice only" (user preferences). Voice loudness target `loudnorm=I=-16`; `-crf 18 -preset medium`; `+faststart`. [DET]
  - B-roll prompt recipe: "match the VIDEO's grade - do NOT force dark/moody like the IG skill." [AI/visual]
  - "Check broll/ first ... regenerate only what the new colour theme invalidates" [process]
  - Self-eval checks: first/last word not clipped; A/V drift < 1 frame; "contact-sheet one frame per shot-plan window" [DET for drift; AI for visual match]
- BECKY MATCH: partial (verified read-only in becky-tools INDEX/README/SKILL). `becky-transcribe --diarize` (word timings), `becky-cut` (cut points), `becky-subtitle` (cut-snapped captions) and the VEGAS extension (`becky-vegas`, BeckyCut/BeckyCaptions) cover the cut and caption steps. The shot-plan / A-B-C-D mode logic is not covered by any becky tool.

### famous-reel-editor (source: famous-reel-editor/SKILL.md + scripts/) - 9:16 reel from one talking-head clip, "Ambra" green-glass style
- PURPOSE: finished vertical reel with animated motion-graphics cards in the top band, karaoke-style one-word captions, music, and 1-2 AI B-roll cutaways.
- FUNCTIONALITY (15 steps): (0) study reference reel (contact sheet with ffmpeg `tile`, sample accent colour); (1) pre-crop landscape raw to 1080x1920 (`crop=1215:2160:<x>:0`) - BUT check the frame first (rotation metadata); (2) transcribe raw with `transcribe.py` (ElevenLabs Scribe, word timings); (3) read word-by-word, hand-pick clean take per sentence, drop director's notes ("put a clip of...", "ok so..."); (4) write keep-EDL `{"sources":{...},"ranges":[{start,end}]}`, extend each end +0.2/0.4s; (5) `cutjoin.py` cut; (6) `silence_keep.py` (defaults -40dB, MIN 0.35s, PAD 0.12s, last word protected); (7) re-transcribe the cut and verify no clipped word; (8) author beats in `gen.py` (card triggers = word/phrase, cards contiguous); (9) copy logos; (10) render cards with `hyperframes render`; (11) patch transcriber errors in caption JSON, `captions.py` (PIL burned captions, Montserrat Black 92-100px, soft shadow); (12) B-roll 1-2 via Higgsfield seedance_2_0 (identity B-roll = uploaded photo as role "image"); (13) `compose.sh` (voice loudnorm -16, music at 6%, +faststart), crop_y tuned by contact sheet; (14) self-evaluate frames; (15) retro + write lessons back into this skill.
- USE CASE FOR JORDAN: a vertical Short/Reel from one clip, with schema graphics that visualise the spoken claims. Jordan's YouTube Shorts / IG workflow.
- DECISION MODEL: LLM free-text (could be System One) for: (a) which sentence is the clean take (take selection); (b) which words get a card and which card type (schema choice); (c) which 1-2 sentences get B-roll. System One question for (a): "Of these N attempts at the same sentence, which is the complete, final take? (probabilities over attempt ids)". For (b): "Which schema from [list] best visualises this sentence? (probabilities)". For (c): "Is this sentence concrete and filmable (yes/no/maybe)?".
- DETERMINISTIC PARTS: all cutting, silence trimming (silencedetect at -40dB), clipped-word detection (word end > file duration; end-start==0; hyphen mega-words >14 chars with 2+ hyphens), duplicate-phrase scan on the cut transcript (consecutive repeated n-grams), caption generation, sync check (ffprobe durations), loudness (volumedetect), the SFX cue timing, EXPECT-guard for anchors (`find_after`).
- DATA IT NEEDS: raw clip, word-level transcript (Scribe), cut transcript, reference reels (for look only), logos, music.
- RULES WORTH COPYING:
  - Style: "9:16, talking-head in the bottom ~55% (overlay y=864), top band 0-864 black for the schemas." [DET: a region check]
  - "ONE accent color per video on black. Default green #2fe081. ... for Claude/GLM content use orange #f0813f. Red #ff5a5a for no/lost/expensive; steel-grey #7f93ad for neutral competitor." [DET: palette count per frame - count of saturated hues > 1 accent]
  - Subtitles: "Montserrat Black, white, UPPERCASE, ONE word, no black outline, ~100px at the edge (CY=700 + overlay y=78)." [DET+ASR: caption = spoken word at time; check at 3-4 sample points]
  - "Decorative layer ALWAYS on ... the top band is never empty/static." [DET: frame-to-frame pixel delta > threshold in top band]
  - "LESS TEXT on cards (user preference, Aug 2026): cards carry the VISUAL ... no explanatory sublines ... Hooks especially: one visual + at most one word." [DET: OCR word count per card frame - Qwen OCR or any OCR]
  - "Sparse cards (1 every 5s) -> cards contiguous, one always present." [DET: gap in card coverage > 0 -> flag]
  - "NEVER per-word pop-scale on the cards. Movement = snappy entrances + continuous decorative layer + self-drawing graphs." [DET: per-word scale events]
  - Caption timing: "Subtitles: one word per frame at a time; caption must match the transcript (sample 3-4 points)." [DET+ASR]
  - Card anchor: "Anchor the key visual EARLY in the beat (one of the first words or s+0.2), not the last." [DET+ASR]
  - "A beat may never show >1s of empty card." [DET: time from card in to first element]
  - Sound effects (default ON): ElevenLabs sound-generation kit: shatter 0.5s, whoosh 0.35, pop 0.25-0.3, boom 0.4, stamp 0.45, tick 0.22, ding 0.35; "Min duration 0.5s". Mix: `alimiter=limit=0.891:level=false`, verify max <= -1 dB. [DET: cue list vs element events]
  - Cut rules: "silence_keep defaults (-40dB, last word protected) + extended EDL ends + VERIFY the cut's AUDIO, NOT the transcript alone"; "NEVER -30/-32dB: it eats the soft word releases"; "-35 dB is the working middle in a noisy room". [DET]
  - Pauses: "gap = start - prev_end > 0.30s between two sentences = a pause to tighten" (MIN 0.35, PAD_AFTER 0.12; "very punchy": MIN 0.30). [DET+ASR]
  - "Sparse sub-second 'So'/'But' micro-fragments" - (from youtube-clean, see below) [DET: range < 0.8s]
  - "Spoken triplets repeated identically -> keep ONE" [AI+ASR]
  - "Stats: a bare number stat = sterile: make a self-drawing curve" [AI/visual]
  - B-roll: "1-2 AI cutaways per reel by default; skip if the reel is <20s with wall-to-wall cards." [DET: duration check]; "2-4s windows; never over hook (first ~2s) or CTA" [DET+ASR]; "Space the two B-rolls apart (~1/3 and ~2/3 in)" [DET]; "bad B-roll is worse than none." [AI]
  - Dark moody B-roll: "B-roll must blend into the black band and keep the white subtitle readable (bright footage = illegible captions)." [DET: mean luma of B-roll window vs caption luma]
  - "Cover the card underneath with a black band for the whole B-roll window, extended to the START of the next beat." [DET]
  - "Trim to best slice: best-motion 3-6s of a 10s generation" [DET: frame-difference motion score]
  - Hook before-state on frame 1: "the BEFORE state of any before/after hook is on screen from t=0 ... swap sequential, not a crossfade (old exits ~0.16s)". [DET: first-frame non-empty; AI for before/after choice]
  - Pace: "Key visual entering on the LAST word leaves the card half-empty for 2-3s" [DET+ASR]
  - Music: "music at 6% (0.06), voice loudnorm -16" [DET]
  - Output: "+faststart needed for QuickTime/IG" [DET]
- BECKY MATCH: partial (verified read-only). `becky-transcribe` (word timings) and `becky-subtitle` (cut-snapped captions, which fixes the flashing-caption problem) match the transcript and caption steps. No becky tool does the shot-grammar or the reel layout.

### famous-thumbnail (source: famous-thumbnail/SKILL.md) - 16:9 YouTube thumbnail from a reference thumbnail + user topic (GPT Image 2 via Higgsfield)
- PURPOSE: produce a converting 16:9 thumbnail that copies a reference's layout/emotion/text treatment with the user's own topic.
- FUNCTIONALITY: (1) study reference (layout grid, text treatment, emotion, colour, why it converts); (2) write concept: <=4-word UPPERCASE curiosity-gap headline; (3) upload reference to Higgsfield (`media_upload` -> curl -> `media_confirm`); (4) `generate_image` model `gpt_image_2`, `aspect_ratio 16:9`, `resolution 2k`, `quality high`, 2 variants; (5) self-evaluate at 200px width and spell-check every word; (6) deliver + one round of targeted edits (re-run with previous output as reference).
- USE CASE FOR JORDAN: generating his thumbnails on demand from a reference he likes plus the video's claim; the ONE-question intake ("What's the one claim/result/emotion the video delivers?") is a template for a thumbnail brief.
- DECISION MODEL: LLM free-text (could be System One). System One questions: (a) "Does this thumbnail headline make a claim/number/tension in 4 words or fewer? (yes/no)" (b) "Which of these 2 variants has the stronger curiosity gap at 200px? (probabilities)" (c) "Does the thumbnail text match the approved title word-for-word? (yes/no)".
- DETERMINISTIC PARTS: word count of headline (<=4), uppercase check, 200px downscale legibility (contrast ratio of text vs local background, min text height in px), face-area ratio (head ~1/3 of frame height), edge-safety check (no text/face in bottom-right ~5% margin or corners where timestamp sits), 3-colour max (palette quantisation count), 16:9 exact dimension check, 2k resolution check, spelling via dictionary on OCR of the image.
- DATA IT NEEDS: reference thumbnail(s), video title/claim, optional creator face photo, brand colours, Jordan's past thumbnails + CTR (for learning what converts).
- RULES WORTH COPYING:
  - "<=4 words, uppercase, one idea. The title carries the sentence; the thumbnail carries the tension." [DET: word count, case]
  - "One focal point (a face with a strong expression beats everything; faces should be large - head ~1/3 of frame height)." [DET: face box height / frame height via insightface or MediaPipe]
  - "3-color max: background + subject + ONE accent. High contrast between text and background always." [DET: palette count + WCAG-style contrast ratio]
  - "Edges matter: keep text/faces off the bottom-right corner (timestamp overlay) and inside ~5% safe margins." [DET: bbox vs margin]
  - "Curiosity gap: ... raise a question the title doesn't fully answer." [AI]
  - "Text 200px test: is the text readable and the emotion legible at feed size?" [DET: downscale + OCR at 200px width]
  - "Never quality:low / 1k output - text goes mushy. Always 2k + high." [DET: file dimension]
  - "GPT Image 2 renders long quoted strings tiny. <=4 words." [DET]
  - "Steal structure, never content or identity." [AI / policy]
  - "If the reference violates one of these but clearly converts anyway, trust the reference." [AI]
- BECKY MATCH: none found (verified: grep for "thumbnail" in becky-tools INDEX.md, README.md and SKILL.md returned no tool).

### youtube-thumbnail-maker (source: youtube-thumbnail-maker/SKILL.md) - "X + Y" two-app thumbnail (Higgsfield nano_banana_2)
- PURPOSE: thumbnail for a video comparing or pairing two tools/apps - two 3D app icons with a white "+" over a dark gradient and green stock-line, plus a headline. (This is the skill Jordan named as "the concept is attainable" - his methodology disagreement is with the author's, not the concept.)
- FUNCTIONALITY: (1) collect two software names; headline text must be supplied (never invented; ask once, or accept "you pick"); (2) load Higgsfield tools via ToolSearch; (3) find official high-res icons per app (WebSearch "<software> app icon png high resolution", prefer press/App Store/Wikipedia, simpleicons last), download with curl, verify it is a real non-empty image; (4) batch upload reference JPG + 2 icons via `media_upload` -> curl PUT -> `media_confirm`; (5) `models_explore` to read the model's accepted media roles; (6) `generate_image` model `nano_banana_2`, `aspect_ratio 16:9`, `count 1`, prompt template with headline in quotes and icon slots; (7) `job_status` sync poll; (8) deliver URL.
- USE CASE FOR JORDAN: "make a thumbnail for <tool A> vs <tool B>" in his house style - which is exactly what he says is "extremely attainable" (the concept, not the author's method).
- DECISION MODEL: none for the image itself. Headline choice is human. System One option for icon search: "Is this downloaded file the official brand icon for <app>? (yes/no/unsure)" (replaces a guess-and-check loop).
- DETERMINISTIC PARTS: download validation (`file` magic bytes, size > 0, PNG/JPEG header), square-ness check on icons (aspect 1:1 +-5%), 16:9 check of output, headline length, icon resolution minimum (e.g. >= 512 px), colour check of output (dark background luma, one green accent line), brand-colour extraction from official icon (for consistent palette), the upload/confirm chain (fixed API sequence = scripted), "never invent headline" (a hard check that headline is present before the generate call).
- DATA IT NEEDS: Jordan's own reference thumbnails (his standards), a library of approved brand icons (cache), past thumbnails + CTR.
- RULES WORTH COPYING:
  - Reference style = "dark gradient background + bold white headline + two 3D iOS-style app icons flanking a white '+' + subtle green stock-chart line." [DET for background luma/gradient, green line presence via hue mask; AI for "3D glossy"]
  - "Never invent the headline text. Ask once, or accept explicit 'you pick'." [policy / DET: headline field non-empty]
  - "Prefer official icon sources over random Google results." [policy]
  - "Validate downloaded icons are non-empty real image files before uploading." [DET]
  - "Use aspect_ratio 16:9 and model nano_banana_2 unless the user explicitly asks otherwise." [DET]
- BECKY MATCH: none found (verified: no "thumbnail" hit in becky-tools INDEX.md, README.md or SKILL.md).

### youtube-broll-maker (source: youtube-broll-maker/SKILL.md) - 10 s Seedance B-roll of the creator doing an action (Higgsfield seedance_2_0)
- PURPOSE: make cutaway footage of the creator himself doing an action from one photo, pick a cinematography preset.
- FUNCTIONALITY: (1) inputs: one photo, action phrase, style preset (asked via AskUserQuestion, never picked for user); (2) upload photo (`media_upload` -> curl -> `media_confirm` type image); (3) `generate_video` seedance_2_0, duration 10, 16:9, resolution per preset, genre per preset, media role "image" (not start_image); optional `get_cost` preflight; (4) poll `job_status` sync, wait 120s+ if pending; (5) deliver mp4 + the resolved prompt verbatim.
- 5 presets (table in SKILL.md): Cinematic Anamorphic (drama, 1080p), Documentary Vlog (auto, 720p), Tech Studio (action, 1080p, RGB/neon, motorised slider), Golden Hour Lifestyle (auto, 1080p), Moody Mono Film (noir, 1080p, B&W 35mm grain).
- USE CASE FOR JORDAN: identity B-roll cutaways for long-form ("me doing X") - the same job famous-youtube-editor mode D needs.
- DECISION MODEL: none in pipeline (preset is a human choice). System One option: "Does this generated 10s clip keep the same wardrobe/face as the reference photo in the first and last second? (yes/no)" - needed because Seedance drifts wardrobe mid-clip (see famous-reel-editor lesson).
- DETERMINISTIC PARTS: photo validity (dimensions, face present), upload chain, duration check (10s), aspect ratio check, slicing best 3-6s window by motion score, wardrobe-drift check via frame-to-frame face-embedding distance (insightface), file hash to avoid re-generating the same clip.
- DATA IT NEEDS: a clean photo of Jordan (or a raw frame), action list, preset choice, cost/credit balance.
- RULES WORTH COPYING:
  - "Use image role (identity reference) not start_image. This gives more dynamic B-roll motion." [AI/tool setting]
  - "Always aspect 16:9, duration 10, model seedance_2_0 unless overridden." [DET]
  - "Surface the chosen preset's resolved prompt verbatim ... so the user can tweak and re-run." [process]
  - Prompt suffix: "identity preserved" in every preset. [DET: string present]
- BECKY MATCH: none found.

### youtube-clipper (source: youtube-clipper/SKILL.md) - YouTube URL -> short clips via Higgsfield personal clipper (FNF Clipify)
- PURPOSE: turn one long YouTube video into N shorts with hook, score, duration and transcript, delivered as a list.
- FUNCTIONALITY: (1) need a YouTube URL; (2) `list_workspaces` + `balance` sanity check; (3) `personal_clipper_create` with `urls` (array), `clips_num` (default 10, max 20), `clip_aspect` 9:16 (also 1:1, 16:9), `subtitle_font` (Inter default; list: Noto Sans, Noto Serif, IBM Plex Sans, M PLUS Rounded 1c, Bebas Neue, Archivo Black, Unbounded, Montserrat, Bangers, Permanent Marker, Playfair Display, Caveat, ...); (4) poll `personal_clipper_status` by `row_id` with ScheduleWakeup 270s then 600-1200s; (5) deliver numbered list with hook, duration, score, transcript, URL; never claim 10 if fewer returned.
- USE CASE FOR JORDAN: his long-form -> Shorts pipeline; a ready-made clip-candidate list with per-clip scores he can accept or reject. Keep the score + hook + transcript; they are the signal.
- DECISION MODEL: the clipper itself is an opaque AI ranker (score per clip). This is a System One-shaped output (scored candidates). System One question he could ask about our own clips: "Would this 30-60 s window stand alone as a short with a hook in the first 3 s? (probabilities)".
- DETERMINISTIC PARTS: clip count verification, duration check (short-form cap), aspect check, subtitle font validation against the allowed list, download and hash, transcript overlap check (is the hook actually the first spoken line).
- DATA IT NEEDS: the long-form video + transcript (we have ours), engagement analytics per clip (to learn which hooks work).
- RULES WORTH COPYING:
  - "Default to 10 clips, 9:16 aspect, Inter subtitle font." [DET]
  - "Surface every per-clip field the API returns (hook, score, duration, transcript) - that's the signal users pay for." [process]
  - "Echo the actual clip count returned; don't pad." [DET]
- BECKY MATCH: partial (verified read-only). README.md "Clipping (shorts)" lists `becky-moment` (which bits are worth posting), `becky-hits` (moments to reel) and `becky-short` (9:16 short with a Gemma-4 critic). These cover the clip-picking step for a video Jordan already has. The YouTube-URL download and Higgsfield clipper step are not covered.

### youtube-popup-graphic (source: youtube-popup-graphic/SKILL.md; its youtube-clean/ subfolder is a duplicate of youtube-clean/) - one "Premiere-style" UI popup card per item, Seedance from a style reference
- PURPOSE: a single UI card popping into the centre of frame in the look of a user-supplied reference image, with the item name on it.
- FUNCTIONALITY: (1) item name + required reference image path (never proceed without one); (2) load Higgsfield tools in one ToolSearch call; (3) `balance` preflight (one 8s 1080p seedance ~12-30 credits); (4) upload reference directly as role "image" (no nano_banana intermediate; no start/end image interpolation); (5) `generate_video` seedance_2_0, 16:9, 8 s, 1080p, genre auto, count 1, short prompt ("Create an Adobe Premiere Pro style popup animation for 1 <thing> called '<item>'... Sound effects only - no background music"); (6) poll with background until-loop 60-90 s; if `preset_recommendation` returned, retry with `declined_preset_id`; (7) multi-item: separate parallel jobs, one item per shot.
- USE CASE FOR JORDAN: branded "lower-third style" pop-up callouts for named tools/skills inside his videos, styled to his own UI reference (matches the brand look; can be a VEGAS title/graphic instead).
- DECISION MODEL: none in pipeline. The reference-style match is human judgement.
- DETERMINISTIC PARTS: reference file validation, job submission and polling, output dimension/duration check (8 s, 1920x1080), audio check that no music bed exists (volume/spectral check), the text-spelling check on the final frame (OCR the item name vs requested string).
- DATA IT NEEDS: Jordan's UI/style reference images; item names; (for VEGAS) the placement timestamps.
- RULES WORTH COPYING:
  - "One card per popup. Three text labels in one 8-second shot reliably get garbled." [DET: OCR word count per frame]
  - "Keep the prompt short: ~6-10 lines max." [process]
  - "SFX-only audio by default ... say 'Sound effects only - no background music, no soundtrack, no melody'." [DET: music-presence check via loudness of non-speech band]
  - "Duration 8 s, 1080p, 16:9." [DET]
- BECKY MATCH: none found (VEGAS title/graphic would be done in VEGAS directly).

### capcut-smartcut (source: capcut-smartcut/SKILL.md + scripts/) - remove silences and duplicate takes inside CapCut draft projects
- PURPOSE: clean talking-head CapCut projects (silences + repeated takes) using CapCut's auto-captions as the transcript, and fix/split/merge captions.
- FUNCTIONALITY: CLI (JSON in/out) with commands `list`, `open`, `smart-cut` (heuristic: silence_threshold_sec 1.0, similarity_threshold 0.5), `cut-ranges` (apply exact cuts decided by the agent), `edit-subtitle`, `split-subtitle`, `merge-subtitles`, `fix-word-timing`, `batch-edit`. Workflow: back up project -> smart-cut -> MANDATORY transcript review pass by the agent (keep LAST take, remove earlier takes, intra-caption repeats, partial restarts, stray fillers) -> cut-ranges in ONE call -> re-open and re-read until clean.
- USE CASE FOR JORDAN: quick clean of a talking-head project already in CapCut. Note: he is a VEGAS editor; CapCut is an alternative tool only.
- DECISION MODEL: LLM free-text (could be System One). Question: "Is this caption a repeat or abandoned restart of a later, fuller caption? (p of repeat; first-take / last-take id)". Also "Is this a stray filler fragment (um, which which)? (yes/no)".
- DETERMINISTIC PARTS: silence detection by threshold on word gaps (smart-cut heuristic), similarity via string similarity (0.5 threshold - heuristic), range merging, timing arithmetic, backup copy, verifying no repeated n-grams after the cut.
- DATA IT NEEDS: CapCut project draft JSON (word timings per caption), auto-captions.
- RULES WORTH COPYING:
  - "ALWAYS keep the LAST take and remove the earlier ones - never the reverse." [DET+ASR: last-take rule]
  - "Repeated takes: remove ALL earlier takes; the speaker retried until they nailed it." [AI+ASR]
  - "Silence threshold default 1.0 s." [DET]
  - "Back up the project folder first, outside the drafts folder." [process]
  - "cut-ranges operates on the CURRENT timeline - re-run open before computing more ranges." [process]
  - "Never run smart-cut while CapCut is open." [process]
- BECKY MATCH: none for CapCut (verified). For silence removal, `becky-cut` (auto-editor + VAD) and `becky-roughcut` (raw takes to a VEGAS timeline) cover the same job on VEGAS. For the duplicate-take detector, becky transcription + an n-gram scan (see youtube-clean 5-gram rule) is the same idea.

### famous-repurpose-ig (source: famous-repurpose-ig/SKILL.md + recipes.md) - respin one of Jordan-style own winning reels into a fingerprint-different variant
- PURPOSE: repost a winning reel without it being flagged as duplicate: change hook, cuts, captions, speed, pitch, colour, flip, metadata.
- FUNCTIONALITY: (0) preflight (input file or Instagram URL; own content only); (1) Whisper transcription with word timestamps; (2) pick a 2-4 s cold-open hook from the middle/end (boldest claim, a number, payoff, curiosity gap), cut on speech boundaries; (3) cut plan: hook + body starting after original first sentence; tighten 2-4 pauses >0.4s to ~0.15s; drop one low-value segment; keep total within +-10%; (4) flip unless text present (extract 3 frames, look for text/logos); (5) ffmpeg pass 1: cut + concat, optional hflip, colour shift, Ken Burns drift, +-2-3% speed, scale to 1080x1920; audio tempo + pitch + EQ; (6) re-transcribe assembled video, ASS burned captions 2-4 words, uppercase, randomised style, position ~72-80%; (7) final encode strips metadata, CRF 19-23, loudness normalisation, output `-respin-N.mp4`; (8) optional `--broll` 1-2 Higgsfield 3-4s cutaways between passes; (9) report hook chosen, cuts, flip, duration; cadence advice.
- USE CASE FOR JORDAN: resurfacing his own old winners on Instagram. Ownership rule: only own content.
- DECISION MODEL: LLM free-text (could be System One). Questions: (a) "Which 2-4s span is the punchiest cold-open? (rank of candidate windows)"; (b) "Does this frame contain readable on-screen text, logo, or numbers? (yes/no)" - decides flip.
- DETERMINISTIC PARTS: segment cutting, pause tightening (gap > 0.4 s -> 0.15 s), duration check (+-10%), flip decision by OCR (not by eye), ASS caption generation, metadata stripping, CRF randomisation, loudness, ffprobe verification of output.
- DATA IT NEEDS: the source reel file, transcript, analytics of which reel won.
- RULES WORTH COPYING:
  - "Wait ~2-4 weeks before reposting a winner; stay under 10 respins per rolling 30 days per account." [DET: count of respins per 30 days]
  - "Cut gaps >0.4s down to ~0.15s; tighten 2-4 pauses." [DET+ASR]
  - "Keep total duration within about +-10% of the original." [DET]
  - "Skip the flip whenever readable on-screen text is detected." [DET via OCR]
  - "Randomize every tunable per variant (speed +-2-3%, colour, CRF 19-23)" [DET]
  - "Never cut mid-word; always cut on Whisper segment/word boundaries." [DET+ASR]
  - Speed/pitch/colour changes "subtle - if a viewer could notice, it's too much." [AI]
  - Copyright / ownership rule: "decline if the video is someone else's content being disguised for reposting." [policy]
- BECKY MATCH: none found for respin. Transcription = becky transcribe; a hook ranking would be a System One question.
### youtube-clean (source: youtube-clean/SKILL.md, cutting.md, layout.md, section-agent.md; youtube-popup-graphic/youtube-clean/ is an identical copy) - long-form YouTube edit: camera + screen recording + script, mediator + section sub-agents
- PURPOSE: one-pass edit of a long-form recording (camera file + screen recording + the script read) into a calm 16:9 video, cut once, verified from frames and signal, stitched only on approval.
- FUNCTIONALITY (pipeline steps 0-9): (0) intake, project folder, copy lib, check Scribe credits (~1,600 credits per 25-min pass); (1) sample screen recording 1 frame per 5 s to find where the script is shown (bright docs page, mean luma > 225) -> non_script_spans.json; read the script from frames; (2) voice cut: Scribe once on camera audio, hand-written targets.py, match words, refine cut points on RMS troughs, split inner pauses > 0.40 s -> edl.json; (3) build_sections.py + verify_sync.py (<= 40 ms), Scribe per section, triple check (coverage, 5-gram duplicates, read-through, no pause > 0.45 s), said_twice_check.py (semantic duplicate check by Jev, 3-way verdict); (3b) build_screen.py strips browser chrome and dissolves cuts; (4) one research agent gathers real numbers and copy; (5) one background agent per section with a brief; (6) mediator verifies each return (ffprobe, contact sheet, first/last frames, verify_final_sync.py = 0 +- 1 frame); (7) notes loop per section; (8) stitch.py only on the owner's word (-14 LUFS, chapters); (9) learn into layout.md.
- USE CASE FOR JORDAN: his long-form edits. Every rule here came from a real Albert note, so it doubles as a list of what he has rejected. Jordan is the editor: the same logic applies to his VEGAS timeline.
- DECISION MODEL: LLM free-text (could be System One). Questions that map onto Jev / System One:
  - Take selection: "Of these attempts at the same script sentence, which is the last COMPLETE one (no restart, filler, laugh, or 'sorry')? (probabilities over attempt ids)"
  - Duplicates: "Is sentence B already said by sentence A in the same video (paraphrase counts)? (p)". This is the existing said_twice_check; thresholds p >= 0.85 said twice, p <= 0.35 clear, between = human read.
  - Mode per beat: "cam / split / full / face / screen? (probabilities)". Done by the mediator by hand today.
  - Stage clutter: "Count the elements on this frame; does any one of them not carry the spoken line? (yes/no)".
- DETERMINISTIC PARTS: camera/screen offset by cross-correlation (+-10 ms), sync build and verify (<= 40 ms), RMS-trough cut points, pause length detection (0.25-0.45 s rule), gap and micro-fragment checks (< 0.8 s ranges), 5-gram duplicate scan, loudness (-30 LUFS music bed, -14 LUFS final, true peak <= -1 dBTP), face-crop maths, chrome detection (flat 59-grey bookmarks band), zoom-factor limits, screen-share dissolve, caption-click cues, "no text on screen-share" check (OCR), swear-word bleep/censor check, emoji blacklist check.
- DATA IT NEEDS: camera file, screen recording, script (or the doc frames), Scribe transcripts, product research (real pages and numbers), music track or licence list, Jordan's notes per section.
- RULES WORTH COPYING (all from Albert's notes; measurability tag in brackets):
  - "Never show his script. The screen recording shows his Google Doc most of the time." [DET: non-script spans via luma > 225 on the doc region]
  - "ElevenLabs Scribe only, never Whisper or another STT." [policy]
  - "Last take wins. Earlier takes, restarts and abandoned sentences go." [AI+ASR]
  - "Nothing twice, nothing missing, no breaks - triple check before any graphics." [DET+ASR for coverage and 5-gram duplicates]
  - "Voice = the screen recording's audio (good mic). Picture = the camera." [policy]
  - "Never concat per-cut files, never the concat demuxer for picture, never -shortest." [process]
  - "Calm, ~50/50: about half of every section is Albert full screen; visuals only where the line needs one. Stretches of 6-15 s per mode; do not flip modes every sentence." [DET: share of time per mode and stretch lengths from the shot plan]
  - "No visual shorter than ~2.5 s; fewer, longer-held surfaces." [DET]
  - Screen-share sections: "mostly screen (60-80%), cam for lines where nothing happens on screen, full for the ONE explainer animation; stretches >= 8 s; enter/leave screen on a sentence start." [DET]
  - "NO CAPTION, ever, on the full-screen camera shot" (Albert 2026-09-20: "remove the captions on the full screen of me as well"). [DET: caption overlay present during cam-only mode = violation]
  - "NO TEXT AT ALL while the screen share is on screen." (Albert 2026-09-19/20). [DET: OCR of the screen-share region = 0 text]
  - "Minimal by default: THREE THINGS MAX on the stage at once (window/diagram, label, number, caption; the subject counts as one)." [DET element count + AI for 'does it carry the line']
  - "Labels <= 3 words; cards and tiles carry a HEADLINE ONLY - no description line under it." [DET: OCR word count]
  - "NO EXPLANATORY SENTENCES anywhere on the stage." [DET: OCR; sentence-shaped text outside the caption = flag]
  - "One accent colour per frame." [DET: count of saturated hue clusters on the stage]
  - "Empty space is the design. Roughly half the stage stays empty." [DET: background-pixel fraction]
  - "Nothing static for more than ~0.6 s; nothing accumulates - an element leaves when he stops talking about it." [DET: frame-diff over time; element persistence]
  - Zoom GENTLE: "1.2-1.4x is the normal amount, 1.6x the hard cap; at most ONE push per screen stretch; moves >= 0.8 s; hold >= 4 s." [DET: zoom factor from the edit]
  - "Never zoom while Screen Studio is already moving." [DET]
  - Presenter card geometry: split = x 1250, y 80, 610x920, r48; full = 300x300 bubble r40 at bottom-right; face = 900x920 at x 510, <= 6 s. [DET: geometry]
  - "Card moves 0.35 s smooth; never cut the card position between two frames." [DET]
  - Sound: "one click per UI press, whoosh only on mode changes (-31 dB), no pop or bubbly sounds anywhere." Music: tense minor key, 100-125 BPM, bed about -30 LUFS pre-duck, sidechain 0.03/2/15/250, never chart music. [DET: LUFS, cue list, no-pop = spectral check on cue files]
  - "Swear words BLEEPED in the audio (1 kHz tone) and censored on screen (sh*t)." [DET+ASR]
  - "Never use the ninja emoji. No emoji depicting a person in a role that stands in for an ethnicity or stereotype." [DET: emoji blacklist; AI for the edge cases]
  - "Music: if Albert supplies a track use it; otherwise a free library with the licence page kept in audio/LICENSES.md." [policy]
  - "Graphics follow what he SAYS; flag misspoken facts." [AI]
  - Face in the slim card: "centre on the EYES/NOSE, crop the back of the head, never the brow or face side." [DET: face landmarks, e.g. macOS Vision in the original]
  - "Cold open on a real moment with a slow push-in; music starts after it." [DET]
  - Intro: "a new visual every 1.5-3 s, mostly full mode; hard number payoffs; voice may be tightened harder than the body." [DET: visual-change interval]
  - "First and last 8 frames of every section: white ground + presenter card in split position, so sections butt-join." [DET]
  - Joins: "body joins keep 60-120 ms of natural air; intro joins 10-40 ms; no gap > 0.25 s inside a section unless a deliberate beat (<= 0.45 s)." [DET+ASR]
  - "No micro-fragments: a kept range < ~0.8 s between two cuts flashes as a double jump cut." [DET: build_edl prints ranges < 0.8 s]
  - "Near-duplicates across sentences count as retakes too; keep the LAST, usually the fuller one." [AI; the Jev said-twice question]
  - "Loudness -14 LUFS integrated, true peak <= -1 dBTP at stitch; chapters from section durations." [DET]
- BECKY MATCH: partial (verified read-only). `becky-transcribe` is the transcript step; `becky-livestream` already does transcript -> becky-cut -> VEGAS for a livestream clip-down, and `becky-otio --import` brings a finished VEGAS cut back in. An n-gram duplicate scan is deterministic and can be built on the transcript. said_twice is a System One question (Jev). No becky tool does mode/layout choice.

### albert-reel-dark (source: albert-reel-dark/SKILL.md, style.md, revisions.md, references/lessons-2026-09-22/23/25/27.md, memes.md, fish-audio.md; template/ holds the Python renderer) - 1080x1920 DARK-MODE reel, recreated Apple UI, no face on the hook
- PURPOSE: Albert's Shorts/Reels in dark mode: recreated product UI (never screenshots), physical no-face hooks, real icons, real country outlines, full-screen cinematic captions, and comment-to-DM automation.
- FUNCTIONALITY (process steps 1-8): (1) project folder, copy template; align camera + OBS mic (`align.py`) -> master + teleprompter frame (the frame is the script); fact-check every product name and number against the live site/repo before drawing; (2) Fish Audio transcription of raw and final cut (`transcribe.py`), `fix_words.py` for corrections, `build_cut_dual_source.py` takes (last take wins, RMS outward edge scan, 70/100 ms pads, hook 1.12x speed), `splice_audit.py` reads every join as the intended sentence; (3) beat map: one row per spoken beat (mode full/split, surface, action, payoff), arc = mystery -> tease -> reveal -> proof -> honest caveat -> fix -> CTA; (4) write scenes.py (Pillow drawing: window(), apptile(), sf(), real brand PNGs); (5) previews (contact sheet, motion strips, dense transition strips); (6) render, mix, and gates: check_air (no quiet run >= 0.30 s), check_motion (no beat still > 0.60 s), check_frames (no blank frame, no ink in top 180 px, nothing under cinematic words), check_provenance (every take 1.0000 correlation within 0.5 ms), ebur128 true-peak on the final MP4; (7) deliver + README + zip; (8) comment automation (keyword = spoken CTA word, one link per promise, link lives in the DM only).
- USE CASE FOR JORDAN: the closest existing system to "AI makes the explainer/Short from my rules". Its gates are the best example in the set of DETERMINISTIC checks on a finished video. Its RULES are Albert's own accepted/rejected list, which is exactly the kind of "rules measured from HIS videos" Jordan asked for.
- DECISION MODEL: LLM free-text (could be System One). Questions it asks an LLM: (a) take selection (last take wins) - "Which attempt of this sentence is the final complete take? (probabilities)"; (b) beat mode - "full or split for this beat? (p)"; (c) which "feeling" lines get a meme - "Is this line a shared feeling (grimace), a payoff (celebration), disbelief, or 'use your brain' (p over four types, plus none)?"; (d) hook choice - "Is this line a physical, no-face hook? Which verb is stressed?"; (e) mis-spoken product name - "Does the spoken name match the product name on the scraped site? (yes/no/unsure)". Reasonable System One candidates: (c), (d), (e). (a) also (see youtube-clean).
- DETERMINISTIC PARTS (largest share of the skill): master alignment (cross-correlation), RMS profile cuts, splice audit, quiet-run check, motion gate (check_motion), frame gate (check_frames), provenance (check_provenance), loudness/true peak (ebur128 on the MP4, limiter .68), frame counts, `at(beat, word)` lookups (word timing from transcript), every number on screen equals a spoken number or a sourced number (compare text to transcript), the caption-click cue list, the no-stamp rule (label/word-on-scene detector), the CTA has no link chip (element check), the Instagram top-180px safe zone, dark-on-dark contrast check (pixel luma vs ground), first frame not blank.
- DATA IT NEEDS: camera file, OBS mic track, transcripts with word times, live product pages/repos (for facts and icons), Apple emoji font, Fluent 3D emoji, iTunes Search API icons, GeoJSON, GIFs (memes), music track (Timeless excerpt approved), past reel gold standard (Laya README), analytics (none in pack).
- RULES WORTH COPYING (all quoted from the skill or Albert's notes; measurability tag in brackets):
  - Mode rhythm: "Alternate face and full screen ... the Laya rhythm was F S S S F S F S S F S F S (5 full / 8 split): full for the hook, the hero object, the numbers, the real page and the partner reveal; face for reactions, the name, and the CTA." [DET: mode sequence from beat map]
  - "13 beats in 32 s, every beat its own surface, every number real, every landing on the spoken word." [DET: beat count / duration; AI: 'own surface']
  - "Every beat gets a different surface. Do not replay a screen for another line." [DET+AI]
  - "Hook is no-face and physical... Keep it big: tiles >= 280 px, cards >= 600 px wide, nothing cut off, and something already moving on frame 0." [DET: tile sizes; frame-0 motion via frame-diff]
  - "Hook tiles 280-360 px, row tiles 112 px, title-bar tiles 48 px, Dock tiles 54 px." [DET]
  - "Caption clicks on first and last word of every full-screen cinematic group. Presenter chips stay silent." [DET: cue list vs caption groups]
  - "Full-screen scenes: UI in the upper area, cinematic words centred around y 980-1150; nothing below." [DET]
  - "Instagram safe top: TOP_SHIFT=90, split line at SPLIT_Y=858; nothing important within ~180 px of the top edge, nothing in the bottom 90 px of a scene canvas." [DET: bbox check]
  - "Visual cues that must land on a word lead it by ~0.13 s (Dock pops); springs <= 0.3 s." Later: "a shot is instantaneous, lead .05 s". [DET+ASR: onset vs cue time]
  - "Nothing static for more than ~0.6 s inside a beat" (gate: check_motion no beat still > 0.60 s). [DET]
  - "Never retime dialogue to fit graphics; retime graphics to the transcript's word onsets via at(beat, word)." [DET+ASR]
  - "Hook pacing: hook voice ~1.12x (atempo, pitch kept); hook ranges keep exact trough times; target 10-20 ms of near-silence at each hook join." [DET]
  - "Hook captions: break before each named item; payoff gets its own group, larger and green (g['big']=170; colour #30D158)." [DET+ASR]
  - "Sentence-start words get a capital." [DET]
  - Music: "Albert picks the song ... put the first hit / beat drop on the first icon landing." Default = approved Timeless excerpt from 23.1176 s at -29 LUFS pre-duck, sidechain threshold .03 ratio 2, limiter .84 (later .68). [DET: LUFS, offset]
  - "Fact-check before you draw. Product names, numbers and claims come from the live site/repo scraped today, never from the teleprompter or memory." [policy + DET: scrape timestamp]
  - "Every number on screen is spoken or sourced. Spoken numbers count up and land exactly on their word with a frame (red for the rival, green for the hero); sourced numbers copied from the README. No invented benchmarks, badges or percentages." [DET+ASR: on-screen numbers must be in transcript or source]
  - "No empty halves. A card that will receive a second row starts small and grows; a tile waiting for a partner sits centred." [DET: card width over time; empty-slot detector]
  - "Keep the new tool a mystery until its name is spoken" ("?" tile -> flip on name). [DET+ASR: flip time = name onset]
  - "Literal objects for literal words: local/your computer -> MacBook; cloud -> packet on a route; faster -> race/gauge; can't write code -> code struck out; tuned/trained -> training curve." [AI for mapping; DET for presence]
  - "Never draw your own person or character figures ... icons from the internet only" (Albert 22 Sep). Apple emoji native 160 px, Fluent 3D emoji for people, iTunes Search API for app icons. The Claude tile is the only drawn character. [DET: image-source check, no vector figures]
  - "A named country gets its real outline." GeoJSON, ink fill, white state lines; wipe in on the word. [DET+ASR]
  - "Shell game = Claude in a top hat." Reveal fires on the word before the payoff; hat off in ~0.16-0.18 s. [DET+ASR]
  - "Reveal timing: payoff visible before the payoff word; three throws in 0.5 s feels rushed; ~0.9 s apart across beats." [DET+ASR]
  - "No grey sub-line under a row title or card headline. One line per row." [DET: OCR line count per row]
  - "Why is that? / any thinking beat = the Apple thinking emoji (4K keyed)." [AI for beat type]
  - "Anything that grows off the top of a card is wrong; size the card so the top of the tallest element stays inside (940x620 max in split mode)." [DET: bbox]
  - "One to three memes per reel, on the feeling lines; never on a proof beat; watermark cropped out; 940 px card; lands on the feeling word." [DET: meme count 1-3; AI for line type; watermark = crop check]
  - "Claude's gestures are drawn in terracotta, never emoji." [DET: no emoji glyph in gesture slot]
  - "Real people hold their props ... sleeves in the photo's suit colour reach both grips; the prop crosses the chest below the chin, never the face." [DET+AI]
  - "Shell-game motion is one continuous choreography for every beat it spans" (no re-entry at the cut). [DET: continuity check = same state at cut]
  - "A list of props being dismissed = thrown away, one per stressed word, spread out, ~0.9 s apart." [DET+ASR]
  - "Cursor picks at least 0.4 s apart; cursor glides between targets." [DET: pick timestamps]
  - "Dark mode, always. Ground #0A0A0B, cards #1C1C1E with a hairline edge, light text, Apple dark system colours." [DET: pixel luma of ground]
  - "Contrast check on every preview sheet: nothing dark-on-dark; no pure white slab bigger than a brand tile." [DET: contrast ratio vs ground]
  - "Products in their own dark theme (X dark, Instagram dark comments, GitHub dark, Safari dark)." [AI+DET: palette match]
  - "Rule 32, NO STAMP/LABEL OVERLAYS: never slam, pop or stamp a word over a scene (no GONE, SOLD OUT, EXPIRED, DONE, NEW, FREE, WOW, rubber stamp, banner). Test: would this word be on the real screen being recreated? If not, cut it and cut its sound cue." [DET: OCR of words on scene vs transcript and vs real UI copy list; cue list cross-check]
  - "No link chip under the CTA. The CTA is the comment UI typing and posting the keyword plus a like on the last word." [DET: element check]
  - "Fact-check outcome: a sourced number -> recreate the source page (article_zoom / pricing_zoom); an unsourced spoken number -> keep it (it is spoken), flag it first." [AI+policy]
  - "Signature move: recreated real page -> camera push-in -> count-up landing on the spoken number -> yellow highlighter sweep on the exact row." [DET+ASR]
  - Gates: "check_air.py (no quiet run >= 0.30 s), check_motion.py (no beat still > 0.60 s), check_frames.py (no blank frame, zero ink in top 180 px, nothing under the cinematic words), check_provenance.py (lag-searched 1.0 correlation per take within +-30 ms)". [DET all]
  - Sound budget: "about 90 cues for 33 s; gunshot -15 dB first/last, -17 singles, -19 burst; whoosh -23 on break, -27..-31 on flips; pop -26..-31 on landings; voice about -17 LUFS integrated after mix, true peak -1.4 dBFS." [DET]
  - "No dead air after a transition: the next beat's hero already falls through the clearing smoke and lands on the first spoken word." [DET+ASR]
  - "Curvy connectors (cubic S-curves), not straight lines." [DET: curve vs line geometry]
  - "He revises a drawn gesture in small steps; preview every gesture as a 2x close-up before rendering; keep the earlier FINAL as -vN.mp4." [process]
  - Process: "Scribe word edges lag the sound: only tighten a gap the 10 ms RMS confirms." [DET]; "Smoke-test every beat (start/middle/last frame) before a full render." [DET]; "Background wait: grep ^mixed|Traceback, not Error." [process]; "Never pkill a render without killing its ffmpeg encoders." [process]; "Remove an overlay = remove its sound cues too." [DET: cue cross-check]; "Weak payoff -> push the physical action, not a word." [AI]
  - Revision recipes: "Remove a whole beat -> drop its range, delete its concat line, shift later beats and words by -dur (all DET)". "Timing feels late -> lead the visual by 0.13 s, shorten spring to ~0.26 s (DET)". "Empty screen complaint -> something must move for the whole beat; check a strip at 0.2/0.6/1.0/1.5 s (DET)". "A take that runs into an aborted sentence -> trough 20-40 ms before next onset, FORCE_END, 12-15 ms FADE_OUT (DET)".
- BECKY MATCH: partial. becky transcription is the Fish/Scribe equivalent; no becky tool produces the gates (air/motion/frame/provenance) or the beat map. The four gates are a strong candidate for becky-style deterministic tools on VEGAS output.

### albert-reel (source: albert-reel/SKILL.md, README, SETUP, references/) - the green/orange 9:16 reel editor, dual-source version
- PURPOSE: same as famous-reel-editor (see that block) for Albert's own account. Body text is ~97% identical to famous-reel-editor; the differences are the items below.
- FUNCTIONALITY ADDED over famous-reel-editor: a **dual-source input** step: when there are two recordings (an OBS .mov with the good mic + a camera .mp4 with the good picture), (1) transcribe both; (2) offset from transcripts = histogram mode + median of matched word time differences; (3) confirm by cross-correlation of 8 kHz mono WAVs over a 30 s window at 5 ms steps (accept when within ~50 ms of the transcript estimate; correlation ~0.2 is normal); (4) mux with `-map 0:v -map 1:a -c:v copy -shortest`; (5) run the normal pipeline on the master. Validated on the yoom reel (offset 2.115 s).
- USE CASE FOR JORDAN: any time he records a take on camera plus a separate mic. The offset method is deterministic and reusable for VEGAS (align two tracks).
- DECISION MODEL: none new. Same LLM uses as famous-reel-editor.
- DETERMINISTIC PARTS: transcript-offset histogram, cross-correlation, mux. Also the same caption, cut and sync checks as famous-reel-editor.
- DATA IT NEEDS: camera file, OBS/mic file, transcripts.
- RULES WORTH COPYING (new ones only; the rest are as in famous-reel-editor):
  - "Offset: trust the cross-correlation peak when it lands within ~50 ms of the transcript estimate." [DET]
  - "Modest correlation ~0.2 is normal (room mic vs voice mic)." [DET]
  - Caption font: the default is 92 in the body text; the white variant later changed it (see albert-reel-white).
  - Error row: "Dupes hide as sentence OPENERS, not repeated sentences" (see famous-reel-editor's error table): keeping the last take of two different sentences can still ship "Well, you don't have to choose. Well, both together." or "And now inside of your project... And now inside of your project...". After the final cut, scan consecutive sentences for identical opening n-grams. [DET+ASR: n-gram scan on consecutive sentence starts]
  - "Hero cards" and SFX: same rules as famous-reel-editor.
- BECKY MATCH: partial. becky transcription + offset alignment would be the same machinery; no becky tool does the two-track mux.

### albert-reel-white (source: albert-reel-white/SKILL.md, references/, scripts/) - WHITE "futuristic popout" 9:16 reel with a head cutout over a floating frame
- PURPOSE: the white, popout look: white ground, dark text, one orange accent, cards at the top, the speaker in a rounded floating frame, and their HEAD CUTOUT rising above the frame line.
- FUNCTIONALITY: same pipeline as famous-reel-editor, but (a) cards re-themed for white (pills and tiles at rgba(accent,0.07) with accent borders, grid 0.14 alpha, glow 0.13); (b) the speaker sits in a rounded card (radius 36) whose top edge is pushed down, with a drop-shadow PNG (PIL rounded rect alpha 90 + GaussianBlur 22); (c) a person matte per frame (rembg u2net, in its own venv, ~600 frames, minutes) gives the head cutout which overlays ~100 px above the frame line; (d) black captions (patched captions.py: fill (20,20,20), halo white alpha 170); (e) compose uses a custom ffmpeg chain (cards base -> frame shadow -> alphamerged rounded video -> popout RGBA sequence -> captions -> loudnorm + music -> SFX remix), not compose.sh.
- USE CASE FOR JORDAN: a distinct on-brand look for a series. The matte step is the one heavy, non-trivial piece.
- DECISION MODEL: none new in the pipeline. The person segmentation (rembg) is a model, not an LLM; it is "is this pixel the person? (matte)".
- DETERMINISTIC PARTS: layout maths (frame top, popout top, caption y), matte erosion (MinFilter 5 + GaussianBlur 1.2), compose, loudness, SFX placement, caption-to-frame gap measurement on frames.
- DATA IT NEEDS: talking-head clip, transcript (word times), card specs, music.
- RULES WORTH COPYING:
  - "White background full frame; text #141414; accent pills rgba(ACCENT,0.07) with accent borders." [DET: ground luma]
  - "Floating video frame: radius 36; drop-shadow alpha 90 + blur 22 - mandatory, or the edge is invisible." [DET: shadow present at frame edge]
  - "Head pops ~100 px above the frame line (rule: most of the head above the line, not just hair - user preference)." Later version: pops ~150-200 px. [DET: matte bbox above frame top]
  - "Black captions; white captions are invisible here. Font size ~66 (user's settled preference after 92 and 76 both read too big)." [DET: caption size; OCR contrast]
  - "NO expanding shockwave rings / circle effects (user removed them from the yoom reel): impact = slam entrance + shake + SFX, never a radiating circle. Deliberate circular GRAPHICS like a stopwatch dial are fine." [DET: ring detector - circular outline expanding over time]
  - "Hero cards hold ~0.3-0.4 s past their sentence before the next beat ('show Yoom a little longer'); re-anchor the next beat's first element to its new start." [DET+ASR: hold time after last word]
  - "Emoji icons render ugly in headless chrome: for any emoji that IS the card's hero icon, draw an inline SVG instead." [DET: emoji glyph in hero slot]
  - "EXPECT anchors: compare case-insensitively." [process]
  - Matte: "Cache full person-mattes; rebuild strips from cached mattes instead of re-running the model." [process]
- BECKY MATCH: none found. Matte step = a segmentation model (not a becky tool).

### albert-reel-white-screen (source: albert-reel-white-screen/SKILL.md, references/animation-library.md, card-snippets.md, style.md, scripts/) - SPLIT-SCREEN TUTORIAL reel: talking head + screen recording with punch-in zooms
- PURPOSE: tutorial/demo Shorts: show the creator's own screen recording in the top band exactly when he demonstrates, with punch-in zooms onto the UI he is describing, and animated cards when he only talks.
- FUNCTIONALITY: (1) decide "screen windows" from the final transcript: a window = a sentence where the creator narrates on-screen action; concept, hook and CTA sentences stay as cards; back-to-back windows merge; (2) extract per-window video through the full EDL chain with `screen_windows.py` (per-window mapping, NOT a companion cut overlaid by time - frame rounding accumulates ~0.4 s over 14 segments); (3) zoom each window (zoompan, slow ease-in, zoom 1.15-1.18 for pages, 1.10-1.12 for IDEs; focus fx/fy chosen by looking at a frame); (4) composite with `compose_screen.py` (1020x574 rounded screen card at (30,120), radius 24; speaker frame; popout; captions; loudnorm + music); (5) cards only for hook/concept/done/CTA; (6) silent demo stretches usually cut; (7) MANDATORY `zoom_review.py` scores every stage for visual detail (blank space < 1.5 fails) and suggests fx/fy - never compose a flagged stage.
- Also in this skill: hook = claim + on-screen PROOF (open on the receipt/balance, punched to zoom 2.2-2.6); question hooks open with a giant accent "?" slam; "two things merge" = orbit -> snap -> burst -> spin (no static plus sign); the IG hook title pill (one black rounded block with uppercase Montserrat 900 ~84 px, shrink to width <= 1000) replaces karaoke captions only during the hook sentence; multi-pair reels (hook pair and body pair recorded separately: align each, concat masters, per-window offsets).
- USE CASE FOR JORDAN: a VEGAS-style tutorial: screen capture cut into the video at the moments he narrates it, plus a zoom into the exact UI element. The "zoom where the viewer must read" rule is a direct template for editing his screen-share segments.
- DECISION MODEL: LLM free-text (could be System One). Questions: (a) "Is this sentence a narration of on-screen action (yes/no)?" (decides windows); (b) "Which UI element must the viewer read in this window? (focus candidates, probabilities)"; (c) "Does this frame show the target UI readable at this zoom? (yes/no)" (replaces by-eye zoom check); (d) "Is this an explicit proof number (balance, price, counter) to punch onto? (yes/no)".
- DETERMINISTIC PARTS: per-window extraction through the EDL chain, zoom maths, zoom_review detail score (blank-space test), the popout seam check (hair continuous across the frame edge), 1 fps overlay detection by brightness probe of its screen region (for dictation popups), cropdetect for pillarbox bars (crop to exact 16:9, e.g. 1664:936:128:40 for 1920x1080), caption-to-frame gap, splice-point gap check (any gap > 0.30 s at a splice = micro-cut), "no whoosh" cue check, EXPECT anchors (regenerate from fresh transcript each time), money normalisation ("three hundred dollars" -> "$300").
- DATA IT NEEDS: talking head, screen recording (same file as mic audio in dual-source), final transcript, per-window EDL chain, music.
- RULES WORTH COPYING:
  - "Windows are word-anchored [first word start - 0.05, next non-demo word start]." [DET+ASR]
  - "Zoom: punch-in model, ~0.5 s full view -> fast cosine punch (~0.35 s) onto the FOCUS -> hold with a barely-visible drift. Never a continuous slow zoom across the window." [DET: zoom curve]
  - "Zoom levels: 1.6-1.9 for small UI (inputs, popups, buttons); 1.3-1.5 for text blocks / whole panels." [DET: zoom factor]
  - "Defaults: zoom 1.15-1.18 for pages/prompts, 1.10-1.12 for busy IDE screens." [DET]
  - "Left-aligned content needs fx=0 (search queries, address bars, code): a centred fx at high zoom crops the start of the text being read." [DET: focus x vs text-left-edge]
  - "Mac pillarbox: ALWAYS run cropdetect on the screen source; never ship bars inside the screen card." [DET]
  - "NO whoosh SFX anywhere (user preference): screen cards silent or pops/booms/ticks/dings only." [DET: cue list check]
  - "Compute cue times from the final transcript's word times, never hardcoded seconds." [DET+ASR]
  - "Rebuild every cut-derived artifact after any re-cut (atomic checklist); verify the popout seam: the hair continues across the frame edge with no duplication or shift." [DET]
  - "Captions sit between the screen card bottom at 694 and the popout at ~870 - never move them." [DET]
  - "Redundant confirmation clauses are cut candidates (trailing clauses that re-state the action just performed)." [AI]
  - "Splice pads re-create pauses AFTER the silence pass: after the final cut, micro-cut any splice-point gap > 0.30 s." [DET+ASR]
  - "Silent demo stretches usually cut from the reel; if a silent demo must stay, give it its own range + window + SFX, no captions." [AI]
  - Card doctrine: "Cards must CHANGE continuously - every card is a sequence of 2-4 staged movements anchored to different words." [DET: event count per card, gap between events]
  - "Question hooks: giant accent '?' slam on the question words; never open a question with the answer's visuals." [AI+DET]
  - "Two-things-merge: snap to touching (separation >= logo width), not overlapping." [DET: bbox separation]
  - Tight layout: frame_top 1020, popout_top 720, capt_y 40, head pops ~150-200 px over the edge. [DET]
  - "Mandatory review: zoom_review.py before compose; a blank frame (score < 1.5) fails." [DET]
  - "Transcriber merges numbers inconsistently: normalise money words ('three hundred dollars' -> '$300')." [DET+ASR]
  - "Never assume indices survive a re-cut; rebuild EXPECT from the fresh transcript." [DET]
  - "Redundant clauses cut; splice pads sum to a 0.35-0.5 s hole - micro-cut." [DET+ASR]
- BECKY MATCH: none found for screen windows / zoom. becky transcription provides the word times needed.

## B. Instagram carousels and social writing

### albert-carousel-ig (source: albert-carousel-ig/SKILL.md, references/style-hacker-light.md) - 6-15 slide "hacker-light" carousel, icon-first, Higgsfield
- PURPOSE: a carousel deck in one locked style (cream background, pixel-art mascot, big flat app icons instead of screenshots) that can be built from a topic, a winning carousel, or a YouTube video.
- FUNCTIONALITY: (1) input: free topic, recreate a winning carousel (keep every slide's text verbatim, change only the visual system), or a YouTube URL (title via oEmbed, auto-subs via yt-dlp, transcript cleaned to text, slides planned from transcript structure); (2) plan 6-15 slides: cover, one slide per item, a "stack grid" recap when 4+ tools appear, CTA; (3) cover via `generate_image` (nano_banana_2, 4:5, 2k) and keep its job id as the style anchor; (4) all other slides via `generate_image_batch` (max 12 per call), each referencing the cover job id; (5) download PNGs named by content (`02_stripe.png`), write caption.txt, deliver; (6) tell the user which slides to eyeball (multi-icon slides and less-famous logos).
- USE CASE FOR JORDAN: turning a YouTube video into an IG carousel automatically (his "repurpose" flow), in his brand colour. Output is only the image set + caption; he posts it himself (skill never posts).
- DECISION MODEL: LLM free-text (could be System One). Questions: (a) "Which slides of this transcript are 'one idea each' vs filler? (yes/no per slide)"; (b) "Is this generated slide's logo the correct brand mark? (yes/no/unsure)" (catches the known-risky logos: GHL, Kit, Fathom, Higgsfield).
- DETERMINISTIC PARTS: oEmbed title fetch, VTT tag stripping, slide count (6-15) and naming, 4:5 / 2k dimension check, slide text kept verbatim vs source (string match), em-dash check in caption and slides (zero allowed), hashtag count (5), reading-level and banned-phrase check ("in today's fast-paced world"), colour check that accent is #E95223 and background luma is light (regenerate if dark), parallel download.
- DATA IT NEEDS: YouTube transcript + description, a winning carousel's slide images (for recreate), icon library descriptions, handle `@albert.olgaard`.
- RULES WORTH COPYING:
  - Accent is ALWAYS #E95223 unless named; "bold, saturated, not muted terracotta". [DET: colour sample]
  - "ONE style per deck: hacker-light. Never dark background, never serif headlines." [DET: background luma > threshold]
  - "Cover first, every other slide references the cover job id." [DET]
  - "4:5 portrait, 2k, nano_banana_2." [DET]
  - "Icons instead of screenshots, always. The icon is the hero of the slide." [AI/DET: no screenshot in deck]
  - "3 monospace benefit lines per card, no more." [DET: line count per card]
  - "One mascot gag per slide, never repeat within a deck." [AI+DET: gag names unique]
  - "NEVER em dashes anywhere (slides, caption, chat)." [DET]
  - "5th-grade reading level on viewer copy. Short sentences." [DET: Flesch-Kincaid grade]
  - "Every deck always has: cover, content slides, recap stack grid when 4+ apps/tools appear, CTA last. NO context slides, NO transition slides, NO filler." [DET: slide-role order]
  - "Caption: hook line, 2-3 concrete value lines, the comment-word CTA, 'Save this', then 5 specific hashtags. No inflated promo words." [DET: hashtag count 5]
  - "NEVER auto-post." [policy]
- BECKY MATCH: none found.

### famous-ig-carousel (source: famous-ig-carousel/SKILL.md, references/style-daylight.md, style-hacker-desk.md) - 6-slide cinematic carousel, two selectable styles
- PURPOSE: a 6-slide Instagram carousel with a cinematic cover, all slides in one visual world: "daylight" (photoreal blue sky, terracotta voxel robot, editorial serif) or "hacker-desk" (near-black desk, pixel-art mascot, condensed sans, code blocks).
- FUNCTIONALITY: (0) pick style: auto from topic (educational/list/announcement = daylight; technical/prompt/tutorial = hacker-desk) or the user names it; (1) accent colour (default terracotta #C2724F) and handle (`@username`), asked once; (2) plan 6 fixed-role slides: 1 cover (hook), 2 what/start here, 3 examples/list (3-5 items), 4 comparison old vs new, 5 how-to/prompt, 6 CTA; (3) cover via nano_banana_2, 4:5, 2k; keep job id; (4) slides 2-6 in parallel, each with the cover as style reference; (5) wait ~40 s, job_status for each; download to `outputs/carousels/<slug>/01_cover.png` ... `06_cta.png`; (6) caption.txt: hook + 2-3 value lines + CTA + 5 hashtags; optional humanizer pass.
- USE CASE FOR JORDAN: pinning a how-to or resource list from a video into a six-slide post. The two styles are a template for "one look per series" in his own brand.
- DECISION MODEL: LLM free-text for the style choice (a 2-way pick: "Is this topic educational/list (daylight) or technical/prompt (hacker-desk)?"). This is a clean System One candidate: probabilities over two styles from the topic and tone.
- DETERMINISTIC PARTS: six fixed roles and file names; 4:5 and 2k check; accent hex and handle present; no em dashes; 5th-grade reading level; "cover title must not promise a number the carousel does not deliver" (a number-match check: numbers in cover title must appear in slides 2-5); one style per deck (palette check: daylight = sky-blue + meadow green, hacker-desk = near-black luma).
- DATA IT NEEDS: topic or transcript, handle, accent colour, nothing else.
- RULES WORTH COPYING:
  - "ALWAYS one style per carousel. Never mix daylight and hacker-desk in the same deck." [DET: palette check]
  - "NO context slide. NO transition slide. NO filler. The cover IS the hook." [DET: slide role count = 6]
  - "The cover title must not promise a number the carousel does not deliver." [DET: number match cover vs body]
  - "Text always on paper cards or wooden signs, never painted on grass or sky (except the big cover headline)." (daylight) [AI+DET]
  - "Headline in editorial serif, roman + italic; accent only on key words and numbers." (daylight) [DET: accent-colour span count]
  - "Always a near-black background with a blurred dev desk. Never sky, never meadow." (hacker-desk) [DET]
  - "Headline always in bold condensed sans, never serif." (hacker-desk) [DET: font classifier]
  - "Code and prompts always in a dark card with terracotta border and monospace." [DET]
  - "Footer identical on every slide." [DET: footer region diff across slides]
  - "5th-grade reading level. No em dashes (titles, subtitles, hooks, caption, chat)." [DET]
- BECKY MATCH: none found.

### albert-dm (source: albert-dm/SKILL.md, voice-reference.md) - draft replies in Albert's DM voice (AI voice-agent sales)
- PURPOSE: write the next DM Albert would send to a prospect or client, in his casual, curious, no-pressure voice, steering to a call or demo.
- FUNCTIONALITY: read a pasted conversation -> identify the person and the stage (cold check-in, prospect already getting results, warm/interested, existing client) -> output 1-3 short lines, each a separate send. Loop used: validate -> probe -> recommend ("if I was you") -> optional low-pressure next step (call/demo).
- USE CASE FOR JORDAN: a template for drafting his own replies to comments and DMs in HIS voice (the same loop works for a creator audience: validate, ask one question, offer a next step). This is a voice-model pattern, not a sales tool to copy as-is.
- DECISION MODEL: LLM free-text (could be System One). Questions: (a) "Which stage is this conversation at: cold opener / prospect already doing it / warm and interested / existing client? (probabilities)"; (b) "Which move fits next: validate / probe / recommend / offer next step? (probabilities)"; (c) "Does this draft contain any hard-sell or pressure words? (yes/no)" (a guard).
- DETERMINISTIC PARTS: line count 1-3, lowercase ratio and no emoji unless the other person uses one, banned-word list (no "let's book a call now"; closes must contain "potentially" / "we can" / "I can" / "if I was you"), a "no dead-end reply" check (must end with a question or a next step), a banned-phrase check on invented product claims.
- DATA IT NEEDS: a voice reference (the 2 annotated transcripts), the pasted thread, the offer details (not invented).
- RULES WORTH COPYING:
  - "Short messages, often split across multiple sends. 2-4 short lines in a row, each one thought." [DET: send count]
  - "Always be advancing. Either ask a question that surfaces pain, validate + recommend, or offer a no-pressure next step. Don't send dead-end replies." [DET: ends with ? or next step]
  - "Closes are always optional-sounding: 'we can potentially...', 'I can make a demo', never 'let's book a call now'." [DET: keyword list]
  - "Never hard-sell or pressure." [DET: pressure-word list]
  - "Lots of questions to qualify and stay curious." [DET: question count >= 1]
  - "Don't invent product claims he wouldn't make." [AI]
  - "No emojis unless the other person is using them. No bullet points." [DET]
- BECKY MATCH: none found.

## C. Sales, outreach and lead generation

### cold-script (source: cold-script/SKILL.md, evals/evals.json) - cold call + DM follow-up script generator (another person's method, "Oliver Rasmussen")
- PURPOSE: write a 5-part cold call script (opener, 2-3 need questions, casual pitch, low-pressure close, soft objection handling) plus a DM follow-up, for any niche and country, per offer.
- FUNCTIONALITY: inputs country, niche, offer (one of 7 mapped offers or custom), optional city, caller name. Offers each have a pricing model (free-for-testimonial, upfront, performance-based) that decides whether price is mentioned. Website offers use 3 openers + one shared core. Voice-AI offer branches on a live answer (lost money vs lost time). Output: markdown script in a fixed structure, then DM split into separate messages.
- USE CASE FOR JORDAN: outbound for a service business. Low direct fit. The branching question logic is the useful part (pick the next question from the answer just given).
- DECISION MODEL: System One is a natural fit for the BRANCH step. Question: "Based on the prospect's answer to 'do you get phone calls while you're working?', which pain branch applies: A lost money (misses calls), B lost time (always answers), C neither? (probabilities)". Also: "Does this reply admit a pain (yes/no)?".
- DETERMINISTIC PARTS: banned-word check (zero hits on AI, automation, bot, software, smart, etc. in spoken lines), required phrases present ('Awesome.', 'No worries.', 'Totally fair.'), no em/en dashes, price-silence rule per offer (grep for price in spoken lines), booking sequence lines, DM message count (2 pitch messages, 1 per need question), city placeholder {city} when not given, output file name kebab-case.
- DATA IT NEEDS: niche list, city list, offer list with pricing model, call outcomes (to learn which opener converts).
- RULES WORTH COPYING:
  - "Never say AI." Banned: AI, artificial intelligence, chatbot, automation, bot, system, software, technology, algorithm, machine learning, smart, automated. [DET: word list]
  - "Price is silent unless required." Free = "free for testimonial"; performance = "no upfront, pay per booked appointment"; upfront = never mention price. [DET]
  - "Never promise numbers. No 'we will get you 20 calls a week'." [DET: digit check in spoken lines]
  - "Cold call is primary, DM is secondary." [process]
  - "Never use the word 'demo' with the prospect; say 'a whole website'." [DET: word check]
  - "Booking sequence: website offers 'Would today or tomorrow be better?'; others 'Could you see it tomorrow or the day after?'." [DET]
  - "Tone: confused and curious for the opener and need questions; confident only after a yes." [AI]
  - "Ask open baseline questions ('How do people usually...?') and never assume the answer; mirror their words." [AI]
  - "Owner language: 'came up higher', never 'ranked'." [DET: word list]
  - "No em dashes or en dashes in any output, including DMs." [DET]
  - "Each need question = separate DM message; pitch layup = 2 messages." [DET: message count]
- BECKY MATCH: none found.

### instantly-campaign (source: instantly-campaign/SKILL.md, voice-guide.md, winning-template.md) - 5-email cold-email sequence in a locked structure (Instantly API)
- PURPOSE: write a new 5-email cold outbound sequence that copies a winning campaign's structure (~16% reply on 278 leads) and create it in Instantly paused.
- FUNCTIONALITY: (1) interview (service, niche + role, a specific quantified pain, a tangible asset for email 3, a second angle for email 5, language, campaign name); (2) draft 5 emails from the winning template; (3) show draft and wait for approval; (4) create the campaign via the Instantly MCP with a locked settings payload (schedule 07:00-16:00 Mon-Fri, timezone Europe/Belgrade, daily limit 100, text only, tracking off, stop on reply) and status left paused.
- USE CASE FOR JORDAN: template for any outbound sequence. The "locked structure, changeable angle" idea is the transferable part.
- DECISION MODEL: LLM free-text for copy. System One candidate: "Is this pain hypothesis specific and quantifiable? (yes/no) - if not, push back once." That check is in the skill as a manual step.
- DETERMINISTIC PARTS: the locked schedule and delays (3/3/4/1/4 days), subject pattern (filled / empty / asset / empty / empty), 5 emails exactly, `{{companyName}}` as only variable, no links or images, line count per email (<= ~6 short lines), HTML `<div>` wrap, paused status, approval gate before any API call.
- DATA IT NEEDS: the winning campaign's reply stats, leads list, the offer text, the asset name.
- RULES WORTH COPYING:
  - "Never change the locked structure: 5 emails, delays 3/3/4/1/4, subject pattern filled/empty/asset/empty/empty, text-only, tracking off." [DET]
  - "Never invent stats; only numbers the user gave. Otherwise a hedged hypothesis." [DET+AI]
  - "CTAs are always soft questions, never 'Book a call' or 'Click here'." [DET: imperative-verb check]
  - "No links, no images, no signature, no logos (text-only deliverability)." [DET]
  - "Match the original's brevity: if an email runs more than ~6 short lines, cut it." [DET: line count]
  - "The campaign is created paused. Never set it active." [DET]
  - "Always show the draft and wait for approval before creating." [process gate]
- BECKY MATCH: none found.

### lead-scraper (source: lead-scraper/SKILL.md) - find local businesses in a niche + city, check each website for an existing chat/AI widget, export CSV
- PURPOSE: a prospect list of local businesses that do not yet have AI or chat on their site, ranked as hottest leads.
- FUNCTIONALITY: (1) ask niche + location; (2) WebSearch "[niche] in [location] site:google.com/maps" plus "[niche] near [location]", 20-50 results, extract name, phone, website, address, rating, review count; alternates "[niche] [city] phone number" or yelp; (3) WebFetch each website and look for chat widget scripts (Intercom, Drift, Zendesk, Tidio, LiveChat, Crisp, HubSpot chat), AI bots (Chatbase, BotPress, Voiceflow), Messenger; label `No AI/Chat`, `Has Chat`, `Has AI`; (4) export CSV `[niche]-[location]-leads.csv` with fixed columns, sorted no-AI first; (5) summary (total, no-AI count, top 5 by reviews with no AI).
- USE CASE FOR JORDAN: a generic prospecting tool. Low fit for a YouTuber, but the "check the site for a widget in the raw HTML" step is a simple deterministic signal pattern.
- DECISION MODEL: none in the scraper. Possible System One: "Is this site's chat element a live chat or only a 'contact us' link? (yes/no/unsure)" (the skill counts any script tag as chat).
- DETERMINISTIC PARTS: the whole check is deterministic once the page HTML is fetched: a vendor-name list matched against script src (Intercom, Drift, Tidio, etc.), CSV writing, sorting, de-duplication by domain, phone-number format check, rating/review parsing.
- DATA IT NEEDS: search results, websites, a vendor list (the chat-widget list is in the skill).
- RULES WORTH COPYING:
  - "Realistic expectation: 15-30 solid leads per scrape, not 50. Quality over quantity." [DET: count]
  - "A script tag for a chat vendor counts as Has Chat even if the widget is not rendered." [DET: string match]
  - "Retry a failed site at most once; a down site is still a lead." [DET]
  - "If fewer than 10 results come back, widen the radius or broaden the niche." [DET]
  - "Speed over perfection: a list of 25 with accurate status beats 50 after 10 minutes." [process]
- BECKY MATCH: none found.

### upwork-proposal (source: upwork-proposal/SKILL.md) - write one high-converting Upwork cover letter from a pasted job post
- PURPOSE: a 150-250 word proposal that mirrors the client's words, gives one number-backed proof, a 3-step mini-plan, one differentiator, and one scoping question.
- FUNCTIONALITY: (1) extract client problem quotes, tools named, vertical, budget signals, specifics; (2) check the freelancer facts the user has given (case study, one number, stack, differentiator, rate, sign-off) and ask ONCE in a consolidated message if missing, never invent; (3) draft with the 5-part formula; (4) output: proposal in a code block, word count, attachment recommendation, boost yes/no, pre-send checklist (word count < 250, attachment ready, applied within 60 min).
- USE CASE FOR JORDAN: only if he takes freelance work. Otherwise a reusable pattern (mirror the audience's words, one proof number, one question) for any pitch.
- DECISION MODEL: LLM free-text. System One candidates: (a) "Does the first sentence reference a specific phrase from the post? (yes/no)"; (b) "Which of the candidate proofs is the closest match to this client's vertical? (probabilities)"; (c) "Is the proposal 'desperate' or 'authoritative'? (probabilities)".
- DETERMINISTIC PARTS: word count (150-250, hard cap 300), no-emoji check, banned opener phrases ('Dear Hiring Manager', 'Hi there', 'I am'), superlative list ('passionate', 'world-class'), exactly one number in the proof, sign-off first name only, exactly one question at the end, no "let me know if you have questions" phrase.
- DATA IT NEEDS: job post text, the freelancer's real case studies and numbers (never invented), rate, stack.
- RULES WORTH COPYING:
  - "150-250 words. Hard cap 300. Count before delivering." [DET]
  - "First sentence must reference something specific from the job post." [DET: overlap with post]
  - "Use ONE number/result. Not a list." [DET: count of numerals in proof]
  - "Never start with 'I am', 'Dear Hiring Manager', 'Hi there', 'I hope this finds you well'." [DET]
  - "End with ONE question. Never 'let me know if you have questions'." [DET]
  - "No questions in the opener." [DET]
  - "Apply within 60 min of posting (response rates drop sharply after the first hour)." [DET: timestamp]
- BECKY MATCH: none found.

## D. Website, client-delivery and operations skills

### build-premium-website (source: build-premium-website/SKILL.md + reference/*.md) - animated React + Vite + Tailwind + GSAP marketing site, one per industry
- PURPOSE: a high-end single-page marketing site for any business, re-skinned per industry (colour, font, signature animation).
- FUNCTIONALITY: Phase 1 intake (about 4 question rounds: name, industry, tone, colours, 4-8 services, contact, trust signals, language, Unsplash hero terms, animation theme); Phase 2 scaffold Vite + React + Tailwind v3 + GSAP; Phase 3 nine sections in order (navbar, hero, features, pillars with count-up, sticky-stack protocol, services grid, trust badges, contact form, footer); Phase 4 signature animation re-skinned per industry (table of 12 industries -> shape + colours, e.g. plumbing teardrop, electrical spark, bakery flour mote); Phase 5 run the dev server, check at 375/768/1440 px, read console.
- USE CASE FOR JORDAN: a landing page for a product or a course (e.g. a creator's own site). Industry table is a template for "re-skin one system per theme".
- DECISION MODEL: none in the pipeline. Choice of tone and industry theme is human (could be System One: "Which of these 12 industry themes fits this business? (probabilities)").
- DETERMINISTIC PARTS: scaffold commands, config substitution, 9-section checklist, responsive checks at three widths, a banned-placeholder check (no lorem ipsum), console-error check, form state machine check (idle -> sending -> sent).
- DATA IT NEEDS: business copy and images (Unsplash search), brand colours.
- RULES WORTH COPYING: "Translate every string" (no leakage between industries) [DET: language check]; "Re-skin the signature animation; never ship water drops on a non-water business" [DET+AI]; "All 9 sections by default" [DET]; "Mobile-first, test at 375 px" [DET]; "Use real images, never placeholder boxes" [DET: no placeholder image]; "Don't write a README unless asked" [process].
- BECKY MATCH: none found.

### website-builder (source: website-builder/SKILL.md, 37 KB, v3.0) - one self-contained HTML agency site with researched real photos
- PURPOSE: one HTML file per local business that "looks like a $15,000 design firm", built from a URL or a description. Used as a sales demo for prospects (see cold-script).
- FUNCTIONALITY: Step 1 capture (URL via WebFetch or a text description; missing fields invented as realistic defaults, never lorem ipsum); Step 2 pick one of 7 personas by business type (trades, medical, real estate, gym, restaurant, law, default) with colour, font and feel; Step 3 MANDATORY photo discovery (at least 7 real photos researched per build, not reused stock); later steps: Ken Burns hero, glass cards, GSAP word-stagger, Lenis smooth scroll, count-up numbers, asymmetric gallery, 3-step process, Swiper testimonials; auto-open in the browser.
- USE CASE FOR JORDAN: a fast one-page site for a business or for his own product. The persona table (type -> palette/font) is a deterministic lookup.
- DECISION MODEL: LLM free-text for business type and tone. System One candidate: "Which of 7 personas fits this business type? (probabilities)" — a clean pick-one.
- DETERMINISTIC PARTS: persona lookup (keyword table with the cheatsheet overrides), colour hex validation, photo URL check (HTTP 200, image content type, minimum size), phone-number format, no-lorem check, HTML single-file check, open-in-browser.
- DATA IT NEEDS: business page or description, photos (Unsplash), reviews.
- RULES WORTH COPYING: "Every site must pass this test: a business owner sees it, you say it cost $15,000, they believe you." [AI]; "Photos come from discovery per business, never from the persona" [DET: unique photo hashes across builds]; "Never use placeholder text or lorem ipsum" [DET]; "Years in business: infer a confident realistic number (multi-year = 8-12)" [policy; DET: number present]; "Language follows the business (Swedish business gets Swedish copy)" [DET: language detect].
- BECKY MATCH: none found.

### composio (source: composio/SKILL.md, sdk-reference.md, auth-and-triggers.md) - build agent integrations with third-party apps (1000+ via Composio)
- PURPOSE: wire an AI agent to Gmail, Slack, GitHub, Notion, etc., with OAuth and event triggers.
- FUNCTIONALITY: create a session scoped to a user id with a list of toolkits -> native tools or an MCP URL; OAuth via connected-account initiate + wait; triggers (e.g. GitHub PR event, Gmail new email) that wake an agent; example workflows (email triage to Slack and Notion; PR monitor; multi-app loop).
- USE CASE FOR JORDAN: connecting his mail/calendar/Drive/Slack to an agent for posting, lead intake, or comment triage. Platform plumbing.
- DECISION MODEL: none in the platform. Agent logic is LLM free-text (could be System One for "classify this email: sponsor / fan / spam / business? (probabilities)").
- DETERMINISTIC PARTS: connected-account status check (ACTIVE/INITIATED/EXPIRED/FAILED/INACTIVE), tool name validation, user-id scoping, secret-in-env check.
- DATA IT NEEDS: account connections only.
- RULES WORTH COPYING: "Only ACTIVE connected accounts can execute tools" [DET]; "Use MCP mode for dynamic tool discovery (fewer tokens)" [process]; "Never hardcode API keys" [DET: no key literals].
- BECKY MATCH: none found.

### cost-reducer (source: cost-reducer/SKILL.md + 3 reference files) - cloud, infra and code cost cutting
- PURPOSE: cut running costs of apps and cloud infrastructure without hurting performance.
- FUNCTIONALITY: impact hierarchy (architecture, data routing, right-sizing, DB, caching, storage, bundles, observability); quick wins (S3 intelligent tiering, N+1 fix, WebP/AVIF, cache-aside, NAT gateway to VPC endpoints, log retention 7 days); red-flag checklist to scan code (N+1, missing index, `import *`, uncompressed images, no cache, 30-day logs, high-cardinality metrics, memory leaks, no lifecycle policy, provisioned concurrency everywhere).
- USE CASE FOR JORDAN: keeping his local AI stack and cloud bills small. Low priority for a creator, except for hosting/storage of video (lifecycle policies, log retention).
- DECISION MODEL: none. Possible: "Which of these services is the most expensive per unit of this workload? (probabilities)" (not needed in practice).
- DETERMINISTIC PARTS: all red-flag checks are static code/config scans (grep for `import *`, `findMany` in loops, missing lifecycle policy); cost arithmetic (GB x rate); log retention value check.
- DATA IT NEEDS: billing export, usage metrics, config files.
- RULES WORTH COPYING: "The cheapest code is code that doesn't run." [AI]; "Measure before cutting." [process]; "Optimize the biggest line item first." [DET: billing sort]; "Set TTLs and lifecycle policies" [DET].
- BECKY MATCH: none found.

### scalability (source: scalability/SKILL.md + 4 reference files) - software scaling guidance
- PURPOSE: keep systems fast as load grows; measure first.
- FUNCTIONALITY: bottleneck decision tree (DB > 50% of time -> indexes, N+1, pagination, pooling; slow external API -> cache or breaker; CPU -> workers or queue; memory -> streams, heap snapshots); quick wins (compound and partial indexes, eager loading).
- USE CASE FOR JORDAN: only when the becky/hj/WHORETANA services slow down. Engineering reference, not content functionality.
- DECISION MODEL: none.
- DETERMINISTIC PARTS: all of it (timing per phase, query plan, pool saturation, p95 latency).
- DATA IT NEEDS: request timings, query logs, profiler output.
- RULES WORTH COPYING: "Don't optimize what you haven't measured." [DET]; "Add the right index: equality fields first, then range, then sort." [DET]
- BECKY MATCH: none found.

### security (source: security/SKILL.md + 4 reference files) - web and desktop app security checklist
- PURPOSE: stop common exploits in web and Electron/Tauri apps.
- FUNCTIONALITY: non-negotiables list (no string-concatenated SQL/HTML/shell, no eval, no secrets in localStorage, no MD5/SHA1 passwords, crypto.randomBytes for tokens, HttpOnly/Secure/SameSite cookies, CSP default-src 'self', rate-limit auth, HSTS; Electron: contextIsolation + sandbox, no nodeIntegration, contextBridge with a minimal validated API, validate IPC sender, validate deep links, code-sign).
- USE CASE FOR JORDAN: any app he ships (e.g. the WHORETANA desktop shell, MissionControl IPC). Engineering reference.
- DECISION MODEL: none.
- DETERMINISTIC PARTS: nearly all: grep for eval/innerHTML/nodeIntegration/`remote`, header presence checks, dependency audit, secret scanning (regex for keys, tokens).
- DATA IT NEEDS: source code, dependency lists, config.
- RULES WORTH COPYING: "Every external input is hostile" [AI]; "Never enable nodeIntegration in renderer; never disable contextIsolation" [DET: config grep].
- BECKY MATCH: none found (security-scan tooling is separate).

### self-healing (source: self-healing/SKILL.md + 3 reference files) - the agent's memory-and-skill self-improvement loop
- PURPOSE: make each session smarter: save hard-won knowledge, fix stale memory, turn repeated workflows into skills.
- FUNCTIONALITY: observe (read MEMORY.md, existing skills, CLAUDE.md, recent work) -> decide (decision matrix: solved problem -> memory; user correction -> update memory; same workflow 2+ times -> new skill; stale -> delete) -> act (memory index under 200 lines, SKILL.md under 300 lines) -> verify (valid markdown, no duplicate facts, no secrets).
- USE CASE FOR JORDAN: this is the "watchdog / self-learning" family Jordan asked about. Note the skill itself is mostly prose rules ("read before writing", "never duplicate"), which the lessons file shows get ignored unless enforced by a hook. Per Jordan's brief, the deterministic parts (size limits, duplicate detection, frontmatter validation) should be a hook or check, not a prompt.
- DECISION MODEL: LLM free-text. System One candidates: "Is this fact already in memory? (yes/no/partly, with the matching file)" (dedupe); "Does this workflow repeat 2+ times? (yes/no)" (create-skill trigger); "Is this memory entry stale? (yes/no)".
- DETERMINISTIC PARTS: MEMORY.md line count (<= 200), SKILL.md line count (<= 300), frontmatter validity, duplicate detection by hash or fuzzy title match, secret regex scan before save, broken-link check in MEMORY.md, file-age stale check.
- DATA IT NEEDS: memory files, skills folder, session transcripts.
- RULES WORTH COPYING: "Only create a skill when a pattern has clear reuse value" [AI]; "Keep MEMORY.md under 200 lines" [DET]; "Never persist secrets" [DET: regex]; "User corrections override everything; update memory immediately" [process].
- BECKY MATCH: partial. becky has a lessons/rules flow; the memory-line and dedupe checks would be deterministic additions.

### know-me (source: know-me/SKILL.md + what-to-track.md, memory-operations.md) - remember user preferences across sessions
- PURPOSE: a personal memory layer that stores preferences, corrections and project context and applies them without being asked again.
- FUNCTIONALITY: listen for signals (direct statement, correction, repeated choice, frustration, project context); save to topic files (user-preferences, project-context, tech-stack, communication-style, corrections); recall before responding; apply; handle corrections (acknowledge, update, log to corrections.md); privacy (never save secrets; delete on "forget X").
- USE CASE FOR JORDAN: direct analogue of Jordan's own CLAUDE.md and memory rules. His existing rules (LESSONS list) already do this.
- DECISION MODEL: LLM free-text. System One candidate: "Is this a durable preference or a one-off task detail? (durable / one-off / unsure)" (the save gate).
- DETERMINISTIC PARTS: secret regex before save, topic-file routing by keyword, dedupe, delete-by-key on "forget".
- DATA IT NEEDS: conversation text (user statements, corrections).
- RULES WORTH COPYING: "Save after 2nd occurrence for a repeated choice" [DET: count]; "Never save temporary task context" [AI]; "Corrections -> corrections.md so the mistake never repeats" [process].
- BECKY MATCH: none found (Jordan's CLAUDE.md LESSONS covers this manually).

### researcher (source: researcher/SKILL.md) - multi-source deep research with a fixed report format
- PURPOSE: well-sourced research report on any topic.
- FUNCTIONALITY: scope into 3-5 sub-questions, parallel WebSearch with varied phrasing, WebFetch the best pages, cross-reference, follow-up searches for gaps, deliver TL;DR, findings with links, comparison table, recommendations, numbered sources. Strategy by research type (tech comparison, best practice, bug, architecture, library, concept).
- USE CASE FOR JORDAN: research on tools, trends or competitors for the roadmap (this task is an instance of it). Relevant as a method, but Jordan's brief requires the browser to be the agent Firefox only, not WebSearch-led browsing of live sites (see CLAUDE.md web-browsing rule).
- DECISION MODEL: LLM free-text. System One candidate: "Does this source directly support this claim? (supports / partly / no / not checked)" (claim-to-source check).
- DETERMINISTIC PARTS: source count (at least 3-5 searches), URL present for each claim, date of source (prefer last 1-2 years), duplicate-source detection, a check that every numbered source is cited in the text.
- DATA IT NEEDS: the web; for Jordan, the local youtube-notes and becky-docs collections first.
- RULES WORTH COPYING: "Always search before answering" [process]; "Cross-reference: a claim backed by multiple independent sources is stronger" [DET: count of independent domains]; "Prefer recent sources" [DET: date check].
- BECKY MATCH: none found for web research (qmd collections are the local equivalent).

### trigger-dev (source: trigger-dev/SKILL.md + 3 reference files) - background jobs, cron and queued AI workflows in TypeScript
- PURPOSE: long-running and scheduled background work (tasks, cron schedules, AI agent workflows).
- FUNCTIONALITY: task / schemaTask / schedules.task (cron) patterns; triggering from the backend (type-only import) and from inside a task (triggerAndWait); machine presets (micro to large-2x); idempotency keys; concurrency limits; retries and AbortTaskRunError for permanent failures; wait functions (free while waiting).
- USE CASE FOR JORDAN: scheduled jobs for his pipeline (e.g. nightly transcription, a "post-longform" step). It is the same job class as the Cubase factory and becky scheduling, so the patterns are directly relevant as a reference.
- DECISION MODEL: none in the platform.
- DETERMINISTIC PARTS: the whole scheduling, retry and concurrency layer; payload JSON-serialisable check; tag count (max 10) and metadata size (256 KB) checks.
- DATA IT NEEDS: job payloads, schedules.
- RULES WORTH COPYING: "Payloads and return values must be JSON serialisable" [DET]; "Always export tasks" [DET]; "Use AbortTaskRunError to fail without retrying on permanent errors" [DET]; "Max 10 tags per run, 256 KB metadata, 1000 items per batch" [DET].
- BECKY MATCH: partial. The becky scheduling and Cubase factory are the local equivalent. A grep of becky-tools INDEX.md, README.md and SKILL.md for cron/schedule terms showed no direct Trigger.dev-style tool (output was capped at 60 lines per file, so this is not a full search).

### n8n (source: n8n/SKILL.md + 3 reference files) - n8n workflow JSON, triggers and custom nodes
- PURPOSE: build n8n automations (webhook, schedule, form, email, workflow trigger) and custom nodes.
- FUNCTIONALITY: workflow JSON structure (nodes, connections, settings), trigger types table, self-hosted Docker run, custom node scaffolding (`npx n8n-node-dev new`), REST API for managing workflows and executions.
- USE CASE FOR JORDAN: no-code glue between his tools (e.g. a YouTube upload trigger that runs the post-longform checklist). Jordan is not a developer; n8n is a visual route for him if he wants it, but becky-tools is his chosen system.
- DECISION MODEL: none in the platform. LLM steps could be System One.
- DETERMINISTIC PARTS: all of it (triggers, routing, retries, error paths).
- DATA IT NEEDS: webhook payloads, schedules.
- RULES WORTH COPYING: none specific to content.
- BECKY MATCH: none found. Overlaps with becky/Cubase factory orchestration.

### new-client-system (source: new-client-system/SKILL.md + references + templates/) - scaffold a client's Next.js dashboard + Trigger.dev backend from a template
- PURPOSE: start a client project quickly: auth-restricted dashboard, design system, integration clients, empty automation registry.
- FUNCTIONALITY: gather client name, email domain, output folder (one question round); derive slug, from-email, billing email; run scaffold.sh (copy templates, substitute 7 placeholders, refuse overwrite); verify no leftover placeholders and valid package.json; list next steps (npm install, Composio connect, dev server).
- USE CASE FOR JORDAN: only if he takes clients. Otherwise a reference for a creator dashboard.
- DECISION MODEL: none.
- DETERMINISTIC PARTS: the whole scaffold (placeholder substitution, overwrite refusal, JSON parse, placeholder grep).
- DATA IT NEEDS: client parameters.
- RULES WORTH COPYING: "Refuse to overwrite existing folders." [DET]; "Do not run npm install automatically; list it." [process]; "Both projects share one Trigger.dev project ref and secret." [DET: env-var match check].
- BECKY MATCH: none found.

### customer-support (source: customer-support/SKILL.md + response-templates.md, escalation-guide.md) - support reply drafting and ticket triage
- PURPOSE: polite, fast support replies and ticket analysis.
- FUNCTIONALITY: response structure (greeting, acknowledgment, explanation, next step, close); tone table by customer state (frustrated, confused, neutral, happy, escalating); ticket analysis (summary, sentiment, root cause, reply, prevention, tags); help-article format (action title, numbered steps, troubleshooting, related links).
- USE CASE FOR JORDAN: replies to viewer or sponsor emails and community questions, in his voice. A pattern for comment triage (see albert-dm for the voice side).
- DECISION MODEL: LLM free-text. System One candidates: "Customer sentiment: frustrated / confused / neutral / happy / escalating (probabilities)"; "Escalate? (yes/no) with the routing bucket (billing / bug / outage / feature)".
- DETERMINISTIC PARTS: required sections present in a reply, no internal-tooling words leaked, no timeline promises beyond an SLA window, tag from a fixed list, next step present.
- DATA IT NEEDS: tickets, comments, response templates.
- RULES WORTH COPYING: "Acknowledge first, solve second." [AI]; "Never promise timelines you cannot guarantee." [DET: date/time regex]; "Never blame the customer." [AI]; "When unsure, escalate." [AI].
- BECKY MATCH: none found.

### frontend-design (source: frontend-design/SKILL.md) - guidance for distinctive, non-generic frontends
- PURPOSE: force a bold design direction and avoid generic "AI slop" UI (no Inter/Roboto/Arial, no purple gradient on white, no cookie-cutter layouts).
- FUNCTIONALITY: pick an extreme tone (brutalist, retro-futuristic, editorial, luxury, etc.), define purpose / constraints / differentiator, then implement HTML/CSS/JS or React with CSS variables, one orchestrated page-load stagger, and layered backgrounds.
- USE CASE FOR JORDAN: the aesthetic rules themselves (no default fonts, dominant colour + sharp accent, one staggered reveal) apply to his thumbnails, carousels and popups.
- DECISION MODEL: none in the skill. A System One check could score a rendered page against the "no default font / no purple gradient" list.
- DETERMINISTIC PARTS: font-family list check (banned default fonts), colour count (dominant + one accent), animation count on load, contrast ratio.
- DATA IT NEEDS: none beyond the brief.
- RULES WORTH COPYING: "Dominant colours with sharp accents outperform timid, evenly-distributed palettes." [DET: palette share]; "Avoid Inter, Roboto, Arial, system fonts." [DET]; "Pair a distinctive display font with a refined body font." [DET: two families]; "One well-orchestrated page load with staggered reveals beats scattered micro-interactions." [DET: count of animated elements].
- BECKY MATCH: none found.

### create-skill (source: create-skill/SKILL.md + reference.md, examples.md) - how to write a new Claude Code skill
- PURPOSE: produce a well-formed skill or slash command.
- FUNCTIONALITY: choose type (task, research, knowledge, dynamic), scope (personal vs project), frontmatter (name kebab-case, action-oriented description with trigger phrases, argument-hint, invocation control fields), write SKILL.md, add reference files.
- USE CASE FOR JORDAN: the mechanism by which the roadmap becomes skills. Relevant only as a process.
- DECISION MODEL: none.
- DETERMINISTIC PARTS: frontmatter validation (name kebab-case, description present and under the limit), folder structure, line limits.
- DATA IT NEEDS: none.
- RULES WORTH COPYING: "Good description: says what it does AND the trigger phrases; bad description: 'X helper'." [DET: description length and trigger-phrase count].
- BECKY MATCH: none found.

### setup-codex-precheck (source: setup-codex-precheck/SKILL.md, install.sh, codex-precheck.py) - install a per-project codex review gate on every edit
- PURPOSE: a PreToolUse hook that sends each Edit/Write to the codex CLI for APPROVE or BLOCK before writing (fail-open if codex is missing).
- FUNCTIONALITY: idempotent installer merges a hook into `.claude/settings.json`, appends a policy to CLAUDE.md, writes an audit log (`codex-precheck.log`: APPROVE / BLOCK / CACHE_HIT / SKIP) and a sha256 cache.
- USE CASE FOR JORDAN: a second-opinion gate on code edits. Note: the skill itself says it adds latency to every edit (up to 120 s per hook call), and the cheaper option is an end-of-task Stop hook.
- DECISION MODEL: this IS a judgement gate. Replacement candidate: System One "Is this proposed edit risky? (safe / needs-review / block, with probabilities)" - faster and calibrated, and it can be deterministic for the easy cases (no delete, no secret, no file outside repo).
- DETERMINISTIC PARTS: the hook plumbing, the cache, the audit log, the fail-open path, the secret pattern checks, the "only stderr on non-zero exit" logged-out detection.
- DATA IT NEEDS: the proposed diff.
- RULES WORTH COPYING: "Fail open: a logged-out reviewer must allow edits with a warning, never brick editing." [DET]; "Never clobber settings or CLAUDE.md; merge or append." [DET].
- BECKY MATCH: none found (the becky/factory uses its own hooks).

### upwork (source: upwork/SKILL.md, patterns.md, examples.md) - audit and rewrite a freelancer profile
- PURPOSE: improve headline, overview, skills tags, project catalog, consultation offer and testimonials on Upwork.
- FUNCTIONALITY: audit (3-5 weak points), rewrite in 3 variants (authoritative, friendly-expert, direct-response), explain why; headline formula (credibility marker first, <= 70 characters); project catalog of 3-4 fixed-price packages ($30-150 entry, $1-2K standard, $7-8K premium); consultation $55-200 for 30 min.
- USE CASE FOR JORDAN: only if he sells freelance services. Reference for "how to package an offer".
- DECISION MODEL: LLM free-text. System One candidate: "Is this headline's first 60 characters a credibility marker? (yes/no)" (a 1-line deterministic check too).
- DETERMINISTIC PARTS: headline length (<= 70 characters), first-60-character credibility marker present (keyword list), presence of a catalog and a CTA, skill tag count (<= 15), "never invent metrics" check (every dollar figure matched to a user-provided fact).
- DATA IT NEEDS: the real profile text and real results from the user.
- RULES WORTH COPYING: "Specificity beats adjectives: $2.3M saved beats lots of savings." [DET: numeral present]; "Never write 'experienced' without a number." [DET]; "Stack the proof: name brands when allowed." [DET+AI]; "Anti-patterns: 'Available 24/7' reads as desperate." [DET: phrase list].
- BECKY MATCH: none found.

### Plumbing (no content functionality; listed for completeness, not for the roadmap)
- setup-codex-precheck is in the block above (it is a hook, not content).
- `youtube-popup-graphic/youtube-clean/` = duplicate of `youtube-clean/` (byte-identical files).
- `.DS_Store`, `.venv/` folders (capcut-smartcut/scripts/.venv, python 3.14 packages), `__pycache__`, `template/assets` (fonts, icons, PNG/MP3 sounds) are not functionality.

## E. Not read in full (honest gaps for this pass)
- `.docx` files (Explanation.docx in several folders, "Create Skill.docx", "Untitled document.docx"): binary Word files, not extracted. They are most likely write-ups of the SKILL.md files.
- Reference markdown only skimmed or not opened: albert-reel-white-screen/references/card-snippets.md and animation-library.md (headings only), instantly-campaign/voice-guide.md and winning-template.md, cold-script/evals/evals.json, upwork/patterns.md and examples.md, the build-premium-website reference/ folder (headings not listed), website-builder SKILL.md past line 90 (persona + photo protocol body only partly read), composio/sdk-reference.md + auth-and-triggers.md, cost-reducer and scalability/security sub-references, self-healing/memory-management.md + pattern-recognition.md + skill-creation-guide.md, customer-support templates, know-me/what-to-track.md, n8n reference files, new-client-system/references/*.md (templates only), create-skill examples.md, trigger-dev reference files.
- Python scripts were not read line by line (their behaviour is taken from the SKILL.md descriptions and the lesson files). Scripts that matter most for Jordan if he wants the deterministic gates: albert-reel-dark/template/check_air.py, check_motion.py, check_frames.py, check_provenance.py; youtube-clean/scripts/said_twice_check.py, build_sections.py, verify_sync.py, verify_final_sync.py; albert-reel-white-screen/scripts/zoom_review.py.
- No .env file was opened (six exist: albert-reel, albert-reel-white, albert-reel-white-screen, famous-reel-editor, new-client-system templates backend/.env.example and frontend/.env.local.example). Their names only were listed.



---

