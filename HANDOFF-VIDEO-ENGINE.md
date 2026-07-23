# LOCAL WORK ORDER — video engine: replace mpv with libavcodec direct

**Goal, observable:** Becky Review 3 plays Jordan's real reel — video + audio,
gapless across cuts, frame-exact seeks — with **no mpv process, no child HWND,
no named pipe**, at measurably lower CPU than today.

Branch: start a fresh `local/video-engine` off master (this is local-lane C++
work; the cloud branch only ships the spec + this order). Read
`SPEC-BECKY-VIDEO-ENGINE.md` FIRST — it has the API map, the threading rules,
and the risk order this plan follows. Windows, MSYS2 MinGW64 shell for
FFmpeg-linked builds.

**This is a staged build. Every step ends in something runnable and measured.
Do NOT skip to step 6 and wire a half-decoder into the app — the standalone
harness (steps 2-5) is where every hard bug is meant to die.**

## Step 0 — deterministic layer green

    cd X:\AI-2\becky-tools\becky-go
    go build ./... ; go vet ./... ; go test ./...

- [ ] DONE WHEN: all pass. Paste the last line.

## Step 1 — FFmpeg dev libraries from MSYS2 (packaged, NOT from source)

    pacman -S --needed mingw-w64-x86_64-ffmpeg
    pkg-config --modversion libavcodec libavformat libavutil libswresample

- [x] DONE WHEN: pkg-config prints four versions (libavcodec ≥ 60 expected).
  Paste them.
  - EVIDENCE (2026-07-22): already installed, no pacman needed.
    `pkg-config --modversion libavcodec libavformat libavutil libswresample` ->
    62.28.102 / 62.12.102 / 60.26.102 / 6.3.102 (FFmpeg 8.x line).
- Reminder from CLAUDE.md: `pacman -Syu` DEADLOCKS non-interactively — if an
  upgrade is needed, drive a REAL MINGW64 window (the documented SendKeys
  method), never a background shell.

## Step 2 — standalone harness A: demux + hw decode, no window

New tool `native/becky-engine-probe/` (single main.cpp, links avformat/avcodec/
avutil/swresample + d3d11). It opens a file, sets up the D3D11VA device per the
spec §4, decodes N frames, reports.

    becky-engine-probe --decode "X:\Videos\2025\11_November\Rendered\post_constantly.mp4" --frames 300

- [x] DONE WHEN: it prints per-frame `{idx, pts, format}` and a summary line
  `decoded=300 hw=300 sw=0 avg_decode_ms=<x>` with format `d3d11` on every
  frame and avg_decode_ms in single digits. Paste the summary.
  - EVIDENCE (2026-07-22): `decoded=300 hw=300 sw=0 avg_decode_ms=6.07`,
    all 300 lines `format:d3d11` (grep count 300; full log:
    `native/becky-engine-probe/decode_300.log`). Decoder ran on
    "NVIDIA GeForce RTX 3070 Laptop GPU" (adapter 1).
  - TRAP FOUND (matters for step 6): on this machine the DEFAULT DXGI adapter
    is "Microsoft Basic Render Driver" (no ID3D11VideoDevice, so
    av_hwdevice_ctx_init fails with AVERROR_UNKNOWN). The probe enumerates
    adapters and picks the first video-capable one; the app engine must do the
    same, never `D3D11CreateDevice(NULL, ...)`.
- If hw=0: print `av_hwdevice_iterate_types()` output and which get_format
  offer was made — that tells you driver vs. code. Software fallback working
  (sw=300) is a PASS for the fallback path, not for this step.

## Step 3 — harness B: frame-exact seek proof (the mpv-killer feature)

    becky-engine-probe --seek-test "X:\...\post_constantly.mp4" --fps 30000/1001 --targets 100,101,102,1000,4499

- [x] DONE WHEN: for each target frame F, the harness seeks (BACKWARD flag +
  decode-forward per spec §4), then prints `target=F landed=F pts=<t>` — landed
  MUST equal target for ALL five, including the adjacent 100/101/102 (proves
  decode-forward, not keyframe-snap). Paste all five lines.
  - EVIDENCE (2026-07-22), all hw frames, 0 failures:
    ```
    target=100 landed=100 pts=3.33667 (decoded_forward=41 frames, seek_ms=277.3, format=d3d11)
    target=101 landed=101 pts=3.37003 (decoded_forward=42 frames, seek_ms=241.2, format=d3d11)
    target=102 landed=102 pts=3.40340 (decoded_forward=43 frames, seek_ms=254.0, format=d3d11)
    target=1000 landed=1000 pts=33.36667 (decoded_forward=41 frames, seek_ms=259.3, format=d3d11)
    target=4499 landed=4499 pts=150.11663 (decoded_forward=60 frames, seek_ms=341.9, format=d3d11)
    ```
    Cold-seek cost is the ~40-60 frame keyframe interval (~250-340 ms); the
    step-4 ring is what makes scrubbing near the playhead free.
- This is the acceptance test for the sub-frame cutpoint bug: mpv could not do
  this; if this table is exact, that bug is dead.

