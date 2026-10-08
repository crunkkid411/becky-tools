@echo off
setlocal
REM ===========================================================================
REM Livestream Edit - System One Stack
REM
REM The Qwen livestream edit (the most accurate of Gemma / Qwen / Claude),
REM with a System One decision model added, everything else the same.
REM Leaves a saved VEGAS Pro project next to the video:
REM   <folder name>-systemone-stack.veg
REM
REM Qwen decides every sentence. System One decides every sentence too.
REM Gemma re-checks Qwen's doubtful calls AND every line where System One
REM disagreed with Qwen. Where Qwen and Gemma still disagree, a sure System One
REM call settles it (two of three agree). System One is capped at $5 a month
REM in code; one livestream costs a few cents.
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

becky-livestream --model systemone-stack %*
echo.
pause
