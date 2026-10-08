# HyperEdit (kevinbadi/hyperedit) - functionality catalogue

Bottom line: HyperEdit is a browser video editor with an AI "Director" chat, plus a set of social-media playbooks (CreatorOS) that publish, schedule, and answer comments for a creator. The one feature closest to Jordan's "post-longform" idea is the CreatorOS post-longform skill, which publishes to YouTube with a title, description, and tags in one command.

Source: `git clone --depth 1 https://github.com/kevinbadi/hyperedit` (commit 35b2736). Read in full: all 10 CreatorOS playbooks, the root `CLAUDE.md`, `creatoros/CLAUDE.md`, `creatoros/START.md`, the TODO files, and the slash-command wrappers. Read in part (headers, prompts, labels, and the parts named in each block): the large files `AIPromptPanel.tsx`, `local-ffmpeg-server.js` (about 9,400 lines), `useProject.ts`, and the other panels. Not read: `.agents/skills/remotion-best-practices/` (Remotion reference docs), `package*.json`, and lint and config files. Nothing in this file was run.

Notes for Jordan:
- The repo has no folder named "creators." The equivalent folder is `creatoros/` (its `skills/` subfolder holds the 10 playbooks). The `.claude/commands/` files are one-line wrappers that point at the same playbooks, so they are not counted again.
- HyperEdit has **no thumbnail generator and no description/SEO writer**. The only YouTube-metadata help is chapter generation (Chapter generation block in Part B).
- Kairos (the kairos-dashboard block in Part A) is a separate repo, `kevinbadi/kairos`. I did not clone it. Its entry here comes from the playbook only.
- Where `CLAUDE.md` and the code disagree, the code is listed (DiCaprio: Remove background block).
- HyperEdit already uses the same "System One" service Jordan calls Jev (TypeSafe). It is wired into the Director router, timeline operations, and the media vault lookup.

---

## Part A - CreatorOS playbooks (the "creators" folder)

### post-longform  (source: hyperedit/creatoros/skills/post-longform/SKILL.md; slash command .claude/commands/post-longform.md)
- PURPOSE: Publish a finished long-form YouTube video, with title, description, and tags, without opening YouTube Studio.
- FUNCTIONALITY: (1) `creatoros auth:check` must pass. (2) Look up the YouTube account ID with `accounts:list`. (3) Upload the video file with `media:upload`, which returns a URL. (4) `posts:create` with `--title`, `--text` (the description), `--tags`, `--media <url>`, and optionally `--scheduledAt <ISO> --timezone <tz>`. (5) Verify with `posts:get <postId>`. If it failed, run `posts:retry` once and report. For an already-published video, `posts:update-metadata <postId> --title --tags` changes metadata. Inputs: video file, title, description, tags, account, optional publish time. Output: post ID and status.
- USE CASE FOR JORDAN: After the final render is exported, one command publishes it or queues it for a set time. Removes the manual upload and form-filling each time.
- DECISION MODEL: LLM free-text (agent judgment) for the title and description copy. System One candidates: "Does this title look like a filename or placeholder (e.g. 'Final_v3.mp4')?" (yes/no) and "Is the description missing?" (yes/no). The playbook says to stop and ask in both cases.
- DETERMINISTIC PARTS: auth check, account lookup, upload, the title-length check (under about 70 characters), the filename check, scheduling, status verification, retry once.
- DATA IT NEEDS: final video file; title; description (first two lines are the pitch, then links and chapters); tags; YouTube account ID; publish time and timezone.
- RULES WORTH COPYING: "Title under ~70 characters so it doesn't truncate in search; front-load the hook." "Description: first 2 lines carry the pitch ... links and chapters after." "Tags are low-impact on YouTube - a handful of accurate ones beats twenty speculative ones." "Never publish with a placeholder title like 'Final_v3.mp4'. If the title looks like a filename, stop and ask." "ask the human if [description/tags] are missing rather than inventing SEO copy."
- BECKY MATCH: none found. (becky-intake ingests YouTube videos; it does not publish.) Already solved elsewhere: no. Jordan's YouTube publishing is manual today.

### post-shortform  (source: creatoros/skills/post-shortform/SKILL.md)
- PURPOSE: Publish one vertical short (under about 90 s, 9:16) to TikTok, Instagram Reels, and YouTube Shorts in one step.
- FUNCTIONALITY: Check the file exists and is a video; for a vertical check the aspect ratio. Upload once with `media:upload`. Check caption length with `validate:post-length --text`. Publish with one `posts:create --accounts <tiktokId>,<instagramId>,<youtubeId> --media <url> [--title] [--hashtags]`, optional schedule. For TikTok, `accounts:tiktok-creator-info` first. Verify each platform with `posts:get`; retry failures once. Inputs: clip file, caption, account IDs. Output: post IDs and per-platform status.
- USE CASE FOR JORDAN: After a short is cut in the Shorts generator (Part B), publish it to all three platforms in one go.
- DECISION MODEL: LLM free-text for the caption (brand voice). System One candidate: "Is this video landscape (16:9) rather than vertical?" (yes/no), which the playbook currently checks with code and a warning.
- DETERMINISTIC PARTS: file checks, aspect-ratio check, length validation, upload, the single publish call, status checks.
- DATA IT NEEDS: short video file; caption and hashtags; the three account IDs; a TikTok creator-info check.
- RULES WORTH COPYING: "If the video is landscape (16:9), warn the human before posting it as a Reel/TikTok - it will look wrong. Post only on their confirmation." "If one platform's validation fails, post to the passing platforms and report the failure - don't block everything." "Don't split into per-platform posts unless captions must differ."
- BECKY MATCH: none found for publishing. The clip-cutting side matches becky-short / becky-moment / becky-hits (Shorts generator block).

### post-everywhere  (source: creatoros/skills/post-everywhere/SKILL.md)
- PURPOSE: Post one announcement, photo, or link to all connected platforms at once.
- FUNCTIONALITY: Confirm the list of target accounts with the user (first run is mandatory). Validate text per platform with `validate:post-length`, trim or split if needed. Upload media once. One `posts:create --accounts id1,id2,... --text ... [--media] [--title] [--hashtags]`. Verify each platform with `posts:get`. Retry failures once. Reddit subreddit targets get `validate:subreddit` first. Inputs: caption, media, account list. Output: per-platform live or scheduled status.
- USE CASE FOR JORDAN: Cross-post a stream announcement or a launch to all channels in one step.
- DECISION MODEL: LLM free-text for per-platform copy tweaks. System One candidate: "Does this caption suit this platform's style (link-heavy, hashtag-heavy)?" per platform.
- DETERMINISTIC PARTS: length validation, the single publish call, per-platform status checks, retry once.
- DATA IT NEEDS: caption, media file, account IDs, subreddit rules (if Reddit).
- RULES WORTH COPYING: "Confirm before 'everywhere.' ... 'everywhere' should never silently include an account they forgot was connected." "Reddit is not a billboard. ... cross-posted marketing copy gets accounts banned." "A post can succeed on four platforms and fail on the fifth - check every one."
- BECKY MATCH: none found.

### post-threads  (source: creatoros/skills/post-threads/SKILL.md)
- PURPOSE: Turn a longer idea (script, blog post, notes) into a multi-part thread on Threads and X.
- FUNCTIONALITY: Draft hook-first parts, one idea each, with a closing call to action. Validate each part's length per platform and split over-limit parts at sentence boundaries. Post part 1 with `posts:create`, capture its ID, then post the rest in order. Each platform is a separate chain. If a post fails midway, stop and ask whether to delete or continue. Inputs: source text or approved draft, account IDs. Output: ordered post IDs.
- USE CASE FOR JORDAN: Turn a long video's key points into an X or Threads thread after it goes live.
- DECISION MODEL: LLM free-text (drafting). System One candidate: "Is this part 1 a hook that stops a scroll?" (score), used to reject weak openers before posting.
- DETERMINISTIC PARTS: length checks, sentence-boundary splitting, ordering, stop-on-failure rule.
- DATA IT NEEDS: source text or script, account IDs, per-platform length limits.
- RULES WORTH COPYING: "4-8 parts is the sweet spot; past ~10, suggest longform instead." "Never pad to reach a part count, never split mid-sentence." "If posting fails midway through a thread, STOP - a half-posted thread is worse than none."
- BECKY MATCH: none found. The playbook itself notes the CLI has no native thread flag, so parts are standalone posts.

