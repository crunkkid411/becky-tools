# playlist-intake.ps1 - run by the "Becky Playlist Scout (idle)" scheduled task.
# Reads up to 3 new videos from the ai-useful playlist into Obsidian notes with
# becky-intake. Task Scheduler gates it on idle; nothing pops up. Result lines
# go to research\playlist-intake\intake.log. Replaced scout-idle.ps1 2026-09-25.
param([switch]$NoPause)
$ErrorActionPreference = 'Continue'

$Exe      = 'C:\Users\only1\bin\becky-intake.exe'
$Playlist = 'https://www.youtube.com/playlist?list=PLLnp7PR3IvheB68zHrkkKu2ih1uNdV8pT'
$Log      = 'X:\AI-2\becky-tools\research\playlist-intake\intake.log'
$stamp    = Get-Date -Format 'yyyy-MM-dd HH:mm'

if (-not (Test-Path $Exe)) {
    Add-Content $Log "$stamp FAIL: becky-intake.exe missing at $Exe"
} else {
    $out = & $Exe $Playlist --limit 3 2>&1 | ForEach-Object { "$_" }
    Add-Content $Log "$stamp exit $LASTEXITCODE"
    $out | ForEach-Object { Add-Content $Log "  $_" }
}
if (-not $NoPause) { Read-Host 'Press Enter to close' }
