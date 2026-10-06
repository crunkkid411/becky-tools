# Real-world test: editing the apology livestream in VEGAS (2026-10-05)

## Bottom line

The edit is done, saved, and open in VEGAS. The 1h52m stream is now **63m 29s**.
It keeps the apology material and what it refers to. It drops the pre-show, the chat and super chats, drink and door breaks, document fumbling, retakes, and dead air.

- **Project:** `X:\Videos\2026\09_sept\30-Apology-livestream\apology-livestream.veg`
- **Format:** 720x1280 vertical at 30 fps, like the footage.
- **Original video:** only read, never changed.

**On the timeline for you to review:**
- **6 orange markers** on tiny jump cuts that remove only 1-3 frames. You asked to judge these yourself, so nothing was smoothed (see below).
- **5 green regions** for things only you can decide:

| Where | What | Why it matters |
|---|---|---|
| 15:42-16:50 | You play John's own video | Copyright: you say on stream (7:55 in the original video) that he removed two of your copyright strikes |
| 22:48-38:08 | Evidence documents on screen | Some frames show a home address and people's names (clear at 30:00) |
| 42:25-42:32 | **Added back:** "Thank you guys for those of you who watched the content for so long. I'm really sorry that I'm doing this. I'm sorry to you guys because I didn't want to do this." | The first transcript had dropped this line completely |
| 46:19-46:42 | Police report on screen | It names Shelby and mentions a sexual-assault allegation |
| 49:58-52:27 | Shelby's videos, plus a photo of two people | Copyright. A flight booking with its confirmation code is visible at 52:27 |

## After your first review (same evening)

You agreed with every becky-tools recommendation except #5, the minimum cut length. You wanted to see examples first. So I left the cuts alone and placed markers instead: two each of 1-, 2- and 3-frame cuts, spread across the edit.

| Marker | Edit time | Frames removed | Near these words |
|---|---|---|---|
| Tiny cut 1 of 6 | 10:57.9 | 1 | "so many police reports" |
| Tiny cut 2 of 6 | 19:13.2 | 2 | "so much so that" |
| Tiny cut 3 of 6 | 27:54.2 | 3 | "There's more. Sorry if" |
| Tiny cut 4 of 6 | 37:29.0 | 1 | "of the house" |
| Tiny cut 5 of 6 | 47:47.1 | 2 | "he's got guns." |
| Tiny cut 6 of 6 | 57:06.0 | 3 | "if the law enforcement" |

The edit has 191 cuts like these: 54 remove 1 frame, 78 remove 2, and 59 remove 3.

Also done this evening:
- **Added back the missed audience apology.** The second transcription pass found it. I took the cut points from the audio: the dip before "Thank you guys", and becky-cut's own silence edge after "do this." I then re-transcribed the result from the timeline. It reads cleanly: "...I heard it with my own ears. Thank you guys for those of you who watched the content for so long. I'm really sorry that I'm doing this. I'm sorry to you guys 'cause I didn't want to do this. Sorry, John..."
- **Flagged the last two publish risks:** the police report and Shelby's section. I had seen both in the final contact sheets before the usage limit hit, but hadn't flagged them yet.
- **Corrected my own mistake.** I first told you the regions were red. VEGAS shows them as green flags; markers are orange.
- **Verified the timeline against the plan.** Every clip matches it exactly: 1,446 video and 1,446 audio clips, no gaps, all grouped, nothing selected. The project is saved. A copy of the version before these changes is `becky-edit\apology-livestream.before-final-touches.veg`.

## How it was done (what worked)

