@echo off
setlocal
REM Open Becky Review 3 - the FULL NATIVE single-window reviewer (no WPF, no browser).
REM Third build. Native D3D11 + Dear ImGui + shared becky engine. ASCII-only (PS 5.1 safe).

set "ROOT=%~dp0"
set "PROJ=%ROOT%native\becky-review"
set "EXE=%PROJ%\becky-review.exe"
set "GSTBIN=C:\Program Files\gstreamer\1.0\msvc_x86_64\bin"

REM GStreamer runtime DLLs + the shared engine must be findable on PATH.
if exist "%GSTBIN%" set "PATH=%GSTBIN%;%PATH%"
set "PATH=%ROOT%becky-go\bin;%PATH%"

REM Shared engine (headless becky-clip bridge) - build once if missing.
if not exist "%ROOT%becky-go\bin\becky-review-engine.exe" (
  where go >nul 2>nul
  if not errorlevel 1 (
    echo First run: building the review engine...
    pushd "%ROOT%becky-go"
    go build -o bin\becky-review-engine.exe .\cmd\clip
    popd
  )
)

REM Build the native app if it is not there yet.
if not exist "%EXE%" (
  echo First run: building Becky Review 3 native app. Please wait...
  call "%PROJ%\_build.bat"
)
if not exist "%EXE%" goto BUILDFAIL

start "" /D "%PROJ%" "%EXE%"
exit /b 0

:BUILDFAIL
echo.
echo Build failed - the native app did not compile. See the messages above.
pause
exit /b 1
