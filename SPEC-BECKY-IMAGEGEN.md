# SPEC — becky-imagegen — the DEFAULT local image generator (Krea-2 via stable-diffusion.cpp)

> ## STATUS — 2026-06-28 (cloud, `claude/default-local-image-gen-lyw127`)
> **Deterministic Go core BUILT + proven offline.** `becky-imagegen` is a new,
> single-purpose tool: a text prompt → a PNG on disk, generated entirely on-device
> by **stable-diffusion.cpp's `sd-cli`** driving **FLUX.1 "Krea-2"** — becky's new
> DEFAULT local image-gen model (per Jordan's link:
> https://github.com/leejet/stable-diffusion.cpp/blob/master/docs/krea2.md).
> Cloud finished its half (the model boundary is the only thing left for local):
> the Go wrapper, the config wiring, the argv builder, full value-asserting tests,
> the freshness-manifest entries, and the `get-krea2.ps1` downloader. The
> **one-command offline proof cloud already ran** is `becky-imagegen --selftest`
> (10/10 PASS). **Left for local** is §8: build/obtain `sd-cli` + download the three
> Krea-2 model files, then make a real PNG (the hardware "see it" gate).

---

## 1. What it is + why (the single-tool principle)

becky had **no image generation** before this — it is genuinely a new capability,
so it is a new tool, not a tangle added to an existing one (CLAUDE.md §1). It does
ONE thing: `prompt in → image file out → exit code`, the becky shape.

It is **generation only.** It does NOT touch — and must never be confused with —
becky's forensic vision *readers* (Gemma-4 / LFM2.5-VL / Qwen single-image). Those
look AT footage; this makes pictures. They share the Qwen family name by accident:
Krea-2 happens to use **Qwen3-VL-4B as its text encoder**, which is an internal
component of the generator, not a becky reader.

**Krea-2 has three local pieces** (docs/krea2.md). DEFAULT = **Turbo Q4_K_M** —
krea's own model card marks the Raw/Base weights *"not recommended for inference"*
(a fine-tuning foundation); **Turbo** is the distilled ~8-step RELEASE model, so it
is the right default. Q4_K_M (~7.2 GB) is the quality/size sweet spot for an 8 GB
GPU (Q8_0 ~13.6 GB is overkill with `--offload-to-cpu`). Verified vs the live HF
repos + krea2.md (2026-06-28). **LICENSE: krea-2-community-license + AUP, NOT
apache-2.0** (the GGUF repo mis-tags it) — mind the AUP for public/commercial use.

| Piece | sd-cli flag | Source (in-repo path) |
|---|---|---|
| Krea-2 diffusion transformer — **default `TURBO/Krea-2-Turbo-Q4_K_M.gguf`** (Raw/Base = opt-in experiment) | `--diffusion-model` | `realrebelai/KREA-2_GGUFs` (TURBO / BASE) |
| Wan 2.1 VAE | `--vae` | `Comfy-Org/Wan_2.1_ComfyUI_repackaged` → `split_files/vae/wan_2.1_vae.safetensors` |
| Qwen3-VL-4B-Instruct text encoder (Q8_0; no `mmproj` needed) | `--llm` | `Qwen/Qwen3-VL-4B-Instruct-GGUF` → `Qwen3VL-4B-Instruct-Q8_0.gguf` |

The verified-good invocation (cloud reproduced it into `BuildArgs`; default = Turbo):
```
sd-cli --diffusion-model Krea-2-Turbo-Q4_K_M.gguf --llm Qwen3VL-4B-Instruct-Q8_0.gguf \
       --vae wan_2.1_vae.safetensors -p "a lovely cat holding a sign says 'krea2.cpp'" \
       --steps 8 --diffusion-fa -v --offload-to-cpu
```

## 2. becky invariants this honours

- **Offline.** The only "AI in the loop" is one explicit local `sd-cli` call. No network at runtime.
- **Deterministic.** A **fixed default seed (42)** → same prompt + params + model → same image (`--seed -1` for a random one). This is becky's fixed-seed invariant.
- **Degrade, never crash.** A missing `sd-cli` / any of the three model files / a run that yields no output file → a typed `Result{Degraded:true, Error:...}` plain-language note and **exit 0** — never a panic.
- **No hardcoded paths.** Every path comes from `~/.becky/config.json` via `config.ImageGen()` (flags/env override). Config edit retargets the tool.
- **Windows-quiet.** Shells out via `proc.NoWindow` so a GUI launcher never flashes a console.

## 3. CLI surface

```
becky-imagegen --prompt "a lovely cat holding a sign that says 'becky'"
becky-imagegen --prompt "..." --out art.png --turbo --steps 8 --seed 7
becky-imagegen --prompt "..." --width 1024 --height 1024 --cfg-scale 1 --guidance 4.5
becky-imagegen --selftest          # OFFLINE, no-hardware argv proof (cloud ran this)
becky-imagegen --dry-run -p "..."  # print the sd-cli command, do not run it
becky-imagegen --prompt "..." --json
```
Defaults: Turbo variant, 1024x1024, seed 42, sampler `euler`, 8 steps (Turbo is
auto-detected from the model filename; Raw/Base = 28), `--cfg-scale 1`,
`--guidance 4.5`, `--diffusion-fa` on, `--offload-to-cpu` on.

## 4. Files

| File | Role |
|---|---|
| `becky-go/cmd/imagegen/main.go` | CLI: flags, `--selftest`, `--dry-run`, `--json`, plain report |
| `becky-go/internal/imagegen/imagegen.go` | deterministic core: `Options`, `Result`, `Generate`, `Plan`, `BuildArgs`, defaults, degrade |
| `becky-go/internal/imagegen/exec.go` | `newCmd` (proc.NoWindow exec) |
| `becky-go/internal/imagegen/imagegen_test.go` | value-asserting tests (argv, defaults, variant, every degrade path, happy path, Plan purity) |
| `becky-go/internal/config/config.go` | `SDCli` + `Krea2{Model,ModelTurbo,VAE,TextEncoder}` fields + `ImageGen()` resolver |
| `becky-go/internal/freshness/manifest.json` | `stable-diffusion-cpp` + `krea-2` dependency rows |
| `scripts/get-krea2.ps1` | downloads the three model files (Turbo default) into the config dir; `-StepUp` adds the Q6_K quality step-up, `-IncludeRaw` adds the Raw/Base experiment file |

## 5. Determinism + the model boundary

The Go layer is 100% deterministic and unit-tested without a GPU. The only thing
cloud cannot run is the actual diffusion pass (`sd-cli` + the multi-GB weights on
the RTX 3070). That is the model boundary handed to local in §8.

## 6. Gates (cloud half — all green)

- `go build ./...`, `go test ./...`, `go vet ./...`, `gofmt -l .` — all clean.
- `becky-imagegen --selftest` — **10/10 PASS** (the offline proof of the real argv path).
- `scripts/check-launchers.sh` — `get-krea2.ps1` is ASCII-only (PowerShell 5.1 safe).
- `build-all-tools.bat` auto-discovers `cmd/imagegen` — runs as local's gate #5.

## 7. Open decisions (tunable, not blocking)

Sampling params (steps / cfg-scale / guidance) are sane FLUX-class starting points,
NOT measured on Krea-2 output. They are flags + config-overridable. Tuning them is
a human "see it" judgement (Jordan looks at the image), like the other model-boundary
tools — record good defaults in `~/.becky/config.json` once dialled in.

---

## 8. LOCAL WORK ORDER (the provable handoff — drive to completion)

> Cloud proved the argv path offline (`--selftest` 10/10). This is the model
> boundary: get `sd-cli` + the Krea-2 weights on the PC and make ONE real image.
> Each step has its exact verify command. Do NOT merge-and-stop.

- [ ] **1. Build / obtain `sd-cli`** (stable-diffusion.cpp, CUDA build). Verify:
  `sd-cli --help` prints usage. Put its path in `~/.becky/config.json` as `"sd_cli"`
  (or rely on the default `C:\stable-diffusion.cpp\build\bin\Release\sd-cli.exe`).
- [ ] **2. Download the three model files (Turbo default):**
  `powershell -ExecutionPolicy Bypass -File scripts\get-krea2.ps1`
  (add `-StepUp` for the higher-quality Q6_K). If a `GET` fails, the GGUF filename
  changed — list the repo and pass `-TurboFile/-LlmFile/...` to match, or set the
  paths in `~/.becky/config.json` (`krea2_model`, `krea2_vae`, `krea2_text_encoder`).
  Verify: the three files exist in `X:\AI-2\becky-tools\models\krea2\`.
- [ ] **3. Confirm the resolved command without running it:**
  `becky-imagegen --dry-run --prompt "a lovely cat" --json`
  Verify: `args` lists your real `--diffusion-model` / `--vae` / `--llm` paths and
  `degraded:false` is implied (Plan never degrades).
- [ ] **4. Make a real image (the hardware "see it" gate):**
  `becky-imagegen --prompt "a lovely cat holding a sign that says 'becky'" --out cat.png -v`
  Verify: `ffprobe cat.png` (or open it) shows a **1024x1024 PNG**; report shows
  `(produced by becky-imagegen via local stable-diffusion.cpp krea-2-raw: ... , seed 42)`.
  Re-run the exact command → byte-identical/visually-identical (seed 42 determinism).
- [ ] **5. Try Turbo + tune:** `becky-imagegen --turbo --prompt "..."` (faster). If the
  defaults look off, find good `--steps/--cfg-scale/--guidance` and record them in
  `~/.becky/config.json`. Update §7 / this checkbox with the dialled-in values.
- [ ] **6. `build-all-tools.bat`** — confirm `becky-imagegen.exe` builds (auto-discovered).
- [ ] **7.** Append the finished entry to the TOP of `HANDOFF-LOG.md`; update CLAUDE.md §6.
