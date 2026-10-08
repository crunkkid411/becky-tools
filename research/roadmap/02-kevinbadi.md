# Kevin Badi repos + "30 Jev AI + Claude Use Cases" - functionality catalogue

Sources: kevinbadi/creator-os-starter, kevinbadi/open-edits, kevinbadi/seo-agent-kit, kevinbadi/social-agents (all cloned with --depth 1 into the roadmap src folder), plus the Jev video note and transcript (MT9uNomIvgk).

## Bottom line

- Everything is catalogued below: 158 blocks in total. That is all 72 creator-os-starter skills, 30 creator-os features and libraries, 8 open-edits blocks, 6 seo-agent-kit blocks, 19 social-agents blocks, and the 23 Jev use cases.
- The Jev video title promises 30 use cases. Its transcript and note only name 23. The 23 are listed. The other 7 are NOT in the video text I have, so I did not invent them.
- Kevin's creator-os app already runs Jev as a router on every prompt (hook: jev-router.mjs). Becky has the same idea in becky-decide (Laya on CPU, plus an optional hosted Jev path). I found no becky hook that sends every prompt through a typed decision model (checked the INDEX and SKILL text only, not the Go code). That is the closest "already solved" overlap.
- The "watchdog" Jordan asked about is real code in creator-os: src/lib/agent-edits/watchdog.ts. It auto-resumes stalled edit runs up to 8 times in 36 hours, based on error-text patterns. It is regex-based, not AI.
- Several of Kevin's browser skills (LinkedIn, Instagram, Facebook, WhatsApp, X, YouTube engage/upload) drive Chrome or Playwright. That conflicts with Jordan's approved browser rule (agent Firefox only). Do not port them as-is.
- Many creator-os skills use fal.ai, Apify, Instantly, Zernio, Late and Gemini paid APIs. Kevin added a hard stop on fal.ai spend (lib/fal-gate.js) on 2026-09-05. That is data for Jordan's budget decisions, not a feature to copy.

## How to read this file

- DECISION MODEL: "System One/Jev" means a typed yes/no, pick-one or score question. "LLM free-text" means the source asks a general model to write something. "(could be System One: ...)" is my suggested typed question.
- Items tagged [desc] were read from the SKILL.md description and headings only. Items tagged [header] were read from the file header only. Everything else was read in the body.
- BECKY MATCH names a becky-tools tool only if INDEX.md, README.md or SKILL.md mentions it for that job. Otherwise "none found". Read-only: nothing in becky-tools was changed.

---

## PART A - creator-os-starter: Jev router, Laya, Jev dispatcher, Treg, agents, scheduler

### Jev prompt router hook (every prompt goes through System One first)  (source: creator-os-starter/.claude/hooks/jev-router.mjs)
- PURPOSE: Stop Claude being invoked for simple read-only questions. One typed decision call decides who handles each prompt.
- FUNCTIONALITY: UserPromptSubmit hook. For every prompt it sends the prompt to TypeSafe Jev (POST api.typesafe.ai/v1/systemone, model jev-latest, ~200 ms, 6 s timeout). Jev answers: lane (tool | build | produce | assess | conversation), skill (picked from a catalog built from every SKILL.md description), persona (kevbuildsapps | kev_ai | megan | danny | creator_os_brand | agencies | none), risk (read_only | local_files | publishes | destructive), platform, is_correction, wants_deploy, near_slot (yes/no), complexity (0-3). If lane=tool and confidence >= 0.75 (DISPATCH_MIN), it calls the Marketing OS dispatcher (/api/jev) and BLOCKS the prompt: the answer prints in the terminal and Claude is not called (zero Claude tokens). Otherwise the decision is attached to the prompt as context. Fails open on any error. Escape: prompt starts with "/" or "!", or add "--jev off" anywhere, or JEV_ROUTER=0.
- USE CASE FOR JORDAN: Ask "what comments came in today" or "next open slot" and get the answer without spending Claude tokens. Also tags each request with the right skill before Claude starts. Could route Becky questions to becky-ask the same way.
- DECISION MODEL: System One/Jev. Questions (verbatim intent): "Who should handle this prompt?" (tool/build/produce/assess/conversation); "Which brand or persona is this about, if any?"; "What is the blast radius of doing what is asked?" (read_only/local_files/publishes/destructive); "Which skill or workflow in the repo owns this?" (catalog); "Is Kevin correcting how something was done, or stating a preference?"; "Does Kevin expect the result to be live on production (Railway)?"; "Does this ask for a deploy or restart that could collide with a scheduled publish slot?"; "How much work is this?" (score 0-3).
- DETERMINISTIC PARTS: Log writing, escape hatches, table rendering, the confidence thresholds, the skill catalog build (reads SKILL.md frontmatter), the fail-open logic.
- DATA IT NEEDS: Skill catalog (SKILL.md descriptions), the live Marketing OS tool list, a log of every prompt and decision (jev-router.log, JSONL).
- RULES WORTH COPYING: TOOL_MIN 0.6 (tool lane read) and DISPATCH_MIN 0.75 (dispatcher must be confident). Example from the source comment: "what is my social growth the past day" scored 0.79 and fell through to Claude, which the dispatcher would have answered at 100%, so the rule was fixed on 2026-09-19. Publish-hour rule: posts go out at 12am/3/6/9pm and 1/4/7/10pm ET.
- BECKY MATCH: partial. becky-ask (act-vs-discuss) and becky-route exist. becky-decide answers typed questions. No becky hook runs on every prompt. Check becky-route before building a new one.

### Laya System One sidecar (local typed-decision model)  (source: creator-os-starter/scripts/laya/server.py, finetune.py, build_tool_dataset.mjs, src/app/api/laya/route.ts, src/app/dashboard/laya/page.tsx)
- PURPOSE: Run the open Laya System One checkpoint locally for typed decisions, instead of paying a hosted model per prompt.
- FUNCTIONALITY: FastAPI server (port 8000 by default, LAYA_URL) loads the Apache-2.0 checkpoint convaiinnovations/laya (typed-decisions variant if present), optional fine-tuned weights from scripts/laya/weights/. Exposes /evaluate with the same contract as laya-decision-brain. CPU or CUDA via LAYA_DEVICE. build_tool_dataset.mjs writes gold labels for the Marketing OS tool questions (copied from the live option text). finetune.py trains on those labels (RLCD-style, per the Laya typed-decisions notes). The Next.js /api/laya route proxies to it; the /dashboard/laya page shows results.
- USE CASE FOR JORDAN: The same local decision engine Becky uses (becky-decide runs Laya on CPU, about 0.5 s). Shows how a Laya fine-tune on your own labelled choices is built end to end.
- DECISION MODEL: System One/Laya (it IS the decision model). Typed questions as in the Jev router entry above.
- DETERMINISTIC PARTS: Dataset building from live option text, the HTTP wrapper, label mapping.
- DATA IT NEEDS: Labelled examples: prompt -> correct tool/lane. Kevin's gold set comes from build_tool_dataset.mjs.
- RULES WORTH COPYING: Gold labels must use the exact option text the live app shows, not short slugs (the source comment says so).
- BECKY MATCH: becky-decide (cmd/decide, internal/systemone, Laya ONNX on CPU, about 0.5 s). Already solved and in use. Becky's own notes say to run "becky-decide --selftest" and not trust published example numbers. Accuracy of Laya versus other models was not checked here.

### Jev chat tool dispatcher (read-only data questions)  (source: creator-os-starter/src/lib/jev/tools.ts, src/app/api/jev/route.ts, src/app/dashboard/jev/*)
- PURPOSE: Answer data questions about the social accounts from one catalog of tools, with Jev picking the tool and time range.
- FUNCTIONALITY: POST {message} -> Jev chooses one tool from TOOLS + a time range (today, yesterday, last_7_days, last_30_days, this_month, year_to_date, all) + sort, platform, status, quantity, media pillar, media brand, media kind. The dispatcher runs the tool. Tools: get_comments, get_agent_posts, get_slots, get_content_posts, get_news, get_link_clicks, get_followers, get_revenue (RevenueCat), get_automations, get_post_analytics, get_media (Obsidian B-roll vault), delete_comment, reply_comment, none.
- USE CASE FOR JORDAN: "How many views did my last 5 videos get", "what is my growth this week", "any unanswered comments". Only useful if Jordan has a similar multi-platform analytics feed. For Becky, the equivalent is looking up past edits or transcripts.
- DECISION MODEL: System One/Jev (tool choice, range choice, sort, platform, status, quantity). Question e.g. "Which catalogued data tool answers this, and over what time range?"
- DETERMINISTIC PARTS: Every tool's data fetch and table formatting (cli-table3 specs per tool). Time-range maths in ET.
- DATA IT NEEDS: Social post analytics, comments, followers, RevenueCat, Obsidian B-roll vault index.
- RULES WORTH COPYING: Keep tool descriptions distinct and honest, because the description IS the choice criterion (the source says so). Table layout per tool, not free text.
- BECKY MATCH: none found for social analytics. Partial: becky-ask for natural-language questions.
- FLAG: The file header says "All tools are read-only", but TOOLS includes delete_comment and reply_comment. Those write to live accounts. Treat that header as wrong until checked.

### Treg live-data desk (Jev picks a catalog lane, then fetches live data)  (source: creator-os-starter/src/lib/treg/*, src/app/api/treg/*, src/app/dashboard/treg/*)
- PURPOSE: Pull live trend data (TikTok viral, search terms, music chart) and turn it into rows you can post to.
- FUNCTIONALITY: Stations: Jev route -> Catalog (search 3600+ endpoints) -> Jev pick (cheapest reliable tool) -> Treg call (fetch live data) -> Result (rows). Search lanes: "US TikTok For You" (viral/trending videos), "TikTok search terms" (daily trending terms), "TikTok music chart" (sounds to use today), "Catalog as asked", "Skip". Each run is stored (treg run records with per-station status). The live-data provider is not named in the code I read.
- USE CASE FOR JORDAN: "Which TikTok sounds are trending today" or "what are people searching this week" to decide video topics. Only if Jordan wants trend input.
- DECISION MODEL: System One/Jev (lane pick, tool pick). Question: "Which lane (tiktok_us_feed | tiktok_search | tiktok_music | catalog | skip) fits this ask?"
- DETERMINISTIC PARTS: The station state machine, the lane definitions, storage, row output.
- DATA IT NEEDS: A live trend-data catalog (external API). No Jordan data needed.
- RULES WORTH COPYING: Stations are an explicit state machine with per-step status, so a failed step shows where it died.
- BECKY MATCH: none found.

