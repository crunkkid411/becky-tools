# waterboxdeveloper/CliperAi-

Source: https://github.com/waterboxdeveloper/CliperAi- | licence: not stated

Read for `shorts-user-feedback.md`: what step does this run that becky does not?
Written by a free model reading the source; the build/skip judgement is not its call.

---

## What it is - 2-3 sentences about input/output
2. ## The pipeline, in order - numbered list of actual stages in code execution order, with function/file names
3. ## Models, libraries and services - table of what it uses, for which stage, local or paid API
4. ## Prompts - verbatim LLM prompts in fenced blocks, or say if none
5. ## How it decides WHAT to clip - selection/ranking logic, thresholds, scores, heuristics, or "it asks an LLM and trusts the answer", name the file
6. ## How it decides framing / cropping - same treatment, if it does not reframe, say so plainly
7. ## Multi-pass or iteration - does anything run more than once, re-check output, or refine? Most don't, if so say so
8. ## Steps here that a transcript-first clipper would MISS - bullet list, specific and mechanical
9. ## Worth stealing - bullets ranked, concrete items with licence if seen, or say "no" if nothing worth stealing

Let me go through the files to extract the actual information.

From README.md:
- CLIPER takes long-form video and generates optimized social media clips
- It has stages: transcription, semantic understanding, segmentation, caption generation, quality validation, computer vision reframing, video engineering & export
- It uses WhisperX for transcription, ClipsAI for text tiling with BERT, Gemini/Claude for captions
- It's a CLI tool, Python-based, uses FFmpeg
- MIT License

From cliper.py:
- Main entry point
- Orchestrates: download → transcribe → generate clips → resize
- Has modules: downloader, transcriber, clips_generator, video_exporter, copys_generator, cleanup_manager, local_importer
- Uses YouTubeDownloader, Transcriber, ClipsGenerator, VideoExporter, generate_copys_for_video, CleanupManager, LocalVideoImporter
- Has OperationProgress class for progress tracking
- Has escanear_videos, mostrar_videos_disponibles, menu_principal functions
- opcion_agregar_video function for adding videos

From src/transcriber.py:
- Transcriber class using WhisperX
- transcribe() method: extracts audio with ffmpeg, transcribes with WhisperX, aligns timestamps, saves JSON
- Uses whisperx.load_model, whisperx.load_align_model, whisperx.align
- get_transcript_summary() method
- transcribe_video() helper function

From src/clips_generator.py:
- ClipsGenerator class
- generate_clips() method: loads transcript, converts to ClipsAI format, uses clip_finder.find_clips()
- If no clips found, falls back to _generate_fixed_time_clips()
- _convert_to_clipsai_format() method converting WhisperX data to ClipsAI Transcription format
- Uses clipsai.ClipFinder, clipsai.Transcription
- min_clip_duration=30, max_clip_duration=90 default

From test_dual_llm_support.py:
- Tests CopysGenerator with Gemini and Claude
- Shows it supports both providers

From src/subtitle_generator.py:
- SubtitleGenerator class
- generate_srt_from_transcript() method
- generate_srt_for_clip() method
- extract_opening_words_from_clip() method
- extract_speaker_hashtags() method
- _create_srt_entries() helper

From docker-compose.example.yml:
- Shows environment variables: GOOGLE_API_KEY, WHISPER_MODEL, etc.
- Uses cliper Python project
- Has volume mounts

From tests/diagnose_subtitles.py:
- Diagnoses subtitle duplication issues
- Uses ffprobe to inspect SRT files

From tests/spike_face_reframing.py:
- MediaPipe face detection validation spike
- Temporary file that will be deleted after validation
- Uses mediapipe.face_detection

From tests/diagnose_subtitle_duplication.py:
- Diagnoses bug of duplicate subtitles
- Creates test video, generates subtitles, exports with logo+subtitles
- Uses VideoExporter and SubtitleGenerator

From src/prompts/__init__.py:
- Prompts module for AI copy generation
- Styles: viral, educational, storytelling
- Spanish and English prompts
- get_prompt_for_style() function

From src/prompts/base_prompts.py:
- SYSTEM_PROMPT base for Gemini
- JSON_FORMAT_INSTRUCTIONS
- build_base_system_prompt() helper
- Rules about 150 character limit, code-switching, mandatory hashtags, etc.

From src/utils/prompt_injector.py:
- inject_channel_config() function to replace placeholders like {mandatory_hashtags}

