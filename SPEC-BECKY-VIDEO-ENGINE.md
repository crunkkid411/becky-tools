# SPEC — becky video engine: libavcodec direct, mpv deleted

*Status: DECIDED direction (CONTINUE-HERE.md, 2026-07-20: Jordan — "mpv is a lazy
dev choice and inappropriate for an NLE"; the measurements agree). This spec is
the cloud half of the handoff: the architecture, the API map, and the risk
list. The build itself is Windows/GPU work — see `HANDOFF-VIDEO-ENGINE.md` for
the ordered work order. Nothing here re-opens the mpv-vs-native question.*

## 1. Why (the measured indictment of mpv)

All from CONTINUE-HERE.md's 2026-07-20 PM session, each measured, none a guess:

- Playback costs ~488% (UI) + ~548% (mpv) of a core with the GPU pinned at
  99.8% — architectural, not a bug (the idle-spin bug was separate and is fixed).
- mpv's `--wid` child HWND permanently conflicts with our swap chain under DWM
  (the 10x idle-CPU bug lived exactly there).
- Every seek is an IPC round-trip over a named pipe (the only reason scrubbing
  needed an async coalescing worker).
- Our playhead clock must be synced from mpv's reported `time-pos` — the sole
  source of the playhead jitter.
- The sub-frame cutpoint error Jordan reported lives in mpv's EDL playback; the
  reel data itself is frame-exact (verified against `post_constantly.reel.json`
  at true 29.97).
- No CPU access to decoded frames without decoding a second time.

Replacing mpv with our own decode+present path deletes all five at once.

## 2. What we build

One in-process playback engine inside `native/becky-review` (C++), built on:

| Piece | Choice |
|---|---|
| Demux | `libavformat` (`avformat_open_input` / `av_read_frame`) |
| Video decode | `libavcodec` with **D3D11VA hwaccel** (NVDEC-class hardware underneath; d3d11va is the vendor-neutral Windows path) |
| Software fallback | same decoder minus the hw device — a config/env switch, never a separate code path |
| Frame transport | `AV_PIX_FMT_D3D11` frames = `ID3D11Texture2D` — decoded straight into GPU memory, **zero copies to draw** |
| Present | NV12 → RGB in a tiny pixel shader, drawn as a textured quad **inside the app's existing swap chain** (the one the ImGui timeline already presents with) |
| Audio decode | `libavcodec` + `libswresample` → float stereo |
| Audio out | **WASAPI shared mode** (event-driven). No SDL, no extra dependency |
| Clock | the **audio clock is master** during playback (samples consumed ÷ sample rate); video schedules to it. Paused/scrubbing: video-only, the playhead IS the clock |
| Seeking | `av_seek_frame(..., AVSEEK_FLAG_BACKWARD)` to the keyframe at/before target, then **decode forward, discarding, until `pts >= target`** — the standard frame-exact seek, precisely what mpv's EDL playback does not give us |
| Scrub cache | a small **frame ring around the playhead** (decoded GPU textures, ~±15 frames) so short scrubs hit cache with zero decode latency |
| The reel | played **directly from the in-memory clip list** (source/in/out per clip) — the mpv EDL file, its temp-file dance, and its sub-frame rounding all go away. Segment N+1's first frames are **pre-opened/pre-rolled** near a boundary so cuts are gapless |

**What gets deleted when this lands:** the mpv child HWND + `SetWindowPos`
management, the JSON-IPC named-pipe client (both connections + reader threads),
the EDL writer (`timeline_edl` stays for any external consumer but the app stops
using it), the `time-pos` clock sync + extrapolation, and the seek worker's
reason to exist (an in-process seek is a function call; keep the coalescing
shape, drop the pipe).

## 3. Architecture

```
                    ┌────────────────────────────────────────────┐
 UI thread          │  frame loop: pick due frame from ring,      │
 (existing ImGui)   │  draw NV12 quad, draw timeline, Present     │
                    └──────────────▲─────────────────────────────┘
                                   │ lock-free ring of {frameIdx, ID3D11Texture2D, pts}
 decode thread      ┌──────────────┴─────────────────────────────┐
 (one per active    │  demux → send_packet → receive_frame (D3D11)│
  source, ≤2)       │  frame-exact seek; fills ring around target │
                    └──────────────▲─────────────────────────────┘
 audio thread       ┌──────────────┴─────────────────────────────┐
 (WASAPI event cb)  │  decode audio → swr → ring buffer → render  │
                    │  client; publishes the master clock         │
                    └────────────────────────────────────────────┘
```

Threading rules (these are the lessons already paid for in main.cpp):
- The UI thread **never blocks on decode** — if the due frame isn't in the ring
  it draws the last frame and moves on (the trace harness will show it).
- One decode thread per open source, max 2 (current + pre-rolled next segment).
- All cross-thread traffic through the ring + atomics; no engine calls from the
  frame loop (the `drainAsync` lesson).

## 4. API map (the exact calls, so local doesn't rediscover them)

Device: create the hw device FROM the app's existing D3D11 device so decoded
textures are usable without sharing gymnastics:

```c
AVBufferRef *hw = av_hwdevice_ctx_alloc(AV_HWDEVICE_TYPE_D3D11VA);
AVHWDeviceContext *hctx = (AVHWDeviceContext*)hw->data;
AVD3D11VADeviceContext *d3d = (AVD3D11VADeviceContext*)hctx->hwctx;
d3d->device = g_d3dDevice;            // the app's device; AddRef it
av_hwdevice_ctx_init(hw);             // NOT _create — we supply the device
codecCtx->hw_device_ctx = av_buffer_ref(hw);
codecCtx->get_format = pick_d3d11;    // return AV_PIX_FMT_D3D11 when offered
```

Decoded frame → texture: `frame->format == AV_PIX_FMT_D3D11`;
`(ID3D11Texture2D*)frame->data[0]` is an **array texture**, slice index
`(intptr_t)frame->data[1]`. Create two SRVs over that slice —
`DXGI_FORMAT_R8_UNORM` (luma) + `DXGI_FORMAT_R8G8_UNORM` (chroma) — and sample
both in the NV12→RGB shader. The decoder recycles its texture pool, so **copy
the slice** (`CopySubresourceRegion`) into a ring-owned texture before
releasing the frame; the ring copy is also what makes scrub-back free.
(One version-sensitive point for local to confirm against installed headers:
creating per-slice SRVs needs `D3D11_SRV_DIMENSION_TEXTURE2DARRAY`.)

Frame-exact seek (per source; times in that source's time_base):

```c
av_seek_frame(fmt, videoStream, target_ts, AVSEEK_FLAG_BACKWARD);
avcodec_flush_buffers(codecCtx);
while (receive_frame(...)) {
    if (frame->best_effort_timestamp + frame_dur > target_ts) break; // this is the frame
    // else discard and keep decoding forward
}
```

Frame index math stays in Go/reel land at the TRUE rate (30000/1001) — the
engine takes seconds, converts to stream timebase, and the frame it lands on is
compared by index, not float equality.

CPU pixels when needed (thumbnails, becky-validate, export checks):
`av_hwframe_transfer_data(swFrame, hwFrame, 0)` — copy-back exactly where
pixels are needed, never on the play path.

Audio: decode the audio stream of the active segment; `swr_convert` to
`AV_SAMPLE_FMT_FLT`, 48 kHz stereo; WASAPI shared, event-driven
(`IAudioClient::Initialize(AUDCLNT_SHAREMODE_SHARED, AUDCLNT_STREAMFLAGS_EVENTCALLBACK, ...)`).
The clip's `-c:a copy` render path is untouched — this is preview audio only.

## 5. The reel player (gapless cuts, no EDL)

- The engine's unit is the **segment list**: `{source, in, out}` in output
  order — handed over whenever the timeline changes (same data `load_reel`
  already holds; no new format).
- Output-time t → (segment, source-time) is a binary search over prefix sums —
  the same arithmetic `internal/subs` uses; it must live in ONE C++ function.
- Approaching a boundary (< ~0.5 s), the second decode thread opens the next
  segment's source (if different) and pre-rolls its first frames into the ring,
  keyed by output frame index; the frame loop never knows a cut happened.
- A cut is frame-exact BY CONSTRUCTION: segment N contributes exactly frames
  `[round(in*fps), round(out*fps))` — the sub-frame error class mpv's EDL had
  cannot exist here.

## 6. Degrade ladder (never crash, per house rule)

1. d3d11va init fails (old GPU, RDP, weird driver) → software decode into the
   same ring (upload via staging texture); slower, identical behavior.
2. WASAPI init fails → silent playback + a visible plain-language note (H-3
   pattern), playhead driven by QPC instead of the audio clock.
3. A corrupt packet / decode error → drop the frame, keep the last one on
   screen, log once to crash.log — never a black flash, never an exit.
4. An unopenable source → that segment renders as the existing placeholder
   color; the rest of the reel still plays.

## 7. Risks the work order must burn down FIRST (ordered by kill-probability)

1. **A/V sync + gapless audio across cuts** is the hardest 20% (mpv did this
   for us). Mitigation: audio-master clock; at a cut, crossfade-free butt splice
   at a sample boundary; measure drift over 60 s of playback (target < 1 frame).
2. **Decoder texture-pool lifetime** — holding `AVFrame`s too long stalls the
   decoder. The ring COPIES slices out immediately (Step 4's proof).
3. **Odd sources** (VFR phone clips, rotated metadata, non-4:2:0). The probe
   step classifies; VFR plays by pts (the ring is pts-keyed anyway); rotation is
   a shader matrix.
4. **Build/link on MSYS2-MinGW** — use the MSYS2 `mingw-w64-x86_64-ffmpeg`
   package first (same lesson as the Shotcut fork: don't build FFmpeg from
   source until a packaged build is proven insufficient). Link avformat,
   avcodec, avutil, swresample only.

## 8. Non-goals (so scope cannot creep)

- No filters, no scalers beyond NV12→RGB, no HDR tone mapping (SDR case files).
- No change to render/export — ffmpeg CLI renders stay exactly as they are.
- No cross-platform abstraction: this is Windows/D3D11 code in a Windows app.
- Audio effects/mixing: out of scope; preview volume only.

## 9. Definition of done (CANVAS-NORTH-STAR DoD applies)

Driven by mouse + keyboard on Jordan's real 88-clip reel: play through ≥3 cuts
gapless with audio; scrub across a cut with no black flash; Ctrl+Right lands on
the exact frame (paused frame == `becky-clip grab_frame` still at that time);
idle CPU no worse than today's 46.9%; playback total CPU **measurably below**
today's ~1000% combined; `crash.log` clean. Numbers pasted into
`HANDOFF-VIDEO-ENGINE.md`, not summarized.
