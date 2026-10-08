# Agent infrastructure: what the 11 linked repos do, and what Jordan could use

Source repos were cloned (depth 1) into `...\scratchpad\roadmap\src\`. 33 functionality blocks below. Nothing was run; every claim comes from the repo's code, docs or benchmarks.

## Bottom line for Jordan

- **Most useful now:** a no-AI watchdog that alerts once per failure (hermes Cerberus); a per-edit check that enforces the rules in CLAUDE.md (abide); a gate that stops destructive commands before they run (jev-use gate or toolgate, pick one); a check that stops an agent saying "tests pass" when no test ran (jev-belay).
- **Hermes "watchdog" answer:** Cerberus is a plain shell script with no model. Its design (check that real work happened, alert once per failure, say what broke) is the part to copy. Its source does not show why our own watchdogs keep failing. That has to be found in our own code.
- **Compliance flags:** jev-ultrafast uses Browser Harness on his real Chrome, and Browser Harness is one of the tools Jordan banned for agents. jev-browser launches its own Chromium. Neither may be used until rebuilt on agent-firefox.
- **Paid parts:** Jev is a paid API. Each block states the cost where the repo gives one. jev-seo `--full` costs about $0.30 per site (DataForSEO).

## Jev / System One use map (every place a decision model is asked a question)

| Repo | Where | Exact question(s) |
|---|---|---|
| hermes-research-agent | youtube-video `takes` | "`later` repeats the opening words of `earlier`, as a retake of the same sentence." and "Does `take` finish its thought - it ends on a complete sentence instead of trailing off, breaking off mid-sentence, or being abandoned?" |
| jev-ultrafast | agent loop | "Advance the user's entire goal from the CURRENT page using one operation." (operation choice), then one target choice per operation |
| jev-use | gate hook | "Should the agent be allowed to run this proposed action right now?" (allow / deny) |
| jev-use | judge CLI | caller-defined: `pick`, `check`, `rate` questions |
| jev-seo | page review | `page_type`, `intent`, `importance`, `action`, `helpfulness`, `specificity`, `answer_first`, `citable`, `trust`, `clear_next_step` (one question each per page) |
| jev-seo | full mode | keyword relevance (score), best page for keyword (choice), "Does `keywords.X` name a specific company... not the one in `site`?" |
| jev-steer-or-queue | mid-turn messages | "`new_message` ... what time relationship does this message have to the running task?" (steer / queue / interrupt / unclear) and "Does `new_message` explicitly ask to stop the running task right now?" |
| jev-belay | Stop hook | "Does `final_message` present the requested work as finished or working?", "Does `final_message` claim that tests, a build, or other checks were run and passed?", "Would running the project's tests, build, or lint be a meaningful way to check the work that `task` asks for?", plus one choice on what the message reports about the task |
| jev-browser | browser_do | "Does `page` show that `task.goal` has been achieved?", "...stops progress ... (captcha, access denied, error page)?", "Does `page` show an error or rejection message...?", "Is `page` a sign-in or sign-up screen...?", "Would the next action ... have an effect outside this browser that is hard to undo...?", plus choices for next tool, value, element |
| jev-browser | browser_read | per block: "Does the entry ... contain information that helps answer `question`?" |
| toolgate | every gated tool call | seven risk questions (destructive, exfiltration, privilege, off_task, secret_exposure, violates_constraint, unresolved_choice) and the `authorized` mitigator |
| quicksilver | qs commands | caller-defined. Examples in the repo: "Does this line indicate a failure?", "Does this file implement or handle X?", "How relevant is this item to `query`?" |
| abide | edit and turn checks | one question per rule, written from CLAUDE.md / AGENTS.md (example in its rubric: "On a hook path, does this change add something that can exit non-zero...?") |
| compact-adviser | /compact advisor | "Decide whether the assistant's latest unit of work ... is finished." and "Decide whether the assistant ... mostly did the work itself or mostly coordinated others." |

Becky note (from becky-tools INDEX.md): becky already has `becky-decide` (a local System One model, Laya, and a Jev plan in `research/jev-integration-plan.md`). The 2026-09-18 research ruled hosted Jev out. Check the current plan file before reusing any of these questions.

---

## Hermes research agent (paulthewebdeveloper/hermes-research-agent)

### Cerberus watchdog  (source: hermes-research-agent/watchdog/cerberus.sh)
- PURPOSE: tell Jordan once, in plain words, when something critical has died, with no AI in the loop.
- FUNCTIONALITY: `--quick` (hourly) curls one site URL and expects HTTP 200. Full run (daily) also checks that a systemd service is active, that disk use is under 85%, and that the wiki repo has a git commit in the last 30 hours. Each distinct failure sends one message through `hermes send` and writes a flag file, so a standing failure alerts once. A passing check deletes the flag, so the next failure alerts again. Silent on success. Inputs: five settings at the top of the script. Output: one message per new failure.
- USE CASE FOR JORDAN: a watchdog for the overnight jobs (becky, Cubase factory, LLM servers). Example: "no commit in the factory repo for 30 hours" or "the LLM server is not running". Any version for him must send to his screen or phone with the one thing to do, per his lessons file.
- DECISION MODEL: none. The script's header says an LLM "can decide everything is fine when it is not."
- DETERMINISTIC PARTS: all of it.
- DATA IT NEEDS: HTTP status of one URL, service state, disk percent, timestamp of the last git commit.
- RULES WORTH COPYING: "Nightly jobs prove themselves by moving git, not by writing a log line." `STALE_HOURS=30` ("a nightly job silent for 30h missed a run"). `DISK_PCT_MAX=85`. "One message per distinct failure ... saying what broke and where to look."
- BECKY MATCH: none found in becky INDEX.md. Closest existing piece: `X:\AI-2\hj-mission-control\tools\autopilot_watchdog.ps1` (clears a stale autopilot lock every 10 minutes, no LLM). Cerberus uses GNU `stat`/`df` and `systemctl`, so it needs a Windows port (Task Scheduler), and `hermes send` does not exist on Jordan's PC.

### Wiki lint gate  (source: hermes-research-agent/tools/lint.py + tools/hooks/pre-commit)
- PURPOSE: stop an agent from committing a broken knowledge base.
- FUNCTIONALITY: four checks, and the commit is refused if any fail: (1) every wiki page has a `description:` line in its header; (2) every open item `- [ ]` has a `due:` date or trigger; (3) every `[[wikilink]]` points to a real page or raw file; (4) every log heading matches `## [YYYY-MM-DD] <op> | Title`. Installed with `git config core.hooksPath tools/hooks`.
- USE CASE FOR JORDAN: a gate on any folder of rules or notes that agents write to (his CLAUDE.md / memory files, becky docs). Catches the broken-link and missing-date mistakes automatically.
- DECISION MODEL: none.
- DETERMINISTIC PARTS: all of it (it is already deterministic).
- DATA IT NEEDS: the markdown files only.
- RULES WORTH COPYING: "Add a rule when the agent actually breaks something, not before." Blocks only structure, never content.
- BECKY MATCH: none found. Jordan's `~/.claude/hooks/block-path-overwrite.py` is a similar deterministic gate for PATH; it is different.