### Agent chat (headless Claude Code in the repo)  (source: creator-os-starter/src/app/api/agent/chat/route.ts, src/app/dashboard/agent/*)
- PURPOSE: Chat with an agent in the dashboard that can read, search, edit and run code in the repo, with the same permissions as a local Claude session.
- FUNCTIONALITY: POST {message, sessionId?} streams NDJSON. Spawns `claude -p` (headless Claude Code, on Kevin's plan) from the repo root, so CLAUDE.md, memory and skills load. The live Marketing OS brief is appended to the system prompt. sessionId resumes a session. Localhost only, behind the password gate. The UI is VoiceAgent.tsx (voice-based dashboard agent).
- USE CASE FOR JORDAN: A dashboard front end for Claude Code. Would sit behind the same trust rules as Jordan's local session. Not a priority: Jordan already has Claude Code and Whoretana.
- DECISION MODEL: LLM free-text (the whole chat). No System One question.
- DETERMINISTIC PARTS: Spawning, streaming, session resume, password gate.
- DATA IT NEEDS: The repo itself, CLAUDE.md, live dashboard brief.
- RULES WORTH COPYING: Passes the live state of the app into the agent's brief instead of making the agent guess.
- BECKY MATCH: partial. Whoretana plus becky-ask is the voice/agent front end. becky-harness exists for agent harness work.

### Scheduled content job registry (every timed content job)  (source: creator-os-starter/src/lib/content/registry.ts, cron.ts)
- PURPOSE: One list of every scheduled job, used both by the timer and by the Railway timeline, so they cannot disagree.
- FUNCTIONALITY: Jobs: megan-advice, danny-advice (advice carousels), danny-thread, megan-thread, kev-thread, kevbuildsapps-thread, kevbuildsagencies-thread (thread posts), megan-proof (proof post), creatoros-reaction, megan-reaction, danny-reaction (reaction videos), pro-tips-deck (weekly 15-slide deck), daily-tip-x (daily X tip), analytics-snapshot, revenuecat-customers, revenuecat-subs, posthog-web (snapshots), ai-news (news ingest), follower-growth. Cron arms one timer per job per hour (ET).
- USE CASE FOR JORDAN: A single schedule file for everything Jordan automates (YouTube publish, news digest, analytics snapshots). Matches the "many watchdogs, unclear what runs" problem if it becomes the one source of truth.
- DECISION MODEL: none (fixed schedule).
- DETERMINISTIC PARTS: All of it.
- DATA IT NEEDS: Job definitions and per-job hours (ET).
- RULES WORTH COPYING: Job definition shared by the timer and the timeline view, so the displayed schedule is the real one.
- BECKY MATCH: partial. becky-pipeline, becky-jobs and becky-foreman exist. Compare before adding anything.

### Business metrics and snapshots (PostHog, RevenueCat, followers, analytics)  (source: creator-os-starter/scripts/posthog-snapshot.mjs, revenuecat-subs-snapshot.mjs, follower-snapshot.mjs, analytics-snapshot.mjs; src/lib/posthog, src/lib/revenuecat, src/lib/followers)
- PURPOSE: Keep a daily record of web visitors, subscriptions/downloads and follower counts so trends can be read later.
- FUNCTIONALITY: Scheduled scripts pull PostHog web numbers, RevenueCat new customers and active subscriptions/MRR, and follower counts per connected account, and write them to the database for dashboards (Calendar, Analytics, Accounts pages).
- USE CASE FOR JORDAN: Daily numbers for YouTube, Twitch or other channels: views, subs, watch time, to spot trends. Jordan's data would come from YouTube Analytics or similar.
- DECISION MODEL: none.
- DETERMINISTIC PARTS: All of it.
- DATA IT NEEDS: Analytics APIs with keys (PostHog, RevenueCat, social platforms).
- RULES WORTH COPYING: One snapshot per day per metric, stored with the date, so trends are comparable.
- BECKY MATCH: none found for channel analytics. Partial: becky-report.

### Knowledge graph of the system ("System brain")  (source: creator-os-starter/src/app/dashboard/gitnexus/*, src/app/api/gitnexus/*, scripts/gitnexus-export.mjs, scripts/system-graph-export.mjs)
- PURPOSE: See how the apps, skills, cron jobs and database tables connect, as an interactive graph.
- FUNCTIONALITY: Export scripts build a graph of the repo (code graph export and system graph export). The dashboard page renders a canvas graph with tabs and a repo explorer. API routes return the graph and the system map.
- USE CASE FOR JORDAN: Lets Jordan see what Becky's tools depend on before changing one. Good for the "which tools are actually used" question.
- DECISION MODEL: none.
- DETERMINISTIC PARTS: The whole export and render.
- DATA IT NEEDS: The codebase and config files.
- RULES WORTH COPYING: Generated from code, so it cannot drift from what is actually there.
- BECKY MATCH: none found for a visual graph. becky-docs/INDEX.md is a text version.

### Obsidian vault contact sheet (Jev media answers)  (source: creator-os-starter/.claude/hooks/vault-sheet.py, src/lib/jev/obsidian.ts)
- PURPOSE: When the Jev answer is a media list (logos, B-roll), show the actual pictures in a Quick Look window, not just file names.
- FUNCTIONALITY: The jev-router hands media rows (name, brand, kind, file, poster, duration) as JSON to a Python script. It renders the images and video poster frames into one PNG on a neon-blue card grid and opens it with Quick Look. The Obsidian media vault (Marketing OS Broll) is the source.
- USE CASE FOR JORDAN: "Show me the Claude logo" or "footage of the app" from a media vault, without searching folders. Useful for Jordan's B-roll library.
- DECISION MODEL: System One/Jev for the lookup (media_brand, media_kind, media_pillar choices). The rendering is none.
- DETERMINISTIC PARTS: Rendering, grid layout, Quick Look call.
- DATA IT NEEDS: An indexed media vault with brand and kind tags.
- RULES WORTH COPYING: Brand logos and icons come from a vault, so the same logo is not re-fetched each time.
- BECKY MATCH: none found for a visual contact sheet of a media vault. Partial: becky-moment / becky-hits for frame previews.

## PART B - creator-os-starter skills (.claude/skills, 72 folders, A to L first)

Shared context for these: Kevin's app runs several AI "personas" (Megan, Danny, Kev, a Creator OS brand). Most skills post to Instagram, TikTok, YouTube, LinkedIn or X through Zernio or Late, and write copy with an LLM. Model routing lives in lib/llm.js: text uses DeepSeek v4 Flash (Ollama cloud), images use Qwen 3.5 vision. Several older skills still mention Gemini or Claude.

### account-analytics  (source: creator-os-starter/.claude/skills/account-analytics) [header]
- PURPOSE: Keep a daily record of how each connected account performs.
- FUNCTIONALITY: Syncs daily account metrics from the Late API into the database: impressions, reach, likes, comments, shares, views per platform, plus follower counts. Run from a script with options (see SKILL.md Usage/Options).
- USE CASE FOR JORDAN: Daily channel-level trend numbers for YouTube, once an analytics source is wired.
- DECISION MODEL: none.
- DETERMINISTIC PARTS: All of it.
- DATA IT NEEDS: Platform analytics API (Late). Accounts list.
- RULES WORTH COPYING: One row per account per day for trend comparison.
- BECKY MATCH: none found.

### advice-carousel  (source: creator-os-starter/.claude/skills/advice-carousel)
- PURPOSE: Daily "marketing manager advice" post that makes the persona look like the expert, with the app as one tip.
- FUNCTIONALITY: Builds a 10-slide deck from a locked skeleton: hook (fal-generated lifestyle image with a proof-first headline and proof inset), stakes line, reframe line, 10 tips (one per slide), one app-screenshot integration slide, proof slide, recap checklist, and a link-in-bio CTA. Tips come from a bank of 109 tips (jun-yuh-tips.json) and transcripts. Publishes the full deck to TikTok with a trending sound, a 10-item cut to Instagram, and a one-tip-per-post thread to X and Threads (advice-publish-thread.js). Templates exist for Megan and Danny.
- USE CASE FOR JORDAN: A weekly "tips for YouTubers" deck built from a bank of tips Jordan approves. The bank and the rules are the valuable part.
- DECISION MODEL: LLM free-text for hook copy; none for the rotation. (could be System One: "Does this tip contradict the product?" The skill uses a fixed EXCLUDE list instead.)
- DETERMINISTIC PARTS: Deck skeleton, rotation by day index, per-platform cut, thread splitting (X 280 chars, Threads 500).
- DATA IT NEEDS: Tip bank, transcripts, screenshots of the app, persona claims ledger.
- RULES WORTH COPYING: Character caps per platform (X 280, Threads 500). Missing accounts are skipped rather than failing the run.
- BECKY MATCH: none found.

### agent-posts  (source: creator-os-starter/.claude/skills/agent-posts)
- PURPOSE: Drop a finished video in and have it scheduled everywhere with captions and cover done for you.
- FUNCTIONALITY: Upload a video to the Agent Posts dashboard. It transcribes the video, reads the "comment X" keyword, writes captions per platform, builds a 9:16 cover, picks a slot and schedules through Creator OS. Refuses to run without its own Creator OS key (the SKILL.md says STOP and ask for a key on first run). Dashboard page at /dashboard/agent-posts.
- USE CASE FOR JORDAN: Drop a finished long or short video, get it scheduled to YouTube Shorts, TikTok, Instagram, with captions. Matches the post-publish problem Jordan wants automated.
- DECISION MODEL: LLM free-text (captions, hook lines). Could be System One: "Does this video's first 3 seconds show the hook?" (yes/no) before scheduling.
- DETERMINISTIC PARTS: Transcription, keyword extraction, cover build, slot picking, scheduling call.
- DATA IT NEEDS: The finished video, transcript, brand notes, platform account IDs.
- RULES WORTH COPYING: Never reuse or invent an API key. Ask the user before first run.
- BECKY MATCH: partial. becky-reel (cover/reel work), becky-captions, becky-subtitle, becky-transcribe. No scheduler found in becky.

### apify-google-maps  (source: creator-os-starter/.claude/skills/apify-google-maps) [desc]
- PURPOSE: Build a local business lead list from Google Maps.
- FUNCTIONALITY: Runs an Apify Google Maps actor with keyword + location, writes results to the business_leads table. Python scripts for sheets export and enrichment.
- USE CASE FOR JORDAN: Lead lists for a sponsor or client outreach. Not a content task.
- DECISION MODEL: none.
- DETERMINISTIC PARTS: All of it. Paid Apify actor.
- DATA IT NEEDS: Apify key, search terms.
- RULES WORTH COPYING: none specific.
- BECKY MATCH: none found.

### carousel-gen  (source: creator-os-starter/.claude/skills/carousel-gen) [header]
- PURPOSE: Render a finished carousel from a plan.
- FUNCTIONALITY: Takes an authored plan JSON and renders 1080x1350 slides, using library photos and/or fal nano-banana-2 images, with burned-in overlay text and a carousel.json sidecar for publishing. Also restyle.js and carousel-overlay.js.
- USE CASE FOR JORDAN: Thumbnail-style image sets (for example a 10-image YouTube community or Instagram carousel).
- DECISION MODEL: none (the plan is written by a person or a skill).
- DETERMINISTIC PARTS: Overlay text placement, sizes, export.
- DATA IT NEEDS: Plan JSON, reference images, persona face.
- RULES WORTH COPYING: Fixed output size (1080x1350) and a sidecar file so publish and QA read the same record.
- BECKY MATCH: partial. becky-imagegen (default image gen in becky), becky-compose.

### carousel-publish-instagram  (source: creator-os-starter/.claude/skills/carousel-publish-instagram) [desc]
- PURPOSE: Post an Instagram carousel with a trending sound on slide 1.
- FUNCTIONALITY: ffmpeg turns slide 1 plus a favorited sound from the sounds table into a short video, which leads the carousel so the whole post plays that sound. Publishes via Zernio.
- USE CASE FOR JORDAN: Sound-led carousel for Instagram. Only if Jordan posts carousels.
- DECISION MODEL: none.
- DETERMINISTIC PARTS: All of it.
- DATA IT NEEDS: Sound library, rendered slides.
- RULES WORTH COPYING: The sound is baked into slide 1 so it always plays.
- BECKY MATCH: none found.

### carousel-publish-tiktok  (source: creator-os-starter/.claude/skills/carousel-publish-tiktok) [desc]
- PURPOSE: Post a photo carousel to TikTok with a native trending sound.
- FUNCTIONALITY: Images only. Uses Zernio with autoAddMusic. Trigger: "post a TikTok carousel for a persona".
- USE CASE FOR JORDAN: Photo-mode TikTok post. Limited value for a video creator.
- DECISION MODEL: none.
- DETERMINISTIC PARTS: All of it.
- DATA IT NEEDS: Rendered slides, TikTok account ID.
- RULES WORTH COPYING: Let TikTok add the music (autoAddMusic) rather than baking it in.
- BECKY MATCH: none found.

### carousel-publish  (source: creator-os-starter/.claude/skills/carousel-publish)
- PURPOSE: Generic carousel publisher to Instagram and TikTok (the two platforms with native carousels).
- FUNCTIONALITY: Reads carousel.json from carousel-gen. Uploads each slide to Insforge storage, then posts via Zernio. "Safe by default": dry run unless told to publish.
- USE CASE FOR JORDAN: The publish step for any image set. The "dry run first" default is the useful part.
- DECISION MODEL: none.
- DETERMINISTIC PARTS: All of it.
- DATA IT NEEDS: carousel.json, storage and Zernio keys.
- RULES WORTH COPYING: Safe default: no live post without an explicit publish flag.
- BECKY MATCH: none found.

### carousel-qa  (source: creator-os-starter/.claude/skills/carousel-qa)
- PURPOSE: Stop AI-generated slides with visible defects from going live.
- FUNCTIONALITY: Sends each slide to a vision model (Claude Opus 4.8 by default, QA_MODEL to override) with a strict rubric: hands and fingers, face integrity, persona identity match, logo distortion, stray text or watermarks, scene physics, duplicated people. Returns per slide: pass or fail, severity, issues, regen_hint, and writes qa.json. With --fix, each failing slide is regenerated through FAL nano-banana-2 with a repair hint and re-checked, up to --max-attempts times. Report-only mode exits with code 2 if any slide fails (usable as a publish gate).
- USE CASE FOR JORDAN: A "does this thumbnail or still have an obvious error" gate before any AI image goes out. Most relevant to Jordan's planned AI thumbnail maker.
- DECISION MODEL: LLM vision free-text, returning structured pass/fail. System One candidate: "Does this slide show any of: malformed hands, distorted face, warped logo, stray text? (yes/no per item)".
- DETERMINISTIC PARTS: Exit codes, qa.json format, the retry loop count, the publish gate.
- DATA IT NEEDS: The rendered slide images, the persona reference face, the plan (for regeneration).
- RULES WORTH COPYING: Grade conservatively. Only clear, visible defects fail; uncertain ones are logged low but pass. The publish gate is a hard exit code, not a suggestion.
- BECKY MATCH: partial. becky-vision (vision models), becky-judge (two-stage judge). No thumbnail defect check found.

### comment-responder  (source: creator-os-starter/.claude/skills/comment-responder) [header]
- PURPOSE: Answer comments on all platforms with a CTA to the academy.
- FUNCTIONALITY: Generates personalised replies with Gemini, pointing to the academy CTA, likes comments, and tracks state in a local JSON file. Cron-driven. Options in SKILL.md.
- USE CASE FOR JORDAN: Reply to YouTube comments with a fixed CTA. Risky: auto-replies on a personal channel need a human rule set. Compare with Social Agents' triage rules (respond-to-comments), which are stricter.
- DECISION MODEL: LLM free-text (reply text). Could be System One: "Is this comment a question that needs a substantive reply? (yes/no)" and "Is it spam, troll, or escalate? (pick one)".
- DETERMINISTIC PARTS: Likes, state file, cron schedule, dedupe.
- DATA IT NEEDS: Comments, the CTA text, the academy link.
- RULES WORTH COPYING: Dedupe by comment ID so no comment is answered twice.
- BECKY MATCH: none found.

### copywriter-longform-kevin  (source: creator-os-starter/.claude/skills/copywriter-longform-kevin)
- PURPOSE: Write a YouTube long-form script (10 to 12 minutes) in Kevin's voice.
- FUNCTIONALITY: Style model built from his 20 most recent long videos (references/style.json, transcripts.md). Hard targets: 10 to 12 minutes, 2000 to 2500 words, about 200 words per minute, 90 to 140 sentences, hook of 20 to 40 words in the first 1 to 2 sentences, intro 90 to 180 words, 7 to 9 sections, CTA: subscribe mid-intro and academy plus next video at the end. Output format and a self-check list at the end. Rebuild with copywriter-ingest-youtube.mjs and copywriter-build-style.mjs.
- USE CASE FOR JORDAN: Jordan's own long-form script in his voice. The most directly reusable item in this repo: the method (measure your own videos, write the numbers down, then write to them) matters more than Kevin's numbers.
- DECISION MODEL: LLM free-text for the script. Typed checks that could be System One: "Does this hook finish inside 8 seconds? (yes/no)", "Is each section one idea? (yes/no)".
- DETERMINISTIC PARTS: Word count, sentence count, reading-pace maths, the self-check list.
- DATA IT NEEDS: Jordan's own transcripts (his 20 most recent long videos) and stats.
- RULES WORTH COPYING: Measure your own videos first (median length 11:30, median 2316 words), then write to that band. Keep long and short scripts separate ("do not mix them").
- BECKY MATCH: partial. becky-transcribe (transcripts), becky-research. No style model found.

### copywriter-shortform-kevin  (source: creator-os-starter/.claude/skills/copywriter-shortform-kevin)
- PURPOSE: Write a 40 to 45 second talking-head short script in Kevin's voice.
- FUNCTIONALITY: Style model from his 14 most recent reels. Targets: 140 to 170 words, about 215 words per minute, 7 to 10 sentences, hook in the first 2 sentences (30 to 40 words, done within 8 seconds), CTA in the last 1 to 2 sentences starting at about 83% of the script, comment word used in 10 of 14 videos. Hook patterns: "So" + what I built + claim (most common), "All right" hype opener + free tool, contrarian "group secret".
- USE CASE FOR JORDAN: Jordan's Shorts script in his voice, with his own hook patterns. Measure his shorts first.
- DECISION MODEL: LLM free-text. Could be System One: "Does the first sentence name a concrete result? (yes/no)". 
- DETERMINISTIC PARTS: Word count, timing estimate, CTA position check.
- DATA IT NEEDS: Jordan's reels/shorts transcripts and view stats.
- RULES WORTH COPYING: Hook pattern names are a usable checklist. Count words, not guessed seconds.
- BECKY MATCH: partial. becky-short, becky-moment (short-clip picking). No script voice model found.

### daily-carousel  (source: creator-os-starter/.claude/skills/daily-carousel) [header]
- PURPOSE: Run a fully automatic daily "day in the life" carousel for Megan.
- FUNCTIONALITY: Builds the day's plan from a rotation (8 hooks, outfits, fictional client names), renders 9 to 10 slides via carousel-gen, gates on carousel-qa with auto-fix, overlays text, publishes to TikTok and Instagram. Aborts before publishing if a slide still fails QA; the draft stays in the carousels feed.
- USE CASE FOR JORDAN: Shows the auto-publish plus QA-gate pattern. Jordan's content is different, so only the gate pattern transfers.
- DECISION MODEL: LLM vision free-text inside the QA gate. Otherwise none.
- DETERMINISTIC PARTS: Rotation by day index (dayIndex % bank length), the publish gate, the time table.
- DATA IT NEEDS: Rotation banks, reference face, FAL and Zernio keys.
- RULES WORTH COPYING: Publish gate: a failed QA stops the publish and leaves the draft for review.
- BECKY MATCH: none found for an automatic publish gate.

### danny-daily-carousel  (source: creator-os-starter/.claude/skills/danny-daily-carousel) [header]
- PURPOSE: Same as daily-carousel for persona Danny (6 PM ET daily).
- FUNCTIONALITY: Rotated hook, scenes and caption, then carousel-gen, a QA gate with alternate-swap retry, Manrope text overlay, TikTok publish with a native trending sound.
- USE CASE FOR JORDAN: Duplicate of daily-carousel for another persona. Not separately useful.
- DECISION MODEL: as daily-carousel.
- DETERMINISTIC PARTS: as daily-carousel.
- DATA IT NEEDS: as daily-carousel.
- RULES WORTH COPYING: Alternate-swap retry on QA failure.
- BECKY MATCH: none found.

### danny-longform-clone  (source: creator-os-starter/.claude/skills/danny-longform-clone) [header]
- PURPOSE: Turn one of Kevin's YouTube long videos into a version presented by persona Danny.
- FUNCTIONALITY: Persona-swapped transcript sent to HeyGen, a v2 single-pass composite (fullscreen plus webcam-bubble picture-in-picture), every-frame QA, and a Danny-swapped thumbnail. Triggered when Kevin drops a URL.
- USE CASE FOR JORDAN: Clone-as-avatar. Relevant only if Jordan wants an AI presenter. Sensitive: it impersonates a real creator's videos with a persona. Jordan should decide if he wants that.
- DECISION MODEL: LLM free-text (script swap). Could be System One for the frame QA: "Is the presenter's face visible and intact in this frame? (yes/no)".
- DETERMINISTIC PARTS: Pipeline steps, compositing, every-frame QA loop.
- DATA IT NEEDS: Source video, transcript, HeyGen avatar.
- RULES WORTH COPYING: Every-frame check before delivery (not sampled).
- BECKY MATCH: partial. becky-reel, becky-compose. No avatar clone in becky.

### facebook-outreach  (source: creator-os-starter/.claude/skills/facebook-outreach) [desc]
- PURPOSE: Send Facebook Messenger DMs from a personal account to business pages.
- FUNCTIONALITY: Playwright on facebook.com, saved session, normalises Facebook handles, tracks delivery status.
- USE CASE FOR JORDAN: Cold outreach. Not a content task.
- DECISION MODEL: none.
- DETERMINISTIC PARTS: All of it.
- DATA IT NEEDS: Lead list, session.
- RULES WORTH COPYING: Save and reuse a single browser session, rather than logging in each time.
- BECKY MATCH: none found. FLAG: uses Playwright on a personal account. Conflicts with Jordan's browser rule.

### facebook-page-finder  (source: creator-os-starter/.claude/skills/facebook-page-finder) [desc]
- PURPOSE: Find each business's Facebook page for outreach.
- FUNCTIONALITY: Google search via Apify matches business names to Facebook pages and writes the handle into business_leads.
- USE CASE FOR JORDAN: Lead enrichment only.
- DECISION MODEL: none (the match is a search).
- DETERMINISTIC PARTS: All of it.
- DATA IT NEEDS: Business leads, Apify key.
- RULES WORTH COPYING: none specific.
- BECKY MATCH: none found.

### find-skills  (source: creator-os-starter/.claude/skills/find-skills)
- PURPOSE: Help discover and install an agent skill that already exists for a task.
- FUNCTIONALITY: Guides a search of a skill leaderboard and registry, checks quality, presents options, then offers install. Lists common skill categories.
- USE CASE FOR JORDAN: Finding existing Claude skills before building one. Matches Jordan's "check if already solved" rule.
- DECISION MODEL: LLM free-text. Could be System One: "Does a published skill already cover this request? (yes/no) and which one?"
- DETERMINISTIC PARTS: Search and install commands.
- DATA IT NEEDS: Skill registry.
- RULES WORTH COPYING: Verify quality before recommending an install.
- BECKY MATCH: none (general tool).

### gemini-viral-shorts  (source: creator-os-starter/.claude/skills/gemini-viral-shorts) [desc]
- PURPOSE: Write viral Twitter and LinkedIn posts from a YouTube video.
- FUNCTIONALITY: Gemini watches the video URL and writes posts with a hook, stats and CTA, using a copywriter persona. Differs from youtube-to-viral-posts by using only Gemini.
- USE CASE FOR JORDAN: Turn a long video into social text posts. Two overlapping skills exist here; keep one.
- DECISION MODEL: LLM free-text (Gemini video analysis).
- DETERMINISTIC PARTS: Output length and format.
- DATA IT NEEDS: YouTube URL, Gemini key.
- RULES WORTH COPYING: none specific.
- BECKY MATCH: partial. becky-transcribe plus becky-judge could do the same without a cloud video model.

### infographic-gen  (source: creator-os-starter/.claude/skills/infographic-gen) [header]
- PURPOSE: Make 4:5 infographic or newspaper-style news posts with captions.
- FUNCTIONALITY: News mode: Gemini with Google Search grounding finds the top 5 viral stories from the past 7 days, writes a newspaper-style post (headline, subheadline, body, CTA). Standard mode: fact infographics on rotating pillars.
- USE CASE FOR JORDAN: News-to-post pipeline. Relevant to Jordan's "AI news" interest.
- DECISION MODEL: LLM free-text for ranking virality. System One candidate: "Is this story from the past 7 days and about AI tools? (yes/no)" as a filter before ranking.
- DETERMINISTIC PARTS: Date window, image size, rotation.
- DATA IT NEEDS: News search results, brand info.
- RULES WORTH COPYING: Date window in code (7 days), not in the prompt.
- BECKY MATCH: none found.

### infographic-publish  (source: creator-os-starter/.claude/skills/infographic-publish) [desc]
- PURPOSE: Publish the infographics to the socials.
- FUNCTIONALITY: Uploads and schedules infographic posts across the supported platforms.
- USE CASE FOR JORDAN: Publish step only.
- DECISION MODEL: none.
- DETERMINISTIC PARTS: All of it.
- DATA IT NEEDS: Rendered images, accounts.
- RULES WORTH COPYING: none specific.
- BECKY MATCH: none found.

### instagram-comment-cta  (source: creator-os-starter/.claude/skills/instagram-comment-cta)
- PURPOSE: Reply to "OS" comments on Instagram posts with the app link.
- FUNCTIONALITY: Live path: Zernio webhook to /api/zernio/webhook replies inline. This skill only drains the retry queue (comment_events rows still marked pending).
- USE CASE FOR JORDAN: Comment-to-link automation. Matches the funnel idea in Social Agents.
- DECISION MODEL: none (keyword match).
- DETERMINISTIC PARTS: Keyword match, queue retry.
- DATA IT NEEDS: Comment events table, Zernio webhook.
- RULES WORTH COPYING: Webhook does the live work; the skill is only a retry drain (no double replies).
- BECKY MATCH: none found.

### instagram-dm-sales-agent  (source: creator-os-starter/.claude/skills/instagram-dm-sales-agent)
- PURPOSE: Send personalised cold DMs to Instagram leads at scale.
- FUNCTIONALITY: Playwright opens Chromium with a saved session. For each lead: opens the DM inbox, composes a new message, searches the username, sends. Template default or "ai" (Claude writes each message). Multi-account and headless modes. Tracks success or failure in the database.
- USE CASE FOR JORDAN: Cold DM outreach. Not a content task. Also a spam risk.
- DECISION MODEL: LLM free-text for personalised messages (could be System One: "Is this lead a fit for the offer? (yes/no)").
- DETERMINISTIC PARTS: Compose flow, session, retries, tracking.
- DATA IT NEEDS: Instagram leads table, account credentials.
- RULES WORTH COPYING: none specific.
- BECKY MATCH: none found. FLAG: drives Chromium with Playwright. Conflicts with Jordan's browser rule.

### instagram-engage  (source: creator-os-starter/.claude/skills/instagram-engage) [desc]
- PURPOSE: Like and comment on Instagram posts from a profile.
- FUNCTIONALITY: Playwright over CDP. Visits a profile, finds recent posts, likes them, comments on posts not yet engaged. Dedupes through an instagram_engagements table so each post gets one engagement.
- USE CASE FOR JORDAN: Engagement automation. Low value and risky.
- DECISION MODEL: LLM free-text for comments (could be System One: "Is this post relevant to our niche? (yes/no)").
- DETERMINISTIC PARTS: Like, dedupe.
- DATA IT NEEDS: Profile list.
- RULES WORTH COPYING: Dedupe table, one engagement per post.
- BECKY MATCH: none found. FLAG: Chrome CDP. Conflicts with Jordan's browser rule.

### instagram-lead-scraper  (source: creator-os-starter/.claude/skills/instagram-lead-scraper) [desc]
- PURPOSE: Build lead lists from people who liked specific posts.
- FUNCTIONALITY: Apify actor scrapes likers of given posts, stores them in instagram_leads.
- USE CASE FOR JORDAN: Audience-building research on a competitor's post likers. Not a content task.
- DECISION MODEL: none.
- DETERMINISTIC PARTS: All of it.
- DATA IT NEEDS: Post URLs, Apify key.
- RULES WORTH COPYING: none specific.
- BECKY MATCH: none found.

### instantly-email  (source: creator-os-starter/.claude/skills/instantly-email)
- PURPOSE: Run cold email campaigns through Instantly.ai.
- FUNCTIONALITY: Scripts for setup, upload leads from business_leads, add leads, activate, analytics, sync replies back to the DB, update templates, reset and push.
- USE CASE FOR JORDAN: Cold email for a sponsor or client list. Not a content task.
- DECISION MODEL: none.
- DETERMINISTIC PARTS: All of it.
- DATA IT NEEDS: Leads, Instantly key.
- RULES WORTH COPYING: Sync replies back to the DB so responses are not missed.
- BECKY MATCH: none found.

### klap-generate-shorts  (source: creator-os-starter/.claude/skills/klap-generate-shorts)
- PURPOSE: Turn a long YouTube video into short clips using the Klap API.
- FUNCTIONALITY: Submits the video to Klap, polls until processing ends, exports all shorts with virality scores, downloads them and stores them in the video_to_shorts_agent table.
- USE CASE FOR JORDAN: Automatic shorts from a long video, with a virality score per clip. Directly relevant to Jordan's shorts problem. Paid third-party service.
- DECISION MODEL: The virality score comes from Klap (a model). Could be System One: "Is this clip a self-contained moment? (yes/no)".
- DETERMINISTIC PARTS: Submit, poll, download, store.
- DATA IT NEEDS: Long video URL, Klap key.
- RULES WORTH COPYING: Keep the score next to each clip so the human can pick.
- BECKY MATCH: strong overlap. becky-short, becky-moment, becky-hits and becky-clip already pick and cut moments locally. Check whether becky already does this before using Klap.

### knowledge-graph-reindex  (source: creator-os-starter/.claude/skills/knowledge-graph-reindex)
- PURPOSE: Keep the system map current for the RAG agent.
- FUNCTIONALITY: Scans the workspace (skills, scripts, config), maps database tables, cron jobs, pipelines and connections into graph.json. Optional cron.
- USE CASE FOR JORDAN: A map of Becky's tools, scripts and jobs that stays current. Would answer "what depends on what".
- DECISION MODEL: none.
- DETERMINISTIC PARTS: All of it.
- DATA IT NEEDS: Workspace files.
- RULES WORTH COPYING: Rebuild on every change (or on a schedule) rather than maintain by hand.
- BECKY MATCH: partial. becky-docs and INDEX.md are hand-kept. No auto graph found.

### lead-enrichment  (source: creator-os-starter/.claude/skills/lead-enrichment) [desc]
- PURPOSE: Find emails and phone numbers for leads.
- FUNCTIONALITY: Apify Contact Details Scraper crawls each lead's website and writes email, phone and social handles into business_leads.
- USE CASE FOR JORDAN: Sponsor or client lead enrichment only.
- DECISION MODEL: none.
- DETERMINISTIC PARTS: All of it.
- DATA IT NEEDS: Lead websites.
- RULES WORTH COPYING: none specific.
- BECKY MATCH: none found.

### lead-scrape-orchestrator  (source: creator-os-starter/.claude/skills/lead-scrape-orchestrator) [desc]
- PURPOSE: Run the whole lead pipeline in order after onboarding.
- FUNCTIONALITY: Google Maps, then LinkedIn profiles, then Instagram likers, then Facebook page finder, then email and phone enrichment.
- USE CASE FOR JORDAN: Not a content task.
- DECISION MODEL: none.
- DETERMINISTIC PARTS: Pipeline order.
- DATA IT NEEDS: Config.
- RULES WORTH COPYING: One orchestrator for a fixed order of steps.
- BECKY MATCH: none found.

### linkedin-connect  (source: creator-os-starter/.claude/skills/linkedin-connect) [desc]
- PURPOSE: Send LinkedIn connection requests with a personal note.
- FUNCTIONALITY: Browser automation on pre-scraped leads from linkedin_leads. Message templates, CLI options, safety limits, cron.
- USE CASE FOR JORDAN: Outreach. Not a content task.
- DECISION MODEL: none.
- DETERMINISTIC PARTS: Templates, limits.
- DATA IT NEEDS: Leads.
- RULES WORTH COPYING: Safety limits built in.
- BECKY MATCH: none found. FLAG: browser automation. Conflicts with Jordan's browser rule.

### linkedin-connection-agent  (source: creator-os-starter/.claude/skills/linkedin-connection-agent) [desc]
- PURPOSE: Track connection requests sent through PhantomBuster.
- FUNCTIONALITY: Monitors acceptance rate, pending invites and new connections; stores analytics; runs daily on Modal.
- USE CASE FOR JORDAN: Analytics for outreach. Not a content task.
- DECISION MODEL: none.
- DETERMINISTIC PARTS: All of it.
- DATA IT NEEDS: PhantomBuster results.
- RULES WORTH COPYING: Daily cron, metric list.
- BECKY MATCH: none found.

### linkedin-email-enrichment  (source: creator-os-starter/.claude/skills/linkedin-email-enrichment) [header]
- PURPOSE: Find emails for LinkedIn leads.
- FUNCTIONALITY: Looks at the most common job titles in the leads, then runs an Apify Leads Finder actor for matching professionals. Runs after profile scraping in onboarding.
- USE CASE FOR JORDAN: Lead enrichment only.
- DECISION MODEL: none.
- DETERMINISTIC PARTS: All of it.
- DATA IT NEEDS: Leads, Apify key.
- RULES WORTH COPYING: Derive search titles from the existing leads.
- BECKY MATCH: none found.

### linkedin-engage  (source: creator-os-starter/.claude/skills/linkedin-engage) [desc]
- PURPOSE: Like and comment on employee LinkedIn posts.
- FUNCTIONALITY: Checks a profile for new posts, likes them, leaves comments in a chosen style, dedupes through the database. Cron option.
- USE CASE FOR JORDAN: Engagement automation. Low value.
- DECISION MODEL: LLM free-text for comments.
- DETERMINISTIC PARTS: Like, dedupe.
- DATA IT NEEDS: Profile list.
- RULES WORTH COPYING: One engagement per post.
- BECKY MATCH: none found. FLAG: browser automation.

### linkedin-lead-enrichment  (source: creator-os-starter/.claude/skills/linkedin-lead-enrichment) [desc]
- PURPOSE: Find company websites for LinkedIn leads, then enrich with contacts.
- FUNCTIONALITY: Apify Google search finds each lead's company site (skips LinkedIn, Facebook, Crunchbase), then scrapes emails and phones.
- USE CASE FOR JORDAN: Lead enrichment only.
- DECISION MODEL: none.
- DETERMINISTIC PARTS: All of it.
- DATA IT NEEDS: Leads.
- RULES WORTH COPYING: Skip-list of known social domains.
- BECKY MATCH: none found.

### linkedin-message-agent  (source: creator-os-starter/.claude/skills/linkedin-message-agent)
- PURPOSE: Check the LinkedIn inbox and reply to unread messages.
- FUNCTIONALITY: CDP browser. Finds unread conversations, reads recent messages for context, decides if a reply is needed (skips automated and spam), writes a reply as "Kev's Assistant", sends it, logs to the database. Has --dry-run and --limit.
- USE CASE FOR JORDAN: Auto-replies to inbox messages. Risky without human review.
- DECISION MODEL: LLM free-text for the reply. The "needs reply?" step is a yes/no a System One model could answer: "Does this message need a human-style reply? (yes/no)".
- DETERMINISTIC PARTS: Unread detection, logging, dry-run.
- DATA IT NEEDS: LinkedIn inbox, persona notes.
- RULES WORTH COPYING: --dry-run first. Log every conversation.
- BECKY MATCH: none found. FLAG: Chrome CDP. Conflicts with Jordan's browser rule.

### linkedin-notifications  (source: creator-os-starter/.claude/skills/linkedin-notifications) [desc]
- PURPOSE: Report new LinkedIn notifications.
- FUNCTIONALITY: Reads notifications (acceptances, reactions, comments, mentions, profile views), dedupes against the database, and sends new ones via iMessage.
- USE CASE FOR JORDAN: Notification digest. The iMessage delivery is Mac-only.
- DECISION MODEL: none.
- DETERMINISTIC PARTS: Dedupe, delivery.
- DATA IT NEEDS: Notifications.
- RULES WORTH COPYING: Dedupe so the same notice is never sent twice.
- BECKY MATCH: none found.

### linkedin-post  (source: creator-os-starter/.claude/skills/linkedin-post) [desc]
- PURPOSE: Publish text and image posts to LinkedIn.
- FUNCTIONALITY: Browser automation: text-only or text plus image, formatting notes, cron option.
- USE CASE FOR JORDAN: Publish step. Better done through an API (Zernio or Late), not a browser.
- DECISION MODEL: none.
- DETERMINISTIC PARTS: All of it.
- DATA IT NEEDS: Post text, image.
- RULES WORTH COPYING: Formatting notes for LinkedIn.
- BECKY MATCH: none found. FLAG: browser automation. Conflicts with Jordan's browser rule.

### linkedin-profile-scraper  (source: creator-os-starter/.claude/skills/linkedin-profile-scraper) [desc]
- PURPOSE: Find LinkedIn prospects.
- FUNCTIONALITY: Apify HarvestAPI actor searches by keyword, title, company and location (no cookies). Stores full profile details.
- USE CASE FOR JORDAN: Prospect lists only.
- DECISION MODEL: none.
- DETERMINISTIC PARTS: All of it.
- DATA IT NEEDS: Search terms, Apify key.
- RULES WORTH COPYING: No cookie login needed.
- BECKY MATCH: none found.

### longform-animated-talking-head  (source: creator-os-starter/.claude/skills/longform-animated-talking-head) [header]
- PURPOSE: Turn a 16:9 talking-head YouTube recording into a fast animated edit with motion graphics, logos, GIFs and kinetic captions. No HeyGen, no avatar.
- FUNCTIONALITY: Layouts: SPLIT (default) = animation slot on the left (1140x950), head in a ringed card on the right (680x950); FULL = face full-bleed for the hook, one big claim, the CTA; HERO = full-width slot when the content is a grid or thumbnails. Captions are phrase groups (4 words or fewer) on an accent line. Engine: longform_engine.py and render templates. Same engine as open-edits (see that section).
- USE CASE FOR JORDAN: The closest match to Jordan's own edit style for long videos (he is a VEGAS editor). The layout rules are the reusable part.
- DECISION MODEL: none in the render. Layout choice is a rule (hook = FULL, claim = FULL, grid = HERO, else SPLIT). Could be System One: "Is this moment a dramatic claim or the CTA? (yes/no)".
- DETERMINISTIC PARTS: Almost all of it (layouts, captions, timings, punch rules).
- DATA IT NEEDS: Landscape talking-head clip, Whisper word timings, logos, GIFs, thumbnails.
- RULES WORTH COPYING: "Head never moves" (no zoom or shake on the head). "No bottom strip." "Frame 0 is a bright billboard on the topic, never darkened." Palette and fonts fixed across a series. These are Kevin's rules, quoted with dates (2026-09-21), so they are his taste, not general fact.
- BECKY MATCH: partial. becky-caption-style, becky-captions, becky-subtitle, becky-edit. No animated overlay engine found.

### longform-publish-folder  (source: creator-os-starter/.claude/skills/longform-publish-folder) [header]
- PURPOSE: Drop edited videos in a watch folder and have them published to YouTube with AI titles, descriptions, tags and thumbnails.
- FUNCTIONALITY: Watches a local folder. For each new video: AI writes title, description and tags, uses a thumbnail, uploads, records metadata. Flags, env vars, a cron schedule.
- USE CASE FOR JORDAN: The "post-longform" automation Jordan asked about. Drop a finished video, it is titled, described and uploaded. The metadata step is where Jordan's own rules would go.
- DECISION MODEL: LLM free-text (title, description, tags). Could be System One: "Is this title over 70 characters? (yes/no)" as a rule instead of prompt text.
- DETERMINISTIC PARTS: Watch-folder logic, upload, sidecar bookkeeping, title length check.
- DATA IT NEEDS: Video file, thumbnail, YouTube account credentials (API).
- RULES WORTH COPYING: Sidecar file marks each video as published (so no double uploads). Social Agents' post-longform adds: never publish a filename as the title, madeForKids stays false unless told otherwise.
- BECKY MATCH: none found for the upload. Partial: becky-intake (playlist), becky-vegas (VEGAS side).

### longform-video-clone-edit  (source: creator-os-starter/.claude/skills/longform-video-clone-edit) [header]
- PURPOSE: Clone a YouTube video with an AI presenter, end to end.
- FUNCTIONALITY: Download the source, transcribe, chunk the transcript for HeyGen, submit avatar jobs, poll and download chunks, stitch and speed-match, detect face and webcam-bubble position, classify segments as PIP, fullscreen or no-face, composite the avatar over the screen recording with lip-synced audio. 464-line playbook; only headings were read.
- USE CASE FOR JORDAN: Only if Jordan wants an AI presenter. Otherwise not useful.
- DECISION MODEL: System One candidate for the segment classifier: "Is a webcam bubble visible in this frame? (yes/no)", "Is the presenter's face on screen? (yes/no)". Currently done with face detection and OpenCV.
- DETERMINISTIC PARTS: Almost all of it (download, stitch, speed match, bubble detection, compositing).
- DATA IT NEEDS: Source video, transcript, HeyGen avatar, frames.
- RULES WORTH COPYING: Detect layout per segment (PIP vs fullscreen) instead of assuming one layout for the whole video.
- BECKY MATCH: partial. becky-identify, becky-diarize, becky-vision (face and layout). No avatar pipeline.

### megan-longform-clone  (source: creator-os-starter/.claude/skills/megan-longform-clone) [header]
- PURPOSE: Same as danny-longform-clone, for persona Megan.
- FUNCTIONALITY: Persona-swapped transcript to HeyGen, v2 composite (fullscreen plus webcam-bubble picture-in-picture), every-frame QA, Megan-swapped thumbnail.
- USE CASE FOR JORDAN: Duplicate of the Danny clone. Same sensitivity note.
- DECISION MODEL: as danny-longform-clone.
- DETERMINISTIC PARTS: as danny-longform-clone.
- DATA IT NEEDS: as danny-longform-clone.
- RULES WORTH COPYING: as danny-longform-clone.
- BECKY MATCH: none found.

### nano-banana-image-gen  (source: creator-os-starter/.claude/skills/nano-banana-image-gen) [header]
- PURPOSE: The one image generator used across the app.
- FUNCTIONALITY: fal.ai nano-banana-2 for text-to-image and image-to-image with a reference face (character consistency for up to 5 people). Options and a cost note in SKILL.md. Guarded by fal-gate (see lib section): only allowed contexts can call it.
- USE CASE FOR JORDAN: A thumbnail or still generator with a reference face. Relevant to the AI thumbnail idea. Cost is per image.
- DECISION MODEL: none.
- DETERMINISTIC PARTS: All of it, except the image itself.
- DATA IT NEEDS: Reference image, prompt, fal key.
- RULES WORTH COPYING: Image-to-image with a reference keeps the person consistent. Spend is capped in code by fal-gate.
- BECKY MATCH: strong overlap. becky-imagegen is becky's default image generator (built 2026-06-28). Use that before adding fal.

### post-analytics  (source: creator-os-starter/.claude/skills/post-analytics) [header]
- PURPOSE: Pull per-post performance into the database.
- FUNCTIONALITY: Syncs post-level metrics from Late (impressions, reach, likes, comments, shares, saves, views, engagement rate) into platform-specific stats tables.
- USE CASE FOR JORDAN: Per-video stats. What Jordan needs for "which videos worked".
- DECISION MODEL: none.
- DETERMINISTIC PARTS: All of it.
- DATA IT NEEDS: Post IDs, Late API.
- RULES WORTH COPYING: Store per-platform tables so each platform's metrics stay complete.
- BECKY MATCH: none found.

### post-publish-folder  (source: creator-os-starter/.claude/skills/post-publish-folder) [header]
- PURPOSE: Publish text and image posts from a folder.
- FUNCTIONALITY: Drop images with caption files (or plain text files). Publishes to LinkedIn, Twitter, Facebook and Instagram. Flags, env vars, carousel handling, cron.
- USE CASE FOR JORDAN: Batch posting from a folder. Same idea as the longform folder, for text and images.
- DECISION MODEL: none.
- DETERMINISTIC PARTS: All of it.
- DATA IT NEEDS: Folder of images and captions, platform keys.
- RULES WORTH COPYING: Folder-as-queue with a sidecar marker.
- BECKY MATCH: none found.

### pro-tips-deck  (source: creator-os-starter/.claude/skills/pro-tips-deck)
- PURPOSE: Weekly 14 to 15 slide tips deck for the brand (TikTok, Instagram, LinkedIn) plus a daily tip on X.
- FUNCTIONALITY: Locked visual system (black canvas, one teal wireframe per slide made by nano-banana-2, Manrope headlines). Content: 10 tips from a bank of 109, rotated by run index, plus 2 quote slides from a verified quote bank. Weekly job at 13:00 ET, self-gated by day index. Off-days write a skip row so the watchdog sees the slot as handled.
- USE CASE FOR JORDAN: A weekly tips post for YouTube creators, with a fixed look. The "locked style, do not restyle per run" rule is good practice.
- DECISION MODEL: none for the rotation. LLM free-text for the image prompt.
- DETERMINISTIC PARTS: Rotation, skip rows, layout, publishing.
- DATA IT NEEDS: Tip bank, quote bank (verified only), app icon.
- RULES WORTH COPYING: Only real, widely documented quotes. Off-days still write a row so the watchdog does not raise a false alarm.
- BECKY MATCH: none found.

### proof-post  (source: creator-os-starter/.claude/skills/proof-post)
- PURPOSE: One founder-style X post a day with a result or number, and the link only in the reply.
- FUNCTIONALITY: Day-rotated bank of 8 posts (proof-flex, transformation, punchline, anti-sell, build-in-public). Published to X via Zernio. Link in reply only; pure-value posts have no reply. Registered as a cron (MEGAN_PROOF_CRON_ENABLED).
- USE CASE FOR JORDAN: Build-in-public posts for Jordan's channel. The rule "link only in the reply, full value free in the post" is copyable.
- DECISION MODEL: none (rotation). Could be System One: "Does this post state a number we can prove? (yes/no)".
- DETERMINISTIC PARTS: Rotation, reply placement, publish.
- DATA IT NEEDS: Post bank, persona claims ledger (numbers must match the ledger).
- RULES WORTH COPYING: Never invent a new number without adding it to the ledger.
- BECKY MATCH: none found.

### reaction-ugc  (source: creator-os-starter/.claude/skills/reaction-ugc)
- PURPOSE: Short reaction meme video: a reaction clip, then real app screen recording, then a payoff line, cut to a trending sound's beat.
- FUNCTIONALITY: Three beat-aligned pieces: reaction clip with a vague hook, app b-roll with a reveal line, same b-roll with a payoff line. Beat detection from the sound. Reveal mode is the default because pure reactions got few views on TikTok (419 and 432 views for reveal-style clips vs 0 and 1 for pure reactions, 2026-07-07). Clip rotation weights clips that already contain a screen-share beat about 70%. 100 combos (10 angles x 10 hook-card pairs). Outputs TikTok video, Instagram Reel, YouTube Short.
- USE CASE FOR JORDAN: Reaction-style short with a beat-synced cut. The data point "reaction plus reveal beat small views" is good evidence for Jordan's Shorts structure.
- DECISION MODEL: none in the render. Could be System One: "Does this reaction clip contain its own reveal beat? (yes/no)" (replaces the hand-tagged clips_preferred list).
- DETERMINISTIC PARTS: Beat grid, cut points, text swaps, rotation weights, publish.
- DATA IT NEEDS: Reaction clips, app b-roll, trending sounds, post stats.
- RULES WORTH COPYING: Reveal beat beats pure reaction (measured on 2 clips only; small sample). Weighted rotation toward proven clips.
- BECKY MATCH: partial. becky-short and becky-moment (picking beats), becky-cut. No beat-sync to music found.

### reddit-demand  (source: creator-os-starter/.claude/skills/reddit-demand) [desc]
- PURPOSE: Draft and publish value-first Reddit posts for the brand.
- FUNCTIONALITY: Target subreddit map (fit versus promo tolerance), per-subreddit notes, bank of draft posts. Live top-post mining is blocked (SKILL.md "Blocked"). Publishes via Zernio.
- USE CASE FOR JORDAN: Reddit as a traffic source with a rules map per subreddit. The subreddit-rules map is reusable.
- DECISION MODEL: none. Could be System One: "Does this subreddit allow self-promotion? (yes/no)" before posting.
- DETERMINISTIC PARTS: Subreddit map, posting.
- DATA IT NEEDS: Subreddit rules, draft bank.
- RULES WORTH COPYING: Per-subreddit tolerance for promotion, checked before every post.
- BECKY MATCH: none found.

### short-form-video-clone-edit  (source: creator-os-starter/.claude/skills/short-form-video-clone-edit)
- PURPOSE: Clone a finished 9:16 reel by replacing the talking head with an AI persona and keeping the original animation and captions.
- FUNCTIONALITY: Two layouts: 55/45 split (graphics top, talking head bottom band) or full-frame 9:16 talking head. Pipeline: source, Whisper word timings on both tracks, HeyGen (one take), composite, global fit to the original duration, deliver. Locked 2026-09-14 after a run.
- USE CASE FOR JORDAN: Re-voicing a Short with a different presenter. Sensitive: Jordan should decide whether he wants AI versions of his own work.
- DECISION MODEL: none in the pipeline.
- DETERMINISTIC PARTS: Timing fit, layouts, composite, Whisper timings.
- DATA IT NEEDS: Source reel, HeyGen avatar, Whisper output.
- RULES WORTH COPYING: Fit to the original duration globally, not clip by clip.
- BECKY MATCH: partial. becky-short, becky-reel. No HeyGen step.

### short-publish-folder  (source: creator-os-starter/.claude/skills/short-publish-folder) [header]
- PURPOSE: Publish Shorts from a folder.
- FUNCTIONALITY: Watches a folder (default ~/Short form videos/) for .mp4, .mov or .webm. Uses Gemini to write captions from filenames. Writes a .published.json sidecar. Optional published/ subfolder.
- USE CASE FOR JORDAN: Drop-folder publishing for Shorts. The folder-plus-sidecar pattern is the reusable bit.
- DECISION MODEL: LLM free-text (captions from filename and context).
- DETERMINISTIC PARTS: Folder scan, sidecar, published check.
- DATA IT NEEDS: Video files, account IDs.
- RULES WORTH COPYING: Filename as caption context (for example "day-in-the-life-austin.mp4").
- BECKY MATCH: none found.

### short-publish  (source: creator-os-starter/.claude/skills/short-publish) [header]
- PURPOSE: Publish persona Shorts to five platforms in one call.
- FUNCTIONALITY: Reads the sidecar JSON from short-video, writes platform-specific captions with Gemini (different length and style per platform), publishes to Instagram Reels, TikTok, YouTube Shorts, Facebook and X in one Late call.
- USE CASE FOR JORDAN: One call, five platforms. Per-platform caption length is the copyable rule.
- DECISION MODEL: LLM free-text for captions.
- DETERMINISTIC PARTS: Length limits, publish call.
- DATA IT NEEDS: Video, sidecar, accounts.
- RULES WORTH COPYING: Caption length per platform, written separately.
- BECKY MATCH: none found.

### short-video  (source: creator-os-starter/.claude/skills/short-video) [header]
- PURPOSE: Make a 30 to 60 second persona video from a topic.
- FUNCTIONALITY: Stage 1: topic from INFORMATION.md, rotates content pillars, picks a HeyGen avatar look, Gemini writes the script, HeyGen renders a 9:16 video. Stage 2: download, Whisper word timestamps, Hormozi-style word captions (ASS), burned in with ffmpeg.
- USE CASE FOR JORDAN: Generated talking-head shorts. Not Jordan's style (he films himself).
- DECISION MODEL: LLM free-text for script. Could be System One: "Is this topic in the approved pillar list? (yes/no)".
- DETERMINISTIC PARTS: Pillar rotation, caption burn, timings.
- DATA IT NEEDS: INFORMATION.md, HeyGen avatars.
- RULES WORTH COPYING: Word-by-word captions, burned in.
- BECKY MATCH: partial. becky-captions and becky-subtitle (captions). No avatar generation.

### shorts-creatorclaw  (source: creator-os-starter/.claude/skills/shorts-creatorclaw) [desc]
- PURPOSE: Watch a folder for shorts and blast them to Instagram, TikTok and YouTube.
- FUNCTIONALITY: Uploads each file to the publishing API, Gemini writes captions, marks the file with a .published marker and dedupes in the DB.
- USE CASE FOR JORDAN: Same as short-publish-folder for another brand.
- DECISION MODEL: LLM free-text (captions).
- DETERMINISTIC PARTS: Upload, marker, dedupe.
- DATA IT NEEDS: Folder, accounts.
- RULES WORTH COPYING: Marker file plus DB dedupe (two independent checks).
- BECKY MATCH: none found.

### social-inbox-agent  (source: creator-os-starter/.claude/skills/social-inbox-agent)
- PURPOSE: Answer inbound DMs on Instagram, X, LinkedIn and YouTube as the persona.
- FUNCTIONALITY: Fetches conversations from Late, syncs messages into a social_inbox table, runs anti-spam safety checks (no self-reply loops), finds the latest unreplied message, Gemini writes the reply with context, sends it, marks it replied. Flags: --dry-run, --limit, --platform.
- USE CASE FOR JORDAN: Auto-replies to DMs. Needs strong safety rules; Jordan would want a human approve step.
- DECISION MODEL: LLM free-text for the reply. The safety checks are deterministic. Candidate System One question: "Is this a sales pitch, spam, or a real question? (pick one)" before replying.
- DETERMINISTIC PARTS: Sync, self-reply check, replied flag, dry-run, limit.
- DATA IT NEEDS: DMs, persona notes, Late and Gemini keys.
- RULES WORTH COPYING: Database as the single source of truth for who was replied to (no file state). Replied flag set atomically with the send.
- BECKY MATCH: none found.

### split-animated-short  (source: creator-os-starter/.claude/skills/split-animated-short, also open-edits/skills/split-animated-short)
- PURPOSE: Turn a script or a source reel (IG, YouTube, TikTok) into a 55/45 vertical short: animations on top, an AI persona talking in the bottom band.
- FUNCTIONALITY: Zero source pixels used. Narration and kinetic captions are synced to the persona's voice. Persona constants per vertical look. Upgraded 2026-09-05 (shared with the talking-head version).
- USE CASE FOR JORDAN: Short explainer with an AI presenter. Only if Jordan wants a persona.
- DECISION MODEL: none in the render. LLM free-text for the script.
- DETERMINISTIC PARTS: Layout, captions, timing.
- DATA IT NEEDS: Script or source URL, persona voice.
- RULES WORTH COPYING: 55/45 split layout (measured elsewhere as the same as open-edits).
- BECKY MATCH: partial. becky-short (shorts assembly).

### split-animated-talking-head  (source: creator-os-starter/.claude/skills/split-animated-talking-head, also open-edits/skills/split-animated-talking-head)
- PURPOSE: Same 50/50-ish split short, but with the creator's own filmed talking head instead of an AI persona.
- FUNCTIONALITY: Inputs: webcam or phone clip plus optional script or source URL. Animations top about 50%, talking head in a taller rounded band at the bottom, kinetic captions synced to the clip's own voice (Whisper), real product logos and screenshots whenever a named tool is said, real portraits of notable people in the niche. 854-line playbook; only headings were read.
- USE CASE FOR JORDAN: This is the open-edits flagship for a faceless-animation short with his own face. Probably the highest-value item in the set for Jordan's Shorts.
- DECISION MODEL: System One candidates: "Is this word the name of a product, tool or company? (yes/no)" (decides when to fetch a logo); "Is this a notable person in the niche? (yes/no)" (decides when to fetch a portrait). Currently done by an LLM or by a name list.
- DETERMINISTIC PARTS: Face-aware crop, Whisper timings, logo fetch, encode, caption timing.
- DATA IT NEEDS: Talking-head clip, optional script, Whisper output, logo and portrait sources.
- RULES WORTH COPYING: Caption sync to the clip's own audio. Real logos, not invented ones. Claude family logo locked by a 2026-09-03 decision.
- BECKY MATCH: partial. becky-captions, becky-subtitle, becky-speaking, becky-identify. No logo or portrait insert step found.

### stories-gen  (source: creator-os-starter/.claude/skills/stories-gen) [header]
- PURPOSE: Make a 15 second vertical story from 3 to 5 frames.
- FUNCTIONALITY: Gemini generates the concept (topic, frames, 2 to 5 words of text per frame, image prompts, persona yes/no per frame). fal nano-banana-2 makes each frame at 9:16 with the reference face. ffmpeg draws text and crossfades into a video.
- USE CASE FOR JORDAN: Quick story-format teaser. Low priority.
- DECISION MODEL: LLM free-text for the concept. Could be System One: "Does this frame show the persona? (yes/no)".
- DETERMINISTIC PARTS: Frame count, text overlay, crossfade.
- DATA IT NEEDS: Reference face, concept.
- RULES WORTH COPYING: 2 to 5 words of on-screen text per frame.
- BECKY MATCH: none found.

### stories-publish-folder  (source: creator-os-starter/.claude/skills/stories-publish-folder) [header]
- PURPOSE: Publish Instagram and Facebook Stories from a folder.
- FUNCTIONALITY: Drop images or short videos, publishes as stories. Story specs for each platform in SKILL.md. Cron option.
- USE CASE FOR JORDAN: Stories from a folder. Low priority.
- DECISION MODEL: none.
- DETERMINISTIC PARTS: All of it.
- DATA IT NEEDS: Folder, accounts.
- RULES WORTH COPYING: Platform story specs in one place.
- BECKY MATCH: none found.

### stories-publish  (source: creator-os-starter/.claude/skills/stories-publish) [header]
- PURPOSE: Publish the latest unpublished story to Instagram and Facebook.
- FUNCTIONALITY: Finds the latest story sidecar, uploads the video by presigned URL, writes a short story-style caption with Gemini.
- USE CASE FOR JORDAN: Single-story publish. Low priority.
- DECISION MODEL: LLM free-text (caption).
- DETERMINISTIC PARTS: Find, upload, publish.
- DATA IT NEEDS: Story video, sidecar.
- RULES WORTH COPYING: none specific.
- BECKY MATCH: none found.

### twitter-engage  (source: creator-os-starter/.claude/skills/twitter-engage) [desc]
- PURPOSE: Like, retweet and comment on tweets from the brand account.
- FUNCTIONALITY: Browser automation. Options and a comment style in SKILL.md.
- USE CASE FOR JORDAN: Engagement. Low value.
- DECISION MODEL: LLM free-text for comments.
- DETERMINISTIC PARTS: Like, retweet.
- DATA IT NEEDS: Tweet list.
- RULES WORTH COPYING: none.
- BECKY MATCH: none found. FLAG: browser automation. Conflicts with Jordan's browser rule.

### veo-video  (source: creator-os-starter/.claude/skills/veo-video) [desc]
- PURPOSE: Generate short AI video clips from text or a reference image.
- FUNCTIONALITY: Google Veo 2, 3, 3 Fast or 3.1 (text-to-video and image-to-video). Optional Imagen 4 reference image made on the fly. Output options and prompt tips in SKILL.md.
- USE CASE FOR JORDAN: B-roll generation. Jordan has his own footage, so the use is limited to filler shots. Costs per clip.
- DECISION MODEL: none.
- DETERMINISTIC PARTS: All of it.
- DATA IT NEEDS: Prompt, reference image, Google key.
- RULES WORTH COPYING: Use a reference frame for consistency between shots.
- BECKY MATCH: partial. becky-imagegen (images only). No video generation found.

### vertical-video-thumbnail  (source: creator-os-starter/.claude/skills/vertical-video-thumbnail)
- PURPOSE: Build the 9:16 cover and the caption for a finished talking-head short.
- FUNCTIONALITY: Input is the edited video. It transcribes, pulls the keyword from the spoken "comment the word X" line, derives the hook line and logos, writes a cover (persona-locked, two-line all-caps hook, floating product logos) and a caption (comment CTA first line, then the spoken text, hashtags last). Writes a Threads-safe 500 character cut. Rule: cover icons must match what is said.
- USE CASE FOR JORDAN: Jordan's Shorts cover and caption. Direct match for the "AI thumbnail to my own standards" idea. Rules are his to set.
- DECISION MODEL: LLM free-text (hook line). Candidate System One: "Does the cover icon match the spoken product? (yes/no)" (Kevin noted this as crucial on 2026-09-18).
- DETERMINISTIC PARTS: Transcript, keyword extraction, cover layout, Threads length cut, no-dash rule.
- DATA IT NEEDS: Edited video, transcript, logos.
- RULES WORTH COPYING: Caption format fixed (CTA line first). Threads 500-character cut. No em or en dashes. Cover text must match the spoken words.
- BECKY MATCH: partial. becky-reel (cover), becky-captions. The thumbnail maker for Jordan's own standards is NOT built yet; this is the template to copy.

### vip-sound  (source: creator-os-starter/.claude/skills/vip-sound) [desc]
- PURPOSE: Put a sound from a TikTok or Reel link at the top of the sound queue.
- FUNCTIONALITY: yt-dlp downloads the audio as mp3, uploads it to Insforge storage, inserts into the sounds table as favourite with a vip_until window.
- USE CASE FOR JORDAN: Sound library management. Low value.
- DECISION MODEL: none.
- DETERMINISTIC PARTS: All of it.
- DATA IT NEEDS: Link, storage.
- RULES WORTH COPYING: Time-boxed priority (vip_until).
- BECKY MATCH: none found.

### wan-video-clone  (source: creator-os-starter/.claude/skills/wan-video-clone) [header]
- PURPOSE: Clone a video with an AI face swap, cloned voice, and WAN 2.2 animate.
- FUNCTIONALITY: Splits long videos into 5 second chunks, runs each through face swap, voice clone and WAN 2.2 animate, stitches back. Flags and timing in SKILL.md.
- USE CASE FOR JORDAN: Face and voice replacement. Sensitive (deepfake of a real person): Jordan should decide whether he wants this at all.
- DECISION MODEL: none.
- DETERMINISTIC PARTS: Chunking, stitching.
- DATA IT NEEDS: Source video, face and voice samples.
- RULES WORTH COPYING: Chunk long jobs to keep each step small and restartable.
- BECKY MATCH: none found.

### whatsapp-outreach  (source: creator-os-starter/.claude/skills/whatsapp-outreach) [desc]
- PURPOSE: Send WhatsApp messages to local businesses.
- FUNCTIONALITY: Playwright on WhatsApp Web, first-run QR login, phone normalisation script, delivery tracking.
- USE CASE FOR JORDAN: Outreach. Not a content task.
- DECISION MODEL: none.
- DETERMINISTIC PARTS: All of it.
- DATA IT NEEDS: Phone numbers.
- RULES WORTH COPYING: Normalise phone numbers before sending.
- BECKY MATCH: none found. FLAG: Playwright browser automation. Conflicts with Jordan's browser rule.

### youtube-engage  (source: creator-os-starter/.claude/skills/youtube-engage) [desc]
- PURPOSE: Like and comment on videos from a channel.
- FUNCTIONALITY: Browser automation. Options and comment style in SKILL.md.
- USE CASE FOR JORDAN: Engagement. Low value; YouTube rules on automated comments apply.
- DECISION MODEL: LLM free-text for comments.
- DETERMINISTIC PARTS: Like.
- DATA IT NEEDS: Channel list.
- RULES WORTH COPYING: none.
- BECKY MATCH: none found. FLAG: browser automation. Conflicts with Jordan's browser rule.

### youtube-to-heygen-longform  (source: creator-os-starter/.claude/skills/youtube-to-heygen-longform) [desc]
- PURPOSE: Turn a YouTube video into a 10 to 14 minute landscape avatar video.
- FUNCTIONALITY: Gemini analyses the video, writes a script condensed to 2000 words max, HeyGen renders a 1920x1080 avatar video.
- USE CASE FOR JORDAN: AI presenter re-make. Sensitive, same as the clone skills.
- DECISION MODEL: LLM free-text (script). 
- DETERMINISTIC PARTS: Word cap, render settings.
- DATA IT NEEDS: YouTube URL, HeyGen avatar.
- RULES WORTH COPYING: Hard word cap (2000) on the condensed script.
- BECKY MATCH: none found.

### youtube-to-heygen-video  (source: creator-os-starter/.claude/skills/youtube-to-heygen-video) [desc]
- PURPOSE: Turn a YouTube video into a 60 second vertical avatar video.
- FUNCTIONALITY: Gemini analyses the video, writes an Alex Hormozi-style script, HeyGen renders a 1080x1920 avatar video with a caption.
- USE CASE FOR JORDAN: AI presenter short. Sensitive.
- DECISION MODEL: LLM free-text.
- DETERMINISTIC PARTS: Length and format.
- DATA IT NEEDS: YouTube URL, HeyGen avatar.
- RULES WORTH COPYING: Script length limits in the format section.
- BECKY MATCH: none found.

### youtube-to-viral-posts  (source: creator-os-starter/.claude/skills/youtube-to-viral-posts) [desc]
- PURPOSE: Turn a YouTube video into viral Twitter or LinkedIn posts.
- FUNCTIONALITY: Gemini video analysis, then a post with hook (headline plus stat), body, steps, CTA. Output constraints in SKILL.md.
- USE CASE FOR JORDAN: Repurpose a long video into text posts. Overlaps gemini-viral-shorts; keep one.
- DECISION MODEL: LLM free-text.
- DETERMINISTIC PARTS: Structure and length.
- DATA IT NEEDS: YouTube URL, Gemini key.
- RULES WORTH COPYING: Fixed post structure (hook, body, steps, CTA).
- BECKY MATCH: partial. becky-transcribe plus becky-judge.

### youtube-upload  (source: creator-os-starter/.claude/skills/youtube-upload) [desc]
- PURPOSE: Upload a video to YouTube through Studio, with title, description and visibility.
- FUNCTIONALITY: Browser automation of YouTube Studio. Public, unlisted or private. Pipeline example with Veo.
- USE CASE FOR JORDAN: Upload step. A YouTube Data API upload is safer than browser automation of Studio.
- DECISION MODEL: none.
- DETERMINISTIC PARTS: All of it.
- DATA IT NEEDS: Video, title, description.
- RULES WORTH COPYING: Visibility chosen explicitly each time.
- BECKY MATCH: none found. FLAG: browser automation on a live Studio account. Conflicts with Jordan's browser rule.

## PART C - creator-os-starter features (dashboard pages, libraries, hooks, scripts)

### Agent edits with watchdog (auto-resume stalled runs)  (source: creator-os-starter/src/lib/agent-edits/watchdog.ts, runner.ts, store.ts; src/app/dashboard/agent-edits/*)
- PURPOSE: Run a video edit as a background agent job, and automatically restart it when it stalls.
- FUNCTIONALITY: runner.ts starts a background job by spawning the Cursor app's agent binary (its path is hard-coded to a macOS location, so this will not run on Jordan's Windows PC as written). watchdog.ts reads the job's disk status and error text. It resumes the job when status is "running", or the error is empty, or the error matches network and provider failures (ECONNRESET, ETIMEDOUT, socket hang up, fetch failed, Provider Error, trouble connecting to the model provider, Interrupted). It does NOT resume on fatal errors (not logged in, cursor-agent not found, cancelled by user, no source clip). Maximum 8 resumes, within a 36 hour window.
- USE CASE FOR JORDAN: This is the "watchdog" Jordan asked about. Concrete design: a fixed list of error patterns, a retry cap, and a time window. It is deterministic. Jordan's watchdogs could use the same three rules (cap, window, and a fatal list that stops retries).
- DECISION MODEL: none. (Could be System One: "Is this failure transient (retry) or fatal (stop)? (pick one)" for unknown errors. Today the regex decides.)
- DETERMINISTIC PARTS: Everything in the watchdog.
- DATA IT NEEDS: Job status files, error text.
- RULES WORTH COPYING: Fatal errors never retry. Retry cap of 8 and a 36 hour window stop endless loops. Every retry is based on the error text, not on a guess.
- BECKY MATCH: partial. becky-harness, becky-foreman and becky-unstick exist. Compare their retry caps before building another watchdog.

