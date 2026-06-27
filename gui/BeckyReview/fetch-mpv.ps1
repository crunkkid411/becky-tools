# fetch-mpv.ps1 - install the mpv video runtime for Becky Review (git-ignored binaries).
#
# Downloads a prebuilt Windows mpv build (zhongfly/mpv-winbuild) and extracts the two
# files Becky Review needs into runtime\mpv\:
#   - mpv.exe       (Step 2: embedded video pane via the wid handle)
#   - libmpv-2.dll  (Step 8: the in-process render API)
#   - include\mpv\*.h + libmpv.dll.a (dev headers + import lib for the render API)
#
# No compiler needed. ASCII-only (Windows PowerShell 5.1 safe). Run from this folder:
#   powershell -ExecutionPolicy Bypass -File fetch-mpv.ps1
#
# Pinned to a known-good release for determinism; pass -Tag to use a different one.
param(
    [string]$Tag = "2026-06-27-70894ae039",
    [string]$Player = "mpv-x86_64-20260627-git-70894ae039.7z",
    [string]$Dev = "mpv-dev-x86_64-20260627-git-70894ae039.7z"
)

$ErrorActionPreference = "Stop"
$root = Split-Path -Parent $MyInvocation.MyCommand.Path
$rt = Join-Path $root "runtime\mpv"
$tmp = Join-Path $env:TEMP ("becky-mpv-" + [guid]::NewGuid().ToString("N"))
New-Item -ItemType Directory -Force -Path $rt, "$rt\include\mpv", $tmp | Out-Null

$base = "https://github.com/zhongfly/mpv-winbuild/releases/download/$Tag"

Write-Output "Downloading mpv runtime ($Tag)..."
Invoke-WebRequest -Uri "$base/$Player" -OutFile "$tmp\player.7z" -UseBasicParsing -TimeoutSec 600
Invoke-WebRequest -Uri "$base/$Dev" -OutFile "$tmp\dev.7z" -UseBasicParsing -TimeoutSec 600

# Need a 7z extractor that supports LZMA (Windows tar/bsdtar cannot). Grab tiny 7zr.exe.
$z = (Get-Command 7z -ErrorAction SilentlyContinue).Source
if (-not $z) { $z = "C:\Program Files\7-Zip\7z.exe" }
if (-not (Test-Path $z)) {
    $z = "$tmp\7zr.exe"
    Write-Output "Fetching 7zr.exe (LZMA extractor)..."
    Invoke-WebRequest -Uri "https://www.7-zip.org/a/7zr.exe" -OutFile $z -UseBasicParsing -TimeoutSec 120
}

Write-Output "Extracting..."
& $z x "$tmp\player.7z" "-o$tmp\player" -y | Out-Null
& $z x "$tmp\dev.7z" "-o$tmp\dev" -y | Out-Null

Copy-Item (Join-Path "$tmp\player" "mpv.exe") (Join-Path $rt "mpv.exe") -Force
Copy-Item (Join-Path "$tmp\dev" "libmpv-2.dll") (Join-Path $rt "libmpv-2.dll") -Force
Copy-Item (Join-Path "$tmp\dev" "libmpv.dll.a") (Join-Path $rt "libmpv.dll.a") -Force
Copy-Item (Join-Path "$tmp\dev" "include\mpv\*.h") (Join-Path $rt "include\mpv") -Force

Remove-Item -Recurse -Force $tmp -ErrorAction SilentlyContinue

Write-Output "Done. runtime\mpv now has:"
Get-ChildItem $rt -Recurse -File | ForEach-Object { "  {0,7:N1} MB  {1}" -f ($_.Length / 1MB), $_.Name }
& (Join-Path $rt "mpv.exe") --version | Select-Object -First 1
