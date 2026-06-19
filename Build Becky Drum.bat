@echo off
REM ============================================================================
REM  Build Becky Drum.bat  -  double-click this ONCE to build your drum machine.
REM
REM  It builds the window + the sound engine, drops a "Becky Drum Machine" icon
REM  on your Desktop, and opens it. After the first time, just use the Desktop
REM  icon. You never have to type anything. This window stays open so you can
REM  read what happened.
REM ============================================================================
setlocal
cd /d "%~dp0"

powershell -NoProfile -ExecutionPolicy Bypass -File "%~dp0build-becky-drum.ps1"
set RC=%ERRORLEVEL%

echo.
if not "%RC%"=="0" (
  echo Build reported a problem ^(exit %RC%^). Read the red text above, or send it
  echo to your assistant. Nothing on your computer was changed.
)
echo.
pause
