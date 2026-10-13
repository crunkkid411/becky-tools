@echo off
setlocal
REM ===========================================================================
REM Prep OBS Footage (no desktop audio) - the same as "Prep OBS Footage.bat",
REM but it ALSO deletes the desktop audio track, so only the picture and your
REM mic are left. Use it when you do not want the desktop sound.
REM
REM How to run: put this file in the footage folder and double-click it,
REM or drag a footage folder onto it, or double-click it and pick a folder.
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

"%EXE%" --no-desktop "%FOLDER%"
set "RC=%ERRORLEVEL%"

echo.
if "%RC%"=="0" (
  echo All done. The project is open in VEGAS Pro and saved in the footage folder.
) else (
  echo It stopped - the reason is in the message above. Nothing of yours was changed.
)
echo.
pause
