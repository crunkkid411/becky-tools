@echo off
setlocal
REM ===========================================================================
REM Make Vertical Shorts - Jordan's "Create Vertical Shorts from existing
REM widescreen video" workflow (vegas\JORDANS-WORKFLOWS.md section 2), built
REM on the VEGAS timeline so you can adjust anything by hand afterwards.
REM
REM   1. Opens your widescreen project and saves a copy as <name>-VERTICAL.veg
REM      (1080 x 1920). Your project is never saved over.
REM   2. Picks the best 3 shorts (Gemma-4 12B reads the whole video), or uses
REM      your own list if a shorts.txt sits next to the project:
REM          6:04 6:58 He threatened my kid
REM      (start, end, title - one short per line)
REM   3. Frames every clip on your face (two people side by side get stacked,
REM      a webcam over a screen gets the screen on top), cuts to a new framing
REM      when you move, adds captions, a hook title away from your face, and
REM      punch-ins / face zooms Gemma-4 picks.
REM   4. Puts a region named with the title around each short and renders
REM      them with your Voukoder "TikTok NVENC" template into the month's
REM      "rendered" folder.
REM
REM How to run: drag the widescreen .veg onto this file, or put this file in
REM the footage folder and double-click it, or double-click it and pick one.
REM ===========================================================================

set "VEG=%~1"
if "%VEG%"=="" for %%F in ("%~dp0*.veg") do (
  echo %%~nF | findstr /i /c:"-VERTICAL" >nul || set "VEG=%%~fF"
)

if "%VEG%"=="" (
  echo Pick the widescreen VEGAS project...
  for /f "usebackq delims=" %%F in (`powershell -NoProfile -Command "Add-Type -AssemblyName System.Windows.Forms; $d = New-Object System.Windows.Forms.OpenFileDialog; $d.Filter = 'VEGAS project (*.veg)|*.veg'; $d.Title = 'Pick the widescreen VEGAS project'; if ($d.ShowDialog() -eq 'OK') { $d.FileName }"`) do set "VEG=%%F"
)

if "%VEG%"=="" (
  echo.
  echo No project picked - nothing to do.
  echo.
  pause
  exit /b 1
)

set "EXE=%USERPROFILE%\bin\becky-vertical.exe"
if not exist "%EXE%" set "EXE=becky-vertical.exe"

set "LIST="
for %%D in ("%VEG%") do if exist "%%~dpDshorts.txt" set "LIST=%%~dpDshorts.txt"

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