| Step | Tool | Time | Result |
|---|---|---|---|
| Transcribe 1h52m | becky-transcribe (Parakeet, GPU) | 4m01s | 15,084 words |
| Measure the silences | becky-cut --dry-run | 1m38s | threshold -41.9 dB |
| Decide what stays | me, reading 1,775 sentences | most of the session | 66 sections, 78.8 min |
| Place the cut points | my own script: the transcript picks the words, the audio picks the cut points | seconds | 0 kept words lost |
| New project, import, save | job script run through `becky-vegas run_script` | 20s | 720x1280, 30 fps |
| Keep only the 66 sections | job script through `run_script` | 0.3s | 132 events, all frame-exact |
| Remove the dead air | **BeckyCut.cs, unchanged, inside VEGAS** | 4m23s | 1,443 clips, 63.4 min |
| Check it | rebuilt the edit's audio, re-transcribed it, diffed it against the plan, sampled 380 frames | ~6 min | details below |
| Second transcription pass | becky-transcribe with the audio shifted 15 s | 3m27s | 37 passages the first pass lacked (25 in kept parts, 12 in cut parts) |
| Final touches: missed line, 6 markers, 3 regions, save | job script through `run_script` | 1.1s | matches the plan exactly |

What worked well:
- **BeckyCut in VEGAS matched my prediction exactly.** All 1,443 clips matched, with no gaps, broken groups or popups. Its analysis inside VEGAS was identical to my standalone run, so the tool is deterministic.
- **becky-vegas was solid.** `status`, `dialogs`, `timeline`, `markers`, `run_script`, `snapshot`, `cursor` and `add_region` all worked first time.
- **becky-cut's cut points are accurate.** Where a sentence starts after a pause, its cut lands on the real start of the voice within 1 frame. I checked 8 words against the waveform.
- **Your outline is covered.** Every point in `sorry.md` that you actually said on stream is in the edit.

## Where I struggled, and what failed

1. **Parakeet's word timestamps can't be trusted for cuts.**
   - After a pause, a word's start comes 0.2-0.35 s before the voice begins.
   - 52% of words have zero length.
   - Words at the end of a sentence stretch into the following silence.

   My first "lost words" check reported 830, then 6,311 lost words. Both were artifacts of these timestamps. I rebuilt the check to test each word's first 0.4 s against the actual audio: **0 real words lost**. The root cause is in the next section.
2. **The first transcript silently dropped whole sentences.** Two more transcriptions caught them: one of the finished edit, and one of the stream with its windows shifted 15 s.
   - About 34 passages of 3 or more words were missing inside the kept parts (found by re-transcribing the edit), and 12 inside the cut parts (found by the shifted pass).
   - Examples: "And I'm sorry that my music is not AI.", "I'm sorry for having such strong opinions...", and the audience apology above.
   - The ones inside kept parts were always in the edit, because their audio was kept.
   - It's the same audio decoded in a different window, so the model skipped the speech; my selection didn't cause it.
3. **Typical Parakeet mistakes, as you suspected:**
   - "six-montography of AI generated disc tracks" for "six-month discography of AI-generated diss tracks"
   - "Take It Back 2007" for TakingBack2007
   - "duresses"
   - "Harry Jordan"
4. **47 of my 136 section edges fell inside continuous speech.** There's no pause there, so my cut point *is* the final cut, and 11 of them weren't quiet. I fixed 3 by keeping one more sentence:
   - "Oh I'm not pressured at all."
   - Shelby's video into "Okay, so that... fun part"
   - "I gotta go for real."

   The remaining 2 sit in the dip between words.
