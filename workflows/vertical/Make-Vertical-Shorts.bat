@echo off
setlocal
REM ===========================================================================
REM Make Vertical Shorts - Jordan's "Create Vertical Shorts from existing
REM widescreen video" workflow (vegas\JORDANS-WORKFLOWS.md section 2), built
REM on the VEGAS timeline so you can adjust anything by hand afterwards.
REM
REM This is the LAST step. It only works from the EDITED video - the project
REM the YouTube edit made (YouTube-Edit.bat). A raw project is refused.
REM
REM   1. Opens the edited project and saves a copy as <name>-VERTICAL.veg
REM      (1080 x 1920). Your project is never saved over.
REM   2. Picks the best 3 shorts (Gemma-4 12B reads the whole video), or uses
REM      your own list if a shorts.txt sits next to the project:
REM          6:04 6:58 He threatened my kid
REM      (start, end, title - one short per line)
REM   3. Frames every clip on your face (two people side by side get stacked,
REM      a webcam over a screen gets the screen on top), cuts to a new framing
REM      when you move, adds captions (no titles on the video), and
REM      punch-ins / face zooms Gemma-4 picks.
REM   4. Puts a region named with the title around each short and renders
REM      them with your Voukoder "TikTok NVENC" template into the month's
REM      "rendered" folder.
REM
REM How to run: put this file in the footage folder and double-click it, or
REM drag the edited .veg (or its folder) onto it.
REM ===========================================================================

set "VEG=%~1"
if "%VEG%"=="" set "VEG=%~dp0."

set "EXE=%USERPROFILE%\bin\becky-vertical.exe"
if not exist "%EXE%" set "EXE=becky-vertical.exe"

set "LIST="
if exist "%VEG%\" (
  if exist "%VEG%\shorts.txt" set "LIST=%VEG%\shorts.txt"
) else (
  for %%D in ("%VEG%") do if exist "%%~dpDshorts.txt" set "LIST=%%~dpDshorts.txt"
)

if defined LIST (
  "%EXE%" "%VEG%" --shorts "%LIST%"
) else (
  "%EXE%" "%VEG%"
)
set "RC=%ERRORLEVEL%"

echo.
if "%RC%"=="0" (
  echo All done. The vertical project is open in VEGAS Pro, and the shorts are in the "rendered" folder.
) else (
  echo It stopped - the reason is in the message above. Your project was not changed.
)
echo.
pause
