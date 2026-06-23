# get-gemma4-qat.ps1 - download the Gemma-4 QAT GGUFs becky-edit / becky-validate use.
#
# DEFAULT (always): the E4B-it QAT model (Unsloth UD-Q4_K_XL) + its BF16 mmproj.
#   This is the new default AVLM (research/gemma4-qat-upgrade.md). QAT = near-bf16
#   quality at 4-bit memory; the Unsloth dynamic quant recovers what a naive q4_0
#   throws away.
# ALTERNATE (-Include12B): the 12B-it QAT model + its BF16 mmproj (a tier up on
#   forensic reasoning + audio; selected at runtime with BECKY_AVLM_VARIANT=12b).
#
# ASCII-only, no Unicode (PowerShell 5.1 safe). Uses the Hugging Face 'hf' CLI.
#
# Usage:
#   powershell -ExecutionPolicy Bypass -File scripts\get-gemma4-qat.ps1            # E4B only
#   powershell -ExecutionPolicy Bypass -File scripts\get-gemma4-qat.ps1 -Include12B  # + 12B
#   powershell ... -File scripts\get-gemma4-qat.ps1 -Force                         # re-download existing

param(
    [switch]$Include12B,
    [switch]$Force
)

$ErrorActionPreference = "Stop"

# Resolve the model dir relative to the repo root (this script lives in scripts/).
$RepoRoot = Split-Path -Parent $PSScriptRoot
$ModelDir = Join-Path $RepoRoot "models\gemma4"

$Hf = (Get-Command hf -ErrorAction SilentlyContinue)
if (-not $Hf) {
    $Hf = (Get-Command huggingface-cli -ErrorAction SilentlyContinue)
}
if (-not $Hf) {
    Write-Error "Hugging Face CLI not found. Install with: pip install -U huggingface_hub"
    exit 1
}
$HfExe = $Hf.Source

if (-not (Test-Path $ModelDir)) {
    New-Item -ItemType Directory -Force -Path $ModelDir | Out-Null
}

# Download one file from a repo into $ModelDir, optionally renaming it. Skips an
# existing target unless -Force.
function Get-GgufFile($Repo, $File, $TargetName) {
    if (-not $TargetName) { $TargetName = $File }
    $Target = Join-Path $ModelDir $TargetName
    if ((Test-Path $Target) -and (-not $Force)) {
        $sizeMB = [math]::Round((Get-Item $Target).Length / 1MB, 1)
        Write-Host ("SKIP  {0} (exists, {1} MB)" -f $TargetName, $sizeMB)
        return
    }
    Write-Host ("GET   {0} <- {1}/{2}" -f $TargetName, $Repo, $File)
    & $HfExe download $Repo $File --local-dir $ModelDir
    if ($LASTEXITCODE -ne 0) {
        Write-Error ("download failed for {0}/{1}" -f $Repo, $File)
        exit 1
    }
    # If the repo path differs from the target name, move it into place.
    $Downloaded = Join-Path $ModelDir $File
    if (($Downloaded -ne $Target) -and (Test-Path $Downloaded)) {
        Move-Item -Force $Downloaded $Target
    }
    $sizeMB = [math]::Round((Get-Item $Target).Length / 1MB, 1)
    Write-Host ("OK    {0} ({1} MB)" -f $TargetName, $sizeMB)
}

Write-Host "Downloading Gemma-4 QAT AVLM into $ModelDir"
Write-Host ""

# DEFAULT: E4B-it QAT (UD-Q4_K_XL) + BF16 mmproj.
Get-GgufFile "unsloth/gemma-4-E4B-it-qat-GGUF" "gemma-4-E4B-it-qat-UD-Q4_K_XL.gguf"
Get-GgufFile "unsloth/gemma-4-E4B-it-qat-GGUF" "mmproj-BF16.gguf"

if ($Include12B) {
    Write-Host ""
    Write-Host "Downloading the 12B QAT alternate (large; verify VRAM on the 3070)..."
    Get-GgufFile "unsloth/gemma-4-12B-it-qat-GGUF" "gemma-4-12B-it-qat-UD-Q4_K_XL.gguf"
    # The 12B mmproj has the same name as E4B's, so store it under a distinct name
    # (config.go GemmaMMProj12B looks for mmproj-12B-BF16.gguf first).
    Get-GgufFile "unsloth/gemma-4-12B-it-qat-GGUF" "mmproj-BF16.gguf" "mmproj-12B-BF16.gguf"
}

Write-Host ""
Write-Host "Done. The tools pick these up automatically (config.go resolves QAT first)."
Write-Host "To use the 12B at runtime: set BECKY_AVLM_VARIANT=12b"
