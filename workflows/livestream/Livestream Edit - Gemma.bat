@echo off
setlocal
REM ===========================================================================
REM Livestream Edit - Gemma
REM
REM Cuts a livestream recording down to the topics you want and leaves a
REM saved VEGAS Pro project next to the video:  <folder name>-gemma4.veg
REM
REM Gemma-4 decides what stays. Qwen3.5 double-checks Gemma's calls where a
REM topic starts or ends, the lines cut close to kept ones, and every call
REM Gemma was unsure about. Both run on this PC - nothing is paid for.
REM
REM Drag the livestream video onto this file, or put this file in the footage
REM folder and double-click it (with several videos it asks which one).
REM What to keep: guidance.txt next to the video, in plain words. Without it,
REM it asks you.
REM
REM Your video is NEVER changed. Everything it writes goes into
REM   <footage folder>\becky-edit\   plus the new .veg project.
REM ===========================================================================

cd /d "%~dp0"
where becky-livestream >nul 2>nul
if errorlevel 1 (
  echo.
  echo becky-livestream is not installed - run build-all-tools.bat in becky-tools.
  echo.
  pause
  exit /b 1
)

becky-livestream --model gemma %*
echo.
pause