### schedule-posts  (source: creatoros/skills/schedule-posts/SKILL.md; slash command .claude/commands/schedule-posts.md)
- PURPOSE: Load a whole content calendar (a week or a month) into the posting queue at once.
- FUNCTIONALITY: Parse a CSV with columns `date, time, platforms, caption, media_path, title, tags` (column names are flexible). Validate every row before any API call: media file exists, caption non-empty and within limits, date in the future. Stop if more than half of rows fail. Upload each row's media, then `posts:create --scheduledAt <ISO> --timezone <tz>` per row, or `posts:bulk-upload --file calendar.json --dryRun` then the real run for more than about 15 rows. Verify with `posts:list` and spot-check with `posts:get`. Output: table of row, post ID, time, accounts, plus skipped rows and reasons.
- USE CASE FOR JORDAN: Plan a month of shorts and longform posts in a spreadsheet, then the platform publishes them on schedule with the laptop closed.
- DECISION MODEL: none for scheduling. LLM free-text only if a caption is missing (the playbook says to ask rather than write one).
- DETERMINISTIC PARTS: almost all of it: parsing, validation, date checks, double-booking check (shift by 30-60 min), dry run, the bulk call, verification.
- DATA IT NEEDS: calendar CSV or folder of assets, account IDs, one timezone, posting times, date range, existing queue (`posts:list`).
- RULES WORTH COPYING: "Respect stated times exactly. If the human said 6pm, schedule 6pm." "One timezone for the whole batch, stated in the final report." "Past dates in the calendar are always a mistake - surface them, never silently bump to tomorrow." "Never schedule a post whose asset or copy is missing - flag the gap."
- BECKY MATCH: none found.

### respond-to-comments  (source: creatoros/skills/respond-to-comments/SKILL.md; slash command .claude/commands/respond-to-comments.md)
- PURPOSE: Keep the comment section answered without reading every comment, while never answering sensitive ones on the creator's behalf.
- FUNCTIONALITY: Fetch comments from the last 24 h (`inbox:comments --since --limit 50`, paged with `--cursor`). Sort each comment into one of five buckets: REPLY, SKIP (spam, bots, trolls), ESCALATE (refunds, complaints, legal or medical claims, press, minors, harassment of a named person), LIKE-ONLY (`inbox:like-comment`), HIDE (scams, slurs; `inbox:hide-comment`, reversible). Draft 1-2 sentence replies in brand voice; post with `inbox:reply --commentId --message`. For private follow-ups use `inbox:private-reply`. Report counts per bucket, quote every ESCALATE in full, list replies posted. Inputs: recent comments, brand voice (`creatoros/BRAND_VOICE.md` if present, else inferred from the last 5 posts). Output: report plus posted replies.
- USE CASE FOR JORDAN: After a big video, let the agent answer the first hours of comments, flag anything that needs him, and leave the rest alone.
- DECISION MODEL: System One/Jev (question: "Which bucket does this comment belong to: reply, skip, escalate, like-only, or hide?" choice over 5 options). The playbook currently does this in free text. Also a yes/no candidate: "Does this comment involve refunds, legal, medical, minors, or harassment of a named person?" (escalate if yes).
- DETERMINISTIC PARTS: fetching, pagination, the like/hide/reply API calls, the reply cap (about 30 per run), retry-once rule, counts in the report.
- DATA IT NEEDS: comment text, author, post ID, comment ID, post time, brand voice examples, the list of sensitive topics.
- RULES WORTH COPYING: "Escalate, never answer, when a comment involves: refunds, billing ... legal, medical, or financial claims ... anything mentioning a minor or safety issue." "Never hide mere criticism - a hidden negative review the human would have wanted to see is worse than a troll left standing." "Rate sanity: if there are more than ~30 REPLY-bucket comments, reply to the 30 with the most substance." "When unsure which bucket, escalate. A missed reply costs nothing; a bad reply is public." "Never promise anything (dates, refunds, features) the human hasn't stated publicly."
- BECKY MATCH: none found. (becky-livechat reads YouTube live chat, which is read-only and not a reply tool.)

