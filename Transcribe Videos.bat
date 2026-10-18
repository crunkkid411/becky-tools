@echo off
REM Drag video files or folders onto this file (or double-click and pick a folder).
REM Each video gets a transcript Becky Review can open. ASCII-only.
powershell -NoProfile -ExecutionPolicy Bypass -File "%~dp0Transcribe Videos.ps1" %*
pause
