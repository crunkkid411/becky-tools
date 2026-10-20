@echo off
setlocal
rem Redo one step of a video edit without redoing the rest.
rem Drag the video onto this file (or double-click and paste its path).
rem Every step saves its answers in <footage folder>\becky-edit; this throws
rem away the chosen step's answers so only that step works again.
rem Also use it to finish a run that crashed: pick "nothing" - it carries on.

where becky-livestream >nul 2>nul
if errorlevel 1 (
  echo becky-livestream is not installed - run build-all-tools.bat in becky-tools.
  pause
  exit /b 1
)

set "VIDEO=%~1"
if "%VIDEO%"=="" (
  echo Drag the video file into this window and press Enter:
  set /p "VIDEO="
)
set "VIDEO=%VIDEO:"=%"
if not exist "%VIDEO%" (
  echo Could not find that video: %VIDEO%
  pause
  exit /b 1
)

echo.
echo Which version made this edit?
echo   1 = YouTube version (newest)
echo   2 = Qwen version (the one you tested first)
set /p "VER=Type 1 or 2 and press Enter: "
set "MODE=--video"
if "%VER%"=="2" set "MODE=--model qwen"

echo.
echo Which step do you want to redo?
echo   0  = nothing - just carry on a run that stopped or crashed
echo   3  = 03_Transcript (slow: listens to the whole video again)
echo   4  = 04_Silence-Map
echo   5  = 05_Analysis
echo   6  = 06_Content-Decision (what stays and what goes)
echo   9  = 09_Visual-Moments
echo   10 = 10_Breath-Check
echo   11 = 11_Publish-Check
echo Several at once: type them with commas, like 6,10
echo (Edited a PROMPT file? You do not need this - that step redoes itself.)
set /p "STEP=Type the number and press Enter: "

echo.
if "%STEP%"=="0" (
  becky-livestream %MODE% "%VIDEO%"
) else (
  becky-livestream %MODE% --redo %STEP% "%VIDEO%"
)
echo.
pause
