@echo off
REM Build every becky-go tool into bin\. Add new tools to the TOOLS list.
setlocal enabledelayedexpansion
cd /d %~dp0
if not exist bin mkdir bin

set TOOLS=transcribe cut vad diarize events osint identify embed search consolidate review export web2md deslop debt-scan validate pipeline eval enroll framematch ocr cluster motion ask new-tool

for %%T in (%TOOLS%) do (
  echo Building becky-%%T...
  go build -o bin\becky-%%T.exe .\cmd\%%T
  if errorlevel 1 (
    echo FAILED: becky-%%T
    exit /b 1
  )
)

REM The top-level orchestrator ships as becky.exe (no becky- prefix).
echo Building becky (orchestrator)...
go build -o bin\becky.exe .\cmd\becky
if errorlevel 1 (
  echo FAILED: becky
  exit /b 1
)

echo.
echo Done. Binaries in %~dp0bin
dir /b bin
