# Start Becky's REAPER brain: boots llama.cpp's llama-server on port 11435 so
# REAPER's "REAPER Chat" extension can control your DAW in plain English.
# This is the fix for the "Failed to connect to http://localhost:11435" error.
# becky standard = llama.cpp (NOT Ollama). ASCII-only (CLAUDE.md launcher rule).
$ErrorActionPreference = "Stop"
$root = Split-Path -Parent $MyInvocation.MyCommand.Path
$exe  = Join-Path $root "becky-go\bin\becky-reaper.exe"

if (-not (Test-Path $exe)) {
  Write-Host "Building becky-reaper.exe ..."
  Push-Location (Join-Path $root "becky-go")
  & go build -o "bin\becky-reaper.exe" ".\cmd\becky-reaper"
  Pop-Location
}

# becky-reaper brain --start finds a chat GGUF + llama-server, binds :11435, and
# serves /v1/chat/completions until you close this window. Leave it open while
# you use REAPER Chat. Set BECKY_LLAMA_SERVER / BECKY_REAPER_MODEL to override.
& $exe brain --start
