# becky-video-preview — build notes

The native GPU video-preview sidecar (GUI-RULES.md §1, Phase 4). Decodes video
frames with the system `ffmpeg` and renders them on the GPU with **wgpu** (no
browser), frame-accurate, for the AI-NLE preview surface. The Go engine drives it
over the NDJSON/stdio seam (GUI-RULES.md §2).

## Build

```bash
cd native/video-preview
cargo build --release          # -> target/release/becky-video-preview.exe
```

Needs: Rust/cargo (built on 1.96), and `ffmpeg`/`ffprobe` on PATH at RUNTIME
(not at build time — we shell out to the binaries, we do NOT link libav*). On
Windows wgpu used the Vulkan backend on the RTX 3070; it also supports DX12.

`target/` is gitignored. Committed: `Cargo.toml`, `Cargo.lock`, `src/`, this file,
`.gitignore`.

## Verify (what was actually run)

```bash
cargo build --release                 # clean, 0 warnings
cargo test --release                  # 17 passed
cargo clippy --release -- -D warnings # clean
cargo fmt --check                     # clean
./target/release/becky-video-preview.exe --selftest   # PASS, exit 0
```

The `--selftest` proof (headless, GUI-RULES.md §3): generates a 640x480 testsrc
clip via ffmpeg, runs `video.open` + `video.frame` + `video.overlay` at t=1.0
through the real GPU path, and asserts each PNG exists, has the right dimensions,
is non-blank (color variance), and (overlay) contains the neon-green forensic bar.

Live seam round-trip also verified (pipe NDJSON in):
`ping`, `video.open`, malformed-JSON (errors, no crash), unknown-verb (errors),
`video.frame`, `video.overlay` — all return one response per id; `video.frame`/
`video.overlay` wrote real 31KB PNGs through Vulkan; the overlay returned the
frame-accurate timecode `00:00:01:15` for t=1.5s @ 30fps. The overlay PNG was
visually confirmed (timecode + caption over the testsrc bars).

## The seam protocol (stdio NDJSON)

One UTF-8 JSON object per line. **stdout is ONLY protocol; all logs go to stderr.**

- startup event: `{"type":"event","name":"ready","data":{"sidecar":"video-preview","version":"0.1.0"}}`
- in:  `{"type":"command"|"query","id":"<str>","name":"<verb>","args":{...}}`
- out: `{"type":"response","id":"<same>","ok":true,"data":{...}}` | `{"ok":false,"error":"..."}`

### Verbs

| verb | args | returns |
|------|------|---------|
| `ping` | — | `{pong,version}` |
| `video.open` | `{path}` | `{width,height,fps,durationSec,frames}` (via ffprobe) |
| `video.frame` | `{path,timeSec,out}` | seeks, decodes that frame, GPU-renders it, writes PNG to `out`; `{out,width,height,timeSec,backend}` |
| `video.overlay` | `{path,timeSec,out,text}` | same + forensic lower-third (running ORIGINAL timecode + caption); `{...,overlay,text,timecode}` |
| `video.window` | `{path}` | the on-screen window runs its own blocking event loop, so over stdio this returns a `launch` hint; the engine spawns a dedicated process: `becky-video-preview --window <path>` |

### Run modes

```bash
becky-video-preview              # NDJSON stdio server (the normal path)
becky-video-preview --selftest   # headless GPU proof, exit 0=PASS / 1=FAIL
becky-video-preview --window <p> # open the on-screen winit+wgpu window
```

Env overrides: `BECKY_FFMPEG`, `BECKY_FFPROBE` (default `ffmpeg`/`ffprobe`).

## What's full vs partial (honest)

- **`video.open`** — FULL. Real ffprobe; derives `frames` from duration*fps when
  the container omits `nb_frames`.
- **`video.frame`** — FULL + headless-verified. ffmpeg input-seek (`-ss` before
  `-i`, frame-accurate for the emitted frame) -> RGBA -> wgpu texture -> full-screen
  triangle (WGSL) -> offscreen render target -> readback (256-aligned rows) -> PNG.
- **`video.overlay`** — FULL for the bar + timecode + caption; text uses a built-in
  5x7 bitmap font. PARTIAL on glyph fidelity: see follow-ups. The overlay is
  composited into the RGBA buffer on the CPU before the GPU upload (the GPU still
  does the frame draw + readback) — a true GPU text shader is a follow-up.
- **`video.window`** — IMPLEMENTED, **Jordan-verified only** (no display in
  headless CI/cloud, so it can't be auto-verified). Real winit 0.30 +
  wgpu surface; arrow keys scrub +/-1 frame, Esc closes. Run via `--window <path>`.

## Documented follow-ups (faster / higher-fidelity paths)

1. **YUV->RGB in the shader.** Today ffmpeg decodes straight to RGBA (simple,
   correct). The faster real path for live scrubbing is to upload Y/U/V planes and
   convert on the GPU (the Gausian editor's approach) — less CPU, less bus traffic.
2. **Persistent decoder for fast scrubbing.** Today each frame is a fresh
   `ffmpeg -ss` seek+decode (real and correct, but one process per frame). A
   persistent piped decoder (or libav binding) would make scrubbing Vegas-fast.
3. **Real glyph crate for the overlay.** `glyphon`/`wgpu_text` give anti-aliased,
   proportional text but currently pin to an older wgpu (~26), which conflicts with
   this sidecar's wgpu 29 — hence the built-in bitmap font for now. Swap once they
   track wgpu 29.
4. **sRGB-aware color management.** The render target is `Rgba8Unorm` so the
   already-sRGB decoded bytes round-trip exactly (no double-encode). A
   color-managed pipeline (working in linear, encoding once) is the proper path if
   precise color is ever needed.

## Reference

Architecture modeled on the Gausian native editor (Rust egui+wgpu, GStreamer/FFmpeg
decode, YUV->RGB shaders + readback) per GUI-RULES.md §1/§8 — studied, not vendored.