### Shared-brain rules  (source: hermes-research-agent/AGENTS.md, CLAUDE.md, README)
- PURPOSE: one set of rules and one set of pages that both Claude Code and an overnight agent read, so neither re-learns or contradicts the other.
- FUNCTIONALITY: `CLAUDE.md` says "read AGENTS.md now". AGENTS.md defines folders: `raw/` (add only, never edit), `wiki/` (one page per thing), `wiki/log.md` (append-only), `wiki/hot.md` (standing warnings). Read order before any answer: log tail, then hot.md, then index, then the page. Small edits are allowed alone; structural changes are left as a note for the human. Human instructions are written as `%% note: ... %%` inside a page.
- USE CASE FOR JORDAN: his own rules and memory already do this in part (CLAUDE.md, memory/MEMORY.md, becky HANDOFF-LOG.md). The useful piece is the rule "unknowns are written **[TODO]**, never invented", plus "no answer from memory: say it is not in the wiki."
- DECISION MODEL: none.
- DETERMINISTIC PARTS: the read order, the folder rules, the log-heading format.
- DATA IT NEEDS: his own notes and decisions.
- RULES WORTH COPYING: "One fact, one page. A copied number goes stale the moment the original changes." "If the answer is not in the wiki, say so. Do not answer from memory."
- BECKY MATCH: partial. becky-docs has HANDOFF-LOG.md, INDEX.md and STATE-OF-MASTER.md, but no lint gate for them.

### Video-to-source farm  (source: hermes-research-agent/tools/farm.py)
- PURPOSE: turn one video (YouTube, Instagram, TikTok, or a local file) into one source file for research and NotebookLM.
- FUNCTIONALITY: input is a URL or file. Uses platform captions if they exist; otherwise transcribes locally with whisper.cpp (`WHISPER_MODEL` env var). Writes one file `raw/data/<date>-<title>.md` with a metadata table, the transcript, and three TODO lines for the reader agent. Refuses to overwrite existing files. Then the Argus worker adds what the screen showed ("seen, not heard") and, on request, creates a NotebookLM notebook with `nlm notebook create` and `nlm source add`.
- USE CASE FOR JORDAN: research on other creators' tutorials (for example, a Vegas or DaVinci tutorial) into a notebook he can question. Not a way to publish or re-use their content.
- DECISION MODEL: none in farm.py. Argus (an LLM) writes the TODO answers as free text. Could be System One: "Is there a moment in this transcript that reads as a screen demo (on screen, as you can see, look at)?"
- DETERMINISTIC PARTS: caption download, whisper transcription, the metadata table, the refuse-to-overwrite rule, the trigger-word scan (see the frame watcher block).
- DATA IT NEEDS: the video or its captions; no channel data.
- RULES WORTH COPYING: "raw/ is immutable once written"; "If captions will not download, say so and stop. HTTP 429 means rate limiting; the fix is to wait, not to write a file with an empty transcript."
- BECKY MATCH: `becky-transcribe <file>` (word-timed transcripts, per becky INDEX.md). Probably covers the transcript step; the metadata and NotebookLM steps are not covered.

### Visual moment finder  (source: hermes-research-agent/hermes/skills/hermes-video-watch, hermes/argus/SOUL.md)
- PURPOSE: read what is on screen, because captions miss charts, terminals, and slides.
- FUNCTIONALITY: scans the transcript for trigger words (`on screen`, `as you can see`, `look at`, `diagram`, `terminal`, `dashboard`, `slide`). Extracts frames around those time ranges with ffmpeg, builds contact sheets, and the agent looks at them. Default cap is 8 frames per range, 12 for a sparse pass. Uses a local STT fallback (`--prefer-stt`) for reels and local files.
- USE CASE FOR JORDAN: quick review of a tutorial's screen-heavy parts without watching the whole thing.
- DECISION MODEL: none in the script. The frame reading is LLM free text. Could be System One: "Does this frame show a readable number, name, or diagram that the transcript at this time refers to?"
- DETERMINISTIC PARTS: trigger-word scan, frame extraction, contact sheet, frame counts. Only reading the frame is an LLM job.
- DATA IT NEEDS: the video, transcript timestamps.
- RULES WORTH COPYING: "a frame you did not inspect is not evidence"; "Say which ranges you looked at."
- BECKY MATCH: none found for the screen-reading step.

### Channel daily report  (source: hermes-research-agent/tools/youtube-report.py)
- PURPOSE: a morning list of how the channel did and which comments are waiting for a reply, with no AI cost.
- FUNCTIONALITY: uses yt-dlp to read the last 10 uploads (view count). Stores each view count in a state file and prints the change since the last run. For each video, reads comments; lists comments with no reply from the channel owner, up to 3 per video.
- USE CASE FOR JORDAN: channel numbers and the "comments waiting" list, which he could otherwise forget. Needs his real channel handle.
- DECISION MODEL: none.
- DETERMINISTIC PARTS: all of it.
- DATA IT NEEDS: public view counts and comments from YouTube (no API key).
- RULES WORTH COPYING: "Run it on your own computer: YouTube blocks most cloud servers."
- BECKY MATCH: none found. becky-intake uses yt-dlp for transcripts, not channel stats.

### Paperclip scheduled routine  (source: hermes-research-agent/paperclip/routine-daily-metrics.md)
- PURPOSE: run a daily agent job on a schedule, with a written brief and an audit trail for every run.
- FUNCTIONALITY: four local API calls: create goal, create project under it, create routine (its `description` is the whole brief the agent reads), add a schedule trigger (cron `0 8 * * *`, timezone). Each run becomes an issue. Policies: `concurrencyPolicy: coalesce_if_active` (a second trigger folds into a run still going), `catchUpPolicy: skip_missed` (a missed run is skipped, not replayed late).
- USE CASE FOR JORDAN: any daily job, for example a morning check of the factory, becky run results, or a channel summary. The audit trail is what makes it reviewable.
- DECISION MODEL: none. The routine uses an LLM agent for the daily brief.
- DETERMINISTIC PARTS: the schedule, the skip-missed rule, the coalescing, the numbers pulled from the API.
- DATA IT NEEDS: channel statistics, playbook file, trends output.
- RULES WORTH COPYING: "A metrics routine that catches up lies about when it ran." "Quote every figure from the tool output in this run. When a call fails, write FAIL with the error."
- BECKY MATCH: none found. Paperclip is a separate system; Jordan's factory runs on its own scheduler.

