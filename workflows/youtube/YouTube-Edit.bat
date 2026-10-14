@echo off
setlocal
REM ===========================================================================
REM YouTube Video Edit - System One
REM
REM Edits the raw recording of a YouTube video (not a livestream) into the
REM video you post, and leaves a saved VEGAS Pro project next to it:
REM   <folder name>-systemone.veg
REM
REM The same workflow as the livestream edits, minus the live chat:
REM System One, Gemma-4 and Qwen3.5 vote on every sentence, the models read
REM the whole edit, dead air and breaths come out, and the edit is checked.
REM What you tell your editor on camera ("I forgot to mention...", "that goes
REM earlier", "cut that") is heard by Gemma-4 12B, Gemma-4 and Qwen3.5 and
REM followed; your words to the editor are cut, and a region says what was done.
REM
REM OBS recordings: run "Prep OBS Footage.bat" on the folder first (it lines
REM the sound up with the picture); this edit reuses that sync.
REM
REM Drag the video onto this file, or put this file in the footage folder and
REM double-click it. Your video is NEVER changed. Everything it writes goes
REM into <footage folder>\becky-edit\  plus the new .veg project.
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

becky-livestream --video --model systemone %*
echo.
pause
