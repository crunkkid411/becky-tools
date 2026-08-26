@echo off
setlocal
REM ===========================================================================
REM Build Rough Cut - a folder of raw takes becomes a populated VEGAS Pro 18
REM timeline with the non-speaking parts cut out and a marker for every quote.
REM
REM Double-click it and pick the folder, or drag a folder onto this file.
REM
REM Your source clips are NEVER modified. Everything it writes goes into
REM   <that folder>\_roughcut\   (speech_spans.json, vegas_cut.json,
REM                               quote_markers.json, rough_cut.veg)
REM
REM If the folder also has Parakeet transcripts in _roughcut (from
REM becky-transcribe) it does two extra things: it places the quote markers
REM where you actually talk about them, and it lets a noise-only clip be
REM removed. Without them it still cuts the silence, it is just more cautious.
REM ===========================================================================

set "ROOT=%~dp0"
set "FOLDER=%~1"

if "%FOLDER%"=="" (
  echo Pick the folder that has your raw clips in it...
  for /f "usebackq delims=" %%F in (`powershell -NoProfile -Command "Add-Type -AssemblyName System.Windows.Forms; $d = New-Object System.Windows.Forms.FolderBrowserDialog; $d.Description = 'Pick the folder of raw takes'; $d.ShowNewFolderButton = $false; if ($d.ShowDialog() -eq 'OK') { $d.SelectedPath }"`) do set "FOLDER=%%F"
)

if "%FOLDER%"=="" (
  echo.
  echo No folder picked - nothing to do.
  echo.
  pause
  exit /b 1
)

echo.
echo Folder: %FOLDER%
echo.

where python >nul 2>nul
if errorlevel 1 (
  echo.
  echo ERROR: python is not on your PATH, so the cut cannot run.
  echo.
  pause
  exit /b 1
)

python "%ROOT%scripts\roughcut.py" --folder "%FOLDER%" --launch-vegas
set "RC=%ERRORLEVEL%"

echo.
if "%RC%"=="0" (
  echo Done. The timeline was saved to:
  echo    %FOLDER%\_roughcut\rough_cut.veg
  echo Open that file in VEGAS Pro to start editing.
) else (
  echo It stopped with an error - the reason is in the messages above.
)
echo.
pause