5. **191 jump cuts of only 1-3 frames.** becky-cut removes even tiny pauses: it made 568 cut decisions of 0.2 s or less. In the final edit, 191 joins remove just 1-3 frames each, 12.9 s in total. I think they look like stutters on a talking head. **You're judging that from the 6 markers before anything changes.**
6. **VEGAS opened with "restore the autosaved project?"** This was left over from an old crashed session.
   - I backed up all 59 autosave files to `%LOCALAPPDATA%\VEGAS Pro\18.0\autosave-backup-2026-10-05\`.
   - Then I answered No with a direct button-click message.
   - Nothing was deleted.
7. **Timeline thumbnails said "Media Offline" while becky-cut was reading the file.** The media was fine: preview frames rendered at 10, 30 and 50 min, and the panel recovered.
8. **Commands that failed or needed workarounds:**
   - ffprobe has no `stream_side_data` option.
   - `df` doesn't work on X: in Git Bash.
   - The harness blocks `sleep`, so I used a background loop instead.
   - My generator's frame check was too strict, because becky-cut rounds to 1 ms.
   - `Events.Clear()` is unproven in VEGAS, so I used BeckyCut's `Events.Remove` loop instead.
   - One run of my own script failed on a misnamed variable. I fixed it and re-ran.
9. **Your roughcut rule says "remove retakes only, never content."** It conflicts with this job. Your prompt asked for content cuts, so I followed the prompt. You confirmed: "you were correct to remove actual content this time because I requested it."

## Root cause: why Parakeet words have zero length (your question)

**It's not the splice points.** The zero-length rate is the same everywhere in a 30 s window:

| Position of the word | Zero-length |
|---|---|
| first second after a splice | 50% |
| 1-3 s after | 52% |
| middle of the window | 52% |
| last 3 s before the next splice | 52% |

**It tracks word length:**

| Letters in the word | 1 | 2 | 3 | 4 | 5 | 6-7 | 8+ |
|---|---|---|---|---|---|---|---|
| Zero-length | 100% | 88% | 75% | 52% | 18% | 8% | ~0% |

**The cause is in becky's own code, not the model.** `becky-go/internal/pyhelpers/transcribe_parakeet_dml.py`, `merge_tokens_to_words`:
- Parakeet hands over each word as small pieces (tokens), with only a START time for each piece.
- The code ends each word at the start of its last piece. A one-piece word ("I", "so", "the") therefore ends exactly where it starts.
- Sentence-final words are the opposite. The period is its own piece, and Parakeet emits it late. Those words run 0.40 s long at the median, and 1.2 s at the 90th percentile, against 0.16 s for other words.

**Why sentences get dropped near splices.** Each window is 32 s of audio, but it keeps only the words in its first 30 s.
- So the start of every window is kept, and the start is where the model has no lead-in.
- The context-rich 2 s at the end is thrown away.
- 7 of 19 dropped blocks sat in the 2.5 s right after a splice, which is only 8% of the time.
- The rest are the model skipping speech. A second pass, ideally by a different model, catches those.

**Root fix (to build; you said the human must never hunt for missing words):**
1. Get true word start and end times from a forced-alignment step: a tool that is given the text and finds each word in the audio. Choosing the aligner goes through the usual research-a-class protocol.
2. Have each window keep its middle, where context exists on both sides, not its start.
3. Always run a second pass and merge the two. You suggested a different model for that pass (e.g. WhisperX), so the two don't make the same mistake.
4. Add a niche word list (your names, places, slang) to correct the text.

## What becky-tools needs, with your decisions

1. **A content-selection tool** for "keep only the apology, drop the chat". This took 90% of the effort and was all my judgment. **Agreed.**
   - Build it as a workflow (`.yaml` or Archon): every deterministic step runs automatically, and Gemma-4 is called only for the content calls.
   - It would label each sentence (chat reply, super chat, break, retake, meta, narrative), group the labels into sections to keep, and flag its own unsure calls.
2. **Multi-pass transcription.** One extra pass (3.5 min here) recovered dozens of dropped sentences. A missing sentence is invisible to every later step. **Agreed.** The second pass may use a different model.
3. **A transcript cleanup pass with Gemma-4**, plus your niche word list. It fixes the text without moving timestamps. **Agreed.**
4. **Correct word timings.** This is now the root fix above: repair the word builder and add forced alignment. **Agreed.**
5. **A minimum cut length in becky-cut** (don't remove pauses under about 6 frames). **On hold:** you're judging the 6 marked examples first.
6. **A keep-list applicator for VEGAS.** It reads a keep list and builds the timeline (what my one-off scripts did), then runs BeckyCut. It also needs edge logic, in Go:
   - snap to a becky-cut pause;
   - when there is no pause, use the quietest frame between words;
   - warn when an edge is still loud.

   **Agreed.**
7. **More becky-vegas verbs:** `run_script` with arguments, plus `new_project`, `save` and `dialog_click`. **Agreed.** SKILL.md currently says there is no save/open/close verb "on purpose", and that one is never to be added without your asking. Your approval on 2026-10-05 is that ask.
8. **An edit-verification tool.** It rebuilds the edit's audio, re-transcribes it, and diffs it against the plan. That is what caught the missed sentences. **Agreed.**
9. **Fix or document BeckyRoughCut.cs.** It calls `vegas.Exit()`, so it can't run in a live session. **Agreed.**
10. **New: a publish-safety step.**
    - Sample frames of the finished edit.
    - Run OCR to catch addresses, phone numbers and booking codes.
    - Ask a vision model specific questions: is another creator's video on screen? are there documents with names?
    - Put a region on each finding.

    Local models won't do this unless the workflow has the step.

## Your workflow plan

This section records what you said, so it isn't lost.
- **Each kind of editing is its own dedicated workflow:**
  - livestream clip-down (this test): the same every time, plus a bit of topical guidance from you;
  - rough cut: different footage, retakes only;
  - shorts from long-form widescreen videos.
- **Ideal use:** a `.bat` you double-click in the footage folder, with a short prompt of context if needed.
- **Determinism:** once locked, a workflow runs exactly the same each time. Only the AI's content decisions change.
- **Order:** make the root tools robust first, especially the Parakeet issues, then lock the workflow.
- **Next test:** a shorter livestream you'll provide. You'll compare my result with local Gemma-4 and local Qwen3.5. You're open to Qwen and Gemma reviewing each other's work inside the workflow.

## Your questions, answered

**How did I know about the copyright and address problems?**
- I used my own vision, not a becky-tools vision tool. I pulled one frame for every 10 s of the finished edit (380 frames), tiled them into 10 contact sheets, and looked at all of them.
- The transcript didn't steer it. It was one even pass over the whole edit, to check what's actually on screen.
- The transcript then backed up the copyright call. At 7:55 in the original video you say John removed two of your copyright strikes, and at 9:23: "I don't want to steal your copyrighted content."
- For the last two flags, I also pulled frames every 2 s, plus three full-size frames, to set exact start and end points and read the text.

**Did I look at other parts of the video?** Yes:
- the whole edit at 10 s spacing;
- three full-size preview frames from inside VEGAS, at 10, 30 and 50 min;
- full-screen screenshots of VEGAS every 30-60 s while it worked.

I did not visually check the material I cut.

**Would local models do this?** Not unless the workflow has a step for it. That's recommendation #10.

**Did I use vision, screenshots, or the mouse?**
- **Vision and screenshots:** yes, everything listed above.
- **Mouse and keyboard:** none. The one VEGAS popup was answered with a click message sent straight to its "No" button.
- **Everything else** went through `becky-vegas` commands and job scripts.

**Would I have continued if the usage limit hadn't hit?** Yes:
- add the missed line;
- smooth the tiny cuts (now on hold, at your request);
- re-check the timeline;
- save;
- write memory notes;
- commit this report and the becky-tools logs.

Flagging the police report and Shelby's section was also still owed. All of it is done now, except the tiny-cut fix you put on hold.

## Files

All in `X:\Videos\2026\09_sept\30-Apology-livestream\becky-edit\`:
- **What to keep:** `edit_plan.py`, `sentences.txt`
- **Cut points:** `build_ranges.py`, `content_ranges.json`, `predicted_final.json`, `final_plan.py` (adds the missed line, picks the marked cuts, maps the regions)
- **VEGAS job scripts:** `ApologySetup.cs`, `ApologyContentCut.cs`, `ApologySave.cs`, `gen_final_cs.py` → `ApologyFinalTouches.cs`
- **Checks:** `check_plan.py`, `assemble_edit.py`, `verify_edit.py`
- **Timeline exports:** after the content cut, after BeckyCut, before and after the final touches (`timeline_final.json`, `markers_final.json`)
- **Project backup:** `apology-livestream.before-final-touches.veg`

VEGAS also writes its normal `.sfk` and `.veg.bak` files next to the project.
