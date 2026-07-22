@echo off
REM Start Becky's REAPER brain so REAPER Chat can control your DAW in plain
REM English. Lightweight proxy on port 11435 - answers come from Claude (your
REM Claude Code login) or OpenCode Zen free models, your choice. No local
REM model, no GPU hog. ASCII-only (CLAUDE.md rule).
powershell -ExecutionPolicy Bypass -NoProfile -File "%~dp0start-becky-brain.ps1"
pause
