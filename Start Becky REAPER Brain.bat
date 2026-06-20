@echo off
REM Start Becky's REAPER brain so REAPER Chat can control your DAW in plain
REM English. Boots llama.cpp llama-server on port 11435. ASCII-only (CLAUDE.md rule).
powershell -ExecutionPolicy Bypass -NoProfile -File "%~dp0start-becky-brain.ps1"
pause
