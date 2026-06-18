@echo off
REM ============================================================================
REM  Build Becky Drum.bat  —  double-click this ONCE to build your drum machine.
REM
REM  It builds the window + the sound engine, drops a "Becky Drum Machine" icon
REM  on your Desktop, and opens it. After the first time, just use the Desktop
REM  icon. You never have to type anything.
REM ============================================================================
setlocal
cd /d "%~dp0"
powershell -NoProfile -ExecutionPolicy Bypass -File "%~dp0build-becky-drum.ps1"
