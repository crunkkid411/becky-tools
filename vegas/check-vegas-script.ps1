# Compile-checks a VEGAS Pro script WITHOUT opening VEGAS.
#
# Why this exists: a VEGAS script is a plain C# class compiled against
# ScriptPortal.Vegas.dll, and VEGAS only reports an error after you have opened a
# project and run it. One wrong character therefore costs a full VEGAS launch to
# discover. csc catches every syntax, name and type error in about a second.
#
# Run it after ANY edit to a .cs in this folder:
#
#   powershell -ExecutionPolicy Bypass -File vegas\check-vegas-script.ps1
#   powershell -ExecutionPolicy Bypass -File vegas\check-vegas-script.ps1 -Script vegas\BeckyReviewTimeline.cs
#
# ASCII ONLY (see CLAUDE.md - PowerShell 5.1 parses this as the ANSI codepage).

param([string]$Script = "")

$ErrorActionPreference = 'Stop'

if ([string]::IsNullOrWhiteSpace($Script)) {
    $Script = Join-Path $PSScriptRoot 'BeckyCaptions.cs'
}
if (-not (Test-Path $Script)) {
    Write-Host "  no such script: $Script" -ForegroundColor Red
    exit 1
}

$csc = @(
    "C:\Windows\Microsoft.NET\Framework64\v4.0.30319\csc.exe",
    "C:\Windows\Microsoft.NET\Framework\v4.0.30319\csc.exe"
) | Where-Object { Test-Path $_ } | Select-Object -First 1

if (-not $csc) {
    Write-Host "  csc.exe (.NET Framework 4) not found - cannot compile-check." -ForegroundColor Red
    exit 1
}

# Newest installed VEGAS wins; the scripting API is stable across versions.
$vegasDll = $null
foreach ($root in @($env:ProgramFiles, ${env:ProgramFiles(x86)})) {
    if (-not $root) { continue }
    $vegasRoot = Join-Path $root 'VEGAS'
    if (-not (Test-Path $vegasRoot)) { continue }
    Get-ChildItem $vegasRoot -Directory -ErrorAction SilentlyContinue |
        Sort-Object Name -Descending | ForEach-Object {
            $dll = Join-Path $_.FullName 'ScriptPortal.Vegas.dll'
            if (-not $vegasDll -and (Test-Path $dll)) { $vegasDll = $dll }
        }
}

if (-not $vegasDll) {
    Write-Host "  ScriptPortal.Vegas.dll not found - is VEGAS Pro installed?" -ForegroundColor Red
    exit 1
}

$out = Join-Path $env:TEMP 'becky-vegas-scriptcheck.dll'

$cscArgs = @(
    '/nologo', '/target:library', '/warn:0',
    "/out:$out",
    "/r:$vegasDll",
    '/r:System.dll', '/r:System.Core.dll', '/r:System.Drawing.dll',
    '/r:System.Windows.Forms.dll', '/r:System.Xml.dll',
    $Script
)

$result = & $csc $cscArgs 2>&1
if ($LASTEXITCODE -eq 0) {
    Write-Host "  COMPILE OK   $Script" -ForegroundColor Green
    Write-Host "  (against $vegasDll)"
    exit 0
} else {
    Write-Host "  COMPILE FAILED   $Script" -ForegroundColor Red
    $result | ForEach-Object { Write-Host "    $_" -ForegroundColor Yellow }
    exit 1
}
