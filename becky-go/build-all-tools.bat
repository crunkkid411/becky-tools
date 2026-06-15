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

echo Done. Built !COUNT! tools. Binaries in %~dp0bin
dir /b bin
