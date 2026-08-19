@echo off
REM Make Shorts - drop a folder of videos on this file, or just double-click it.
REM
REM Runs the whole chain: find the good moments -> build a reel -> render vertical
REM shorts that stay framed on you. Shorts land in a "shorts" folder beside the
REM footage. ASCII only and ends with pause, per the launcher rules.

setlocal
set BECKY=%~dp0becky-go\bin
set FOLDER=%~1

if "%FOLDER%"=="" (
  echo.
  echo   Drag a folder of videos onto this file, or type the folder path below.
  echo.
  set /p FOLDER="Folder: "
)

if "%FOLDER%"=="" goto :nofolder
if not exist "%FOLDER%\" goto :nofolder

echo.
echo ==============================================================
echo   Making shorts from: %FOLDER%
echo ==============================================================
echo.
echo [1/3] Finding the best moments (this reads the transcripts)...
"%BECKY%\becky-moment.exe" --folder "%FOLDER%" --top 10 --out "%TEMP%\becky_moments.json"
if errorlevel 1 goto :failed

echo.
echo [2/3] Matching them to the footage...
"%BECKY%\becky-hits.exe" --hits "%TEMP%\becky_moments.json" --folder "%FOLDER%" --out "%TEMP%\becky_moments.reel.json"
if errorlevel 1 goto :failed

echo.
echo [3/3] Rendering vertical shorts (framed on you, not centre-cropped)...
"%BECKY%\becky-short.exe" --reel "%TEMP%\becky_moments.reel.json" --outdir "%FOLDER%\shorts"
if errorlevel 1 goto :failed

echo.
echo ==============================================================
echo   Done. Your shorts are in:  %FOLDER%\shorts
echo ==============================================================
start "" "%FOLDER%\shorts"
goto :end

:nofolder
echo.
echo   No folder given - nothing to do.
goto :end

:failed
echo.
echo   Something did not finish. The message above says which step and why.
echo   Nothing was overwritten.

:end
echo.
pause
