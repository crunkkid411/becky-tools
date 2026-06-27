# get-qwen35.ps1 - download the Qwen3.5-4B orchestrator GGUFs becky uses.
#
# Qwen3.5-4B (Unsloth) is becky's GENERATIVE orchestrator + ask-routing model AND
# the independent CROSS-FAMILY corroborator (a DIFFERENT family than Gemma-4, so an
# agreeing Qwen+Gemma watch is real corroboration, not Gemma echoing itself). It is
# IMAGE-CAPABLE via its OWN F16 mmproj - it is NOT a separate "Qwen3.5-VL" (no such
# model; the distinct heavy Qwen3-VL is only for a dedicated VL job). The pinned
# GGUF is the Unsloth UD-Q4_K_XL (Dynamic-2.0 quant), the exact file Jordan linked:
#   https://huggingface.co/unsloth/Qwen3.5-4B-GGUF
#
# Downloads into X:\HuggingFace\models\unsloth\Qwen3.5-4B-GGUF (where config.Qwen()
# looks first). Override the dir with -ModelDir.
#
# ASCII-only, no Unicode (PowerShell 5.1 safe). Uses the Hugging Face 'hf' CLI.
#
# Usage:
#   powershell -ExecutionPolicy Bypass -File scripts\get-qwen35.ps1
#   powershell ... -File scripts\get-qwen35.ps1 -IncludeQ4KM   # + the smaller Q4_K_M fallback
#   powershell ... -File scripts\get-qwen35.ps1 -Force         # re-download existing

param(
    [string]$ModelDir = "X:\HuggingFace\models\unsloth\Qwen3.5-4B-GGUF",
    [switch]$IncludeQ4KM,
    [switch]$Force
)

$ErrorActionPreference = "Stop"
$Repo = "unsloth/Qwen3.5-4B-GGUF"

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

# Download one file from the repo into $ModelDir. Skips an existing target unless -Force.
function Get-GgufFile($File) {
    $Target = Join-Path $ModelDir $File
    if ((Test-Path $Target) -and (-not $Force)) {
        $sizeMB = [math]::Round((Get-Item $Target).Length / 1MB, 1)
        Write-Host ("SKIP  {0} (exists, {1} MB)" -f $File, $sizeMB)
        return
    }
    Write-Host ("GET   {0} <- {1}" -f $File, $Repo)
    & $HfExe download $Repo $File --local-dir $ModelDir
    if ($LASTEXITCODE -ne 0) {
        Write-Error ("download failed for {0}/{1}" -f $Repo, $File)
        exit 1
    }
    $sizeMB = [math]::Round((Get-Item $Target).Length / 1MB, 1)
    Write-Host ("OK    {0} ({1} MB)" -f $File, $sizeMB)
}

Write-Host "Downloading Qwen3.5-4B orchestrator into $ModelDir"
Write-Host ""

# PINNED: the UD-Q4_K_XL (Dynamic-2.0) model + its F16 IMAGE mmproj.
Get-GgufFile "Qwen3.5-4B-UD-Q4_K_XL.gguf"
Get-GgufFile "mmproj-F16.gguf"

if ($IncludeQ4KM) {
    Write-Host ""
    Write-Host "Downloading the smaller Q4_K_M fallback..."
    Get-GgufFile "Qwen3.5-4B-Q4_K_M.gguf"
}

Write-Host ""
Write-Host "Done. becky picks these up automatically (config.Qwen() resolves UD-Q4_K_XL first)."
Write-Host "Used by: becky-ask routing, becky-scout proposer, becky-new-tool, and becky-validate --backend qwen35-local."