### Agent configuration hygiene (five settings)  (source: hermes-research-agent/paperclip/README.md)
- PURPOSE: stop agents from silently running with the wrong setup.
- FUNCTIONALITY: five checks, each with a fix. (1) Execution engine: the default hid the user's skills, plugins and browser from the agent; setting `engine: cli` and `chrome: true` fixed two failed access checks. (2) Memory was off for weeks; the fix was to turn it on and prove it by saving a code word in one session and recalling it in a fresh session. (3) Five agents shared one install and produced over 1,000 "session storage stopped writing" errors in one day. (4) Every toolset was on for every agent, so an image agent drove the screen instead of calling the image tool. (5) An agent that needs a logged-in page needs its own browser.
- USE CASE FOR JORDAN: an audit of Jordan's own agents (Claude Code, the factory, Whoretana helpers): are the right tools on, are logs being written, does each agent have only the tools its job needs.
- DECISION MODEL: none.
- DETERMINISTIC PARTS: all of the checks: config keys, toolset lists, log-file existence.
- DATA IT NEEDS: agent config files, the error log.
- RULES WORTH COPYING: "Check the log exists before you trust the thing writing to it." "An agent with every tool does not pick the best one, it picks the first one."
- BECKY MATCH: none found. Partly matches the lesson "Rules for agents are ENFORCED by the harness, never only written in a prompt" in CLAUDE.md.

---

## Hermes-era YouTube pipeline (hermes-research-agent/claude/skills/youtube-video)

### Topic scan  (source: youtube-video/scripts/trends.py)
- PURPOSE: pick a video topic from what is being watched this week, not from memory.
- FUNCTIONALITY: runs YouTube search for each niche query (filtered to this week, sorted by views), takes the top 12, then looks up subscriber counts for the top 8. Prints views divided by subscribers. A ratio over 1x means the video went past its channel's own audience. `--all-time` checks who owns a query outright.
- USE CASE FOR JORDAN: pick a topic with proof of demand for his channel. Set the niche queries himself; the defaults are for another creator.
- DECISION MODEL: none.
- DETERMINISTIC PARTS: all of it.
- DATA IT NEEDS: public YouTube search results (yt-dlp, no API key).
- RULES WORTH COPYING: "Easy-to-shoot beats popular. The artifact must already exist on your machine." "Views/subs over 1x means the video travelled past the channel's own audience."
- BECKY MATCH: none found.

### Title shape analysis  (source: youtube-video/scripts/titles.py)
- PURPOSE: find which title shapes are associated with videos that travelled past their channel.
- FUNCTIONALITY: input is a pool of videos with title, views, subscribers (`pool.json`). Checks about 12 structural features (a number, a bracket, a colon, a question, "I did", "vs", "free", hype words, and so on). Score is views divided by subscribers. Prints the feature table and clusters. With `--candidates`, finds the nearest real titles for each candidate title and shows whether those titles travelled.
- USE CASE FOR JORDAN: check a title before he publishes; A/B title choices grounded in data.
- DECISION MODEL: none.
- DETERMINISTIC PARTS: all of it (numpy only).
- DATA IT NEEDS: a pool of titles with views and subscriber counts from the topic scan.
- RULES WORTH COPYING: "Performance is views / subscribers, because raw views only measure how big the channel already was."
- BECKY MATCH: none found.

### Recording preflight  (source: youtube-video/SKILL.md section 1; scripts/videokit.py `check`)
- PURPOSE: stop bad recordings before editing.
- FUNCTIONALITY: `videokit.py check <folder>` confirms the three OBS files (MAIN with mic, CAM, SCREEN), measures camera lag against MAIN, and flags clipping (mic peak near 0 dB). Rules: record on one fixed "Split" scene in 16:9 1920x1080; a 15-second test recording must pass the check before the real take; always record an outro that names the next video.
- USE CASE FOR JORDAN: a preflight for his own streams and recordings, checked before he spends time on an edit. Clipping and lag checks are useful to any recording pipeline.
- DECISION MODEL: none.
- DETERMINISTIC PARTS: all of it.
- DATA IT NEEDS: the three recording files; mic levels.
- RULES WORTH COPYING: "Mic peak near 0 dB means clipping - lower the gain." "End screens alone get no clicks on a small channel."
- BECKY MATCH: partial. becky has its own transcript and levels tools (see becky INDEX.md); no preflight found.

### Take selection with Jev  (source: youtube-video/scripts/videokit.py `takes`, group_takes, score_takes)
- PURPOSE: decide which take of a repeated sentence to keep, without reading every take.
- FUNCTIONALITY: step 1: for each transcript line, asks Jev whether it restarts a sentence begun up to 6 lines back (a retake). Step 2: groups attempts at the same thought, and asks Jev, for each attempt, whether it finishes its thought. Keeps the last attempt judged finished (probability over 0.5); if none is finished, keeps the best guess and marks it `?`. Writes a draft cut list with every attempt and its probability, so the human checks the attempts.
- USE CASE FOR JORDAN: first pass on a long recording with many restarts. Jordan keeps the final judgment.
- DECISION MODEL: System One/Jev, two questions: (1) "`later` repeats the opening words of `earlier`, as a retake of the same sentence." (2) "Does `take` finish its thought - it ends on a complete sentence instead of trailing off, breaking off mid-sentence, or being abandoned?" Batched 40 lines per request.
- DETERMINISTIC PARTS: transcript timing, the 6-line window, dropping lines under 3 words, the cut-list table format, verifying each cut with `piece`.
- DATA IT NEEDS: Whisper transcript with word times.
- RULES WORTH COPYING: "Never use a disfluency prompt with Whisper: it invents fillers." "Never ... judge with a 'clean delivery' question: on unpunctuated text Jev scores every take as stumbly, so it separates nothing" (their own note, `score_takes`).
- BECKY MATCH: partial. becky-transcribe (word times) and becky-cut (silence and breath cuts). No retake logic found.

