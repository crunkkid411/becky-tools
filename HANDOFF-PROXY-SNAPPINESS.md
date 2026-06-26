# HANDOFF-PROXY-SNAPPINESS.md — make scrubbing snappy (the real Shotcut-lag fix)

> **For the LOCAL agent (Jordan's Win10 PC).** Ordered, checkboxed work order. The
> cloud agent did the research; you carry out the steps and paste evidence. This is a
> *provable handoff* per `STANDARDS-WORKFLOW.md §7`: each step has a one-command proof.
>
> **Bottom line you are proving or disproving:** Shotcut (and any MLT editor) is laggy
> because it scrubs **long-GOP H.264/HEVC**, where every seek must decode a whole group
> of frames. The fix is **intra-frame proxies** (every frame stands alone) + **constant
> frame rate** (VFR sources make Shotcut "constantly recalculate the next frame"). This
> is codec-driven, not app-driven — it works in the Shotcut fork, kdenlive, and VEGAS
> alike. If this fixes it, we do NOT need to jump ship again.

## Why this is almost certainly the cause (read before starting)

The research found two independent things:
1. Shotcut's *default* proxy is plain long-GOP H.264 — "still not editing friendly,
   timeline scrubbing still very slow." Intra-frame (DNxHR LB / MJPEG / all-intra H.264)
   is the documented fix.
2. **becky's own `internal/reel/proxy.go` has the same flaw, twice over:**
   - It only builds a proxy when the source is **not** already web-safe H.264
     (`webSafeCodecs` short-circuit at `proxy.go:39`). So the most common evidence —
     long-GOP H.264 — gets **no scrub proxy at all**.
   - When it *does* build one, `proxyArgs` (`proxy.go:74`) uses `libx264 -preset
     veryfast` with the **default GOP** (~250 frames) — a long-GOP proxy, i.e. still
     slow to scrub.
   So becky is currently incapable of producing a scrub-friendly proxy. That is the bug.

---

## Step 0 — Capture the baseline (PROVE the problem first)

- [ ] Pick one real clip that scrubs badly in the Shotcut fork (a long camera/livestream
      H.264 or HEVC file). Note its path as `%SRC%`.
- [ ] Check whether it is variable-frame-rate (VFR) and its codec/GOP:
  ```bat
  ffprobe -v error -select_streams v:0 -show_entries stream=codec_name,avg_frame_rate,r_frame_rate -of default=nw=1 "%SRC%"
  ```
  **Proof to paste:** the codec name + whether `avg_frame_rate` ≠ `r_frame_rate` (that
  inequality = VFR, the frame-step lag culprit).
- [ ] Open `%SRC%` in the Shotcut fork, scrub/frame-step, and confirm the lag by eye
      (this is the "before").

## Step 1 — Make an intra-frame, constant-frame-rate proxy by hand

Build a low-res, every-frame-a-keyframe, CFR proxy. Three options; **DNxHR LB is the
most reliable scrubber**, all-intra H.264 is the smallest/most compatible. Pick one to
start (do DNxHR LB first):

- [ ] **DNxHR LB (recommended, .mov):**
  ```bat
  ffmpeg -y -i "%SRC%" -vf "scale=-2:540,fps=30" -c:v dnxhd -profile:v dnxhr_lb -pix_fmt yuv422p -c:a pcm_s16le "%SRC%.proxy.mov"
  ```
- [ ] **All-intra H.264 (.mp4, every frame a keyframe):**
  ```bat
  ffmpeg -y -i "%SRC%" -vf "scale=-2:540,fps=30" -c:v libx264 -preset veryfast -crf 20 -g 1 -keyint_min 1 -sc_threshold 0 -pix_fmt yuv420p -movflags +faststart -c:a aac "%SRC%.proxy.mp4"
  ```
- [ ] **MJPEG (.mov, weakest-hardware fallback, biggest files):**
  ```bat
  ffmpeg -y -i "%SRC%" -vf "scale=-2:540,fps=30" -c:v mjpeg -q:v 5 -pix_fmt yuvj420p -c:a pcm_s16le "%SRC%.proxy.mov"
  ```
  **Proof to paste:** `ffprobe` of the proxy showing the new codec + CFR (`fps=30`), and
  the output file size.

## Step 2 — Confirm the proxy scrubs smoothly (the decisive test)

- [ ] Open the proxy file in the Shotcut fork (and, if you want the comparison,
      kdenlive). Scrub and frame-step.
- [ ] **Proof to paste:** plain statement that scrubbing/stepping is now smooth on the
      proxy vs. laggy on the original. **This is the go/no-go.** If smooth → the
      hypothesis holds; proceed to wire it into becky. If still laggy → STOP and report;
      the cause is elsewhere (GPU driver, disk, the fork's preview path) and we
      re-diagnose before changing more code.

## Step 3 — Teach becky to build SCRUB proxies (code change)

Make becky able to produce intra-frame CFR proxies on demand. Keep the existing
web-preview proxy behavior intact; add a *scrub* profile beside it.

- [ ] In `becky-go/internal/reel/proxy.go`:
  - Add an exported `ScrubProxy(source, outDir string) (string, error)` (or add a
    `mode` param) that:
    - does **NOT** short-circuit on web-safe H.264 (long-GOP H.264 still needs a scrub
      proxy — drop/condition the `webSafeCodecs[codec]` early return for this mode);
    - writes `<stem>.scrub.mp4` (or `.mov`);
    - uses intra-frame CFR args. Add a pure, unit-tested `scrubProxyArgs(source, out)`
      mirroring `proxyArgs`, e.g. the all-intra H.264 recipe from Step 1:
      ```
      -y -hide_banner -loglevel error -i <src>
      -vf scale=-2:540,fps=30
      -c:v libx264 -preset veryfast -crf 20 -g 1 -keyint_min 1 -sc_threshold 0
      -pix_fmt yuv420p -movflags +faststart -c:a aac <out>
      ```
  - Optionally honor a `BECKY_PROXY_RES` / `BECKY_PROXY_CODEC` env so resolution/codec
    are tunable without a rebuild.
- [ ] Add a value-asserting test `TestScrubProxyArgs` (assert the argv contains
      `-g`, `1`, `-sc_threshold`, `0`, and the `fps=30` filter — NOT just "not empty").
      This is the regression test required by `STANDARDS-ENGINEERING.md` (every fixed
      bug ships a value-asserting test).
- [ ] `go build ./... && go vet ./... && go test ./... && gofmt -l .` all green.

## Step 4 — Use the scrub proxy in the NLE/preview path

- [ ] Wire whichever surface Jordan scrubs in to open the `.scrub` proxy for
      preview/timeline while keeping the **original** for any final export
      (frame-accurate export must use the source, never the proxy):
  - **Shotcut fork:** either enable Shotcut's own Proxy feature pointed at an
    intra-frame custom codec, OR have becky pre-generate `.scrub` files and point the
    dock at them. (Document which you chose in `HANDOFF-SHOTCUT-FORK.md`.)
  - **becky-clip (WebView2):** the all-intra **H.264** scrub proxy is web-playable, so
    `<video>` benefits directly — swap the preview to use `ScrubProxy`.
- [ ] **Proof to paste:** scrub the real clip *through becky* (not a hand-made file) and
      confirm it's smooth; paste the proxy path becky generated.

## DONE when

- [ ] A real laggy clip scrubs smoothly through becky's own proxy (Step 4 proof).
- [ ] `ScrubProxy` + `scrubProxyArgs` exist, with a value-asserting test; all five gates
      green; `build-all-tools.bat` builds.
- [ ] You've recorded in `HANDOFF-LOG.md` (top) whether the proxy fix **resolved the
      Shotcut lag** — because that answer decides whether we keep the Shotcut fork or
      move to one of the alternatives in `SPEC-BECKY-OTIO.md`.

## If it does NOT fix it (the honest branch)

If Step 2 shows the proxy still scrubs badly, the problem is not the codec. Likely
suspects, in order: GPU driver / hardware-decode path in the fork's MLT consumer; the
fork's preview widget repaint; disk throughput on the evidence drive. Report the Step 0/2
evidence and stop — do not keep changing proxy code against a non-proxy cause.
