@echo off
REM Open Becky Edit - launches the forked Shotcut (the becky-edit forensic NLE)
REM with the Becky dock. Sets up the MSYS2/MinGW64 runtime so it finds its Qt6 +
REM MLT DLLs without a terminal. The dock auto-spawns the becky-edit Go bridge.
REM ASCII-only (PowerShell 5.1 / cmd safe).
setlocal
set "PATH=C:\msys64\mingw64\bin;%PATH%"
set "MLT_REPOSITORY=C:\msys64\mingw64\lib\mlt"
set "MLT_DATA=C:\msys64\mingw64\share\mlt"
cd /d "X:\AI-2\becky-shotcut\build\src"
if not exist "shotcut.exe" (
  echo ERROR: shotcut.exe not found at X:\AI-2\becky-shotcut\build\src
  echo Build it first - see HANDOFF-SHOTCUT-FORK.md for the recipe.
  pause
  exit /b 1
)
start "" "shotcut.exe"
