@echo off
REM =====================================================================
REM  Caption This Edit
REM
REM  DRAG YOUR VEGAS EDIT ONTO THIS FILE. That is the whole thing.
REM
REM  It accepts:
REM    - a Vegas EDL text export   (.txt)
REM    - a Final Cut Pro 7 XML     (.xml)
REM    - a becky reel              (.json)
REM
REM  What it does:
REM    1. Reads your cut points out of the edit.
REM    2. Transcribes the source video if it has not been done yet
REM       (one time per video, and it is the slow part).
REM    3. Builds captions timed TO THE CUT POINTS, so a caption can never
REM       blink on or off for a few frames at a cut.
REM    4. If a video with the same name sits next to the edit, it burns
REM       the captions in and writes a NEW file ending _captioned.mp4.
REM
REM  Your original video is never modified.
REM =====================================================================
setlocal

if "%~1"=="" (
  echo.
  echo   Drag a Vegas .txt or .xml edit file onto this batch file.
  echo.
  echo   In Vegas: File ^> Export ^> EDL Text        gives you the .txt
  echo             File ^> Export ^> Final Cut Pro   gives you the .xml
  echo.
  pause
  exit /b 1
)

set "EDIT=%~1"
set "BASE=%~dpn1"

REM A reel is loaded with --reel; an NLE edit is imported with --edit.
set "MODE=--edit"
if /i "%~x1"==".json" set "MODE=--reel"

REM Prefer the freshly built copy in the repo, fall back to the PATH copy.
set "TOOL=%~dp0becky-go\bin\becky-subtitle.exe"
if not exist "%TOOL%" set "TOOL=becky-subtitle.exe"

echo.
echo   Edit     : %EDIT%
echo   Captions : %BASE%.srt
echo.

if exist "%BASE%.mp4" (
  echo   Found the rendered video next to it:
  echo     %BASE%.mp4
  echo.
  echo   Burning the captions in. This re-encodes the video, so give it
  echo   a few minutes. The original is left alone.
  echo.
  "%TOOL%" %MODE% "%EDIT%" --out "%BASE%.srt" --burn "%BASE%.mp4" --burn-out "%BASE%_captioned.mp4" --verbose
) else (
  echo   No video named %~n1.mp4 sits next to the edit, so this writes
  echo   the caption file only. Drop it on your timeline, or put the
  echo   rendered video next to the edit and run this again to burn it in.
  echo.
  "%TOOL%" %MODE% "%EDIT%" --out "%BASE%.srt" --verbose
)

echo.
if errorlevel 1 (
  echo   Something went wrong. The message above says what.
) else (
  echo   Done.
)
echo.
pause
