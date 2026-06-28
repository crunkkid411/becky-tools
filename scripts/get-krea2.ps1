# get-krea2.ps1 - download the Krea-2 image-generation models becky-imagegen uses.
#
# Krea-2 (FLUX.1 Krea-2) is becky's DEFAULT local text->image model, run on-device
# via stable-diffusion.cpp's sd-cli (docs/krea2.md). It has THREE pieces:
#   1. the Krea-2 diffusion transformer  (--diffusion-model)
#   2. the Wan 2.1 VAE                    (--vae)
#   3. Qwen3-VL-4B-Instruct text encoder (--llm)
#
# DEFAULT = Krea-2 *Turbo* Q4_K_M. This was verified against the live HF repos +
# docs/krea2.md on 2026-06-28:
#   - krea's OWN model card marks the "Raw"/"Base" weights "not recommended for
#     inference" (they are a fine-tuning foundation). Turbo is the distilled RELEASE
#     model (~8 steps), so it is the right default for actually making images.
#   - Q4_K_M (~7.2 GB) is the quality/size sweet spot for an 8 GB GPU; Q8_0 (~13.6 GB)
#     is overkill given sd-cli --offload-to-cpu (which saves VRAM at no speed cost).
#   - The realrebelai GGUF repo names the non-turbo folder "BASE" (= official "Raw"),
#     and the Qwen GGUF repo names files "Qwen3VL-..." (NO hyphen). The earlier
#     "Krea-2-Raw"/"Qwen3-VL" guesses 404'd - these are the real paths.
#
# LICENSE NOTE: the Krea-2 weights are under the **krea-2-community-license** (+ an
# Acceptable Use Policy), NOT apache-2.0 (the GGUF repo mis-tags it). That license
# follows the weights; mind the AUP if you publish or sell generated images.
#
# Files land FLAT in the dir config.ImageGen() looks in first:
#   X:\AI-2\becky-tools\models\krea2
#
# ASCII-only, no Unicode (PowerShell 5.1 safe). Uses the Hugging Face 'hf' CLI.
#
# Usage:
#   powershell -ExecutionPolicy Bypass -File scripts\get-krea2.ps1
#   powershell ... -File scripts\get-krea2.ps1 -StepUp        # Turbo Q6_K (bigger/nicer)
#   powershell ... -File scripts\get-krea2.ps1 -IncludeRaw    # + the Raw/Base experiment file
#   powershell ... -File scripts\get-krea2.ps1 -Force         # re-download existing

param(
    [string]$ModelDir   = "X:\AI-2\becky-tools\models\krea2",
    # Default diffusion transformer: Krea-2 TURBO Q4_K_M (the release model).
    [string]$TurboFile  = "TURBO/Krea-2-Turbo-Q4_K_M.gguf",
    # Optional quality step-up (-StepUp): Turbo Q6_K (~10.5 GB, near-lossless).
    [string]$StepUpFile = "TURBO/Krea-2-Turbo-Q6_K.gguf",
    # Optional Raw/Base experimentation file (-IncludeRaw); NOT for normal inference.
    [string]$RawFile    = "BASE/Krea-2-Base-Q4_K_M.gguf",
    [string]$VaeFile    = "split_files/vae/wan_2.1_vae.safetensors",
    # Text encoder: Qwen3-VL-4B-Instruct Q8_0 (runs once/image; the quality upgrade
    # over Q4_K_M is nearly free and protects prompt adherence).
    [string]$LlmFile    = "Qwen3VL-4B-Instruct-Q8_0.gguf",
    [switch]$StepUp,
    [switch]$IncludeRaw,
    [switch]$Force
)

$ErrorActionPreference = "Stop"

$DiffRepo = "realrebelai/KREA-2_GGUFs"
$VaeRepo  = "Comfy-Org/Wan_2.1_ComfyUI_repackaged"
$LlmRepo  = "Qwen/Qwen3-VL-4B-Instruct-GGUF"

