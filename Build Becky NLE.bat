@echo off
REM ============================================================================
REM  Build Becky NLE.bat  -  double-click this ONCE to build your video editor.
REM
REM  It builds the editor window + the GPU video preview, drops a "Becky NLE"
REM  icon on your Desktop, and opens it. After the first time, just use the
REM  Desktop icon. You never have to type anything. This window stays open so you
REM  can read what happened.
REM ============================================================================
setlocal
cd /d "%~dp0"

powershell -NoProfile -ExecutionPolicy Bypass -File "%~dp0build-becky-nle.ps1"
set RC=%ERRORLEVEL%

echo.
if not "%RC%"=="0" (
  echo Build reported a problem ^(exit %RC%^). Read the red text above, or send it
  echo to your assistant. Nothing on your computer was changed.
)
echo.
pause
