# build.ps1 - configure + build becky-audio-host with the MinGW (g++) toolchain.
# ASCII-only. Run from anywhere; resolves paths relative to itself.
#
#   .\build.ps1            # fetch deps if missing, configure, build (Release)
#   .\build.ps1 -SelfTest  # ...then run becky-audio-host --selftest
#   .\build.ps1 -Clean     # wipe the build dir first
#
# Toolchain: g++ from C:\msys64\mingw64\bin (overridable via $env:BECKY_MINGW_BIN).
# Ninja + mingw32-make are found on PATH (Strawberry Perl ships both).

param(
    [switch]$SelfTest,
    [switch]$Clean
)

$ErrorActionPreference = "Stop"
$here = Split-Path -Parent $MyInvocation.MyCommand.Path
$root = Split-Path -Parent $here
$build = Join-Path $root "build"

# Resolve the MinGW toolchain.
$mingwBin = $env:BECKY_MINGW_BIN
if (-not $mingwBin) { $mingwBin = "C:\msys64\mingw64\bin" }
$gpp = Join-Path $mingwBin "g++.exe"
$gcc = Join-Path $mingwBin "gcc.exe"
if (-not (Test-Path $gpp)) { throw "g++ not found at $gpp (set BECKY_MINGW_BIN)" }
$env:PATH = "$mingwBin;$env:PATH"

# Ensure deps are present.
$moduleH = Join-Path $root "third_party\vst3sdk\public.sdk\source\vst\hosting\module.h"
if (-not (Test-Path $moduleH)) {
    Write-Host "[build] deps missing -> running fetch-deps.ps1"
    & (Join-Path $here "fetch-deps.ps1")
}

if ($Clean -and (Test-Path $build)) {
    Write-Host "[build] cleaning $build"
    Remove-Item -Recurse -Force $build
}
New-Item -ItemType Directory -Force -Path $build | Out-Null

# Pick a generator: prefer Ninja, fall back to MinGW Makefiles.
$gen = "Ninja"
if (-not (Get-Command ninja -ErrorAction SilentlyContinue)) {
    if (Get-Command mingw32-make -ErrorAction SilentlyContinue) {
        $gen = "MinGW Makefiles"
    } else {
        throw "neither ninja nor mingw32-make found on PATH"
    }
}
Write-Host "[build] generator: $gen"

Write-Host "[build] configuring..."
cmake -S $root -B $build -G $gen `
    -DCMAKE_BUILD_TYPE=Release `
    -DCMAKE_C_COMPILER="$gcc" `
    -DCMAKE_CXX_COMPILER="$gpp"
if ($LASTEXITCODE -ne 0) { throw "cmake configure failed" }

Write-Host "[build] building..."
cmake --build $build --config Release -j
if ($LASTEXITCODE -ne 0) { throw "cmake build failed" }

$exe = Join-Path $build "becky-audio-host.exe"
if (-not (Test-Path $exe)) { $exe = Join-Path $build "becky-audio-host" }
Write-Host "[build] OK -> $exe"

if ($SelfTest) {
    Write-Host "[build] running --selftest"
    & $exe --selftest
}
