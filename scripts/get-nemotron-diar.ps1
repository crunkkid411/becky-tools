# get-nemotron-diar.ps1 - set up becky-diarize's speaker-diarization engine.
#
# becky-diarize (and so `becky-transcribe <file> --diarize`) runs NVIDIA's
# Nemotron-3-Diarization through NeMo-Speech.cpp's native nemo-speech.exe. This
# replaced the sherpa-onnx pyannote + CAM++ pipeline on 2026-09-24. Two pieces:
#   1. nemo-speech.exe, built from source: CPU backend, ASR + diarization only.
#      Release v0.1.0 (2026-08-19) predates Nemotron-3 support, so the source is
#      pinned to the main commit that added it. Needs VS 2022 Build Tools (C++),
#      CMake >= 3.26, Ninja and Git. NVIDIA's build script fetches sentencepiece
#      through vcpkg into %LOCALAPPDATA%\NeMoSpeech. It only changes PATH inside
#      its own process, never the saved one.
#   2. Nemotron-3-Diarization.q8_0.gguf (107 MB) from the pinned Hugging Face
#      revision, checked against the sha256 in NeMo-Speech.cpp's models/index.json.
#      The model is not gated; no Hugging Face token is needed.
#
# Files land where internal/config looks:
#   X:\AI-2\becky-tools\models\diar\nemo-speech\nemo-speech.exe (+ its DLLs)
#   X:\AI-2\becky-tools\models\diar\Nemotron-3-Diarization.q8_0.gguf
#
# ASCII-only (PowerShell 5.1 safe).
#
# Usage:
#   powershell -ExecutionPolicy Bypass -File scripts\get-nemotron-diar.ps1
#   powershell ... -File scripts\get-nemotron-diar.ps1 -Force    # rebuild + re-download

param(
    [string]$DiarDir  = "X:\AI-2\becky-tools\models\diar",
    [string]$Commit   = "97a15afa5caa9bce5baaa86c1184103877af4101",
    [string]$Revision = "f667ed73aee57d40cc39428eb768b4fd87a0a29e",
    [string]$Sha256   = "08456d9e22cd9a323c0364d98375f3746d6e68507ebb705cd46438c534c7a3a1",
    [int]$Jobs = 6,
    [switch]$Force
)

$ErrorActionPreference = "Stop"
$Src   = Join-Path $DiarDir "NeMo-Speech.cpp"
$Build = Join-Path $Src "build-cpu-asr"
$Bin   = Join-Path $DiarDir "nemo-speech"
$Exe   = Join-Path $Bin "nemo-speech.exe"
$Gguf  = Join-Path $DiarDir "Nemotron-3-Diarization.q8_0.gguf"
New-Item -ItemType Directory -Force -Path $DiarDir | Out-Null

# --- 1. The runtime -------------------------------------------------------------
if ($Force -or -not (Test-Path $Exe)) {
    if (Test-Path (Join-Path $Src ".git")) {
        git -C $Src fetch --depth 1 origin $Commit
    } else {
        git clone https://github.com/NVIDIA/NeMo-Speech.cpp $Src
    }
    if ($LASTEXITCODE -ne 0) { throw "could not get the NeMo-Speech.cpp source" }
    git -C $Src checkout --detach $Commit
    if ($LASTEXITCODE -ne 0) { throw "git checkout $Commit failed" }
    git -C $Src submodule update --init --depth 1 ggml llama.cpp
    if ($LASTEXITCODE -ne 0) { throw "git submodule update failed" }

    $buildScript = Join-Path $Src "scripts\windows\build.ps1"
    & powershell -NoProfile -ExecutionPolicy Bypass -File $buildScript -Backend cpu -AsrOnly -Jobs $Jobs -BuildDir $Build
    if ($LASTEXITCODE -ne 0) { throw "NeMo-Speech.cpp build failed ($LASTEXITCODE)" }

    New-Item -ItemType Directory -Force -Path $Bin | Out-Null
    Copy-Item -Force (Join-Path $Build "bin\*") $Bin
    Write-Host "runtime: $Exe"
} else {
    Write-Host "runtime already there: $Exe"
}

# --- 2. The model ---------------------------------------------------------------
function Test-Sha([string]$Path) {
    (Test-Path $Path) -and ((Get-FileHash $Path -Algorithm SHA256).Hash.ToLower() -eq $Sha256)
}
if ($Force -or -not (Test-Sha $Gguf)) {
    $url = "https://huggingface.co/nvidia/Nemotron-3-Diarization/resolve/$Revision/Nemotron-3-Diarization.q8_0.gguf"
    $part = "$Gguf.part"
    [Net.ServicePointManager]::SecurityProtocol = [Net.SecurityProtocolType]::Tls12
    $ProgressPreference = "SilentlyContinue"
    Invoke-WebRequest -Uri $url -OutFile $part -UseBasicParsing
    if (-not (Test-Sha $part)) {
        Remove-Item -Force $part
        throw "downloaded model does not match sha256 $Sha256"
    }
    Move-Item -Force $part $Gguf
    Write-Host "model: $Gguf (sha256 ok)"
} else {
    Write-Host "model already there: $Gguf (sha256 ok)"
}

# --- 3. Smoke check ---------------------------------------------------------------
& $Exe diarize --help | Out-Null
if ($LASTEXITCODE -ne 0) { throw "nemo-speech.exe does not run ($LASTEXITCODE)" }
Write-Host "OK: becky-diarize can now use Nemotron-3-Diarization."
