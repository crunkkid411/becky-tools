# becky-tools agent feedback, 2026-10-15

Written by Claude (Opus 5.5), working in `X:\AI-2\contentx`, at Jordan's request after he saw the
GPU sitting idle. This is a record of what happened, so a builder can fix becky-tools. Everything
here was checked on the machine unless it is marked "not measured".

## The short version (for Jordan)

I re-transcribed 9 livestream videos with `becky-transcribe`. I ran them **one at a time**, and
most of each run happened **on the processor, not the graphics card**. After 3 hours, only 4 of
the 9 were done; a large part of that time could have been saved (how much is not measured).
Three things caused it:

1. **I did not check what becky would run on.** I copied the corpus command and started it.
2. **becky's built-in setting is "use the processor"** for speaker matching, faces and voices, and
   `becky-transcribe` also runs a whole "who is in this video" check (faces, voices, on-screen
   text) on every video by default. Our job never used that result.
3. **Every becky tool starts its own copy of the Gemma model.** With 3 copies plus another
   agent's model on the 8 GB card, mine did not fit and ran on the processor.

None of the watchdogs would have caught this: they are all switched off, and none of them
looks at whether the GPU is being used.

---

## 1. What I was doing

The task: 9 videos in the Hair Jordan corpus (`E:\hair_jordan\youtube`) had transcripts from a
free cloud API whose timings drift. I re-transcribed them locally, then ran the corpus's
who-is-Jordan and misheard-word steps, then checked the flagged words by hand.

The command, copied from `X:\AI-2\hj-clone-corpus\tools\corpus\p3_process.py` line 35:

```
becky-transcribe ./<id>.mp4 --diarize --kb E:\hair_jordan\becky-kb
```

My runner (a Python script in my scratch folder) ran the 9 videos **in a row, one at a time**,
starting 16:47. Its only GPU check was "wait until 3,000 MB of graphics memory is free" before
each video.

## 2. Timeline (all times 2026-10-15, local)

| Time | What happened |
|---|---|
| 16:47 | Runner starts video 1 (H1MDpTVEyD0, 73 min long). |
| 16:58 | becky-transcribe starts its own Gemma-4 E4B llama-server (`-ngl auto`). |
| 17:05 | Snapshot: GPU 7,815 MB used, **204 MB free**, 1% busy. Three Gemma-4 E4B servers are running: mine (from becky-transcribe), one from `becky-regrab`, one from `becky-intake`, plus another agent's LiquidAI d1-3B server. |
| 17:11 | My Gemma server has used **3,213 seconds of processor time in 13 minutes** (about 4 cores flat out), so it was running on the processor, not the card. I noted "it will be slower" and **did not dig in**. |
| 17:41 | Video 1 done: **3,251 s for a 4,370 s video** (0.74x real time). |
| 17:58 | Video 2 (HUQUrjERKIY) done: 929 s for 1,410 s (0.66x). |
| 18:36 | Video 3 (JABvuSm5keM) done: 2,186 s for 2,995 s (0.73x). |
| 19:02 | Video 4 (KwxgECCtYKo) done: 1,492 s for 2,189 s (0.68x). |
| 19:03 | Video 5 (PU-6WD32AHc, 66 min) starts. |
| 19:46 | Video 5 is still not done: becky-transcribe has started `becky-identify` (the `--forensic` step). Its helpers `voice_embed.py` and `face_embed.py` run on the processor. |
| 19:48 | Jordan: the GPU is not being used. GPU at 3% busy, 5.7 GB free. |
| 19:49 | I start a second worker, so two videos run at once (the second also ran WhisperX on the GPU, 100% busy at 19:55). |

So: **4 videos (3.0 hours of footage) took 2 h 15 min, one after another**, while the 8 GB card sat
mostly idle except when other agents' jobs used it.

## 3. Everything that wasted time, tokens or the machine

### A. becky-tools defaults (for the builder)