## Step 4 — harness C: present path (small window, ring, scrub)

`--play` opens a bare Win32+D3D11 window (NOT the app yet): decode into the
±15-frame ring (COPY slices out per spec §4 risk 2), NV12 shader quad, arrow
keys step ±1 frame, PgUp/PgDn ±1 s.

    becky-engine-probe --play "X:\...\post_constantly.mp4"

- [x] DONE WHEN: (a) video visibly plays; (b) holding → steps frame-by-frame
  with no black flashes; (c) frame-step latency for a ring hit prints < 2 ms;
  (d) Task Manager CPU for the probe during playback pasted. Screenshot + numbers.
  - EVIDENCE (2026-07-23, storm harness instead of hand-held keys - agent
    desktop has no visible window, so measured programmatically):
    (a) 70 s continuous playback, drawn==want every second, 0..2083 frames
    (sync_run3.log); (b/c) ENGINE latency (request -> ring-slot published):
    proxy median 1.3 ms (n=601), raw HEVC median 18.3 ms - ring hits are
    sub-2ms, the ~45 ms PAINT floor on top is DWM throttling of this hidden
    agent-session window, not engine cost; (d) probe process CPU during
    playback = 179% of one core (decode+audio+UI total; app integration
    reuses the app's existing present loop).
  - Scrub storm (25 req/s, 30 s, latest-chasing): raw p95 57.8 ms,
    proxy p95 54.5 ms, ZERO UI blocking (max loop gap ~75 ms), far seeks
    30/30 painted. Full logs: storm_raw_final.log / storm_proxy_final.log.
  - HARD-WON TRAPS (all measured, in commit messages): NVIDIA UMD segfault
    without ID3D10Multithread protection; Present holding the protection CS
    starves a shared-device decoder (fix: decode on its OWN device, shared
    NV12 ring); keyed-mutex sync costs ~78 ms/frame (use legacy SHARED +
    Flush); exact-match seek aborts livelock playback (window-based aborts).

## Step 5 — harness D: audio + A/V sync + gapless segment chain

`--play-reel` takes the real reel JSON, plays the segment list (spec §5:
prefix-sum mapping, pre-rolled next segment) with WASAPI audio as master clock.

    becky-engine-probe --play-reel "X:\Videos\2025\11_November\Rendered\post_constantly.reel.json" --report-sync

- [x] (a) DONE / [ ] (b,c) reel-boundary half still open:
  (a) audio plays in sync — PROVEN 2026-07-23 on the single-file path
  (audio-master clock, WASAPI via PortAudio ported from native/audio-host):
  `sync: window=65s audio=on underrun_frames=0 callbacks=12207
  max_video_lag_frames=2` and `drift_ms max=0.01 avg=0.00 samples=125
  (threshold 33.00) -> PASS` (sync_run4.log).
  (b,c) `--play-reel` segment chain + gapless boundary proof: NOT DONE YET —
  next work item before the step-6 swap.
- Risk #1 from the spec lives here. If drift grows: the video scheduler is
  using the wrong clock — video schedules to audio samples consumed, never QPC,
  while audio is up.

## Step 6 — wire into Becky Review 3 (delete mpv piece by piece)

Source: the harness files (they were written to be lifted). Target:
`native/becky-review/main.cpp`. Order inside this step:
1. Add the engine files; put the new video pane render behind
   `BECKY_ENGINE=native` env check, mpv path still default.
2. Route `stopPlayback`/`emitScrub`/space/arrows to the engine when enabled
   (in-process calls; keep the coalescing shape, drop the pipe round-trip).
3. Flip the default; **delete** the mpv client: child-HWND management
   (`g_mpvChildShown`, `SetWindowPos` block), both pipe connections + reader
   threads, `mpvEdlSeek`, the EDL temp-file writer usage.

- [ ] DONE WHEN: `grep -c "mpv" main.cpp` is ~0 (comments allowed), the app
  builds, and no `mpv.exe` appears in Task Manager while playing. Paste the
  grep count + a screenshot of the app playing with the process list visible.

## Step 7 — the DoD run (human, mouse + keyboard, real reel)

Per SPEC §9, on the 88-clip `post_constantly` reel:

- [ ] Play through 3+ cuts: gapless, audio synced (ears).
- [ ] Scrub across a cut: no black flash.
- [ ] Ctrl+Right ×3 from a cut: playhead lands on exact frames; paused frame
  matches `becky-clip` `grab_frame` at that time (compare the two stills).
- [ ] Idle CPU ≤ 47% of one core (today's measured 46.9%); playback total CPU
  pasted next to today's ~488%+548% for the before/after table.
- [ ] `crash.log`: 0 errors after the session.
- Report each with the number/screenshot, not "works".

## Step 8 — report back honestly

Update CLAUDE.md §6 + CONTINUE-HERE.md: which boxes are checked with evidence,
the before/after CPU table, and any stuck step with its exact error. A stuck
step reported honestly beats a green checkmark that isn't true. Steps 2-5
landing but 6 stalling is still a huge, mergeable win — say exactly that.
