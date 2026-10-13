@echo off
setlocal
REM ===========================================================================
REM Prep OBS Footage - Jordan's OBS prep (obs-workflow.md), one double-click.
REM
REM   1. Puts the folder's .mp4 recordings in VEGAS Pro in recording order.
REM      The first one sets the project size and frame rate.
REM   2. Deletes the mic+desktop mix track. Keeps desktop audio and mic.
REM   3. Finds your clap: the spike in the mic, and the first video frame
REM      where your hands come together. Moves the audio so the spike lands
REM      on that frame, checks it in VEGAS's own playback, then groups the
REM      audio and video again.
REM   4. Saves <folder name minus the day number>.veg in the footage folder.
REM
REM How to run: put this file in the footage folder and double-click it,
REM or drag a footage folder onto it, or double-click it and pick a folder.
REM No AI chat is needed. Your recordings are only read, never changed,
REM and an existing .veg is never overwritten.
REM ===========================================================================

set "FOLDER=%~1"
if "%FOLDER%"=="" if exist "%~dp0*.mp4" set "FOLDER=%~dp0."

if "%FOLDER%"=="" (
  echo Pick the folder that has your OBS recordings in it...
  for /f "usebackq delims=" %%F in (`powershell -NoProfile -Command "Add-Type -AssemblyName System.Windows.Forms; $d = New-Object System.Windows.Forms.FolderBrowserDialog; $d.Description = 'Pick the OBS footage folder'; $d.ShowNewFolderButton = $false; if ($d.ShowDialog() -eq 'OK') { $d.SelectedPath }"`) do set "FOLDER=%%F"
)

if "%FOLDER%"=="" (
  echo.
  echo No folder picked - nothing to do.
  echo.
  pause
  exit /b 1
)

set "EXE=%USERPROFILE%\bin\becky-obsprep.exe"
if not exist "%EXE%" set "EXE=becky-obsprep.exe"

"%EXE%" "%FOLDER%"
set "RC=%ERRORLEVEL%"

echo.
if "%RC%"=="0" (
  echo All done. The project is open in VEGAS Pro and saved in the footage folder.
) else (
  echo It stopped - the reason is in the message above. Nothing of yours was changed.
)
echo.
pause