1. **The default device is the processor.** `becky-go/internal/config/config.go` (about line 437)
   sets `Device: "cpu"`, and **there is no `C:\Users\only1\.becky\config.json`** to override it. So
   every tool that takes its device from the config runs on the processor: becky-identify (face,
   voice and the sherpa diarizer it still uses), `voice_embed.py`, `face_embed.py`,
   `diarize_sherpa.py` (all three Python helpers default to `--device cpu` too).
   `internal/voiceprint/voiceprint.go:98` hard-codes `--device cpu`.
   Only the speech recognition (Parakeet, `--device auto`) and WhisperX (`--compute_type float16`)
   use the GPU.
2. **`becky-transcribe --forensic` is on by default** (`cmd/transcribe/main.go:167`). Every transcribe
   call, after the transcript is done, runs `becky-identify` over the **whole video** (face prints,
   voice prints, on-screen text plan) on the processor. In video 5 this step had been running for
   9+ minutes at 19:55. Our pipeline throws the result away: `p4_who.py` works out who Jordan is
   on its own, with its own voice embeddings. So the same voices get embedded **three times** per
   video (becky-diarize, forensic identify, p4_who), all on the processor.
3. **becky-diarize is processor-only on purpose** (`cmd/diarize/main.go` header): nemo-speech.exe is
   built without CUDA and upstream hard-codes 4 threads, "so diarization never takes VRAM from
   the shared 8 GB GPU". The model is a 107 MB GGUF. Keeping 107 MB off an 8 GB card in exchange
   for long processor-only runs on every video looks like the wrong trade (not measured: how much
   faster a CUDA build would be).
4. **Every becky process starts its own llama-server** (`internal/llmlocal/client.go`). At 17:05 three
   separate Gemma-4 E4B servers (mine via becky-transcribe, becky-regrab, becky-intake) plus another
   agent's d1-3B server competed for 8 GB. `-ngl auto` then "fits" by pushing layers onto the
   processor, silently. There is no shared server and no queue.
5. **The "never -ngl 99" rule is not enforced.** `llmlocal/client.go:89` says never use `-ngl 99`, but
   the becky-intake server at 17:09 was started with `-ngl 99 -c 16384`, and these still hard-code
   it: `cmd/scout/model.go:80`, `internal/ground/ground.go:175`, `cmd/validate` (always -ngl 99),
   `internal/avlm`, `cmd/vision` default.
6. **No record of where the time went.** Without `--verbose`, the transcript JSON and stderr say
   nothing about how long each stage took or what device it used. The ASR's `fell_back` field (CUDA
   to CPU) is in the internal result (`main.go:134`) but **not in the saved transcript**. I could
   only see a total ("transcribe rc=0 3251s"), so I could not tell that most of the time was
   processor stages.
7. **The word list is compiled into the program** (`go:embed lexicon.txt`). I added 7 names at 17:20,
   but I could not rebuild `becky-transcribe.exe` because the running copy locks the file, so the
   remaining videos used the old list. (contentx applies the list again from the text file, so
   nothing was lost, but any other caller would get the stale list.)
8. **No batch mode.** Each video is a separate process that loads every model again (Parakeet,
   WhisperX large-v2, alignment model, Gemma, nemo-speech, face and voice models). There is no way
   to keep the GPU busy with video N+1's speech recognition while video N is in its processor
   stages.

### B. Corpus pipeline in hj-clone-corpus (also slowed this down)

9. `p4_who.py` and `p4_mishear.py` only run as **endless loops** over the whole folder; there is no
   "just these videos" mode. I had to write a separate driver that imports their functions.
10. Misheard-line ids are `<video>-<line number>`, so **re-running a video reuses old ids** that still
    point at the old transcript's verdicts. I had to rename the new ones (`<video>-r<line>`).
11. Line numbers come from `build_perspective_library.load_segments`, which **skips empty lines**, so
    they are off by one or more from the transcript's own line list. My review sheets showed the
    wrong neighbouring line until I matched by time instead.
12. The voice-embedding cache (`E:\hair_jordan\cache\emb\<id>.npy`) is reused whenever the **line
    count** matches, even if the transcript changed. I had to move the 9 old caches aside by hand.

