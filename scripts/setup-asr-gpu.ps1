# setup-asr-gpu.ps1 - create the GPU ASR venv that runs Parakeet on the GPU.
#
# becky-transcribe runs the SAME Parakeet model it always has, but through
# onnx-asr + onnxruntime-directml (DirectML) - the approach the Rust app "Handy"
# uses. DirectML accelerates the int8 model on the GPU via DirectX 12 with NO
# CUDA / cuDNN setup (which is why Handy "just works"). Measured ~4-5x faster
# than the CPU path on an RTX 3070 (about 57x realtime): a 2-hour stream
# transcribes in ~2 minutes instead of ~15.
#
# becky-transcribe AUTO-DETECTS this venv (config.detectDMLTranscribePython) and
# prefers it; if it is absent it falls back to the proven sherpa CPU Parakeet, so
# nothing breaks either way. ASCII-only (PowerShell 5.1 safe).
#
# Usage:  powershell -ExecutionPolicy Bypass -File scripts\setup-asr-gpu.ps1

$ErrorActionPreference = "Stop"

$Repo = Split-Path -Parent $PSScriptRoot
$Venv = Join-Path $Repo "models\asr\venv-dml"
$Py = "C:\ProgramData\anaconda3\python.exe"   # any Python 3.10-3.12 base

if (-not (Test-Path $Py)) {
    $Py = (Get-Command python -ErrorAction SilentlyContinue).Source
}
if (-not $Py) { Write-Error "No base Python found (need 3.10-3.12)."; exit 1 }

if (-not (Test-Path $Venv)) {
    Write-Host "Creating venv at $Venv ..."
    & $Py -m venv $Venv
}
$VPy = Join-Path $Venv "Scripts\python.exe"

# This machine sets PIP_TARGET / PYTHONUSERBASE globally; clear them so installs
# land INSIDE the venv instead of the shared user site-packages.
$env:PIP_TARGET = ""
$env:PYTHONUSERBASE = ""

Write-Host "Installing onnx-asr + onnxruntime-directml + soundfile + huggingface_hub ..."
& $VPy -m pip install --upgrade pip
& $VPy -m pip install onnx-asr onnxruntime-directml soundfile huggingface_hub
if ($LASTEXITCODE -ne 0) { Write-Error "pip install failed"; exit 1 }

Write-Host "Pre-caching the Parakeet v3 (int8) model ..."
& $VPy -c "import onnx_asr; onnx_asr.load_model('nemo-parakeet-tdt-0.6b-v3', quantization='int8', providers=['CPUExecutionProvider']); print('Parakeet v3 cached')"

Write-Host ""
Write-Host "Done. becky-transcribe will now run Parakeet on the GPU via DirectML."
Write-Host "Verify:  becky-transcribe <audio-or-video> --verbose   (look for 'via DirectML')"
