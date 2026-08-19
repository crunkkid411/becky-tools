# alexandremendoncaalvaro/CorridorKey-Runtime

Source: https://github.com/alexandremendoncaalvaro/CorridorKey-Runtime | licence: NOASSERTION

Read for `shorts-user-feedback.md`: what step does this run that becky does not?
Written by a free model reading the source; the build/skip judgement is not its call.

---

## What it is
CorridorKey-Runtime is a native AI **keying/matting** runtime (green/blue screen removal) with an OFX plugin for DaVinci Resolve and Foundry Nuke, a CLI (`corridorkey` / `ck-engine.exe`), and a Tauri-based GUI. It takes a video file (and optional alpha hint) and emits a keyed output video with transparency. It is **not** a short-form clip selector or transcript-based editor.

## The pipeline, in order
1. **Hardware/Backend Detection** — `corridorkey doctor` / `refreshInfo()` in `src/gui/src/lib/engine.ts` (not fully visible; referenced by GUI store) probes TensorRT (RTX), DirectML, CPU fallback, MLX (Apple Silicon).
2. **Model Selection** — Quality ladder (Draft 512, High 1024, Ultra 1536, Maximum 2048) mapped to ONNX model packs (Green/Blue) via `model_inventory.json` and `artifact_manifest.json` (packaging scripts: `scripts/windows.ps1` tasks `certify-rtx-artifacts`, `regen-rtx-release`).
3. **ONNX Runtime / TensorRT Engine Build** — First-frame TensorRT compilation (10–30 s on Windows RTX) or MLX graph capture on macOS; backend chosen by VRAM/device capability.
4. **Frame-by-Frame Inference** — CLI `corridorkey process input.mp4 output.mp4 [--preset max] [--alpha-hint hint.mp4]` runs the selected model on each frame; GUI `ProcessFlow` component orchestrates the same via Tauri `invoke("process", …)` (implementation in `corridorkey_gui_lib` Rust crate, not in provided files).
5. **Video Encode** — Output written via FFmpeg (bundled; `CORRIDORKEY_FFMPEG_PATH` at package time) with user-selected encode mode (`videoEncodeMode` in job store).

*No clip selection, transcript analysis, or re-framing stages exist in this codebase.*

## Models, libraries and services

| Component | Stage | Local / Paid API |
|-----------|-------|------------------|
| ONNX Runtime | Inference backend (all platforms) | Local (bundled) |
| TensorRT (NVIDIA RTX 30+) | Windows RTX acceleration | Local (requires NVIDIA driver) |
| DirectML | Experimental Windows non-RTX path | Local (Windows 10+ DX12) |
| MLX / Metal | macOS Apple Silicon acceleration | Local (bundled via MLX Swift/ObjC bridge) |
| FFmpeg | Output encoding | Local (bundled in portable runtime) |
| CorridorKey model packs (Green/Blue) | Neural matte generation | Local `.onnx` files (int8 quantized, 512–2048 px) |
| Tauri 2.x | GUI desktop shell | Local (Rust + WebView) |
| OpenFX 1.4 | Host plugin interface | Local (host-provided) |

## Prompts
No LLM prompts found in the provided files. The project uses no LLMs.

## How it decides WHAT to clip
**Not applicable.** This is a keying tool, not a clip selector. It processes the entire input video frame-by-frame. No selection/ranking logic exists.

## How it decides framing / cropping
**It does not reframe or crop.** The model runs at fixed input resolutions (512/1024/1536/2048) with letterbox/pad-to-square preprocessing (inferred from quality ladder naming); output resolution matches input. No auto-reframe, face tracking, or crop logic is visible in the provided files.

## Multi-pass or iteration
**No multi-pass or iterative refinement.** Each frame is inferred once. TensorRT engine build is a one-time per-session cost. No re-check, no refinement loop.

## Steps here that a transcript-first clipper would MISS
- Hardware-aware backend selection (TensorRT / DirectML / MLX / CPU) with VRAM-aware quality ladder (`Auto` preset respects safe VRAM ceiling)
- First-frame TensorRT compilation cache (10–30 s warm-up) — critical for interactive OFX responsiveness
- Model certification pipeline (`certify-rtx-artifacts`, `regen-rtx-release`) that binds exact `.onnx` + `*_ctx.onnx` artifacts to a signed `artifact_manifest.json` — prevents silent model drift
- Alpha hint input (`--alpha-hint hint.mp4`) for guided keying on difficult edges
- OFX 1.4 host-agnostic plugin surface — same binary loads in Resolve **and** Nuke
- Portable runtime bundle (`package-runtime`) that carries CLI + GUI + models + FFmpeg without host install

## Worth stealing
1. **Quality ladder + VRAM-aware `Auto` preset** — `Draft (512) / High (1024) / Ultra (1536) / Maximum (2048)` with explicit fallback if a rung OOMs (README, `SUPPORT_MATRIX.md`).
2. **Model certification & manifest** — `scripts/windows.ps1` tasks `certify-rtx-artifacts` / `regen-rtx-release` produce `artifact_manifest.json` + `model_inventory.json`; packaging fails if manifests mismatch (prevents stale-model silent failures).
3. **OFX 1.4 single-binary dual-host plugin** — same `CorridorKey.ofx` loads in Resolve and Nuke (README "Supported Surfaces").
4. **Portable runtime bundle pattern** — `package-runtime` emits a self-contained ZIP (CLI + GUI + models + FFmpeg) that runs without installer or OFX host (README "Installation → CLI", `windows.ps1 -Task package-runtime`).
5. **Hardware doctor command** — `corridorkey doctor` emits machine-readable JSON (`--json` → NDJSON) for CI/automation gating (README "CLI Usage").
6. **License** — CC BY-NC-SA 4.0 (permits commercial video processing, forbids repackaging/selling the software itself).
