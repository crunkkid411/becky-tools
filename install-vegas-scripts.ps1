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
Write-Host ''
Write-Host '  In VEGAS: Tools - Scripting - (the script name).'
Write-Host '  Already open? Tools - Scripting - Rescan Script Menu Folder.'
Write-Host ''
Read-Host '  Press Enter to close'
