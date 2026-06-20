# fetch-deps.ps1 - fetch the local-only SDKs for becky-audio-host.
# ASCII-only. Run from anywhere; it resolves paths relative to itself.
#
# Fetches (into ..\third_party, which is gitignored):
#   - VST3 SDK (MIT)        : steinbergmedia/vst3sdk (recursive submodules)
#   - PortAudio (MIT)       : PortAudio/portaudio
#   - nlohmann/json (MIT)   : single header json.hpp
#
# The Steinberg ASIO SDK is account-gated and is NOT fetched here. To enable ASIO,
# download it yourself and either set BECKY_ASIO_SDK to its extracted folder, or put
# the extracted folder at ..\third_party\asiosdk, then re-run build.ps1.

$ErrorActionPreference = "Stop"
$here = Split-Path -Parent $MyInvocation.MyCommand.Path
$root = Split-Path -Parent $here
$tp = Join-Path $root "third_party"
New-Item -ItemType Directory -Force -Path $tp | Out-Null

function Have($name) { return [bool](Get-Command $name -ErrorAction SilentlyContinue) }

if (-not (Have git)) { throw "git is required on PATH" }

# --- VST3 SDK ---
$vst3 = Join-Path $tp "vst3sdk"
if (Test-Path (Join-Path $vst3 "public.sdk\source\vst\hosting\module.h")) {
    Write-Host "[fetch] VST3 SDK already present."
} else {
    Write-Host "[fetch] cloning VST3 SDK (recursive, shallow)..."
    git clone --recursive --depth 1 https://github.com/steinbergmedia/vst3sdk.git $vst3
}

# --- PortAudio ---
$pa = Join-Path $tp "portaudio"
if (Test-Path (Join-Path $pa "CMakeLists.txt")) {
    Write-Host "[fetch] PortAudio already present."
} else {
    Write-Host "[fetch] cloning PortAudio (shallow)..."
    git clone --depth 1 https://github.com/PortAudio/portaudio.git $pa
}

# --- nlohmann/json single header ---
$nl = Join-Path $tp "nlohmann"
New-Item -ItemType Directory -Force -Path $nl | Out-Null
$jsonHpp = Join-Path $nl "json.hpp"
if (Test-Path $jsonHpp) {
    Write-Host "[fetch] nlohmann/json already present."
} else {
    Write-Host "[fetch] downloading nlohmann/json..."
    $url = "https://github.com/nlohmann/json/releases/latest/download/json.hpp"
    Invoke-WebRequest -Uri $url -OutFile $jsonHpp
}

# --- ASIO SDK note ---
if ($env:BECKY_ASIO_SDK -and (Test-Path $env:BECKY_ASIO_SDK)) {
    Write-Host "[fetch] ASIO SDK detected at BECKY_ASIO_SDK -> ASIO will be enabled."
} elseif (Test-Path (Join-Path $tp "asiosdk")) {
    Write-Host "[fetch] ASIO SDK detected at third_party\asiosdk -> ASIO will be enabled."
} else {
    Write-Host "[fetch] ASIO SDK NOT present (account-gated; download it yourself)."
    Write-Host "        Set BECKY_ASIO_SDK to its extracted folder for low-latency ASIO."
    Write-Host "        Without it the host uses WASAPI (works now on the UR12)."
}

Write-Host "[fetch] done."
