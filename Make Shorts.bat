@echo off
REM Make Shorts - drag a VIDEO or a FOLDER onto this file, or just double-click it.
REM
REM Runs the whole chain: find the good moments -> match them to the footage ->
REM render vertical shorts that stay framed on you. Shorts land in a "shorts"
REM folder beside the footage.
REM
REM ASCII only and ends with pause, per the launcher rules. Delayed expansion is
REM on because a plain %VAR% inside an if-block is expanded when the block is
REM PARSED, not when it runs - so a value set inside the block reads back empty.

setlocal enabledelayedexpansion
set "BECKY=%~dp0becky-go\bin"
set "TARGET=%~1"

if "!TARGET!"=="" (
  echo.
  echo   Drag a video file or a folder onto this file,
  echo   or type a path below and press Enter.
  echo.
  set /p "TARGET=Video or folder: "
)

REM Strip any quotes the user typed or that came in from the drop.
set "TARGET=!TARGET:"=!"

if "!TARGET!"=="" goto :nothing

REM A folder and a single file take different flags, so work out which this is.
set "MODE="
if exist "!TARGET!\" (
  set "MODE=folder"
  set "FOLDER=!TARGET!"
) else (
  if exist "!TARGET!" (
    set "MODE=file"
    for %%F in ("!TARGET!") do set "FOLDER=%%~dpF"
    REM %%~dpF keeps a trailing backslash; drop it so paths join cleanly.
    if "!FOLDER:~-1!"=="\" set "FOLDER=!FOLDER:~0,-1!"
  )
)
if "!MODE!"=="" goto :notfound

echo.
echo ==============================================================
if "!MODE!"=="file" (
  echo   Making shorts from this clip:
  echo   !TARGET!
) else (
  echo   Making shorts from this folder:
  echo   !FOLDER!
)
echo ==============================================================
echo.
echo [1/3] Finding the best moments ^(reads the transcript, then asks the
echo       local model which bits are actually worth posting^)...
if "!MODE!"=="file" (
  "!BECKY!\becky-moment.exe" --transcript "!TARGET!" --top 10 --out "%TEMP%\becky_moments.json"
) else (
  "!BECKY!\becky-moment.exe" --folder "!FOLDER!" --top 10 --out "%TEMP%\becky_moments.json"
)
if errorlevel 1 goto :failed

echo.
echo [2/3] Matching them to the footage...
"!BECKY!\becky-hits.exe" --hits "%TEMP%\becky_moments.json" --folder "!FOLDER!" --out "%TEMP%\becky_moments.reel.json"
if errorlevel 1 goto :failed

echo.
echo [3/3] Rendering vertical shorts ^(framed on you, captions burned in, and
echo       your own cuts kept where the footage already has them^)...
"!BECKY!\becky-short.exe" --reel "%TEMP%\becky_moments.reel.json" --outdir "!FOLDER!\shorts"
if errorlevel 1 goto :failed

echo.
echo ==============================================================
echo   Done. Your shorts are in:  !FOLDER!\shorts
echo ==============================================================
start "" "!FOLDER!\shorts"
goto :end

:nothing
echo.
echo   Nothing given, so there is nothing to do.
goto :end

:notfound
echo.
echo   Could not find this on disk:
echo     !TARGET!
echo.
echo   Drag a video file or a folder of videos onto this .bat and it will work.
goto :end

:failed
echo.
echo   That step did not finish - the message above says which one and why.
echo   Nothing was overwritten.
echo.
echo   becky transcribes anything that has no transcript yet, so a missing .srt
echo   is NOT the problem - you never have to make one first.
echo.
echo   What usually goes wrong instead:
echo     - the clip has no talking head in it, so it refused rather than
echo       render something framed on the wrong thing
echo     - the file is still being written, or is on a drive that went away

:end
echo.
pause