### Agent video cloning runner  (source: creator-os-starter/src/lib/agent-clones/*, src/app/dashboard/agent-clones/*)
- PURPOSE: Clone a video (via a clip and a prompt) as a background job.
- FUNCTIONALITY: Builds a prompt from the clip, notes and source URL, spawns a runner script with a workdir, tracks status, pulls the result back. HeyGen MCP wrapper in heygen-mcp.ts. Personas in personas.ts.
- USE CASE FOR JORDAN: Same as the clone skills. Sensitive.
- DECISION MODEL: LLM free-text (clone prompt).
- DETERMINISTIC PARTS: Job, status, workdir.
- DATA IT NEEDS: Source clip, persona.
- RULES WORTH COPYING: One workdir per job, so a crash does not leave shared state.
- BECKY MATCH: none found.

### Agent edits page and Jordan-style edit jobs  (source: creator-os-starter/src/app/dashboard/agent-edits/AgentEditsDesk.tsx, src/app/api/agent-edits/*)
- PURPOSE: Upload a clip, pick a style, and get an edited short from an agent in the background.
- FUNCTIONALITY: Dashboard desk to start, watch, cancel and download edits. API routes for list, clip, file.
- USE CASE FOR JORDAN: A job queue UI for edits. Becky already has the VEGAS and Becky Review UIs, so this is for comparison only.
- DECISION MODEL: LLM free-text inside the edit job.
- DETERMINISTIC PARTS: Job queue, status, file serving.
- DATA IT NEEDS: Clip, style notes.
- RULES WORTH COPYING: Cancel is a first-class action (fatal error list includes user cancel).
- BECKY MATCH: partial. becky-review and becky-canvas are the Becky UI for this.