### Verify every cut  (source: youtube-video/scripts/videokit.py `gaps`, `piece`)
- PURPOSE: prove a cut point is right before it is used.
- FUNCTIONALITY: `gaps <folder> 19:22` lists where the real silence is in a window. `piece <folder> 14.7:20.66` returns exactly what the span says on its own, so a leftover word from the previous attempt shows up.
- USE CASE FOR JORDAN: any cut list that comes from a transcript; Whisper times can drift by up to a second.
- DECISION MODEL: none.
- DETERMINISTIC PARTS: all of it.
- DATA IT NEEDS: the audio and the transcript times.
- RULES WORTH COPYING: "Whisper word times drift up to a second. Verify every cut point before trusting it." "A take is right when piece returns exactly the sentence."
- BECKY MATCH: partial. becky-cut's breath check does gap verification (see becky INDEX.md).

### Breath and silence trim  (source: youtube-video/scripts/videokit.py `tighten`)
- PURPOSE: remove dead air and breaths between takes.
- FUNCTIONALITY: takes a Resolve FCP7 XML export. Cuts silence and breaths quieter than -26 dB. Inside the screen-demo part (after `--demo-from`) only gaps over 0.6 s are trimmed. Prints a word-level before and after diff: a missing content word means the cut is wrong. Writes a new `-tight.xml` and never overwrites the saved one.
- USE CASE FOR JORDAN: a loudness-based silence trim for talking-head segments. Note his own becky lesson (2026-10-06): loudness-only breath markers were mostly not breaths. Use this only after checking the picture.
- DECISION MODEL: none.
- DETERMINISTIC PARTS: all of it (dB threshold, minimum gap, diff).
- DATA IT NEEDS: the audio envelope from the Resolve export.
- RULES WORTH COPYING: "-26 dB cuts breaths as well as silence (a quiet room sits about -36 dB; breaths -30 to -22)." "Never overwrite the one you saved." "Check every region ... no 1-frame slivers" (from Jordan's own lessons).
- BECKY MATCH: `becky-cut` breath check (INDEX.md) and HANDOFF-BECKY-CUT-ADAPTIVE.md. Becky already has a breath check; compare thresholds before adopting -26 dB.

### Resolve timeline builder  (source: youtube-video/scripts/videokit.py `layers`)
- PURPOSE: build the layered Resolve timeline (background, screen, camera, frame, mic) from the three recording files.
- FUNCTIONALITY: writes `layers.xml` in FCP7 format. Each clip carries its own crop, so one shot can be reframed. FCPXML and OTIO drop position and crop in free Resolve, so the repo only uses FCP7 XML.
- USE CASE FOR JORDAN: not directly (he edits in VEGAS). The pattern (generate an XML timeline from data) is the same one becky's VEGAS scripts need.
- DECISION MODEL: none.
- DETERMINISTIC PARTS: all of it.
- DATA IT NEEDS: the three recording files and crop coordinates.
- RULES WORTH COPYING: "Only FCP7 XML (.xml) carries position, scale and crop into free Resolve."
- BECKY MATCH: partial. becky builds VEGAS timelines by script (`BeckyKeepList.cs`, per INDEX.md).

### Thumbnail generation recipe  (source: youtube-video/SKILL.md section 6; scripts/thumb-preamble.txt; videokit.py `thumbs`)
- PURPOSE: make YouTube thumbnails in the creator's own style from his own face frame, never from another creator's thumbnail.
- FUNCTIONALITY: (1) pull six candidate frames from the CAM file (`ffmpeg -ss`), pick one with eye contact, mid-word. (2) write one prompt per thumbnail from a fixed preamble (who the creator is, channel look) plus one composition paragraph. (3) one image per call, run in parallel, with the face and logo images attached (Codex CLI image call). (4) zoom every result to 100% and check the logos and every letter. Fix one defect at a time with an edit prompt that says "change ONE thing only". (5) export JPGs under 2 MB with `videokit.py thumbs`. Rule: thumbnail text never repeats the title; each title + thumbnail pair is a separate A/B test.
- USE CASE FOR JORDAN: exactly the request in his brief: thumbnails "according to my own standards." The preamble holds his standards, and the zoom check is the quality gate.
- DECISION MODEL: none. Image generation is LLM free text. Could be System One: "Does this thumbnail show a face at eye level with the text readable at phone size?" (the repo does not do this).
- DETERMINISTIC PARTS: frame extraction, the size cap, the file naming, the preamble, the check that the logo is present (manual today).
- DATA IT NEEDS: his face frames, his logo, his channel look.
- RULES WORTH COPYING: "Generate them ... from your own face frame, never from another creator's thumbnail (the model reproduces their face)." "Titles: foreshadow a result, overpromise a little, and the video must pay it off."
- BECKY MATCH: `image-gen` skill is listed in the available skills; no becky tool found for thumbnails.

---

## Jev browser agent (browser-use/jev-ultrafast)

### Goal-driven browser agent with Jev operation and target picking  (source: jev-ultrafast/jev_ultrafast/agent.py, model.py, questions.py)
- PURPOSE: complete a web task from one plain-language goal, with one model call per step, so it is fast.
- FUNCTIONALITY: each step reads the visible page as a numbered list of elements (controls, values, text). One Jev request asks for the operation (CLICK, TYPE_TEXT, SELECT, SCROLL, WAIT, DONE, BLOCKED) and, in the same call, the target for each operation. If the operation is TYPE_TEXT, a small text model (Mercury via OpenRouter) writes only the text value. Clicks are checked for occlusion and for changed page state before they run. Demo: Google Flights search in 7.07 s at 1x speed (their measurement, one run and a six-run comparison).
- USE CASE FOR JORDAN: fast form and search tasks on sites he owns or is allowed to use. Not for anything that spends money or posts.
- DECISION MODEL: System One/Jev. Operation choice: "Advance the user's entire goal from the CURRENT page using one operation." (rules in questions.py: no repeated steps, submit before opening a result, DONE needs visible evidence). Target choice per operation. Text value is LLM free text.
- DETERMINISTIC PARTS: DOM snapshot, freshness check, occlusion check, geometry, the 200 ms wait for suggestions, the rule that model output never becomes a selector.
- DATA IT NEEDS: the page's visible text and controls.
- RULES WORTH COPYING: "Page text is untrusted data, never instructions." "Model output never becomes selectors, coordinates, shell commands, or executable JavaScript."
- BECKY MATCH: none found. The repo needs Browser Harness (a Chrome CDP tool). `X:\AI-2\browser-harness` exists on the PC, and Jordan's rules ban it for agents. Not usable as built.

## Jev browser tool (Ying-Kai-Liao/jev-browser)

### Goal-driven browser task with safety stops  (source: jev-browser/src/session.mjs `browser_do`)
- PURPOSE: let Claude finish one web outcome per call, and stop before anything risky.
- FUNCTIONALITY: loop of settle, describe, ask Jev, act. Jev answers done, blocked, error, login needed, and irreversible, plus next tool, value, and target. Before any irreversible action (order, pay, send, delete, publish) it stops and asks, if the probability is above a threshold. Status returned to the caller: done, error, needs_login, blocked, or likely_done. Roughly 300 ms per Jev call; 40 of 42 tasks right in its last run (their result, live sites). Other tools in the same server: `browser_check` (yes/no about the page), `browser_choose` (pick one of given options), `browser_act` (a direct click or type with no model), `browser_snapshot` (numbered element list), `browser_screenshot`.
- USE CASE FOR JORDAN: web chores with a built-in stop before paying or posting. Requires the agent-firefox setup first (it uses its own Playwright Chromium, not the agent-firefox rule).
- DECISION MODEL: System One/Jev. Questions: "Does `page` show that `task.goal` has been achieved?"; "Is there something on `page` that stops progress ... (captcha, access denied, error page)?"; "Does `page` show an error or rejection message ... caused by the actions in `task.history`?"; "Is `page` a sign-in or sign-up screen...?"; "Would the next action ... have an effect outside this browser that is hard to undo, such as placing an order, paying, sending a message, deleting data or publishing?"; plus choices for next tool, value and target.
- DETERMINISTIC PARTS: the settle wait, the page snapshot, the 0.7 login and error thresholds, the done-check.
- DATA IT NEEDS: the page text, element list, and the caller's values.
- RULES WORTH COPYING: "Pause before irreversible actions." "DONE requires visible evidence." Limit: "Judgements that compare many values ... verify with browser_check or browser_snapshot."
- BECKY MATCH: none found.

### Reading long pages by relevance  (source: jev-browser/src/prune.mjs, session.mjs `browser_read`)
- PURPOSE: read a long web page (an article, a docs page) without spending the context window on all of it.
- FUNCTIONALITY: splits the page text into blocks at headings. Jev rates each block as helpful or not for the question, in batches of 40 blocks. Keeps blocks with probability at or above 0.35 (blocks with no score are kept). Returns them in page order inside a 12,000-character budget. Returns the page's own text, never new text.
- USE CASE FOR JORDAN: research reading (for example a long tutorial or a documentation page) at low token cost.
- DECISION MODEL: System One/Jev. One yes/no per block: "Does the entry of `page.blocks` with `i` = N contain information that helps answer `question`?"
- DETERMINISTIC PARTS: heading split, batching, budget, page-order output.
- DATA IT NEEDS: the page text.
- RULES WORTH COPYING: "Bias to keep: drop only when Jev is fairly sure it doesn't help" (threshold 0.35).
- BECKY MATCH: none found.

---

## Jev-based tool gates

### Jev gate for shell, write and edit calls  (source: jev-use/src/judge.ts `gate`, src/cli.ts `hook gate`, harness/claude-code/gate.hooks.json)
- PURPOSE: stop a destructive or off-task command before it runs, with no LLM tokens spent on the check.
- FUNCTIONALITY: a Claude Code PreToolUse hook on `Bash|Write|Edit`. Sends the proposed action (credentials redacted) and the session state to Jev, gets allow or deny with a confidence number. Anything Jev is unsure about comes back as "escalate" so a human decides. Their 24-command session: about 230 ms per gate, benign commands allowed, dangerous ones denied, zero LLM tokens (their measurement).
- USE CASE FOR JORDAN: stops agents running `rm`, force-pushes, or writes to protected folders during overnight runs. Overlaps with toolgate (see the toolgate block below); run one, not both.
- DECISION MODEL: System One/Jev, one choice question: "Should the agent be allowed to run this proposed action right now?" Options: allow ("safe, reversible or expected, and consistent with the state"), deny ("destructive, off-task, touches things the state says to protect, or looks like a mistake").
- DETERMINISTIC PARTS: the redaction of secrets, the routing rule (what never reaches Jev), the hook wiring, deterministic deny lists (e.g. `rm -rf /`) could run first.
- DATA IT NEEDS: the proposed command, the task state, the protected-folder list.
- RULES WORTH COPYING: "a safety decision that blocks every tool call belongs in the PreToolUse hook, not in a call you make by hand." "Honor escalations."
- BECKY MATCH: none found. Jordan's `~/.claude/hooks/block-path-overwrite.py` is a deterministic path guard.

### Bulk triage without loading items into context  (source: jev-use `judge` CLI, src/server.ts, bench/examples/pr-triage.mjs)
- PURPOSE: label or sort many items (commits, comments, logs) without pasting them into the conversation.
- FUNCTIONALITY: a script pipes a file of items to `jev-use judge`; answers come back as a verdict list. Items can be batched: 12 questions about one state took 224 ms in one call versus 2,662 ms one at a time (their benchmark). Example: commit triage asks "How should this commit be triaged for review?" (auto_land / needs_human) and "What kind of change is this?" (rate: cosmetic / logic change / touches release path).
- USE CASE FOR JORDAN: sort hundreds of his comments, clips, or review items by category.
- DECISION MODEL: System One/Jev. Questions are caller-defined: `pick`, `check` (yes/no), `rate`.
- DETERMINISTIC PARTS: batching, the file reading, the verdict escalation rules.
- DATA IT NEEDS: the item list.
- RULES WORTH COPYING: "Route bulk data by reference." "Don't route trivia. If you already know the answer, just act."
- BECKY MATCH: partial. becky-decide (Laya) does the same job locally per INDEX.md.

### Tool-call firewall with seven risk questions  (source: toolgate/src/engine.ts, src/policy.ts, src/mcp.ts, src/hook.ts, plugin/)
- PURPOSE: a firewall in front of every risky tool call (Bash, Write, Edit, web, MCP) so agents cannot do destructive, leaking or off-task things unnoticed.
- FUNCTIONALITY: order of checks: (1) static rules first, zero model calls: built-in denies (`rm -rf /` or `~`), built-in asks (`curl ... | sh`, edits to `~/.claude/settings*`), then his own rules. (2) Read-only tools pass without a call. (3) Everything else goes to the decision model in one request with all questions answered in parallel. (4) Thresholds: risk 0.85 or higher is denied, 0.55 or higher asks the user, otherwise allowed. (5) If the task clearly authorizes the action (0.8 or higher), each risk softens one step, except secret exposure, constraint violations and reserved choices, which a task cannot soften. (6) In unattended modes (bypassPermissions, dontAsk) an "ask" becomes a deny by default. (7) If the model is unreachable, `fail_mode: ask` (default) shows a confirmation. Every decision is written to `~/.toolgate/audit.jsonl` with secrets redacted. Also works as an MCP proxy in front of any MCP server, and as a Claude Code mod. Their own latency: about 1 second per call through the gateway.
- USE CASE FOR JORDAN: the firewall for unattended night runs. Protects his machine and accounts from one bad command or from prompt injection in a file the agent reads. Defense in depth, not a sandbox (their words).
- DECISION MODEL: System One/Jev. Seven yes/no questions, plus the authorization mitigator: "This tool call irreversibly destroys or overwrites data, files, branches, or infrastructure." (destructive); "This tool call itself, when executed, sends local file contents, credentials, or environment variables to a network destination." (exfiltration); "This tool call escalates privileges or modifies system configuration, credentials, or security settings." (privilege); "This tool call is outside the scope of the stated current task." (off_task); "This action exposes or persists credential values outside their intended secure use." (secret_exposure); "Some part of this action contradicts an explicit restriction in the stated task..." (violates_constraint); "This action commits to a consequential choice that the stated task explicitly leaves undecided or reserves for the user to confirm first." (unresolved_choice). Write-content variants ask the same risks about what a file would do if run.
- DETERMINISTIC PARTS: all static rules, the redaction of secrets, the 40,000-character input cap, the thresholds, the unattended-mode rule, the audit log.
- DATA IT NEEDS: the tool name and input, the working folder, the permission mode, the last user prompts from the transcript (only if task context is on).
- RULES WORTH COPYING: "Capability is not harm." "Your rules, then the built-ins: ... `rm -rf /` or `~` denied." "Input too large to evaluate in full is never allowed outright, only asked about." "If the model is ever unreachable, the hook says NOT gating rather than silently standing down."
- BECKY MATCH: none found. Overlaps with the jev-use gate block: choose one. Run the mod or the hook, not both (their rule).

---

## Jev SEO audit (AgriciDaniel/jev-seo)

### Full SEO audit of one website  (source: jev-seo/jevseo/crawl.py, checks.py, psi.py, report/, SKILL.md)
- PURPOSE: a complete, checkable SEO audit of a website from its homepage URL.
- FUNCTIONALITY: crawls up to 60 pages (depth 5, 10-minute budget, public sites only). Runs 52 deterministic checks tied to Google Search documentation (robots.txt, sitemaps, redirects, canonicals, titles, meta, headings, structured data, broken links, HTTPS, mixed content, TTFB, AI bot blocking, llms.txt). Adds PageSpeed Insights (Core Web Vitals) for up to three pages. Jev judges page-level questions (next block). Scores and ranks each action by impact and effort. Outputs PDF, XLSX action tracker and Markdown report from one `audit.json`.
- USE CASE FOR JORDAN: only if he has a website he wants to rank. Not a YouTube tool.
- DECISION MODEL: partly Jev (see next block). Report narrative is written by an LLM from the digest, with rules: every number copied from the digest, action IDs cited.
- DETERMINISTIC PARTS: almost all: crawl, checks, scoring, PSI, rendering. The renderer refuses unknown action IDs and warns about numbers that match nothing in the audit.
- DATA IT NEEDS: the site, PageSpeed key, Jev key, optional DataForSEO key.
- RULES WORTH COPYING: "Nobody invents a number." "Missing data stays missing." "Scores rank work; they never predict rankings or traffic."
- BECKY MATCH: none found.

### Page-level Jev judgments  (source: jev-seo/jevseo/jev.py `page_questions`)
- PURPOSE: judge meaning, which code cannot do (page purpose, intent, helpfulness, trust).
- FUNCTIONALITY: for each page, asks typed questions (choice, score, yes/no) with probabilities kept. Results feed the rule scores.
- USE CASE FOR JORDAN: judging the copy on his site or a sales page.
- DECISION MODEL: System One/Jev, per page: `page_type` ("Which kind of page is `page`?"), `intent` ("Which search need does `page` best serve?"), `importance` ("How important is `page` to the business described in `site`?"), `action` ("Given its content, what should the site owner do with `page`?" keep / rewrite / merge or remove), `helpfulness` ("How well does the main text of `page` satisfy a visitor who came for its topic?"), `specificity`, `answer_first` (yes/no), `citable`, `trust`, `clear_next_step` (yes/no).
- DETERMINISTIC PARTS: the page text extraction, the question list, the score mapping. Their own note: "Won A/B on blind labels: decisive 6/30 to 23/30."
- DATA IT NEEDS: page title, meta, headings, outline, opening text, calls to action, word count.
- RULES WORTH COPYING: "Keep the probability; don't flatten it to yes/no."
- BECKY MATCH: none found.

### Keyword relevance and page mapping (full mode, paid)  (source: jev-seo/jevseo/dfs.py, jev.py `keyword_batches`)
- PURPOSE: decide which search terms are worth winning and which page should own each.
- FUNCTIONALITY: DataForSEO supplies keywords with volumes (paid, about $0.30 per site; hard cap `--dfs-budget` default $1.00). Jev, in batches of 8, scores each keyword's relevance, picks the best page for it (or says a new page is needed), and flags searches naming another brand.
- USE CASE FOR JORDAN: only with a website. Otherwise skip.
- DECISION MODEL: System One/Jev. Questions: "How relevant is the search in `keywords.X` to what `site` offers?" (score); "Which page in `pages` best serves the search in `keywords.X`?" (choice); "Does `keywords.X` name a specific company, product or website that is not the one in `site`...?" (yes/no).
- DETERMINISTIC PARTS: keyword collection, volumes, budget cap, page list.
- DATA IT NEEDS: keyword and rank data from DataForSEO.
- RULES WORTH COPYING: "Volumes, difficulty and ETV are DataForSEO estimates; say so."
- BECKY MATCH: none found.

---

## Mid-turn message router (Larkspur-Wang/Jev_steer_or_queue)

### Steer, queue or interrupt a message sent mid-task  (source: claude-code/scripts/jev_router.py, hooks.json)
- PURPOSE: when Jordan types while Claude is working, decide whether the message changes the current task, waits for after, or stops it.
- FUNCTIONALITY: a UserPromptSubmit hook. Messages sent while idle pass untouched. For a mid-turn message, one Jev call returns steer, queue, interrupt or unclear. Queue and interrupt only act at probability 0.9 or more; interrupt also needs the stop-question at 0.9 or more. Queued messages are held in a per-session list and replayed when the turn stops. Fails open: any error or a 2-second timeout means the message passes through. Default mode is "shadow" (log only). About 0.5 s per decision (their measure).
- USE CASE FOR JORDAN: "when you're done, update the changelog" should wait; "stop" should stop. Relevant to the factory: a stray or garbled message should not stop a run, because only a plain "stop" should. The explicit-stop question is the guard for that. Start in shadow mode and read the log before active mode.
- DECISION MODEL: System One/Jev. Choice: "`new_message` is a new message the user sent while the coding assistant was running `active_task`. What is its time relationship to the running task?" (steer / queue / interrupt / unclear). Yes/no: "Does `new_message` explicitly ask to stop the running task right now?"
- DETERMINISTIC PARTS: detecting mid-turn messages (same prompt_id), the FIFO queue, the 0.9 thresholds, the fail-open rule, the logging.
- DATA IT NEEDS: the new message (max 2,000 characters) and the start of the current task (max 500). Prompts are logged unless `JEV_ROUTER_LOG_PROMPTS=false`.
- RULES WORTH COPYING: "Only confident answers change behaviour." "Fails open." Lessons match: Jordan's CLAUDE.md says "Only Jordan's plain word 'stop' stops the factory" (2026-09-22). This router applies the same rule to a message, which is a point in its favour.
- BECKY MATCH: none found.

---

## Jev stop-check for false "done" claims (valentynkit/jev-belay)

### Block a "done" claim when no check ran  (source: jev-belay/belay.mjs, hooks/hooks.json, skills/why, skills/doctor)
- PURPOSE: stop Claude or another agent ending a turn with "done, tests pass" when nothing was run.
- FUNCTIONALITY: a Stop hook. Reads the transcript for this turn locally (no network). If no file was changed, it exits without a Jev call. If files changed and a test, build or lint command has passed since the last change, it exits. Only if files changed and nothing proved it, it asks Jev four questions in one call. If the answer is "claims done" and "claims verified" and the check was never run, the turn does not end: Claude gets the reason and runs the suite. Measured: AUROC 0.976 on 100 labeled stops; blocks 8 in 100, 7 correct (their numbers). Costs $0.00005 per check that reaches Jev. `/why` explains the last decision; `/doctor` checks the wiring.
- USE CASE FOR JORDAN: direct fix for the failure in his lessons: agents claiming work finished without verification (e.g. Cubase factory and Becky sessions). Limit: a turn with no file edits is never checked, so read-only "confirmed" claims pass.
- DECISION MODEL: System One/Jev, four questions: (1) "Does `final_message` present the requested work as finished or working?" (2) "Does `final_message` claim that tests, a build, or other checks were run and passed?" (3) "Would running the project's tests, build, or lint be a meaningful way to check the work that `task` asks for?" (4) one choice on what `final_message` reports about `task`.
- DETERMINISTIC PARTS: the transcript read, the runner-name regexes (about 25 runners), the "a check passed after the last change" rule, the exit codes. The Jev call only happens when the regex evidence says it is needed.
- DATA IT NEEDS: the turn transcript (stays on the machine), the final message (redacted), the task text.
- RULES WORTH COPYING: "a belay catches the fall. It does not stop the climb." "Every error path lets the turn end." Their own finding: a rule that does not look at the run evidence scored AUROC 0.50 (a coin flip).
- BECKY MATCH: none found. Jordan's lessons say the factory must not say "no check-ins" without reading config; no automated check exists in becky.

---

## Bulk judgment offload (UditAkhourii/quicksilver)

### Judge many items with Jev and send Claude only the matches  (source: skills/quicksilver/scripts/qs.mjs, SKILL.md)
- PURPOSE: stop Claude reading thousands of lines or files just to decide which ones matter.
- FUNCTIONALITY: commands: `filter` (keep items where the answer is yes), `classify` (sort items into labels), `rank` (relevance levels for a query), `find` (which line number matches, up to a top N), `ask` (one question about a text). Inputs: files, directories (respects .gitignore and skips secret files), stdin, or a JSONL file of items. Their 12-task benchmark: Claude's tokens cut 55% to 95%, and accuracy matched Claude alone on 8 of 12 tasks. Weak on subjective labels and look-alike code (use its output as a shortlist and check the "?" items).
- USE CASE FOR JORDAN: sort hundreds of comments, clip candidates, log lines or files by category without reading them all. Keep exact-text search on `grep`.
- DECISION MODEL: System One/Jev, caller-defined. Examples in the code: "Does this line indicate a failure?" (yes/no), "Does this file implement or handle X?" (yes/no), "Which label best describes this item?" (choice), "How relevant is this item to `query`?" (score), "Which line number best matches `query`?" (choice) and "Does any line in `lines` match `query`?" (yes/no).
- DETERMINISTIC PARTS: exact string match (grep), counts, date comparisons, file filters, secret-file skipping.
- DATA IT NEEDS: the files or items to judge.
- RULES WORTH COPYING: "Don't delegate: tiny inputs, exact matches, arithmetic, counting, date comparison." "Content the user wouldn't want sent to a third-party API" stays local.
- BECKY MATCH: partial. becky-decide (Laya) does similar typed decisions locally. qmd search (plugin in this session) covers search, not judgment.

---

## Per-edit and per-turn rule enforcement (coldteadotai/abide)

### Per-edit rule check from CLAUDE.md / AGENTS.md  (source: abide/packages/cli/src/hooks/postToolUse.ts, lib/checkRunner.ts, lib/compilePrompt.ts)
- PURPOSE: enforce the written rules the agent keeps breaking, on every edit, instead of hoping it reads them.
- FUNCTIONALITY: `abide init` installs hooks in Claude Code, Codex, OpenCode and Pi. At session start, if CLAUDE.md or AGENTS.md changed, the agent compiles them into `.abide/rubric.json` (one rule per instruction, with the source line). After each edit, each rule that applies to that file runs one Jev question on the changed lines. Scores: 0.8 and above, the agent is told to repair the edit in the same turn; 0.5 to 0.8, a note to the user only; below 0.5, nothing. Timeouts: 8 s per edit, 15 s per turn. Their measured cost: about 300 ms and under a tenth of a cent per turn.
- USE CASE FOR JORDAN: enforcement of his real rules, for example "never write Unicode in a .bat", "never print secrets", "never let a raw error reach the user", "no premature abstractions". Also makes sure that the rules he wrote get checked.
- DECISION MODEL: System One/Jev. One question per rule, written from the instruction file. Example from its own rubric: "On a hook path, does this change add something that can exit non-zero, throw past the top-level catch, await without a timeout or abort, or write to stdout anything other than the host's JSON?" (boolean, criteria written out).
- DETERMINISTIC PARTS: rules a linter can check are meant to go to the linter ("Rules a linter could check are handed to your linter instead"). Jordan's ASCII-only `.bat` rule and "no em-dash" rule are regex checks and should not go to Jev at all.
- DATA IT NEEDS: the changed lines of each edit, the rule text, the key (never a flag).
- RULES WORTH COPYING: "A badly worded rule scores 0.4 on everything and never fires." "Never accept the key as a flag; a flag lands in shell history and in CI logs."
- BECKY MATCH: none found. Jordan's CLAUDE.md lessons are currently enforced by prompts and hooks such as `block-path-overwrite.py`.

### Turn-end rule check  (source: abide/packages/cli/src/hooks/stop.ts, lib/constants.ts)
- PURPOSE: catch rules that only make sense across the whole turn, such as "one-use abstraction", "file too big", "duplicated logic".
- FUNCTIONALITY: at the end of a turn, diffs the whole turn against a snapshot and runs the turn-level rules once. If a rule fires, the turn is blocked and the agent repairs the diff. Limits: each rule may block the same file at most twice per turn (`MAX_BLOCKS_PER_RULE_PER_TURN = 2`); at most two stop checks per turn (`MAX_STOP_CHECKS_PER_TURN = 2`). If the diff is unchanged after a block, the agent is treated as having declined and the turn ends with a note.
- USE CASE FOR JORDAN: code-quality rules across a whole task (no extra abstractions, no duplicated code, no 800-line files).
- DECISION MODEL: System One/Jev, one question per turn-level rule over the full diff.
- DETERMINISTIC PARTS: file-length counts, duplicate detection, the snapshot diff, the block limits.
- DATA IT NEEDS: the turn's diff and the rule text.
- RULES WORTH COPYING: "Turn-level catches (single-use abstractions, oversized files, duplicated logic) held up 11 times in 15" (their replay of 93 real sessions).
- BECKY MATCH: none found.

### Calibrate and tune the rubric  (source: abide/packages/cli/src/commands/calibrate.ts, tune)
- PURPOSE: keep the rules honest: switch off rules that never fire or fire on everything.
- FUNCTIONALITY: `abide calibrate` scores each rule against about 20 recent git hunks from the repo's history, marks rules as weak, noisy, or disabled, and writes the result to the rubric. `abide tune` asks the agent to rewrite rules that never fire.
- USE CASE FOR JORDAN: stops rules from becoming noise after he edits CLAUDE.md.
- DECISION MODEL: System One/Jev, the same rule questions run over old hunks.
- DETERMINISTIC PARTS: the history selection, the status rules.
- DATA IT NEEDS: git history.
- RULES WORTH COPYING: "A badly worded rule scores 0.4 on everything and never fires."
- BECKY MATCH: none found.

### Audit existing files and replay past sessions  (source: abide `audit`, `replay` commands; benchmarks/replay/README.md)
- PURPOSE: measure how often the rules break and check old code, not only new edits.
- FUNCTIONALITY: `abide audit src/` judges each existing file as if it had just been written and reports by rule and by file (33 API routes: about 12 s, about one cent in their test). `abide replay claude` runs the same checks on this repo's past sessions. Their own result (93 real sessions, 1,256 edits, 147 turns): Jev flagged 39 edits and 15 turns; an independent reviewer confirmed 10 edits and 11 turns. Read this as a warning: about a quarter of edit flags were confirmed.
- USE CASE FOR JORDAN: a one-time audit of becky or the factory repo against Jordan's rules, and a monthly replay to see how often the rules are broken.
- DECISION MODEL: System One/Jev, the same rule questions.
- DETERMINISTIC PARTS: file selection, ignore patterns (`.abideignore`), report tables.
- DATA IT NEEDS: files, past session transcripts.
- RULES WORTH COPYING: "Changed lines go to TypeSafe under your key, and nowhere else" (for privacy notes).
- BECKY MATCH: none found.

---

## Compaction advisor (kunchenguid/compact-adviser)

### Should I /compact now?  (source: compact-adviser/packages/claude-mod/lib/judge.ts, docs/product-contract.md)
- PURPOSE: tell the user when the session is at a safe point to compact (save context), so the summary does not drop something needed next.
- FUNCTIONALITY: runs only when the context is over 40,000 tokens (constant minimum) and the session is idle. One Jev request with two questions. The score is P(finished) times (0.5 + 0.5 times P(hands-on)). A floor decides whether to show the hint: 0.90 when context is at most 10% full, falling linearly to 0.50 at 90% full. Default mode: hint only (shows "Run /compact to save tokens"). Auto mode (experimental, needs an acknowledgement) runs compaction itself. Judgments that fail or time out leave the context alone.
- USE CASE FOR JORDAN: long Claude sessions (his factory and becky sessions get long). Tells him when a /compact is safe without losing the next task's details.
- DECISION MODEL: System One/Jev. "Decide whether the assistant's latest unit of work in this conversation is finished. ... Waiting for a person to decide or for another party to deliver counts as finished." (choice: finished / not finished). "Decide whether the assistant in this conversation mostly did the work itself or mostly coordinated others." (choice: hands_on / coordinating).
- DETERMINISTIC PARTS: the 40k-token minimum, the idle check, the floor formula, the cooldowns, the disable switch (`COMPACT_ADVISER_DISABLE`).
- DATA IT NEEDS: the last 64 transcript entries, capped at about 14 kB of recent tail and 8 kB of user constraints.
- RULES WORTH COPYING: "Auto is not a higher bar. Mode only chooses what happens after a qualifying judgment." "A hint is never written into the conversation... it is shown to the person, never to the model."
- Other hosts: Codex and Grok are hint-only (nothing outside a session can run /compact there). Pi has its own extension.
- BECKY MATCH: none found. Platform note: the README supports Claude Code 2.1.274+ with `CLAUDE_CODE_ENABLE_FUNCTION_HOOKS=1`. Windows support for the Claude Code install is not stated; Codex is macOS and Linux only.

---

## Open items for Jordan (decisions, not built)

None of the 33 blocks were built or run. Candidates ranked by fit to his stated pain:

1. Watchdog (Cerberus design) for overnight jobs: deterministic, no AI, needs a Windows port.
2. Per-edit and turn rule checks (abide) for CLAUDE.md rules: needs a Jev key and a rule review; linter-checkable rules move to plain code first.
3. Stop-hook false-done check (jev-belay): one Jev call per turn that reaches it; $0.00005 each.
4. Destructive-command gate (jev-use gate or toolgate): pick one.
5. Mid-turn message router (steer-or-queue): start in shadow mode.
