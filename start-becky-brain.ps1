# Start Becky's REAPER brain: a LIGHTWEIGHT proxy on port 11435 that REAPER's
# "REAPER Chat" extension connects to. No local model, no GPU, a few MB of RAM
# (the old llama-server brain loaded a 4B model onto the GPU - that was the
# resource hog). The thinking happens in the backend YOU pick below:
#   1 = Claude (your Claude Code login - already paid for by Claude Max)
#   2 = OpenCode Zen (free models only - enforced in code, never spends money)
# ASCII-only (CLAUDE.md launcher rule).
$ErrorActionPreference = "Stop"
$root = Split-Path -Parent $MyInvocation.MyCommand.Path
$exe  = Join-Path $root "becky-go\bin\becky-reaper.exe"

if (-not (Test-Path $exe)) {
  Write-Host "Building becky-reaper.exe ..."
  Push-Location (Join-Path $root "becky-go")
  & go build -o "bin\becky-reaper.exe" ".\cmd\becky-reaper"
  Pop-Location
}

# Remember Jordan's last backend choice so Enter just reuses it.
$cfgDir  = Join-Path $env:APPDATA "becky"
$cfgFile = Join-Path $cfgDir "reaper-brain-backend.txt"
$remembered = "claude"
if (Test-Path $cfgFile) {
  $saved = (Get-Content $cfgFile -ErrorAction SilentlyContinue | Select-Object -First 1)
  if ($saved -eq "zen" -or $saved -eq "claude") { $remembered = $saved }
}

Write-Host ""
Write-Host "Who should answer REAPER Chat?" -ForegroundColor Cyan
Write-Host "  1  Claude (your Claude Code login - already paid for)" -ForegroundColor Green
Write-Host "  2  OpenCode Zen (free models only)" -ForegroundColor Yellow
Write-Host ("Press 1 or 2, or just Enter for your usual: " + $remembered) -ForegroundColor White
$choice = Read-Host "Choice"
$backend = $remembered
if ($choice -eq "1") { $backend = "claude" }
if ($choice -eq "2") { $backend = "zen" }

New-Item -ItemType Directory -Force -Path $cfgDir | Out-Null
Set-Content -Path $cfgFile -Value $backend

# Zen needs Jordan's API key once; store it in his user environment so he never
# has to paste it again. (Free models only - becky refuses paid ids in code.)
if ($backend -eq "zen" -and -not $env:OPENCODE_API_KEY -and -not $env:OPENCODE_ZEN_API_KEY) {
  Write-Host ""
  Write-Host "One-time setup: paste your OpenCode Zen API key (from opencode.ai/zen)" -ForegroundColor Yellow
  $key = Read-Host "Zen API key"
  if ($key) {
    [Environment]::SetEnvironmentVariable("OPENCODE_API_KEY", $key, "User")
    $env:OPENCODE_API_KEY = $key
    Write-Host "Saved. You won't be asked again." -ForegroundColor Green
  } else {
    Write-Host "No key entered - switching to Claude for this run." -ForegroundColor Red
    $backend = "claude"
  }
}

# Serves /v1/chat/completions on :11435 until you close this window. Leave it
# open while you use REAPER Chat.
& $exe brain --start --backend $backend
