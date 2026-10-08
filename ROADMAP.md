# ROADMAP - what the AI videos and repos do, and what becky should do next

Built 2026-10-08 from Jordan's "AI useful" playlist videos and every repo they link
(HyperEdit/Creator OS, Kevin Badi's repos, Albert Olgaard's 38 skills, 11 agent-safety repos,
8 Jev/System One videos). Five research passes read the real code and skill files.
Full notes, one block per feature, are in `research/roadmap/` (01 to 05).

## How to read this

- `[x]` = becky already does it. The line says **which tool**, and whether it is **USED**
  (it ran on real work) or only **BUILT** (it exists but nothing runs it).
- `[ ]` = not done. "Partly: becky-X" means a piece exists.
- Every item says:
  - **Does:** what it is for and what it does.
  - **S1:** the question(s) a System One decision model would answer (yes/no, pick one, score).
    "None" means plain code is enough.
  - **Code:** the parts that can be deterministic. These should never go to a model.
  - **Data:** which becky tools or files feed it.
  - **From you:** what only Jordan can give. **I can get:** what Claude can fetch or measure
    alone, once you say yes.
- Their methods were ignored. Only what the feature does, and whether it uses a decision model,
  is kept. Albert's editing rules are a LOOSE template: the numbers that count are yours, measured
  from your own VEGAS projects.

## Start here - the 10 most useful next steps (my ranking)

1. **One watchdog that actually runs** (A1). Today's watchdogs are all switched off (see A0).
2. **Your editing numbers, measured from your VEGAS projects overnight** (B6). Every rule below
   needs your numbers, not Albert's.
3. **Rhythm gate on a finished edit**: "something new on screen every ~5 s" (B7).
4. **Thumbnail maker to your rules, with a check gate** (E1, E2).
5. **Stop agents claiming "done" when nothing was checked** (A2).
6. **Comment triage + "comments waiting for you" list** (G1, G2).
7. **Best-take picker in the rough cut** (B1 is built; wire it in).
8. **Confidence gate + calibration for every System One question** (I2). Your past edits are the
   answer key.
9. **Posture signal in the chat-reply finder** (C4).
10. **post-longform checklist** (F1). Title and description checks before you upload. Nothing is
   posted without your OK.

---

## A. Watchdogs and agent reliability

### A0. What exists today (checked 2026-10-08, Task Scheduler and `C:\Users\only1\bin`)

You were right: there are several watchdogs, and almost none of them run.

| Watchdog | State | Last ran |
|---|---|---|
| Becky Claude Deadman | switched OFF | 2026-07-23 |
| BeckyModelHeartbeat | switched OFF | 2026-07-25 |
| BeckyBullshitCheck | switched OFF, last run FAILED | 2026-07-20 |
| BeckyVisualCritic | switched OFF | 2026-07-25 |
| MissionControl Autopilot Watchdog | switched OFF | 2026-07-23 |
| Overnight Autopilot Guard | switched OFF | 2026-07-23 |
| dark-factory-cubase-ai-toolkit | switched OFF | 2026-09-23 |
| BeckyRoughcutOvernightCheck / SafetyCheck2 | on, but last ran 2026-08-25 | 2026-08-25 |
| Becky Playlist Scout (idle) | **on and working** | today 09:00 |
| `becky-foreman` (the job runner) | **never built**. SPEC-BECKY-FOREMAN.md, "dont build it yet" | - |
| `becky-harness` | built: runs an agent over an allowlist of becky tools. Not a watchdog | - |
| `becky-unstick` | built and USED: the agent Firefox calls it on stuck pages | every browse |

### Items

