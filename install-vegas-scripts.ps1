# Copies becky's VEGAS Pro scripts into the VEGAS scripting menu.
#
# NO ADMINISTRATOR RIGHTS NEEDED (changed 2026-09-10). VEGAS reads scripts from
# SEVEN folders, not only the one under C:\Program Files - see VEGAS's own
# scripting FAQ, "1.10: How do I add a script to the Scripting menu?"
# (vegasprodata\VEGASScriptFAQ.html). The per-user one,
# Documents\Vegas Script Menu, needs no elevation at all.
#
# Two things measured on VEGAS Pro 18 on 2026-09-10, which is why this is safe:
#   1. Tools > Scripting lists ONE entry per script NAME, not one per copy.
#   2. The per-user copy WINS. A stale BeckyCut.cs that could not even compile
#      was sitting in Program Files at the time; after installing here, the menu
#      item ran the good copy.
#
# The old version of this script copied only into C:\Program Files, so it needed
# a UAC prompt every single time - and it filtered on '*.cs', which silently
# skipped .cs.config sidecars. That is how BeckyRoughCut ended up in the menu
# WITHOUT the assembly reference it needs, failing with "The type or namespace
# name 'Script' does not exist in the namespace 'System.Web'".
#
# ASCII ONLY. A double-clicked .bat runs Windows PowerShell 5.1, which reads a
# BOM-less .ps1 as the system ANSI codepage - one stray Unicode character and the
# whole script fails to PARSE with no visible error.

$ErrorActionPreference = 'Stop'

$source = Join-Path $PSScriptRoot 'vegas'
$menu = Join-Path ([Environment]::GetFolderPath('MyDocuments')) 'Vegas Script Menu'

# A script and its .cs.config sidecar are one unit - copy both or neither.
$scripts = Get-ChildItem $source -File -ErrorAction SilentlyContinue |
    Where-Object { $_.Name -like '*.cs' -or $_.Name -like '*.cs.config' }

if (-not $scripts) {
    Write-Host ''
    Write-Host "  No scripts found in $source" -ForegroundColor Red
    Write-Host ''
    Read-Host '  Press Enter to close'
    exit 1
}

New-Item -ItemType Directory -Force -Path $menu | Out-Null

Write-Host ''
Write-Host "  -> $menu" -ForegroundColor Cyan
$copied = 0
$failed = 0
foreach ($s in $scripts) {
    try {
        Copy-Item $s.FullName (Join-Path $menu $s.Name) -Force
        Write-Host ("     installed  " + $s.Name) -ForegroundColor Green
        $copied++
    } catch {
        Write-Host ("     FAILED     " + $s.Name + "  -  " + $_.Exception.Message) -ForegroundColor Red
        $failed++
    }
}

# Older installs put copies under C:\Program Files. Those are shadowed by the
# ones just written, so they are harmless - but say so, because a stale copy
# there is exactly what made BeckyCut look broken.
$stale = @()
foreach ($root in @($env:ProgramFiles, ${env:ProgramFiles(x86)})) {
    if (-not $root) { continue }
    $vegasRoot = Join-Path $root 'VEGAS'
    if (-not (Test-Path $vegasRoot)) { continue }
    Get-ChildItem $vegasRoot -Directory -ErrorAction SilentlyContinue | ForEach-Object {
        $old = Join-Path $_.FullName 'Script Menu'
        if (-not (Test-Path $old)) { return }
        foreach ($s in $scripts) {
            $p = Join-Path $old $s.Name
            if (Test-Path $p) {
                # Try to refresh it too. No elevation here, so this usually
                # fails - that is fine, the copy above already wins.
                try { Copy-Item $s.FullName $p -Force } catch { $stale += $p }
            }
        }
    }
}

