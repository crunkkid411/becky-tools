# becky visual assets + icons — research (2026-06-21)

Produced **locally** (HuggingFace API + becky's own tools + engineering judgment) after the
Anthropic API repeatedly 529'd on subagent spawn. This is the durable answer to "icons
should never be an issue" + "can we generate visual assets locally on 8GB?".

> **Honest status of becky-research:** I ran `becky-research` for this and it **degraded** —
> `"no search backend; live search skipped; no-model; 0 sources"`. becky-research is a
> deterministic ORCHESTRATOR SKELETON; its live-search backend and local synthesis model are
> **stubbed/unwired**. So becky cannot yet do autonomous deep research. Wiring it (a keyless
> search backend + the on-disk Qwen3-4B as the synthesis model) is a real, high-value
> self-sufficiency build — it's what makes becky independent of the flaky Anthropic API.
> Model facts below were gathered via the working **HuggingFace API** (`huggingface_hub` 1.18).

---

## 1. UI ICONS — a vector-pack problem, NOT a generation problem (deterministic, zero-VRAM)

**Root cause of the "square play button":** becky-canvas ALREADY embeds the Material Design
vector pack (`golang.org/x/exp/shiny/materialdesign/icons`, Apache-2.0) and uses it for the
dock. The transport/overlay bypassed it and drew unicode **font glyphs** ("▶","■","✓","✗") the
Go font lacks → empty tofu boxes. AI image-gen is the WRONG tool for crisp monochrome UI
glyphs (inconsistent at 18px); a vetted vector pack is right.

**Plan — finish `internal/canvasicons` (becky-icons) so a glyph can never sneak back in:**
- Expand the `iconSet` to cover EVERY control from the Material pack: play=`AVPlayArrow`,
  stop=`AVStop`, pause=`AVPause`, record=`AVFiberManualRecord`, save=`ContentSave`,
  load=`FileFolderOpen`, undo=`ContentUndo`, redo=`ContentRedo`, mute=`AVVolumeOff`,
  solo=`ActionStarRate`, metronome=`ImageTimer`, loop=`AVLoop`, add=`ContentAddCircle`,
  delete=`ActionDelete`, apply=`NavigationCheck`, reject=`NavigationClose`, zoom=`ActionZoomIn`.
- Route ALL controls (transport, toolbar, overlay) through `iconBtn(icons.X)` — no `material.H6(symbol)`.
- Gaps (DAW-specific glyphs Material lacks): fill from **Lucide** (ISC license — ship-safe),
  rendered in Gio via `oksvg`+`rasterx`, or pre-converted to the IconVG byte format
  `widget.NewIcon` expects (build-time codegen). Avoid GPL packs.
- **Regression guard:** a test asserting `loadIcons()` returns non-nil for every named control.

## 2. LOCAL IMAGE GENERATION on 8 GB (for ARTWORK — album art, textures, memes; NOT UI icons)

Backend: **stable-diffusion.cpp** (GGUF, llama.cpp-family, deterministic seeded, offline) is
the becky-aligned path — and **leejet** (its author) publishes GGUFs directly. ComfyUI (Jordan
has it) is viable but heavier/stateful; sd.cpp CLI is the cleaner deterministic shell.

**8GB-viable models (HF, by downloads, 2026-06-21):**
| Model (HF id) | Why |
|---|---|
| `leejet/Z-Image-Turbo-GGUF` | sd.cpp-native turbo, few-step, low-VRAM — **best default for 8GB** |
| `stabilityai/sdxl-turbo` (+ GGUF quants) | 1–4 step, fast, proven floor |
| `unsloth/Qwen-Image-2512-GGUF`, `city96/Qwen-Image-gguf` | Jordan's pick; higher quality; Q4 for 8GB |
| `city96/FLUX.1-schnell-gguf`, `leejet/FLUX.1-schnell-gguf` | high quality, 4-step; Q4_K fits 8GB |
| `unsloth/ERNIE-Image-Turbo-GGUF` | another turbo option |

**`becky-art` sketch:** `becky-art "prompt" --seed N --model <gguf> --steps 4 --out art.png`,
shelling sd.cpp; deterministic; offline at runtime; degrade-never-crash. Pair with an upscale
GGUF for final res. Editing/inpaint → Qwen-Image-Edit GGUF.

## 3. HYPERFRAMES on local models

Hyperframes-class web/animation generation leans on frontier reasoning; an 8GB local model
won't match it. Recommendation: keep hyperframes for when Claude drives it (cost-aware), and
use the §2 local image-gen for everyday static artwork. Jordan's 10yr pre-made asset library +
win32/AutoHotkey screenshot/automation are additional deterministic, zero-cost sources.

## Sources
HuggingFace model IDs above are authoritative (queried via `huggingface_hub` 1.18 on
2026-06-21). stable-diffusion.cpp: github.com/leejet/stable-diffusion.cpp. Material icons:
pkg.go.dev/golang.org/x/exp/shiny/materialdesign/icons. Lucide: lucide.dev (ISC). Web-cited
expansion pending Anthropic API recovery (becky-research backend stubbed — see top).