- [ ] **A1. One watchdog that alerts once per failure** (Hermes "Cerberus")
  - **Does:** hourly and daily checks: is the LLM server up, is the disk under 85%, did each
    nightly job **move git** in the last 30 hours (a log line is not proof). One message per NEW
    failure, then silence until it recovers. It has no model in it on purpose ("an LLM can decide
    everything is fine when it is not").
  - **S1:** none.
  - **Code:** all of it. Port the shell script to one Windows scheduled task.
  - **Data:** the scheduled-task list above, git logs of becky / the factory, `becky-notify`
    (Telegram, built) or a popup to send the alert.
  - **From you:** which jobs matter, and where alerts go (popup on screen, or Telegram on your
    phone). Alerts follow your rule: what happened + the ONE thing to do.
  - **I can get:** everything else. I would also delete or merge the 7 dead watchdogs, with your OK.
- [ ] **A2. Retry cap for long agent runs** (Kevin's `watchdog.ts`)
  - **Does:** resumes a stalled run only for known passing errors (timeouts, rate limits), stops
    for a fixed list of fatal errors, and never more than 8 resumes in 36 hours.
  - **S1:** none (pattern list).
  - **Code:** all of it.
  - **Data:** run status files of the factory and becky jobs.
  - **From you:** nothing. **I can get:** the error patterns from past logs.
- [ ] **A3. Stop "done, tests pass" when no test ran** (jev-belay)
  - **Does:** at the end of a turn, if files changed and no build or test ran after the last
    change, the agent is not allowed to stop until it runs one. About $0.00005 per check.
  - **S1:** "Does the final message say the work is finished?" "Does it claim tests or a build
    passed?" "Would running tests be a real check of this task?"
  - **Code:** reading the transcript, the "a check passed after the last edit" rule (about 25
    test-runner names). The model is asked only when the code cannot tell.
  - **Data:** the session transcript (stays on the PC).
  - **From you:** a yes to install it as a hook. **I can get:** the rest.
- [ ] **A4. Gate before dangerous commands** (toolgate, jev-use gate, Cole Medin's hook)
  - **Does:** before each shell command or file write: is it destructive, does it leak secrets,
    does it send data out, is it off task? Deny, ask, or allow. In unattended night runs "ask"
    becomes "deny".
  - **S1:** the seven yes/no risk questions (destroys data / sends files out / raises privileges /
    off task / exposes a secret / breaks a stated rule / makes a choice you reserved).
  - **Code:** hard blocks first (`rm -rf` on known roots, `.env` writes, force-push to master,
    PATH edits). Partly: `~/.claude/hooks/block-path-overwrite.py` already does PATH.
  - **From you:** a yes. Pick ONE of toolgate or jev-use (they overlap).
- [ ] **A5. Check every edit against your written rules** (abide)
  - **Does:** turns CLAUDE.md lessons into one question each and checks every edit against
    them. Strong hits make the agent fix the edit in the same turn. A "calibrate" step switches off
    rules that never fire or always fire.
  - **S1:** one yes/no per rule, written from your LESSONS.
  - **Code:** rules a regex can check never go to the model (ASCII-only .bat, no em-dashes,
    `pause` at the end, no PATH edits).
  - **From you:** a yes. Their own audit: only about 1 in 4 edit flags was real, so start in
    "note only" mode.
- [ ] **A6. "Only a plain stop stops" for messages sent mid-task** (Jev_steer_or_queue)
  - **Does:** when you type while an agent works: change course, wait until done, or stop.
    It acts only at 90%+ certainty, and "stop" also needs an explicit "stop right now" answer.
    This matches your lesson about the stuck spacebar.
  - **S1:** "steer / queue / interrupt / unclear?" and "Does this explicitly ask to stop now?"
  - **Code:** queue, thresholds, fail-open on any error. Start in log-only mode.
- [ ] **A7. Lint gate for notes and docs** (Hermes wiki lint)
  - **Does:** refuses a commit when a doc has broken `[[links]]`, an open `[ ]` with no date or
    trigger, or a bad log heading. Structure only, never content.
  - **S1:** none. **Code:** all. **Data:** becky docs, memory files.
- [ ] **A8. Agent setup audit** (Hermes "five settings")
  - **Does:** checks each agent has only the tools its job needs, that its log file exists and
    is being written, and that memory really works (save a code word, recall it in a new session).
  - **S1:** none. **Code:** all.
- [x] **A9. Browser unsticker** - `becky-unstick`. USED (called by the agent Firefox on every
  stuck page).
- [ ] **A10. System One as the front door for becky-ask** (Creator OS jev-router, HyperEdit)
  - **Does:** every request is first sorted by a decision model to the right tool. Only the
    hard ones go to a big model.
  - **Partly:** `becky-ask` has its own intent router (act / answer / ask one question), and
    `becky-route` exists. Checked in code: **becky-ask does not call System One today.**
  - **S1:** "Which becky tool fits this request, or none?" (pick one).
  - **Code:** the tool table, running the tool, the log of every routing decision. That log is
    the training data for a local router later.

---

## B. Editing (raw footage, long-form)

- [x] **B1. Best-take picker** - `becky-besttake`. BUILT (2026-10-07). It asks System One "is the
  later line a restart of the earlier one?" and "does this take finish its thought?". It was scored
  against your real cuts of 3 raw clips. **Not yet wired into the rough cut.**
  - **Next:** wire it into `becky-roughcut`, so the last complete take wins.
- [x] **B2. Raw takes to a VEGAS rough cut** - `becky-roughcut`. BUILT. Its overnight checks last
  ran 2026-08-25.
- [x] **B3. Dead-air removal** - `becky-cut` (auto-editor + voice check). USED inside
  becky-livestream.
- [ ] **B4. Filler-word trim** (um, uh, "like")
  - **Does:** cuts filler words by their word timings, never whole sentences.
  - **S1:** "Is this 'like' a filler or part of the sentence?" (only for ambiguous words).
  - **Code:** word list, word edges snapped to the frame grid, no slivers under 1 frame.
  - **Data:** becky-transcribe word times; your word list (2026-10-05 transcription fix).
  - **From you:** your filler list, and 5 marked examples to check before any blanket use
    (your rule: you are the editor).
- [ ] **B5. Said-twice and repeated-opener scan**
  - **Does:** after the cut, finds a sentence said twice (even reworded), and consecutive
    sentences that start with the same words ("And now inside... And now inside...").
  - **S1:** "Does sentence B say what sentence A already said?" Albert's thresholds: 0.85+ said
    twice, 0.35 or less clear, anything between goes to you.
  - **Code:** 5-word repeat scan, opener scan. Partly: becky-besttake's coverage check.
- [ ] **B6. Measure YOUR editing numbers from your VEGAS projects** (open-edits reverse-engineer,
  Kevin's "measure your own videos")
  - **Does:** for every finished project: shot length (median, 10th and 90th percentile), cuts
    per minute, zoom count and size, time on face vs other, how often something new happens, the
    loudness. Then write the numbers down as your rule book.
  - **S1:** per shot, "face / screen / graphic / B-roll?" (pick one), only where the project
    file does not say.
  - **Code:** almost all of it. The read-only VEGAS export already exists.
  - **Data:** `vegas/BeckyDumpProject.cs` + `veg_export_all.py` (reads .veg files, never changes
    them), `vegas/edit_habits.py`, `vegas/edit-learning/habits.md`.
  - **From you:** which projects count as "finished, my style".
  - **I can get:** with your OK, an overnight run over all of them (copies only; originals are
    never touched).
- [ ] **B7. Rhythm gate on a finished edit** (famous-youtube-editor, Albert's gates)
  - **Does:** flags every stretch longer than your number (Albert: ~5 s) with nothing new on
    screen, any one shot held too long, B-roll back to back, a first shot that is not your face,
    and a CTA that is not your face. You get a list or markers, nothing is changed.
  - **S1:** "Is this sentence the hook / the call to action?" (yes/no).
  - **Code:** all the timing checks, from the .veg export. Thresholds come from B6.
- [ ] **B8. Shot plan per sentence** (famous-youtube-editor A/B/C/D modes)
  - **Does:** gives each sentence a mode: A face (opinion, joke, CTA), B full graphic (concept,
    list, number), C picture-in-picture (pointing at something), D B-roll (a concrete action).
    Mode changes land on word boundaries.
  - **S1:** "Which mode fits this sentence: A / B / C / D?" with probabilities.
  - **Code:** mode length limits, no two B-rolls in a row, word-boundary snapping.
  - **From you:** 5 example sentences per mode from your own edits, so the questions are tested
    first.
- [ ] **B9. Render check gates** (Albert's check_air / check_motion / check_frames, the sync checks)
  - **Does:** after a render: no quiet gap over 0.30 s inside speech, no frozen picture over
    0.6 s, no blank frame, no clipped first or last word, sound and picture in sync within 1 frame,
    final loudness -14 LUFS.
  - **S1:** none. **Code:** all (ffmpeg, the transcript). **Data:** the rendered file,
    becky-transcribe.
- [ ] **B10. Zoom / punch-in on emphasis**
  - **Partly:** `vegas/BeckyFX.cs` applies your zoom preset to a range (built 2026-10-07, test
    project in `vegas/edit-learning/fxtest/`).
  - **S1:** "Is he stressing this word or phrase?" (yes/no + score).
  - **Code:** zoom size and hold limits from B6 (Albert's: 1.2-1.4x normal, 1.6x max, hold 4 s+).
- [x] **B11. Bleep a swear word** - `vegas/BeckyFX.cs` (bleep + on-screen censor). BUILT, in the
  fx test project for your review. Not used on a real edit yet.
- [ ] **B12. Screen-recording windows with zoom to the thing you are talking about**
  (albert-reel-white-screen)
  - **Does:** for tutorials: shows the screen exactly while you narrate an action, then zooms onto
    the button or text you mean.
  - **S1:** "Is he describing something on screen in this sentence?" "Which part of the screen
    must be readable?" (pick one).
  - **Code:** the windows from word times, zoom maths, a "blank frame" check.
- [ ] **B13. Blur secrets on screen recordings** (API keys, emails)
  - **S1:** "Is this text a secret?" only for odd cases.
  - **Code:** OCR + key patterns (sk-, api_key=, long hex). **Data:** `becky-ocr`.
- [x] **B14. Captions on the timeline** - `becky-captions`, `becky-subtitle`, `vegas/BeckyCaptions.cs`.
  BUILT. STATE-OF-MASTER still lists "AWAITING JORDAN: one step in VEGAS", so not confirmed used.
- [ ] **B15. YouTube chapters from the transcript** (HyperEdit)
  - **S1:** "Does a new topic start at this sentence?" (yes/no).
  - **Code:** chapter times, the 0:00 first chapter, YouTube's minimum chapter length.
- [ ] **B16. Logos and portraits when a product or person is named** (open-edits)
  - **Does:** when you say a product or a person, the real logo or photo pops in. Never invented
    ones.
  - **S1:** "Is this word a product, a person, or neither?" (pick one).
  - **Code:** fetching from fixed sources (Wikimedia, simple-icons), caching, placing on the word.
- [ ] **B17. "Get me the clip where I talk about X"** (Jev media library, HyperEdit Obsidian agent)
  - **Partly:** `becky-search`, `becky-library`, qmd search over transcripts.
  - **S1:** "Which of these clips matches the request?" (pick one).
  - **Code:** the index of transcripts and files, the import into VEGAS.
- [ ] **B18. Recording preflight** (Hermes videokit check)
  - **Does:** a 15-second test recording must pass first: mic not clipping (peak near 0 dB),
    camera in sync with the mic.
  - **S1:** none. **Code:** all.
- [x] **B19. Learn your edits from your VEGAS projects** - `veg_export_all.py`, `editlearn.py`,
  `edit_habits.py`, `becky-habits`. USED (overnight 2026-10-07; results in
  `vegas/edit-learning/PROGRESS.md` and `habits.md`).
- [ ] **B20. AI B-roll and animated graphics** (Higgsfield, Remotion, open-edits engine)
  - Not recommended now: the generators cost money and none of it is in VEGAS. Listed so it is
    not lost. Partly: `becky-imagegen` (still images).

---

## C. Livestreams and live chat

- [x] **C1. Cut a stream down to the topics you ask for** - `becky-livestream` (Claude, Gemma, Qwen
  or System One). USED (27-livestream, the apology stream). The System One version is new today:
  `workflows/livestream/Livestream-Edit_System-One.bat`.
- [x] **C2. Chat replay + "is he reading chat aloud?"** - `becky-livechat`. USED.
- [x] **C3. "Is he ANSWERING a chat message?"** - `becky-livechat` (new 2026-10-08). USED once: on the
  apology stream it found 73 answers, usually about 16 s after the message. About 68 of them are
  clearly right; about 5 are doubtful fragments.
  - List to review: `vegas/edit-learning/apology-chat-replies.txt`.
  - S1 question: "Which recent chat message (or none) is he answering with this sentence?",
    accepted only at 70%+. After "let me scroll up" or "someone asked", it looks back 5 minutes.
  - **Not measured:** how many answers it misses.
- [ ] **C4. Use your body language in the reply finder**
  - **Does:** a reply counts as more likely if your eyes went to the chat screen in the 30 s
    before it (your example).
  - **S1:** none for the glance itself. The reply question stays as in C3.
  - **Code:** a gaze/head-turn signal across the whole stream. The picture signals exist
    (`vegas/edit-learning` gaze files, MediaPipe in becky-livestream), but they are not computed
    for the whole stream yet.
  - **From you:** where the chat screen sits (left or right of the camera).
- [ ] **C5. Live keyword moderation** (Jev use cases 17 and 18)
  - **Does:** during a live stream, hides chat messages with your banned words. Each word maps to
    one fixed action (hide, timeout, ban).
  - **S1:** "Is this message spam / a slur / harassment?" (yes/no each).
  - **Code:** reading live chat, the word list, the action, the log.
  - **From you:** the banned list and the actions. It needs moderator access to your live chat.
- [x] **C6. Find every moment a long recording talks about a topic** (Clipfast) - partly
  `becky-clip` and `becky-quotes`. USED for forensic quote search.

---

## D. Shorts and clipping

- [x] **D1. Shorts from a finished video** - `becky-short`, `becky-moment`, `becky-hits`. BUILT. The
  state is in `HANDOFF-SHORTS-2026-08-20.md`. Recent use not confirmed.
- [ ] **D2. Hook check on each short**
  - **Does:** the first 3 s must stand alone as a hook. Never cut mid-word.
  - **S1:** "Would this 30-60 s window stand alone, with a hook in the first 3 s?" (score).
  - **Code:** duration, word-boundary edges, the first spoken line.
- [ ] **D3. Repost an old winner as a fresh version** (famous-repurpose-ig)
  - Only your own videos, waiting 2-4 weeks, at most 10 per 30 days. Low priority.
- [ ] **D4. Faceless news-explainer format** (Fireship style: a cut every ~2.2 s, nothing static
  over 1.5 s)
  - Only if you want a no-face channel. The measured numbers are a template.

---

## E. Thumbnails and titles

- [ ] **E1. Thumbnail from your own face frame, to your rules** (Hermes recipe, famous-thumbnail,
  youtube-thumbnail-maker "X + Y")
  - **Does:** pulls 6 candidate frames of your face (eye contact, mid-word), builds the thumbnail
    from YOUR frame (never another creator's thumbnail), then checks every letter at 100% zoom.
    The "two app icons + plus sign" layout is one template.
  - **S1:** "Which frame shows the strongest expression?" (pick one). "Is this downloaded icon
    the official logo for this app?" (yes/no).
  - **Code:** frame extraction, face size, the layout, file size under 2 MB, 1280x720.
  - **Data:** `becky-identify`/insightface (face box), MediaPipe (expression scores),
    `becky-imagegen`.
  - **From you:** 5-10 thumbnails you like (yours or others), and your rules.
- [ ] **E2. Thumbnail check gate** (carousel-qa, famous-thumbnail rules)
  - **Does:** a pass/fail check before you see a thumbnail: 4 words or fewer, face at least 1/3
    of the height, 3 colours at most, nothing in the bottom-right corner (the timestamp), text
    readable when shrunk to 200 px wide, spelling checked by OCR.
  - **S1:** "Does the headline make a claim or tension?" (yes/no).
  - **Code:** almost all of it.
- [ ] **E3. Shorts cover + caption** (vertical-video-thumbnail)
  - **Does:** a 9:16 cover with a two-line hook, and a caption. Cover icons must match what you say.
  - **S1:** "Does the cover icon match the product he names?" (yes/no).
- [ ] **E4. Title check** (post-longform rules, Hermes titles.py)
  - **Does:** under ~70 characters with the hook first, never a filename as a title, and how
    similar titles performed (views divided by subscribers).
  - **S1:** "Does this title look like a filename?" (yes/no).
  - **Code:** length, the views/subs table (public data through yt-dlp).

---

## F. Publishing (nothing is ever posted without your OK)

- [ ] **F1. post-longform checklist** (Creator OS / social-agents)
  - **Does:** title under ~70 characters, pitch in the first 2 lines of the description, chapters,
    thumbnail under 2 MB, madeForKids left OFF (ON disables comments for good), scheduled uploads
    go up private and turn public at the time. It checks the post after upload, retries once, then
    reports.
  - **S1:** none needed.
  - **Code:** all the checks.
  - **From you:** whether an agent may upload at all (it needs your YouTube login). Until then it
    only prepares and checks.
- [ ] **F2. One upload to Shorts / TikTok / Reels**
  - If one platform fails, post the rest and report.
- [ ] **F3. Batch schedule from a list** (schedule-posts)
  - Rule worth keeping: a row with no time publishes IMMEDIATELY, so every row is checked.
- [ ] **F4. Instagram carousels from a video** (albert-carousel-ig) + carousel check gate.
  Low priority.
- [ ] **F5. Brand file** (brand-interview)
  - **Does:** one file with your voice, words you never use, emoji and hashtag policy. Asked one
    question at a time, never guessed.
  - **From you:** the answers (10 minutes, by voice through Whoretana).

---

## G. Comments, DMs and inbox

- [ ] **G1. Comment triage into 5 buckets** (respond-to-comments)
  - **Does:** REPLY / SKIP (spam, bots, trolls) / ESCALATE (money, legal, medical, press, minors,
    safety) / LIKE ONLY / HIDE (scam links, slurs; never hide criticism). "When unsure, escalate."
    Never reply to itself, never argue, never promise. At most ~30 replies per run.
  - **S1:** "Which bucket?" (pick one). "Does it touch money, legal, medical, press, a minor or
    safety?" (yes/no).
  - **Code:** fetching, the escalation word list, caps, counts.
  - **From you:** whether replies are drafted for you or sent.
- [ ] **G2. "Comments waiting for you" daily list** (Hermes youtube-report.py)
  - **Does:** every morning: view changes on your last 10 uploads, and up to 3 comments per
    video you have not answered. No AI, no API key.
  - **S1:** none. **Code:** all, through `internal/ytdlp` (your yt-dlp .conf is never touched).
  - **From you:** your channel handle. **I can get:** the rest.
- [ ] **G3. Sponsor inbox sorting** (Jev use case 2)
  - **Does:** tags each email: sponsor / business / fan / spam.
  - **S1:** "Which tag fits this email?" (pick one).
  - **Data:** `becky-gmail` (built, read-only Gmail reader).
- [ ] **G4. DM reply drafts in your voice** (albert-dm, respond-to-messages)
  - Rules worth keeping: one reply per conversation, no links before they ask, never double-text.

---

## H. Ideas, analytics and research

- [x] **H1. Research intake of your saved videos** - `becky-intake` + `becky-decide` (Laya, local).
  USED: the "Becky Playlist Scout" task runs daily at 09:00 (ran today).
- [ ] **H2. Topic scan: which videos beat their channel size** (Hermes trends.py, Jev use case 21)
  - **Does:** for your niche searches this week: views divided by subscribers. Over 1x means the
    video travelled past its own audience.
  - **S1:** "Is this topic in Jordan's niche?" (yes/no).
  - **Code:** search, ratio, sort (yt-dlp, no key).
  - **From you:** your niche search words.
- [ ] **H3. Your own winning videos, measured** (copywriter "measure your own", Jev use case 14)
  - **Does:** pairs each of your transcripts with its views, then asks what the winners share
    (hook length, video length, words per minute).
  - **Code:** the measurements. **Data:** becky-transcribe, public view counts.
- [ ] **H4. Weekly channel report with ONE recommendation** (analytics-report)
  - "Flat is flat, down is down." Numbers and changes, stale data marked as stale.
- [ ] **H5. Questions your viewers ask** (Reddit mining, Jev use case 20)
  - Needs the agent Firefox (your browser rule) and your subreddit list. Low priority.

---

## I. System One plumbing (what makes every S1 question above trustworthy)

- [x] **I1. Paid decision model with a hard $5/month cap** - `internal/systemone` Hosted. USED by
  `becky-besttake`, `becky-livechat` and `becky-livestream --model systemone`. Spend so far about
  $0.05 of $5 this month.
- [x] **I2a. Local decision model** - `becky-decide` (Laya, CPU). USED by becky-intake.
- [ ] **I2. Confidence gate + calibration for each question** (Sam Witteveen, The AI Automators)
  - **Does:** each answer below a set certainty goes to you or a second model. Scores are corrected
    with one number (a "temperature") found on a separate answer key. In their test this cut wrong
    accepted answers from 97 to 36 per 1,000.
  - **Code:** all of it.
  - **Data:** your past edits are the answer key: `vegas/edit-learning` answer keys, the 300
    labelled sentences of the 27-livestream (Claude, Gemma, Qwen and System One all compared on
    2026-10-08).
  - **From you:** how many mistakes you accept versus how much you want to review. That sets
    the threshold.
- [ ] **I3. Test every question on every model before trusting it** (per-question bake-off)
  - Partly done today on one stream: System One agreed with Claude on 269 of 300 sentences,
    Gemma on 268, Qwen on 272.
- [ ] **I4. Train the local model on your decisions** (Laya fine-tune, Jev videos)
  - Needs a few thousand labelled decisions (kept vs cut, take chosen vs not), split into 4 piles.
    The overnight VEGAS export (B6) can produce them.
- [ ] **I5. Picture questions** (ImaJev on images)
  - "Is the title on screen?" "Is the logo present?" on a frame, with no OCR. For E2 and B9.
- [ ] **I6. Sort big piles without reading them** (quicksilver / jev-use judge)
  - Logs, comments, files, with only the matches handed to the agent.

---

## What I need from you (the short list)

1. **Which watchdog alerts you want, and where** (on screen or Telegram) - A1.
2. **Which VEGAS projects are "finished, my style"** - B6.
3. **5-10 thumbnails you like, and your thumbnail rules** - E1.
4. **Your channel handle and your niche search words** - G2, H2.
5. **Where the chat screen sits relative to the camera** - C4.
6. **Yes/no on installing the agent-safety hooks** (A3, A4, A5) - they change how every Claude
   session behaves.

## What I can do myself once you say yes

- **Overnight:** measure every VEGAS project you name (copies only, originals never touched). This
  gives your rhythm, zoom, cut and shot numbers (B6) and the answer keys for calibration (I2).
- Port the watchdog (A1) and clean up the 7 switched-off ones.
- Build the "comments waiting" list and the topic scan from public YouTube data (G2, H2).
- Wire becky-besttake into the rough cut (B1).

## Not for your channel (kept, not planned)

Lead scraping, cold-call and cold-email scripts (Instantly), Upwork profiles and proposals,
LinkedIn / Facebook / Instagram outreach bots, website and landing-page builders, SEO audits and
blog engines, CRMs, deal-hunting agents, game-playing agents, game modding and reverse engineering
(rea, universal-modder), AI avatar clones (HeyGen), cloud workers (Railway), and the
cost / scalability / security reference skills.

## Flags

- **Breaks your browser rule as built (never run as-is):** jev-ultrafast (uses Browser Harness on
  real Chrome), jev-browser (its own Chromium), and the Creator OS facebook / instagram / linkedin /
  twitter engage skills (Playwright or Chrome CDP on a personal account). Any web task goes through
  the agent Firefox.
- **Costs money (not allowed without your yes):** Higgsfield, ElevenLabs, fal, Klap, DataForSEO,
  Creator OS. Only System One is paid, capped at $5/month in code.
- **Unverified claims:** speed, cost and accuracy numbers in this file are the authors' own unless
  it says becky measured them.
