# becky-clip FIX-PLAN — make it actually work on real footage (2026-06-18)

> Shared brief + contract for the parallel agents fixing becky-clip. Read this whole
> file first. Authoritative background: `BECKY-CLIP-HANDOFF.md`, `SPEC-BECKY-CLIP.md`.

## The problem (root cause — CONFIRMED with hard evidence)

becky-clip is **entirely transcript-gated**. `internal/footage.Index` only sets
`HasTranscript=true` when a subtitle sidecar (`<stem>.srt/.vtt/.json3`) already sits
next to the video. There is **no way in the GUI to generate a transcript**, and **no way
to even play a video that lacks one** (preview only triggers from a search result or a
timeline clip — both require transcripts). The existing, working `becky-transcribe` ASR
tool was never wired in.

Result for Jordan's real 500GB (raw camera/phone video, NO `.srt` sidecars): every video
→ `has_transcript=false` → search greps nothing, clicking a video shows "no cues", preview
is unreachable, timeline never populates. The tool only ever "worked" on `demo-case/` — a
toy folder of hand-authored `.srt` next to SMPTE color-bar test clips.

### Evidence (already gathered, do not re-run)
- `becky-clip search demo-case "Penguin"` -> 2 hits. The whole loop works WHEN transcripts exist.
- A folder of 2 real videos with no `.srt` -> `has_transcript:false`, search count 0. Dead.
- `becky-transcribe.exe X:/AI-2/becky-tools/2-speakers-test.mp4 --format srt` -> 112 words,
  14 segments, exit 0 (Parakeet model present at the config default; sherpa_onnx imports).
- `reel.Proxy` already returns the original for web-safe codecs (h264/vp8/vp9/av1) and
  builds an H.264 proxy for anything else (HEVC/prores/...). Engine is sound.

## What "works today" means (the bar)
A detective opens a folder of RAW footage and can, BOTH by hand AND by asking becky:
1. See every video and **play any of them** (raw, no transcript needed) — incl. exotic codecs (HEVC) via proxy.
2. **Generate transcripts** with a click (per-video + "transcribe all"), using the real local Parakeet ASR, with progress feedback.
3. **Search** the transcripts (keyword) and get clickable results.
4. Click a result -> preview that exact moment; double-click -> add to the timeline.
5. See the **timeline**, toggle the forensic lower-third, and **export** a compilation MP4.
6. **Ask becky** in plain English ("find every time he offered money for the cat") and have it actually populate results / propose timeline actions (propose-then-apply).

## Architecture (so you don't re-derive it)
- The window (`cmd/clip/window_gui.go`, `//go:build gui && windows`) binds ONE JS function:
  `window.beckyCall(verb, argsJSON)` -> `App.Call` -> `dispatch()` (default-deny verb table in
  `bridge.go`) -> an `App` method. Reply is always the JSON envelope `{ok,data,error}`.
- A loopback HTTP server (`server.go`) serves the embedded UI (`assets/index.html|app.css|app.js`)
  and `GET /media?path=<abs>` (range-seekable, folder-scoped). `<video>` plays from `/media`.
- `App` (`app.go`) is cross-platform + window-free -> unit-testable + headless-smoke-testable:
  `becky-clip call <verb> [argsJSON]`, `becky-clip info <folder>`, `becky-clip search <folder> <q>`.
- Preview flow: JS `previewAt` -> `call("media_url",{source})` -> `mediaURLReply` -> `ProxyFor`
  (proxy if exotic) -> returns a `/media` URL -> `<video src>` + seek + play.

## INVARIANTS (do not break)
- Originals are sacred: never write a source VIDEO. Writing a NEW sidecar next to it
  (`<stem>.srt`, like the existing `<stem>.beckymeta.json`) is allowed and is how we persist transcripts.
- Offline + deterministic by default; degrade-never-crash (missing binary/model/ffmpeg -> typed
  error + partial result, never a panic).
- `go build ./...` and `go test ./...` (NO tags) MUST stay green (window code is build-tagged off).
- Offline tests only: no test may shell ffmpeg / call a model / hit the network. Put every such
  boundary behind a seam with a fake (see existing `pickFolderFn` pattern in app.go).
- Launchers stay ASCII-only. gofmt your new Go files (scoped).

## THE VERB CONTRACT (both agents code to this; do not deviate)
New/changed `beckyCall` verbs (backend owns `bridge.go` + `app.go`; frontend only CALLS these):

| verb | args | returns (data) | notes |
|------|------|----------------|-------|
| `transcribe` | `{name}` | `FolderView` (re-indexed) | run ASR on one video, write `<stem>.srt`, re-index. Long-running. |
| `transcribe_all` | `{}` | `{folder:FolderView, transcribed:int, failed:int, errors:[{name,error}]}` | transcribe every video lacking a transcript. |
| `reindex` | `{}` | `FolderView` | re-walk the open folder (after external changes). |
| `media_url` | `{source}` | `{url, note}` | UNCHANGED -- already exists; works for any indexed video (proxy for exotic). Frontend uses this to PLAY a raw video chip. |

`FolderView = {root, videos:[{path,name,has_transcript,date?,person?,location?,source_fps?}]}` (unchanged).

Everything else (`open_folder, pick_folder, transcript, search, add_clip, remove_clip, reorder,
set_overlay, timeline, export, grab_frame, save_reel, load_reel, ask, apply_proposal,
reject_proposal, set_online`) already exists -- see `bridge.go`. Do not rename them.

## Build / test / verify commands
```
cd becky-go
go build ./...                                  # MUST stay green (headless)
go test ./cmd/clip/... ./internal/footage/... ./internal/reel/... ./internal/assistant/...
CGO_ENABLED=0 go build -tags gui -o bin/becky-clip.exe ./cmd/clip   # the REAL window
go build -o bin/becky-clip-headless.exe ./cmd/clip                  # headless smoke
# headless end-to-end (proves backend without a window):
bin/becky-clip-headless.exe info  <folder>
bin/becky-clip-headless.exe call  transcribe '{"name":"<video>.mp4"}'
bin/becky-clip-headless.exe search <folder> <word>
```
Fixtures: real speech video `X:/AI-2/becky-tools/2-speakers-test.mp4`; working transcript
folder `X:/AI-2/becky-tools/becky-clip-work/demo-case`; `becky-transcribe.exe` is at
`becky-go/bin/becky-transcribe.exe` (resolve it next to becky-clip.exe, or `$BECKY_TRANSCRIBE`, or on PATH).

## Definition of Done (per agent -- VERIFY, don't claim)
Each agent returns: exact files changed, the commands it ran, and the OUTPUT proving its piece works.
No "should work" -- show the evidence. The orchestrator does the final GUI launch + screenshot.