### Comments and comment-to-DM automation (library + Zernio webhook)  (source: creator-os-starter/src/lib/comments/*, src/app/api/zernio/webhook/route.ts, src/app/api/comments/*, src/app/dashboard/posts/*)
- PURPOSE: Reply to keyword comments with a link automatically, and let a person delete or reply from the dashboard.
- FUNCTIONALITY: Webhook receives comment.received, verifies the HMAC-SHA256 signature, logs to comment_events, and replies inline if the comment is the CTA keyword. If the reply cannot be sent, the row stays pending for the retry skill. Other modules: automated.ts (auto replies), automations.ts, cta.ts (keyword match), dm-copy.ts (DM text), own.ts (skip own replies), reply.ts, delete.ts.
- USE CASE FOR JORDAN: Comment "GUIDE" to get a link. Matches Social Agents' funnel. Jordan's channel would need a comment bot rule set.
- DECISION MODEL: none (keyword match). Could be System One: "Is this comment asking for the offer? (yes/no)".
- DETERMINISTIC PARTS: Keyword match, HMAC check, retry queue, own-comment skip.
- DATA IT NEEDS: Comment webhook from Zernio, CTA keyword, DM copy.
- RULES WORTH COPYING: Verify webhook signature. Skip own comments (no self-reply). Pending rows are retried, not dropped.
- BECKY MATCH: none found.

