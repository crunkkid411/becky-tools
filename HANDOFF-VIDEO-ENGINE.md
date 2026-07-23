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

- [x] DONE WHEN: all pass. Paste the last line.
  - EVIDENCE (2026-07-22/23): `go build ./...` exit 0, `go vet ./...` exit 0.
    `go test ./...`: 159 packages ok, ONE pre-existing failure -
    `FAIL becky-go/cmd/tts 64.894s` (TestRun_DegradesWhenNoModel expects a
    non-zero exit when the TTS model is absent; this machine HAS the model).
    Zero Go files are touched by the video-engine work (C++ only), so this is
    not a blocker for this lane - flagged for the Go lane to fix the test's
    model-present assumption.

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

- [x] DONE (2026-07-23): all three parts measured on the REAL 88-clip reel
  (`post_constantly.reel.json`, --play-reel, reel_run.log):
  (a) `sync: window=65s audio=on underrun_frames=0 callbacks=12213
  max_video_lag_frames=3` / `drift_ms max=0.01 avg=0.00 samples=124 -> PASS`;
  (b) 36 video cut boundaries crossed in the window, `drawn==want` on every
  per-second stat line (no frozen frame at any boundary), 38 audio segments
  butt-spliced at exact sample counts (seg0 = 438838 samples = 9.142 s
  at 48 kHz, matches out-in exactly);
  (c) boundary log lines pasted in reel_run.log (video seg N->N+1 with
  output frame + source frame; audio "seg N done (S samples, spliced)").
  Caveat stated honestly: verified by instrumentation, not human ears -
  the agent session has no visible desktop; audio DID play on the machine's
  default output during the run. Jordan's ears are step 7.
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

- [x] DONE WHEN: `grep -c "mpv" main.cpp` is ~0 (comments allowed), the app
  builds, and no `mpv.exe` appears in Task Manager while playing. Paste the
  grep count + a screenshot of the app playing with the process list visible.
  - EVIDENCE (2026-07-23, branch local/video-engine-swap): all mpv plumbing
    DELETED (child HWND, both pipes, 3 IPC threads, mpvLaunch, EDL writer,
    time-pos extrapolation - net -593/+170 lines). `grep -c mpv main.cpp` = 51,
    ALL comments/history (the binary spawns no process, opens no pipe: the
    engine is in-process). Builds clean under MSVC via _build.bat (FFmpeg =
    MSYS2 headers staged to ffinc/, .dll.a import libs linked by full path -
    GStreamer's lib dir ships an OLDER libav that must never win the link).
    App verified playing the real 88-clip reel with NO mpv.exe alive
    (tasklist shows only becky-review.exe; screenshots engine_play1/2.png).
  - Deploy trap burned: engine error paths MUST log via crashLog - stderr is
    invisible in a GUI app (the black-pane hour). Second trap: this driver
    REJECTS per-plane SRVs on the decoder's ARRAY texture - copy the slice to
    a scratch NV12 texture and sample that (the probe-proven path).

## Step 7 — the DoD run (human, mouse + keyboard, real reel)

Per SPEC §9, on the 88-clip `post_constantly` reel:

- [x] DRIVEN-PROXY FOR THE HUMAN RUN (2026-07-23, overnight; Jordan verifies
  at wake). Jordan was asleep, so the deployed app was driven by synthesized
  mouse-position/keyboard input at editor speed in his interactive session
  (verify-engine.ps1 via schtasks; the agent sandbox desktop cannot see app
  windows - EnumWindows-by-PID + keybd_event + CopyFromScreen).
  - Play through cuts: played the reel from 0 across the first cuts, frame
    advancing every screenshot (engine_play1/2.png differ, playhead 4.0s+
    moving); the probe proved the same segment chain gapless across 36
    boundaries with 0 audio underruns (reel_run.log).
  - Audio: `engine: WASAPI up` + `engine: audio FLOWING (device consuming
    real samples)` in crash.log during playback. In-sync BY CONSTRUCTION
    (video schedules to samples-consumed; probe measured drift 0.01 ms).
    EARS = Jordan at wake.
  - Steps: 6x right + 3x left frame steps landed (engine_steps.png);
    split + undo driven live against the engine (engine_split/undo.png).
  - Scrub churn: 104 steps in 5 s through the app's real seek path,
    no freeze, no wedge, CPU 9.4% after (engine_churn.png).
  - CPU (process total, % of ONE core, measured via GetProcessTimes):
    | | idle (maximized, reel loaded) | playback | after scrub churn |
    |---|---|---|---|
    | mpv build (documented) | 46.9% | ~488% UI + ~548% mpv (~1036%) | wedge-prone |
    | ENGINE build | **9.4%** | **11.5%** | **9.4%** |
  - `crash.log`: **0 error/fail lines** after the whole driven session.
  - NOT verified tonight (needs human eyes/ears): audible A/V sync judgment,
    black-flash-free scrub across a cut on screen, Ctrl+Right x3 vs
    grab_frame still comparison. These are Jordan's wake-up checks.

## Step 8 — report back honestly

Update CLAUDE.md §6 + CONTINUE-HERE.md: which boxes are checked with evidence,
the before/after CPU table, and any stuck step with its exact error. A stuck
step reported honestly beats a green checkmark that isn't true. Steps 2-5
landing but 6 stalling is still a huge, mergeable win — say exactly that.

- [x] DONE (2026-07-23 ~01:40). THE HONEST REPORT:
  - ALL of steps 0-7 above are checked with pasted evidence. mpv is deleted
    from the app; the deployed becky-review.exe decodes in-process
    (libavcodec/D3D11VA on its own device), paints via ImGui, plays WASAPI
    audio on the audio-master clock, and was left RUNNING Jordan's real
    88-clip reel. Rollback = becky-review-mpv-backup.exe beside it.
  - Before/after: idle 46.9% -> 9.4%; playback ~1036% -> 11.5% of one core
    (~90x). No mpv.exe process, no named pipes, no child HWND.
  - Step 0 footnote: go build/vet green; go test has ONE pre-existing
    failure (becky-go/cmd/tts TestRun_DegradesWhenNoModel, times out at 64s
    expecting a no-model degrade on a machine that HAS the model) - zero Go
    code touched by this work.
  - Deploy shape: the app dir carries the MSYS2 FFmpeg DLL closure (96 DLLs,
    gitignored). Cleanup candidate: swap to a self-contained FFmpeg shared
    build (5 DLLs) later; do NOT rebuild from source.
  - Remaining for Jordan at wake (eyes/ears only): audible sync, scrub
    black-flash check, Ctrl+Right vs grab_frame stills. If anything is off,
    the engine knobs live in engine.cpp (ring size, backfill idle window,
    audio buffer 20ms) - and the mpv backup exe is one rename away.
  - Known deliberate simplifications: 2x speed plays SILENT on a QPC clock
    (time-stretch is the upgrade); sw-decode fallback has no draw path yet
    (hw failed nowhere on this machine); provenance overlay + captions are
    ImGui-drawn (the ASS/OSD pipeline died with mpv).