From src/prompts/viral_prompt_es.py:
- VIRAL_STYLE_PROMPT_ES constant
- get_viral_prompt_es() function
- Emphasizes that copy must begin with exact opening words from speaker
- Code-switching rules (Spanish verbs, English technical terms)
- Hashtag rules, engagement metrics

Now let me organize this into the required sections.

## What it is
CLIPER takes a long-form video (YouTube URL or local file) and processes it through an AI pipeline to generate optimized short-form clips for social media platforms. It outputs publication-ready vertical video clips with synchronized subtitles, captions, and optimized framing.

## The pipeline, in order
Looking at cliper.py, the main flow is:
1. Download video (YouDownloader or LocalVideoImporter)
2. Transcribe (Transcriber using WhisperX)
3. Generate clips (ClipsGenerator using ClipsAI TextTiling with BERT embeddings)
4. Generate captions/copys (generate_copys_for_video using LLM)
5. Export video (VideoExporter combining all elements)

But let me check the actual execution order more carefully. Looking at cliper.py, the main function isn't fully shown, but there are references to the pipeline stages. The README shows a pipeline diagram:

Long-Form Video → Transcription & Analysis → Semantic Understanding → Intelligent Segmentation → Agentic Caption Generation → Quality Validation → Computer Vision Optimization → Video Engineering & Export → Publication-Ready Clips

But I need to report what the actual code shows, not just the README. Let me look at cliper.py more carefully.

From cliper.py, I see these modules are imported and used:
- YoutubeDownloader or LocalVideoImporter (for download)
- Transcriber (for transcription)
- ClipsGenerator (for clip segmentation)
- VideoExporter (for final export)
- generate_copys_for_video (for captions)
- CleanupManager
- get_state_manager

The execution flow seems to be:
1. User selects a video
2. It gets downloaded/imported
3. Transcribed using WhisperX
4. Clips are generated using ClipsAI
5. Captions are generated using LLM (Gemini/Claude)
6. Video is exported with subtitles and optimization

But I need to be precise about the actual code execution order. Let me look for a main() function or the primary orchestration.

Actually, looking at cliper.py, I don't see a complete main() function that shows the full pipeline execution order. The file has helper functions and the OperationProgress class, but the actual pipeline orchestration might be elsewhere or the main flow is described in the README.

Let me check if there's more in cliper.py... It seems the file ends with the menu_principal function and opcion_agregar_video. The actual pipeline execution might happen elsewhere or through a different entry point.

Given the instructions to report "only what the source actually shows" and "be concrete", I should list the stages that are clearly identifiable from the code.

From the code, I can identify these stages:
1. Video download/import (YoutubeDownloader/LocalVideoImporter)
2. Transcription (Transcriber with WhisperX)
3. Clip generation (ClipsGenerator with ClipsAI)
4. Caption generation (generate_copys_for_video)
5. Video export (VideoExporter)

But I need to determine the actual execution order from the code. Let me look more carefully.

Actually, looking at the README's pipeline diagram and the description in cliper.py comments, the order seems to be:
1. Download video
2. Transcribe audio
3. Generate clips (detect topic shifts)
4. Generate captions/copys
5. Export video with subtitles and optimization

But I should only include what's actually in the files read. Let me be more careful.

From src/transcriber.py: The transcribe() method extracts audio, runs WhisperX, aligns timestamps, saves JSON.

From src/clips_generator.py: The generate_clips() method loads transcript, converts to ClipsAI format, uses clip_finder.find_clips(), falls back to fixed time clips if needed.

From src/copys_generator.py (referenced but not fully shown): generate_copys_for_video function.

From src/video_exporter.py (referenced but not shown in the excerpts): VideoExporter class.

Actually, I don't see the full video_exporter.py in the provided files. Let me check what files were provided... Looking at the FILE list, I see:
- README.md
- cliper.py
- test_dual_llm_support.py
- src/transcriber.py
- src/clips_generator.py
- src/subtitle_generator.py
- docker-compose.example.yml
- tests/diagnose_subtitles.py
- tests/spike_face_reframing.py
- tests/diagnose_subtitle_duplication.py
- src/prompts/__init__.py
- src/prompts/base_prompts.py
- src/utils/prompt_injector.py
- src/prompts/viral_prompt_es.py

