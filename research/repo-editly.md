# mifi/editly

Source: https://github.com/mifi/editly | licence: MIT

Read for `shorts-user-feedback.md`: what step does this run that becky does not?
Written by a free model reading the source; the build/skip judgement is not its call.

---

## What it is
Editly is a declarative non-linear video editor that takes a JSON specification (or CLI shorthand) describing clips, layers, transitions, and audio, and streams the rendered output directly to ffmpeg without writing intermediate video files. It emits a single MP4, MKV, or GIF file.

## The pipeline, in order
1. **CLI / API entry** – `src/cli.ts` parses flags or a JSON/JSON5 spec into a `ConfigurationOptions` object; `src/index.ts` `Editly()` is the main async function.
2. **Configuration normalization** – `src/configuration.ts` (imported as `Configuration`) validates, applies defaults, and computes derived fields.
3. **Spec parsing & asset probing** – `src/parseConfig.ts` (`parseConfig`) resolves each clip’s layers, probes video files with `ffprobe` (`src/ffmpeg.ts:readVideoFileInfo`) to get duration, dimensions, frame rate, rotation, and calculates per-layer `speedFactor` so that `cutFrom`/`cutTo` segments match the declared clip `duration`.
4. **Audio pre-pass** – `src/audio.ts:editAudio` runs *before* video rendering:
   - `createMixedAudioClips` extracts audio from each clip’s video/audio layers (respecting `cutFrom`/`cutTo` and `speedFactor`), mixes multiple layers per clip with `amix`, or generates silence.
   - `crossFadeConcatClipAudio` concatenates per-clip audio files with `acrossfade` using the clip’s transition duration and curves.
   - `mixArbitraryAudio` overlays user-supplied `audioTracks` (with `start`, `cutFrom`, `cutTo`, `loop`, `mixVolume`), applies optional `dynaudnorm` (gaussSize=5, maxGain=30) and global `outputVolume`.
5. **Output geometry & fps decision** – `src/index.ts` picks width/height/fps: explicit CLI/spec values > first video’s native values > defaults (640×640 @ 25 fps); `--fast` forces ~250px per dimension @ 15 fps.
6. **FFmpeg writer process spawn** – `startFfmpegWriterProcess` launches `ffmpeg -f rawvideo -pix_fmt rgba -s WxH -r FPS -i -` (plus audio input if present) piping stdin for raw RGBA frames.
7. **Frame source creation** – `src/frameSource.ts:createFrameSource` builds a per-clip async iterator (`readNextFrame({time})`) that composites all layers (video, image, text, canvas, fabric, GL, gradients, etc.) onto a Fabric.js `StaticCanvas` at the given timestamp.
8. **Main render loop** – `src/index.ts` `while` loop:
   - Reads next frame from current clip’s frame source (`frameSource1.readNextFrame`).
   - If within a transition window, also reads from next clip’s frame source (`frameSource2`) and runs the transition function (`transition.create({width,height,channels})` returning a blend function).
   - Writes resulting RGBA buffer to ffmpeg stdin.
   - Advances frame counters; on transition end, swaps frame sources and moves to next clip.
9. **Cleanup** – closes frame sources, ends ffmpeg stdin, waits for exit.

## Models, libraries and services
| What | Stage | Local / Paid API |
|------|-------|------------------|
| ffmpeg / ffprobe | Video/audio decoding, encoding, probing, filtering | Local binary (required in PATH) |
| fabric.js (via `fabric/node`) | Layer compositing, text, images, custom shapes | Local (npm) |
| node-canvas (`canvas`) | Backing bitmap for Fabric & custom canvas layers | Local (npm, native) |
| headless-gl | GL shader layer (`type: "gl"`) | Local (npm, native) |
| lodash-es | Utilities (`sortBy`, `flatMap`, etc.) | Local (npm) |
| p-map | Concurrency-limited mapping (audio extraction, frame source init) | Local (npm) |
| execa | Spawning ffmpeg/ffprobe | Local (npm) |
| json5 | Parsing spec files | Local (npm) |
| meow | CLI argument parsing | Local (npm) |
| compare-versions | ffmpeg version check | Local (npm) |
| fs-extra | File system ops | Local (npm) |
| file-type | MIME detection for CLI input files | Local (npm) |
| BoxBlur.js (embedded) | `contain-blur` resize mode (letterbox blur) | Local (embedded in `src/BoxBlur.js`) |

