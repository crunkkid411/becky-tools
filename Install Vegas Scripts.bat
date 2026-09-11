@echo off
REM Copies becky's VEGAS Pro scripts into the VEGAS scripting menu.
REM No administrator rights needed - it installs into your own Documents folder,
REM which is one of the seven places VEGAS looks for scripts.
REM ASCII ONLY - a stray Unicode char makes PowerShell 5.1 fail to parse this.

setlocal
set "PS=%~dp0install-vegas-scripts.ps1"

echo.
echo   Installing becky's VEGAS scripts...
echo.

powershell -NoProfile -ExecutionPolicy Bypass -File "%PS%"

echo.
echo   Done. In VEGAS: Tools - Scripting - Becky Cut / BeckyCaptions
echo   Already open? Tools - Scripting - Rescan Script Menu Folder
echo.
pause