### Copywriter pipeline (Instagram and YouTube source to rewritten script)  (source: creator-os-starter/src/lib/copywriter/*, src/app/api/copywriter/*, src/app/dashboard/copywriter/*, scripts/copywriter-*.mjs)
- PURPOSE: Take a source video, transcribe it, analyse it, and write a new script in Kevin's voice.
- FUNCTIONALITY: Transcribe route starts one Apify run for each Instagram URL and records one row per URL. Runs are polled. analyze.ts reads the topic of the transcript through the LLM gateway. rewrite.ts writes the new script. skill.ts loads the style skill (copywriter-longform-kevin / shortform-kevin). Ingest scripts pull the 20 latest YouTube videos or 20 latest reels, and build the style numbers.
- USE CASE FOR JORDAN: Jordan's version of a competitor's video, in his style, from a source link. Also the "measure my own videos, then write to them" method.
- DECISION MODEL: LLM free-text (topic read, rewrite). Could be System One: "Is the transcript about a tool or a how-to? (pick one)".
- DETERMINISTIC PARTS: Ingest, style numbers, run polling, storage.
- DATA IT NEEDS: Source transcripts, Jordan's own transcripts and stats.
- RULES WORTH COPYING: Build a style file from real numbers first (see copywriter-longform-kevin).
- BECKY MATCH: partial. becky-transcribe, becky-research, becky-intake.

### Script writer (dashboard)  (source: creator-os-starter/src/lib/script-writer/write.ts, src/app/api/script-writer/route.ts, src/app/dashboard/script-writer/*)
- PURPOSE: Write a script from a brief in the dashboard.
- FUNCTIONALITY: write.ts (212 lines) calls the LLM gateway with the brief and returns the script. Only the signature was read; the prompt was not.
- USE CASE FOR JORDAN: A script from a brief. Overlaps copywriter-longform-kevin.
- DECISION MODEL: LLM free-text.
- DETERMINISTIC PARTS: Route and storage.
- DATA IT NEEDS: Brief.
- RULES WORTH COPYING: none checked.
- BECKY MATCH: none found.

### Content feed (carousels, posts, calendar, channels, accounts)  (source: creator-os-starter/src/app/dashboard/carousels/*, posts/*, calendar/*, channels/*, clients/*)
- PURPOSE: One place to review everything that will be posted, per channel and per client account.
- FUNCTIONALITY: Carousels page shows each rendered carousel with a slide viewer and delete. Posts page lists posts with a composer (new post). Calendar page has a monthly view and heatmap. Channels page manages connected accounts. Accounts page (clients) lists and edits accounts and per-client pages.
- USE CASE FOR JORDAN: A review queue for every output before it goes live. Matches Kevin's standing rule: drafts stay in the feed for review.
- DECISION MODEL: none.
- DETERMINISTIC PARTS: All of it.
- DATA IT NEEDS: Rendered outputs, accounts, posts.
- RULES WORTH COPYING: "Drafts stay in the feed for review" (from the daily-carousel publish gate).
- BECKY MATCH: partial. becky-review (review UI), becky-review-index.

### Sounds library  (source: creator-os-starter/src/app/dashboard/sounds/*, src/app/api/sounds/*, scripts/refresh-sounds.mjs, scripts/add-vip-sound.mjs)
- PURPOSE: Keep a library of trending sounds for TikTok and Instagram.
- FUNCTIONALITY: Browser page to play, favourite and pick sounds. refresh-sounds.mjs refreshes the list. add-vip-sound.mjs adds a time-boxed priority sound.
- USE CASE FOR JORDAN: Music bed library for shorts (copyright-safe only). Jordan's bed library is in Becky (becky-drum, becky-samples).
- DECISION MODEL: none.
- DETERMINISTIC PARTS: All of it.
- DATA IT NEEDS: Sound list from platforms.
- RULES WORTH COPYING: Favourites and a rotation, so the same sound is not used on every post.
- BECKY MATCH: partial. becky-drum (drum machine), samples folder.

### GIF search and import (Giphy)  (source: creator-os-starter/src/app/dashboard/giphy/*, src/app/api/giphy/*)
- PURPOSE: Find and import reaction GIFs for edits.
- FUNCTIONALITY: Search Giphy, preview in the browser grid, import a GIF into the asset store.
- USE CASE FOR JORDAN: Reaction GIFs for edits. Open-edits uses Giphy frame banks too.
- DECISION MODEL: none.
- DETERMINISTIC PARTS: All of it.
- DATA IT NEEDS: Giphy key, search terms.
- RULES WORTH COPYING: Import once into storage so the edit does not depend on a live link.
- BECKY MATCH: none found.

### AI news feed and brief  (source: creator-os-starter/src/app/dashboard/news/*, src/components/AiNews*.tsx, src/lib/ai-news/*, scripts/ai-news-ingest.mjs, scripts/ai-news-blocklist.json)
- PURPOSE: Daily list of AI news, tools and ideas for content.
- FUNCTIONALITY: ai-news-ingest.mjs pulls items, a blocklist removes sources or keywords Kevin does not want, the page shows a feed and a daily brief written by the LLM gateway.
- USE CASE FOR JORDAN: A daily AI-news input for Jordan's video ideas. The blocklist is the taste filter.
- DECISION MODEL: LLM free-text for the brief. Could be System One: "Is this item about a tool a creator can use this week? (yes/no)" (replaces the blocklist guesses).
- DETERMINISTIC PARTS: Ingest, blocklist, dedupe.
- DATA IT NEEDS: News sources, blocklist.
- RULES WORTH COPYING: Keep a blocklist file that is easy to edit (taste is a file, not a prompt).
- BECKY MATCH: partial. becky-radar and becky-scout (watch and scout content). Compare first.

### Goals, accomplishments and business progress  (source: creator-os-starter/src/app/dashboard/progress/*, src/lib/goals/*, src/lib/accomplishments/*)
- PURPOSE: Track goals and a calendar of what was done.
- FUNCTIONALITY: Goal manager (set goals, compute metrics against them), accomplishments calendar (log wins by day), time helpers.
- USE CASE FOR JORDAN: Goal tracking for channel milestones. Low priority.
- DECISION MODEL: none.
- DETERMINISTIC PARTS: All of it.
- DATA IT NEEDS: Goals, logged wins.
- RULES WORTH COPYING: Metrics tied to goals, computed from the same snapshots.
- BECKY MATCH: none found.

### Philosophy content and ingest  (source: creator-os-starter/src/app/dashboard/philosophy/*, scripts/philosophy-ingest.mjs)
- PURPOSE: Ingest a body of philosophy material for content.
- FUNCTIONALITY: Ingest script loads source material; the dashboard page shows it. Only the script name was read.
- USE CASE FOR JORDAN: Unknown; probably a source-library pattern. Not verified.
- DECISION MODEL: none checked.
- DETERMINISTIC PARTS: Ingest.
- DATA IT NEEDS: Source texts.
- RULES WORTH COPYING: none checked.
- BECKY MATCH: none found.

### Screensaver page  (source: creator-os-starter/src/app/dashboard/screensaver/page.tsx) [header]
- PURPOSE: Full-screen display of the dashboard numbers.
- FUNCTIONALITY: A standalone page for a wall screen. Not checked in detail.
- USE CASE FOR JORDAN: Not needed.
- DECISION MODEL: none.
- DETERMINISTIC PARTS: All.
- DATA IT NEEDS: Dashboard numbers.
- RULES WORTH COPYING: none.
- BECKY MATCH: none found.

### Overview and settings dashboard  (source: creator-os-starter/src/app/dashboard/page.tsx, settings/page.tsx, layout.tsx, components/KpiStrip.tsx, Sidebar.tsx)
- PURPOSE: Home page with the headline numbers and the settings screen.
- FUNCTIONALITY: KPI strip (followers, posts, revenue, comments), sidebar navigation, settings for keys and personas. Chart components: calendar heatmap, follower growth heatmap, audience breakdown, new customers chart.
- USE CASE FOR JORDAN: Home screen for channel numbers. Becky has no equivalent for channel numbers.
- DECISION MODEL: none.
- DETERMINISTIC PARTS: All.
- DATA IT NEEDS: Snapshots (see snapshot entry).
- RULES WORTH COPYING: One headline strip at the top of the dashboard.
- BECKY MATCH: none found.

### Persona brand packs and shared libs  (source: creator-os-starter/.claude/lib/persona.js, src/lib/persona, .claude/lib/seo-caption.js, reel-thumbnail.js, zernio-media.js)
- PURPOSE: One source of truth for each persona's voice, claims and brand, used by every content skill.
- FUNCTIONALITY: persona.js loads the personas table (name, voice, niche, reference images, accounts). seo-caption.js writes Instagram and TikTok captions at publish time from the seed caption (fallback to the old caption if the API fails). reel-thumbnail.js builds a 1080x1920 Reels cover (app icon left, persona centre, Claude logo right, two bold text lines). zernio-media.js uploads large publish masters through Zernio's own store (presigned, multi-GB) instead of the 50 MB Insforge limit.
- USE CASE FOR JORDAN: A brand pack per channel (voice, claims, banned phrases, logos). The "one file every caption flows from" idea is the most useful part.
- DECISION MODEL: LLM free-text for captions. Could be System One: "Is this caption within the brand's word and emoji rules? (yes/no)".
- DETERMINISTIC PARTS: Pack loading, cover layout, upload routing, fallback logic.
- DATA IT NEEDS: Persona table, logos, reference images.
- RULES WORTH COPYING: Captions are written at publish time, with the old caption as seed and fallback, so an API error never blocks a post. Large masters go to the media host, not the small store.
- BECKY MATCH: partial. becky-reel (cover), becky-canvas. No brand pack file found in becky.

### LLM gateway (one model for every automation)  (source: creator-os-starter/.claude/lib/llm.js)
- PURPOSE: One door for all automations to call a model, so the provider can change in one place.
- FUNCTIONALITY: Keeps the Anthropic Messages request shape on both ends. Text-only goes to DeepSeek v4 Flash (Ollama cloud). Any request with images goes to Qwen 3.5 397B (vision). A JSON-only instruction is forced and the first JSON object is cut out of the reply, because the cloud model ignores the JSON schema. Verified 2026-08-11.
- USE CASE FOR JORDAN: A single place to route cheap text calls and vision calls. Directly relevant to Becky's model routing (Gemma, Qwen, Laya).
- DECISION MODEL: none (routing by content type: images or not).
- DETERMINISTIC PARTS: Routing by image presence, JSON extraction, the one-credential rule.
- DATA IT NEEDS: Ollama API key, prompts.
- RULES WORTH COPYING: Do not trust the provider's JSON schema. Brace-match the JSON in code (the source found the schema ignored).
- BECKY MATCH: partial. becky-decide, becky-vision, becky-judge and the local model router. No single gateway found in becky; check becky-go/internal before adding one.

### Fal spend gate (hard stop)  (source: creator-os-starter/.claude/lib/fal-gate.js)
- PURPOSE: Stop fal.ai image spend from every script except the approved marketing page.
- FUNCTIONALITY: Every fal call must pass assertFal(context). Only contexts listed in FAL_ALLOW (default: kevbuildsapps) may proceed. Anything else throws and the pipeline falls back to banks or stock images.
- USE CASE FOR JORDAN: A spend stop-gate for any paid API. Jordan should have one for every paid key.
- DECISION MODEL: none.
- DETERMINISTIC PARTS: All of it.
- DATA IT NEEDS: Allow-list env.
- RULES WORTH COPYING: Default deny. Every paid call goes through one check.
- BECKY MATCH: none found.

### Creator OS database schema  (source: creator-os-starter/db/schema.sql, scripts/db-setup.mjs)
- PURPOSE: The tables behind every feature above.
- FUNCTIONALITY: Creates these Postgres tables (verified from db/schema.sql): accomplishments, advice_tip_usage, agent_clones, agent_edits, agent_posts, ai_news_briefs, ai_news_items, analytics_snapshots, channel_profiles, channels, comment_dm_setups, comment_events, content_posts, copywriter_sources, daily_view_snapshots, follower_snapshots, gifs, goals, jev_decisions (log of Jev decisions, the training data for a local model), link_clicks, manager_reports, megos_assets, persona_images, personas, philosophy_content, philosophy_creators, philosophy_insights, publish_claims, revenuecat_customers, revenuecat_metric_snapshots, revenuecat_subscriptions, sounds, web_analytics_snapshots. db-setup.mjs applies it. Note: some older skills name tables (business_leads, instagram_leads, linkedin_leads, social_inbox, video_to_shorts_agent) that are NOT in this schema file; they may live in another database.
- USE CASE FOR JORDAN: A reference for the data model Becky could use for analytics.
- DECISION MODEL: none.
- DETERMINISTIC PARTS: All.
- DATA IT NEEDS: n/a.
- RULES WORTH COPYING: Keep one table per platform for stats (see post-analytics).
- BECKY MATCH: partial. becky uses JSON and files, not Postgres.

### Reaction clip generation and QA scripts  (source: creator-os-starter/scripts/gen-reaction-clips.mjs, qa-reaction-clips.mjs)
- PURPOSE: Make the reaction clips and check them before use.
- FUNCTIONALITY: Generates reaction clips with a script. qa-reaction-clips.mjs checks them (the QA rubric is not read in detail).
- USE CASE FOR JORDAN: Quality check on generated clips. Relevant to Jordan's review rule.
- DECISION MODEL: LLM vision (QA). Could be System One yes/no per clip.
- DETERMINISTIC PARTS: Generation settings.
- DATA IT NEEDS: Clips.
- RULES WORTH COPYING: Check every generated clip before use.
- BECKY MATCH: partial. becky-vision.

### Comment webhook and CTA setup scripts  (source: creator-os-starter/scripts/setup-comment-cta.mjs, setup-comment-webhook.mjs)
- PURPOSE: One-time setup of the comment-to-link feature.
- FUNCTIONALITY: Registers the webhook and the CTA keyword with the platform.
- USE CASE FOR JORDAN: Setup only.
- DECISION MODEL: none.
- DETERMINISTIC PARTS: All.
- DATA IT NEEDS: Webhook URL, keys.
- RULES WORTH COPYING: none.
- BECKY MATCH: none found.

### Dev server and deploy (Railway)  (source: creator-os-starter/scripts/dev-local.mjs, railway.json, nixpacks.toml, src/instrumentation.ts, src/proxy.ts)
- PURPOSE: Run locally and deploy to Railway.
- FUNCTIONALITY: dev-local.mjs starts the local dev server. Railway config builds with Nixpacks. Instrumentation and the proxy file set the password gate and exempt the webhook path.
- USE CASE FOR JORDAN: Reference for a single-process deploy with password gate.
- DECISION MODEL: none.
- DETERMINISTIC PARTS: All.
- DATA IT NEEDS: env.
- RULES WORTH COPYING: Exempt webhook paths from the login gate, and verify them by signature instead.
- BECKY MATCH: none found.

## PART D - open-edits (OPEN VIDEO EDIT, 6 skills, Python render engine)

Context: open-edits is the video-editing engine behind Kevin's animated shorts. Every frame is drawn in Python and rendered locally, no paid generation. Its README calls it "Claude Code skills that turn a raw talking-head clip into a fully animated short". Its examples folder holds the real render.py files of shipped edits. This is the most relevant repo for Jordan's editing work.

### split-animated-talking-head (flagship, open-edits)  (source: open-edits/skills/split-animated-talking-head/SKILL.md, scripts/prep_talking_head.py, fetch_brand_asset.py, fetch_figure.py, fetch_giphy.py, number_beats.py; templates/hyper_edits.py, light_fx.py, media_marks.py, claude_marks.py, split_55_45_compositor.py)
- PURPOSE: Turn a raw talking-head clip into a 1080x1920 vertical short with motion graphics on top and the speaker in a rounded band at the bottom.
- FUNCTIONALITY: prep_talking_head.py crops around the face (never ships a black bar), cleans audio and runs Whisper (small.en) for word timestamps, primed with product names. The script is split into beats; a beat planner decides which graphic goes on each beat. Named products trigger a logo fetch (fetch_brand_asset.py: Wikimedia, simple-icons, GitHub or site favicon). Named people trigger a face-cropped portrait fetch (fetch_figure.py). Spoken numbers get a rolling counter (number_beats.py). Giphy reaction GIF frame banks. Hyper-edit effects (punch-in, shake, whip cut, 1 to 2 frame flash, spring entrance, typewriter). Light effects (scanlines, neon grid, bloom). Encode. 854-line playbook; the headings and the pipeline were read, not every rule.
- USE CASE FOR JORDAN: The most complete "face plus animated graphics" short system in this set. Fits his Shorts. Does not need an AI avatar. It is a VEGAS-free pipeline, so it competes with his own editing, not with Becky.
- DECISION MODEL: None in the render. Beat planning is rule-based. System One candidates: "Is this word a product, tool or company name? (yes/no)" (decides logo fetch); "Is this a notable person in the niche? (yes/no)" (decides portrait fetch); "Does this beat need a graphic or can the talking head carry it? (pick one)". Currently an LLM or a name list decides.
- DETERMINISTIC PARTS: Face-aware crop, Whisper timings, counter maths, effect timing, logo and portrait fetch, encode.
- DATA IT NEEDS: Talking-head clip, script (optional), Whisper word timestamps, logo and portrait sources (public).
- RULES WORTH COPYING: Real logos and real portraits only, not invented ones. Captions synced to the clip's own audio. Claude family logo is locked (a decision Kevin made on 2026-09-03). Look at SKILL.md's learnings list before changing the look.
- BECKY MATCH: partial. becky-captions, becky-subtitle, becky-speaking, becky-identify, becky-moment. No logo or portrait insert step and no animated graphics layer found in becky.

### longform-animated-talking-head (open-edits)  (source: open-edits/skills/longform-animated-talking-head/SKILL.md, scripts/prep_longform_head.py, render_parallel.sh, setup_workdir.sh; templates/longform_engine.py)
- PURPOSE: The 16:9 long-form version: a hyper-edit of a landscape talking head for YouTube.
- FUNCTIONALITY: Same layout system as creator-os's version (SPLIT default, FULL for hook and claim, HERO for grids). Chapter and zoom-dissolve elements. Renders in parallel (render_parallel.sh). The same locked rules: head never moves, no bottom strip, phrase captions, consistent palette and type across a series.
- USE CASE FOR JORDAN: Long-form YouTube edit with graphics, for his own face. Good match for his VEGAS long videos as a second opinion on pacing.
- DECISION MODEL: None in the render. Layout choice is a rule. Could be System One: "Is this a dramatic claim, the hook, or the CTA? (yes/no)" to choose FULL layout.
- DETERMINISTIC PARTS: Layouts, captions, timing, parallel render.
- DATA IT NEEDS: Landscape talking-head clip, Whisper timings, graphics assets.
- RULES WORTH COPYING: "Frame 0 is a bright billboard on the topic, never darkened" (no mystery cold open for long-form). Parallel render split by segment.
- BECKY MATCH: partial. becky-edit, becky-cut, becky-roughcut (long-form cuts). No graphics overlay engine found.

### split-animated-short (open-edits)  (source: open-edits/skills/split-animated-short/SKILL.md, scripts/fetch_figure.py, fetch_giphy.py, number_beats.py, prep_talking_head.py)
- PURPOSE: 55/45 split short from a script or a source reel, presented by an AI avatar (HeyGen) in the bottom band, with animations on top. Zero source pixels.
- FUNCTIONALITY: Same graphic engine as the talking-head version, but the bottom band is an avatar video, not the creator's face. Persona constants per vertical look. Shares its 2026-09-05 upgrade with the talking-head version.
- USE CASE FOR JORDAN: Only if Jordan wants an avatar presenter. The graphic engine is the reusable part.
- DECISION MODEL: none in the render. LLM free-text for the script.
- DETERMINISTIC PARTS: Layout, graphics, captions.
- DATA IT NEEDS: Script or source URL, avatar video.
- RULES WORTH COPYING: Same layout rules as the talking-head version.
- BECKY MATCH: partial. becky-short.

