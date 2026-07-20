# HANDOFF — Becky Review 3 (2026-07-20, ~05:15)

Read `CONTINUE-HERE.md` first for the standing rules. This is what's live, what
failed, and what to do next. Written for the agent replacing me.

## THE ONE THING I FAILED AT — do this first

Jordan asked TWICE for a **2-hourly VISUAL comparison**: screenshot Becky Review 3
next to `becky-review-native` and have a model LOOK at both and say what Review 3
still gets wrong. **I did not build that.** What I built (`X:\AI-2\fleet\bullshit-check.ps1`,
scheduled task `BeckyBullshitCheck`, every 2h) reads DOCS and CODE TEXT only — it
never takes a screenshot. That is not what he asked for. He was right to be angry.

**Build the real thing. The parts already exist — wire them together, don't reinvent:**
- `X:\AI-2\fleet\verify-shot.ps1` — screenshots a window AND sends TWO images to a
  FREE vision-model rotation (cohere command-a-vision → ollama minimax-m3 →
  openrouter llama-3.2-11b-vision:free → gemini-3-flash). It already takes a
  `-Reference <png>` arg and prompts the model to compare "current vs reference".
- `X:\AI-2\becky-tools\scripts\gui-diff.ps1` — launches both apps, closes the other
  before each shot (a fix from tonight — `CopyFromScreen` grabs whatever's on top),
  writes `becky-review-3.png` and `becky-review-native.png`.
- Compose: launch both → gui-diff shots → `verify-shot.ps1 -ShotPath review3.png
  -Reference native.png -Question "What does image 1 still get wrong vs image 2? List the 3 worst."`
  → append the answer to a dated file → register a `Register-ScheduledTask` every 2h,
  indefinite (see how `bullshit-check.ps1` self-registers `BeckyBullshitCheck`).
- FREE OR OAUTH ONLY. The vision rotation above is all free. Never a paid API.
- When it works, **retire or fold in `BeckyBullshitCheck`** so there aren't two
  half-overlapping critics.

## What is actually working (verified by me, on screen, tonight)

- **The freeze is gone.** Render / Render Selection / Save / Export / Ask were
  blocking the UI thread up to 300s. Now async. Measured: pressing Ctrl+Right
  during a Render Selection went from a **2,173ms** worst frame to **47ms**.
- **96 / 120** of his acceptance criteria done (was 69). Audit:
  `GUI-CHECKLIST-STATUS.md` (dated 2026-07-20, supersedes all earlier — an older
  copy was confidently wrong and caused a false "you lied" from the critic).
- Real **Segoe MDL2 icons** where self-evident (play/pause/skip/camera/save/open/
  runner), words where not. `BECKY_ICONS=0` forces a text fallback that was tested.
- **Clip colours in HIS order**: #14FF39 video 1, #00AEEF video 2, #DC143C video 3,
  assigned first-seen and NEVER reassigned (deleting video 2 can't recolour video 3).
- Library **cards** with middle-ellipsis names + tooltips (no more sliced filenames),
  a **folder chip** colour-coded by drive (a SAFETY feature — it caught an E:-vs-X:
  hazard live), an **ask-becky** chat panel, timeline **N clips · M:SS / − px/s +**.
- **Clicking a clip no longer moves the playhead** (his must-never-do), Undo/Redo
  **buttons** (not just keys), pause returns to playback start, Enter stops, search
  hits reachable by keyboard + right-click, a "most relevant" sort.
- **A finished, postable video exists**: `X:\Videos\2025\11_November\Rendered\
  post_constantly.captioned.mp4` — captions burned in (I pulled the t=130s frame
  and read "27 times a day" off it), audio intact, frame-exact.
- Captions: **202/34/7 → 149/4/1** (cues / one-word lines / stutters).

## Bugs I INTRODUCED and fixed (so you trust the pattern, not me)

- Gave ruler-pan a gesture id caption-move already owned → caption dragging silently
  died for hours. Caught by an agent driving the app, not by me reading code.
- `Ctrl+Shift+Z` did an UNDO not a redo (GetAsyncKeyState latch consumed by the Z
  handler). Both reversibility keys did the same thing.
- Adding the ±1f buttons pushed **Render off-screen**. Caught on a screenshot.
- Hand-drew a "running man" that looked like a slip-on-a-banana-peel. Should have
  delegated it; did, and it's a real font glyph now.

**The lesson, and the method that worked:** agents DRIVING the app with Win32
mouse/keyboard and reading their own screenshots caught every one of these; me
reading code did not. Keep doing that. Verify by behaviour (drive it, screenshot
the playhead value before/after), never by "it compiles".

## For Jordan, ready to use

- **Edit the caption LLM prompt in plain text**: `X:\AI-2\becky-tools\caption-prompt.txt`.
  Save, run captions again, no rebuild. becky-subtitle's JSON `"prompt"` field names
  the file it used, so he can confirm his edit took.

## Traps that will bite you

- Becky Review 3 loads **`becky-review-engine.exe`**, NOT `becky-clip.exe`. Both
  build from `cmd/clip`. If a Go change doesn't show up in the app, it's the stale
  alias — `build-all-tools.bat` builds it now, but check first.
- **LANDMINE (documented, not defused):** `apply_proposal`'s async callback mutates
  `g_track` from inside `drainAsync()`. Does NOT crash today only because of where
  the drain sits. If you touch `drainAsync` or the proposal path, fix the ordering
  FIRST, with a test.
- Output goes with the FOOTAGE (a `Rendered/` subfolder of the source), never the
  cwd, never a hardcoded drive. `E:` is a removable criminal-case evidence drive —
  his YouTube edits must never land there. Enforced by tests.

## Watchdogs running (scheduled tasks)

- `BeckyModelHeartbeat` — every 30 min, proves the free models answer, writes
  `X:\AI-2\fleet\model-heartbeat.json`.
- `BeckyBullshitCheck` — every 2h, the TEXT critic. **Replace with the visual one above.**

## Branch / state

Everything is pushed to `master` (66 commits tonight, tree clean). `cmd/tts`'s
`TestRun_DegradesWhenNoModel` fails — pre-existing, environmental, not ours; a TTS
model is installed so the "no model" degrade can't trigger.
