# scout-watch.ps1 — keep an eye on Jordan's "ai useful" YouTube playlist.
#
# Runs becky-scout on the playlist and shows ONLY the videos you've added since
# the last run (it remembers what it already looked at in a small state file), so
# you get a short "here's what's new and whether becky cares" digest instead of
# re-reading all 100 every time.
#
# Two ways to use it (Jordan never types anything either way):
#   1. Double-click the Desktop shortcut "Watch Becky Playlist" to run it now.
#   2. Run once with -Register to have Windows run it automatically on a schedule
#      (default: every Monday 9am). After that it just happens; new finds wait in
#      the report file for you.
#
# Needs yt-dlp installed (one-time: `pip install yt-dlp`). becky-scout itself is
# built by build-all-tools.bat into becky-go\bin.

param(
    [string]$Playlist = 'https://youtube.com/playlist?list=PLLnp7PR3IvheB68zHrkkKu2ih1uNdV8pT',
    [switch]$Deep = $true,        # pull descriptions/tags so findings corroborate (slower)
    [string]$Cookies = 'chrome',  # read cookies from this browser so YouTube doesn't block deep fetches ('' to disable)
    [switch]$Register,            # set up the weekly scheduled task and exit
    [switch]$All,                 # assess the whole playlist, not just new entries
    [switch]$NoPause
)

$ErrorActionPreference = 'Stop'
if (Get-Variable PSNativeCommandUseErrorActionPreference -Scope Global -ErrorAction SilentlyContinue) {
    $PSNativeCommandUseErrorActionPreference = $false
}

$Repo  = if ($env:BECKY_REPO) { $env:BECKY_REPO } else { 'X:\AI-2\becky-tools' }
$Exe   = Join-Path $Repo 'becky-go\bin\becky-scout.exe'
$State = Join-Path $Repo 'scout-state.json'
$Report = Join-Path $Repo 'scout-latest.txt'

function Say  ($m) { Write-Host $m -ForegroundColor Gray }
function Good ($m) { Write-Host $m -ForegroundColor Green }
function Warn ($m) { Write-Host $m -ForegroundColor Yellow }
function Finish ($code) { Write-Host ''; if (-not $NoPause) { Read-Host 'Press Enter to close this window' }; exit $code }

# ----- one-time: register the weekly scheduled task -----------------------------
if ($Register) {
    Say 'Setting up the weekly playlist check...'
    $ps = (Get-Command powershell.exe).Source
    $arg = "-NoProfile -ExecutionPolicy Bypass -File `"$PSCommandPath`" -NoPause"
    $action  = New-ScheduledTaskAction -Execute $ps -Argument $arg
    $trigger = New-ScheduledTaskTrigger -Weekly -DaysOfWeek Monday -At 9am
    $set     = New-ScheduledTaskSettingsSet -StartWhenAvailable -RunOnlyIfNetworkAvailable
    Register-ScheduledTask -TaskName 'Becky Playlist Watch' -Action $action -Trigger $trigger `
        -Settings $set -Description 'Assess new videos in the ai-useful YouTube playlist for becky' -Force | Out-Null
    Good 'Done. Windows will check the playlist every Monday 9am.'
    Say  "New findings will be saved to: $Report"
    Finish 0
}

# ----- normal run: assess (new entries only, unless -All) -----------------------
if (-not (Test-Path $Exe)) {
    Warn "Couldn't find becky-scout.exe at $Exe"
    Say  'Build the tools first: run becky-go\build-all-tools.bat (or the Get Becky Updates button).'
    Finish 1
}

$scoutArgs = @($Playlist)
if ($Deep) { $scoutArgs += '--deep' }
if (-not $All) { $scoutArgs += @('--new-only', '--state', $State) }

# Deep fetches hit YouTube's "are you a bot?" check unless requests look like a
# real signed-in browser. Reading cookies from Chrome fixes that on a home PC.
# becky-scout forwards BECKY_YTDLP_ARGS straight to yt-dlp.
if ($Deep -and $Cookies) { $env:BECKY_YTDLP_ARGS = "--cookies-from-browser $Cookies" }

Say "Checking the playlist for new videos becky should know about..."
Say "(this can take a few minutes with --deep; it reads each new video's description)"
& $Exe @scoutArgs | Tee-Object -FilePath $Report
$code = $LASTEXITCODE

Write-Host ''
Good "Saved this report to: $Report"
if (-not $All) { Say "Only NEW videos since last time were shown. Run with -All to re-check everything." }
Say "Want becky to act on something above? Open your assistant and say which one."
Finish $code
