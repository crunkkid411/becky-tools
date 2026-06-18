@echo off
REM ============================================================================
REM  Build Becky Clip.bat  -  double-click this ONCE to build your video editor.
REM
REM  It builds the becky-clip window, drops a "Becky Clip" icon on your Desktop,
REM  and opens it. After the first time, just use the Desktop icon. You never
REM  have to type anything. This window stays open so you can read what happened.
REM ============================================================================
setlocal
cd /d "%~dp0"

powershell -NoProfile -ExecutionPolicy Bypass -File "%~dp0build-becky-clip.ps1"
set RC=%ERRORLEVEL%

echo.
if not "%RC%"=="0" (
  echo Build reported a problem ^(exit %RC%^). Read the red text above, or send it
  echo to your assistant. Nothing on your computer was changed.
)
echo.
pause