I don't see src/video_exporter.py or src/copys_generator.py in the file list, though they're imported in cliper.py. The user said "If something is not in the files you were given, write 'not visible in the files read'".

So for the pipeline stages, I should only include what's actually visible in the provided files.

From what I can see:
1. Video download/import - mentioned in cliper.py (YoutubeDownloader, LocalVideoImporter)
2. Transcription - visible in src/transcriber.py
3. Clip generation - visible in src/clips_generator.py
4. Caption generation - referenced via generate_copys_for_video in cliper.py, but the actual source isn't fully provided (though test_dual_llm_support.py shows some CopysGenerator code)
5. Video export - referenced in cliper.py but src/video_exporter.py not in file list

Let me be careful to only include what's actually in the files.

Actually, re-reading the user's request: "Report only what the source actually shows." and "If something is not in the files you were given, write 'not visible in the files read' - never guess, never pad, never write marketing copy."

So for stages not visible in the files, I should note that.

Let me now think about the pipeline in order based on what's visible:

From cliper.py imports and descriptions:
- YoutubeDownloader or LocalVideoImporter for getting the video
- Transcriber for transcription
- ClipsGenerator for clip segmentation  
- generate_copys_for_video for captions (though the actual module isn't fully provided)
- VideoExporter for final export (module not fully provided)

From src/transcriber.py: transcription stage
From src/clips_generator.py: clip generation stage

The execution order from the code flow seems to be:
1. Download/import video
2. Transcribe video
3. Generate clips from transcript
4. Generate captions/copys
5. Export video

But since I can't see the full orchestration in cliper.py's main flow (only helper functions), I should describe the stages as they appear in the code, noting what's visible.

Actually, let me re-read cliper.py more carefully. It has functions like escanear_videos, mostrar_videos_disponibles, menu_principal, opcion_agregar_video. These seem to be the CLI interface, but the actual pipeline execution might be in those functions or called from them.

Looking at opcion_agregar_video, it seems to handle adding videos but I don't see the full pipeline execution there either.

Given the constraint to only report what's actually shown, I'll list the stages that are clearly identifiable from the code, in the order they appear to be executed based on the function calls and imports.

Let me now determine the actual pipeline stages from the code:

1. Video acquisition: YouTubeDownloader or LocalVideoImporter (from cliper.py imports)
2. Transcription: Transcriber class from src/transcriber.py (uses WhisperX)
3. Clip generation: ClipsGenerator class from src/clips_generator.py (uses ClipsAI TextTiling with BERT)
4. Caption generation: generate_copys_for_video function (imported in cliper.py, but the actual source isn't fully provided - though test_dual_llm_support.py shows CopysGenerator)
5. Video export: VideoExporter class (imported in cliper.py, but src/video_exporter.py not in file list)

For the pipeline order, based on the README and the function dependencies:
- You need the video first (stage 1)
- Then you transcribe it (stage 2)
- Then you can generate clips from the transcript (stage 3)
- Then generate captions (stage 4)
- Then export the video (stage 5)

But I should only include what's visible. Let me be more precise.

Actually, I think the best approach is to list the stages as they're implemented in the code files, in the order they would be executed based on the code flow.

From what I can see:

Stage 1: Video download/import - The code has YoutubeDownloader and LocalVideoImporter modules imported in cliper.py. The opcion_agregar_video function handles user choice between YouTube and local import.

Stage 2: Transcription - Clearly implemented in src/transcriber.py. The Transcriber class has a transcribe() method that's called with a video path.

Stage 3: Clip generation - Implemented in src/clips_generator.py. The ClipsGenerator class has a generate_clips() method that takes a transcript path and returns clip timestamps.

Stage 4: Caption generation - Referenced via generate_copys_for_video in cliper.py. The actual module isn't fully provided, but test_dual_llm_support.py shows CopysGenerator which supports Gemini and Claude.

Stage 5: Video export - Referenced via VideoExporter in cliper.py, but the actual source file isn't in the provided files.

For the "pipeline, in order" section, I should list the actual stages in execution order as shown by the code. Since I don't have a complete main() function showing the exact execution order, I'll list the stages based on the dependencies and the order they'd logically be called.

Let me now think about what's most concrete and accurate.

From the code, the clearest pipeline I can identify is:

1. Video download/import (YoutubeDownloader/LocalVideoImporter) - from cliper.py
2. Transcription with WhisperX (Transcriber) - from src/transcriber.py
3. Clip segmentation with ClipsAI (ClipsGenerator) - from src/clips_generator.py
4. Caption generation with LLM (generate_copys_for_video/CopysGenerator) - referenced but full source not provided
5. Video export with subtitles and optimization (VideoExporter) - referenced but full source not provided

But since stages 4 and 5's full source isn't in the files, I should note that or only include what's visible.

Actually, re-reading the user's instruction: "Use exactly these sections" and "Report only what the source actually shows." So I need to include all sections but only put what's actually in the files.

For stages not visible, I should probably still list them but note what's visible and what isn't, or use "not visible in the files read" where appropriate.

Let me now move on to other sections and gather all the information, then format the output.

## Models, libraries and services

From the files, I can identify:

Libraries used:
- whisperx (for transcription, local)
- clipsai (for clip finding, seems to be a library)
- google-generativeai or similar (for Gemini, paid API)
- mediapipe (for face detection, in spike test file)
- mediapipe/face_detection (in tests/spike_face_reframing.py)
- mediapipe is likely local
- ffmpeg (for audio extraction, mentioned in transcriber.py)
- pytorch/torch (mentioned in transcriber.py)
- transformers (mentioned in cliper.py startup log suppression)
- rich (for CLI interface)
- json, re, pathlib, typing (standard Python)

Specific models:
- Whisper models: tiny, base, small, medium, large-v2 (from transcriber.py)
- Gemini models: gemini-2.0-flash-exp (from test_dual_llm_support.py and docker-compose)
- Claude models: claude-3-5-sonnet-20241022 (from test_dual_llm_support.py)

Services:
- Google Gemini API (paid, requires GOOGLE_API_KEY) - mentioned in docker-compose.example.yml and test_dual_llm_support.py
- WhisperX models run locally

From docker-compose.example.yml:
- GOOGLE_API_KEY: required for AI caption generation
- WHISPER_MODEL: can be set (default base)
- Other API keys and settings

From test_dual_llm_support.py:
- Supports both Gemini and Claude
- Gemini: fast, good for creative outputs
- Claude: better reasoning, larger context window

From src/prompts/base_prompts.py:
- SYSTEM_PROMPT for Gemini
- Uses code-switching (Spanish + English)

## Prompts

The user wants verbatim LLM prompts found in the source. Let me check what's actually there.

From src/prompts/base_prompts.py:
- SYSTEM_PROMPT constant with detailed instructions
- JSON_FORMAT_INSTRUCTIONS
- build_base_system_prompt() function

From src/prompts/viral_prompt_es.py:
- VIRAL_STYLE_PROMPT_ES constant
- get_viral_prompt_es() function

These are prompts used for AI copy generation. The user wants "Quote verbatim any LLM prompt found in the source, in a fenced block. If there are none, say so."

So I should include these prompts verbatim. Let me check if they're full LLM prompts or just parts.

From base_prompts.py, SYSTEM_PROMPT is a long string with detailed instructions for the LLM. This appears to be a full system prompt.

From viral_prompt_es.py, VIRAL_STYLE_PROMPT_ES is another prompt string.

These should be quoted verbatim in fenced blocks.

## How it decides WHAT to clip

From src/clips_generator.py:
- The ClipsGenerator uses ClipsAI's ClipFinder to detect topic shifts
- It uses TextTiling algorithm enhanced with BERT embeddings (from README)
- If no clips found, it falls back to fixed-time clips
- Parameters: min_clip_duration=30s, max_clip_duration=90s
- The clip_finder.find_clips() method detects changes of theme

From the code: "ClipsAI analyzes the semantic structure of your content to identify optimal clip boundaries. This ensures each generated clip is a self-contained narrative unit rather than a random cut."

The selection logic: clip_finder.find_clips(clipsai_transcript) returns clips with start_time and end_time. If no clips found, falls back to _generate_fixed_time_clips() which divides video into clip_duration segments.

## How it decides framing / cropping

From the README and other files, but I need to check what's actually in the provided files.

Looking at the files, I don't see a full video_exporter.py or a dedicated reframing module in the provided files. The spike_face_reframing.py tests MediaPipe face detection but is described as "TEMPORARY - This file will be deleted after validating results".

From the README: "Stage 6: Computer Vision & ML-Based Reframing" describes converting horizontal to vertical using ML-based solution that detects key visual elements.

But since the actual video_exporter.py isn't in the provided files, and the spike test is temporary, I need to be careful.

Actually, looking more carefully at the files provided, I don't see a complete implementation of framing/cropping logic. The spike_face_reframing.py is a validation spike, not production code.

From what I can see:
- The README mentions computer vision reframing
- The spike test uses MediaPipe for face detection
- But the actual production framing/cropping code isn't fully in the provided files

I should report what's actually visible. If the information isn't in the files, I should say "not visible in the files read".

## Multi-pass or iteration

From the code:
- ClipsGenerator has a fallback mechanism: if ClipsAI doesn't find clips, it falls back to fixed-time clips
- The transcriber doesn't seem to have iteration
- The copys generation might have multiple attempts (from test_dual_llm_support.py mentioning MAX_COPY_GENERATION_ATTEMPTS=3 in docker-compose)

From docker-compose.example.yml: MAX_COPY_GENERATION_ATTEMPTS: ${MAX_COPY_GENERATION_ATTEMPTS:-3}

From the code flow, it seems like there's at least one fallback (ClipsAI → fixed time), but I need to check if anything re-runs or re-checks its output.

From src/clips_generator.py: The generate_clips() method, if no clips found, falls back to fixed-time clips. That's a single fallback, not really "multi-pass" or "iteration" in the sense of re-checking output.

From the overall pipeline, there's no indication of running stages multiple times or re-checking output, except the single fallback in clips generation.

## Steps here that a transcript-first clipper would MISS

This requires thinking about what CLIPER does that a simple transcript-first clipper wouldn't.

From the code:
- ClipsAI uses TextTiling with BERT embeddings to detect topic shifts, not just fixed time intervals
- It extracts opening words from the speaker's actual words (from src/subtitle_generator.py extract_opening_words_from_clip method)
- It uses code-switching in captions (Spanish verbs + English technical terms)
- It extracts speaker hashtags from the clip text
- It has quality validation (from README)
- It does computer vision reframing (from README, though not fully in provided files)
- It synchronizes subtitles precisely with word-level timestamps

A transcript-first clipper that just chops video into fixed intervals would miss:
- Topic-shift-based clip boundaries
- Opening words extraction for authenticity
- Speaker's actual hashtags preservation
- Code-switched caption generation
- Quality validation against brand guidelines

## Worth stealing

Concrete items from the code that could be valuable:

1. WhisperX transcription with word-level timestamps (src/transcriber.py) - MIT License per README
2. ClipsAI TextTiling with BERT embeddings for topic shift detection (src/clips_generator.py)
3. The opening words extraction method (src/subtitle_generator.py extract_opening_words_from_clip)
4. The code-switching prompt system (src/prompts/base_prompts.py and viral_prompt_es.py)
5. The prompt injector (src/utils/prompt_injector.py)
6. The dual LLM support (test_dual_llm_support.py shows Gemini + Claude)
7. The SRT generation with smart word grouping (src/subtitle_generator.py _create_srt_entries)
8. The fallback from topic-based to fixed-time clips (src/clips_generator.py _generate_fixed_time_clips)

Licensing: README says MIT License. Individual files may have different licenses, but I didn't see explicit licenses for each file except the overall MIT mentioned in README.

Now let me organize all this into the required format.

Let me double-check each section:

## What it is
Two or three sentences. What does it take in, what does it emit.

CLIPER takes a long-form video (YouTube URL or local file) as input and processes it through an AI pipeline to generate optimized vertical video clips for social media platforms. It outputs publication-ready clips with synchronized subtitles, AI-reasoned captions, and computer-vision-optimized framing.

## The pipeline, in order
A numbered list of the ACTUAL stages in the code, in execution order. For each stage name the function or file that implements it.

Based on my analysis, the stages visible in the code:

1. Video download/import - YouTubeDownloader or LocalVideoImporter (src/modules in cliper.py)
2. Transcription - Transcriber class from src/transcriber.py (uses WhisperX)
3. Clip generation - ClipsGenerator class from src/clips_generator.py (uses ClipsAI TextTiling with BERT)
4. Caption generation - generate_copys_for_video function (imported in cliper.py, CopysGenerator shown in test_dual_llm_support.py)
5. Video export - VideoExporter class (imported in cliper.py, but src/video_exporter.py not in provided files)

Wait, I need to be more careful. The user said "Report only what the source actually shows." So I should only include stages that are actually implemented in the files I was given.

Let me re-examine what's actually in the files for pipeline stages:

From cliper.py:
- Imports: YoutubeDownloader, Transcriber, ClipsGenerator, VideoExporter, generate_copys_for_video, CleanupManager, LocalVideoImporter
- Functions: escanear_videos, mostrar_videos_disponibles, menu_principal, opcion_agregar_video

From src/transcriber.py: Full Transcriber class with transcribe() method

From src/clips_generator.py: Full ClipsGenerator class with generate_clips() method

The other modules (VideoExporter, generate_copys_for_video) are imported but their full source isn't in the provided files.

So for the pipeline stages, I should list what's actually implemented in the visible files:

1. Video acquisition: YouTubeDownloader/LocalVideoImporter (from cliper.py) - this is visible
2. Transcription: Transcriber from src/transcriber.py - this is fully visible
3. Clip generation: ClipsGenerator from src/clips_generator.py - this is fully visible
4. Caption generation: generate_copys_for_video (referenced but full source not visible) - I should note this
5. Video export: VideoExporter (referenced but full source not visible) - I should note this

But the user wants "A numbered list of the ACTUAL stages in the code, in execution order." So I need to list the stages that are actually in the code, with the function/file that implements them.

Let me list only the stages that are clearly implemented in the provided files:

1. Video download/import - YouTubeDownloader or LocalVideoImporter (cliper.py) - acquires the source video
2. Transcription - Transcriber.transcribe() from src/transcriber.py - converts audio to text with timestamps
3. Clip segmentation - ClipsGenerator.generate_clips() from src/clips_generator.py - detects topic shifts

For stages 4 and 5, since the full source isn't provided, I should either:
- Include them but note "not visible in the files read" for the detailed implementation
- Or only include what's visible

Given the instruction to "Report only what the source actually shows" and the section requirement to have a numbered list, I think I should include all stages that are part of the pipeline as shown by the code structure, but be honest about what's fully vs partially visible.

Actually, re-reading: "If something is not in the files you were given, write 'not visible in the files read' - never guess, never pad, never write marketing copy."

So for the pipeline stages, if a stage's implementation isn't fully in the files, I should note that.

Let me structure the pipeline list to include only what's visible, and for any stage not fully visible, add a note.

Actually, I think the best approach is to list the stages as they're clearly implemented in the files, and for any that reference external modules not fully provided, add a note.

From my analysis, stages 1-3 are fully visible in the provided files. Stages 4-5 reference modules that are imported but whose full source isn't in the provided file list.

Let me now write the pipeline section:

## The pipeline, in order

1. Video acquisition - YouTubeDownloader or LocalVideoImporter (cliper.py: orchestrates user choice between YouTube URL and local ~/Downloads/ import)
2. Transcription - Transcriber.transcribe() from src/transcriber.py: extracts audio via ffmpeg, runs WhisperX alignment, outputs word-level timestamped JSON transcript
3. Clip segmentation - ClipsGenerator.generate_clips() from src/clips_generator.py: loads transcript, converts to ClipsAI format, uses ClipFinder.find_clips() to detect topic shifts via TextTiling + BERT; falls back to fixed-time clips if none detected
4. Caption generation - generate_copys_for_video() from (module not fully visible in files read): invokes LLM (Gemini/Claude) with system prompts to generate 150-char captions with code-switching and mandatory hashtags
5. Video export - VideoExporter.export_clips() from (module not fully visible in files read): combines clips, subtitles, reframing, and exports vertical video

But I'm not sure about the exact execution order from the code. Let me think about this differently.

The user wants "the ACTUAL stages in the code, in execution order." Since I don't have a main() function showing the exact execution flow, I should list the stages based on the code structure and the natural order of dependencies.

From the README pipeline diagram and the code structure, the execution order is:
1. Download video
2. Transcribe
3. Generate clips
4. Generate captions
5. Export video

And from the code, I can map these to specific functions/files.

Let me now finalize what I'll put in each section.

Actually, I realize I should more carefully determine what's "in the files read" vs not. The user provided a list of files, and I should only reference what's in those files.

Let me go through each file one more time to be sure what's included:

Provided files:
1. README.md - has description, architecture, stages 1-7 as described
2. cliper.py - main CLI orchestrator, helper functions
3. test_dual_llm_support.py - tests for CopysGenerator with Gemini/Claude
4. src/transcriber.py - WhisperX transcription module
5. src/clips_generator.py - ClipsAI clip generation module
6. src/subtitle_generator.py - SRT generation module
7. docker-compose.example.yml - Docker configuration
8. tests/diagnose_subtitles.py - subtitle diagnosis
9. tests/spike_face_reframing.py - MediaPipe face detection spike
10. tests/diagnose_subtitle_duplication.py - duplicate subtitle diagnosis
11. src/prompts/__init__.py - prompts module
12. src/prompts/base_prompts.py - base prompts for Gemini
13. src/utils/prompt_injector.py - prompt config injector
14. src/prompts/viral_prompt_es.py - viral style prompt in Spanish

Not provided (but referenced):
- src/video_exporter.py
- src/copys_generator.py (though test_dual_llm_support.py tests CopysGenerator)
- Other src modules

Now for the pipeline, I'll list stages 1-3 fully visible, and for 4-5 note they reference modules not fully in the files.

Actually, wait. The user said "Use exactly these sections" and one section is "## The pipeline, in order - A numbered list of the ACTUAL stages in the code, in execution order. For each stage name the function or file that implements it. This is the most important section - be concrete."

So I need to be concrete and name the function or file that implements each stage. For stages where the implementation isn't fully in the provided files, I should say "not visible in the files read".

Let me now determine what I can concretely say:

Stage 1: Video download/import
- File: cliper.py (YoutubeDownloader, LocalVideoImporter)
- Function: opcion_agregar_video() handles user choice
- What it does: Acquires video from YouTube or local ~/Downloads/ folder

Stage 2: Transcription
- File: src/transcriber.py (Transcriber class)
- Function: transcribe() 
- What it does: Extracts audio via ffmpeg, runs WhisperX, aligns timestamps, saves word-level JSON transcript

Stage 3: Clip generation
- File: src/clips_generator.py (ClipsGenerator class)
- Function: generate_clips()
- What it does: Loads transcript, converts to ClipsAI format, uses ClipFinder.find_clips() to detect topic shifts via TextTiling+BERT; falls back to fixed-time clips

Stage 4: Caption generation
- The generate_copys_for_video function is imported in cliper.py but its full source isn't in the provided files
- test_dual_llm_support.py shows CopysGenerator which supports Gemini/Claude
- I should note "not visible in the files read" for the detailed implementation

Stage 5: Video export
- VideoExporter class imported in cliper.py but src/video_exporter.py not in provided files
- Should note "not visible in the files read"

Stage 6: Computer vision reframing
- Mentioned in README but not in provided source files (except spike test)
- Should note "not visible in the files read"

Stage 7: Quality validation
- Referenced in README but not in provided source files
- Should note "not visible in the files read"

Actually, this is getting complicated. Let me re-read the user's instruction for this section: "A numbered list of the ACTUAL stages in the code, in execution order. For each stage name the function or file that implements it. This is the most important section - be concrete."

And: "If something is not in the files you were given, write 'not visible in the files read' - never guess, never pad, never write marketing copy."

So I should only include stages that are actually in the code files I was given. If a stage isn't implemented in the provided files, I shouldn't invent it or include it.

From my analysis, the clearly implemented stages in the provided files are:
1. Video acquisition (cliper.py)
2. Transcription (src/transcriber.py)
3. Clip generation (src/clips_generator.py)

Stages 4-7 are referenced but their full implementation isn't in the provided files.

But the README describes 7 stages. The user might expect me to include all 7, but I can only report what's actually in the files.

Let me take a different approach: I'll list the stages that are implemented in the code files, with their function/file names, and for any stage not fully visible, I'll add a note "not visible in the files read" after the stage description.

Actually, re-reading more carefully: "Report only what the source actually shows." So I should only report what's in the source files.

Let me list only the 3 stages that are clearly implemented:

1. Video acquisition - YouTubeDownloader/LocalVideoImporter (cliper.py: opcion_agregar_video) - acquires source video file
2. Transcription - Transcriber.transcribe() from src/transcriber.py - WhisperX-based word-level transcription
3. Clip segmentation - ClipsGenerator.generate_clips() from src/clips_generator.py - ClipsAI TextTiling with BERT topic shift detection

For the other stages, since they're not "in the files you were given" in terms of full implementation, I would note that, but the section requires a numbered list of actual stages. If I only have 3 stages, I'll list 3.

But wait, the user's description of the project says it has 7 stages (from the README pipeline). And the user wants me to write an engineering