### C. My own mistakes (agent behaviour)

13. **I copied a command without reading what it does.** `--kb` turns on the forensic step's naming,
    and `--forensic` is on by default. `--forensic=false` would have skipped a whole processor-only
    stage that we never used.
14. **I never checked which device each stage used.** I assumed "transcription = GPU".
15. **I only measured free graphics memory, never how busy the card was.** The 17:05 to 17:11
    numbers (204 MB free, my Gemma burning 4 processor cores) were a clear sign, and I wrote it off
    as "slower because other agents are using the card".
16. **I ran the 9 videos one after another** for over 3 hours with 12 processor cores, 18 GB free
    memory and, most of the time, free graphics memory. I only started a second worker after Jordan
    complained.
17. **I gave Jordan "about 4 more hours" as if that were normal**, instead of asking why a 73-minute
    video needed 54 minutes.
18. Token waste in my own checks: I printed long review sheets in full (one cut off and had to be
    re-run), my review script crashed on emoji in chat messages (Windows console code page) and
    had to be re-run, I pointed a check script at the wrong folder (`hj-clone-corpus` instead of
    `hj-clone`) and ran it twice, and I spent three tries pulling an old inline script out of the
    session log.

## 4. Suggestions (for the builder; none of these are tested)

In order of how much time they would likely save. Per Jordan's rules, each one belongs in code
(a default or a check that runs by itself), not in a "please remember" note.

1. **Make the GPU the default.** Change `Device: "cpu"` to `"auto"` (try CUDA, fall back to CPU, the
   way `transcribe_parakeet.py` already does) for identify, voice, face and the sherpa diarizer.
   Then measure graphics-memory use with two becky jobs at once.
2. **Don't run `--forensic` when nobody reads it.** Either default it off for batch callers, or have
   the corpus command (`p3_process.py`) pass `--forensic=false`. Whoretana's "one dumb call" can keep
   it on if it needs it.
3. **One shared llama-server per model.** Before starting a server, reuse one already serving the same
   GGUF (fixed port or a lock file), so three tools share one Gemma-4 E4B instead of loading three.
4. **Enforce the "-ngl auto" rule in code**: route scout, ground, validate, avlm and vision through
   `llmlocal`, or have a test fail on any `"-ngl", "99"`.
5. **Build nemo-speech with CUDA** and measure it. If it is much faster, the 107 MB is worth it.
6. **Write per-stage timings and devices into the transcript JSON** (for example `"timings":
   [{"stage": "asr", "s": 240, "device": "cuda"}, {"stage": "forensic", "s": 900, "device": "cpu"}]`),
   plus a one-line summary on stderr. Save `fell_back` too. Then a slow, processor-bound run is
   visible in the output instead of needing someone to watch Task Manager.
7. **A GPU-idle watchdog** (fits ROADMAP A1, no model inside): every few minutes, if a becky process
   has run for more than 10 minutes while the GPU is under 10% busy and has more than 3 GB free,
   write one line to a log and alert once. That would have fired around 17:15 today.
8. **Batch mode for becky-transcribe** (several files, models loaded once, GPU stage of the next file
   overlapping the processor stages of the current one).
9. **Read the word list from `lexicon.txt` at run time** (keep the embedded copy as fallback), so edits
   take effect without a rebuild.
10. **Corpus tools:** a `--only <id>` mode for p4_who / p4_mishear, ids that change when the transcript
    changes, one shared line-numbering helper, and an embedding cache keyed by the transcript's hash.

## 5. State when this was written (19:58)

- Videos 1 to 4 are done, checked, and their word fixes recorded.
- Video 5 (PU-6WD32AHc) is in its forensic step; video xoPYYgZcqj8 is running in parallel on a
  second worker; 3 more (Wx9U031VRlE, XyzqNMeIm7c, Y5uaqn7cw1U) are queued.
- I have not changed any becky-tools code or settings; the fixes above are for the builder to decide.
