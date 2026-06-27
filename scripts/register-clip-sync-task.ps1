# register-clip-sync-task.ps1 - install the daily iPhone-history archive task.
#
# Creates a Windows Scheduled Task that runs clip-sync.ps1 every day at 5 PM.
# If the PC is OFF at 5 PM, StartWhenAvailable makes it run as soon as possible
# after the next time the PC is on (Task Scheduler's missed-start catch-up) -
# which is exactly "run the next time I turn the computer on", and it only ever
# downloads pages it does not already have.
#
# Runs as the current user, only when logged on (no stored password needed; the
# Chrome history it reads is this user's). Window is hidden (no console flash).
# Re-running this script just updates the task (-Force). ASCII-only / PS 5.1 safe.

[CmdletBinding()]
param(
    [string]$ScriptPath = "X:\AI-2\becky-tools\scripts\clip-sync.ps1",
    [string]$Time = "17:00",
    [string]$TaskName = "Becky iPhone History Archive"
)

$ErrorActionPreference = "Stop"

if (-not (Test-Path $ScriptPath)) {
    Write-Output "ERROR: clip-sync.ps1 not found at $ScriptPath"
    exit 1
}

$psArgs = "-NoProfile -ExecutionPolicy Bypass -WindowStyle Hidden -File `"$ScriptPath`""
$action = New-ScheduledTaskAction -Execute "powershell.exe" -Argument $psArgs
$trigger = New-ScheduledTaskTrigger -Daily -At $Time
$settings = New-ScheduledTaskSettingsSet `
    -StartWhenAvailable `
    -AllowStartIfOnBatteries `
    -DontStopIfGoingOnBatteries `
    -ExecutionTimeLimit (New-TimeSpan -Hours 3) `
    -MultipleInstances IgnoreNew
$principal = New-ScheduledTaskPrincipal -UserId "$env:USERDOMAIN\$env:USERNAME" -LogonType Interactive -RunLevel Limited

Register-ScheduledTask -TaskName $TaskName -Action $action -Trigger $trigger `
    -Settings $settings -Principal $principal `
    -Description "Daily 5 PM: download new iPhone Chrome history pages to Obsidian as verified markdown (becky-radar -> becky-web2md -> becky-clipcheck). Catches up after a missed start if the PC was off." `
    -Force | Out-Null

Write-Output "Registered scheduled task: '$TaskName'"
Write-Output "  Runs daily at $Time; if the PC is off, runs at the next opportunity."
Write-Output "  Action: powershell.exe $psArgs"
Write-Output "To remove: Unregister-ScheduledTask -TaskName '$TaskName' -Confirm:`$false"
