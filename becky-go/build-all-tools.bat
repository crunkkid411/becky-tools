@echo off
REM Build EVERY becky-go tool into bin\ as a real .exe.
REM
REM Auto-discovers every cmd\* directory, so NEW tools are picked up with zero
REM edits here (the old hardcoded TOOLS list kept going stale). This is the
REM "build to completion" step: after building or modifying ANY tool, run this
REM so the actual .exe binaries Jordan runs are produced. cmd\becky ships as
REM becky.exe (the orchestrator, no prefix); every other cmd\<name> ships as
REM becky-<name>.exe.
setlocal enabledelayedexpansion
cd /d %~dp0
if not exist bin mkdir bin

set FAILED=
set COUNT=0
for /d %%D in (cmd\*) do (
  set NAME=%%~nxD
  if /i "!NAME!"=="becky" (
    set OUT=becky.exe
  ) else (
    set OUT=becky-!NAME!.exe
  )
  echo Building !OUT! ^(cmd\!NAME!^)...
  go build -o bin\!OUT! .\cmd\!NAME!
  if errorlevel 1 (
    echo FAILED: !OUT!
    set FAILED=1
  ) else (
    set /a COUNT+=1
  )
)

echo.
if defined FAILED (
  echo One or more tools FAILED to build. See FAILED lines above.
  exit /b 1
)

REM --- Special variants: the GUI window + the real-time audio backend ---
REM becky-canvas ships as the real Gio GUI window (build tag: gui), not the headless
REM scene-dumper. becky-daw-engine ships with the real miniaudio backend (build tag:
REM audio, needs a C compiler). Both are best-effort: if a variant fails to build, the
REM plain build from the loop above is kept and a WARN is printed (never blocks).
echo Building becky-canvas.exe ^(GUI window, -tags gui^)...
go build -tags gui -o bin\becky-canvas.exe .\cmd\canvas
if errorlevel 1 echo WARN: GUI canvas build failed - headless becky-canvas.exe kept.

set "BECKY_OLDCC=%CC%"
if exist "C:\msys64\mingw64\bin\gcc.exe" set "CC=C:\msys64\mingw64\bin\gcc.exe"
set "CGO_ENABLED=1"
echo Building becky-daw-engine.exe ^(real audio, -tags audio^)...
go build -tags audio -o bin\becky-daw-engine.exe .\cmd\daw-engine
if errorlevel 1 echo WARN: audio daw-engine build failed - stub becky-daw-engine.exe kept.
set "CC=%BECKY_OLDCC%"

echo.
echo Done. Built !COUNT! tools ^(+ GUI/audio variants^). Binaries in %~dp0bin
dir /b bin
