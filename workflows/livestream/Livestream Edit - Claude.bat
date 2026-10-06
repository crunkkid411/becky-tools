@echo off
setlocal
REM ===========================================================================
REM Livestream Edit - Claude
REM
REM Cuts a livestream recording down to the topics you want and leaves a
REM saved VEGAS Pro project next to the video:  <folder name>-claude.veg
REM
REM Claude decides what stays, through your Claude subscription (the same
REM login Claude Code uses). Never a paid API.
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

becky-livestream --model claude %*
echo.
pause
