# Open Becky Canvas - the central hub window (Gio, native, no browser). It has the
# launch buttons that open the real tools: Drum Machine / REAPER DAW / Clip / NLE /
# Ask. This script builds becky-canvas.exe if it is missing, then opens it.
# becky standard GUI = Go + Gio (GUI-RULES.md). ASCII-only (CLAUDE.md launcher rule).
$ErrorActionPreference = "Stop"
$root = Split-Path -Parent $MyInvocation.MyCommand.Path
$exe  = Join-Path $root "becky-go\bin\becky-canvas.exe"

if (-not (Test-Path $exe)) {
  Write-Host "Building becky-canvas.exe (Gio GUI window) ..."
  Push-Location (Join-Path $root "becky-go")
  $env:CGO_ENABLED = "0"
  & go build -tags gui -ldflags "-H windowsgui" -o "bin\becky-canvas.exe" ".\cmd\canvas"
  Pop-Location
}

if (-not (Test-Path $exe)) {
  Write-Host "Could not build becky-canvas.exe. Run build-all-tools.bat in becky-go and check the output."
  exit 1
}

# becky-canvas.exe is a windowsgui binary (no console), so starting it just opens
# the hub window. The launch buttons inside open the other tools as their own windows.
Start-Process -FilePath $exe
Write-Host "Becky Canvas is opening. Use the buttons on the left to open your tools."