$Hf = (Get-Command hf -ErrorAction SilentlyContinue)
if (-not $Hf) { $Hf = (Get-Command huggingface-cli -ErrorAction SilentlyContinue) }
if (-not $Hf) {
    Write-Error "Hugging Face CLI not found. Install with: pip install -U huggingface_hub"
    exit 1
}
$HfExe = $Hf.Source

if (-not (Test-Path $ModelDir)) {
    New-Item -ItemType Directory -Force -Path $ModelDir | Out-Null
}

# Download $RepoPath from $Repo and place it FLAT at $ModelDir\$FlatName. hf keeps
# the repo subfolders, so we fetch into a temp dir and move the single file out.
function Get-ModelFile($Repo, $RepoPath, $FlatName) {
    $Target = Join-Path $ModelDir $FlatName
    if ((Test-Path $Target) -and (-not $Force)) {
        $sizeMB = [math]::Round((Get-Item $Target).Length / 1MB, 1)
        Write-Host ("SKIP  {0} (exists, {1} MB)" -f $FlatName, $sizeMB)
        return
    }
    Write-Host ("GET   {0} <- {1}/{2}" -f $FlatName, $Repo, $RepoPath)
    $Tmp = Join-Path $ModelDir ".dl"
    if (-not (Test-Path $Tmp)) { New-Item -ItemType Directory -Force -Path $Tmp | Out-Null }
    & $HfExe download $Repo $RepoPath --local-dir $Tmp
    if ($LASTEXITCODE -ne 0) {
        Write-Error ("download failed for {0}/{1}" -f $Repo, $RepoPath)
        exit 1
    }
    $Src = Join-Path $Tmp ($RepoPath -replace "/", "\")
    if (-not (Test-Path $Src)) {
        Write-Error ("downloaded file not found at {0} - check the repo path" -f $Src)
        exit 1
    }
    Move-Item -Force -Path $Src -Destination $Target
    $sizeMB = [math]::Round((Get-Item $Target).Length / 1MB, 1)
    Write-Host ("OK    {0} ({1} MB)" -f $FlatName, $sizeMB)
}

Write-Host "Downloading Krea-2 image-gen models into $ModelDir"
Write-Host ""

# 1. The default diffusion transformer: Krea-2 Turbo Q4_K_M.
Get-ModelFile $DiffRepo $TurboFile "Krea-2-Turbo-Q4_K_M.gguf"
# 2. The Wan 2.1 VAE.
Get-ModelFile $VaeRepo  $VaeFile "wan_2.1_vae.safetensors"
# 3. The Qwen3-VL-4B text encoder (Q8_0).
Get-ModelFile $LlmRepo  $LlmFile "Qwen3VL-4B-Instruct-Q8_0.gguf"

if ($StepUp) {
    Write-Host ""
    Write-Host "Downloading the Turbo Q6_K quality step-up..."
    Get-ModelFile $DiffRepo $StepUpFile "Krea-2-Turbo-Q6_K.gguf"
}

if ($IncludeRaw) {
    Write-Host ""
    Write-Host "Downloading the Raw/Base experiment file (NOT for normal inference)..."
    Get-ModelFile $DiffRepo $RawFile "Krea-2-Raw-Q8_0.gguf"
}

# Clean up the temp download dir if empty.
$Tmp = Join-Path $ModelDir ".dl"
if (Test-Path $Tmp) { Remove-Item -Recurse -Force $Tmp -ErrorAction SilentlyContinue }

Write-Host ""
Write-Host "Done. becky-imagegen picks these up automatically (config.ImageGen() resolves them)."
Write-Host "Smoke test (sd-cli must be built/installed): becky-imagegen --prompt ""a lovely cat"" --out cat.png -v"
Write-Host "NOTE: verify the exact GGUF filenames against the repo listing if a GET fails -"
Write-Host "      pass -TurboFile / -LlmFile / -RawFile to match, or set the paths in ~/.becky/config.json."
