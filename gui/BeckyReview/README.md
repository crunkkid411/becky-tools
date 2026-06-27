# Becky Review

The one-window forensic video reviewer. becky finds the moments; you click one and
it plays instantly in a smooth, frame-exact player — all in a single window.

```
 ┌─ Becky Review (native WPF, .NET 8) ───────────────────────────────┐
 │  [ Pick folder... ]   [ search box ]            high-contrast      │
 │  ┌── LEFT: WebView2 (HTML, no server) ─┐ ┌── RIGHT: mpv video ──┐  │
 │  │  clip list / transcript search hits │ │  GPU decode, smooth   │  │
 │  │  click a hit -> seeks the player    │ │  frame-step + scrub   │  │
 │  └─────────────────────────────────────┘ └───────────────────────┘ │
 └────────────────────────────────────────────────────────────────────┘
        shells out to existing becky-*.exe (JSON in/out) - engine unchanged
```

## Run it

Double-click **`Open Becky Review.bat`** at the repo root (or the Desktop
**"Becky Review"** shortcut). First run builds the app, builds the folder-index
tool, and downloads the mpv video runtime; after that it opens instantly.

## How it works

- **LEFT pane = WebView2** loaded from the local `ui/` folder via
  `SetVirtualHostNameToFolderMapping` — a virtual `https://beckyreview.local/`
  origin with **no TCP server**. This is the AI-authorable, CDP-verifiable surface.
- **RIGHT pane = mpv** embedded as a native child window (`--wid`), driven over a
  JSON IPC pipe. Video is GPU-decoded (`--hwdec`) and frame-exact (`--hr-seek=yes`)
  and never goes through the browser — that path can't seek without a server.
- **The engine is reused, not rebuilt.** `becky-review-index` (a thin wrapper over
  `internal/footage`) lists a folder's videos + transcripts and ranks transcript
  cue hits offline (no DB, no model). The window just shells out for JSON.

## Use

1. **Pick folder...** -> the LEFT pane lists every video (with a transcript badge).
2. Type a term + Enter -> the LEFT pane lists ranked transcript cue hits, each with
   its exact in-point.
3. Click a hit -> the RIGHT pane jumps to that moment and plays.
4. Frame-step with the **frame** buttons (or Left/Right arrows); Space = play/pause.

## Files

| Path | What |
|---|---|
| `MainWindow.xaml[.cs]` | the two-pane window + folder/search/play wiring |
| `MpvPlayer.cs` | embeds + drives mpv over IPC (load/seek/frame-step) |
| `BeckyTools.cs` | resolves `becky-go/bin` and shells `becky-review-index` |
| `ui/index.html` | the LEFT pane UI (list, search, host bridge) |
| `runtime/mpv/` | the mpv binaries (git-ignored; `fetch-mpv.ps1` installs them) |
| `fetch-mpv.ps1` | downloads the pinned mpv build |

## Dev / verify hooks (env vars, off by default)

| Variable | Effect |
|---|---|
| `BECKY_REVIEW_FOLDER` | auto-load this folder on startup |
| `BECKY_REVIEW_SEARCH` | auto-run this search after loading |
| `BECKY_REVIEW_TEST_VIDEO` | load this clip into the player on startup |
| `BECKY_REVIEW_CDP_PORT` | expose the WebView2 over CDP for agent self-verification |

## Status

Steps 0-7 of `HANDOFF-BECKY-REVIEW-APP.md` are built + verified (walking skeleton,
folder index, search, click-to-play, the cat use case, CDP self-verify, transport).
Step 8 (the libmpv **render API** so becky draws its own playhead/markers + a
sprite-sheet thumbnail strip) is the remaining polish; the `--wid` embed already
plays + scrubs + frame-steps smoothly, so it is an enhancement, not a blocker.