Write-Host ''
Write-Host "  $copied file(s) installed." -ForegroundColor Green
if ($failed -gt 0) { Write-Host "  $failed file(s) FAILED." -ForegroundColor Red }
if ($stale.Count -gt 0) {
    Write-Host ''
    Write-Host "  Note: $($stale.Count) older copy/copies still sit under C:\Program Files." -ForegroundColor Yellow
    Write-Host '  They are IGNORED - VEGAS uses the ones just installed. Nothing to do.' -ForegroundColor Yellow
}
# ---------------------------------------------------------------------------
# The Becky Search panel (vegas\BeckyVegas): a VEGAS Application Extension DLL.
# It installs into Documents\Vegas Application Extensions - no admin, one of the
# seven folders VEGAS reads extensions from (scripting FAQ 4.2). VEGAS only
# loads extensions when it starts, and it locks the DLL while running, so this
# part is skipped (with a plain message) while VEGAS is open.
#
# It also retires the old VegasAIBridge.dll. That bridge never worked (every
# command ran on the wrong thread - see vegas\BeckyVegas\UiThread.cs) and it is
# what threw "failed to start HTTP server" popups. Nothing else uses it
# (checked 2026-09-13). It is MOVED, never deleted, to
# %LOCALAPPDATA%\BeckyVegas\retired - OUTSIDE every extension folder, because
# VEGAS also loads DLLs from subfolders (VEGASpython lives in one).
# ---------------------------------------------------------------------------
$extSource = Join-Path $PSScriptRoot 'vegas\BeckyVegas'
$extDll = Join-Path $extSource 'bin\BeckyVegas.dll'
$extDir = Join-Path ([Environment]::GetFolderPath('MyDocuments')) 'Vegas Application Extensions'
Write-Host ''
Write-Host '  Becky Search panel' -ForegroundColor Cyan
if (Get-Process -Name vegas180 -ErrorAction SilentlyContinue) {
    Write-Host '     VEGAS is open. Close VEGAS, then run this again to install the panel.' -ForegroundColor Yellow
} else {
    $newest = Get-ChildItem $extSource -Filter *.cs | Sort-Object LastWriteTime -Descending | Select-Object -First 1
    if (-not (Test-Path $extDll) -or ($newest -and $newest.LastWriteTime -gt (Get-Item $extDll).LastWriteTime)) {
        Write-Host '     building it...'
        & powershell -NoProfile -ExecutionPolicy Bypass -File (Join-Path $extSource 'build.ps1') | Out-Null
    }
    if (-not (Test-Path $extDll)) {
        Write-Host '     FAILED: the panel could not be built (Visual Studio Build Tools missing?).' -ForegroundColor Red
    } else {
        try {
            New-Item -ItemType Directory -Force -Path $extDir | Out-Null
            Copy-Item $extDll (Join-Path $extDir 'BeckyVegas.dll') -Force
            Write-Host '     installed  BeckyVegas.dll' -ForegroundColor Green
        } catch {
            Write-Host ('     FAILED     BeckyVegas.dll  -  ' + $_.Exception.Message) -ForegroundColor Red
        }
        $retired = Join-Path $env:LOCALAPPDATA ('BeckyVegas\retired\' + (Get-Date -Format 'yyyy-MM-dd_HHmmss'))
        $oldBridges = @(
            (Join-Path $extDir 'VegasAIBridge.dll'),
            (Join-Path $env:ProgramData 'VEGAS Pro\Application Extensions\VegasAIBridge.dll')
        )
        foreach ($old in $oldBridges) {
            if (-not (Test-Path $old)) { continue }
            try {
                New-Item -ItemType Directory -Force -Path $retired | Out-Null
                $tag = if ($old -like "$env:ProgramData*") { 'ProgramData' } else { 'Documents' }
                Move-Item $old (Join-Path $retired ('VegasAIBridge-from-' + $tag + '.dll')) -Force
                Add-Content (Join-Path $retired 'HOW-TO-PUT-BACK.txt') ('VegasAIBridge-from-' + $tag + '.dll came from: ' + $old)
                Write-Host ('     retired    the old VegasAIBridge.dll (' + $tag + ')') -ForegroundColor Green
            } catch {
                Write-Host ('     could not move the old bridge at ' + $old + '  -  ' + $_.Exception.Message) -ForegroundColor Yellow
            }
        }
    }
}

Write-Host ''
Write-Host '  In VEGAS: Tools - Scripting - (the script name).'
Write-Host '  Already open? Tools - Scripting - Rescan Script Menu Folder.'
Write-Host '  Becky Search panel: View - Extensions - Becky Search.'
Write-Host ''
Read-Host '  Press Enter to close'
