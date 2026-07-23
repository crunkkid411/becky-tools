# HANDOFF: mpv-swap orchestration state (written 2026-07-23 ~00:40, mid-mission)

Written per Jordan's order so ANY fresh session (post-compact, post-crash, post-limit)
can pick this up cold. Supersedes nothing; pairs with HANDOFF-VIDEO-ENGINE.md (the build
order) and SPEC-BECKY-VIDEO-ENGINE.md (the architecture).

## THE MISSION (Jordan's order, non-negotiable)
Replace mpv in Becky Review 3 TONIGHT. mpv makes the app unusable for him: eats ~10
cores + pins GPU (keyboard lag), corrupts 29.97 sub-frame timing on the timeline.
"INSTANT - no lag" is the bar; a wall is a task, not a stopping condition. The ONLY
hard line: never deploy without working, in-sync audio.

## ACCEPTANCE (measured, on Jordan's real forensic reel, not adjectives)
- Scrub storm: 20+ random seeks/sec sustained 30s; UI never blocks; latest-frame chasing.
- Proxy-backed seek-to-paint p95 <= 50ms.
- Audio in sync; clean clip-boundary transitions; instant +/-1f.
- Before/after numbers vs mpv (use repo-documented mpv numbers; do NOT relaunch mpv build).

## CURRENT STATE
- master = 80d040d (pushed). Contains ALL app fixes from the 2-day run: crash fix
  (ecc35ec), edit ops on preview clips (79551e9), waveforms-back via ffmpeg peaks
  (b10aeef), captions from each clip's own transcript (ac8881e), one-frozen-color-per-
  source (7c2bb75), violent-input fixes (80d040d). App exe deliberately KILLED on
  Jordan's desktop (mpv resource theft) - do NOT relaunch any mpv build.
- Engine harness: worktree X:\AI-2\becky-wt-engine, branch local/video-engine @ 8dd12d3.
  Steps 0-3 of HANDOFF-VIDEO-ENGINE.md PROVEN (D3D11VA hw decode + frame-exact seek).
  All-intra scrub proxy PROVEN (304x540 h264, 4500 frames, CFR, frame-count == source)
  - that work may be UNCOMMITTED in the worktree; commit it first.
- Remaining: step 4 storm-proof numbers, step 5 audio (PORT from native/audio-host -
  working WASAPI on this machine), step 6 swap in MAIN checkout (branch
  local/video-engine-swap off master; copy native\becky-review\becky-review.exe to
  becky-review-mpv-backup.exe BEFORE touching main.cpp; build via _build.bat).
- A Claude agent carries this with full context; if the session is fresh and that agent
  is gone, dispatch a new one with THIS file + HANDOFF-VIDEO-ENGINE.md + the spec.

## INFRASTRUCTURE ARMED (do not rebuild, just know it exists)
- Repo stall monitor (session Monitor): pings on every new commit, alerts at 20-min
  commit silence. Re-arm on session restart.
- X:\AI-2\fleet\PAUSE = lane switch. PRESENT now (Claude lane owns repo). "Becky Claude
  Deadman" schtask (2h) deletes it if commits stop; "Overnight Autopilot Guard" (10min)
  then revives the free fleet, whose order file becky-review-build-order.md has THIS
  mission prepended (2026-07-22 21:30 override). If Claude lane is alive: keep PAUSE.
- Fleet trap learned tonight: the autopilot's cycle scheduler picks "sonnet" mode itself
  and starts on its GENERIC job list unless the order override is respected - verify
  what it's building within 10 min of any unpause, kill + re-PAUSE if wrong.
- Rate-limit reality: Claude subagents + orchestrator + wakeup timers share ONE session
  limit and all die together (killed workers 3x these two nights). Resume dead agents
  via SendMessage (context survives); commits-every-20-min is mandatory for workers.

## JORDAN'S STANDING RULES FOR THIS MISSION
Sonnet 5 OAuth as backup when free APIs limit - never stop working. No new protocol
docs. ASCII-only C++. Never write to E:\ except transcript sidecars. Commit early/often,
branch -> FF master -> push (hook blocks direct master commits). High-contrast colors
stay. Done = driven + measured + screenshotted, never "compiles".

## IF CONTEXT/BUDGET DIES ENTIRELY
Everything needed to continue is: this file, HANDOFF-VIDEO-ENGINE.md (checkboxes show
exactly where the build stands), SPEC-BECKY-VIDEO-ENGINE.md, branch local/video-engine,
and git log --all since 2026-07-21. The mission is done when the acceptance numbers
above are measured true on Jordan's reel in the deployed, mpv-free app.
