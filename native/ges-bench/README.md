# native/ges-bench — the video-engine bake-off (2026-07-03)

Standalone C harnesses that measured, on real footage + the RTX 3070, which engine can drive
Jordan's world-class timeline: **scrub + cut + manipulate a live multi-layer composite** faster
than any "good enough" NLE. Full context + the decision live in the memory
`native-nle-engine-bakeoff.md`. Becky Review is the good-enough holdover; this settles the
long-horizon best-performance build.

## The result

| engine (2-layer scrub, real footage) | fps | per-seek | verdict |
|---|--:|--:|---|
| GES (`ges_scrub.c`) | ~1 | 800 ms | DEAD — per-seek engine overhead, rebuilds its composition every seek (keyunit==accurate proves it's not decode -> proxies can't fix it) |
| 2xlibmpv (`../timeline-bench`, `--pip`) | ~20 | 50 ms | DEAD — two players contend |
| GStreamer `d3d11compositor` (`gst_scrub.c`) | — | — | DEAD for scrub — the aggregator won't forward a flush-seek across branches (same shared-coordination trap as GES) |
| **N independent d3d11 decoders (`gst_scrub_indep.c`)** | **2325** (3-layer: 1730) | **0.43 ms** | **WON** — each layer its own decoder, seeked independently, no shared aggregator |

`gst_compose.c` then proved CORRECTNESS: two independently-seeked d3d11 decoders composited into
one frame (layer A full + layer B PiP, alpha-blended) — a valid image from real footage.

**Architecture:** N independent NVDEC/d3d11 decoders -> own composite shader -> VRAM frame-ring ->
all-intra proxies (becky ScrubProxy) -> becky `editmodel` + ImGui/ImSequencer UI. libmpv stays the
single-source scrub surface; GES/MLT/Shotcut/Vegas are all measured-dead (see the memory).

## Build + run (MSVC 2022 BuildTools + installed GStreamer 1.28.4)

```
# each harness: cl against GStreamer's MSVC libs, then run with d3d11 GPU decode forced.
# _build*.bat (gitignored) call vcvars64 + cl; the run env sets:
#   PATH=<gstreamer>\bin;...   GST_PLUGIN_SYSTEM_PATH_1_0=<gstreamer>\lib\gstreamer-1.0
#   GST_PLUGIN_FEATURE_RANK=d3d11h264dec:512
gst_scrub_indep.exe <frames> <accurate|keyunit> <fileA> <fileB> [fileC...]   # the winning number
gst_compose.exe     <fileA> <fileB> <out.rgba> [seconds]                      # composite proof
```

Proxies for a fair scrub number are all-intra (every frame a keyframe):
`ffmpeg -ss T -t D -i <src> -an -c:v libx264 -x264-params keyint=1:min-keyint=1 -vf scale=-2:540 -r 30 proxy.mp4`

## Next (not built): the visual engine

Wire the proven independent-decoder core into a window: swapchain + the ImGui/ImSequencer timeline
+ becky `editmodel` (over NDJSON) + the GPU composite shader (shared d3d11 device across decoders).
Best built + verified on-screen with Jordan (his verify-on-real-input rule).
