@echo off
REM Becky DAW: becky builds a REAPER session and opens it. ASCII-only (CLAUDE.md rule).
powershell -ExecutionPolicy Bypass -NoProfile -File "%~dp0open-becky-daw.ps1"
pause