### fireship-style-edit (faceless news explainer, 16:9)  (source: open-edits/skills/fireship-style-edit/SKILL.md, scripts/fetch_footage.py, capture_page.mjs; templates/fireship_engine.py)
- PURPOSE: Edit a faceless tech-news explainer in the "Code Report" style: narrator never on screen, every line shown by footage that literally matches it.
- FUNCTIONALITY: Measured numbers from a study of a reference video (Fireship): 107 hard cuts in 340 seconds, a new shot every 2.2 seconds median, no static element longer than 1.5 seconds, narration 213 words per minute, no burned-in captions, music bed 2 to 3 dB under the voice, loudness to -14 LUFS. Visual devices: real footage fetched by yt-dlp search (fetch_footage.py), article screenshots with an orange highlighter sweep, tweet cards with profile pictures, headline card stacks, background-removed cutouts, meme cut-ins, sticker text, built-up diagrams, glitch cuts. Script formula: cold open with a joke, title card with date, history, how it works (long diagram build), rapid product news, sponsor segment, short outro. Rule: never copy Fireship's own footage, music or logo ("Learn the grammar; never copy their assets").
- USE CASE FOR JORDAN: A faceless news or tech-explainer format for a channel where Jordan does not want to be on camera. The measured pacing numbers are a template for his own rules.
- DECISION MODEL: LLM free-text for the script. System One candidate for footage choice: "Does this clip literally show what the line says? (yes/no)" (Kevin 2026-10-02: "footage literally shows what each line says").
- DETERMINISTIC PARTS: Cut timing (about 2.2 s), element timing (1.5 s max static), loudness normalisation, footage download, the card layouts.
- DATA IT NEEDS: Voiceover, footage (fetched or supplied), article screenshots, tweet data.
- RULES WORTH COPYING: Measure a reference video first (cuts per minute, shot length, things per minute) and then hit the measured numbers. "Never invent words for a real person; never put a real stranger's face on an invented post."
- BECKY MATCH: partial. becky-cut (cut pacing), becky-edit, becky-moment. No measured-style study step found. The editlearn.py and edit_habits.py scripts may cover it. Check before building.

### fireship-style-short (vertical version)  (source: open-edits/skills/fireship-style-short/SKILL.md, scripts/setup_short.sh)
- PURPOSE: 9:16 Shorts version of the Fireship grammar: 30 to 60 seconds, faceless, news footage as bands over a blurred fill.
- FUNCTIONALITY: Hard cut about every 1.5 seconds. Safe zones for platform overlays. Word-chunk captions in the safe zone. Clip and audio policy (Kevin 2026-10-02). Wikipedia or document highlighter cards, tweet cards, built-up diagrams, meme cut-ins, stickers.
- USE CASE FOR JORDAN: Shorts version of the news explainer, faceless. Same note as above.
- DECISION MODEL: As fireship-style-edit.
- DETERMINISTIC PARTS: Cut timing, safe zones, captions, layout.
- DATA IT NEEDS: Voiceover, footage.
- RULES WORTH COPYING: Keep captions in the platform safe zone.
- BECKY MATCH: partial. becky-short.

### reverse-engineer (learn any editing style from a reference video)  (source: open-edits/skills/reverse-engineer/SKILL.md, scripts/study.py, scaffold_skill.py)
- PURPOSE: Measure the editing style of any reference video and turn it into a new editing skill.
- FUNCTIONALITY: Step 1: study.py downloads the reference with yt-dlp (2 to 5 minutes is enough for long-form), then measures: cuts, shot-length median, mean, 10th and 90th percentiles, cuts per minute, share of shots under 1 second or over 3 seconds (ffmpeg scene detection, default threshold 0.30); visual events per minute (frame-difference spikes at 10 fps); motion, faces and layout, colour, loudness and speech pace. Step 2: an agent looks at shot-by-shot contact sheets (the part a script cannot do). Step 3: scaffold_skill.py creates a new .claude/skills/<style>-edit folder with the measured numbers. Step 4: the agent fills in the style bible. Step 5: a demo render proves it.
- USE CASE FOR JORDAN: The most direct tool for Jordan's stated need: "famous-youtube-editor" style rules and a rule base taken from HIS videos. Run it on his own past videos, then on references from other creators. It answers "what are the rules of my edits" with numbers.
- DECISION MODEL: none in the measurements. LLM vision for the shot-by-shot look (step 2). System One candidate: "Is this shot a face, a graphic, or b-roll? (pick one)" per shot.
- DETERMINISTIC PARTS: All measurement (cuts, shot lengths, event rates, loudness, pace).
- DATA IT NEEDS: Reference videos (his own and others), frames.
- RULES WORTH COPYING: Measure first, write rules second. Keep the measured numbers in the skill folder so they can be re-checked. This is the method for the "rules are a loose template" idea in Jordan's brief.
- BECKY MATCH: partial. becky-tools scripts editlearn.py and edit_habits.py (edit habits: cut rhythm, zooms, effects, from Jordan's own edits) do part of the same job. Compare these first; they may already cover it.

### Fetch and measure helpers (brand logos, figures, GIFs, footage, numbers)  (source: open-edits/skills/*/scripts/fetch_brand_asset.py, fetch_figure.py, fetch_giphy.py, fetch_footage.py, number_beats.py)
- PURPOSE: Get real assets for the edit so it never uses invented ones.
- FUNCTIONALITY: fetch_brand_asset.py: real logos from Wikimedia, simple-icons, GitHub or a site favicon. fetch_figure.py: face-cropped portraits of people behind a niche (named examples: Anthropic founders, OpenAI, Meta, GaryVee, Hormozi). fetch_giphy.py: Giphy search, downloaded as frame banks. fetch_footage.py: yt-dlp search and download (auto-updates on a YouTube 403), 1 fps contact sheet, frame-accurate cut, crop for news picture-in-picture. number_beats.py: finds every spoken number so it gets a counter.
- USE CASE FOR JORDAN: A library of real logos and people for his own explainer videos, with the sourcing done for him.
- DECISION MODEL: none for the fetch. System One candidate: "Is this a company, product, person, or number? (pick one)" per spoken word.
- DETERMINISTIC PARTS: Everything.
- DATA IT NEEDS: Public web sources, names from the script.
- RULES WORTH COPYING: Sources listed by priority in SKILL.md (for example "Claude family is locked"). Use real marks only.
- BECKY MATCH: partial. becky-identify (faces), becky-ocr. No logo fetch found.

### Animation and effects engine (templates)  (source: open-edits/templates/hyper_edits.py, light_fx.py, media_marks.py, claude_marks.py, apple_counter.py, examples/*/render.py)
- PURPOSE: Reusable drawing code used by all six skills, plus the shipped render files as examples.
- FUNCTIONALITY: hyper_edits.py (punch-ins, shake, whip cuts, 1 to 2 frame flashes, spring entrances, typewriter text, and a lane guard so only one moving stream runs per band). light_fx.py (scanlines, HUD scan, neon floor grid, bloom, light hits, light rays, caption glow). apple_counter.py (iOS-style rolling odometer with commas and motion blur). media_marks.py (GIF cards, logo badges, figure rows, spotlights, logo conveyors, spinning globe). claude_marks.py (animated Claude mark). examples/: about a dozen example folders of real render.py files (social-agents, gitnexus, claude-hunger-games, claude-ads-api, jev-app-store, name-is-jef, viktor, fireship, and more; list in the README).
- USE CASE FOR JORDAN: A drawing library for graphics. Jordan would need VEGAS-side or Becky-side hooks to use it. The examples are the best way to learn the style.
- DECISION MODEL: none.
- DETERMINISTIC PARTS: All of it.
- DATA IT NEEDS: Assets.
- RULES WORTH COPYING: "One moving stream per band" lane guard prevents clutter. Keep examples as the style reference.
- BECKY MATCH: none found for an animated graphics library.

## PART E - seo-agent-kit (self-improving blog SEO, 4 skills + PostHog dashboard)

Context: a kit that writes, fact-checks and publishes blog posts to WordPress through Creator OS, then re-weights topics by real signups. Requires Node 20, Postgres, a Creator OS key, PostHog and an LLM key (Anthropic or any OpenAI-compatible API).

### seo-engine: scout (topic finding)  (source: seo-agent-kit/.claude/skills/seo-engine/scripts/scout.mjs, seeds.example.json)
- PURPOSE: Find the next blog topics worth writing.
- FUNCTIONALITY: Sources: winner spin-offs (top posts with at least 1 signup and 15 or more entry visitors, or 3 or more AI-engine visitors in 14 days, become 6 new distinct-intent ideas each: comparison, model, niche, use-case, how-to; capped per day), Search Console gaps, news, releases, and seed topics. Dedupes by token and by semantic similarity. Rejected topics keep a reason.
- USE CASE FOR JORDAN: Video-topic and blog-topic scouting from what already worked. The "spin off a winner" rule is the copyable part: a post that works produces more posts of distinct intent.
- DECISION MODEL: LLM free-text for idea wording. System One candidates: "Is this topic a distinct search intent from the existing posts? (yes/no)"; "Is this a winner by the threshold? (yes/no)" (the thresholds are code now).
- DETERMINISTIC PARTS: Winner threshold, dedupe, daily cap, seed reading.
- DATA IT NEEDS: PostHog signups and visitors per post, Search Console, seeds.
- RULES WORTH COPYING: Concrete winner thresholds in code (1 signup, 15 visitors, or 3 AI visitors, within 14 days). Per-day cap on spin-offs.
- BECKY MATCH: partial. becky-scout (assesses a playlist video by video), becky-research, becky-radar. Those score videos, not blog topics.

### seo-engine: write and fact gate (publish)  (source: seo-agent-kit/.claude/skills/seo-engine/scripts/publish.mjs, lib.mjs, llm.mjs; facts.example.json)
- PURPOSE: Write a blog post that cannot invent product features.
- FUNCTIONALITY: Writes the post from a facts.json file (real, checkable product facts only). Deterministic gate first: em and en dashes removed, under 700 words rejected, under 4 H2 headings rejected, banned strings from facts.banned rejected, recurring prices not in allowed_prices rejected, unknown CLI commands and invented flags removed, undocumented API paths removed (if api_prefixes set), unknown internal links unlinked, dead external links unlinked, signup CTA and YouTube link added if missing. Then an LLM audit lists unsupported product claims, competitor fact claims and invented stats. Up to 2 rewrites, else the topic is rejected with a reason (seo_topics.reason). Publishes to WordPress through the Creator OS blog API.
- USE CASE FOR JORDAN: Writing that is checked against a list of real facts before it goes out. The "facts file plus a deterministic gate" pattern is the strongest idea in this repo for Jordan's content rules (for example: only the numbers in his own ledger may appear).
- DECISION MODEL: LLM free-text (post) plus LLM audit (claims). System One candidate for the audit, one yes/no per claim: "Is this product claim supported by facts.json? (yes/no)". Today it is an LLM audit with a free-text answer.
- DETERMINISTIC PARTS: Almost the whole gate (word count, H2 count, banned strings, prices, CLI flags, link checks, dashes, CTA insertion).
- DATA IT NEEDS: facts.json, banned list, allowed prices, API prefixes, WordPress via Creator OS.
- RULES WORTH COPYING: "The gate is deterministic first and the LLM audit second" (cheap models invent CLI flags). Watch seo_topics.reason: many rejections with one cause means fix facts.json, not the prompt.
- BECKY MATCH: partial. becky-validate (validation of outputs), becky-judge (two-stage judge), becky-route. None checks claims against a facts file.

### seo-engine: measure and learning loop  (source: seo-agent-kit/.claude/skills/seo-engine/scripts/measure.mjs, add-youtube-links.mjs; schema.sql)
- PURPOSE: Learn which topics bring signups and re-weight the queue.
- FUNCTIONALITY: Measure attributes every PostHog session to the blog post it entered on (entry visitors, signup-page visits, signups, AI-engine visitors), adds Search Console clicks, impressions and position. Cluster weight = Bayesian-smoothed signups per post against the blog average, clamped 0.5x to 3x. Visitors and AI referrals break ties. Queued topic score = base x weight (never compounding). Nightly. add-youtube-links.mjs adds YouTube links to posts.
- USE CASE FOR JORDAN: A nightly loop that re-ranks the next video or post topics by the results of past ones. The "never compounding" rule (multiply the base score, not the last score) is an easy mistake to copy wrong, so it is worth keeping.
- DECISION MODEL: none in the scoring. System One candidate: "Did this post lead to a signup? (yes/no)" per session, to replace the attribution maths. Not needed; the counts are deterministic.
- DETERMINISTIC PARTS: All of it (attribution, smoothing, clamp, scoring).
- DATA IT NEEDS: PostHog sessions, Search Console, signups.
- RULES WORTH COPYING: Bayesian smoothing and a clamp (0.5x to 3x) so one lucky post cannot dominate. Score = base x weight, never compounding.
- BECKY MATCH: none found for a feedback loop on results. Partial: becky-report.

### ai-search-files (AI answer engine visibility)  (source: seo-agent-kit/.claude/skills/ai-search-files/SKILL.md, templates/llms.ts, llms-route.ts, robots.ts, sitemap.ts, article-jsonld.ts, wp-blog.ts)
- PURPOSE: Make the blog easy for search engines and AI answer engines (ChatGPT, Claude, Perplexity, Gemini, Copilot) to read and cite.
- FUNCTIONALITY: Auto-generates llms.txt and llms-full.txt, a sitemap with every post, robots rules for AI crawlers, BlogPosting and FAQPage JSON-LD (from the FAQ section of each post), IndexNow pings, and AI-referral tracking in PostHog. Templates for Next.js App Router, reading posts from a WordPress REST API.
- USE CASE FOR JORDAN: Makes Jordan's written pages (channel pages, guides, tutorials) citable. Relevant if he publishes text pages. Not a video task.
- DECISION MODEL: none.
- DETERMINISTIC PARTS: All of it.
- DATA IT NEEDS: Post list from WordPress, optional IndexNow key, Search Console, PostHog.
- RULES WORTH COPYING: Every post gets an FAQ section (h3 questions), which becomes FAQPage JSON-LD.
- BECKY MATCH: none found.

### posthog-analytics (visitors, signups, traffic sources)  (source: seo-agent-kit/.claude/skills/posthog-analytics/SKILL.md, scripts/posthog-snapshot.mjs, lib/posthog-*.ts, components/WebFunnel.tsx, TrafficSources.tsx, WebTrafficChart.tsx)
- PURPOSE: Daily snapshots of website visitors, the signup funnel and traffic sources into Postgres, with dashboard cards.
- FUNCTIONALITY: PostHog HogQL queries run daily. Snapshots go to Postgres (web_analytics_snapshots and daily tables). Dashboard cards: web chart, web funnel, traffic sources (including AI referrals). Needs a PostHog personal key with query-read access.
- USE CASE FOR JORDAN: The same daily snapshot pattern as Creator OS, for a website. Jordan's site analytics if he has one.
- DECISION MODEL: none.
- DETERMINISTIC PARTS: All of it.
- DATA IT NEEDS: PostHog events.
- RULES WORTH COPYING: Rows must not be capped at 100 (the SKILL.md troubleshooting names a 100-row cap as a common bug).
- BECKY MATCH: none found.

### wordpress-blog (connect and edit a WordPress site through Creator OS)  (source: seo-agent-kit/.claude/skills/wordpress-blog/SKILL.md)
- PURPOSE: Let an agent list, write, update, publish or delete articles on a WordPress blog.
- FUNCTIONALITY: Connect a self-hosted WordPress site (Application Password) or WordPress.com through Creator OS. Then list, write, update, publish and delete posts through the MCP connector, the REST API or the creatoros CLI. Workflows: write one article, repurpose a winning social post into an article, refresh an old post, bulk-fix every post, publish an approved draft.
- USE CASE FOR JORDAN: Turning a winning video into a written article for a blog. Only if Jordan has a WordPress blog.
- DECISION MODEL: LLM free-text (article).
- DETERMINISTIC PARTS: Connect, list, publish, delete.
- DATA IT NEEDS: WordPress credentials, post text.
- RULES WORTH COPYING: "Publish an approved draft" as a separate step from writing.
- BECKY MATCH: none found.

## PART F - social-agents (CreatorOS agent harness, CLI + worker + dashboard)

Context: a CLI and a worker that run a whole social presence through the CreatorOS API (posting, automations, comment and DM replies, analytics). The README says "no AI setup, no model keys" for the basic setup: the brain uses the logged-in Claude CLI (or an Anthropic key). Templates are 12 skills; the source tree has the CLI, worker, dashboard, onboarding, webhook receiver, tool registry and automations.

### Agent Posts (video drop to schedule everywhere)  (source: social-agents/templates/skills/agent-posts, templates/skills/agent-posts/scripts/vertical-video-thumbnail)
- PURPOSE: Hand over a finished vertical video and get it posted everywhere, with the comment funnel armed on sign-off.
- FUNCTIONALITY: Transcribes the video, pulls the "comment X" keyword, writes on-brand captions for every platform, builds a 9:16 cover (the vertical-video-thumbnail script burns the hook lines), schedules to every connected social through CreatorOS, verifies with get_post, and with sign-off arms the comment-to-DM funnel.
- USE CASE FOR JORDAN: The end-to-end "post my finished video" flow. The same job as creator-os agent-posts, in a different harness.
- DECISION MODEL: LLM free-text (captions, hook lines). Candidate System One: "Is the cover icon what the video says? (yes/no)" before scheduling.
- DETERMINISTIC PARTS: Transcript, keyword, cover burn, schedule call, verify.
- DATA IT NEEDS: Finished vertical video, brand pack, CreatorOS keys.
- RULES WORTH COPYING: Verify each post after scheduling (get_post), and stop and report on a failed platform rather than guessing.
- BECKY MATCH: partial. becky-reel, becky-captions. No scheduler found in becky.

### analytics-report (weekly performance report)  (source: social-agents/templates/skills/analytics-report/SKILL.md)
- PURPOSE: A weekly report that says what worked, what did not, and one thing to do next week.
- FUNCTIONALITY: follower_stats for the last 7 days vs the prior 7, post_analytics for the top 3 and bottom 3 posts, daily_metrics for the week's shape, best_time_to_post (slots in UTC, day 0 = Monday, converted to the user's timezone). Competitor notes refreshed when older than 2 weeks. Output: platform one-liners, what worked and did not (2 to 3 sentences), competitor movement (1 to 2 sentences), one recommendation.
- USE CASE FOR JORDAN: A weekly "what worked" note for his channel. The report shape (one recommendation, no metric dump) is the reusable part.
- DECISION MODEL: LLM free-text for the read-out. System One candidate: "Did this post beat the channel average? (yes/no)" per post.
- DETERMINISTIC PARTS: Numbers, deltas, top and bottom 3, timezone conversion.
- DATA IT NEEDS: Follower and post analytics (add-on needed on the CreatorOS plan).
- RULES WORTH COPYING: "Flat is flat, down is down." Absolute numbers AND deltas. Mark stale data rather than present it as current. Exactly one recommendation.
- BECKY MATCH: partial. becky-report (report builder). No channel analytics feed in becky.

