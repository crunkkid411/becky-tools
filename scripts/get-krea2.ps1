# get-krea2.ps1 - download the Krea-2 image-generation models becky-imagegen uses.
#
# Krea-2 (FLUX.1 Krea-2) is becky's DEFAULT local text->image model, run on-device
# via stable-diffusion.cpp's sd-cli (docs/krea2.md). It has THREE pieces:
#   1. the Krea-2 diffusion transformer  (--diffusion-model)  Raw = quality default
#   2. the Wan 2.1 VAE                    (--vae)
#   3. Qwen3-VL-4B-Instruct text encoder (--llm)
#
# This downloads all three (plus the optional Turbo transformer) into the flat
# model dir where config.ImageGen() looks first:
#   X:\AI-2\becky-tools\models\krea2
# Override the dir with -ModelDir, or the exact GGUF filenames with the params below
# (verify them against the repo listing first - GGUF filenames can change).
#
# ASCII-only, no Unicode (PowerShell 5.1 safe). Uses the Hugging Face 'hf' CLI.
#
# Usage:
#   powershell -ExecutionPolicy Bypass -File scripts\get-krea2.ps1
#   powershell ... -File scripts\get-krea2.ps1 -IncludeTurbo   # + the Turbo variant
#   powershell ... -File scripts\get-krea2.ps1 -Force          # re-download existing

param(
    [string]$ModelDir   = "X:\AI-2\becky-tools\models\krea2",
    [string]$RawFile    = "BASE/Krea-2-Raw-Q8_0.gguf",
    [string]$TurboFile  = "TURBO/Krea-2-Turbo-Q8_0.gguf",
    [string]$VaeFile    = "split_files/vae/wan_2.1_vae.safetensors",
    [string]$LlmFile    = "Qwen3-VL-4B-Instruct-Q4_K_M.gguf",
    [switch]$IncludeTurbo,
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

# 1. The Krea-2 Raw diffusion transformer (the quality default).
Get-ModelFile $DiffRepo $RawFile "Krea-2-Raw-Q8_0.gguf"
# 2. The Wan 2.1 VAE.
Get-ModelFile $VaeRepo  $VaeFile "wan_2.1_vae.safetensors"
# 3. The Qwen3-VL-4B text encoder.
Get-ModelFile $LlmRepo  $LlmFile "Qwen3-VL-4B-Instruct-Q4_K_M.gguf"

if ($IncludeTurbo) {
    Write-Host ""
    Write-Host "Downloading the Krea-2 Turbo variant (fewer steps)..."
    Get-ModelFile $DiffRepo $TurboFile "Krea-2-Turbo-Q8_0.gguf"
}

# Clean up the temp download dir if empty.
$Tmp = Join-Path $ModelDir ".dl"
if (Test-Path $Tmp) { Remove-Item -Recurse -Force $Tmp -ErrorAction SilentlyContinue }

Write-Host ""
Write-Host "Done. becky-imagegen picks these up automatically (config.ImageGen() resolves them)."
Write-Host "Smoke test (after building sd-cli): becky-imagegen --prompt ""a lovely cat"" --out cat.png -v"
Write-Host "NOTE: verify the exact GGUF filenames against the repo listing if a GET fails -"
Write-Host "      pass -RawFile / -TurboFile / -LlmFile to match, or set the paths in ~/.becky/config.json."
