@echo off
setlocal
REM Open Forensic Hits - turn a forensic agent's hit-list into a Becky Review timeline.
REM
REM Usage:  "Open Forensic Hits.bat" [hits.json] [caseFolder]
REM   hits.json  = the agent's findings, a JSON list of {srt, t, q}
REM                (default: E:\TakingBack2007\_forensic_hits.json)
REM   caseFolder = the evidence folder (default: E:\TakingBack2007)
REM
REM It builds a review reel (mapping each .srt + timestamp to the right video clip),
REM then opens Becky Review with those clips already on the timeline.

set "ROOT=%~dp0"
set "HITS=%~1"
if "%HITS%"=="" set "HITS=E:\TakingBack2007\_forensic_hits.json"
set "CASE=%~2"
if "%CASE%"=="" set "CASE=E:\TakingBack2007"
set "REEL=%CASE%\becky-hits.reel.json"
set "HITTOOL=%ROOT%becky-go\bin\becky-hits.exe"

if not exist "%HITS%" (
  echo Could not find the hit-list file:
  echo    %HITS%
  echo Have the forensic agent write its findings there first.
  pause
  exit /b 1
)

if not exist "%HITTOOL%" (
  where go >nul 2>nul
  if errorlevel 1 (
    echo becky-hits.exe is missing and Go is not installed to build it.
    pause
    exit /b 1
  )
  echo First run: building becky-hits...
  pushd "%ROOT%becky-go"
  go build -o bin\becky-hits.exe .\cmd\becky-hits
  popd
)

echo Building the review reel from:
echo    %HITS%
"%HITTOOL%" --hits "%HITS%" --folder "%CASE%" --out "%REEL%"
if errorlevel 1 (
  echo becky-hits failed - see the messages above.
  pause
  exit /b 1
)

REM Hand the folder + reel to Becky Review. Its engine pre-loads BECKY_REVIEW_REEL
REM on startup and the window auto-opens BECKY_REVIEW_FOLDER, so the clips are ready.
set "BECKY_REVIEW_FOLDER=%CASE%"
set "BECKY_REVIEW_REEL=%REEL%"
call "%ROOT%Open Becky Review.bat"
exit /b 0