### automations (cloud funnels vs scheduled agent runs)  (source: social-agents/templates/skills/automations/SKILL.md, src/worker/schedule.ts, src/automations/crons.ts)
- PURPOSE: Choose the right kind of automation: a server-side rule or a scheduled agent run.
- FUNCTIONALITY: Two kinds. (1) CreatorOS cloud automations (comment-to-DM funnels), deterministic, run on the CreatorOS servers. (2) Scheduled agent runs (cron), a full agent that runs a skill on a schedule for judgment work (triage, picking the day's clip, weekly report). The in-process scheduler parses a strict 5-field cron (no MON or JAN names). Starter set: daily-shortform, weekly-calendar, engagement-sweep, weekly-analytics.
- USE CASE FOR JORDAN: The rule "deterministic trigger goes to a server rule, judgment goes to a scheduled agent" is a clean split for Becky's automations.
- DECISION MODEL: none for the split. Could be System One: "Does this task need judgment? (yes/no)" to choose the kind.
- DETERMINISTIC PARTS: The cron parser, the funnel rule.
- DATA IT NEEDS: Pathway settings, timezone, brand pack.
- RULES WORTH COPYING: Strict 5-field cron (the source says "no MON/JAN names"). Tell the user in plain language what will run and when. Say that local runs execute unattended.
- BECKY MATCH: partial. becky-jobs, becky-foreman, becky-pipeline.

### brand-interview (the brand pack, asked one question at a time)  (source: social-agents/templates/skills/brand-interview/SKILL.md)
- PURPOSE: Build BRAND.md, the one file every caption, description and call to action comes from.
- FUNCTIONALITY: One question at a time, reflect back, one follow-up if thin. Questions: about (what it is, what is marketed), offers (what is sold, with links), voice in three adjectives, one thing the copy must never sound like, emoji policy (none, sparingly, free), hashtag policy (none, 2 to 4, aggressive), target audience in one sentence.
- USE CASE FOR JORDAN: A channel brand file for Jordan (voice, never-words, emoji and hashtag policy). Directly useful for the "my standards" thumbnail and caption rules.
- DECISION MODEL: none (a questionnaire). Could be System One: "Is this answer specific enough to write from? (yes/no)" to decide when to ask a follow-up.
- DETERMINISTIC PARTS: The question order.
- DATA IT NEEDS: Jordan's answers.
- RULES WORTH COPYING: "Never batch the questions into a form, never fill in guesses." Ask one at a time.
- BECKY MATCH: none found. None found.

### comment-dm-funnel (comment keyword to DM)  (source: social-agents/templates/skills/comment-dm-funnel/SKILL.md, src/automations/funnels.ts)
- PURPOSE: When someone comments a keyword, they automatically get a DM with a link. Instagram and Facebook only.
- FUNCTIONALITY: Per-post funnels (one active per post) or account-wide funnels (stack, with their own keywords). Default match mode "contains" (also "word" and "exact"). Swapping a funnel is delete then create (CreatorOS has no pause). Daily log check at 9am: failures are the signal. The DM text is read back to the human before creating.
- USE CASE FOR JORDAN: "Comment GUIDE and get the link." A proven creator pattern for a YouTube or Instagram channel with a lead magnet.
- DECISION MODEL: none (keyword match). System One candidate: "Is this comment asking for the offer? (yes/no)" for free-form comments that do not use the keyword.
- DETERMINISTIC PARTS: Keyword match, the funnel, logs, daily check.
- DATA IT NEEDS: Brand offer copy and links, connected account IDs, funnel logs.
- RULES WORTH COPYING: Nothing goes live or dies without sign-off (DMs go to strangers; deletes destroy logs). One keyword beats five. Never use an empty keyword list (it matches every comment).
- BECKY MATCH: none found.

### post-longform (YouTube long-form upload)  (source: social-agents/templates/skills/post-longform/SKILL.md)
- PURPOSE: Publish a long video to YouTube with title, description and tags, and update metadata on published videos.
- FUNCTIONALITY: upload_media (video), create_post with the YouTube target options (title, visibility, category, playlist, madeForKids, first comment), description up to 5000 characters, title up to 100 characters, custom thumbnail (JPEG or PNG under 2 MB) through the cover field. update_youtube_metadata for published videos. Scheduled uploads go up private and flip public at the scheduled time.
- USE CASE FOR JORDAN: Exactly the post-longform automation Jordan asked about. Its judgment rules are copyable as is: title under about 70 characters with the hook first; first 2 lines of the description carry the pitch; never publish a filename as the title (stop and ask); madeForKids stays false unless told otherwise (true disables comments permanently).
- DECISION MODEL: LLM free-text (description and tags). System One candidates: "Does the title look like a filename? (yes/no)" (stop and ask if yes); "Is this title over 70 characters? (yes/no)" (rule, not AI).
- DETERMINISTIC PARTS: Upload, title length, madeForKids default, scheduling, retry once, verification by get_post.
- DATA IT NEEDS: Long video file, thumbnail, YouTube account ID, brand pack (links and CTA).
- RULES WORTH COPYING: The judgment rules listed above. Retry once, then report. Verify the post after creation.
- BECKY MATCH: none found for the upload. Partial: becky-vegas (VEGAS side), becky-intake (playlist to notes).

### post-shortform (short vertical publish to TikTok, Reels, Shorts)  (source: social-agents/templates/skills/post-shortform/SKILL.md)
- PURPOSE: Post one short video to TikTok, Instagram Reels and YouTube Shorts in one call.
- FUNCTIONALITY: Validates the file (exists, video, vertical), upload_media once (the media ID works on every network), validate_post_length per network, TikTok creator info for privacy options, one create_post with platforms, post_type short_video, a YouTube title and a cover (or cover_timestamp_ms). Timing: schedule_at with a timezone, or nothing (publishes now). Keeps a POSTED.md ledger when pulling from a content library.
- USE CASE FOR JORDAN: One upload, three platforms. The "upload once, one call" pattern is what Jordan wants for his Shorts.
- DECISION MODEL: LLM free-text (caption). System One candidate: "Is this video landscape? (yes/no)" (the skill warns before posting landscape as a Reel or TikTok).
- DETERMINISTIC PARTS: Validation, upload, length checks, the posted ledger.
- DATA IT NEEDS: Video, cover, caption, brand pack.
- RULES WORTH COPYING: If one platform fails validation, post to the rest and report. Never post without the content existing. Ask about the funnel after posting.
- BECKY MATCH: none found for a posting step.

### post-threads (X and Threads thread posts)  (source: social-agents/templates/skills/post-threads/SKILL.md)
- PURPOSE: Post a multi-part text thread to X and Threads.
- FUNCTIONALITY: Hook first in part 1, one idea per part, a closing call to action. Native threads (threadItems), not sequential posts. Limits: X 280 characters per part, Threads 500. Splits long parts at sentence boundaries.
- USE CASE FOR JORDAN: Text-thread companion to a video. Low priority.
- DECISION MODEL: LLM free-text.
- DETERMINISTIC PARTS: Length checks, splitting, posting.
- DATA IT NEEDS: Thread text, accounts.
- RULES WORTH COPYING: Validate length per platform before posting.
- BECKY MATCH: none found.

### schedule-posts (batch calendar scheduling)  (source: social-agents/templates/skills/schedule-posts/SKILL.md)
- PURPOSE: Schedule a whole content calendar in one go.
- FUNCTIONALITY: From a CSV, a table or a folder. Exactly one timing mode per post: exact time (schedule_at + timezone), queue (queuedFromProfile, next open slot in the user's queue), or draft. If none is set, the post publishes immediately, so the skill checks for that.
- USE CASE FOR JORDAN: Batch-schedule a month of uploads from a sheet. The "exactly one timing mode" rule prevents accidental live posts.
- DECISION MODEL: none.
- DETERMINISTIC PARTS: All of it.
- DATA IT NEEDS: Calendar sheet, timezone, content library ledger.
- RULES WORTH COPYING: A missing timing field publishes immediately. Check it on every row.
- BECKY MATCH: none found.

### respond-to-comments (comment triage and replies)  (source: social-agents/templates/skills/respond-to-comments/SKILL.md)
- PURPOSE: Triage comments across accounts and post on-brand replies, on a schedule or on demand.
- FUNCTIONALITY: Fetches comments since the last run (default last 24 hours, paged by cursor). Drops the bot's own comments (marked). Triage into five buckets: REPLY, SKIP (spam, bots, trolls), ESCALATE (refunds, billing, complaints, legal, medical, financial, press, minors, safety), LIKE-ONLY (positive, empty), HIDE (scam links, slurs, harassment; never hides criticism). Draft in brand voice, short, specific. Caps at about 30 replies per run. Report counts per bucket, quote every escalation.
- USE CASE FOR JORDAN: A safe comment-reply assistant for his channel. The five-bucket triage with a fixed escalation list is the best part, and it is a clear candidate for a System One pick-one question.
- DECISION MODEL: LLM free-text (reply). System One candidate, pick one per comment: "Which bucket: REPLY, SKIP, ESCALATE, LIKE-ONLY, HIDE?" plus yes/no "Does this touch refunds, billing, legal, medical, financial, press, a minor, or safety?"
- DETERMINISTIC PARTS: Self-comment drop, fetch window, paging, the 30-reply cap, escalation keyword list, report counts.
- DATA IT NEEDS: Comments, brand voice, escalation topics, engagement agent objective.
- RULES WORTH COPYING: Never reply to yourself on a cron run (the cardinal rule). "When unsure which bucket, escalate." Never argue. Never promise dates, refunds or features the human has not stated publicly. Hide, do not delete (delete is irreversible).
- BECKY MATCH: none found.

### respond-to-messages (DM replies)  (source: social-agents/templates/skills/respond-to-messages/SKILL.md)
- PURPOSE: Reply to DMs across accounts, one reply per conversation per run.
- FUNCTIONALITY: Lists conversations for the enabled platforms. Latest message is yours: skip. Latest message is theirs: read the thread, then REPLY, SKIP or ESCALATE. Sends one reply per conversation. No link drops before the person asks. Does not double-text.
- USE CASE FOR JORDAN: DM replies with strict limits. The "do not double-text" and "no link before they ask" rules are copyable.
- DECISION MODEL: LLM free-text (reply). System One candidate: "Is the latest message from them and unanswered? (yes/no)" as the handled check.
- DETERMINISTIC PARTS: Latest-sender check, one reply per run, escalation list.
- DATA IT NEEDS: DM threads, brand voice, escalation topics.
- RULES WORTH COPYING: The inbox is personal space: no unprompted pitches. "When unsure, escalate."
- BECKY MATCH: none found.

### provision-railway (cloud worker set-up)  (source: social-agents/templates/skills/provision-railway/SKILL.md, src/automations/railwayProvision.ts, src/dashboard/railway.ts)
- PURPOSE: Build the Railway cloud worker end to end (project, upload, variables, domain, health check) so scheduled runs happen without the laptop on.
- FUNCTIONALITY: Reads the Railway token from the local credentials file (not printed). Uploads the workspace with a method that includes gitignored files (plain railway up would drop them, which the skill warns about). Sets variables, public domain, health check.
- USE CASE FOR JORDAN: A cloud worker so scheduled jobs run without his PC. Useful later; costs money and needs Jordan's accounts.
- DECISION MODEL: none.
- DETERMINISTIC PARTS: All of it.
- DATA IT NEEDS: Railway account token, workspace files.
- RULES WORTH COPYING: Account token, not project token, or provisioning refuses to run. Never print a secret. Ask before global installs.
- BECKY MATCH: none found.

### Onboarding interview and API key check  (source: social-agents/src/onboarding/interview.ts, state.ts, render.ts)
- PURPOSE: Set up the tool in a few questions: "Do you have a CreatorOS key?", "How many?", then each key is checked live.
- FUNCTIONALITY: One key equals one workspace equals one set of socials. Keys are validated against the API before saving. State is saved, so setup resumes.
- USE CASE FOR JORDAN: The shape of a good setup flow for Becky's install: few questions, live check, resumable.
- DECISION MODEL: none.
- DETERMINISTIC PARTS: All of it.
- DATA IT NEEDS: API keys.
- RULES WORTH COPYING: Validate each key live before saving it.
- BECKY MATCH: partial. becky install scripts (Install Vegas Scripts.bat).

### Brain (how the agent thinks)  (source: social-agents/src/util/brain.ts, src/config/brainSetup.ts)
- PURPOSE: Pick the model that drives the agent.
- FUNCTIONALITY: Default is Claude through the user's own logged-in claude CLI (no API key) or an ANTHROPIC_API_KEY. Fallback: any Anthropic-compatible endpoint (base URL, key, model id) run through the same Agent SDK by setting ANTHROPIC_BASE_URL.
- USE CASE FOR JORDAN: Shows how to use Jordan's own Claude plan with no API key. Relevant to his "Claude plan burn" concern.
- DECISION MODEL: none (routing by config).
- DETERMINISTIC PARTS: Config selection.
- DATA IT NEEDS: Credentials.
- RULES WORTH COPYING: Use the logged-in CLI first; fall back to a key.
- BECKY MATCH: partial. becky-go model selection and becky-harness.

### Tool registry and platform matrix  (source: social-agents/src/agent/registry.ts, tools.ts, src/client/platformMatrix.ts, selfGuard.ts, endpoints.ts)
- PURPOSE: One list of what the agent may do, and a hard limit on what each platform allows.
- FUNCTIONALITY: registry.ts (506 lines) defines the tool belt used by both the Claude Agent SDK and any OpenAI-style API loop. The allow-list, hard blocks and platform matrix (which platform supports which action, for example TikTok comments are not supported, delete is not on some networks) are enforced inside the CreatorOS client, so the model cannot bypass them. selfGuard.ts is a guard against the agent calling its own tools in a loop (not read in detail).
- USE CASE FOR JORDAN: The "enforced in code, not in the prompt" approach. Matches Jordan's rule that rules must be enforced by the harness.
- DECISION MODEL: none.
- DETERMINISTIC PARTS: The matrix and the blocks.
- DATA IT NEEDS: Platform capability matrix.
- RULES WORTH COPYING: Hard blocks in the client, not in the prompt.
- BECKY MATCH: partial. becky-harness (single-tool harness). The enforce-in-code pattern is in becky's own rules.

### Background worker and automation runner  (source: social-agents/src/worker/index.ts, runner.ts, server.ts, automations.ts; Dockerfile.worker)
- PURPOSE: Run the scheduled jobs in a container or on the laptop.
- FUNCTIONALITY: In-process scheduler (cron, next-run calculation), runner executes each skill as an agent run, server exposes status, Dockerfile.worker builds the container for Railway.
- USE CASE FOR JORDAN: A single worker process for scheduled jobs. Compare with Becky's run-everything jobs.
- DECISION MODEL: none.
- DETERMINISTIC PARTS: Scheduler, container.
- DATA IT NEEDS: Job list.
- RULES WORTH COPYING: One worker, one scheduler, a status endpoint.
- BECKY MATCH: partial. becky-jobs, becky-foreman.

### Webhook receiver  (source: social-agents/src/webhooks/receiver.ts)
- PURPOSE: Receive platform events (comments, messages) instead of polling.
- FUNCTIONALITY: Receives webhook events and passes them to the automations. Tests exist (webhooks.test.ts).
- USE CASE FOR JORDAN: Event-driven comment handling. Better than polling.
- DECISION MODEL: none.
- DETERMINISTIC PARTS: All of it.
- DATA IT NEEDS: Platform events.
- RULES WORTH COPYING: Event over polling.
- BECKY MATCH: none found.

### Dashboard and agent understanding panel  (source: social-agents/dashboard/server.ts, src/dashboard/understanding.ts, flows.ts, workflows.ts, src/ui/*)
- PURPOSE: Show what the agent is, what it is aiming for, and what is running.
- FUNCTIONALITY: understanding.ts composes the agent's persona, objective, KPIs, how it handles comments and messages, and what the account sells, all read from the same brand and config files. Flows and workflows show the automations. Terminal UI with banner, markdown and preview.
- USE CASE FOR JORDAN: A plain "what is the agent doing for me" panel. Kevin's rule that everything the agent does must be visible.
- DECISION MODEL: none.
- DETERMINISTIC PARTS: All of it.
- DATA IT NEEDS: Config and brand files.
- RULES WORTH COPYING: Read the panel from the same files the agent reads, so the display cannot disagree with the agent.
- BECKY MATCH: partial. becky-presence, becky-canvas.

### Social Agents knowledge and tutorial files  (source: social-agents/README.md "Teaching Social Agents new patterns", templates/skills/automations "knowledge/TUTORIALS.md")
- PURPOSE: Teach the agent new patterns by writing them down.
- FUNCTIONALITY: Before building a pattern the agent has not built, it reads knowledge/TUTORIALS.md and follows the taught steps. Competitor notes in knowledge/COMPETITORS.md, refreshed when stale.
- USE CASE FOR JORDAN: Same as Becky's "learn from notes" pattern. Note: a "teach the agent" file is only as good as its upkeep.
- DECISION MODEL: none.
- DETERMINISTIC PARTS: Reading the file.
- DATA IT NEEDS: Notes.
- RULES WORTH COPYING: Check the taught steps before building a new automation.
- BECKY MATCH: partial. becky-docs and becky-research notes (qmd collection).

## PART G - "These 30 Jev AI + Claude Use Cases Are INSANE" (Kev Builds Apps, 2026-10-07, video MT9uNomIvgk)

Source files: Obsidian browser_data/YouTube/2026-10-07_MT9uNomIvgk.md (note) and .transcript.md (YouTube auto-captions, about 17 minutes). Jev is a typed "System One" decision model from TypeSafe. The video's whole premise is that a yes/no, pick-one or ranking question answered by Jev is cheaper and faster than asking a big LLM. Every use case below is therefore a System One candidate by definition. The question text is the one the video implies. Where the video does not say the exact question, the question is marked (inferred).

GAP: The title says 30. The note and the transcript name 23 use cases, numbered 1 to 23 below. The other 7 are not in the video text I have. The eight "pillars" the speaker lists are lead and prospect intelligence, social and customer communication, real-time moderation and live operations, custom scoring and evaluation models, agent orchestration and routing, knowledge graph and workflow memory, computer and browser use, and marketing. Social and customer communication and custom scoring and evaluation models are named as pillars but have no separate use case in the text. Only the transcript can close this gap. Fetching the full video (not done, no browser use per Jordan's rule) would find the other 7.

Overlap note for every block: Becky already has System One through becky-decide (Laya on CPU, plus a paid hosted Jev path via systemone.Hosted, capped at $5 a month). "Already solved" means the decision engine exists. "Utilised" means a tool calls it for that job. Most of these jobs are NOT yet calling it.

### Jev 1 - Intent detection on warm inbound leads  (source: MT9uNomIvgk note + transcript [01:02] to [02:02])
- PURPOSE: Find which inbound contacts (website visitors, social DMs, SMS, voicemail) have real buying intent, so they get handled first.
- FUNCTIONALITY: Feed the inbound text or click trail (for example PostHog checkout-page clicks) to Jev. Jev ranks each one against what typical customers say. High score gets tagged "warm lead". The video says you can scan thousands of DMs at once and rank by intent.
- USE CASE FOR JORDAN: Sponsor and business inquiries to his channel: rank them by intent so he replies to the serious ones first. Not a content task.
- DECISION MODEL: System One/Jev (question (inferred): "Is this contact a warm buyer or sponsor lead? (yes/no) / rank by intent").
- DETERMINISTIC PARTS: Pulling the messages, storing them, the tagging step once a score exists, the clicks trail itself.
- DATA IT NEEDS: Inbound messages (email, DMs), page events from analytics, a definition of a warm lead written by Jordan.
- RULES WORTH COPYING: A typed score, not a free summary, so the result can be sorted and checked.
- BECKY MATCH: none found. becky-intake and becky-decide are the nearest pieces (decision on playlist items). Not connected to any inbox.

### Jev 2 - Tagging and filtering an inbox  (source: note + transcript [02:02])
- PURPOSE: Sort a mixed inbox into sponsorship, lead, spam, and other, without reading each email.
- FUNCTIONALITY: Each new email gets one tag from a short list. The video's example is the speaker's personal inbox (sponsorship, lead, spam).
- USE CASE FOR JORDAN: Sorting his sponsor inbox and the spam. Saves reading time, which matters because reading is physically costly for him.
- DECISION MODEL: System One/Jev (question (inferred): "Which tag fits this email: sponsorship | lead | spam | other? (pick one)").
- DETERMINISTIC PARTS: Fetching mail, applying a label, filtering rules for known spam senders.
- DATA IT NEEDS: Email access (Gmail), tag list.
- RULES WORTH COPYING: Keep the tag list short and fixed so every email gets exactly one.
- BECKY MATCH: none found.

### Jev 3 - Lead scoring against an ideal customer profile  (source: note + transcript [03:04])
- PURPOSE: Rank a large list of scraped leads by how well each fits the ideal customer, then write personal outreach only for the top ones.
- FUNCTIONALITY: Give Jev the ideal customer description and each lead's metadata (location, follower count, occupation, profile). Jev scores each lead. The top slice goes to personalised outreach.
- USE CASE FOR JORDAN: Ranking a list of possible sponsors or collaborators against his channel profile (audience size, niche, location). Not a content task, but the scoring method is the same as for any ranking.
- DECISION MODEL: System One/Jev (question (inferred): "Does this lead match the ideal customer on: [criteria]? (score)").
- DETERMINISTIC PARTS: Filtering on hard limits (for example minimum followers), sorting, exporting the list.
- DATA IT NEEDS: Lead list with metadata, ideal-customer text written by Jordan.
- RULES WORTH COPYING: Hard filters first (deterministic), then the typed score on what is left.
- BECKY MATCH: none found.

### Jev 4 - Influencer discovery (rank creators to sponsor)  (source: note + transcript [03:04])
- PURPOSE: Find creators worth sponsoring, ranked by a set of criteria the user writes.
- FUNCTIONALITY: Same method as lead scoring: write the ideal influencer, feed candidates, Jev ranks them.
- USE CASE FOR JORDAN: Finding collaborators for his channel. Useful only if he plans to sponsor or collaborate.
- DECISION MODEL: System One/Jev (question (inferred): "Does this creator match the ideal sponsored creator? (rank)").
- DETERMINISTIC PARTS: Candidate pulling, filtering, sorting.
- DATA IT NEEDS: Creator profile data (followers, niche, engagement).
- RULES WORTH COPYING: Criteria are written down first; the ranking is then reproducible.
- BECKY MATCH: none found.

### Jev 5 - Sponsor discovery (rank sponsors to pitch to)  (source: note + transcript [03:04])
- PURPOSE: Decide which brands to pitch for sponsorship, ranked by fit.
- FUNCTIONALITY: Same method as influencer discovery, with "what sponsors do I want on the channel" as the criteria.
- USE CASE FOR JORDAN: Choosing which brands to pitch. A business-side task.
- DECISION MODEL: System One/Jev (question (inferred): "Is this brand a good sponsor fit for the channel's criteria? (rank)").
- DETERMINISTIC PARTS: Candidate list, sorting, the criteria file.
- DATA IT NEEDS: Brand list, channel criteria written by Jordan.
- RULES WORTH COPYING: Written criteria for what he refuses to sponsor (the video does not cover this; it is a suggestion, not from the video).
- BECKY MATCH: none found.

### Jev 6 - Automated CRM updating from tags  (source: note + transcript [04:06])
- PURPOSE: Keep the contact list up to date from the tags in pillars 1 to 5, with no human typing.
- FUNCTIONALITY: Once an inbound message is tagged warm (or sponsor, or spam), the CRM record is updated automatically, so the next step in the pipeline runs.
- USE CASE FOR JORDAN: Only if he keeps a CRM of sponsors or collaborators. Not needed today.
- DECISION MODEL: System One/Jev for the tag; none for the write.
- DETERMINISTIC PARTS: The record write, the field mapping, the pipeline trigger.
- DATA IT NEEDS: A CRM, tags from earlier blocks.
- RULES WORTH COPYING: The tag is the trigger; the human does not retype.
- BECKY MATCH: none found.

### Jev 7 - Sending outreach (computer-use, with LLM only for the wording)  (source: note + transcript [04:43])
- PURPOSE: Once a lead is warm and fits the profile, send the message. Only the wording needs an LLM; the sending is done by computer-use steps.
- FUNCTIONALITY: Find the user, open their inbox, send the message with a computer-use tool. The video says the LLM is needed only for writing the outbound message. A yes/no trigger (Jev) starts the send when the lead is warm.
- USE CASE FOR JORDAN: Automatic outreach. Carries real risk on a personal channel (spam, bans). Jordan's browser rule rules out Chrome-based computer use; the agent-firefox route would be the only allowed route.
- DECISION MODEL: System One/Jev (question (inferred): "Is this lead warm and fits the profile? (yes/no) -> send"). LLM free-text for the message itself.
- DETERMINISTIC PARTS: Finding the inbox, the send action, rate limits, logging.
- DATA IT NEEDS: Lead list, approved message template, inbox access.
- RULES WORTH COPYING: Keep the LLM only for the wording (the video's cost argument).
- BECKY MATCH: none found. Browser automation conflicts with Jordan's approved browser rule.

### Jev 8 - Playing video games with computer-use (probability-based)  (source: note + transcript [05:33] to [06:38])
- PURPOSE: An agent that plays simple 2D games by clicking, with the choice made by probability of winning or surviving.
- FUNCTIONALITY: A fast model clicks buttons; a bigger model (the speaker names Opus 5.5) picks the move. The video shows Snake, chess and Subway Surfers. The speaker promises a masterclass on reverse-engineering games.
- USE CASE FOR JORDAN: Content (a gameplay-agent video) rather than a workflow. Could be a stream segment. Low priority.
- DECISION MODEL: System One/Jev (question (inferred): "Which of these moves has the highest probability of surviving or winning? (pick one)").
- DETERMINISTIC PARTS: Clicking, screen capture, the game loop.
- DATA IT NEEDS: Screen capture of the game.
- RULES WORTH COPYING: Fast model for the clicks, slow model for the choice (this split matches the video's cost argument).
- BECKY MATCH: none found.

### Jev 9 - App and interface testing with many simulated users  (source: note + transcript [06:38])
- PURPOSE: Test an app by running many agents that click around it, then have an AI review each recording.
- FUNCTIONALITY: Spin up many Jev agents. Each clicks through the interface, records its screen, and sends the recording to an AI model for review. Bugs are reported before launch.
- USE CASE FOR JORDAN: Testing a Becky UI (for example Becky Review) with many scripted runs. A useful test idea for the GUI work he has been doing.
- DECISION MODEL: System One/Jev (question (inferred): "Did this click path reach the goal screen? (yes/no)") plus an AI review of the recording (LLM).
- DETERMINISTIC PARTS: Running the clicks, recording, collecting the results.
- DATA IT NEEDS: The app to test, a list of tasks.
- RULES WORTH COPYING: Many small runs instead of one long one; each run's result is typed.
- BECKY MATCH: partial. becky-review-index, becky-review (UI) exist; no simulated-user test runner found.

### Jev 10 - Deal-hunting swarm (buy, negotiate, hand off)  (source: note + transcript [07:41])
- PURPOSE: An agent that searches for a deal (the video example is a used car on Facebook Marketplace with filters), and for B2B, negotiates with suppliers until a target price, then hands over to a human.
- FUNCTIONALITY: Computer-use agent with filters and a target. Negotiation goes back and forth until the price target is met, then passes to a human.
- USE CASE FOR JORDAN: Not a content task. Could be used to source gear for filming or editing at a price target. Low priority.
- DECISION MODEL: System One/Jev (question (inferred): "Has the offer reached the target price? (yes/no)"; "Is this listing within the filters? (yes/no)").
- DETERMINISTIC PARTS: Search filters, price comparison, stop rule, hand-off.
- DATA IT NEEDS: Listings, target price, filters.
- RULES WORTH COPYING: A hard stop at the target, then a human decision.
- BECKY MATCH: none found.

### Jev 11 - Computer desktop cleaning and file sorting (by many criteria)  (source: note + transcript [07:41])
- PURPOSE: Sort a messy computer's files into folders by rules (up to about 100 criteria), with a vision model for images and documents.
- FUNCTIONALITY: Jev with a vision model reads each file's name, type and content, then picks a folder from a list. The speaker says the code is already open source and he is building a mobile version.
- USE CASE FOR JORDAN: Cleaning up his footage, project and export folders, which are large and messy. Relevant to a VEGAS project library. Needs a file-by-file review before moving anything (move is reversible only if logged).
- DECISION MODEL: System One/Jev (question (inferred): "Which folder does this file belong in? (pick one from the list)").
- DETERMINISTIC PARTS: Listing files, moving files, logging, the folder list.
- DATA IT NEEDS: File list and metadata, the folder rules.
- RULES WORTH COPYING: Log every move so it can be undone. Becky already has cleanup scripts (cleanup-orphans.ps1 in becky-tools).
- BECKY MATCH: partial. becky-tools has cleanup-orphans.ps1 and CLEANUP-ORPHANS-SPEC.md, which clean up orphaned files in its own repo, not a general file sorter. Check before building.

### Jev 12 - Automating anything with no API (social and web tasks) through computer-use  (source: note + transcript [08:44])
- PURPOSE: Use a computer-use agent for any site or app that has no API, with Jev deciding each step, to save tokens.
- FUNCTIONALITY: Jev picks the next action (click, type, read) and the LLM is called only when a free-text answer is needed. The video says a whole pillar of this (about 12 to 14 use cases) has a tutorial.
- USE CASE FOR JORDAN: Any site without an API. Browser rule applies: agent-firefox only.
- DECISION MODEL: System One/Jev (question (inferred): "What is the next action? (pick one of: click | type | read | done)").
- DETERMINISTIC PARTS: Executing the action, screenshots, the step loop.
- DATA IT NEEDS: The site, the goal.
- RULES WORTH COPYING: Jev chooses the step; the LLM is called only for free text (the video's token-saving claim, not verified here).
- BECKY MATCH: partial. becky-unstick (browser-control model for one stuck step, Fara1.5-4B). Compare with agent-firefox before any new browser tool.

### Jev 13 - Receipt and expense extractor (bank transactions to a knowledge graph)  (source: note + transcript [09:48])
- PURPOSE: Pull every business transaction from a bank account, tag it as a business expense, and store it so expense categories can be pulled up instantly.
- FUNCTIONALITY: Transactions are extracted, tagged by Jev (business expense: yes or no; category), and stored in an Obsidian knowledge graph. Queries return verified categories.
- USE CASE FOR JORDAN: Business expense tracking for his channel income and tax. Bank data is sensitive; keep it local.
- DECISION MODEL: System One/Jev (question (inferred): "Is this transaction a business expense? (yes/no)"; "Which expense category? (pick one)").
- DETERMINISTIC PARTS: Extraction, storing, totals, date filters.
- DATA IT NEEDS: Bank export (CSV), category list.
- RULES WORTH COPYING: Totals are computed in code, never by the model (Becky already has this rule: "the model picks, code computes").
- BECKY MATCH: none found.

### Jev 14 - Internal data search (study winning sales calls)  (source: note + transcript [10:39])
- PURPOSE: Store a company's call transcripts, then study the calls that won and find the tactics that worked.
- FUNCTIONALITY: Transcripts are stored; Jev answers "which calls closed?" and retrieves examples of the tactic. The video's example is sales calls. Its analogy for Jordan: video transcripts (his own winning videos).
- USE CASE FOR JORDAN: Studying his own winning videos: store transcripts, ask which hooks or openings worked. Ties to the "measure my own videos" idea in the copywriter skill.
- DECISION MODEL: System One/Jev (question (inferred): "Did this video or call reach the success outcome? (yes/no)") plus LLM free-text for the lesson.
- DETERMINISTIC PARTS: Storing transcripts, retrieving by tag, counting.
- DATA IT NEEDS: Transcripts, outcome numbers (views, sales).
- RULES WORTH COPYING: Pair each transcript with a measured outcome before asking "what worked".
- BECKY MATCH: partial. becky-transcribe (transcripts), becky-besttake, becky-judge (cross-corpus evidence). Outcome data (views) not linked.

### Jev 15 - Internal data tagging and retrieval  (source: note + transcript [10:39])
- PURPOSE: Tag company data as it arrives and fetch it again by tag.
- FUNCTIONALITY: Each item gets tags from a fixed list (Jev decides), then retrieval by tag. The video gives no specific example beyond the note.
- USE CASE FOR JORDAN: Tagging his footage and transcripts so he can ask for "every clip where I mention X".
- DECISION MODEL: System One/Jev (question (inferred): "Which tags apply to this item? (multi-select from list)").
- DETERMINISTIC PARTS: Storing, indexing, retrieval.
- DATA IT NEEDS: Items plus a tag list.
- RULES WORTH COPYING: Fixed tag list, not free tags.
- BECKY MATCH: partial. becky-search (search tool), becky-identify, qmd search over the Obsidian notes.

### Jev 16 - Media library retrieval for a video editor (knowledge graph feeds the editor)  (source: note + transcript [10:39])
- PURPOSE: Ask for a clip in plain language and have the editor pull it in instantly from a knowledge graph of media.
- FUNCTIONALITY: The Jev plus Obsidian graph holds the media index. The video says the editor (not named) pulled files in instantly through this link.
- USE CASE FOR JORDAN: "Get me the clip where I talk about X" inside VEGAS or Becky. Core to his editing work.
- DECISION MODEL: System One/Jev (question (inferred): "Which clip in the library matches this request? (pick one)").
- DETERMINISTIC PARTS: Indexing, file lookup, import.
- DATA IT NEEDS: Indexed media library, transcripts, tags.
- RULES WORTH COPYING: Index first (transcripts and tags), then search.
- BECKY MATCH: partial. becky-search, becky-moment, becky-hits, qmd. Creator-os has a similar vault search (get_media).

### Jev 17 - Live stream keyword moderation (remove comments that match a trigger)  (source: note + transcript [11:26])
- PURPOSE: Watch a live chat, and remove any comment that contains a banned word. The Jev yes/no answer is in milliseconds, so it can run in real time.
- FUNCTIONALITY: A watcher reads the chat feed. Each new comment that contains a trigger word is passed to Jev: "is this the word? yes or no". If yes, the comment is removed. The video says it runs and waits until a trigger appears.
- USE CASE FOR JORDAN: Live streams. Keyword removal for his own chat. Pairs with the becky-livechat tool idea.
- DECISION MODEL: System One/Jev (question (inferred): "Does this comment contain a banned word or phrase? (yes/no)").
- DETERMINISTIC PARTS: Reading the chat feed, the keyword list check, the delete action, the log.
- DATA IT NEEDS: Live chat feed, banned word list (Jordan writes this).
- RULES WORTH COPYING: The word list is the rule; the model answers yes or no only, so the result is binary.
- BECKY MATCH: partial. becky-livechat (chat replay), becky-livestream (clip-down). No live delete action found.

### Jev 18 - Discord and chat moderation bot (spam, slurs, timeouts)  (source: note + transcript [12:30])
- PURPOSE: Moderate a large chat community automatically: ban spammers, block bad messages, time out rude users for a set period.
- FUNCTIONALITY: Each message gets a yes/no trigger (spam? slur? rude?). The action is a fixed rule (block, timeout 30 minutes or 5 minutes in the example, or ban). The speaker says this is "binary, which is great", with no LLM thinking time.
- USE CASE FOR JORDAN: His Discord or live chat, if he runs one. Rules must be fixed in writing so the bot never acts on a guess.
- DECISION MODEL: System One/Jev (question (inferred): "Is this message spam? / a slur? / rude? (yes/no each)").
- DETERMINISTIC PARTS: Action mapping (block, timeout, ban), the time limits, the log.
- DATA IT NEEDS: Chat feed, moderation rules.
- RULES WORTH COPYING: Each trigger maps to one fixed action. A human reviews the rule list, not each action.
- BECKY MATCH: none found.

### Jev 19 - Viral idea mining from feeds (scan at high speed, extract posts with keywords)  (source: note + transcript [12:30] to [13:34])
- PURPOSE: Scan a feed (Twitter/X, Reddit, Instagram, TikTok, Google) far faster than a person, pull posts that match a keyword, and rank them by engagement.
- FUNCTIONALITY: Search by keyword, collect the most recent matches (the video's example: the latest 10,000 posts about a new model, ranked by engagement), show the top results instantly.
- USE CASE FOR JORDAN: Finding video ideas from what is trending in his niche (AI tools, editing). This is the "what should I make next" input. Jordan's rules would define the niche keywords.
- DECISION MODEL: System One/Jev (question (inferred): "Is this post about the topic keyword? (yes/no)"); engagement ranking is a number, not a model.
- DETERMINISTIC PARTS: Feed collection, keyword match, engagement sort, the export.
- DATA IT NEEDS: Feed access (platform APIs; some need paid keys), keyword list.
- RULES WORTH COPYING: Rank by a number (engagement), not by the model's opinion.
- BECKY MATCH: partial. becky-radar and becky-scout (scout and research watch lists; mainly for playlists and Chrome history per their specs). Not a feed scanner.

### Jev 20 - Finding the user's voice on Reddit (scored questions for content ideas)  (source: note + transcript [14:37])
- PURPOSE: Find the questions real people ask in niche subreddits, score them for fit to the audience, and turn the best ones into video ideas or posts.
- FUNCTIONALITY: An agent scrolls many marketing or niche subreddits. For each question it asks Jev if it fits the ideal customer, and scores it. The top questions go to a video idea or a post (the speaker says both are possible).
- USE CASE FOR JORDAN: Finding questions his viewers already ask (for example about editing or AI tools). A direct source of video topics.
- DECISION MODEL: System One/Jev (question (inferred): "Is this a real question from a target viewer? (yes/no)"; "How recent and how strong? (score)").
- DETERMINISTIC PARTS: Subreddit list, reading posts, sorting by recency, storing.
- DATA IT NEEDS: Subreddit list, ideal viewer text.
- RULES WORTH COPYING: A recent, specific question beats a popular general one (the speaker's emphasis on "most recently asked").
- BECKY MATCH: none found. Reddit access is also a browser-rule question. Check before automating.

### Jev 21 - Viral video identification (views against follower count)  (source: note + transcript [14:37])
- PURPOSE: Find videos in a niche that do better than the account size predicts, as a proxy for a video idea that works.
- FUNCTIONALITY: Scan Instagram and TikTok posts in the niche, compute views divided by followers (the video's example rule: views above total followers suggests a good idea), and rank.
- USE CASE FOR JORDAN: Spotting YouTube or Shorts topics that punch above the channel's size. A number-based rule Jordan can apply by hand.
- DECISION MODEL: none for the ratio. System One/Jev could answer "Is this video a strong idea for our niche? (yes/no)" as a second check (inferred).
- DETERMINISTIC PARTS: The ratio, the sort, the threshold (views over followers).
- DATA IT NEEDS: Post views and follower counts from each platform.
- RULES WORTH COPYING: The views-over-followers rule is simple and deterministic. The video says "whatever you think the calculation can be"; this is one example, not a validated metric.
- BECKY MATCH: none found. Creator-os has post analytics (get_post_analytics), which is the same kind of data.

### Jev 22 - Viral blog finding via Google Search Console (score competitor posts for a rewrite)  (source: note + transcript [15:40])
- PURPOSE: Each day, check a list of the best niche keywords in Google Search Console, see which posts rank, score them, and recreate the best ones.
- FUNCTIONALITY: A daily check of about 100 keywords. The top-ranking posts are scored by how close they are to what the account can recreate. An LLM writes the new version and it is published to the site.
- USE CASE FOR JORDAN: Only if Jordan runs a text blog. For a video channel the equivalent is "which keywords my videos should target".
- DECISION MODEL: System One/Jev (question (inferred): "Can we recreate this post well with what we know? (yes/no)"; "Is this post in our niche? (yes/no)"). LLM free-text for the rewrite.
- DETERMINISTIC PARTS: Keyword list, daily check, ranking data, publish.
- DATA IT NEEDS: Search Console access, keyword list, the site.
- RULES WORTH COPYING: Keyword list fixed in a file; the daily job is deterministic. The seo-agent-kit repo has the same loop (see that section).
- BECKY MATCH: none found. seo-agent-kit (Kevin's repo, see Part E) already does the same job in code.

### Jev 23 - Agentic routing (Jev as the front door that picks the model or skill)  (source: note + transcript [16:42])
- PURPOSE: Stop every prompt from going to a big model. Jev reads the prompt first and sends it to the right model, or straight to a skill, saving tokens and time.
- FUNCTIONALITY: Jev chooses between skills and models. The video says it can bypass the model entirely and run the skill directly. The speaker links a tutorial that connects Jev to Claude Code.
- USE CASE FOR JORDAN: Becky already does this. becky-ask routes plain English to a tool, and becky-route does the routing. Creator OS's jev-router hook does it on every prompt (see Part A).
- DECISION MODEL: System One/Jev (question: "Which skill or model should handle this prompt? (pick one)").
- DETERMINISTIC PARTS: Running the skill, the model call, the logging.
- DATA IT NEEDS: Skill list with plain descriptions; logged routing decisions (creator-os keeps them in jev_decisions).
- RULES WORTH COPYING: Keep the routing log; it is the training data for a local router (creator-os and Becky both plan this).
- BECKY MATCH: already solved. becky-ask and becky-route do this. Check whether becky-ask is calling becky-decide (Laya or hosted Jev) or a plain word match before adding another router.

## Summary of Jev blocks

- Blocks written: 23 (numbers 1 to 23 above). Video title claims 30; 7 not present in the text I have.
- Already solved or partly covered in Becky: 23 (routing: yes, becky-ask and becky-route); 16 (partly, via becky-search and becky-moment); 17 (partly, via becky-livechat). The rest are not covered.
- Jordan's most useful items from this video, in order (my judgement, not the video's): 16 (media retrieval for the editor), 19 (idea mining), 20 (question mining), 17 (live chat keyword removal), 21 (views over followers as a rule).
