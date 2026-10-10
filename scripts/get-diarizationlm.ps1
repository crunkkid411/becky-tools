# get-diarizationlm.ps1 - fetch the model behind becky-diarfix (speaker double-check).
#
# becky-transcribe --diarize labels speakers from the SOUND (becky-diarize, Nemotron), then
# becky-diarfix re-reads the words near every speaker change with DiarizationLM-Gemma-4-E4B-v1
# (Google, Apache-2.0) and moves words that the wording says belong to the other person.
# This script downloads its Q4_K_M GGUF (5.3 GB), the file the model card recommends, from a
# pinned Hugging Face revision and checks its sha256. It runs on the llama-server becky already
# uses (config llama_server); nothing to build.
#
# Note: Google's own repo (google/DiarizationLM-Gemma-4-E4B-v1) was taken down after release;
# diarizers-community hosts the same card and files. The sha256 below is the file tested here.
#
# File lands where internal/config looks:
#   X:\AI-2\becky-tools\models\diar\diarizationlm-gemma4-e4b\DiarizationLM-Gemma-4-E4B-v1-q4_k_m.gguf
#
# ASCII-only (PowerShell 5.1 safe).
#
# Usage:
#   powershell -ExecutionPolicy Bypass -File scripts\get-diarizationlm.ps1
#   powershell ... -File scripts\get-diarizationlm.ps1 -Force    # re-download

param(
    [string]$Dir      = "X:\AI-2\becky-tools\models\diar\diarizationlm-gemma4-e4b",
    [string]$Repo     = "diarizers-community/DiarizationLM-Gemma-4-E4B-v1",
    [string]$Revision = "450e4b244dd2ac5ad0dcbaefa10123e9a46bf657",
    [string]$File     = "DiarizationLM-Gemma-4-E4B-v1-q4_k_m.gguf",
    [string]$Sha256   = "935b122a66c70b1e54fcaba2511910e5ff0a1b4713806d0448bab6e6fb9bb3d0",
    [switch]$Force
)

$ErrorActionPreference = "Stop"
New-Item -ItemType Directory -Force -Path $Dir | Out-Null
$Out  = Join-Path $Dir $File
$Part = "$Out.part"

if ($Force -or -not (Test-Path $Out)) {
    $url = "https://huggingface.co/$Repo/resolve/$Revision/$File"
    Write-Host "Downloading $File (5.3 GB, resumable) ..."
    # curl.exe ships with Windows 10+; -C - resumes a broken download, --retry rides out drops.
    & curl.exe -L -C - --retry 20 --retry-delay 5 --speed-limit 50000 --speed-time 60 -o $Part $url
    if ($LASTEXITCODE -ne 0) { throw "download failed ($LASTEXITCODE); run the script again to resume" }
    Move-Item -Force $Part $Out
}

$got = (Get-FileHash -Algorithm SHA256 $Out).Hash.ToLower()
if ($got -ne $Sha256) { throw "checksum mismatch for $Out (got $got); delete it and run again" }
Write-Host "OK: $Out"