## Prompts
No LLM prompts found in the provided files.

## How it decides WHAT to clip
**It does not decide.** The user explicitly lists clips in order in the edit spec (`clips[]` array) or via CLI positional arguments. Each clip’s `layers[]` defines its visual/audio content. `cutFrom`/`cutTo` on video/audio layers select sub-segments; the clip’s `duration` then time-stretches that segment to fit. No scoring, ranking, or transcript-based selection exists.

## How it decides framing / cropping
Per-layer, via these properties (see `src/types.ts` `VideoLayer`, `ImageLayer`, `ImageOverlayLayer`):
- `resizeMode`: `"contain"` (letterbox), `"contain-blur"` (letterbox with blurred background via `BoxBlur.js`), `"cover"` (crop to fill), `"stretch"` (ignore AR).
- `width`, `height` (0–1 relative to output), `left`, `top` (0–1), `originX`, `originY` (`"left"|"center"|"right"`, `"top"|"center"|"bottom"`).
- `position` string shortcuts (`"top"`, `"bottom-right"`, etc.) resolved in `src/util.ts:getPositionProps`.
- Ken Burns: `zoomDirection` (`"in"|"out"|"left"|"right"`) + `zoomAmount` (default 0.1) computed in `src/util.ts:getZoomParams` / `getTranslationParams` and applied in the video/image frame sources (not shown in provided files but referenced).

## Multi-pass or iteration
- **Audio is a separate full pass** that completes before video rendering starts.
- **Video rendering is single-pass streaming**: frames are generated sequentially and piped to ffmpeg; no re-encoding or multi-pass rate control.
- Transitions are computed on-the-fly during the single render loop; no iteration or refinement.

## Steps here that a transcript-first clipper would MISS
- Explicit, frame-accurate `cutFrom`/`cutTo` on any video/audio layer with automatic speed adjustment to match declared `duration`.
- Ken Burns pan/zoom on static images with configurable direction and amount.
- Custom GLSL fragment/vertex shaders as first-class layers (`type: "gl"`).
- Arbitrary Fabric.js / Canvas 2D drawing code via `type: "fabric"` or `type: "canvas"` layers.
- Per-clip transitions with configurable name, duration, and audio crossfade curves (`audioOutCurve`, `audioInCurve`).
- `contain-blur` resize mode that fills letterbox bars with a real-time box blur of the frame (embedded `BoxBlur.js`).
- Multiple independent audio tracks with per-track `start`, `cutFrom`/`cutTo`, `loop`, `mixVolume`, plus global `dynaudnorm` and `outputVolume`.
- Picture-in-picture via `width`/`height`/`left`/`top`/`originX`/`originY` on any video/image layer.
- Declarative JSON spec that can be generated programmatically (no timeline UI required).

## Worth stealing
1. **Streaming architecture** – `src/index.ts` pipes raw RGBA frames directly to ffmpeg stdin; no intermediate clip files, low disk I/O.
2. **Frame source abstraction** – `src/frameSource.ts:createFrameSource` (not fully visible but invoked) isolates layer compositing logic per clip, enabling pluggable layer types.
3. **Transition system** – `src/transition.ts` (referenced) exports a `create({width,height,channels})` factory returning a blend function; transitions are pure frame-to-frame functions.
4. **Audio pre-pass with crossfade concat** – `src/audio.ts:crossFadeConcatClipAudio` builds an ffmpeg `filter_complex` that crossfades *between clips* using each clip’s own transition duration/curves.
5. **Declarative spec + CLI shorthand** – `src/cli.ts` accepts both a full JSON spec and a terse `title:'text' file.mp4 ...` syntax, lowering the barrier for quick edits.
6. **Embedded box blur for `contain-blur`** – `src/BoxBlur.js` (MIT licensed, original author Mario Klingemann) runs entirely on CPU in Node via `canvas` ImageData, no GPU dependency.
7. **Ken Burns math** – `src/util.ts:getZoomParams` / `getTranslationParams` cleanly separates scale/translation computation from rendering.
8. **License** – MIT (stated in repo description).
