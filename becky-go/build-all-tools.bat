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
  REM Default: cmd\<name> ships as becky-<name>.exe. Two exceptions:
  REM  - cmd\becky is the orchestrator (becky.exe, no prefix).
  REM  - cmd\becky-<name> is ALREADY prefixed (becky-reaper, becky-vst, ...) so
  REM    ship it as-is; prepending again would emit becky-becky-<name>.exe and
  REM    leave the real becky-<name>.exe stale (silent build-skip bug, fixed).
  set OUT=becky-!NAME!.exe
  if /i "!NAME!"=="becky" set OUT=becky.exe
  if /i "!NAME:~0,6!"=="becky-" set OUT=!NAME!.exe
  REM search_library is called directly (no becky- prefix) per
  REM hj-mission-control\docs\library-contract.md - Whoretana calls it by
  REM that literal name.
  if /i "!NAME!"=="search_library" set OUT=search_library.exe
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
set "BECKY_OLDCGO=%CGO_ENABLED%"
set "CGO_ENABLED=0"
REM -H windowsgui = no console window flashes alongside the Gio window when the hub
REM is double-clicked. Without it becky-canvas is a console subsystem exe and shows a
REM black cmd box. Pure Go (Gio, no cgo) so force CGO off, matching clip/nle.
go build -tags gui -ldflags "-H windowsgui" -o bin\becky-canvas.exe .\cmd\canvas
if errorlevel 1 echo WARN: GUI canvas build failed - headless becky-canvas.exe kept.
set "CGO_ENABLED=%BECKY_OLDCGO%"

REM becky-clip ships as the real WebView2 GUI window (build tag: gui), not the
REM headless stub. Pure Go (go-webview2, no cgo), so force CGO off for this build.
echo Building becky-clip.exe ^(GUI window, -tags gui^)...
set "BECKY_OLDCGO=%CGO_ENABLED%"
set "CGO_ENABLED=0"
REM -H windowsgui = no console window pops up when the .exe is double-clicked.
go build -tags gui -ldflags "-H windowsgui" -o bin\becky-clip.exe .\cmd\clip
if errorlevel 1 echo WARN: GUI clip build failed - headless becky-clip.exe kept.
set "CGO_ENABLED=%BECKY_OLDCGO%"

REM becky-nle ships as the real Gio GUI window (build tag: gui), the AI-integrated NLE.
REM Pure Go (Gio, no cgo), so force CGO off; -H windowsgui = no console flash on launch.
REM It previews video via the sibling becky-video-preview.exe (built by build-becky-nle.ps1).
echo Building becky-nle.exe ^(GUI window, -tags gui^)...
set "BECKY_OLDCGO=%CGO_ENABLED%"
set "CGO_ENABLED=0"
go build -tags gui -ldflags "-H windowsgui" -o bin\becky-nle.exe .\cmd\becky-nle
if errorlevel 1 echo WARN: GUI nle build failed - headless becky-nle.exe kept.
set "CGO_ENABLED=%BECKY_OLDCGO%"

REM becky-drummachine ships as the real Gio GUI window (build tag: gui) - the 16-pad
REM Maschine-class drum machine with kit loading + sample browser. Pure Go (Gio, no
REM cgo), so force CGO off; -H windowsgui = no console flash. WITHOUT this it would
REM build as the headless !gui stub (the loop above) and the desktop shortcut would
REM just flash a cmd window and die. Keep this gui variant.
echo Building becky-drummachine.exe ^(GUI window, -tags gui^)...
set "BECKY_OLDCGO=%CGO_ENABLED%"
set "CGO_ENABLED=0"
go build -tags gui -ldflags "-H windowsgui" -o bin\becky-drummachine.exe .\cmd\drummachine
if errorlevel 1 echo WARN: GUI drummachine build failed - headless becky-drummachine.exe kept.
set "CGO_ENABLED=%BECKY_OLDCGO%"

set "BECKY_OLDCC=%CC%"
if exist "C:\msys64\mingw64\bin\gcc.exe" set "CC=C:\msys64\mingw64\bin\gcc.exe"
set "CGO_ENABLED=1"
echo Building becky-daw-engine.exe ^(real audio, -tags audio^)...
go build -tags audio -o bin\becky-daw-engine.exe .\cmd\daw-engine
if errorlevel 1 echo WARN: audio daw-engine build failed - stub becky-daw-engine.exe kept.
set "CC=%BECKY_OLDCC%"

REM becky-edit ships with the IN-PROCESS Gemma-4 model (llama.dll via cgo, -tags llamacgo),
REM installed to becky-go\becky-edit.exe (the path the Shotcut Becky dock spawns). The loop
REM above already built a portable warm-llama-server bin\becky-edit.exe as the fallback. This
REM is best-effort: it needs the local llama.cpp build + gendef/dlltool; if it fails the warm
REM build is kept. Launch via "Open Becky Edit.bat" (it puts the llama DLLs on PATH).
echo Building becky-edit.exe ^(in-process Gemma-4, -tags llamacgo^)...
powershell -NoProfile -ExecutionPolicy Bypass -File "%~dp0..\scripts\build-becky-edit-llama.ps1"
if errorlevel 1 echo WARN: in-process becky-edit build failed - warm-server bin\becky-edit.exe kept.

echo.
echo Done. Built !COUNT! tools ^(+ GUI/audio variants^). Binaries in %~dp0bin
dir /b bin

REM --- Install: copy EVERY built tool onto Jordan's PATH bin -----------------
REM becky-AI-Agent-review-1.md acceptance criterion 6 (F5 "deployment gaps"):
REM becky-vision/becky-ocr sat in becky-go\bin\ where no agent's fresh shell
REM could find them until someone manually copied them. Copy-only (never
REM delete/mirror) so unrelated tools already in that folder (auto-editor,
REM yt-dlp, exiftool, ...) are untouched; a locked file (a tool currently
REM running) is skipped by `copy` with a warning, never aborts the rest.
set "BECKY_PATH_BIN=C:\Users\only1\bin"
echo.
echo Installing tools to PATH bin (%BECKY_PATH_BIN%)...
if not exist "%BECKY_PATH_BIN%" mkdir "%BECKY_PATH_BIN%"
copy /Y bin\*.exe "%BECKY_PATH_BIN%\" >nul
echo Installed. Fresh-shell smoke test: becky-vision, becky-ocr, becky-perceive,
echo search_library, becky should all resolve on PATH now.