### respond-to-dms  (source: creatoros/skills/respond-to-dms/SKILL.md; slash command .claude/commands/respond-to-dms.md)
- PURPOSE: Keep the direct-message inbox triaged and answered, with sensitive threads left to the creator.
- FUNCTIONALITY: List active conversations (`inbox:conversations --status active`, filter by account or platform). Read each conversation fully with `inbox:messages` before replying. Sort into REPLY, ARCHIVE (spam or finished; `inbox:update-conversation --status archived`, reversible), ESCALATE (refunds, complaints, legal, press or sponsorship, romantic or harassing, minors, requests for the creator's personal contact), or LEAVE (waiting on the other side). Send with `inbox:send --message` (optional `--mediaUrl`), then `inbox:mark-read`. Fix own mistakes with `inbox:edit-message` or `inbox:delete-message`. Report counts, quote each escalation with its conversation ID, list sent replies.
- USE CASE FOR JORDAN: A sponsor-free, low-maintenance inbox: routine fan questions answered, spam archived, anything about money or contracts left for him.
- DECISION MODEL: System One/Jev candidate (choice over REPLY / ARCHIVE / ESCALATE / LEAVE): "Given this conversation, which bucket applies?" Currently free text.
- DETERMINISTIC PARTS: listing, reading, archive call, mark-read, the send call, retry-once, the report, the rule "never send links, prices or promises the human hasn't approved".
- DATA IT NEEDS: conversation and message history, account IDs, the creator's public statements (for "don't promise what isn't public"), approved link and price list (none in the repo).
- RULES WORTH COPYING: "Never send links, prices, or promises the human hasn't already stated publicly or pre-approved." "Don't start conversations ... never cold outreach on your own initiative." "Archive is the 'hide,' not delete." "A slow reply costs little; a bad DM from the brand account costs trust."
- BECKY MATCH: none found.

### analytics  (source: creatoros/skills/analytics/SKILL.md; slash command .claude/commands/analytics.md)
- PURPOSE: Answer "how did that do?", "am I growing?", "when should I post?" with a readable weekly report.
- FUNCTIONALITY: Default window is the last 7 days, compared with the previous 7 days. `analytics:posts --from --to --sortBy engagement --limit 20` for top posts (narrow by platform or post ID). `analytics:daily` for trend shape. `accounts:follower-stats --granularity day|week|month` for follower change. `analytics:best-time` when asked about timing. Output: headline (biggest move), per-platform followers with net change, top 2-3 posts, one observation and one suggestion, each tied to a number.
- USE CASE FOR JORDAN: A weekly "how are the channels doing" note, and a check on whether a particular video or short worked.
- DECISION MODEL: LLM free-text for the narrative. System One candidate: "Is this week's change caused by one outlier post?" (yes/no), to decide whether to report the median or the mean.
- DETERMINISTIC PARTS: all the numbers (fetch, date windows, previous-window comparison, median, outlier flagging, percent change). The playbook says every number must trace to a command run that session.
- DATA IT NEEDS: post engagement by post and platform, daily totals, follower counts by day, account health, posting times.
- RULES WORTH COPYING: "Never invent causality." "Absolute + relative, always." "Flag anomalies instead of averaging over them." "Zero or missing data is a finding, not an error - check accounts:health before blaming the numbers."
- BECKY MATCH: none found.

### automations  (source: creatoros/skills/automations/SKILL.md; slash command .claude/commands/automations.md)
- PURPOSE: Set up hands-off systems: a keyword comment that triggers a DM with an offer (a funnel), or a recurring agent task.
- FUNCTIONALITY: Two kinds. (a) CreatorOS cloud funnel: `automations:create --name --profileId --accountId --platformPostId --keywords "a,b" --dmMessage "..." [--matchMode exact|contains] [--commentReply]`. It runs on CreatorOS servers. Empty keywords = every comment triggers. Manage with `automations:list --cloud`, `:get`, `:update`, `:delete`, `:logs`. (b) Local scheduled agent run: `automations:create daily-comments --schedule "0 9 * * *" --skill respond-to-comments` installs a macOS launchd agent (Linux crontab; Windows prints Task Scheduler steps). `--target railway` makes an always-on version. Output: a stored automation with ID and logs.
- USE CASE FOR JORDAN: "Anyone who comments LINK on this video gets the link in a DM." Or a daily 9 am comment-triage run.
- DECISION MODEL: none for cloud funnels (deterministic keyword match). Local runs hand the job to a full LLM agent (judgment work).
- DETERMINISTIC PARTS: keyword matching, DM sending, scheduling, log collection.
- DATA IT NEEDS: profile, account and platform-post IDs, keywords, DM copy, schedule and timezone.
- RULES WORTH COPYING: "Confirm the exact keyword and DM copy with the human before creating - the DM goes out automatically to strangers." "Never create an automation that posts unreviewed generated content unless the human explicitly opted into that." "For local runs the machine must be awake at the scheduled time." "Local agent runs use --dangerously-skip-permissions; the human should understand the agent acts unattended." "For plain scheduled posts, no automation is needed - use posts:create --scheduledAt."
- BECKY MATCH: none found. (Local scheduling in becky-tools is a separate concern; the INDEX does not list a publishing scheduler.)

### kairos-dashboard  (source: creatoros/skills/kairos-dashboard/SKILL.md; slash command .claude/commands/kairos-dashboard.md)
- PURPOSE: Watch what the social-media agent is doing: activity log, posts, automations, analytics.
- FUNCTIONALITY: Starts a local web dashboard (default `http://localhost:4180`, changeable with `KAIROS_DASHBOARD_PORT`) from the Kairos repo (`npm run dashboard` or `kai dashboard`). It reads the Kairos repo files, the activity log `logs/activity.jsonl`, and the CreatorOS API. If no credentials, it shows a connect screen. Output: a live local page.
- USE CASE FOR JORDAN: A single screen that shows what the agent did overnight (for example, which comments it answered).
- DECISION MODEL: none.
- DETERMINISTIC PARTS: all of it (log reading, API reads, the page).
- DATA IT NEEDS: the activity log, CreatorOS API data, the Kairos repo (not in hyperedit; the playbook says to clone it from github.com/kevinbadi/kairos).
- RULES WORTH COPYING: "Don't kill an already-running dashboard to start another." "Never paste the API key into files or the URL."
- BECKY MATCH: none found for a dashboard. becky-tools has an activity/jobs view (becky-jobs, becky-report) that may cover the same need. Not verified.

### creatoros-start  (first-run briefing; source: creatoros/START.md)
- PURPOSE: Introduce the agent to the account: who is connected, how they are doing, what the agent can do.
- FUNCTIONALITY: Runs `accounts:list`, `accounts:health`, `accounts:follower-stats`, `analytics:posts --limit 20`, `analytics:daily`. Writes a short briefing: each account (platform and handle), health warnings, follower counts, the best recent posts, one or two honest observations, a list of its skills, and a suggested first move. Output: a readable briefing, not raw data.
- USE CASE FOR JORDAN: The "what do I have to work with" overview at setup, or after a long break.
- DECISION MODEL: LLM free-text (summary). No decision.
- DETERMINISTIC PARTS: all the data pulls, the account health flags.
- DATA IT NEEDS: connected accounts and their health, follower history, recent post analytics.
- RULES WORTH COPYING: "If any command fails, say so plainly and continue with the rest."
- BECKY MATCH: none found.

### creatoros-standing-rules  (guardrails; source: creatoros/CLAUDE.md "Standing rules")
- PURPOSE: The non-negotiable safety rules every social-media action must follow.
- FUNCTIONALITY: Six rules: (1) never post without the content existing, no placeholder captions or invented media URLs; (2) confirm deletes, disconnects, and unpublishing; (3) do not move or bulk-edit scheduled posts unless asked; (4) escalate refunds, complaints, legal or medical topics; (5) verify after every publish with `posts:get` and report failures honestly; (6) the platform is always called CreatorOS. Enforced only by the playbooks' written text (see the Rules section below).
- USE CASE FOR JORDAN: The agent cannot post an empty or invented post or quietly delete a post.
- DECISION MODEL: none.
- DETERMINISTIC PARTS: none of these is enforced in code except the delete/cancel check in the in-app Creator OS chat block.
- DATA IT NEEDS: none.
- RULES WORTH COPYING: all six. Note: they are text in a markdown file. The repo's own in-app chat enforces the delete rule in code; the playbooks do not.
- BECKY MATCH: none found. (This is the kind of rule Jordan's LESSONS say must be enforced in a hook, not just written. Worth checking whether the CreatorOS playbooks are hook-enforced on Jordan's PC.)

---

## Part B - Editor features in the app (src/ and scripts/)

### Director workflow router (Jev)  (source: src/react-app/components/AIPromptPanel.tsx; scripts/local-ffmpeg-server.js `handleDirectorRoute`; scripts/jev.js)
- PURPOSE: Decide which tool a plain-English Director request should run, without the user picking a menu.
- FUNCTIONALITY: Input: the typed or spoken request, plus editor state (video loaded, animation open, selected clip, playhead, clip labels). The app sends it to `POST /director/route`. Jev answers in one call: a Choice over the 15 workflows (edit-animation, create-animation, batch-animations, motion-graphics, captions, auto-gif, b-roll, dead-air, chapter-cuts, transcript-animation, contextual-animation, extract-audio, ffmpeg-edit, timeline-op, vault-media) and a Noul (refers to the existing animation?). The same call also answers the timeline-op questions (Timeline operations block). The server then forces edit-animation when an animation is in context and the Noul confidence is 0.6 or higher. The client uses Jev's answer only if confidence is 0.35 or higher and it answers within 4 s. Otherwise a keyword router (`determineWorkflow`) decides. Both answers are logged to the console. Output: workflow name, confidence, latency.
- USE CASE FOR JORDAN: Just say "add captions", "cut the dead air", "make the logo bigger" in any order, and the right tool runs.
- DECISION MODEL: System One/Jev. Question: "Which editor workflow does `request` ask for, given the `editor` state?" (choice over 15 workflow descriptions). Second: "Does `request` refer to the animation the editor already has open or selected...?" (Noul, true/false). This is already a System One use in HyperEdit.
- DETERMINISTIC PARTS: the keyword fallback, the animation-in-context override, the 0.35 confidence and 4 s cut-offs, the parsing of times (mm:ss, "12 seconds") in `parseTimelineNumbers`.
- DATA IT NEEDS: the request text; editor state flags; the clip labels on the timeline. Jev needs an API key (`TYPESAFE_API_KEY`); without it, keywords decide.
- RULES WORTH COPYING: "Ask all questions for one decision in a single request; Jev evaluates them in parallel." "Every caller must degrade gracefully when [Jev is not configured]." "Workflow routing asks Jev first ... the keyword determineWorkflow decides [otherwise]." The 0.35 and 4 s thresholds are the only numbers.
- BECKY MATCH: becky-ask (the one-call "plain English" front door named in our CLAUDE.md) is the closest equivalent. Jev/System One is already part of becky-decide (INDEX). Possibly already solved; the match is a guess, not verified.

### Timeline operations by request (timeline-op)  (source: src/react-app/lib/directorOps.ts; AIPromptPanel.tsx; Home.tsx `executeDirectorTimelineOp`)
- PURPOSE: Edit the timeline with words instead of the mouse: delete, split, move, trim, extend, resize, position, seek, play, pause, clear a track.
- FUNCTIONALITY: Jev returns one `DirectorTimelineOp` with 14 operations (delete, split, move, trim_start, trim_end, extend_start, extend_end, set_duration, scale, position, seek, play, pause, clear_track), a target (selected, at_playhead, first, last, all_on_track, everything, or a specific clip by label), tracks (T1, V3, V2, V1, A1, A2), direction, size preset (tiny, small, half, full), position preset (corners or center), seconds, time, and scale. Code parses numbers ("0:30", "by 2 seconds", "30%") with regex. The client runs the op on the active timeline tab through the existing handlers and returns a one-line result. Input: request and clip labels. Output: the timeline changed plus a one-line message.
- USE CASE FOR JORDAN: "Put the logo top right, small", "split at 1:20", "delete everything on V2", "move the intro to 0:10 later".
- DECISION MODEL: System One/Jev, several Choices in one call: operation, target (choice over clip labels), track, to_track, direction, size, position.
- DETERMINISTIC PARTS: number parsing, the actual edits (split, delete, move), the 0.05 s split guard, the active-tab routing.
- DATA IT NEEDS: the timeline as labels (`V1 · intro.mp4 · 0:00-0:30`), playhead time, selected clip, track layout.
- RULES WORTH COPYING: "Track placement: B-roll images go on V3 ... AI-generated animations always go on V2." "`splitClip` returns null if the split point is within 50 ms of either edge." "Pick none if it is not [a timeline command]."
- BECKY MATCH: becky-timeline / becky-arrange / becky-compose (names only, guessed from the INDEX). The VEGAS-editing side is becky-vegas. Not verified.

### Director FFmpeg edits from plain English (ffmpeg-edit)  (source: src/worker/index.ts `/api/ai-edit`; scripts/local-ffmpeg-server.js `handleProcess`, `handleProcessAsset`)
- PURPOSE: Re-encode footage with a command typed in plain English: speed, reverse, crop, rotate, flip, resize, brightness, contrast, color filter, mute, volume, denoise, fade.
- FUNCTIONALITY: The request goes to Claude (`claude-sonnet-5`) with a system prompt that tells it to produce FFmpeg commands. The reply is parsed into `{command, explanation}`. The server runs the FFmpeg command on the asset (`process-asset` replaces the file in place). The Cloudflare Worker only generates the command (`/api/ai-edit/start`, status, and one-shot endpoints); it does not run FFmpeg. Input: request, asset. Output: edited asset (replaced in place) and an explanation.
- USE CASE FOR JORDAN: "Speed this clip up to 1.25x", "make it black and white", "mute the first 3 seconds".
- DECISION MODEL: LLM free-text (Claude, writes an FFmpeg command). A System One candidate: "Which of these 15 effect families does `request` ask for?" (choice), which would then map to fixed FFmpeg templates. Right now the free-text output is run directly.
- DETERMINISTIC PARTS: the FFmpeg run, the template parsing, the in-place replacement, the cache-busting URL refresh. A fixed template for each family would remove the LLM from the run path.
- DATA IT NEEDS: the asset file and its duration, dimensions, and audio status.
- RULES WORTH COPYING: "The segment-based approach (extract + concat) is required - single-pass filter approaches (select/aselect, trim/atrim) drop audio streams." (in CLAUDE.md, about dead-air).
- BECKY MATCH: becky-edit (guess from the name; not verified). VEGAS effects are done in becky-vegas.

### Voice mode for the Director  (source: src/react-app/hooks/useVoiceDirector.ts; AIPromptPanel.tsx; `POST /director/tts`)
- PURPOSE: Talk to the editor and hear the answers, without typing.
- FUNCTIONALITY: Push-to-talk mic (browser Web Speech API, Chrome, Edge, Safari; no server). The final transcript is sent as a normal request. The headphones button turns on voice mode: every new reply is read aloud (`toSpeakable` strips markdown and code, caps at 420 characters), then the mic reopens automatically. Speech comes from OpenAI `tts-1` (voice `onyx`, overridable with `DIRECTOR_TTS_VOICE` and `DIRECTOR_TTS_MODEL`) through `POST /director/tts`. If that fails, the browser's speech synthesis is used. The mic is never open while the Director speaks.
- USE CASE FOR JORDAN: Hands-free editing while watching the screen.
- DECISION MODEL: none (plumbing for input and output).
- DETERMINISTIC PARTS: all of it.
- DATA IT NEEDS: a microphone; `OPENAI_API_KEY` for the nicer voice.
- RULES WORTH COPYING: "The mic is never open while the Director is speaking." "The Director no longer requires a video to be uploaded before you can talk to it."
- BECKY MATCH: becky-voice (the INDEX lists it). Jordan's rule says spoken output must go through Whoretana only, so this copy of the voice stack would not be allowed on his PC as is.

### Auto captions with Whisper + caption styling  (source: src/react-app/components/CaptionPropertiesPanel.tsx; scripts/whisper-transcribe.py; local-ffmpeg-server.js `handleTranscribe`, `buildAssFromCaptions`, `getOrTranscribeVideo`)
- PURPOSE: Put on-screen captions on a video, word by word, in a chosen style.
- FUNCTIONALITY: Local OpenAI Whisper (`base` model, CPU only) transcribes the audio with word-level timestamps. Words are grouped into lines of at most 5 words, or split at a 0.7 s pause. Each group becomes a caption clip on track T1. Styling: font (Arial, Bebas Neue, Helvetica, Inter, Montserrat, Oswald, Poppins, Roboto), weight, size, colors, highlight color, stroke width, position (top, center, bottom), animation (None, Fade In, Pop, Bounce, Karaoke), time offset. At render, captions are written as an ASS subtitle file and burned into the video. Input: video (or its extracted audio). Output: caption clips and transcript JSON, cached per asset.
- USE CASE FOR JORDAN: Captions for any long-form or short, styled to his brand, without paying for a captioning service.
- DECISION MODEL: none (Whisper is a fixed model; there is no judgment step). Gemini is the fallback if Whisper is missing, which the code says struggles with long audio.
- DETERMINISTIC PARTS: the entire chain (transcription, grouping, timing, rendering). Only the styling is user choice.
- DATA IT NEEDS: the video or audio file; a Python environment with `openai-whisper` and `torch` (CPU); fonts.
- RULES WORTH COPYING: "Caption chunking: Max 5 words per chunk OR when there's a 0.7s pause between words." "MPS (Apple GPU) is NOT supported - Whisper's sparse tensors crash on MPS. The script runs on CPU only." "Caption word timestamps are relative to clip start, not absolute project time."
- BECKY MATCH: becky-subtitle and becky-captions (names in the INDEX; HANDOFF-VEGAS-CAPTIONS.md covers the VEGAS captions work order). becky-transcribe covers the transcription. Not verified.

### Dead-air / silence removal  (source: src/react-app/pages/Home.tsx `handleRemoveDeadAir`; local-ffmpeg-server.js `handleRemoveDeadAir`, `detectSilence`, `calculateKeepSegments`)
- PURPOSE: Cut out silent or dead gaps from a talking video in one click.
- FUNCTIONALITY: FFmpeg `silencedetect` at -26 dB with a minimum silence of 0.4 s finds silent spans. Keep segments are the inverse. Each keep segment is extracted (re-encoded, libx264 ultrafast, aac) and the pieces are concatenated with `-c copy`. The original file is replaced in place. If the audio was split to the A1 track, that audio is cut with the same segment list in one `atrim`+`concat` pass and replaced too, so V1 and A1 stay in sync. The client then refreshes the asset and updates the clip lengths. Input: video (or linked audio). Output: shorter video, updated clips.
- USE CASE FOR JORDAN: Tighten a rambly talking-head video. This is the one editing feature the repo marks "stable - keep."
- DECISION MODEL: none (fixed thresholds).
- DETERMINISTIC PARTS: all of it.
- DATA IT NEEDS: the video's audio track (mean volume must be above -60 dB), or the linked A1 audio; the clip timing.
- RULES WORTH COPYING: "The segment-based approach (extract + concat) is required - single-pass filter approaches (select/aselect, trim/atrim) drop audio streams." "silencedetect ... threshold: -26dB, min duration: 0.4s." The repo's own CLAUDE.md warns: "change it only deliberately."
- BECKY MATCH: becky-cut (its INDEX entry covers dead-air spans; the spec says NOT to re-cut a finished video on silence). Matches the idea, not the method.

### Auto GIF from the transcript (keyword-triggered)  (source: src/react-app/components/GifSearchPanel.tsx; local-ffmpeg-server.js `extractKeywordsFromTranscript`, `searchGiphy`, `downloadGifAsAsset`)
- PURPOSE: When a named brand, product, or person is said, drop a matching GIF on the screen at that moment.
- FUNCTIONALITY: Transcribe the video (Whisper). Scan the transcript for words in a hardcoded list (`KNOWN_KEYWORDS`: tech companies, social platforms, products, people, and so on) with timestamps. Sort by time. For each, search GIPHY for the keyword (limit 1), download the GIF as an asset, and place it on the timeline at that time. Input: video transcript. Output: GIF assets placed on the timeline.
- USE CASE FOR JORDAN: "Anthropic", "Tesla", "iPhone" mentions get a relevant reaction GIF or logo-style clip.
- DECISION MODEL: none. The keyword list is hardcoded. A System One candidate for the choice that matters: "Is this a moment that would benefit from a reaction GIF (funny, emphatic)?" (yes/no), which the code does not ask.
- DETERMINISTIC PARTS: the keyword match, timestamps, GIPHY call, download, placement.
- DATA IT NEEDS: transcript with word timestamps; GIPHY API key (`GIPHY_API_KEY`).
- RULES WORTH COPYING: "Known keywords/brands to detect in transcripts" is a list you can edit. The to-do file lists "Custom keyword lists for GIF extraction" as not built.
- BECKY MATCH: none found for GIF placement. becky-transcribe covers the transcript.

### GIPHY manual search and add (GIF panel)  (source: src/react-app/components/GifSearchPanel.tsx; `/session/:id/giphy/search`, `/giphy/trending`, `/giphy/add`)
- PURPOSE: Find a GIF or meme by hand and add it to the timeline.
- FUNCTIONALITY: Search box ("Search for memes, reactions, GIFs...") and a trending button. Results come from the server's GIPHY proxy. Clicking one downloads it as an asset (`giphy/add`). Input: search text. Output: a GIF asset in the library.
- USE CASE FOR JORDAN: Quick reaction GIF that he picks himself.
- DECISION MODEL: none.
- DETERMINISTIC PARTS: all of it.
- DATA IT NEEDS: GIPHY API key.
- RULES WORTH COPYING: none.
- BECKY MATCH: none found.

### GIF from a still image (create-gif)  (source: local-ffmpeg-server.js `handleCreateGif`)
- PURPOSE: Make an animated GIF from one image with a motion effect.
- FUNCTIONALITY: Input: image asset, effect options (motion effects, fps default 15). Output: GIF asset. Pure FFmpeg, no AI.
- USE CASE FOR JORDAN: Make a moving logo or still-photo GIF for a graphic.
- DECISION MODEL: none.
- DETERMINISTIC PARTS: all of it.
- DATA IT NEEDS: one image.
- RULES WORTH COPYING: none.
- BECKY MATCH: none found.

### B-roll image generation (static)  (source: local-ffmpeg-server.js `analyzeBrollOpportunities`, `generateImageWithGemini`, `handleGenerateBroll`)
- PURPOSE: Put AI-made illustration images over the spots in the talk where a visual would help.
- FUNCTIONALITY: Transcribe the video. Gemini reads the transcript and picks opportunities (keywords or products mentioned, funny or emphatic moments, important concepts, brand names or people, abstract concepts). For each, it writes a prompt of 10-20 words. Gemini generates a 1:1 image. The image goes on V3 at scale 0.2, centered (per UI rules). Input: transcript, video duration. Output: image assets placed on the timeline.
- USE CASE FOR JORDAN: Automatic simple illustrations over talking-head footage.
- DECISION MODEL: LLM free-text (Gemini: which moments to illustrate, and the prompt). System One candidate: "Is this moment visual-worthy?" (yes/no) and "Does this moment name a brand (logo needed) or a concept (illustration needed)?" (choice). The vault lookup (Brand media vault block) is the better fit for brand logos.
- DETERMINISTIC PARTS: transcript timestamps, placement at V3, scale 0.2, the 1:1 format, the image-generation call.
- DATA IT NEEDS: transcript with timestamps, video duration, a Gemini key (`GEMINI_API_KEY`).
- RULES WORTH COPYING: "Keep prompts concise (10-20 words)." "Use 'minimalist', 'icon', 'simple', 'flat design' style descriptors." "Avoid complex scenes - prefer single subjects with clean backgrounds." "Images will be 1:1 square format." "Never swap brands" is said for the vault (Brand media vault block), not this.
- BECKY MATCH: becky-imagegen (generates images; cloud). Placement and transcript match via becky-transcribe. Not verified.

### Chapter generation, YouTube chapter copy, and chapter cuts  (source: local-ffmpeg-server.js `handleGenerateChapters`, `handleSessionChapters`; src/react-app/pages/Home.tsx `handleCopyChapters`, `handleChapterCuts`)
- PURPOSE: Make YouTube chapter timestamps for a long video, and optionally cut the video at each chapter boundary.
- FUNCTIONALITY: Gemini listens to the audio (full video duration given) and returns 3-8 chapters with a start time, a title, and a 1-2 sentence summary. The first chapter starts at 0 s. Chapters must be at least 30 s apart. The app formats them as YouTube chapter lines and copies them to the clipboard. "Chapter cuts" (`handleChapterCuts`) also makes a cut at each chapter point. Input: video. Output: chapter list, summary, YouTube-format text, optional cuts.
- USE CASE FOR JORDAN: Paste-ready chapter list for the YouTube description (the post-longform playbook asks for chapters after the pitch). Also a rough structure pass on a long stream.
- DECISION MODEL: LLM free-text (Gemini, audio analysis). System One candidate: "Does this moment start a new topic?" (yes/no) at candidate timestamps, to replace the open-ended ask.
- DETERMINISTIC PARTS: the 0 s first chapter, the 30 s minimum spacing check, the YouTube formatting, the clipboard copy, the cut points.
- DATA IT NEEDS: the video or audio, total duration, a Gemini key.
- RULES WORTH COPYING: "First chapter should always start at 0 seconds." "Aim for 3-8 chapters depending on content length and topic diversity." "Chapters should be at least 30 seconds apart."
- BECKY MATCH: none found for chapters. becky-judge and becky-vision are not chapterers. Jordan's own chapters are not in becky-tools. Not verified.

### Motion graphics templates (Remotion)  (source: src/remotion/templates/*.tsx and index.ts; src/react-app/components/MotionGraphicsPanel.tsx; local-ffmpeg-server.js `handleRenderMotionGraphic`)
- PURPOSE: Drop a pre-built animated graphic on the video, with the creator's text and colors.
- FUNCTIONALITY: 11 templates: Animated Text (styles typewriter, bounce, fade-up, word-by-word, glitch), Lower Third (modern, minimal, bold, gradient, news), Call to Action (subscribe, like, follow, share, custom; pill, box, floating, pulse), Counter, Logo Reveal, Screen Frame (phone or screen mockup), Social Proof, Progress Bar, Comparison, Zoom & Pan, Data Chart. Each has default props. Preview runs in the browser (`@remotion/player`); the server renders the chosen one to a video asset with FFmpeg/Remotion. Note: the server comment says this route is a placeholder that makes a simple text overlay with FFmpeg. Input: template ID, props, duration, fps 30, size 1920x1080. Output: rendered motion-graphic asset.
- USE CASE FOR JORDAN: Subscribe button, lower third with his name, counter for a stat, progress bar for a series. Consistent with his brand colors.
- DECISION MODEL: none (user picks the template).
- DETERMINISTIC PARTS: all of it.
- DATA IT NEEDS: template props (text, colors, names, numbers).
- RULES WORTH COPYING: "When working on templates, use the /remotion-best-practices skill." "Tailwind only scans ./src/react-app/."
- BECKY MATCH: none found.

### AI-generated animation: create  (source: local-ffmpeg-server.js `handleGenerateAnimation`, `handleRenderFromConcept`; src/remotion/DynamicAnimation.tsx)
- PURPOSE: Describe an animation in words (title card, stats, steps, chart, countdown, emoji, GIF, Lottie, end screen) and get a rendered overlay on V2.
- FUNCTIONALITY: Gemini writes the scene data or Remotion JSX from the description and the video's context. Scene types include title, steps, features, stats (a number that counts up), chart, countdown, emoji, gif (GIPHY search), lottie. The server renders the `DynamicAnimation` composition with the Remotion CLI and registers it as an asset on V2. Input: description, optional video context and time range, optional attached assets. Output: animation asset on V2 with its source kept for editing.
- USE CASE FOR JORDAN: "Make an intro card with the three things I cover in this video" or "an end screen with a subscribe box".
- DECISION MODEL: LLM free-text (Gemini writes scene data or JSX). System One candidate for scene type choice: "Which of these 9 scene types fits this beat?" (choice).
- DETERMINISTIC PARTS: the Remotion render, the composition, V2 placement, asset registration, the per-type duration rules (see the rules).
- DATA IT NEEDS: the description, the video's transcript (for relevance), a Gemini key, optional GIPHY key.
- RULES WORTH COPYING: "Be 4-8 seconds" for intros. "Be 5-10 seconds" for outros (CTA, summary, thank-you). "Be brief and visually interesting" for quick overlays (2-4 s). "Draw attention to an important point" 3-6 s. "The animation content should directly relate to the video's actual topic and message."
- BECKY MATCH: none found.

### AI-generated animation: edit in place  (source: local-ffmpeg-server.js `handleEditAnimation`; src/react-app/components/AIPromptPanel.tsx "edit" routing; Home.tsx `handleOpenAnimationInTab`)
- PURPOSE: Change an existing animation with a sentence ("make it bigger", "change the colors", "add a zoom") without re-creating it.
- FUNCTIONALITY: The stored scene data (the original scenes JSON) and the request go to Gemini, which returns modified scenes. The animation is re-rendered and the same asset ID is reused (in place). Animations open in an edit tab (Timeline tabs block). Input: animation asset, edit request. Output: same asset, re-rendered.
- USE CASE FOR JORDAN: Fix an animation without losing its place on the timeline.
- DECISION MODEL: LLM free-text (Gemini edits scenes). System One candidate: "Is this request about the existing animation or the main footage?" (already a Noul in the Director router block).
- DETERMINISTIC PARTS: re-render, same asset ID, timeline position kept.
- DATA IT NEEDS: stored scene JSON (`aiGenerated` flag, `editCount`), the request text.
- RULES WORTH COPYING: "Edit an existing animation with a new prompt. Takes the original scene data and modifies it based on the prompt." "Assets with aiGenerated: true are deprioritized when selecting context video for new animation generation."
- BECKY MATCH: none found.

### Batch animations across the whole video  (source: local-ffmpeg-server.js `handleGenerateBatchAnimations`)
- PURPOSE: "Add 5 animations throughout the video" in one step, spaced out.
- FUNCTIONALITY: Gemini reads the transcript and video duration and proposes several animations, each with a start time, a duration (typically 3-5 s), a purpose, and a visual style. It spaces them through the video, puts the first at 0 s as an intro, and may end with a call to action. Each is then rendered as an animation (same path as create). Input: video, count or instructions. Output: several animations on V2 at their times.
- USE CASE FOR JORDAN: A starting set of overlays across a long video to edit down.
- DECISION MODEL: LLM free-text (Gemini plans the set). System One candidate: "Is this moment a good place for an animation?" (yes/no) at each transcript candidate.
- DETERMINISTIC PARTS: spacing, first-at-zero rule, rendering, placement.
- DATA IT NEEDS: transcript, duration, Gemini key.
- RULES WORTH COPYING: "First animation should typically be an intro (startTime: 0)." "Space animations throughout the video, not clustered together." "Each animation should enhance understanding or engagement."
- BECKY MATCH: none found.

### Contextual animation on a marked range  (source: local-ffmpeg-server.js `handleGenerateContextualAnimation`; AIPromptPanel.tsx "contextual-animation")
- PURPOSE: An animation that reacts to what is said during a time range the editor has marked.
- FUNCTIONALITY: The editor marks an in and out time. The server transcribes the video, reads the transcript in that range, decides what the video is about, and generates an animation of the chosen type (intro 4-8 s, outro 5-10 s, transition 2-4 s, emphasis 3-6 s). Rendered at 30 fps. Output: animation asset on V2 at the range.
- USE CASE FOR JORDAN: "Make an animation for the part where I talk about pricing" with the range marked.
- DECISION MODEL: LLM free-text (Gemini). System One candidate: "Which of 4 animation types fits this range?" (choice).
- DETERMINISTIC PARTS: transcript slicing to the range, duration per type, rendering, placement.
- DATA IT NEEDS: transcript for the range, marked in/out times.
- RULES WORTH COPYING: "Use specific terms, concepts, and themes from the transcript."
- BECKY MATCH: none found.

### Kinetic typography from speech (transcript-animation)  (source: local-ffmpeg-server.js `handleGenerateTranscriptAnimation`)
- PURPOSE: Animate key spoken phrases as on-screen text, synced to the audio.
- FUNCTIONALITY: Transcribe the video. The LLM picks phrases spread through the video: important or impactful statements, keywords or product names, emotional or emphatic moments, key points. Each phrase is 2-6 words, with its start and end time. Each becomes a text scene timed to its words, at least 2 s long. Output: a text animation asset on V2.
- USE CASE FOR JORDAN: Big on-screen words for the important lines, like a short-form style.
- DECISION MODEL: LLM free-text (Gemini picks phrases). System One candidate: "Is this phrase important enough to put on screen?" (score, 0-100 with a threshold), since this is the decision the model makes for each phrase.
- DETERMINISTIC PARTS: word timestamps, phrase-to-time mapping, minimum 2 s, frames at 30 fps, placement.
- DATA IT NEEDS: word-level transcript, duration.
- RULES WORTH COPYING: "Pick phrases that are spread throughout the video. Each phrase should be 2-6 words." "At least 2 seconds" per phrase.
- BECKY MATCH: none found.

### Analyze, approve, then render (animation concept workflow)  (source: local-ffmpeg-server.js `handleAnalyzeForAnimation`, `handleRenderFromConcept`)
- PURPOSE: Show the editor the proposed animation scenes first, so he can approve or change them before anything renders.
- FUNCTIONALITY: Step 1 `analyze-for-animation`: transcribes the video, has the LLM propose scenes (type, text, duration, and for stats a numeric value; GIF scenes use a GIPHY search term) that relate to the actual topic. No rendering. Returns transcript and scene list. Step 2 `render-from-concept`: takes the approved scenes and renders them directly, skipping analysis. Input: video or range. Output (step 1): scene plan. Output (step 2): rendered animation.
- USE CASE FOR JORDAN: Review the plan before paying for a render. This matches his rule of "let the editor judge the work."
- DECISION MODEL: LLM free-text (plan). The approval itself is the human decision; no System One needed.
- DETERMINISTIC PARTS: rendering, timing (30 fps), the approval step itself.
- DATA IT NEEDS: transcript, video duration, approved scene JSON.
- RULES WORTH COPYING: "The animation content should directly relate to the video's actual topic and message." "stats: use numericValue for counting animation (must be a NUMBER)." Scene durations as listed in the AI animation create block.
- BECKY MATCH: none found. (The approval-before-render pattern is worth copying into any becky render step.)

### Extract audio to its own track (A1)  (source: local-ffmpeg-server.js `handleExtractAudio`; AIPromptPanel.tsx "extract-audio")
- PURPOSE: Split the audio off a video so it can be edited on its own track.
- FUNCTIONALITY: FFmpeg creates a silent video asset and an audio asset, and links the audio to the video (`sourceAssetId`). The audio goes on A1. Input: video. Output: two assets. Used by dead-air removal and transcription when the video itself is muted.
- USE CASE FOR JORDAN: Cut the audio separately from the picture, for example to keep the voice while swapping the footage.
- DECISION MODEL: none.
- DETERMINISTIC PARTS: all of it.
- DATA IT NEEDS: video with an audio track.
- RULES WORTH COPYING: "resolveAudioSource ... so a muted V1 never produces the 'Output file does not contain any stream' FFmpeg error."
- BECKY MATCH: none found.

### Shorts generator (viral clips)  (source: src/react-app/components/ShortsPanel.tsx; local-ffmpeg-server.js `handleShortsStart`, `runShortsJob`, `rankHighlightsWithClaude`, `snapCandidateToWords`, `dedupeCandidates`, `buildShortsVideoFilter`)
- PURPOSE: Find the most shareable moments in a long talking video and cut them into ready vertical shorts.
- FUNCTIONALITY: Pick a source video (speaking video, at least 20 transcribed words). Options: count 1-8 (default 3), ratio 9:16, 1:1 or 4:5, min 20 s and max 60 s (default), hook on or off, crop position left/center/right. Steps: (1) transcribe (cached); (2) Claude Sonnet 5 classifies the video (content type and pacing) and returns about 3x the count of candidate spans, each scored 0-100 on a virality rubric and with a title and a hook of up to 7 words; (3) snap each candidate to word boundaries, preferring sentence ends, enforce min and max length; (4) dedupe overlapping spans (code default overlap threshold 0.25), keep the top N; (5) cut with FFmpeg, crop to the ratio, scale to 1080x1920 / 1080x1350 / 1080x1080, burn the hook in the first 3 s (one drawtext per line, max 3 lines); (6) register each clip as an asset with its score, title, hook, reason, source range. The panel lists them; "Add to timeline" puts a short on V1. Output: N vertical clips with scores and hooks.
- USE CASE FOR JORDAN: Long stream or video in, ready-to-post shorts out. Directly relevant to his shorts workflow.
- DECISION MODEL: LLM free-text (Claude ranks and scores). Strongest System One candidate in the repo: the 0-100 score per span is a `score` question, and a Jev score with a threshold is a clean fit. Also: "Is this span self-contained with zero context?" (yes/no) per candidate.
- DETERMINISTIC PARTS: word snapping, duration limits, dedupe, crop and scale, the hook burn-in, asset registration, the 20-word minimum.
- DATA IT NEEDS: transcript with word timestamps; source video; the virality rubric (in the prompt); an Anthropic key.
- RULES WORTH COPYING: The virality rubric (8 items in the prompt): hook strength, emotional peak, opinion bomb, revelation, conflict or tension, quotable, story peak, practical value. "Penalise spans that rely on visuals you cannot see, that reference earlier context, or that are rambling." "Each must be 20-60 seconds, start at the beginning of a thought and end at a natural stopping point." "Hook: an on-screen title of at most 7 words ... curiosity gap, bold claim or direct address. No hashtags, no emoji." "Each hook line is a separate drawtext filter - FFmpeg 8 renders a tofu box for embedded newlines."
- BECKY MATCH: becky-short, becky-moment, becky-hits (the CLAUDE.md of becky-tools names these; `HANDOFF-SHORTS-2026-08-20.md` is the current state). The becky-tools INDEX also mentions a virality rubric. Strong match, not verified against the code.

### Brand media vault lookup (Jev media agent) and Obsidian import  (source: scripts/obsidian-agent.js `queryVault`; scripts/local-ffmpeg-server.js `handleJevQuery`, `handleObsidianImport`; src/react-app/components/ObsidianPanel.tsx; AIPromptPanel.tsx `vault-media`, `placeVaultMedia`)
- PURPOSE: Ask in plain English for a logo, icon, profile picture, or clip and get the right file from a brand vault, without hunting folders.
- FUNCTIONALITY: The vault ("Marketing OS Broll") is a folder of media, each with a sidecar note (name, type, pillar, brand, kind, file, aliases, tags, colors, dimensions, duration). Pillars: ai-companies (third-party logos), brand-assets (own brands and profile pictures), video-broll (clips). One Jev call reads the request: plural or singular (Noul), kind, pillar, and brand (Choice over the live brand catalog). Code then applies the rules: a brand that is part of a company hub returns the whole hub. A plural ask returns all matches; a singular ask returns the best plus `more`. Ranking: exact brand > canonical name > plain mark > logo > icon > profile > clip > resolution. A second Jev call runs for descriptive asks ("the pink logo"). The server reads a non-iCloud mirror (`rsync` copy), because iCloud evicts files. Input: request text. Output: rows with file, thumb, and size. Import (`/obsidian/import`) copies rows into the session. The Director places them: images on V3 at the playhead (default small, top-right), videos on V1 if V1 is empty else V2, audio on A1.
- USE CASE FOR JORDAN: "Put the Claude logo top right" or "drop in the Hoops AI clip" from his own library, with the right size and place.
- DECISION MODEL: System One/Jev. Question (brand): "Which entry in `brands` does `message` explicitly name, by its name or one of its aliases? Only pick a brand that is actually said..." (choice with pick-none). Questions (kind, pillar): choice. Plural: Noul ("does message ask for several files?"). Descriptive: Noul ("does it describe a color, variant, scene, action, platform, or subject?"). Per item: "Is items[i] what the editor is asking for?" (Noul).
- DETERMINISTIC PARTS: the ranking list, the hub expansion rule, the plural/singular contract, the 15-row import limit in voice mode, the mirror sync every 5 minutes, placement presets, the vault file reading.
- DATA IT NEEDS: the vault (sidecar `.md` notes plus media files); `TYPESAFE_API_KEY`; the `OBSIDIAN_VAULT_PATH` override.
- RULES WORTH COPYING: "Never swap brands. An empty result for a named brand means it isn't on file; say so rather than substituting another company's mark." "Singular vs plural is the contract." "The server never reads the iCloud folder directly." "Replies are one clipped sentence."
- BECKY MATCH: none found. (becky-identify and becky-imagegen are different jobs.)

### DiCaprio: Animate Image (image to video)  (source: local-ffmpeg-server.js `handleGenerateVideo`; src/react-app/components/DiCaprioPanel.tsx)
- PURPOSE: Turn a still picture into a short moving video from a text prompt.
- FUNCTIONALITY: Input: an image asset and a short prompt. Claude expands the prompt into a detailed cinematic one. The fal.ai model `fal-ai/kling-video/v1.5/pro/image-to-video` generates the video. The result is saved as a new asset. Output: video asset.
- USE CASE FOR JORDAN: Bring a logo, screenshot, or thumbnail still to life for a short intro.
- DECISION MODEL: LLM free-text (Claude expands the prompt).
- DETERMINISTIC PARTS: calling the model, saving the asset.
- DATA IT NEEDS: image asset, prompt, `FAL_API_KEY`, `ANTHROPIC_API_KEY`.
- RULES WORTH COPYING: none stated.
- BECKY MATCH: none found. (becky-imagegen is still images.)

### DiCaprio: Restyle video  (source: local-ffmpeg-server.js `handleRestyleVideo`)
- PURPOSE: Re-style existing footage with a text prompt (for example, a different look).
- FUNCTIONALITY: Input: video asset and style prompt. Claude expands the prompt. The fal.ai model `fal-ai/ltx-2-19b/video-to-video` restyles the video. Output: new video asset.
- USE CASE FOR JORDAN: A stylized version of a clip for a bit or an intro.
- DECISION MODEL: LLM free-text (Claude expands the prompt).
- DETERMINISTIC PARTS: calling the model, saving the asset.
- DATA IT NEEDS: video asset, prompt, fal and Anthropic keys.
- RULES WORTH COPYING: none stated.
- BECKY MATCH: none found.

### DiCaprio: Remove background from video  (source: local-ffmpeg-server.js `handleRemoveVideoBg`)
- PURPOSE: Cut the person or subject out of a video, for compositing.
- FUNCTIONALITY: Input: video asset. The code calls fal.ai model `fal-ai/ben/v2/video`. Output: a video with the background removed. The repo's CLAUDE.md says this uses "Bria"; the running code uses the model above. The code is what runs.
- USE CASE FOR JORDAN: A talking head on a new background, or a logo clip with no backdrop.
- DECISION MODEL: none.
- DETERMINISTIC PARTS: all of it.
- DATA IT NEEDS: video; fal key.
- RULES WORTH COPYING: none.
- BECKY MATCH: none found.

### Image generation (nano-banana-pro; not in the UI)  (source: local-ffmpeg-server.js `handleGenerateImage`)
- PURPOSE: Make a still image from a prompt for the "Picasso" agent, which was removed.
- FUNCTIONALITY: Claude may enhance the prompt, then fal.ai `fal-ai/nano-banana-pro` makes the image. The endpoint exists in the server, but no panel calls it (CLAUDE.md says so). Output: image asset.
- USE CASE FOR JORDAN: Only if he wants to bring it back. Not in use now.
- DECISION MODEL: LLM free-text (prompt enhancement).
- DETERMINISTIC PARTS: calling the model.
- DATA IT NEEDS: prompt; fal key.
- RULES WORTH COPYING: none.
- BECKY MATCH: becky-imagegen (already in becky; the closest working match).

### Creator OS in-app chat (natural language to publishing plan)  (source: src/react-app/components/CreatorOSPanel.tsx; local-ffmpeg-server.js `handleCreatorOSChat*`, `runCreatorOSChatJob`, `runCreatorOS`)
- PURPOSE: Run the CreatorOS commands in the editor's right-hand panel by typing what you want, for example "download the video in the timeline and upload it to all socials".
- FUNCTIONALITY: First message is the API key (masked), saved by the CLI itself (not the server). Each later message goes to Claude Sonnet 5 with the CLI reference and the playbook text. Claude returns a strict JSON plan of steps: `render` (the timeline export) or `cli` (a `creatoros` command with args). Steps are chained with placeholders (`{{RENDER_PATH}}`, `{{MEDIA_URL}}`, `{{ACCOUNT_IDS}}`). The server runs the steps in order, and the chat shows progress. Any command containing `:delete` or `:cancel` is blocked unless the message contains "yes", "confirm", or "go ahead". Input: chat message and the timeline. Output: executed steps and results.
- USE CASE FOR JORDAN: Render the timeline and post it everywhere from the editor, without opening a terminal.
- DECISION MODEL: LLM free-text (Claude plans the steps as JSON). A System One candidate: "Which CreatorOS operation does this request ask for?" (choice over the CLI commands), which would replace the free-form plan.
- DETERMINISTIC PARTS: the step runner, placeholder substitution, the delete/cancel block, the render call, the CLI calls.
- DATA IT NEEDS: the timeline render, the CreatorOS key (stored by CLI), accounts list, Anthropic key.
- RULES WORTH COPYING: "Safety: commands matching :delete/:cancel are blocked server-side unless the user's message contains explicit confirming language." "Ask-first, remember-after: a fresh browser always gets the API key greeting."
- BECKY MATCH: none found.

### Manual timeline editing (clips)  (source: src/react-app/components/Timeline.tsx, TimelineClip.tsx; src/react-app/hooks/useProject.ts; Home.tsx handlers)
- PURPOSE: Place, move, resize, cut, and delete clips by mouse.
- FUNCTIONALITY: Six tracks: T1 captions, V3 top overlay, V2 overlay, V1 base video, A1 and A2 audio. Drag to move (`moveClip`), drag edges to resize (`resizeClip`), split at the playhead (`splitClip`, guard 0.05 s), delete the selected clip (Delete key), delete all on a track (button), zoom in and out, play, stop, and auto-snap toggle. With auto-snap on, deleting a clip ripples later clips on the same track backward. Add text overlay button. Input: mouse. Output: the timeline state.
- USE CASE FOR JORDAN: Normal editing without AI.
- DECISION MODEL: none.
- DETERMINISTIC PARTS: everything.
- DATA IT NEEDS: the project state.
- RULES WORTH COPYING: "Ripple delete: When autoSnap is true, deleting a clip shifts subsequent clips on the same track backward." "Tracks are always initialized client-side (never loaded from server)." "Auto-save is intentionally disabled to prevent excessive saves during drag operations."
- BECKY MATCH: becky-vegas (VEGAS does this natively, the actual editor Jordan uses). No match needed for the web timeline.

### Timeline tabs (edit tabs for animations)  (source: src/react-app/components/TimelineTabs.tsx; useProject.ts `createTimelineTab`, `switchTimelineTab`, `closeTimelineTab`, `updateTabClips`)
- PURPOSE: Open an animation in its own timeline tab so it can be edited without changing the main timeline.
- FUNCTIONALITY: A tab has its own clips list. Operations check whether the active tab is main and route to the tab's clips. Create (+ button), switch, close. Input: clicks. Output: separate timeline per tab.
- USE CASE FOR JORDAN: Work on an animation in isolation, then close the tab.
- DECISION MODEL: none.
- DETERMINISTIC PARTS: everything.
- DATA IT NEEDS: the clip list per tab.
- RULES WORTH COPYING: "All move/resize/delete operations must check activeTabId !== 'main' and dispatch to updateTabClips instead."
- BECKY MATCH: none found.

### Clip transform properties  (source: src/react-app/components/ClipPropertiesPanel.tsx; CaptionPropertiesPanel.tsx)
- PURPOSE: Set exact position, scale, rotation, and crop for a clip.
- FUNCTIONALITY: Panel shows Position, Scale, Rotation, Crop for the selected clip. Caption clips on T1 get the caption panel instead (Auto captions block). Input: number fields and sliders. Output: transform saved on the clip.
- USE CASE FOR JORDAN: Place a logo exactly in the corner.
- DECISION MODEL: none.
- DETERMINISTIC PARTS: everything.
- DATA IT NEEDS: clip transform values.
- RULES WORTH COPYING: "Overlay images anchor at left 50% + x px, top 70% + y px in the preview." (directOps). "B-roll images go on V3 with default scale: 0.2, centered." Corner offsets are fixed in code.
- BECKY MATCH: none found.

### Asset library and uploads  (source: src/react-app/components/AssetLibrary.tsx, VideoUpload.tsx; useProject.ts `uploadAsset`, `deleteAsset`, `refreshAssets`; local-ffmpeg-server.js `handleAssetUpload`, `generateThumbnail`)
- PURPOSE: Hold all source clips, images, audio, GIFs, and animations for a project.
- FUNCTIONALITY: Import files (button or drag and drop). The server makes thumbnails (video frames; images resized), reads duration, width, height and audio presence (ffprobe), and stores the file in the session folder. Assets can be deleted from the library (`deleteAsset`). Asset types: video, image, audio, gif, animation (Remotion, marked `aiGenerated`). Search for GIFs opens the GIF panel. Input: files. Output: assets with thumbnails and metadata.
- USE CASE FOR JORDAN: Drop his footage in, then drag it to the timeline.
- DECISION MODEL: none.
- DETERMINISTIC PARTS: everything.
- DATA IT NEEDS: media files.
- RULES WORTH COPYING: "Image clips default to 5-second duration everywhere." "Sessions persist to /tmp/hyperedit-ffmpeg/sessions/{sessionId}/" (a Mac temp folder, so it can be cleared by the OS; relevant if Jordan adopts this).
- BECKY MATCH: becky-clip is the only name in the becky INDEX that looks close (name only; not verified).

### Final render / export  (source: local-ffmpeg-server.js `handleProjectRender`, `handleRenderDownload`, `buildAssFromCaptions`; Home.tsx; useProject.ts `renderProject`)
- PURPOSE: Export the finished timeline as one video file.
- FUNCTIONALITY: Takes the tracks, clips, transforms, and captions. Captions are written as ASS subtitles with the chosen font, colors, position, and animation, and burned in. Output is a rendered file that can be downloaded (`/session/:id/download` or renders).- USE CASE FOR JORDAN: Produce the file to upload or to pass to CreatorOS.
- DECISION MODEL: none.
- DETERMINISTIC PARTS: everything.
- DATA IT NEEDS: timeline and asset files.
- RULES WORTH COPYING: "Test the full export flow - ensure rendered video actually works" (TODO.md; not yet verified by the author).
- BECKY MATCH: becky-vegas (VEGAS renders natively). Not a match for the web renderer.

### Project save and load  (source: src/react-app/hooks/useProject.ts `saveProject`, `loadProject`; local-ffmpeg-server.js `handleProjectGet`, `handleProjectSave`)
- PURPOSE: Keep a project's timeline, caption styles, and settings between sessions.
- FUNCTIONALITY: Saves are manual (auto-save is off on purpose). Stored as `project.json` in the session folder plus `assets-meta.json` (aiGenerated, duration, editCount). The session ID is kept in browser localStorage (`clipwise-session`). If the server restarts and the session is gone, the stored session is cleared and a new one is made. Output: saved project.
- USE CASE FOR JORDAN: Come back to a half-edited video the next day.
- DECISION MODEL: none.
- DETERMINISTIC PARTS: everything.
- DATA IT NEEDS: the timeline, asset metadata.
- RULES WORTH COPYING: "Auto-save is intentionally disabled to prevent excessive saves during drag operations. Saves must be triggered explicitly via saveProject()."
- BECKY MATCH: none found (VEGAS project files are the real save for Jordan).

### Roadmap items written but not built  (source: TODO.md; docs/todo.md)
- PURPOSE: The creators' own list of what they wanted next. Useful as a list of ideas, not as a feature.
- FUNCTIONALITY: Not built. Listed as "Future Ideas": auto-captions with styling (partly built, Auto captions block), B-roll suggestion and insertion (partly built, B-roll block), music matching to video mood, social media format presets (9:16, 1:1; partly built in the Shorts generator), direct publish to YouTube and TikTok (partly built via CreatorOS, post-longform block), collaboration features. Also listed: undo and redo, more AI edit commands (speed up, add music, auto-captions), custom keyword lists for GIF extraction, GIF position and size controls, audio track visualization, keyboard shortcuts, project save to cloud, user accounts, more export formats.
- USE CASE FOR JORDAN: Mostly a checklist of gaps. Undo/redo and keyboard shortcuts are the two that would most help a VEGAS editor used to both.
- DECISION MODEL: none.
- DETERMINISTIC PARTS: n/a.
- DATA IT NEEDS: n/a.
- RULES WORTH COPYING: "Don't build these until you have users asking for them."
- BECKY MATCH: none needed. (Undo and keyboard shortcuts are VEGAS behaviors.)

---

## Summary of the System One question (for the becky research list)

Places in HyperEdit where a fixed Jev-style question replaces a free-text LLM decision (copy these into the research list):
1. Director route: which of 15 workflows; does the request mean the open animation (Director workflow router). Already in use.
2. Timeline op: operation, target, track, size, position (Timeline operations). Already in use.
3. Vault media: brand, kind, plural or singular, descriptive or not (Brand media vault). Already in use.
4. Comment triage: REPLY / SKIP / ESCALATE / LIKE-ONLY / HIDE (respond-to-comments).
5. DM triage: REPLY / ARCHIVE / ESCALATE / LEAVE (respond-to-dms).
6. Shorts ranking: per-span score 0-100 and "self-contained?" (Shorts generator).
7. Chapters: "does this moment start a new topic?" (Chapter generation).
8. Post copy: "is the title a filename or placeholder?" (post-longform).
9. FFmpeg effect family: which of 15 families (Director FFmpeg edits).

Counts: 45 blocks total (Part A: 12 CreatorOS blocks; Part B: 33 app blocks, including one for roadmap ideas that are not built).
