# setup-picture-gpu.ps1 - create the venv that runs becky-livestream's picture
# check (face, mouth, body, gestures, drinks) on the graphics card.
#
# - insightface 2.1 on CUDA (onnxruntime-gpu with the CUDA 13 / cuDNN 9 pip
#   wheels; DirectML crashed in det_10g on this PC, CUDA does not).
# - MediaPipe's own networks through LiteRT (ai-edge-litert): the Windows
#   mediapipe wheel is built with GPU switched off, LiteRT runs the same .tflite
#   files on Direct3D 12 through WebGPU. That needs Microsoft's shader compiler
#   (dxcompiler.dll + dxil.dll) next to LiteRT's DLLs, downloaded below.
#
# becky-livestream AUTO-DETECTS this venv (config.detectPictureGPUPython); without
# it the picture check runs on the processor as before. ASCII-only (PowerShell 5.1).
#
# Usage:  powershell -ExecutionPolicy Bypass -File scripts\setup-picture-gpu.ps1

$ErrorActionPreference = "Stop"

$Repo = Split-Path -Parent $PSScriptRoot
$Venv = Join-Path $Repo "models\picture\venv-gpu"
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
# land INSIDE the venv. --isolated also skips the pip.ini extra index.
$env:PIP_TARGET = ""
$env:PYTHONUSERBASE = ""
$env:PYTHONPATH = ""

Write-Host "Installing insightface + onnxruntime-gpu (CUDA) + mediapipe + LiteRT ..."
& $VPy -m pip --isolated install insightface==2.1 "onnxruntime-gpu[cuda,cudnn]==1.31.0" mediapipe==1.0.1 opencv-python-headless ai-edge-litert==2.3.0
if ($LASTEXITCODE -ne 0) { Write-Error "pip install failed"; exit 1 }
# insightface pulls the processor-only onnxruntime, which shares onnxruntime-gpu's
# folder: remove it, then put the GPU build back on top.
& $VPy -m pip --isolated uninstall -y onnxruntime
& $VPy -m pip --isolated install --force-reinstall --no-deps onnxruntime-gpu==1.31.0
if ($LASTEXITCODE -ne 0) { Write-Error "onnxruntime-gpu reinstall failed"; exit 1 }

$Site = Join-Path $Venv "Lib\site-packages"
if (-not (Test-Path (Join-Path $Site "ai_edge_litert\dxcompiler.dll"))) {
    Write-Host "Downloading Microsoft DirectX Shader Compiler v1.9.2609 ..."
    $Tmp = Join-Path $env:TEMP "becky-dxc"
    New-Item -ItemType Directory -Force $Tmp | Out-Null
    $Zip = Join-Path $Tmp "dxc.zip"
    Invoke-WebRequest "https://github.com/microsoft/DirectXShaderCompiler/releases/download/v1.9.2609/dxc_2026_09_29.zip" -OutFile $Zip
    Expand-Archive $Zip -DestinationPath $Tmp -Force
    foreach ($d in @("ai_edge_litert", "mediapipe\tasks\c")) {
        Copy-Item (Join-Path $Tmp "bin\x64\dxcompiler.dll") (Join-Path $Site $d) -Force
        Copy-Item (Join-Path $Tmp "bin\x64\dxil.dll") (Join-Path $Site $d) -Force
    }
}

Write-Host "Checking the graphics card runs the networks ..."
& $VPy -s -c "import onnxruntime as o; o.preload_dlls(); assert 'CUDAExecutionProvider' in o.get_available_providers(); from ai_edge_litert.compiled_model import CompiledModel; print('CUDA + LiteRT ok')"
if ($LASTEXITCODE -ne 0) { Write-Error "the GPU check failed"; exit 1 }

Write-Host ""
Write-Host "Done. becky-livestream's picture check now runs on the graphics card."
