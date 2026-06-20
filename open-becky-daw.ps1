# Becky DAW launcher: becky authors a REAPER session (your Cubase-style bus tree
# at 132 BPM) and opens it in REAPER. REAPER is the DAW; becky is the AI brain.
# ASCII-only on purpose (CLAUDE.md launcher rule).
$ErrorActionPreference = "Stop"
$root = Split-Path -Parent $MyInvocation.MyCommand.Path
$exe  = Join-Path $root "becky-go\bin\becky-reaper.exe"

if (-not (Test-Path $exe)) {
  Write-Host "Building becky-reaper.exe ..."
  Push-Location (Join-Path $root "becky-go")
  & go build -o "bin\becky-reaper.exe" ".\cmd\becky-reaper"
  Pop-Location
}

$out = Join-Path $root "becky-session.rpp"
Write-Host "becky is authoring your session ..."
& $exe template --out $out

$reaper = "C:\Program Files\REAPER (x64)\reaper.exe"
if (-not (Test-Path $reaper)) { $reaper = "C:\Program Files\REAPER\reaper.exe" }
if (Test-Path $reaper) {
  Write-Host "Opening in REAPER ..."
  Start-Process $reaper -ArgumentList ('"' + $out + '"')
} else {
  Write-Host "REAPER not found. Open this file manually in your DAW:"
  Write-Host "  $out"
}
