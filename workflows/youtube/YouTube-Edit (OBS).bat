@echo off
setlocal
REM ===========================================================================
REM YouTube Video Edit - OBS recording
REM
REM The same edit as YouTube-Edit.bat, for footage you recorded in OBS.
REM One double-click does both jobs:
REM   1. lines the OBS sound up with the picture (your clap), the way
REM      "Prep OBS Footage.bat" does - skipped when it was already done
REM      for this folder
REM   2. the YouTube edit, saved as <folder name>-video.veg
REM
REM Drag the video onto this file, or put this file in the footage folder and
REM double-click it. Your video is NEVER changed.
REM Not an OBS recording? Use YouTube-Edit.bat instead.
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

REM The footage folder: the dragged folder, the dragged video's folder, or here.
set "FOLDER=%~dp0."
if not "%~1"=="" (
  if exist "%~1\*" (set "FOLDER=%~f1") else (set "FOLDER=%~dp1.")
)
for %%F in ("%FOLDER%") do set "NAME=%%~nxF"

set "SYNC=%LOCALAPPDATA%\becky\obsprep\%NAME%\sync.json"
if exist "%SYNC%" (
  echo The OBS sound is already lined up for this folder - going straight to the edit.
) else (
  echo Step 1 of 2: lining the OBS sound up with the picture...
  set "EXE=%USERPROFILE%\bin\becky-obsprep.exe"
  if not exist "%USERPROFILE%\bin\becky-obsprep.exe" set "EXE=becky-obsprep.exe"
  call "%%EXE%%" "%FOLDER%"
  if errorlevel 1 (
    echo.
    echo It stopped while lining up the sound - the reason is in the message above.
    echo Nothing of yours was changed.
    echo.
    pause
    exit /b 1
  )
  echo.
  echo Step 2 of 2: the edit...
)

becky-livestream --video %*
echo.
pause
