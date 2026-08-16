# Copies becky's VEGAS Pro scripts into the VEGAS "Script Menu" folder.
# Runs elevated (launched that way by "Install Vegas Scripts.bat") because that
# folder is under C:\Program Files.
#
# ASCII ONLY. A double-clicked .bat runs Windows PowerShell 5.1, which reads a
# BOM-less .ps1 as the system ANSI codepage - one stray Unicode character and the
# whole script fails to PARSE with no visible error.

$ErrorActionPreference = 'Stop'

$source = Join-Path $PSScriptRoot 'vegas'

# Find every installed VEGAS Pro, newest first, rather than hardcoding 18.0.
$targets = @()
foreach ($root in @($env:ProgramFiles, ${env:ProgramFiles(x86)})) {
    if (-not $root) { continue }
    $vegasRoot = Join-Path $root 'VEGAS'
    if (-not (Test-Path $vegasRoot)) { continue }
    Get-ChildItem $vegasRoot -Directory -ErrorAction SilentlyContinue | ForEach-Object {
        $menu = Join-Path $_.FullName 'Script Menu'
        if (Test-Path $menu) { $targets += $menu }
    }
}

if ($targets.Count -eq 0) {
    Write-Host ''
    Write-Host '  Could not find a VEGAS "Script Menu" folder.' -ForegroundColor Red
    Write-Host '  Is VEGAS Pro installed?'
    Write-Host ''
    Read-Host '  Press Enter to close'
    exit 1
}

$scripts = Get-ChildItem $source -Filter '*.cs' -ErrorAction SilentlyContinue
if (-not $scripts) {
    Write-Host "  No .cs scripts found in $source" -ForegroundColor Red
    Read-Host '  Press Enter to close'
    exit 1
}

$copied = 0
foreach ($menu in $targets) {
    Write-Host ''
    Write-Host "  -> $menu" -ForegroundColor Cyan
    foreach ($s in $scripts) {
        $dest = Join-Path $menu $s.Name
        try {
            Copy-Item $s.FullName $dest -Force
            Write-Host ("     installed  " + $s.Name) -ForegroundColor Green
            $copied++
        } catch {
            Write-Host ("     FAILED     " + $s.Name + "  -  " + $_.Exception.Message) -ForegroundColor Red
        }
    }
}

Write-Host ''
Write-Host "  $copied file(s) installed." -ForegroundColor Green
Write-Host '  In VEGAS: Tools - Scripting - (the script name)'
Write-Host ''
Read-Host '  Press Enter to close'
